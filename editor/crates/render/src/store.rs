//! The content-addressed store under `.retake/cache/`:
//!
//! - `blobs/<aa>/<hash>`: immutable bytes named by their BLAKE3 hash (segments, layer frames, audio).
//! - `index/<kind>/<key>.json`: what a cache key produced (which blobs, boxes, frame counts).
//! - `assets.json`: asset content hashes, keyed by path, size and modification time, so big media is hashed once.

use anyhow::{Context, Result};
use serde_json::Value;
use std::collections::BTreeMap;
use std::io::Read;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicU64, Ordering};

pub struct Store {
    root: PathBuf,
    asset_hashes: Mutex<BTreeMap<String, Value>>,
}

pub fn hex_of(bytes: &[u8]) -> String {
    blake3::hash(bytes).to_hex().to_string()
}

impl Store {
    pub fn open(root: &Path) -> Result<Store> {
        // Absolute, because ffmpeg resolves concat list entries relative to the list file.
        let root = &std::path::absolute(root).with_context(|| format!("cache path {}", root.display()))?;
        for d in ["blobs", "index", "tmp"] {
            std::fs::create_dir_all(root.join(d)).with_context(|| format!("creating {}", root.join(d).display()))?;
        }
        let asset_hashes = std::fs::read_to_string(root.join("assets.json"))
            .ok()
            .and_then(|s| serde_json::from_str(&s).ok())
            .unwrap_or_default();
        Ok(Store { root: root.to_path_buf(), asset_hashes: Mutex::new(asset_hashes) })
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn blob_path(&self, hex: &str) -> PathBuf {
        self.root.join("blobs").join(&hex[..2]).join(hex)
    }

    pub fn has_blob(&self, hex: &str) -> bool {
        self.blob_path(hex).exists()
    }

    /// A fresh temporary path inside the cache (same filesystem as the blobs, so renames are atomic).
    pub fn tmp_path(&self, ext: &str) -> PathBuf {
        // Process-wide, so two stores on the same cache in one process never pick the same name.
        static SEQ: AtomicU64 = AtomicU64::new(0);
        let n = SEQ.fetch_add(1, Ordering::Relaxed);
        self.root.join("tmp").join(format!("{}-{n}.{ext}", std::process::id()))
    }

    pub fn put_bytes(&self, bytes: &[u8]) -> Result<String> {
        let hex = hex_of(bytes);
        let p = self.blob_path(&hex);
        if !p.exists() {
            std::fs::create_dir_all(p.parent().unwrap())?;
            let tmp = self.tmp_path("blob");
            std::fs::write(&tmp, bytes)?;
            std::fs::rename(&tmp, &p)?;
        }
        Ok(hex)
    }

    /// Moves a finished file into the store; returns its hash.
    pub fn put_file(&self, path: &Path) -> Result<String> {
        let hex = hash_file(path)?;
        let p = self.blob_path(&hex);
        if p.exists() {
            std::fs::remove_file(path)?;
        } else {
            std::fs::create_dir_all(p.parent().unwrap())?;
            std::fs::rename(path, &p).with_context(|| format!("moving {} into the store", path.display()))?;
        }
        Ok(hex)
    }

    pub fn get_blob(&self, hex: &str) -> Result<Vec<u8>> {
        std::fs::read(self.blob_path(hex)).with_context(|| format!("reading blob {hex}"))
    }

    fn index_path(&self, kind: &str, key: &str) -> PathBuf {
        self.root.join("index").join(kind).join(format!("{key}.json"))
    }

    pub fn get_index(&self, kind: &str, key: &str) -> Option<Value> {
        let s = std::fs::read_to_string(self.index_path(kind, key)).ok()?;
        serde_json::from_str(&s).ok()
    }

    pub fn set_index(&self, kind: &str, key: &str, v: &Value) -> Result<()> {
        let p = self.index_path(kind, key);
        std::fs::create_dir_all(p.parent().unwrap())?;
        let tmp = self.tmp_path("json");
        std::fs::write(&tmp, serde_json::to_vec(v)?)?;
        std::fs::rename(&tmp, &p)?;
        Ok(())
    }

    /// The BLAKE3 of an asset's content, cached by (path, size, mtime).
    pub fn asset_hash(&self, path: &Path) -> Result<String> {
        let meta = std::fs::metadata(path).with_context(|| format!("asset {}", path.display()))?;
        let mtime = meta
            .modified()
            .ok()
            .and_then(|m| m.duration_since(std::time::UNIX_EPOCH).ok())
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0);
        let key = path.to_string_lossy().into_owned();
        if let Some(v) = self.asset_hashes.lock().unwrap().get(&key)
            && v["size"] == meta.len()
            && v["mtime"] == mtime
            && let Some(h) = v["hash"].as_str()
        {
            return Ok(h.to_string());
        }
        let hex = hash_file(path)?;
        self.asset_hashes
            .lock()
            .unwrap()
            .insert(key, serde_json::json!({"size": meta.len(), "mtime": mtime, "hash": hex}));
        Ok(hex)
    }

    pub fn save_asset_hashes(&self) -> Result<()> {
        let map = self.asset_hashes.lock().unwrap();
        let tmp = self.tmp_path("json");
        std::fs::write(&tmp, serde_json::to_vec_pretty(&*map)?)?;
        std::fs::rename(&tmp, self.root.join("assets.json"))?;
        Ok(())
    }

    /// Removes this process's temporary files, and anyone's older than a day (an interrupted run). Another render
    /// using the same cache at the same time keeps its files.
    pub fn clean_tmp(&self) {
        let mine = format!("{}-", std::process::id());
        let day = std::time::Duration::from_secs(86_400);
        if let Ok(rd) = std::fs::read_dir(self.root.join("tmp")) {
            for e in rd.flatten() {
                let stale = e.metadata().ok().and_then(|m| m.modified().ok()).and_then(|m| m.elapsed().ok());
                if e.file_name().to_string_lossy().starts_with(&mine) || stale.is_some_and(|a| a > day) {
                    let _ = std::fs::remove_file(e.path());
                }
            }
        }
    }
}

pub fn hash_file(path: &Path) -> Result<String> {
    let mut f = std::fs::File::open(path).with_context(|| format!("opening {}", path.display()))?;
    let mut h = blake3::Hasher::new();
    let mut buf = vec![0u8; 1 << 20];
    loop {
        let n = f.read(&mut buf)?;
        if n == 0 {
            break;
        }
        h.update(&buf[..n]);
    }
    Ok(h.finalize().to_hex().to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(name: &str) -> PathBuf {
        let d = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/tmp/store").join(name);
        let _ = std::fs::remove_dir_all(&d);
        d
    }

    #[test]
    fn blobs_are_content_addressed() {
        let s = Store::open(&tmp("blobs")).unwrap();
        let a = s.put_bytes(b"hello").unwrap();
        assert_eq!(a, hex_of(b"hello"));
        assert_eq!(s.put_bytes(b"hello").unwrap(), a, "same bytes, same blob");
        assert_eq!(s.get_blob(&a).unwrap(), b"hello");
        let f = s.tmp_path("x");
        std::fs::write(&f, b"hello").unwrap();
        assert_eq!(s.put_file(&f).unwrap(), a);
        assert!(!f.exists());
        s.set_index("unit", "k1", &serde_json::json!({"blob": a})).unwrap();
        assert_eq!(s.get_index("unit", "k1").unwrap()["blob"], a.as_str());
        // Tests run in the crate folder: a relative cache path still gives absolute blob paths (ffmpeg concat lists).
        let rel = Store::open(Path::new("../../target/tmp/store/relative")).unwrap();
        assert!(rel.root().is_absolute() && rel.blob_path(&a).is_absolute());
        assert!(s.get_index("unit", "k2").is_none());
    }

    #[test]
    fn asset_hashes_follow_content() {
        let root = tmp("assets");
        let s = Store::open(&root).unwrap();
        let f = root.join("a.bin");
        std::fs::write(&f, b"one").unwrap();
        let h1 = s.asset_hash(&f).unwrap();
        assert_eq!(h1, hex_of(b"one"));
        std::thread::sleep(std::time::Duration::from_millis(10));
        std::fs::write(&f, b"two!").unwrap();
        assert_eq!(s.asset_hash(&f).unwrap(), hex_of(b"two!"));
        s.save_asset_hashes().unwrap();
        let s2 = Store::open(&root).unwrap();
        assert_eq!(s2.asset_hash(&f).unwrap(), hex_of(b"two!"));
    }
}
