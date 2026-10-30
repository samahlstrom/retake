//! End to end on the demo project at low resolution: generated media → render → a patch tagged with a note id →
//! render again (only the changed unit renders; unchanged units are byte-identical) → a ripple → note resolution →
//! a cold-cache render gives the same bytes. Needs ffmpeg and ffprobe on PATH.

use retake_timeline::schema::{check_manifest, check_note_resolution};
use serde_json::Value;
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::process::Command;

const BIN: &str = env!("CARGO_BIN_EXE_retake-edit");

fn demo_src() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples/demo")
}

fn edit(args: &[&str]) -> Value {
    let out = Command::new(BIN).args(args).arg("--json").output().expect("retake-edit runs");
    let v: Value = serde_json::from_slice(&out.stdout).unwrap_or_else(|e| {
        panic!("{args:?}: {e}\n{}\n{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr))
    });
    assert_eq!(v["ok"], true, "{args:?}: {v:#}");
    v
}

fn probe(p: &Path, args: &[&str]) -> String {
    let out = Command::new("ffprobe").args(["-v", "error"]).args(args).arg(p).output().expect("ffprobe runs");
    String::from_utf8_lossy(&out.stdout).trim().to_string()
}

/// MD5 of every video packet, copied without decoding.
fn packet_md5s(p: &Path) -> Vec<String> {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-v", "error", "-i"])
        .arg(p)
        .args(["-map", "0:v", "-c", "copy", "-f", "framemd5", "-"])
        .output()
        .expect("ffmpeg runs");
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .filter(|l| !l.starts_with('#'))
        .filter_map(|l| l.rsplit(',').next().map(|s| s.trim().to_string()))
        .collect()
}

fn manifest(p: &Path) -> Value {
    let v: Value = serde_json::from_str(&std::fs::read_to_string(p).unwrap()).unwrap();
    assert_eq!(check_manifest(&v), vec![], "the manifest matches spec/manifest.schema.json");
    v
}

fn by_unit(m: &Value) -> BTreeMap<String, (String, String, String)> {
    m["units"]
        .as_array()
        .unwrap()
        .iter()
        .map(|u| {
            let s = |k: &str| u[k].as_str().unwrap().to_string();
            (format!("{}@{}", s("shot"), u["frames"][1]), (s("key"), s("video_hash"), s("audio_hash")))
        })
        .collect()
}

#[test]
fn demo_end_to_end() {
    let dir = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("demo");
    let _ = std::fs::remove_dir_all(&dir);
    std::fs::create_dir_all(dir.join("patches")).unwrap();
    for f in ["timeline.json", "vo.words.json", "patches/01-note-pop.json", "patches/02-trim-vo.json"] {
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
    let t = tl.to_str().unwrap();
    let low = ["--scale", "0.25", "--jobs", "2"];

    // Validate and resolve.
    let v = edit(&["validate", t]);
    assert_eq!(v["units"], 6);
    let r = edit(&["resolve", t]);
    assert_eq!(r["frames"], 483);

    // First render: everything renders.
    let out0 = dir.join("r0.mp4");
    let mut args = vec!["render", t, "-o", out0.to_str().unwrap()];
    args.extend(low);
    let r0 = edit(&args);
    assert_eq!((r0["units"].as_u64(), r0["reused"].as_u64(), r0["rendered"].as_u64()), (Some(6), Some(0), Some(6)));
    assert_eq!(
        probe(
            &out0,
            &["-count_frames", "-select_streams", "v:0", "-show_entries", "stream=nb_read_frames", "-of", "csv=p=0"]
        ),
        "483"
    );
    assert_eq!(
        probe(&out0, &["-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0"]),
        "480,270"
    );
    assert_eq!(
        probe(&out0, &["-select_streams", "a:0", "-show_entries", "stream=codec_name", "-of", "csv=p=0"]),
        "aac"
    );
    let dur: f64 = probe(&out0, &["-show_entries", "format=duration", "-of", "csv=p=0"]).parse().unwrap();
    assert!((dur - 16.1).abs() < 0.05, "{dur}");
    let m0 = manifest(&dir.join("r0.manifest.json"));
    let frames: i64 = m0["units"]
        .as_array()
        .unwrap()
        .iter()
        .map(|u| u["frames"][1].as_i64().unwrap() - u["frames"][0].as_i64().unwrap())
        .sum();
    assert_eq!(frames, 483);

    // Plan says everything is cached now.
    let mut args = vec!["plan", t];
    args.extend(low);
    let p = edit(&args);
    assert_eq!((p["hits"].as_u64(), p["misses"].as_u64()), (Some(6), Some(0)));

    // A patch answering note v1#3 changes the pop: one unit re-renders.
    let pa = edit(&["patch", "apply", t, dir.join("patches/01-note-pop.json").to_str().unwrap()]);
    assert_eq!(pa["patch"]["id"], "p1");
    assert_eq!(pa["affected"]["rerender"], 1);
    assert_eq!(pa["affected"]["changed"], serde_json::json!(["u5"]));
    let out1 = dir.join("r1.mp4");
    let mut args = vec!["render", t, "-o", out1.to_str().unwrap()];
    args.extend(low);
    let r1 = edit(&args);
    assert_eq!((r1["reused"].as_u64(), r1["rendered"].as_u64()), (Some(5), Some(1)), "{r1:#}");
    assert_eq!(r1["layers_rendered"], 1, "only the pop's graphics");
    assert_eq!(r1["audio_reused"], true, "a picture-only patch keeps the mix");
    let m1 = manifest(&dir.join("r1.manifest.json"));
    let (u0, u1) = (by_unit(&m0), by_unit(&m1));
    let changed: Vec<&String> = u1.keys().filter(|k| u0[*k].0 != u1[*k].0).collect();
    assert_eq!(changed, vec!["body/footage@332"]);
    for (k, (key, vh, ah)) in &u1 {
        assert_eq!(ah, &u0[k].2, "{k}: same sound");
        if key == &u0[k].0 {
            assert_eq!(vh, &u0[k].1, "{k}: a reused unit has the same video hash");
        }
    }
    // Byte identity in the published files: every packet outside the changed unit is the same.
    let (p0, p1) = (packet_md5s(&out0), packet_md5s(&out1));
    assert_eq!(p0.len(), 483);
    assert_eq!(p1.len(), 483);
    let diff: Vec<usize> = (0..483).filter(|i| p0[*i] != p1[*i]).collect();
    assert!(!diff.is_empty() && diff.iter().all(|i| (171..332).contains(i)), "only u5's packets differ: {diff:?}");

    // A ripple: trim 0.3 s from spine item a. Moved units with relative content are reused; the timeline-clock
    // background re-encodes but its graphics come from the cache.
    edit(&["patch", "apply", t, dir.join("patches/02-trim-vo.json").to_str().unwrap()]);
    let out2 = dir.join("r2.mp4");
    let mut args = vec!["render", t, "-o", out2.to_str().unwrap()];
    args.extend(low);
    let r2 = edit(&args);
    assert_eq!(r2["frames"], 474);
    let m2 = manifest(&dir.join("r2.manifest.json"));
    let reused: Vec<String> = m2["units"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|u| u["reused"] == true)
        .map(|u| u["unit"].as_str().unwrap().to_string())
        .collect();
    assert_eq!(reused, ["u1", "u3", "u5"], "title, insert and the moved body shot are reused");
    assert_eq!(r2["layers_rendered"], 2, "only the badge on the resized footage shot");
    // u6 has two graphics runs (card + title + clock, consecutive layers, are one; the lower third is the other).
    assert_eq!(r2["layers_reused"], 2, "u6's graphics come from the cache");
    let u5_before = m1["units"][4]["video_hash"].clone();
    assert_eq!(m2["units"][4]["video_hash"], u5_before, "the moved unit is the same bytes");
    assert_ne!(m2["units"][4]["t"], m1["units"][4]["t"], "…at a new time");

    // The log has both patches with their note ids.
    let log = edit(&["patch", "log", t]);
    assert_eq!(log["patches"][0]["answers"], serde_json::json!(["v1#3"]));
    assert_eq!(log["patches"][1]["answers"], serde_json::json!(["v1#5"]));

    // A stroke over the picture-in-picture at 9.0 s resolves to it, ahead of the shot under it.
    let mp = dir.join("r2.manifest.json");
    let n = edit(&["resolve-note", mp.to_str().unwrap(), "9.0", "0.7", "0.65", "0.1", "0.1"]);
    let mut nr = n.clone();
    nr.as_object_mut().unwrap().remove("ok");
    assert_eq!(check_note_resolution(&nr), vec![]);
    assert_eq!(n["targets"][0]["id"], "pip/v");
    assert_eq!(n["targets"][1]["id"], "pip");
    assert_eq!(n["word"][2], "for");

    // Stills.
    let st = edit(&["frames", t, "1.0", "9.0", "-o", dir.join("stills").to_str().unwrap(), "--scale", "0.25"]);
    assert_eq!(st["frames"].as_array().unwrap().len(), 2);
    assert!(dir.join("stills/frame-001.000.png").exists());

    // Cold cache, same machine: the same segment bytes (x264 with pinned threads, bitexact muxing).
    let cold = dir.join("cold-cache");
    let mut args = vec!["manifest", t, "--cache", cold.to_str().unwrap()];
    args.extend(low);
    let mc = edit(&args);
    let hashes = |m: &Value| -> Vec<String> {
        m["units"].as_array().unwrap().iter().map(|u| u["video_hash"].as_str().unwrap().to_string()).collect()
    };
    assert_eq!(hashes(&mc["manifest"]), hashes(&m2), "a cold render reproduces every segment byte for byte");

    // Errors are typed.
    let bad = dir.join("bad.json");
    std::fs::write(&bad, r#"{"base": 2, "answers": [], "summary": "x", "ops": [{"op": "set", "id": "pop/faster", "path": "from", "value": "word:b/fastr#2"}]}"#).unwrap();
    let out = Command::new(BIN).args(["patch", "apply", t, bad.to_str().unwrap(), "--json"]).output().unwrap();
    assert!(!out.status.success());
    let e: Value = serde_json::from_slice(&out.stdout).unwrap();
    assert_eq!(e["ok"], false);
    assert_eq!(e["errors"][0]["code"], "anchor_word_not_found");
    assert_eq!(e["errors"][0]["hint"], "did you mean 'faster'?");
}
