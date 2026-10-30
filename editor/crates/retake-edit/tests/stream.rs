//! Streamed renders (spec/bridge.md, "Streaming") on the demo at low resolution: the playlist only ever grows at its
//! end and is always whole when read, units publish in timeline order even when a later one finishes first, the
//! segments decode to the same pictures as the MP4 export and to its sound sample for sample (no seam clicks), and a
//! second render publishes its cached units at once. Needs ffmpeg and ffprobe on PATH.

use retake_timeline::schema::check_manifest;
use serde_json::Value;
use std::io::{BufRead, BufReader};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

const BIN: &str = env!("CARGO_BIN_EXE_retake-edit");

fn demo_src() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples/demo")
}

/// Runs a streamed render; returns its report and the progress lines it printed, while `watch` sees each playlist
/// read along the way.
fn stream(
    tl: &Path,
    playlist: &Path,
    extra: &[&str],
    delay: &str,
    watch: impl Fn(&str) + Send + 'static,
) -> (Value, Vec<Value>) {
    let stop = Arc::new(AtomicBool::new(false));
    let reader = {
        let (stop, playlist) = (stop.clone(), playlist.to_path_buf());
        std::thread::spawn(move || {
            while !stop.load(Ordering::SeqCst) {
                if let Ok(s) = std::fs::read_to_string(&playlist) {
                    watch(&s);
                }
                std::thread::sleep(Duration::from_millis(3));
            }
        })
    };
    let mut child = Command::new(BIN)
        .arg("render")
        .arg(tl)
        .arg("--hls")
        .arg(playlist)
        .args(["--scale", "0.25", "--jobs", "3", "--progress", "--json"])
        .args(extra)
        .env("RETAKE_EDIT_TEST_UNIT_DELAY_MS", delay)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("retake-edit runs");
    let lines: Vec<Value> = BufReader::new(child.stderr.take().unwrap())
        .lines()
        .map_while(Result::ok)
        .map(|l| serde_json::from_str(&l).unwrap_or_else(|e| panic!("progress line {l:?}: {e}")))
        .collect();
    let out = child.wait_with_output().unwrap();
    stop.store(true, Ordering::SeqCst);
    reader.join().unwrap();
    let v: Value = serde_json::from_slice(&out.stdout).expect("JSON report");
    assert_eq!(v["ok"], true, "{v:#}");
    (v, lines)
}

/// The segment names and durations of a playlist, checking that it's whole: a header, then EXTINF/name pairs, then
/// at most an ENDLIST, every line ended.
fn parse(s: &str) -> (Vec<(String, f64)>, bool, u32) {
    assert!(s.starts_with("#EXTM3U\n") && s.ends_with('\n'), "a whole playlist: {s:?}");
    let mut segs = vec![];
    let (mut ended, mut target, mut dur) = (false, 0, None);
    for l in s.lines() {
        assert!(!ended, "nothing after ENDLIST");
        if let Some(t) = l.strip_prefix("#EXT-X-TARGETDURATION:") {
            target = t.parse().unwrap();
        } else if let Some(d) = l.strip_prefix("#EXTINF:") {
            dur = Some(d.trim_end_matches(',').parse::<f64>().unwrap());
        } else if l == "#EXT-X-ENDLIST" {
            ended = true;
        } else if !l.starts_with('#') {
            segs.push((l.to_string(), dur.take().expect("EXTINF before each segment")));
        }
    }
    assert!(dur.is_none(), "no EXTINF without its segment");
    (segs, ended, target)
}

fn pcm(input: &Path) -> Vec<f32> {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-v", "error", "-i"])
        .arg(input)
        .args(["-map", "0:a", "-f", "f32le", "-ac", "2", "-ar", "48000", "-"])
        .output()
        .unwrap();
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    out.stdout.as_chunks::<4>().0.iter().map(|b| f32::from_le_bytes(*b)).collect()
}

/// MD5 of every decoded video frame.
fn frame_md5s(input: &Path) -> Vec<String> {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-v", "error", "-i"])
        .arg(input)
        .args(["-map", "0:v", "-f", "framemd5", "-"])
        .output()
        .unwrap();
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| l.rsplit(',').next().map(|s| s.trim().to_string()))
        .collect()
}

#[test]
fn streamed_render() {
    let dir = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("stream");
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(dir.join("patches")).unwrap();
    for f in ["timeline.json", "vo.words.json", "patches/01-note-pop.json"] {
        std::fs::copy(demo_src().join(f), dir.join(f)).unwrap();
    }
    let st = Command::new("bash")
        .arg(demo_src().join("make-media.sh"))
        .arg(dir.join("media"))
        .arg(BIN)
        .env("DEMO_SIZE", "320x180")
        .status()
        .expect("make-media.sh runs");
    assert!(st.success());
    let tl = dir.join("timeline.json");
    let playlist = dir.join("v1/demo_v1.m3u8");
    let export = dir.join("v1/demo_v1.export.mp4");

    // The first unit is slow and the others aren't, so units 2.. finish first: they still publish after it, in order.
    let seen: Arc<Mutex<Vec<String>>> = Arc::new(Mutex::new(vec![]));
    let log = seen.clone();
    let t = Instant::now();
    let (rep, lines) = stream(&tl, &playlist, &["-o", export.to_str().unwrap()], "900,0", move |s| {
        let mut log = log.lock().unwrap();
        if log.last().map(String::as_str) != Some(s) {
            parse(s); // whole, every time
            log.push(s.to_string());
        }
    });
    assert!(t.elapsed() >= Duration::from_millis(900));
    let seen = seen.lock().unwrap().clone();
    assert!(seen.len() >= 2, "the playlist was read while growing: {seen:?}");
    for w in seen.windows(2) {
        assert!(w[1].starts_with(w[0].trim_end_matches("#EXT-X-ENDLIST\n")), "it only grows at its end");
        assert!(!w[0].contains("#EXT-X-ENDLIST"), "ENDLIST only at the very end");
    }
    let plan = &lines[0];
    assert_eq!(plan["progress"], "plan");
    let units = plan["units"].as_u64().unwrap() as usize;
    assert_eq!(units, 6);
    let publishes: Vec<&Value> = lines.iter().filter(|l| l["progress"] == "publish").collect();
    let done: Vec<u64> = publishes.iter().map(|l| l["done"].as_u64().unwrap()).collect();
    assert_eq!(done, (1..=units as u64).collect::<Vec<_>>(), "units publish in timeline order");
    assert!(publishes[0]["elapsed"].as_f64().unwrap() >= 0.9, "the first unit held everything back");
    assert_eq!(lines.last().unwrap()["progress"], "end");

    let text = std::fs::read_to_string(&playlist).unwrap();
    let (segs, ended, target) = parse(&text);
    assert!(ended);
    assert_eq!(rep["stream"]["segments"].as_u64().unwrap() as usize, segs.len());
    let names: Vec<String> = (0..segs.len()).map(|i| format!("seg_{i:05}.ts")).collect();
    assert_eq!(segs.iter().map(|s| s.0.clone()).collect::<Vec<_>>(), names, "in order, numbered");
    assert!(segs.iter().all(|s| s.1.round() as u32 <= target), "TARGETDURATION covers every segment");
    for (name, _) in &segs {
        assert!(dir.join("v1").join(name).is_file());
    }
    let leftovers: Vec<String> = std::fs::read_dir(dir.join("v1"))
        .unwrap()
        .flatten()
        .map(|e| e.file_name().to_string_lossy().into_owned())
        .filter(|n| n.starts_with('.'))
        .collect();
    assert_eq!(leftovers, Vec::<String>::new(), "no temporary files left");

    // The manifest beside the playlist names it; the segments follow the units, their durations sum to the units'.
    let m: Value =
        serde_json::from_str(&std::fs::read_to_string(dir.join("v1/demo_v1.manifest.json")).unwrap()).unwrap();
    assert_eq!(check_manifest(&m), vec![]);
    assert_eq!(m["video"], "demo_v1.m3u8");
    let total: f64 = segs.iter().map(|s| s.1).sum();
    assert!((total - m["duration"].as_f64().unwrap()).abs() < 1e-3, "{total} vs {}", m["duration"]);
    let mut t_unit = 0.0;
    let starts: Vec<f64> = segs
        .iter()
        .map(|s| {
            let a = t_unit;
            t_unit += s.1;
            a
        })
        .collect();
    for u in m["units"].as_array().unwrap() {
        let t0 = u["t"][0].as_f64().unwrap();
        assert!(starts.iter().any(|s| (s - t0).abs() < 1e-3), "a segment starts where unit {} does", u["unit"]);
    }

    // The same pictures as the export, frame for frame.
    let (a, b) = (frame_md5s(&playlist), frame_md5s(&export));
    assert_eq!(a.len(), 483);
    assert_eq!(a, b, "the segments decode to the export's frames");

    // The same sound, sample for sample after the first AAC frame (the priming frame is left out), seams included.
    let (hls, mp4) = (pcm(&playlist), pcm(&export));
    assert!(hls.len() >= mp4.len(), "{} vs {}", hls.len(), mp4.len());
    let worst = (2048..mp4.len()).map(|i| (hls[i] - mp4[i]).abs()).fold(0.0f32, f32::max);
    assert!(worst < 1e-3, "the stream's audio is the export's: worst sample difference {worst}");
    let seams: Vec<usize> = m["units"].as_array().unwrap()[1..]
        .iter()
        .map(|u| (u["t"][0].as_f64().unwrap() * 48000.0) as usize * 2)
        .collect();
    for s in seams {
        let near = (s - 2048..s + 2048).map(|i| (hls[i] - mp4[i]).abs()).fold(0.0f32, f32::max);
        assert!(near < 1e-4, "no click at the seam at sample {}: {near}", s / 2);
    }

    // A patch that changes one unit: everything before it is cached and published at once.
    let st = Command::new(BIN).args(["patch", "apply"]).arg(&tl).arg(dir.join("patches/01-note-pop.json")).status();
    assert!(st.unwrap().success());
    let (rep2, lines2) = stream(&tl, &dir.join("v2/demo_v2.m3u8"), &[], "", |_| {});
    assert_eq!(rep2["reused"].as_u64(), Some(5), "{rep2:#}");
    assert!(rep2.get("output").is_none(), "no MP4 unless asked");
    let first = lines2.iter().find(|l| l["progress"] == "publish").unwrap();
    assert!(first["elapsed"].as_f64().unwrap() < rep2["stream"]["done_s"].as_f64().unwrap() + 1e-9);
    assert!(dir.join("v2/demo_v2.manifest.json").is_file());
    assert!(parse(&std::fs::read_to_string(dir.join("v2/demo_v2.m3u8")).unwrap()).1);

    // A stream covers the whole timeline.
    let out = Command::new(BIN)
        .arg("render")
        .arg(&tl)
        .arg("--hls")
        .arg(dir.join("v3/demo_v3.m3u8"))
        .args(["--range", "1-2", "--json"])
        .output()
        .unwrap();
    assert!(!out.status.success());
    assert!(String::from_utf8_lossy(&out.stdout).contains("--range"));
    assert!(!dir.join("v3/demo_v3.m3u8").exists());
}
