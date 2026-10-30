//! Component backends (spec/components.md, "Backends"): a program draws a component's frames over `retake-raster/1`.
//! The example (editor/examples/backends/python-text) renders through the pipeline and the cache; the live path, lint
//! and previews read it too; editing the program re-renders its uses (and moving the project doesn't); a backend that
//! hangs, talks nonsense or crashes fails with a typed error; and a timeline can't write a raster layer itself.
//!
//! Needs `python3` on PATH (standard library only); skipped with a message without it.

use retake_render::composite::Frame;
use retake_render::engine::{Engine, Options};
use retake_render::expr::Js;
use retake_render::preview::{PreviewOptions, preview};
use retake_render::unit::{Graphics, compose};
use retake_render::{graphics::Painter, pipeline};
use retake_timeline::component::{LockMode, load_component, package_hash};
use retake_timeline::{Errors, Project};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

fn python() -> bool {
    Command::new("python3").arg("--version").output().is_ok_and(|o| o.status.success())
}

macro_rules! need_python {
    () => {
        if !python() {
            eprintln!("skipped: python3 isn't on PATH (component backends need it for this test)");
            return;
        }
    };
}

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("backend")
}

fn example() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../examples/backends/python-text")
}

fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let st = Command::new("ffmpeg")
            .args(["-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000"])
            .args(["-t", "4"])
            .arg(d.join("vo.wav"))
            .status()
            .expect("ffmpeg runs");
        assert!(st.success());
        d
    })
}

fn copy_dir(src: &Path, dst: &Path) {
    std::fs::create_dir_all(dst).unwrap();
    for e in std::fs::read_dir(src).unwrap() {
        let e = e.unwrap();
        let to = dst.join(e.file_name());
        if e.file_type().unwrap().is_dir() {
            copy_dir(&e.path(), &to);
        } else {
            std::fs::copy(e.path(), &to).unwrap();
        }
    }
}

/// A 320x180, 10 fps timeline: a plain blue shot, and `reference` as an overlay from 0.5 s to 1.8 s.
fn timeline(reference: &str, props: Value) -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "backend", "rev": 0,
        "format": {"size": [320, 180], "fps": 10},
        "assets": {"proj:vo": {"path": format!("{m}/vo.wav")}},
        "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": 2}],
        "scenes": [{"id": "s", "shots": [{"id": "bg", "at": "start:a", "picture": {"background": "#204060", "layers": []}}],
                    "overlays": [{"id": "pop", "use": reference, "from": "t:0.5", "to": "t:1.8", "props": props}]}]
    })
}

/// A project folder with the timeline and the packages (folder name, source) vendored under components/, pinned.
fn project(name: &str, doc: &Value, packages: &[(&str, &Path)]) -> PathBuf {
    let d = root().join("projects").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    for (dir, src) in packages {
        copy_dir(src, &d.join("components").join(dir));
    }
    let path = d.join("timeline.json");
    std::fs::write(&path, serde_json::to_string_pretty(doc).unwrap()).unwrap();
    pin(&path, LockMode::PinNew);
    path
}

fn pin(path: &Path, mode: LockMode) {
    retake_render::init();
    let p = Project::load_with(path, mode).unwrap_or_else(|e| panic!("{e}"));
    p.lock.write(&p.lock_path()).unwrap();
}

/// A tiny backend package: `component.json` with `backend` running `prog.py`.
fn package(name: &str, prog: &str, timeout: f64) -> PathBuf {
    let d = root().join("packages").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    let c = json!({"schema": "retake-component/0.1", "name": name, "version": "1.0.0", "license": "CC0-1.0",
                   "use_as": ["overlay"], "backend": {"command": ["python3", "prog.py"], "timeout": timeout}});
    std::fs::write(d.join("component.json"), c.to_string()).unwrap();
    std::fs::write(d.join("prog.py"), prog).unwrap();
    d
}

fn engine(path: &Path, cache: &str) -> Engine {
    Engine::open(path, Options { cache: Some(root().join(cache)), jobs: 2, ..Options::default() })
        .unwrap_or_else(|e| panic!("{e:#}"))
}

/// The composited frame `f` (output frames), graphics from the cache or drawn live.
fn frame_at(e: &Engine, f: i64, live: bool) -> Frame {
    let plans = e.plan().unwrap();
    let p = plans.iter().find(|p| p.span.f0 <= f && f < p.span.f1).unwrap();
    let mut js = Js::new().unwrap();
    let mut out = None;
    let i = f - p.span.f0;
    let faces = e.resources.faces(p.runs.iter().flat_map(|r| r.layers.iter()));
    let mut painter = Painter::new(e.resources.clone(), &faces).unwrap();
    let gfx = if live { Graphics::Live(&mut painter, &p.runs) } else { Graphics::Cache(&e.store) };
    compose(p, i, i + 1, gfx, &mut js, |fr| {
        out = Some(fr.clone());
        Ok(())
    })
    .unwrap();
    out.unwrap()
}

fn px(f: &Frame, x: u32, y: u32) -> [u8; 3] {
    let i = ((y * f.w + x) * 4) as usize;
    [f.px[i], f.px[i + 1], f.px[i + 2]]
}

fn raster_keys(e: &Engine) -> Vec<String> {
    e.plan()
        .unwrap()
        .iter()
        .flat_map(|p| p.runs.iter().filter(|r| r.layers.iter().any(|l| l.is_raster())))
        .map(|r| r.key.clone())
        .collect()
}

#[test]
fn the_example_renders_through_the_pipeline_and_the_cache() {
    need_python!();
    let ex = example();
    package_hash(&ex).unwrap();
    let path = project("example", &timeline("python-text@1.0.0", json!({"text": "Hi"})), &[("python-text@1.0.0", &ex)]);
    let p = Project::load(&path).unwrap();
    let raster = &p.doc["scenes"][0]["overlays"][0]["layers"][0];
    assert_eq!(raster["type"], "raster");
    assert_eq!(raster["id"], "pop/raster");
    assert_eq!(raster["component"], "python-text@1.0.0");
    assert_eq!(raster["backend"]["hash"], json!(p.lock.packages["python-text@1.0.0"].hash));

    let _ = std::fs::remove_dir_all(root().join("cache"));
    let e = engine(&path, "cache");
    let out = root().join("example.mp4");
    let r = pipeline::render(&e, Some(&out)).unwrap_or_else(|err| panic!("{err:#}"));
    assert_eq!(r.layers_rendered, 1, "the raster layer is one graphics run");
    // 1.3 s at 10 fps; the badge settles after 0.3 s, and settled frames repeat (`same`) without drawing.
    assert_eq!(r.graphics_frames, 13);
    assert!(r.graphics_drawn <= 5, "{} frames drawn", r.graphics_drawn);

    // The design canvas fits 320x180 at k = 1/6: "HI" in the 5x7 font at 3 px per font pixel is a 45x33 badge,
    // centred on x 160 and y 860/6; the H's top-left font pixel is at (138 + 6, 127 + 6).
    let f = frame_at(&e, 15, false);
    assert_eq!(px(&f, 145, 134), [0xff, 0xd2, 0x3f], "a letter pixel");
    assert_eq!(px(&f, 141, 143), [0x1b, 0x1d, 0x24], "the badge's padding");
    assert_eq!(px(&f, 10, 10), [0x20, 0x40, 0x60], "the shot outside the badge");
    assert_eq!(px(&f, 136, 143), [0x20, 0x40, 0x60], "just left of the badge");
    // Before the overlay: nothing.
    assert_eq!(px(&frame_at(&e, 3, false), 145, 134), [0x20, 0x40, 0x60]);
    // Halfway through the entrance it's lower and half faded.
    let mid = frame_at(&e, 6, false);
    assert_ne!(px(&mid, 145, 134), [0xff, 0xd2, 0x3f]);
    // Stills draw live, with a one-frame request: the same pixels.
    assert_eq!(frame_at(&e, 15, true), f);
    assert_eq!(frame_at(&e, 6, true), mid);

    // Again: everything from the cache.
    let again = pipeline::render(&e, Some(&root().join("example-again.mp4"))).unwrap();
    assert_eq!((again.rendered, again.layers_rendered), (0, 0));

    // Lint measures the badge's box through the backend too.
    let l = retake_render::lint::lint_timeline(&path, Some(vec![]), true).unwrap();
    assert!(l.boxes, "{:?}", l.notes);
    // (Measured at a quarter of the size, where the pixel font is coarser: only roughly the same box.)
    let b = l.checked.iter().find(|x| x.id == "pop").and_then(|x| x.bbox).expect("a box for the overlay");
    assert!((b[0] + b[2] / 2.0 - 160.0).abs() < 6.0 && (b[1] + b[3] / 2.0 - 143.0).abs() < 8.0, "{b:?}");
}

#[test]
fn editing_the_program_rerenders_and_moving_the_project_doesnt() {
    need_python!();
    let path =
        project("edit", &timeline("python-text@1.0.0", json!({"text": "Go"})), &[("python-text@1.0.0", &example())]);
    let before = raster_keys(&engine(&path, "cache-edit"));
    assert_eq!(before.len(), 1);

    // The same project in another folder: the backend's `dir` isn't in the key.
    let moved = root().join("projects").join("edit-moved");
    let _ = std::fs::remove_dir_all(&moved);
    copy_dir(path.parent().unwrap(), &moved);
    assert_eq!(raster_keys(&engine(&moved.join("timeline.json"), "cache-edit")), before);

    // A changed program is a changed package: refused until re-pinned, then a new key.
    let prog = path.parent().unwrap().join("components/python-text@1.0.0/draw.py");
    let mut text = std::fs::read_to_string(&prog).unwrap();
    text.push_str("\n# edited\n");
    std::fs::write(&prog, text).unwrap();
    let err = Project::load(&path).unwrap_err();
    assert!(err.0.iter().any(|e| e.code == "component_changed"), "{err}");
    pin(&path, LockMode::Repin(vec![]));
    let after = raster_keys(&engine(&path, "cache-edit"));
    assert_eq!(after.len(), 1);
    assert_ne!(after, before);
    // Props are in the key too.
    let mut doc: Value = serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
    doc["scenes"][0]["overlays"][0]["props"]["text"] = json!("Stop");
    std::fs::write(&path, doc.to_string()).unwrap();
    assert_ne!(raster_keys(&engine(&path, "cache-edit")), after);
}

/// Renders a timeline using the package and returns the error (it must fail).
fn render_err(name: &str, pkg: &Path) -> String {
    let path = project(name, &timeline(&format!("{name}@1.0.0"), json!({})), &[(&format!("{name}@1.0.0"), pkg)]);
    let e = engine(&path, &format!("cache-{name}"));
    let err = pipeline::render(&e, None).expect_err("the backend fails");
    format!("{err:#}")
}

#[test]
fn a_backend_that_hangs_talks_nonsense_or_crashes_fails_typed() {
    need_python!();
    let sleepy = package("sleepy", "import sys, time\nsys.stdin.readline()\ntime.sleep(30)\n", 1.0);
    let t0 = std::time::Instant::now();
    let e = render_err("sleepy", &sleepy);
    assert!(e.contains("backend_timeout"), "{e}");
    assert!(t0.elapsed().as_secs_f64() < 15.0, "killed at its timeout");

    let chatty = package("chatty", "import sys\nsys.stdin.readline()\nprint('hello')\n", 10.0);
    let e = render_err("chatty", &chatty);
    assert!(e.contains("backend_output") && e.contains("isn't JSON"), "{e}");

    let short = package(
        "short",
        "import sys\nsys.stdin.readline()\nsys.stdout.write('{\"box\": [0, 0, 4, 4]}\\n')\nsys.stdout.write('xx')\n",
        10.0,
    );
    let e = render_err("short", &short);
    assert!(e.contains("backend_output") && e.contains("64 bytes"), "{e}");

    let crashy =
        package("crashy", "import sys\nsys.stdin.readline()\nsys.stderr.write('boom\\n')\nsys.exit(3)\n", 10.0);
    let e = render_err("crashy", &crashy);
    assert!(e.contains("backend_failed") && e.contains("boom"), "{e}");

    let lost = package("lost", "", 10.0);
    std::fs::write(
        lost.join("component.json"),
        json!({"schema": "retake-component/0.1", "name": "lost", "version": "1.0.0", "use_as": ["overlay"],
               "backend": {"command": ["./no-such-program"]}})
        .to_string(),
    )
    .unwrap();
    let e = render_err("lost", &lost);
    assert!(e.contains("backend_failed") && e.contains("can't start"), "{e}");
}

#[test]
fn raster_layers_only_come_from_backends() {
    retake_render::init();
    // A timeline that writes one is refused before anything runs.
    let mut doc = timeline("python-text@1.0.0", json!({}));
    doc["scenes"][0]["overlays"] = json!([]);
    doc["scenes"][0]["shots"][0]["picture"]["layers"] = json!([{"id": "bg/r", "type": "raster",
        "component": "evil@1.0.0", "backend": {"command": ["sh", "-c", "touch pwned"], "dir": "/tmp",
        "hash": format!("b3:{}", "0".repeat(64))}}]);
    let d = root().join("projects").join("handmade");
    std::fs::create_dir_all(&d).unwrap();
    let err: Errors = Project::from_doc(&d.join("timeline.json"), doc).unwrap_err();
    assert_eq!(err.0[0].code, "raster_source", "{err}");
    assert_eq!(err.0[0].path.as_deref(), Some("/scenes/0/shots/0/picture/layers/0"));

    // So does a component template, and a component with both layers and a backend.
    let bad = package("template", "", 10.0);
    let c = json!({"schema": "retake-component/0.1", "name": "template", "version": "1.0.0", "use_as": ["overlay"],
                   "layers": [{"id": "r", "type": "raster"}]});
    std::fs::write(bad.join("component.json"), c.to_string()).unwrap();
    assert_eq!(load_component(&bad).unwrap_err().code, "raster_source");
    let c = json!({"schema": "retake-component/0.1", "name": "template", "version": "1.0.0", "use_as": ["overlay"],
                   "layers": [{"id": "t", "type": "text", "text": "x"}], "backend": {"command": ["python3", "x.py"]}});
    std::fs::write(bad.join("component.json"), c.to_string()).unwrap();
    assert_eq!(load_component(&bad).unwrap_err().code, "component_invalid");
    let c = json!({"schema": "retake-component/0.1", "name": "template", "version": "1.0.0", "use_as": ["overlay"],
                   "backend": {"command": ["../../bin/x"]}});
    std::fs::write(bad.join("component.json"), c.to_string()).unwrap();
    assert_eq!(load_component(&bad).unwrap_err().code, "component_invalid");
}

#[test]
fn previews_run_the_backend() {
    need_python!();
    let dir = root().join("preview").join("python-text@1.0.0");
    let _ = std::fs::remove_dir_all(&dir);
    copy_dir(&example(), &dir);
    let out = root().join("preview").join("out");
    let opts = PreviewOptions { out: Some(out.clone()), webp: false, work: Some(root().join("preview").join("work")) };
    preview(&dir, &opts).unwrap_or_else(|e| panic!("{e:#}"));
    assert!(out.join("sheet.png").exists());
}
