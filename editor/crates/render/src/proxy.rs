//! Proxies: smaller, short-GOP copies of video assets. A long screen or camera recording usually has a keyframe
//! every few seconds, so every seek decodes up to that many frames first; a proxy has one every half second and is
//! draft-sized, so draft renders decode it instead (automatically, once it exists). Full renders always read the
//! source. A proxy lives in the cache's store, indexed by the source's content hash (`index/proxy/<hash>.json`).

use crate::engine::Asset;
use crate::ffmpeg::ffmpeg_bin;
use crate::store::Store;
use anyhow::{Context, Result, bail};
use serde::Serialize;
use serde_json::json;
use std::path::PathBuf;
use std::process::{Command, Stdio};
use std::time::Instant;

/// Part of every proxy's index entry: bump when the proxy encode changes.
pub const PROXY_VERSION: &str = "proxy/1";

/// The default proxy height: a 1080p source becomes 960×540, what a half-scale draft of 1080p shows.
pub const DEFAULT_HEIGHT: u32 = 540;

/// What `retake-edit proxy` did for one asset.
#[derive(Debug, Clone, Serialize)]
pub struct ProxyReport {
    pub asset: String,
    pub source: PathBuf,
    pub proxy: PathBuf,
    pub size: [u32; 2],
    /// Frames between keyframes.
    pub gop: u32,
    /// False when an up-to-date proxy was already there.
    pub made: bool,
    pub seconds: f64,
}

/// The proxy of a source (by content hash), if one was made and is still in the store: its path and hash.
pub fn lookup(store: &Store, asset_hash: &str) -> Option<(PathBuf, String)> {
    let v = store.get_index("proxy", asset_hash)?;
    let blob = v["blob"].as_str()?;
    store.has_blob(blob).then(|| (store.blob_path(blob), blob.to_string()))
}

fn even(x: f64) -> u32 {
    ((x / 2.0).round() as u32 * 2).max(2)
}

/// The proxy's size for a source: `height` lines (never more than the source), the width keeping the aspect.
pub fn proxy_size(src: [u32; 2], height: u32) -> [u32; 2] {
    let h = even(height.min(src[1]) as f64);
    [even(src[0] as f64 * h as f64 / src[1].max(1) as f64), h]
}

/// Frames between keyframes in a proxy: half a second.
pub fn proxy_gop(fps: f64) -> u32 {
    ((fps / 2.0).round() as u32).max(1)
}

/// Makes the proxy of a video asset (or keeps the one there is, unless `force`).
pub fn make(store: &Store, a: &Asset, height: u32, force: bool) -> Result<ProxyReport> {
    let began = Instant::now();
    let Some(src) = a.info.size else { bail!("asset `{}` has no video stream", a.id) };
    let size = proxy_size(src, height);
    let gop = proxy_gop(a.info.fps.unwrap_or(30.0));
    let want = json!({"v": PROXY_VERSION, "size": size, "gop": gop});
    if !force
        && let Some(v) = store.get_index("proxy", &a.hash)
        && v["params"] == want
        && let Some((path, _)) = lookup(store, &a.hash)
    {
        return Ok(ProxyReport {
            asset: a.id.clone(),
            source: a.path.clone(),
            proxy: path,
            size,
            gop,
            made: false,
            seconds: 0.0,
        });
    }
    let tmp = store.tmp_path("mp4");
    let g = gop.to_string();
    let out = Command::new(ffmpeg_bin())
        .args(["-nostdin", "-v", "error", "-y", "-i"])
        .arg(&a.path)
        .args(["-map", "0:v:0", "-an", "-sn", "-dn", "-fps_mode", "passthrough"])
        .args(["-vf", &format!("scale={}:{}:flags=bicubic", size[0], size[1])])
        .args(["-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-tune", "fastdecode", "-pix_fmt", "yuv420p"])
        .args(["-g", &g, "-keyint_min", &g, "-sc_threshold", "0", "-bf", "0"])
        .args(["-movflags", "+faststart", "-map_metadata", "-1", "-f", "mp4"])
        .arg(&tmp)
        .stdin(Stdio::null())
        .output()
        .with_context(|| format!("running ffmpeg for the proxy of {}", a.id))?;
    if !out.status.success() {
        let _ = std::fs::remove_file(&tmp);
        bail!("ffmpeg failed (proxy of {}): {}", a.id, String::from_utf8_lossy(&out.stderr).trim());
    }
    let blob = store.put_file(&tmp)?;
    store.set_index("proxy", &a.hash, &json!({"blob": blob, "params": want, "asset": a.id}))?;
    Ok(ProxyReport {
        asset: a.id.clone(),
        source: a.path.clone(),
        proxy: store.blob_path(&blob),
        size,
        gop,
        made: true,
        seconds: (began.elapsed().as_secs_f64() * 100.0).round() / 100.0,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sizes_keep_the_aspect_and_never_upscale() {
        assert_eq!(proxy_size([1920, 1080], 540), [960, 540]);
        assert_eq!(proxy_size([3840, 2160], 540), [960, 540]);
        assert_eq!(proxy_size([1080, 1920], 540), [304, 540], "portrait");
        assert_eq!(proxy_size([640, 360], 540), [640, 360], "small sources stay as they are");
        assert_eq!(proxy_gop(30.0), 15);
        assert_eq!(proxy_gop(59.94), 30);
        assert_eq!(proxy_gop(1.0), 1);
    }
}
