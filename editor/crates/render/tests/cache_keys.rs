//! The two cache keys. Unit key: change any input of a unit and its key changes; move a unit whose content is
//! relative and it doesn't. Layer key: position-independent, so a ripple never re-renders cached graphics. And the
//! unit description is hermetic: every field of it is in the key.

use retake_render::engine::{Engine, Options, UnitPlan};
use retake_timeline::canonical_json;
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("cache_keys")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

/// Tiny generated media, made once.
fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        ffmpeg(&["-f", "lavfi", "-i", "testsrc2=size=64x36:rate=10", "-t", "6", "-pix_fmt", "yuv420p", &p("clip.mp4")]);
        ffmpeg(&["-f", "lavfi", "-i", "testsrc=size=64x36:rate=10", "-t", "6", "-pix_fmt", "yuv420p", &p("other.mp4")]);
        std::fs::copy(d.join("clip.mp4"), d.join("clip-copy.mp4")).unwrap();
        ffmpeg(&["-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000", "-t", "8", &p("vo.wav")]);
        d
    })
}

fn timeline() -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "keys", "rev": 0,
        "format": {"size": [160, 90], "fps": 10},
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav")},
            "proj:clip": {"path": format!("{m}/clip.mp4")}
        },
        "spine": [
            {"id": "a", "asset": "proj:vo", "in": 0, "out": 2},
            {"id": "b", "asset": "proj:vo", "in": 2, "out": 4},
            {"id": "c", "asset": "proj:vo", "in": 4, "out": 6}
        ],
        "scenes": [{"id": "s", "shots": [
            {"id": "s1", "at": "start:a", "picture": {"layers": [
                {"id": "s1/v", "type": "video", "asset": "proj:clip", "in": 0.0},
                {"id": "s1/t", "type": "text", "text": "ONE", "size": 20, "transform": {"position": [80, 20]}}]}},
            {"id": "s2", "at": "start:b", "picture": {"layers": [
                {"id": "s2/bg", "type": "video", "asset": "proj:clip", "loop": true, "clock": "timeline"},
                {"id": "s2/t", "type": "text", "text": "TWO", "size": 20}]}},
            {"id": "s3", "at": "start:c", "picture": {"layers": [
                {"id": "s3/v", "type": "video", "asset": "proj:clip", "in": 1.0},
                {"id": "s3/t", "type": "text", "text": {"expr": "'T' + Math.floor(t * 10)"}, "size": 20}]}}
        ], "overlays": [
            {"id": "o", "from": "start:b+0.20", "to": "start:b+1.00", "layers": [
                {"id": "o/t", "type": "text", "text": "POP", "size": 30,
                 "transform": {"scale": {"keys": [{"t": 0, "v": 1.3, "ease": "out-back"}, {"t": 0.2, "v": 1}]}}}]}
        ]}]
    })
}

struct Keys {
    units: Vec<(String, String)>,
    runs: Vec<(String, Vec<String>)>,
    plans: Vec<UnitPlan>,
}

fn keys(name: &str, doc: &Value, opts: Options) -> Keys {
    let d = root().join("tl").join(name);
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let opts = Options { cache: Some(root().join("cache")), ..opts };
    let e = Engine::open(&path, opts).unwrap_or_else(|err| panic!("{name}: {err:#}"));
    let plans = e.plan().unwrap();
    Keys {
        units: plans.iter().map(|p| (p.span.source.clone(), p.key.clone())).collect(),
        runs: plans.iter().map(|p| (p.span.source.clone(), p.runs.iter().map(|r| r.key.clone()).collect())).collect(),
        plans,
    }
}

fn unit<'a>(k: &'a Keys, source: &str) -> &'a str {
    &k.units.iter().find(|(s, _)| s == source).unwrap().1
}

fn runs<'a>(k: &'a Keys, source: &str) -> &'a [String] {
    &k.runs.iter().find(|(s, _)| s == source).unwrap().1
}

#[test]
fn every_input_changes_the_unit_key() {
    let base = keys("base", &timeline(), Options::default());
    assert_eq!(base.units.len(), 3);
    type Mutation = (&'static str, fn(&mut Value), &'static [&'static str]);
    let cases: &[Mutation] = &[
        ("overlay text", |d| d["scenes"][0]["overlays"][0]["layers"][0]["text"] = json!("BOP"), &["s2"]),
        ("overlay timing", |d| d["scenes"][0]["overlays"][0]["to"] = json!("start:b+1.10"), &["s2"]),
        (
            "keyframe ease",
            |d| d["scenes"][0]["overlays"][0]["layers"][0]["transform"]["scale"]["keys"][0]["ease"] = json!("linear"),
            &["s2"],
        ),
        ("video in", |d| d["scenes"][0]["shots"][0]["picture"]["layers"][0]["in"] = json!(0.5), &["s1"]),
        (
            "text position",
            |d| d["scenes"][0]["shots"][0]["picture"]["layers"][1]["transform"]["position"] = json!([80, 21]),
            &["s1"],
        ),
        (
            "expression",
            |d| {
                d["scenes"][0]["shots"][2]["picture"]["layers"][1]["text"] = json!({"expr": "'T' + Math.round(t * 10)"})
            },
            &["s3"],
        ),
        (
            "layer order",
            |d| {
                let l = d["scenes"][0]["shots"][0]["picture"]["layers"].as_array_mut().unwrap();
                l.swap(0, 1);
            },
            &["s1"],
        ),
        ("background", |d| d["format"]["background"] = json!("#101010"), &["s1", "s2", "s3"]),
        ("video fit", |d| d["scenes"][0]["shots"][2]["picture"]["layers"][0]["fit"] = json!("cover"), &["s3"]),
        ("font weight", |d| d["scenes"][0]["shots"][0]["picture"]["layers"][1]["weight"] = json!(500), &["s1"]),
        (
            "asset content",
            |d| {
                let m = media().to_string_lossy().into_owned();
                d["assets"]["proj:clip"]["path"] = json!(format!("{m}/other.mp4"));
            },
            &["s1", "s2", "s3"],
        ),
    ];
    for (name, f, changed) in cases {
        let mut d = timeline();
        f(&mut d);
        let k = keys(&format!("m-{}", name.replace(' ', "-")), &d, Options::default());
        for s in ["s1", "s2", "s3"] {
            let same = unit(&k, s) == unit(&base, s);
            assert_eq!(
                same,
                !changed.contains(&s),
                "{name}: unit {s} key should {}change",
                if same { "" } else { "not " }
            );
        }
    }
    // Same bytes under another path or another asset id: same keys (content, not names, is the input).
    let mut d = timeline();
    let m = media().to_string_lossy().into_owned();
    d["assets"]["proj:clip"]["path"] = json!(format!("{m}/clip-copy.mp4"));
    let k = keys("copy", &d, Options::default());
    assert_eq!(k.units, base.units, "a copy of the same file renders the same");
    // Renaming a layer doesn't re-render either.
    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["layers"][0]["id"] = json!("o/renamed");
    assert_eq!(keys("rename", &d, Options::default()).units, base.units);
    // Encoder and size settings are inputs too.
    let draft = keys("draft", &timeline(), Options { draft: true, ..Options::default() });
    for s in ["s1", "s2", "s3"] {
        assert_ne!(unit(&draft, s), unit(&base, s));
    }
    let scaled = keys("scaled", &timeline(), Options { scale: Some(0.5), ..Options::default() });
    assert_ne!(unit(&scaled, "s1"), unit(&base, "s1"));
}

#[test]
fn a_ripple_moves_units_without_touching_layer_keys() {
    let base = keys("ripple0", &timeline(), Options::default());
    // Lengthen spine item a by 0.5 s: s1 grows, s2 and s3 move 0.5 s later unchanged.
    let mut d = timeline();
    d["spine"][0]["out"] = json!(2.5);
    let k = keys("ripple1", &d, Options::default());
    let f0 = |k: &Keys, s: &str| k.plans.iter().find(|p| p.span.source == s).unwrap().span.f0;
    assert_eq!(f0(&k, "s3"), f0(&base, "s3") + 5);
    // s1 is longer: new key, and its text run covers more frames: new layer key.
    assert_ne!(unit(&k, "s1"), unit(&base, "s1"));
    // s3 only moved and its content is relative to it: the same key, so its segment is reused.
    assert_eq!(unit(&k, "s3"), unit(&base, "s3"));
    assert_eq!(runs(&k, "s3"), runs(&base, "s3"));
    // s2 reads a looping background on the timeline clock: moving it changes what it shows, so its unit key
    // changes (the v7 topo bug can't happen) while its graphics (text and the overlay) keep their layer keys.
    assert_ne!(unit(&k, "s2"), unit(&base, "s2"));
    assert_eq!(runs(&k, "s2"), runs(&base, "s2"));
    assert_eq!(runs(&k, "s2").len(), 2, "the shot's text and the overlay");
}

/// Every leaf of the description the compositor gets is part of the unit key.
#[test]
fn unit_descriptions_are_hermetic() {
    let base = keys("hermetic", &timeline(), Options::default());
    for p in &base.plans {
        let v = serde_json::to_value(&p.desc).unwrap();
        assert_eq!(p.key, blake3::hash(canonical_json(&v).as_bytes()).to_hex().to_string());
        // Sources are identified by content hash, not by path.
        let s = v.to_string();
        assert!(!s.contains(media().to_string_lossy().as_ref()), "no paths in the key");
        assert!(s.contains(&p.desc.encode.ffmpeg), "the ffmpeg build is an input");
        let mut leaves = vec![];
        collect_leaves(&v, String::new(), &mut leaves);
        assert!(leaves.len() > 20, "{}", leaves.len());
        for ptr in leaves {
            let mut w = v.clone();
            let leaf = w.pointer_mut(&ptr).unwrap();
            *leaf = match leaf {
                Value::Number(n) => json!(n.as_f64().unwrap() + 1.0),
                Value::String(s) => json!(format!("{s}x")),
                Value::Bool(b) => json!(!*b),
                _ => json!("changed"),
            };
            let k = blake3::hash(canonical_json(&w).as_bytes()).to_hex().to_string();
            assert_ne!(k, p.key, "changing {ptr} must change the key");
        }
    }
}

fn collect_leaves(v: &Value, ptr: String, out: &mut Vec<String>) {
    match v {
        Value::Object(m) => m.iter().for_each(|(k, c)| collect_leaves(c, format!("{ptr}/{k}"), out)),
        Value::Array(a) => a.iter().enumerate().for_each(|(i, c)| collect_leaves(c, format!("{ptr}/{i}"), out)),
        _ => out.push(ptr),
    }
}
