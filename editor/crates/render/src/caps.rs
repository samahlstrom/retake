//! What the ffmpeg in use can do on this machine: whether it has zimg (`zscale`), and which H.264 encoder `auto`
//! picks (a real one-frame test encode: NVENC on NVIDIA, VideoToolbox on macOS, else x264). Each is probed once per
//! process and cached per machine for a day in `<user cache>/retake/ffmpeg-caps.json`, keyed by the ffmpeg binary
//! and its version line, so `plan` doesn't pay for a test encode every time.

use crate::ffmpeg::{self, EncodeSettings, EncoderKind};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::OnceLock;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

/// How long a probe stays good.
pub const TTL_S: u64 = 24 * 3600;
/// A test encode that takes longer than this failed.
const TEST_TIMEOUT: Duration = Duration::from_secs(20);

#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct Probed<T> {
    pub value: T,
    /// Unix seconds.
    pub at: u64,
}

/// One ffmpeg binary's entry in the cache file.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
pub struct Entry {
    pub version: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub zscale: Option<Probed<bool>>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub auto: Option<Probed<EncoderKind>>,
    /// The test encodes `auto` was decided by: other encoder settings (a new version's) decide again.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub auto_for: String,
}

fn now() -> u64 {
    SystemTime::now().duration_since(UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0)
}

/// The user's cache folder for Retake: `$XDG_CACHE_HOME/retake`, else `~/Library/Caches/retake` on macOS,
/// `%LOCALAPPDATA%\retake` on Windows, `~/.cache/retake` elsewhere.
pub fn user_cache_dir() -> Option<PathBuf> {
    let env = |k: &str| std::env::var_os(k).map(PathBuf::from).filter(|p| p.is_absolute());
    if let Some(x) = env("XDG_CACHE_HOME") {
        return Some(x.join("retake"));
    }
    if cfg!(windows) {
        return env("LOCALAPPDATA").map(|p| p.join("retake"));
    }
    let home = env("HOME")?;
    Some(if cfg!(target_os = "macos") { home.join("Library/Caches/retake") } else { home.join(".cache/retake") })
}

/// The cache file (`RETAKE_CAPS_FILE` overrides it; tests point it at a scratch file).
pub fn cache_file() -> Option<PathBuf> {
    match std::env::var_os("RETAKE_CAPS_FILE") {
        Some(p) => Some(PathBuf::from(p)),
        None => user_cache_dir().map(|d| d.join("ffmpeg-caps.json")),
    }
}

fn read_all(file: &Path) -> BTreeMap<String, Entry> {
    std::fs::read_to_string(file).ok().and_then(|s| serde_json::from_str(&s).ok()).unwrap_or_default()
}

/// The entry for `bin` at `version`, if the file has one for that version.
pub fn load(file: &Path, bin: &str, version: &str) -> Option<Entry> {
    read_all(file).remove(bin).filter(|e| e.version == version)
}

/// Updates `bin`'s entry (a fresh one when the version changed) and writes the file whole (a temporary file renamed
/// over it, so a reader never sees half). Best effort: a cache that can't be written is only slower.
pub fn update(file: &Path, bin: &str, version: &str, f: impl FnOnce(&mut Entry)) {
    let mut all = read_all(file);
    let e = all.entry(bin.to_string()).or_default();
    if e.version != version {
        *e = Entry { version: version.to_string(), ..Entry::default() };
    }
    f(e);
    let Ok(text) = serde_json::to_string_pretty(&all) else { return };
    if let Some(d) = file.parent() {
        let _ = std::fs::create_dir_all(d);
    }
    let tmp = file.with_extension(format!("{}.tmp", std::process::id()));
    if std::fs::write(&tmp, text).is_ok() && std::fs::rename(&tmp, file).is_err() {
        let _ = std::fs::remove_file(&tmp);
    }
}

/// A cached value when it's younger than [`TTL_S`].
pub fn fresh<T: Copy>(p: Option<Probed<T>>, now: u64) -> Option<T> {
    p.filter(|p| now.saturating_sub(p.at) < TTL_S).map(|p| p.value)
}

/// Looks a value up in the cache, or probes it and stores it.
fn cached<T: Copy>(
    get: impl Fn(&Entry) -> Option<Probed<T>>,
    set: impl FnOnce(&mut Entry, Probed<T>),
    probe: impl FnOnce() -> T,
) -> T {
    let (bin, version) = (ffmpeg::ffmpeg_bin(), ffmpeg::version());
    let file = cache_file();
    let t = now();
    if let Some(v) = file.as_deref().and_then(|f| load(f, &bin, version)).and_then(|e| fresh(get(&e), t)) {
        return v;
    }
    let value = probe();
    if let Some(f) = file.as_deref() {
        update(f, &bin, version, |e| set(e, Probed { value, at: t }));
    }
    value
}

/// Whether ffmpeg has the `zscale` filter (zimg).
pub fn has_zscale() -> bool {
    static Z: OnceLock<bool> = OnceLock::new();
    *Z.get_or_init(|| cached(|e| e.zscale, |e, p| e.zscale = Some(p), probe_zscale))
}

fn probe_zscale() -> bool {
    Command::new(ffmpeg::ffmpeg_bin())
        .args(["-hide_banner", "-filters"])
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .ok()
        .map(|o| filters_have_zscale(&String::from_utf8_lossy(&o.stdout)))
        .unwrap_or(false)
}

/// `ffmpeg -filters` lists zscale (` .S zscale  V->V  Apply resizing, …`).
pub fn filters_have_zscale(list: &str) -> bool {
    list.lines().any(|l| l.split_whitespace().nth(1) == Some("zscale"))
}

/// The encoders `auto` tries, in order, on this OS.
pub fn auto_candidates() -> Vec<EncoderKind> {
    if cfg!(target_os = "macos") { vec![EncoderKind::Videotoolbox] } else { vec![EncoderKind::Nvenc] }
}

/// What `auto` picks: the first candidate whose test encode works, else x264.
pub fn auto_encoder() -> EncoderKind {
    static A: OnceLock<EncoderKind> = OnceLock::new();
    *A.get_or_init(|| {
        let tests = crate::store::hex_of(
            format!("{:?}", auto_candidates().into_iter().map(test_command).collect::<Vec<_>>()).as_bytes(),
        );
        cached(
            |e| e.auto.filter(|_| e.auto_for == tests),
            |e, p| {
                e.auto = Some(p);
                e.auto_for = tests.clone();
            },
            || auto_candidates().into_iter().find(|k| test_encode(*k)).unwrap_or(EncoderKind::X264),
        )
    })
}

/// The command line of a one-frame test encode with `kind`'s real render settings, into nothing.
pub fn test_command(kind: EncoderKind) -> Vec<String> {
    let s = EncodeSettings::new(false, 30, kind);
    let mut a: Vec<String> = ["-nostdin", "-v", "error", "-hide_banner"].map(String::from).to_vec();
    a.extend(s.input_args());
    a.extend(["-f", "lavfi", "-i", "color=c=gray:s=256x144:r=30", "-frames:v", "2", "-an"].map(String::from));
    a.extend(s.output_args());
    a.extend(["-f", "null", "-"].map(String::from));
    a
}

/// Whether `kind` encodes here: ffmpeg has it, and the device behind it works (a hardware encoder fails at once
/// without one).
pub fn test_encode(kind: EncoderKind) -> bool {
    let Ok(mut child) = Command::new(ffmpeg::ffmpeg_bin())
        .args(test_command(kind))
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
    else {
        return false;
    };
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(st)) => return st.success(),
            Ok(None) if start.elapsed() < TEST_TIMEOUT => std::thread::sleep(Duration::from_millis(20)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return false;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_filter_list_says_whether_zscale_is_there() {
        let with =
            " TS psnr              VV->V      Calculate the PSNR\n .S zscale            V->V       Apply resizing";
        assert!(filters_have_zscale(with));
        assert!(!filters_have_zscale(" .. scale             V->V       Scale the input video size"));
        assert!(!filters_have_zscale("zscale mentioned in a sentence"));
    }

    #[test]
    fn entries_are_per_binary_and_version_and_go_stale() {
        let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/tmp/caps-file");
        let _ = std::fs::remove_dir_all(&dir);
        let f = dir.join("sub/ffmpeg-caps.json");
        assert_eq!(load(&f, "ffmpeg", "ffmpeg version 9"), None, "no file yet");
        update(&f, "ffmpeg", "ffmpeg version 9", |e| {
            e.zscale = Some(Probed { value: true, at: 100 });
            e.auto = Some(Probed { value: EncoderKind::Nvenc, at: 100 });
        });
        update(&f, "/opt/ffmpeg", "ffmpeg version 6", |e| e.zscale = Some(Probed { value: false, at: 100 }));
        let e = load(&f, "ffmpeg", "ffmpeg version 9").unwrap();
        assert_eq!(fresh(e.auto, 100 + 60), Some(EncoderKind::Nvenc));
        assert_eq!(fresh(e.auto, 100 + TTL_S), None, "a day later it's probed again");
        assert!(!load(&f, "/opt/ffmpeg", "ffmpeg version 6").unwrap().zscale.unwrap().value);
        assert_eq!(load(&f, "ffmpeg", "ffmpeg version 10"), None, "another build is another entry");
        // A new version replaces the old entry rather than mixing with it.
        update(&f, "ffmpeg", "ffmpeg version 10", |e| e.zscale = Some(Probed { value: true, at: 200 }));
        let e = load(&f, "ffmpeg", "ffmpeg version 10").unwrap();
        assert_eq!(e.auto, None);
        let left: Vec<_> = std::fs::read_dir(f.parent().unwrap()).unwrap().flatten().map(|e| e.file_name()).collect();
        assert_eq!(left, vec![std::ffi::OsString::from("ffmpeg-caps.json")], "no temporary file left");
    }

    #[test]
    fn auto_tries_the_platforms_hardware_encoder() {
        let c = auto_candidates();
        if cfg!(target_os = "macos") {
            assert_eq!(c, vec![EncoderKind::Videotoolbox]);
        } else {
            assert_eq!(c, vec![EncoderKind::Nvenc]);
        }
        let cmd = test_command(EncoderKind::Nvenc).join(" ");
        assert!(cmd.contains("-f lavfi -i color=c=gray:s=256x144:r=30 -frames:v 2"), "{cmd}");
        assert!(cmd.contains("-c:v h264_nvenc") && cmd.ends_with("-f null -"), "{cmd}");
        assert!(test_encode(EncoderKind::X264), "x264 always works (ffmpeg on PATH has it)");
    }
}
