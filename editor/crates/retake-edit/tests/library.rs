//! The starter library through the CLI: every component previews (small, deterministic sheets), `component lock` and
//! `component upgrade`, and `lint` with the clean kit's house rules (typed violations, exit 1 on errors).

use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;

const BIN: &str = env!("CARGO_BIN_EXE_retake-edit");

fn library() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../library")
}

fn tmp(name: &str) -> PathBuf {
    let d = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("library").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    d
}

/// Runs retake-edit with --json; returns the exit code and the JSON.
fn edit(args: &[&str]) -> (i32, Value) {
    let out = Command::new(BIN).args(args).arg("--json").output().expect("retake-edit runs");
    let v: Value = serde_json::from_slice(&out.stdout).unwrap_or_else(|e| {
        panic!("{args:?}: {e}\n{}\n{}", String::from_utf8_lossy(&out.stdout), String::from_utf8_lossy(&out.stderr))
    });
    (out.status.code().unwrap_or(-1), v)
}

fn ok(args: &[&str]) -> Value {
    let (code, v) = edit(args);
    assert!(code == 0 && v["ok"] == true, "{args:?}: {v:#}");
    v
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

fn copy_dir(src: &Path, dst: &Path) {
    std::fs::create_dir_all(dst).unwrap();
    for e in std::fs::read_dir(src).unwrap() {
        let e = e.unwrap();
        if e.file_type().unwrap().is_dir() {
            copy_dir(&e.path(), &dst.join(e.file_name()));
        } else {
            std::fs::copy(e.path(), dst.join(e.file_name())).unwrap();
        }
    }
}

fn components() -> Vec<String> {
    let mut v: Vec<String> = std::fs::read_dir(library().join("components"))
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .collect();
    v.sort();
    v
}

#[test]
fn every_library_component_previews() {
    let names = components();
    assert!(names.len() >= 8, "{names:?}");
    for n in &names {
        let src = library().join("components").join(n);
        let committed = src.join("previews/sheet.png");
        let size = std::fs::metadata(&committed).unwrap_or_else(|_| panic!("{n}: no committed sheet")).len();
        assert!(size < 300 * 1024, "{n}: the committed sheet is {size} bytes");
        let out = tmp(&format!("preview-{n}"));
        let v = ok(&["component", "preview", src.to_str().unwrap(), "-o", out.to_str().unwrap(), "--no-webp"]);
        let sheet = out.join("sheet.png");
        let bytes = std::fs::read(&sheet).unwrap();
        assert_eq!(&bytes[1..4], b"PNG", "{n}");
        assert!(bytes.len() < 300 * 1024, "{n}: {} bytes", bytes.len());
        let c: Value = serde_json::from_str(&std::fs::read_to_string(src.join("component.json")).unwrap()).unwrap();
        assert_eq!(v["examples"].as_array().unwrap().len(), c["examples"].as_array().unwrap().len(), "{n}");
        assert_eq!(c["license"], "CC0-1.0", "{n}");
        let hash = ok(&["component", "hash", src.to_str().unwrap()]);
        assert!(hash["files"].as_array().unwrap().iter().all(|f| !f.as_str().unwrap().starts_with("previews/")));
    }
}

#[test]
fn previews_are_deterministic_and_animate() {
    let src = library().join("components/pop-text");
    let (a, b) = (tmp("det-a"), tmp("det-b"));
    let v = ok(&["component", "preview", src.to_str().unwrap(), "-o", a.to_str().unwrap()]);
    ok(&["component", "preview", src.to_str().unwrap(), "-o", b.to_str().unwrap()]);
    assert_eq!(std::fs::read(a.join("sheet.png")).unwrap(), std::fs::read(b.join("sheet.png")).unwrap());
    let webp = std::fs::read(a.join("preview.webp")).unwrap();
    assert_eq!(&webp[0..4], b"RIFF");
    assert_eq!(&webp[8..12], b"WEBP");
    assert!(webp.windows(4).any(|w| w == b"ANIM"), "an animated WebP");
    assert_eq!(v["examples"][0]["times"].as_array().unwrap().len(), 5);
}

/// A project with the clean kit, pop-text and title-card vendored, and generated media.
fn project(name: &str, pops: &[(&str, &str, &str)]) -> PathBuf {
    let d = tmp(name);
    ffmpeg(&[
        "-f",
        "lavfi",
        "-i",
        "testsrc2=size=64x36:rate=10",
        "-t",
        "6",
        "-pix_fmt",
        "yuv420p",
        d.join("clip.mp4").to_str().unwrap(),
    ]);
    ffmpeg(&["-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono", "-t", "6", d.join("vo.wav").to_str().unwrap()]);
    for (pkg, from) in [("pop-text", "components"), ("title-card", "components"), ("clean", "kits")] {
        let dst = d.join("components").join(format!("{pkg}@1.0.0"));
        copy_dir(&library().join(from).join(pkg), &dst);
    }
    let overlays: Vec<Value> = pops
        .iter()
        .map(
            |(id, from, to)| json!({"id": id, "use": "pop-text@1.0.0", "from": from, "to": to, "props": {"text": *id}}),
        )
        .collect();
    let tl = json!({
        "schema": "timeline/0.1", "id": name, "rev": 0, "format": {"size": [320, 180], "fps": 10},
        "kits": ["clean@1.0.0"],
        "assets": {"proj:vo": {"path": "vo.wav"}, "proj:clip": {"path": "clip.mp4"}},
        "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": 2}, {"id": "b", "asset": "proj:vo", "in": 2, "out": 5}],
        "scenes": [{"id": "s", "shots": [
            {"id": "open", "at": "start:a", "picture": {"use": "title-card@1.0.0", "props": {"title": "Hi"}}},
            {"id": "body", "at": "start:b", "picture": {"layers": [{"id": "body/v", "type": "video", "asset": "proj:clip"}]}}
        ], "overlays": overlays}]
    });
    let path = d.join("timeline.json");
    std::fs::write(&path, serde_json::to_string_pretty(&tl).unwrap()).unwrap();
    path
}

#[test]
fn lint_reports_house_rule_violations() {
    let path = project("lint", &[("pop/a", "start:b+0.20", "start:b+0.80"), ("pop/b", "start:b+0.50", "start:b+1.40")]);
    let tl = path.to_str().unwrap();
    // Unlocked: validate says how to fix it; lock pins all three packages.
    let (code, v) = edit(&["validate", tl]);
    assert_eq!(code, 1);
    assert_eq!(v["errors"][0]["code"], "component_unlocked");
    let l = ok(&["component", "lock", tl]);
    assert_eq!(l["changed"], true);
    assert_eq!(l["packages"].as_object().unwrap().len(), 3);
    assert!(path.with_file_name("timeline.lock").exists());
    ok(&["validate", tl]);

    let (code, v) = edit(&["lint", tl, "--elements"]);
    assert_eq!(code, 1, "errors exit 1: {v:#}");
    assert_eq!(v["ok"], true);
    assert_eq!(v["boxes"], true);
    let msgs: Vec<&str> = v["violations"].as_array().unwrap().iter().map(|x| x["message"].as_str().unwrap()).collect();
    assert!(msgs.contains(&"pop overlaps pop at 0:02.5–0:02.8 (pop/a, pop/b); one pop at a time"), "{msgs:#?}");
    let one = v["violations"].as_array().unwrap().iter().find(|x| x["id"] == "one-pop-at-a-time").unwrap();
    assert_eq!(one["code"], "lint_no_overlap");
    assert_eq!(one["ptrs"], json!(["/scenes/0/overlays/0", "/scenes/0/overlays/1"]));
    assert_eq!(one["hint"], "end pop/a before 0:02.5 or start pop/b after 0:02.8");
    // The custom rule (the escape hatch) and the safe-area rule ran too; pops are inside the action-safe area.
    assert!(v["violations"].as_array().unwrap().iter().all(|x| x["id"] != "graphics-in-action-safe"), "{v:#}");
    let pop = v["checked"].as_array().unwrap().iter().find(|e| e["id"] == "pop/a").unwrap();
    assert_eq!(pop["kinds"], json!(["overlay", "pop", "pop-text"]));
    assert!(pop["box"].is_array(), "boxes measured: {pop}");

    // Fixed: one pop after the other, with room.
    let fixed =
        project("lint-ok", &[("pop/a", "start:b+0.20", "start:b+0.80"), ("pop/b", "start:b+1.60", "start:b+2.40")]);
    ok(&["component", "lock", fixed.to_str().unwrap()]);
    let (code, v) = edit(&["lint", fixed.to_str().unwrap()]);
    assert_eq!(code, 0, "{v:#}");
    assert_eq!(v["clean"], true);
    assert_eq!(v["rules"], 7);

    // A rule file overrides the kit's; a custom rule with a bad expression is a note, not a crash.
    let rules = path.with_file_name("rules.json");
    std::fs::write(
        &rules,
        r#"[{"id": "x", "rule": "custom", "expr": "elements.map((e) => ({message: e.id}))"},
        {"id": "y", "rule": "custom", "expr": "nope("}]"#,
    )
    .unwrap();
    let (code, v) = edit(&["lint", tl, "--rules", rules.to_str().unwrap()]);
    assert_eq!(code, 1);
    assert_eq!(v["violations"].as_array().unwrap().len(), 4, "the title, the shot and two pops: {v:#}");
    assert_eq!(v["notes"][0]["code"], "lint_rule");
}

#[test]
fn upgrade_one_element() {
    let path =
        project("upgrade", &[("pop/a", "start:b+0.20", "start:b+0.80"), ("pop/b", "start:a+0.20", "start:a+1.00")]);
    let tl = path.to_str().unwrap();
    ok(&["component", "lock", tl]);
    let comps = path.parent().unwrap().join("components");
    copy_dir(&comps.join("pop-text@1.0.0"), &comps.join("pop-text@1.1.0"));
    let cj = comps.join("pop-text@1.1.0/component.json");
    let text = std::fs::read_to_string(&cj).unwrap().replace("\"version\": \"1.0.0\"", "\"version\": \"1.1.0\"");
    std::fs::write(&cj, text.replace("\"default\": 132", "\"default\": 150")).unwrap();
    let (code, v) = edit(&["component", "upgrade", tl, "all", "--to", "1.1.0"]);
    assert_eq!((code, v["errors"][0]["code"].as_str()), (1, Some("upgrade_ambiguous")), "pop-text or title-card?");
    let (code, v) = edit(&["component", "upgrade", tl, "all", "--to", "1.1.0", "--component", "pop-text"]);
    assert_eq!(code, 0, "{v:#}");
    assert_eq!(v["patch"]["summary"], "Upgrade pop-text 1.0.0 → 1.1.0 on pop/a, pop/b");
    let p = ok(&["component", "upgrade", tl, "pop/a", "--to", "1.0.0", "--answers", "v2#3"]);
    assert_eq!(p["patch"]["answers"], json!(["v2#3"]));
    assert_eq!(p["affected"]["rerender"], 1, "only pop/a's unit: {p:#}");
    let log = ok(&["patch", "log", tl]);
    assert_eq!(log["patches"].as_array().unwrap().len(), 2);
    let lock: Value =
        serde_json::from_str(&std::fs::read_to_string(path.with_file_name("timeline.lock")).unwrap()).unwrap();
    assert!(lock["packages"]["pop-text@1.1.0"]["hash"].as_str().unwrap().starts_with("b3:"));
    let (code, v) = edit(&["component", "upgrade", tl, "pop/a", "--to", "1.0.0"]);
    assert_eq!((code, v["errors"][0]["code"].as_str()), (1, Some("upgrade_nothing")));
    let x = ok(&["expand", tl]);
    assert_eq!(x["timeline"]["scenes"][0]["overlays"][1]["layers"][0]["id"], "pop/b/text");
    assert_eq!(x["uses"].as_array().unwrap().len(), 3);
}
