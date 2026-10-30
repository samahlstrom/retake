//! Anchor resolver, ripples, inserts, units and validation errors, on small in-memory timelines.

use retake_timeline::resolve::{UnitKind, check_refs};
use retake_timeline::{Project, resolve, validate};
use serde_json::{Value, json};
use std::path::PathBuf;

/// A scratch folder with a words file. Media files are never opened by the resolver, so they're empty.
fn scratch(name: &str) -> PathBuf {
    let d = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("resolve").join(name);
    std::fs::create_dir_all(&d).unwrap();
    // "Welcome to the tool shop. Everyone at your rank, up to Everest, buys the same stuff. The same stuff."
    let words = [
        (" Welcome", 0.50, 0.90),
        (" to", 0.95, 1.05),
        (" the", 1.10, 1.20),
        (" tool", 1.25, 1.50),
        (" shop.", 1.55, 1.90),
        (" Everyone", 3.00, 3.40),
        (" at", 3.45, 3.55),
        (" your", 3.60, 3.75),
        (" rank,", 3.80, 4.10),
        (" up", 4.30, 4.40),
        (" to", 4.45, 4.55),
        (" Everest,", 4.60, 5.10),
        (" buys", 5.20, 5.45),
        (" the", 5.50, 5.60),
        (" same", 5.65, 5.90),
        (" stuff.", 5.95, 6.30),
        (" The", 7.00, 7.10),
        (" same", 7.15, 7.40),
        (" stuff.", 7.45, 7.80),
    ];
    let ws: Vec<Value> = words.iter().map(|(w, s, e)| json!({"w": w, "s": s, "e": e, "p": 0.9})).collect();
    let doc = json!({"files": {"vo.wav": {"size": 0, "model": "hand", "duration": 8.0, "words": ws}}});
    std::fs::write(d.join("vo.words.json"), doc.to_string()).unwrap();
    for f in ["vo.wav", "clip.mp4", "meme.mp4"] {
        std::fs::write(d.join(f), b"").unwrap();
    }
    d
}

fn timeline() -> Value {
    json!({
        "schema": "timeline/0.1", "id": "t", "rev": 0,
        "format": {"size": [640, 360], "fps": 30},
        "assets": {
            "proj:vo": {"path": "vo.wav", "words": "vo.words.json"},
            "proj:clip": {"path": "clip.mp4"},
            "proj:meme": {"path": "meme.mp4"}
        },
        "spine": [
            {"id": "a", "asset": "proj:vo", "in": 0.4, "out": 2.0},
            {"id": "b", "asset": "proj:vo", "in": 2.9, "out": 6.4, "gap_before": 0.5},
            {"id": "c", "asset": "proj:vo", "in": 6.9, "out": 7.9}
        ],
        "scenes": [
            {"id": "intro", "shots": [
                {"id": "intro/1", "at": "start:a", "picture": {"layers": [
                    {"id": "intro/1/v", "type": "video", "asset": "proj:clip", "in": 10.0}]}},
                {"id": "intro/2", "at": "word:b/Everyone-0.10", "picture": {"background": "#202020", "layers": [
                    {"id": "intro/2/title", "type": "text", "text": "RANKS",
                     "transform": {"scale": {"keys": [
                        {"t": 0, "v": 1.2, "ease": "out-back"},
                        {"t": "word:b/Everest", "v": 1.0}]}}}]}}
            ],
            "overlays": [
                {"id": "pop", "from": "word:b/Everest", "to": "word:b/same@end+0.20", "layers": [
                    {"id": "pop/t", "type": "text", "text": "UP TO EVEREST",
                     "transform": {"opacity": {"keys": [{"t": 0, "v": 0}, {"t": "word:b/buys", "v": 1}]}}}]}
            ]},
            {"id": "outro", "shots": [
                {"id": "outro/1", "at": "start:c", "picture": {"layers": [
                    {"id": "outro/1/v", "type": "video", "asset": "proj:clip", "sync": {"src": 50.0, "to": "word:c/same"}}]}}
            ]}
        ],
        "audio": [{"id": "bed", "asset": "proj:clip", "from": "start:a", "to": "end:b+0.50"}]
    })
}

fn project(name: &str, doc: Value) -> Project {
    let d = scratch(name);
    Project::from_doc(&d.join("timeline.json"), doc).unwrap()
}

fn near(a: f64, b: f64) -> bool {
    (a - b).abs() < 1e-6
}

#[test]
fn spine_layout_and_anchors() {
    let p = project("basic", timeline());
    assert_eq!(check_refs(&p), vec![]);
    let r = resolve(&p).unwrap();
    // a: 0-1.6, gap 0.5, b: 2.1-5.6, c: 5.6-6.6
    assert!(near(r.spine[0].end, 1.6));
    assert!(near(r.spine[1].start, 2.1) && near(r.spine[1].end, 5.6));
    assert!(near(r.spine[2].start, 5.6) && near(r.end, 6.6));
    // "Everyone" is at 3.0 in the asset, 0.1 into b (in 2.9), so 2.2 s out; minus 0.10.
    let s2 = r.shot("intro/2").unwrap();
    assert!(near(s2.start, 2.1));
    assert!(near(r.shot("intro/1").unwrap().end, 2.1), "a shot ends where the next starts");
    // pop: Everest at 4.6 → 2.1 + 1.7 = 3.8; to = first "same" end (5.9 → 5.1) + 0.2 = 5.3
    let pop = &r.overlays[0];
    assert!(near(pop.from, 3.8) && near(pop.to, 5.3), "{} {}", pop.from, pop.to);
    // Keyframe anchors become composition seconds: buys (5.2 → 4.4) - 3.8 = 0.6.
    assert_eq!(pop.comp.layers_json[0]["transform"]["opacity"]["keys"][1]["t"], json!(0.6));
    // Title keyframe: Everest 3.8 - shot start 2.1 = 1.7.
    assert_eq!(s2.comp.layers_json[0]["transform"]["scale"]["keys"][1]["t"], json!(1.7));
    // sync: source 50.0 plays at word c/same (7.15 → 5.6 + 0.25 = 5.85), 0.25 s into the shot: in = 49.75.
    assert_eq!(r.shot("outro/1").unwrap().comp.layers_json[0]["in"], json!(49.75));
    assert!(near(r.audio[0].to, 6.1));
    // Words land on the output clock.
    let w = r.words.iter().find(|w| w.text == "Everest,").unwrap();
    assert!(near(w.t0, 3.8) && w.spine == "b");
}

#[test]
fn occurrence_and_end_and_prefix() {
    // Let b cover both "same stuff." phrases.
    let widen = |d: &mut Value| {
        d["spine"][1]["out"] = json!(7.9);
        d["spine"][2] = json!({"id": "c", "asset": "proj:vo", "in": 7.9, "out": 8.5});
        d["scenes"][1]["shots"][0]["picture"]["layers"][0].as_object_mut().unwrap().remove("sync");
        d["scenes"][0]["overlays"][0]["layers"][0]["transform"]["opacity"]["keys"][1]["t"] = json!(0.5);
    };
    let mut d = timeline();
    widen(&mut d);
    d["scenes"][0]["overlays"][0]["from"] = json!("word:b/same#2");
    d["scenes"][0]["overlays"][0]["to"] = json!("word:b/stuf*#2@end");
    let r = resolve(&project("occ", d)).unwrap();
    // b starts at 2.1 with in 2.9: the second "same" (7.15) is at 6.35, the second "stuff." ends (7.8) at 7.0.
    assert!(
        near(r.overlays[0].from, 6.35) && near(r.overlays[0].to, 7.0),
        "{:?}",
        (r.overlays[0].from, r.overlays[0].to)
    );
    let mut d = timeline();
    widen(&mut d);
    d["scenes"][0]["overlays"][0]["from"] = json!("word:b/same#3");
    let e = resolve(&project("occ2", d)).unwrap_err();
    assert_eq!(e[0].code, "anchor_word_occurrence");
    assert_eq!(e[0].hint.as_deref(), Some("use #1 to #2"));
    assert_eq!(e[0].path.as_deref(), Some("/scenes/0/overlays/0/from"));
}

#[test]
fn word_not_found_suggests() {
    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("word:b/Everist");
    let e = resolve(&project("sugg", d)).unwrap_err();
    assert_eq!(e[0].code, "anchor_word_not_found");
    assert_eq!(e[0].hint.as_deref(), Some("did you mean 'Everest,'?"));
    assert!(e[0].message.contains("word:b/Everist"));
    // A word that's only in another spine item says where it is.
    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("word:a/Everest");
    let e = resolve(&project("sugg2", d)).unwrap_err();
    assert_eq!(e[0].hint.as_deref(), Some("it is in spine item b"));
}

#[test]
fn unknown_ids_cycles_and_kinds() {
    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("start:intro/3");
    let e = resolve(&project("unk", d)).unwrap_err();
    assert_eq!(e[0].code, "anchor_unknown_id");
    assert!(e[0].suggestions.contains(&"intro/1".to_string()) || e[0].suggestions.contains(&"intro/2".to_string()));

    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("start:bed");
    d["audio"][0]["from"] = json!("start:pop");
    let e = resolve(&project("cycle", d)).unwrap_err();
    assert!(e.iter().any(|e| e.code == "anchor_cycle"), "{e:?}");

    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("word:intro/1/hello");
    let e = resolve(&project("notspine", d)).unwrap_err();
    assert_eq!(e[0].code, "anchor_not_spine");

    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["from"] = json!("start:pop/t");
    let e = resolve(&project("layeranchor", d)).unwrap_err();
    assert_eq!(e[0].code, "anchor_unsupported");
}

#[test]
fn scene_edges() {
    let mut d = timeline();
    d["audio"][0]["to"] = json!("end:intro");
    let r = resolve(&project("scene", d)).unwrap();
    assert!(near(r.audio[0].to, 5.6), "intro ends where outro/1 starts");
    assert!(near(r.scenes[1].start, 5.6) && near(r.scenes[1].end, 6.6));
}

#[test]
fn ripple_shifts_later_anchors_and_keeps_local_times() {
    let p0 = project("ripple0", timeline());
    let r0 = resolve(&p0).unwrap();
    // Trim 0.3 s off the end of spine item a: everything after it moves 0.3 s earlier.
    let mut d = timeline();
    d["spine"][0]["out"] = json!(1.7);
    let r1 = resolve(&project("ripple1", d)).unwrap();
    assert!(near(r1.end, r0.end - 0.3));
    assert!(near(r1.overlays[0].from, r0.overlays[0].from - 0.3));
    assert!(near(r1.shot("intro/2").unwrap().start, r0.shot("intro/2").unwrap().start - 0.3));
    // Composition-local times don't move, so graphics keys don't either.
    assert_eq!(r1.overlays[0].comp.layers_json, r0.overlays[0].comp.layers_json);
    assert_eq!(r1.shot("intro/2").unwrap().comp.layers_json, r0.shot("intro/2").unwrap().comp.layers_json);
    // Units: same count, later ones shifted by 9 frames (0.3 s at 30 fps); the first one shorter.
    assert_eq!(r0.units.len(), r1.units.len());
    assert_eq!(r1.units[0].frames(), r0.units[0].frames() - 9);
    for (a, b) in r0.units.iter().zip(&r1.units).skip(1) {
        assert_eq!(b.f0, a.f0 - 9);
        assert_eq!(b.frames(), a.frames());
    }
}

#[test]
fn units_are_contiguous_and_cover_the_output() {
    let r = resolve(&project("units", timeline())).unwrap();
    assert_eq!(r.units[0].f0, 0);
    for w in r.units.windows(2) {
        assert_eq!(w[0].f1, w[1].f0);
    }
    assert_eq!(r.units.last().unwrap().f1, r.frames);
    assert_eq!(r.frames, 198);
    let kinds: Vec<_> = r.units.iter().map(|u| (u.kind, u.source.as_str())).collect();
    assert_eq!(kinds, [(UnitKind::Shot, "intro/1"), (UnitKind::Shot, "intro/2"), (UnitKind::Shot, "outro/1")]);
}

#[test]
fn gain_keys_resolve_on_each_elements_clock_and_duck_targets_are_checked() {
    let mut d = timeline();
    let keys = json!({"keys": [{"t": 0, "v": -20}, {"t": "word:b/Everest", "v": 0}]});
    // Everest is at 3.8 s on the output clock; b starts at 2.1.
    d["audio"][0]["gain_db"] = keys.clone();
    d["audio"][0]["duck"] = json!([{"db": -7}, {"db": -96, "under": ["a", "b"], "attack": 0.2}]);
    d["spine"][1]["audio"] = json!({"gain_db": keys.clone(), "fade_curve": "s-curve", "stream": 1, "loudness": -23});
    d["scenes"][0]["shots"][0]["picture"]["layers"][0]["audio"] =
        json!({"gain_db": keys.clone(), "duck": {"db": -6, "under": "inserts"}});
    d["master"] = json!({"loudness": -18, "true_peak": -2, "lowpass": 16500});
    let p = project("gain-keys", d.clone());
    let r = resolve(&p).unwrap();
    let t1 = |p: &retake_timeline::keyframe::Prop| match p {
        retake_timeline::keyframe::Prop::Keys { keys, .. } => keys[1].t,
        _ => panic!("keys"),
    };
    assert!(near(t1(r.audio[0].gain.as_ref().unwrap()), 3.8), "from the bed's `from` (0)");
    assert!(near(t1(r.spine_item("b").unwrap().gain.as_ref().unwrap()), 1.7), "from b's start");
    assert!(r.spine_item("a").unwrap().gain.is_none());
    let layer = &r.shot("intro/1").unwrap().comp.layers_json[0];
    assert_eq!(layer["audio"]["gain_db"]["keys"][1]["t"], json!(3.8), "composition seconds");
    assert_eq!(p.tl.master.as_ref().unwrap().true_peak, Some(-2.0));
    // A constant gain stays a number.
    let mut c = d.clone();
    c["audio"][0]["gain_db"] = json!(-6);
    let r = resolve(&project("gain-const", c)).unwrap();
    assert!(matches!(r.audio[0].gain, Some(retake_timeline::keyframe::Prop::Const(_))));
    // Ducking under something that isn't a spine item, or a key anchor that doesn't resolve.
    let mut bad = d.clone();
    bad["audio"][0]["duck"] = json!({"db": -6, "under": ["pop"]});
    bad["scenes"][0]["shots"][0]["picture"]["layers"][0]["audio"]["duck"] = json!({"db": -6, "under": ["zz"]});
    bad["spine"][1]["audio"]["gain_db"]["keys"][1]["t"] = json!("word:b/Everist");
    let e = resolve(&project("gain-bad", bad)).unwrap_err();
    let codes: Vec<(&str, &str)> = e.iter().map(|e| (e.code.as_str(), e.path.as_deref().unwrap_or(""))).collect();
    assert!(codes.contains(&("duck_under", "/audio/0/duck/under")), "{codes:?}");
    assert!(codes.contains(&("duck_under", "/scenes/0/shots/0/picture/layers/0/audio/duck/under")), "{codes:?}");
    assert!(codes.contains(&("anchor_word_not_found", "/spine/1/audio/gain_db/keys/1/t")), "{codes:?}");
    // The schema: a gain out of range, an unknown curve, a duck on a spine item.
    for (path, v) in [
        ("/audio/0/gain_db", json!(30)),
        ("/audio/0/fade_curve", json!("cosine")),
        ("/spine/1/audio/duck", json!({"db": -6})),
        ("/audio/0/stream", json!(-1)),
    ] {
        let mut s = d.clone();
        *s.pointer_mut(path.rsplit_once('/').unwrap().0)
            .unwrap()
            .as_object_mut()
            .unwrap()
            .entry(path.rsplit_once('/').unwrap().1)
            .or_insert(json!(null)) = v;
        assert!(!retake_timeline::schema::check_timeline(&s).is_empty(), "{path} should fail the schema");
    }
}

#[test]
fn inserts_cut_shots_and_pause_their_clocks() {
    let mut d = timeline();
    // A 1 s insert after spine item a, in the middle of nothing; and one in the middle of shot intro/2's span.
    d["spine"] = json!([
        {"id": "a", "asset": "proj:vo", "in": 0.4, "out": 2.0},
        {"id": "b", "asset": "proj:vo", "in": 2.9, "out": 4.5, "gap_before": 0.5},
        {"id": "meme", "kind": "insert", "asset": "proj:meme", "in": 8.0, "frames": 30, "hold_from": 8.8,
         "audio": {"gain_db": -3}},
        {"id": "b2", "asset": "proj:vo", "in": 4.5, "out": 6.4},
        {"id": "c", "asset": "proj:vo", "in": 6.9, "out": 7.9}
    ]);
    d["scenes"][0]["overlays"][0]["from"] = json!("word:b/rank");
    d["scenes"][0]["overlays"][0]["to"] = json!("word:b2/same@end");
    d["scenes"][0]["overlays"][0]["layers"][0]["transform"]["opacity"]["keys"][1]["t"] = json!("word:b2/buys");
    d["scenes"][0]["shots"][1]["picture"]["layers"][0]["transform"]["scale"]["keys"][1]["t"] = json!("word:b2/Everest");
    let r = resolve(&project("inserts", d)).unwrap();
    let meme = r.spine_item("meme").unwrap();
    assert!(near(meme.start, 3.7) && near(meme.end, 4.7));
    // intro/2 runs 2.1 → 5.6+1.0 and is cut by the insert into two units around it.
    let kinds: Vec<_> = r.units.iter().map(|u| (u.kind, u.source.clone(), u.f0, u.f1)).collect();
    assert_eq!(
        kinds,
        vec![
            (UnitKind::Shot, "intro/1".to_string(), 0, 63),
            (UnitKind::Shot, "intro/2".to_string(), 63, 111),
            (UnitKind::Insert, "meme".to_string(), 111, 141),
            (UnitKind::Shot, "intro/2".to_string(), 141, 198),
            (UnitKind::Shot, "outro/1".to_string(), 198, 228),
        ]
    );
    // The shot's clock pauses during the insert: frame 141 is local frame 48, right after 47 (frame 110).
    let c0 = r.frame(r.shot("intro/2").unwrap().start);
    assert_eq!(r.local_frame(c0, 110), 47);
    assert_eq!(r.local_frame(c0, 141), 48);
    // Keyframe anchors after the insert subtract it: Everest (4.6 in asset, in b2 at 4.5 → 4.7 + 0.1 = 4.8 out)
    // minus shot start 2.1 minus the 1.0 s insert = 1.7, the same as without the insert.
    assert_eq!(r.shot("intro/2").unwrap().comp.layers_json[0]["transform"]["scale"]["keys"][1]["t"], json!(1.7));
    // The insert's default picture: its asset full frame, holding at hold_from.
    let pic = &meme.comp.as_ref().unwrap().layers_json[0];
    assert_eq!(pic["type"], "video");
    assert_eq!(pic["in"], json!(8.0));
    assert_eq!(pic["hold_from"], json!(8.8));
}

#[test]
fn shot_order_is_enforced() {
    let mut d = timeline();
    d["scenes"][1]["shots"][0]["at"] = json!("start:a+0.20");
    let e = resolve(&project("order", d)).unwrap_err();
    assert_eq!(e[0].code, "shot_order");
    assert_eq!(e[0].path.as_deref(), Some("/scenes/1/shots/0/at"));
}

#[test]
fn semantic_checks() {
    let mut d = timeline();
    d["scenes"][1]["shots"][0]["id"] = json!("intro/1");
    d["spine"][0]["asset"] = json!("proj:v0");
    d["scenes"][0]["shots"][1]["picture"]["layers"][0]["font"] = json!("dispaly");
    d["scenes"][0]["shots"][0]["picture"]["layers"][0]["transform"] = json!({"position": [1, 2, 3]});
    let p = project("semantic", d);
    let errs = validate(&p);
    let codes: Vec<&str> = errs.iter().map(|e| e.code.as_str()).collect();
    assert!(codes.contains(&"duplicate_id"), "{codes:?}");
    assert!(codes.contains(&"asset_unknown"), "{codes:?}");
    assert!(codes.contains(&"font_unknown"), "{codes:?}");
    assert!(codes.contains(&"prop_shape"), "{codes:?}");
    let a = errs.iter().find(|e| e.code == "asset_unknown").unwrap();
    assert_eq!(a.suggestions[0], "proj:vo", "closest first");
    let f = errs.iter().find(|e| e.code == "font_unknown").unwrap();
    assert_eq!(f.suggestions, vec!["display".to_string()]);
}

#[test]
fn keys_out_of_order_and_missing_words() {
    let mut d = timeline();
    d["scenes"][0]["overlays"][0]["layers"][0]["transform"]["opacity"]["keys"][1]["t"] = json!(-1.0);
    let e = resolve(&project("keys", d)).unwrap_err();
    assert_eq!(e[0].code, "keys_order");
    let mut d = timeline();
    d["assets"]["proj:vo"].as_object_mut().unwrap().remove("words");
    let e = resolve(&project("nowords", d)).unwrap_err();
    assert!(e.iter().all(|e| e.code == "words_missing"), "{e:?}");
}

#[test]
fn missing_media_is_reported() {
    let mut d = timeline();
    d["assets"]["proj:gone"] = json!({"path": "nope/gone.mp4"});
    let p = project("missing", d);
    let errs = check_refs(&p);
    assert_eq!(errs.len(), 1);
    assert_eq!(errs[0].code, "asset_missing");
    assert_eq!(errs[0].path.as_deref(), Some("/assets/proj:gone"));
    let mut d = timeline();
    d["format"]["fps"] = json!(24);
    d["format"]["sample_rate"] = json!(44100);
    let errs = check_refs(&project("rates", d));
    assert_eq!(errs[0].code, "format_rates");
}

/// Slice 4's layer constructs: groups resolve their children's anchors on the composition clock, track mattes and
/// corner pins are checked, and expr_lib wraps every expression.
#[test]
fn groups_mattes_corners_and_expr_lib() {
    let mut d = timeline();
    d["expr_lib"] = json!("const k = 2;");
    let pic = &mut d["scenes"][0]["shots"][1]["picture"];
    pic["expr_lib"] = json!("const j = 3;");
    pic["layers"] = json!([
        {"id": "g", "type": "group", "transform": {"opacity": 0.5, "rotation_y": 20, "perspective": 900},
         "mask": {"radius": 12, "feather": 1, "stroke": {"color": "#fff", "width": 2}}, "blend": "screen",
         "layers": [
            {"id": "g/m", "type": "shape", "shape": "path", "path": "M0 0 L10 0 L10 10 Z", "fill": "#fff"},
            {"id": "g/t", "type": "text", "text": "HI", "from": "word:b/Everest", "hinting": "normal",
             "subpixel": false, "metrics": "pillow", "matte": {"layer": "g/m", "mode": "luma"},
             "transform": {"opacity": {"expr": "k * j * t"}}}
         ]},
        {"id": "pin", "type": "image", "asset": "proj:clip", "resample": "lanczos",
         "transform": {"corners": [0, 0, 10, 0, 10, 10, 0, 10]}}
    ]);
    let p = project("s4-layers", d.clone());
    let r = resolve(&p).unwrap_or_else(|e| panic!("{e:?}"));
    let comp = &r.shots.iter().find(|s| s.id == "intro/2").unwrap().comp;
    let child = &comp.layers_json[0]["layers"][1];
    // Everest is at 3.8 s in output time; the shot starts at "Everyone - 0.10" = 2.1 s: 1.7 s on the shot's clock.
    assert!(near(child["from"].as_f64().unwrap(), 1.7), "{child}");
    let e = child["transform"]["opacity"]["expr"].as_str().unwrap();
    assert!(e.contains("const k = 2;") && e.contains("const j = 3;") && e.contains("k * j * t"), "{e}");
    assert!(e.find("const k").unwrap() < e.find("const j").unwrap(), "the timeline's library comes first");
    // Errors: corners with a position, a matte that isn't a sibling, a layer matted by itself, a matted matte.
    let mut bad = d.clone();
    bad["scenes"][0]["shots"][1]["picture"]["layers"][1]["transform"]["position"] = json!([1, 2]);
    bad["scenes"][0]["shots"][1]["picture"]["layers"][0]["layers"][1]["matte"]["layer"] = json!("pin");
    let e = resolve(&project("s4-bad", bad)).unwrap_err();
    let codes: Vec<&str> = e.iter().map(|x| x.code.as_str()).collect();
    assert!(codes.contains(&"transform_conflict") && codes.contains(&"matte_unknown"), "{codes:?}");
    let mut own = d.clone();
    own["scenes"][0]["shots"][1]["picture"]["layers"][0]["layers"][1]["matte"]["layer"] = json!("g/t");
    assert_eq!(resolve(&project("s4-self", own)).unwrap_err()[0].code, "matte_self");
    let mut chain = d.clone();
    chain["scenes"][0]["shots"][1]["picture"]["layers"][0]["layers"][0]["matte"] = json!({"layer": "g/t"});
    let e = resolve(&project("s4-chain", chain)).unwrap_err();
    assert!(e.iter().any(|x| x.code == "matte_chain"), "{e:?}");
    // The schema refuses unknown blend modes and a corner pin of the wrong length.
    let mut s = d.clone();
    s["scenes"][0]["shots"][1]["picture"]["layers"][0]["blend"] = json!("dodge");
    assert!(Project::from_doc(&scratch("s4-schema").join("timeline.json"), s).is_err());
    let mut c = d;
    c["scenes"][0]["shots"][1]["picture"]["layers"][1]["transform"]["corners"] = json!([0, 0, 1, 1]);
    let e = resolve(&project("s4-corners", c)).unwrap_err();
    assert_eq!(e[0].code, "prop_shape");
}

/// Regression (slice 3 broke it): a slice-2 timeline listed plug-ins and other references in `kits`, which 0.1
/// recorded without loading. They still open, validate and resolve; only kit packages are loaded.
#[test]
fn slice2_kits_are_recorded_not_rejected() {
    let mut d = timeline();
    d["kits"] = json!(["plugin:deadlock", "deadlock", "plugin:captions@2.1.0", "brand:acme@3"]);
    let p = project("slice2-kits", d.clone());
    assert!(validate(&p).is_empty(), "{:?}", validate(&p));
    assert!(p.kits.is_empty());
    assert_eq!(p.kits_recorded, vec!["plugin:deadlock", "deadlock", "plugin:captions@2.1.0", "brand:acme@3"]);
    // Recorded, not dropped: the document keeps them, and the expansion is otherwise unchanged.
    assert_eq!(p.doc["kits"], d["kits"]);
    let mut plain = d.clone();
    plain.as_object_mut().unwrap().remove("kits");
    let q = project("slice2-nokits", plain);
    let (a, b) = (resolve(&p).unwrap(), resolve(&q).unwrap());
    assert_eq!(a.units.len(), b.units.len());
    assert_eq!(serde_json::to_value(&a.shots).unwrap(), serde_json::to_value(&b.shots).unwrap());
    // A kit package that isn't installed is still an error (a typo shouldn't pass silently).
    let mut d = timeline();
    d["kits"] = json!(["clen@1.0.0"]);
    let e = Project::from_doc(&scratch("kit-typo").join("timeline.json"), d).unwrap_err();
    assert_eq!(e.0[0].code, "kit_not_found");
}
