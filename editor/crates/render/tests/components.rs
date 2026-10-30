//! Components from the starter library (library/, spec/components.md): expansion with kit tokens and the canvas fit,
//! the lockfile, typed errors, and hermetic cache keys: changing a component re-renders only the elements whose
//! expansion changed, and upgrading is per element.

use retake_render::engine::{Engine, Options};
use retake_timeline::component::{LockMode, package_hash};
use retake_timeline::{Project, patch};
use serde_json::{Value, json};
use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("components")
}

fn library() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../library")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        ffmpeg(&["-f", "lavfi", "-i", "testsrc2=size=64x36:rate=10", "-t", "8", "-pix_fmt", "yuv420p", &p("clip.mp4")]);
        ffmpeg(&["-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000", "-t", "8", &p("vo.wav")]);
        let words = json!({"words": [
            {"w": " Rinse", "s": 0.2, "e": 0.5}, {"w": " the", "s": 0.55, "e": 0.65}, {"w": " filter", "s": 0.7, "e": 1.1},
            {"w": " first.", "s": 1.15, "e": 1.6}]});
        std::fs::write(d.join("vo.words.json"), words.to_string()).unwrap();
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

/// A project folder `name` with the timeline and the given library packages vendored as `components/<name>@1.0.0`.
fn project(name: &str, doc: &Value, packages: &[&str]) -> PathBuf {
    let d = root().join("projects").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    for p in packages {
        let src = if *p == "clean" { library().join("kits").join(p) } else { library().join("components").join(p) };
        copy_dir(&src, &d.join("components").join(format!("{p}@1.0.0")));
        let _ = std::fs::remove_dir_all(d.join("components").join(format!("{p}@1.0.0")).join("previews"));
    }
    let path = d.join("timeline.json");
    std::fs::write(&path, serde_json::to_string_pretty(doc).unwrap()).unwrap();
    path
}

fn timeline() -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "lib", "rev": 0,
        "format": {"size": [320, 180], "fps": 10},
        "kits": ["clean@1.0.0"],
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav"), "words": format!("{m}/vo.words.json")},
            "proj:clip": {"path": format!("{m}/clip.mp4")}
        },
        "spine": [
            {"id": "a", "asset": "proj:vo", "in": 0, "out": 2},
            {"id": "b", "asset": "proj:vo", "in": 2, "out": 4},
            {"id": "c", "asset": "proj:vo", "in": 4, "out": 6}
        ],
        "scenes": [{"id": "s", "shots": [
            {"id": "open", "at": "start:a", "picture": {"use": "title-card@1.0.0", "props": {"title": "Hello"}}},
            {"id": "mid", "at": "start:b", "picture": {"layers": [{"id": "mid/v", "type": "video", "asset": "proj:clip"}]}},
            {"id": "end", "at": "start:c", "picture": {"background": "kit:color.surface", "layers": [
                {"id": "end/t", "type": "text", "text": "BYE", "size": 20, "fill": "kit:color.accent"}]}}
        ], "overlays": [
            {"id": "cap", "use": "caption@1.0.0", "from": "start:a", "to": "end:a", "props": {"words": "$words:a"}},
            {"id": "pop/a", "use": "pop-text@1.0.0", "from": "start:b+0.20", "to": "start:b+1.00", "z": 2,
             "props": {"text": "One"}},
            {"id": "pop/b", "use": "pop-text@1.0.0", "from": "start:c+0.20", "to": "start:c+1.00", "z": 2,
             "props": {"text": "Two", "enter": "rise"}}
        ]}]
    })
}

const PACKAGES: &[&str] = &["title-card", "pop-text", "caption", "clean"];

fn pinned(path: &Path) -> Project {
    retake_render::init();
    let p = Project::load_with(path, LockMode::PinNew).unwrap_or_else(|e| panic!("{e}"));
    if p.lock_changed {
        p.lock.write(&p.lock_path()).unwrap();
    }
    Project::load(path).unwrap_or_else(|e| panic!("{e}"))
}

fn unit_keys(path: &Path) -> BTreeMap<String, String> {
    let e = Engine::open(path, Options { cache: Some(root().join("cache")), ..Options::default() })
        .unwrap_or_else(|err| panic!("{err:#}"));
    e.plan().unwrap().iter().map(|p| (format!("{}@{}", p.span.source, p.span.f0), p.key.clone())).collect()
}

fn codes(e: &retake_timeline::Errors) -> Vec<String> {
    e.0.iter().map(|x| x.code.clone()).collect()
}

#[test]
fn expansion_tokens_fit_and_words() {
    retake_render::init();
    let path = project("expand", &timeline(), PACKAGES);
    // Nothing is locked yet: every package says so, at its use.
    let err = Project::load(&path).unwrap_err();
    assert!(err.0.iter().all(|e| e.code == "component_unlocked"), "{err}");
    let ptrs: Vec<String> = err.0.iter().filter_map(|e| e.path.clone()).collect();
    assert!(
        ptrs.contains(&"/kits/0".to_string()) && ptrs.contains(&"/scenes/0/overlays/1/use".to_string()),
        "{ptrs:?}"
    );

    let p = pinned(&path);
    assert_eq!(p.lock.packages.len(), 4);
    let lock = &p.lock.packages["pop-text@1.0.0"];
    assert_eq!(lock.kind, "component");
    assert_eq!(lock.path.as_deref(), Some("components/pop-text@1.0.0"));
    assert_eq!(lock.hash, package_hash(&path.parent().unwrap().join("components/pop-text@1.0.0")).unwrap());
    assert_eq!(p.lock.packages["clean@1.0.0"].kind, "kit");
    assert_eq!(p.uses.len(), 4);
    assert_eq!(p.kits.len(), 1);

    // The source is untouched; the expansion has layers, kit tokens and the fit to 320x180 (k = 1/6).
    assert!(p.src["scenes"][0]["overlays"][1].get("layers").is_none());
    let pic = &p.doc["scenes"][0]["shots"][0]["picture"];
    assert_eq!(pic["background"], "#15171c", "kit:color.bg from the clean kit");
    let ids: Vec<&str> = pic["layers"].as_array().unwrap().iter().map(|l| l["id"].as_str().unwrap()).collect();
    assert_eq!(ids, ["open/glow", "open/title", "open/rule", "open/subtitle"]);
    let pop = &p.doc["scenes"][0]["overlays"][1];
    let text = &pop["layers"][0];
    assert_eq!(text["text"], "One");
    assert_eq!(text["fill"], "#ffd23f");
    assert!((text["size"].as_f64().unwrap() - 22.0).abs() < 1e-9, "132 / 6: {}", text["size"]);
    assert_eq!(text["transform"]["opacity"]["keys"][2]["t"], "end:pop/a-0.14");
    assert_eq!(pop["props"]["size"], 132, "props stay in design pixels");
    assert_eq!(p.doc["scenes"][0]["shots"][2]["picture"]["background"], "#20232b", "tokens in plain layers too");
    assert_eq!(p.doc["scenes"][0]["shots"][2]["picture"]["layers"][0]["fill"], "#ffd23f");
    // A rise has its position keys from the $expr.
    let rise = &p.doc["scenes"][0]["overlays"][2]["layers"][0]["transform"]["position"]["keys"];
    assert_eq!(rise.as_array().unwrap().len(), 2);

    // $words:a becomes the spine item's words on the overlay's clock.
    let r = retake_timeline::resolve(&p).unwrap();
    let cap = r.overlays.iter().find(|o| o.id == "cap").unwrap();
    assert_eq!(cap.comp.props["words"][0], json!({"w": "Rinse", "s": 0.2, "e": 0.5}));
    assert_eq!(cap.comp.props["words"].as_array().unwrap().len(), 4);
    assert_eq!(cap.comp.props["words_per_line"], 4, "kit:caption.words_per_line");

    // It renders a still, and the manifest names the components.
    let e = Engine::open(&path, Options { cache: Some(root().join("cache")), ..Options::default() }).unwrap();
    let shots = retake_render::stills(&e, &[0.5, 2.5], &root().join("projects/expand/frames")).unwrap();
    assert!(shots.iter().all(|(_, f)| f.exists()));
    let rep = retake_render::render(&e, None).unwrap();
    let m = serde_json::to_value(&rep.manifest).unwrap();
    assert_eq!(retake_timeline::schema::check_manifest(&m), vec![]);
    let uses: Vec<(String, String)> = rep
        .manifest
        .units
        .iter()
        .flat_map(|u| u.elements.iter())
        .filter_map(|el| el.use_.clone().map(|x| (el.id.clone(), x)))
        .collect();
    assert!(uses.contains(&("pop/a".into(), "pop-text@1.0.0".into())), "{uses:?}");
    assert!(uses.contains(&("open".into(), "title-card@1.0.0".into())), "{uses:?}");
}

#[test]
fn a_component_change_rerenders_only_its_elements() {
    let path = project("hermetic", &timeline(), PACKAGES);
    pinned(&path);
    let before = unit_keys(&path);
    let dir = path.parent().unwrap().join("components/pop-text@1.0.0");

    // A file that doesn't change the expansion: the hash changes, so it must be re-pinned, but nothing re-renders.
    std::fs::write(dir.join("NOTES.md"), "house notes\n").unwrap();
    let err = Project::load(&path).unwrap_err();
    assert_eq!(codes(&err), ["component_changed"], "{err}");
    assert!(err.0[0].hint.as_deref().unwrap().contains("--update pop-text@1.0.0"));
    let p = Project::load_with(&path, LockMode::Repin(vec!["pop-text@1.0.0".into()])).unwrap();
    assert!(p.lock_changed);
    p.lock.write(&p.lock_path()).unwrap();
    assert_eq!(unit_keys(&path), before, "same expansion, same keys");

    // A change to how it draws: only the units with a pop change.
    let cj = dir.join("component.json");
    let text = std::fs::read_to_string(&cj).unwrap().replace("\"default\": -3", "\"default\": 6");
    std::fs::write(&cj, text).unwrap();
    let p = Project::load_with(&path, LockMode::Repin(vec![])).unwrap();
    p.lock.write(&p.lock_path()).unwrap();
    let after = unit_keys(&path);
    let changed: Vec<&String> = after.iter().filter(|(k, v)| before.get(*k) != Some(v)).map(|(k, _)| k).collect();
    assert_eq!(changed.len(), 2, "the two pops' units: {changed:?}");
    assert!(changed.iter().all(|k| k.starts_with("mid@") || k.starts_with("end@")), "{changed:?}");
    assert_eq!(after.get("open@0"), before.get("open@0"), "the title card's unit is untouched");
}

#[test]
fn upgrading_is_per_element() {
    let path = project("upgrade", &timeline(), PACKAGES);
    let p = pinned(&path);
    let before = unit_keys(&path);
    // pop-text@1.1.0: a new version next to the old one, with another default stroke.
    let comps = path.parent().unwrap().join("components");
    copy_dir(&comps.join("pop-text@1.0.0"), &comps.join("pop-text@1.1.0"));
    let cj = comps.join("pop-text@1.1.0/component.json");
    let text = std::fs::read_to_string(&cj)
        .unwrap()
        .replace("\"version\": \"1.0.0\"", "\"version\": \"1.1.0\"")
        .replace("\"default\": \"kit:color.ink\"", "\"default\": \"#3a0ca3\"");
    std::fs::write(&cj, text).unwrap();
    // What `retake-edit component upgrade <timeline> pop/a --to 1.1.0` writes.
    let pj = json!({"base": 0, "answers": ["v1#4"], "summary": "Upgrade pop-text 1.0.0 → 1.1.0 on pop/a",
        "ops": [{"op": "set", "id": "pop/a", "path": "use", "value": "pop-text@1.1.0"}]});
    let a = patch::apply(&p, &pj).unwrap_or_else(|e| panic!("{e}"));
    assert!(a.project.lock_changed, "the patch pins the new version");
    patch::commit(&a).unwrap();
    let p2 = Project::load(&path).unwrap();
    assert!(p2.lock.packages.contains_key("pop-text@1.1.0") && p2.lock.packages.contains_key("pop-text@1.0.0"));
    assert_eq!(p2.src["scenes"][0]["overlays"][1]["use"], "pop-text@1.1.0");
    assert_eq!(p2.src["scenes"][0]["overlays"][2]["use"], "pop-text@1.0.0", "pop/b stays pinned");
    let after = unit_keys(&path);
    let changed: Vec<&String> = after.iter().filter(|(k, v)| before.get(*k) != Some(v)).map(|(k, _)| k).collect();
    assert_eq!(changed, vec!["mid@20"], "only pop/a's unit");
    // The patch log keeps the upgrade, and a revert brings 1.0.0 back.
    let rp = patch::revert_patch(&p2, "p1").unwrap();
    let r = patch::apply(&p2, &rp).unwrap();
    assert_eq!(r.project.src["scenes"][0]["overlays"][1]["use"], "pop-text@1.0.0");
}

#[test]
fn errors_are_typed_and_actionable() {
    retake_render::init();
    let path = project("errors", &timeline(), PACKAGES);
    pinned(&path);
    let p = Project::load(&path).unwrap();
    let try_doc = |f: &dyn Fn(&mut Value)| {
        let mut d = p.src.clone();
        f(&mut d);
        Project::open(&path, d, LockMode::PinNew, Some(p.lock.clone())).unwrap_err()
    };
    let e = try_doc(&|d| d["scenes"][0]["overlays"][1]["use"] = json!("pop-txt@1.0.0"));
    assert_eq!(codes(&e), ["component_not_found"]);
    assert_eq!(e.0[0].suggestions, vec!["pop-text@1.0.0".to_string()]);
    let e = try_doc(&|d| d["scenes"][0]["overlays"][1]["use"] = json!("pop-text@2.0.0"));
    assert_eq!(e.0[0].hint.as_deref(), Some("versions here: pop-text@1.0.0"));
    let e = try_doc(&|d| d["scenes"][0]["overlays"][1]["props"] = json!({"txt": "x"}));
    assert!(codes(&e).iter().all(|c| c == "component_props"), "{e}");
    assert!(e.0.iter().any(|x| x.message.contains("\"text\" is a required property")), "{e}");
    let e = try_doc(&|d| d["scenes"][0]["overlays"][1]["props"]["size"] = json!(999));
    assert_eq!(e.0[0].path.as_deref(), Some("/scenes/0/overlays/1/props/size"));
    assert!(e.0[0].hint.as_deref().unwrap().starts_with("size: Letter size"), "{:?}", e.0[0].hint);
    let e = try_doc(&|d| {
        d["scenes"][0]["overlays"][1] =
            json!({"id": "t", "use": "title-card@1.0.0", "from": "t:0", "to": "t:1", "props": {"title": "x"}})
    });
    assert_eq!(codes(&e), ["component_target"]);
    let e = try_doc(&|d| d["scenes"][0]["shots"][2]["picture"]["background"] = json!("kit:color.surfase"));
    assert_eq!(codes(&e), ["kit_token_unknown"]);
    assert_eq!(e.0[0].suggestions[0], "kit:color.surface", "best first");
    assert_eq!(e.0[0].path.as_deref(), Some("/scenes/0/shots/2/picture/background"));
    // A patch on a layer a component made points at the props.
    let pj = json!({"base": 0, "answers": [], "summary": "x",
        "ops": [{"op": "set", "id": "pop/a/text", "path": "fill", "value": "#ff0000"}]});
    let Err(e) = patch::apply(&p, &pj) else { panic!("a component's layer isn't in the source") };
    assert_eq!(codes(&e), ["patch_target_not_found"]);
    assert!(e.0[0].hint.as_deref().unwrap().contains("set `props` on `pop/a`"), "{e}");
}
