//! The speed paths, and that they don't change what renders: static graphics drawn once and reused whatever the
//! unit's length, animated runs that skip frames that look like the one before, video decoded at its displayed size,
//! and short-GOP proxies that draft renders use automatically. Needs ffmpeg and ffprobe on PATH.

use retake_render::engine::{Engine, Item, Options};
use retake_render::expr::Js;
use retake_render::graphics::Painter;
use retake_render::{pipeline, proxy, unit};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("speed")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

/// Tiny generated media, made once: a clip with one keyframe in 6 s (like a long screen recording), and a VO.
fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            "testsrc2=size=128x72:rate=10",
            "-t",
            "6",
            "-c:v",
            "libx264",
            "-g",
            "600",
            "-pix_fmt",
            "yuv420p",
            &p("long-gop.mp4"),
        ]);
        ffmpeg(&["-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000", "-t", "8", &p("vo.wav")]);
        d
    })
}

fn timeline(first_out: f64) -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "speed", "rev": 0,
        "format": {"size": [160, 90], "fps": 10},
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav")},
            "proj:clip": {"path": format!("{m}/long-gop.mp4")}
        },
        "spine": [
            {"id": "a", "asset": "proj:vo", "in": 0, "out": first_out},
            {"id": "b", "asset": "proj:vo", "in": 3, "out": 5}
        ],
        "scenes": [{"id": "s", "shots": [
            {"id": "card", "at": "start:a", "picture": {"background": "#202028", "layers": [
                {"id": "card/box", "type": "shape", "shape": "rect", "size": [120, 50], "radius": 8, "fill": "#ffffff",
                 "shadow": {"dx": 0, "dy": 4, "blur": 12}},
                {"id": "card/title", "type": "text", "text": "STATIC", "size": 18, "fill": "#000000"},
                {"id": "card/clock", "type": "text", "text": {"expr": "'t=' + t.toFixed(1)"}, "size": 12,
                 "transform": {"position": [80, 80]}},
                {"id": "card/late", "type": "text", "text": "NEVER", "size": 12, "from": 100.0}]}},
            {"id": "pip", "at": "start:b", "picture": {"layers": [
                {"id": "pip/full", "type": "video", "asset": "proj:clip", "in": 4.0},
                {"id": "pip/small", "type": "video", "asset": "proj:clip", "in": 1.0, "fit": "contain",
                 "transform": {"scale": 0.3, "position": [120, 60]}},
                {"id": "pip/pop", "type": "text", "text": "POP", "size": 20,
                 "transform": {"scale": {"keys": [{"t": 0, "v": 1.5, "ease": "out-cubic"}, {"t": 0.2, "v": 1.0}]}}}]}}
        ]}]
    })
}

fn engine(name: &str, doc: &Value, opts: Options) -> Engine {
    let d = root().join("tl").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let opts = Options { cache: opts.cache.or_else(|| Some(d.join("cache"))), jobs: 2, ..opts };
    Engine::open(&path, opts).unwrap_or_else(|err| panic!("{name}: {err:#}"))
}

#[test]
fn static_graphics_are_one_image_whatever_the_units_length() {
    let e = engine("static", &timeline(2.0), Options::default());
    let plans = e.plan().unwrap();
    let card = plans.iter().find(|p| p.span.source == "card").unwrap();
    // The box and title (static) are one run, the clock (an expression) another; NEVER shows on no frame: dropped.
    let runs: Vec<(bool, Vec<&str>)> =
        card.runs.iter().map(|r| (r.still, r.layers.iter().map(|l| l.id.as_str()).collect())).collect();
    assert_eq!(runs, vec![(true, vec!["card/box", "card/title"]), (false, vec!["card/clock"])]);
    let pip = plans.iter().find(|p| p.span.source == "pip").unwrap();
    assert_eq!(pip.runs.len(), 1);
    assert!(!pip.runs[0].still, "a keyframed pop is animated");

    let rep = pipeline::render(&e, None).unwrap();
    assert_eq!(rep.layers_rendered, 3);
    // 20 frames of the card's static run are 1 image; the clock changes every frame (t has one decimal at 10 fps);
    // the pop settles after 0.2 s, so 2 of its 20 frames are drawn (the third looks like the second... and on).
    assert_eq!(rep.graphics_frames, 60);
    assert!(rep.graphics_drawn <= 1 + 20 + 4, "{} drawn", rep.graphics_drawn);
    assert!(rep.graphics_drawn >= 1 + 20 + 2, "{} drawn", rep.graphics_drawn);
    let idx = e.store.get_index("layer", &card.runs[0].key).unwrap();
    assert_eq!(idx["static"], true);
    assert!(idx["frame"].is_array() && idx.get("frames").is_none(), "one image: {idx}");
    assert_eq!(e.store.get_index("layer", &card.runs[1].key).unwrap()["frames"].as_array().unwrap().len(), 20);

    // A ripple that lengthens the card shot: the unit re-renders, its static graphics don't (no range in their key);
    // the clock covers more frames, so it does.
    let longer = engine("static-longer", &timeline(2.5), Options { cache: e.opts.cache.clone(), ..Options::default() });
    let lp = longer.plan().unwrap();
    let lcard = lp.iter().find(|p| p.span.source == "card").unwrap();
    assert_ne!(lcard.key, card.key);
    assert_eq!(lcard.runs[0].key, card.runs[0].key, "the static run is reused");
    assert_ne!(lcard.runs[1].key, card.runs[1].key);
    let r2 = pipeline::render(&longer, None).unwrap();
    assert_eq!((r2.reused, r2.rendered), (1, 1), "the moved picture-in-picture shot is reused");
    assert_eq!((r2.layers_reused, r2.layers_rendered), (1, 1), "only the clock draws again");
}

#[test]
fn a_settled_animation_skips_frames_and_matches_drawing_every_frame() {
    let e = engine("hold", &timeline(2.0), Options::default());
    let plans = e.plan().unwrap();
    let pop = &plans.iter().find(|p| p.span.source == "pip").unwrap().runs[0];
    let faces = e.resources.faces(pop.layers.iter());
    let mut painter = Painter::new(e.resources.clone(), &faces).unwrap();
    let st = unit::render_run(pop, &mut painter, &e.canvas, e.fps(), &e.store).unwrap();
    assert_eq!(st.frames, 20);
    assert!(st.drawn <= 4, "keys settle at 0.2 s: {st:?}");
    // Every stored frame equals what drawing that frame gives.
    let data = unit::load_run(&e.store, &pop.key).unwrap();
    for k in pop.range[0]..pop.range[1] {
        let px = painter
            .render(
                &pop.layers,
                &pop.props,
                (e.canvas.w, e.canvas.h),
                e.canvas.scale,
                e.canvas.comp_f(),
                k as f64 / 10.0,
            )
            .unwrap();
        let want = retake_render::composite::alpha_bbox(&px, e.canvas.w, e.canvas.h);
        let got = data.at(k).map(|(_, r)| r);
        assert_eq!(got, want, "frame {k}");
        if let Some((hex, _)) = data.at(k) {
            let raw = lz4_flex::decompress_size_prepended(&e.store.get_blob(&hex).unwrap()).unwrap();
            let r = want.unwrap();
            assert_eq!(raw, retake_render::composite::crop_px(&px, e.canvas.w, r), "frame {k} pixels");
        }
    }
}

#[test]
fn video_decodes_at_its_displayed_size() {
    let e = engine("decode", &timeline(2.0), Options::default());
    let plans = e.plan().unwrap();
    let pip = plans.iter().find(|p| p.span.source == "pip").unwrap();
    let mut js = Js::new().unwrap();
    let sizes: Vec<(String, (u32, u32))> = pip
        .desc
        .items
        .iter()
        .filter(|it| matches!(it, Item::Video { .. }))
        .map(|it| {
            let Item::Video { typed, .. } = it else { unreachable!() };
            (typed.id.clone(), unit::decode_size(it, &e.canvas, e.fps(), &mut js).unwrap())
        })
        .collect();
    // 128x72 into 160x90 contain = 1.25x: the full-frame layer decodes at the source size (never above it times
    // the render scale); the picture-in-picture at 30% of that: 128 * 0.375 = 48, 72 * 0.375 = 27 -> 28.
    assert_eq!(sizes, vec![("pip/full".to_string(), (128, 72)), ("pip/small".to_string(), (48, 28))]);
    // A draft (half scale) halves the full-frame one and keeps the small one under it.
    let d = engine("decode-draft", &timeline(2.0), Options { draft: true, ..Options::default() });
    let dp = d.plan().unwrap();
    let dpip = dp.iter().find(|p| p.span.source == "pip").unwrap();
    let it = dpip.desc.items.iter().find(|it| matches!(it, Item::Video { .. })).unwrap();
    assert_eq!(unit::decode_size(it, &d.canvas, d.fps(), &mut js).unwrap(), (64, 36));
}

fn keyframes(p: &Path) -> usize {
    let out = Command::new("ffprobe")
        .args(["-v", "error", "-select_streams", "v:0", "-skip_frame", "nokey", "-show_entries", "frame=pts_time"])
        .args(["-of", "csv=p=0"])
        .arg(p)
        .output()
        .expect("ffprobe runs");
    String::from_utf8_lossy(&out.stdout).lines().filter(|l| !l.trim().is_empty()).count()
}

fn psnr(a: &Path, b: &Path) -> f64 {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-v", "info", "-i"])
        .arg(a)
        .arg("-i")
        .arg(b)
        .args(["-lavfi", "psnr", "-f", "null", "-"])
        .output()
        .expect("ffmpeg runs");
    let err = String::from_utf8_lossy(&out.stderr);
    let avg = err.rsplit("average:").next().and_then(|s| s.split_whitespace().next()).unwrap_or("0");
    if avg == "inf" { f64::INFINITY } else { avg.parse().unwrap_or(0.0) }
}

#[test]
fn proxies_are_short_gop_and_drafts_use_them() {
    let cache = root().join("proxy-cache");
    let _ = std::fs::remove_dir_all(&cache);
    let full = engine("proxy-full", &timeline(2.0), Options { cache: Some(cache.clone()), ..Options::default() });
    let draft0 = engine(
        "proxy-draft0",
        &timeline(2.0),
        Options { cache: Some(cache.clone()), draft: true, ..Options::default() },
    );
    let src = draft0.assets.get("proj:clip").unwrap();
    assert!(src.proxy.is_none());
    assert_eq!(keyframes(&src.path), 1);
    let out0 = root().join("draft-source.mp4");
    pipeline::render(&draft0, Some(&out0)).unwrap();
    let key0: Vec<String> = draft0.plan().unwrap().iter().map(|p| p.key.clone()).collect();

    let made = proxy::make(&full.store, full.assets.get("proj:clip").unwrap(), 36, false).unwrap();
    assert!(made.made && made.proxy.exists());
    assert_eq!(made.size, [64, 36]);
    assert_eq!(made.gop, 5);
    assert_eq!(keyframes(&made.proxy), 12, "a keyframe every half second of 6 s");
    let again = proxy::make(&full.store, full.assets.get("proj:clip").unwrap(), 36, false).unwrap();
    assert!(!again.made, "an up-to-date proxy is kept");

    // Drafts decode the proxy (and say so in their keys); full renders never do.
    let draft = engine(
        "proxy-draft",
        &timeline(2.0),
        Options { cache: Some(cache.clone()), draft: true, ..Options::default() },
    );
    let a = draft.assets.get("proj:clip").unwrap();
    assert_eq!(a.proxy.as_ref().map(|(p, _)| p.clone()), Some(made.proxy.clone()));
    let plans = draft.plan().unwrap();
    let pip = plans.iter().find(|p| p.span.source == "pip").unwrap();
    let Some(Item::Video { src, .. }) = pip.desc.items.iter().find(|it| matches!(it, Item::Video { .. })) else {
        panic!("no video item")
    };
    assert_eq!(src.path, made.proxy);
    assert!(src.proxy.is_some());
    let key1: Vec<String> = plans.iter().map(|p| p.key.clone()).collect();
    assert_eq!(key1[0], key0[0], "the graphics-only unit doesn't read the clip");
    assert_ne!(key1[1], key0[1], "a draft from a proxy isn't the draft from the source");
    let full2 = engine("proxy-full2", &timeline(2.0), Options { cache: Some(cache.clone()), ..Options::default() });
    assert!(full2.assets.get("proj:clip").unwrap().proxy.is_none(), "full renders read the source");

    // Same moments: the draft from the proxy looks like the draft from the source.
    let out1 = root().join("draft-proxy.mp4");
    pipeline::render(&draft, Some(&out1)).unwrap();
    let db = psnr(&out0, &out1);
    assert!(db > 28.0, "PSNR {db} dB");
}
