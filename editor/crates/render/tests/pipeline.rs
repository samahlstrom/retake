//! The unit pipeline: layers that read the same frames of a file share one decoder, decoders are read by threads of
//! their own, the encoder is fed from another thread, footage is converted by zimg, and hardware encoders cut where
//! x264 does. Each is checked against the lockstep path or an independent reference. Needs ffmpeg and ffprobe.

use retake_render::caps;
use retake_render::composite::Frame;
use retake_render::engine::{Engine, Options, UnitPlan};
use retake_render::expr::Js;
use retake_render::ffmpeg::{Convert, EncoderChoice, EncoderKind};
use retake_render::graphics::Painter;
use retake_render::pipeline;
use retake_render::store::Store;
use retake_render::unit::{self, Feeds, Graphics};
use serde_json::{Value, json};
use std::cell::RefCell;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

const W: u32 = 160;
const H: u32 = 90;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("pipeline")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

/// A moving test pattern (H.264, tagged BT.709), one flat YUV colour twice (tagged BT.709, and untagged), lossless,
/// and a VO.
fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        let tags = ["-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv"];
        let mut a = vec!["-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10", "-t", "4"];
        let to709 = "format=rgb24,scale=out_color_matrix=bt709:out_range=tv,format=yuv420p";
        a.extend(["-vf", to709, "-c:v", "libx264", "-crf", "10"]);
        a.extend(["-g", "10"]);
        a.extend(tags);
        let clip = p("clip.mp4");
        a.push(&clip);
        ffmpeg(&a);
        // Exactly flat planes (Y 100, Cb 178, Cr 102: a blue), so a pixel's reference needs no chroma filter.
        let flat = "color=c=black:s=64x36:r=10,format=yuv420p,geq=lum=100:cb=178:cr=102";
        let mut b = vec!["-f", "lavfi", "-i", flat, "-t", "1", "-c:v", "ffv1"];
        b.extend(tags);
        let blue709 = p("blue709.mkv");
        b.push(&blue709);
        ffmpeg(&b);
        ffmpeg(&["-f", "lavfi", "-i", flat, "-t", "1", "-c:v", "ffv1", &p("blue601.mkv")]);
        ffmpeg(&["-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000", "-t", "8", &p("vo.wav")]);
        d
    })
}

fn timeline(layers: Value, overlays: Value, dur: f64) -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "pipe", "rev": 0,
        "format": {"size": [W, H], "fps": 10},
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav")},
            "proj:clip": {"path": format!("{m}/clip.mp4")},
            "proj:blue709": {"path": format!("{m}/blue709.mkv")},
            "proj:blue601": {"path": format!("{m}/blue601.mkv")}
        },
        "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": dur}],
        "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a", "picture": {"background": "#000000", "layers": layers}}],
                    "overlays": overlays}]
    })
}

/// Six tiles of one moment of the clip, each a 40 × 30 crop shown at 0.8 (so all want the same decode), in a grid.
fn tiles() -> Vec<Value> {
    (0..6)
        .map(|i| {
            let (cx, cy) = ((i % 3) * 40 + 20, (i / 3) * 30 + 10);
            json!({"id": format!("tile{i}"), "type": "video", "asset": "proj:clip", "in": 1.0, "fit": "none",
                   "crop": [cx, cy, 40, 30],
                   "transform": {"anchor": [0, 0], "scale": 0.8, "position": [5 + (i % 3) * 50, 8 + (i / 3) * 40]}})
        })
        .collect()
}

/// Everything at once: a background, the tiles (one of them showing only from 1 s), a held layer, a looping one, a
/// cropped Lanczos one, one that never shows, and overlays with footage that start late in the unit.
fn busy() -> Value {
    let mut layers = vec![json!({"id": "bg", "type": "video", "asset": "proj:clip", "in": 0.5, "fit": "cover"})];
    layers.extend(tiles());
    layers.push(json!({"id": "late", "type": "video", "asset": "proj:clip", "in": 1.0, "fit": "none", "from": 1.0,
        "crop": [60, 40, 40, 30], "transform": {"anchor": [0, 0], "scale": 0.8, "position": [110, 50]}}));
    layers.push(
        json!({"id": "held", "type": "video", "asset": "proj:clip", "in": 1.0, "hold_from": 1.3, "fit": "contain",
        "transform": {"scale": 0.3, "position": [30, 70]}}),
    );
    layers.push(json!({"id": "loop", "type": "video", "asset": "proj:clip", "in": 3.5, "loop": true, "fit": "contain",
        "transform": {"scale": 0.3, "position": [80, 70]}}));
    layers.push(
        json!({"id": "lz", "type": "video", "asset": "proj:clip", "in": 2.0, "fit": "none", "resample": "lanczos",
        "crop": [20, 10, 80, 60], "transform": {"anchor": [0, 0], "position": [100, 2], "scale": 0.5}}),
    );
    layers.push(json!({"id": "never", "type": "video", "asset": "proj:clip", "in": 0.0, "from": 100.0}));
    let overlays = json!([
        {"id": "o1", "from": "start:a+0.8", "to": "start:a+1.6", "layers": [
            {"id": "o1/v", "type": "video", "asset": "proj:clip", "in": 0.2, "fit": "contain",
             "transform": {"scale": 0.25, "position": [130, 20]}}]},
        {"id": "o2", "from": "start:a+2.5", "to": "start:a+2.9", "layers": [
            {"id": "o2/v", "type": "video", "asset": "proj:clip", "loop": true, "clock": "timeline", "fit": "contain",
             "transform": {"scale": 0.25, "position": [130, 60]}}]}
    ]);
    timeline(Value::Array(layers), overlays, 3.0)
}

fn engine(name: &str, doc: &Value, opts: Options) -> Engine {
    let d = root().join("tl").join(name);
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let opts = Options { cache: opts.cache.clone().or_else(|| Some(d.join("cache"))), jobs: 2, ..opts };
    Engine::open(&path, opts).unwrap_or_else(|err| panic!("{name}: {err:#}"))
}

/// Every frame of unit 0, composited with graphics drawn live, read `mode`'s way, with frame buffers handed back
/// (zeroed) and given out again as a render does.
fn frames(e: &Engine, p: &UnitPlan, mode: Feeds) -> Vec<Frame> {
    let faces = e.resources.faces(p.runs.iter().flat_map(|r| r.layers.iter()));
    let mut painter = Painter::new(e.resources.clone(), &faces).unwrap();
    let mut js = Js::new().unwrap();
    let (w, h) = (p.desc.canvas.w, p.desc.canvas.h);
    let spare: RefCell<Vec<Frame>> = RefCell::new(vec![]);
    let mut out = vec![];
    unit::compose_with(
        p,
        0,
        p.desc.frames,
        Graphics::Live(&mut painter, &p.runs),
        &mut js,
        mode,
        || spare.borrow_mut().pop().unwrap_or_else(|| Frame::new(w, h)),
        |mut f| {
            out.push(f.clone());
            f.px.fill(0);
            spare.borrow_mut().push(f);
            Ok(())
        },
    )
    .unwrap();
    out
}

fn px(f: &Frame, x: u32, y: u32) -> [u8; 4] {
    let i = ((y * f.w + x) * 4) as usize;
    [f.px[i], f.px[i + 1], f.px[i + 2], f.px[i + 3]]
}

fn psnr(a: &Frame, b: &Frame) -> f64 {
    let se: f64 = a.px.iter().zip(&b.px).map(|(x, y)| (*x as f64 - *y as f64).powi(2)).sum();
    let mse = se / a.px.len() as f64;
    if mse == 0.0 { 99.0 } else { 10.0 * (255.0f64 * 255.0 / mse).log10() }
}

#[test]
fn layers_that_read_the_same_frames_share_one_decoder() {
    let e = engine("share-count", &busy(), Options::default());
    let p = e.plan().unwrap().remove(0);
    let mut js = Js::new().unwrap();
    // 14 video items: bg, 6 tiles and "late" (one decode: same file, `in`, timing), held, loop, Lanczos, never (no
    // decode at all: it never shows), and the overlays' (their own active frames: their own decodes).
    let (decoders, items) = unit::decoder_count(&p, &mut js).unwrap();
    assert_eq!(items, 14);
    assert_eq!(decoders, 7, "bg, tiles, held, loop, lanczos, o1, o2");
}

#[test]
fn a_shared_decode_gives_every_layer_its_own_pixels() {
    // Each tile alone, against the same tile among the others: the decode is shared, the pixels aren't touched.
    let all = engine("share-all", &timeline(Value::Array(tiles()), json!([]), 2.0), Options::default());
    let pa = all.plan().unwrap().remove(0);
    let fa = frames(&all, &pa, Feeds::Inline);
    for (i, t) in tiles().into_iter().enumerate() {
        let one = engine(&format!("share-one-{i}"), &timeline(json!([t]), json!([]), 2.0), Options::default());
        let po = one.plan().unwrap().remove(0);
        let fo = frames(&one, &po, Feeds::Inline);
        let (x0, y0) = (5 + (i as u32 % 3) * 50, 8 + (i as u32 / 3) * 40);
        for k in [0usize, 7, 19] {
            for y in y0 + 1..y0 + 23 {
                for x in x0 + 1..x0 + 31 {
                    assert_eq!(px(&fa[k], x, y), px(&fo[k], x, y), "tile {i}, frame {k}, ({x}, {y})");
                }
            }
        }
    }
    // And the tiles move: frame 19 isn't frame 0.
    assert_ne!(fa[0].px, fa[19].px);
}

#[test]
fn threaded_readers_give_the_same_frames_as_lockstep() {
    let e = engine("threaded", &busy(), Options::default());
    let p = e.plan().unwrap().remove(0);
    let inline = frames(&e, &p, Feeds::Inline);
    let threaded = frames(&e, &p, Feeds::Threaded);
    assert_eq!(inline.len(), 30);
    for (k, (a, b)) in inline.iter().zip(&threaded).enumerate() {
        assert!(a.px == b.px, "frame {k} differs");
    }
    // Held from 1.3 s of source (0.3 s into the unit): its corner stops changing; the looping one doesn't.
    let held =
        |f: &Frame| (40..50).flat_map(|x| (62..68).map(move |y| (x, y))).map(|(x, y)| px(f, x, y)).collect::<Vec<_>>();
    assert_eq!(held(&inline[10]), held(&inline[25]), "held");
    assert_ne!(held(&inline[0]), held(&inline[10]), "moving before the hold");
}

#[test]
fn pipelined_and_lockstep_renders_are_the_same_bytes() {
    let a = engine("bytes-a", &busy(), Options::default());
    let pa = a.plan().unwrap().remove(0);
    let mut js = Js::new().unwrap();
    // No graphics runs in this unit: units render straight from the plan.
    assert!(pa.runs.is_empty());
    let sa = Store::open(&root().join("bytes-store-a")).unwrap();
    let sb = Store::open(&root().join("bytes-store-b")).unwrap();
    let ia = unit::render_unit_with(&pa, &sa, &mut js, true).unwrap();
    let ib = unit::render_unit_with(&pa, &sb, &mut js, false).unwrap();
    assert_eq!(ia["blob"], ib["blob"], "the same segment bytes");
    assert_eq!(ia["boxes"], ib["boxes"], "the same boxes");
    let bytes = std::fs::read(sa.blob_path(ia["blob"].as_str().unwrap())).unwrap();
    assert!(bytes.len() > 1000);
}

#[test]
fn zimg_reads_tagged_and_untagged_footage_as_it_was_made() {
    if !caps::has_zscale() {
        eprintln!("skipped: this ffmpeg has no zscale");
        return;
    }
    let layers = json!([
        {"id": "l", "type": "video", "asset": "proj:blue709", "fit": "none", "transform": {"anchor": [0, 0], "position": [0, 0]}},
        {"id": "r", "type": "video", "asset": "proj:blue601", "fit": "none", "transform": {"anchor": [0, 0], "position": [80, 0]}}
    ]);
    let e = engine("colour", &timeline(layers, json!([]), 1.0), Options::default());
    let mut p = e.plan().unwrap().remove(0);
    assert_eq!(p.desc.convert, Convert::Zimg);
    // The reference: the file's own Y, Cb, Cr (a flat colour) through each matrix in floating point, limited range.
    let rgb = |file: &str, kr: f64, kb: f64| -> [f64; 3] {
        let out = Command::new("ffmpeg")
            .args(["-nostdin", "-v", "error", "-i"])
            .arg(media().join(file))
            .args(["-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "yuv420p", "-"])
            .output()
            .unwrap();
        let (n, b) = (64 * 36, &out.stdout);
        let (y, cb, cr) =
            ((b[0] as f64 - 16.0) / 219.0, (b[n] as f64 - 128.0) / 224.0, (b[n * 5 / 4] as f64 - 128.0) / 224.0);
        let (r, bl) = (y + 2.0 * (1.0 - kr) * cr, y + 2.0 * (1.0 - kb) * cb);
        let g = (y - kr * r - kb * bl) / (1.0 - kr - kb);
        [r, g, bl].map(|v| (v * 255.0).clamp(0.0, 255.0))
    };
    let (bt709, bt601) = ((0.2126, 0.0722), (0.299, 0.114));
    let tagged = rgb("blue709.mkv", bt709.0, bt709.1);
    let untagged = rgb("blue601.mkv", bt601.0, bt601.1);
    let wrong = rgb("blue709.mkv", bt601.0, bt601.1);
    assert!((0..3).any(|c| (tagged[c] - wrong[c]).abs() > 3.0), "the test tells the matrices apart");
    let near = |got: [u8; 4], want: [f64; 3], tol: f64| (0..3).all(|c| (got[c] as f64 - want[c]).abs() <= tol);
    let z = frames(&e, &p, Feeds::Inline).remove(3);
    assert!(near(px(&z, 30, 18), tagged, 1.0), "BT.709, tagged: {:?} vs {tagged:?}", px(&z, 30, 18));
    assert!(near(px(&z, 110, 18), untagged, 1.0), "BT.601, untagged: {:?} vs {untagged:?}", px(&z, 110, 18));
    // ffmpeg's scaler reads them the same way (within its rounding).
    p.desc.convert = Convert::Swscale;
    let s = frames(&e, &p, Feeds::Inline).remove(3);
    assert!(near(px(&s, 30, 18), tagged, 2.0), "swscale, tagged: {:?}", px(&s, 30, 18));
    assert!(near(px(&s, 110, 18), untagged, 2.0), "swscale, untagged: {:?}", px(&s, 110, 18));
    // The two converters on real pictures (scaled, cropped, Lanczos): the same picture, not the same bytes.
    let e = engine("convert-busy", &busy(), Options::default());
    let mut p = e.plan().unwrap().remove(0);
    let zf = frames(&e, &p, Feeds::Inline);
    p.desc.convert = Convert::Swscale;
    let sf = frames(&e, &p, Feeds::Inline);
    let q = psnr(&zf[12], &sf[12]);
    // (testsrc2 is all hard edges, where two scalers differ most; a misplaced picture is under 15 dB.)
    assert!(q > 25.0 && q < 99.0, "zimg vs swscale: {q:.1} dB");
}

#[test]
fn the_converter_and_the_encoder_are_in_the_unit_key() {
    let doc = busy();
    let mut e = engine("key-a", &doc, Options::default());
    let k = |e: &Engine| e.plan().unwrap().remove(0).key;
    let base = k(&e);
    e.convert = if e.convert == Convert::Zimg { Convert::Swscale } else { Convert::Zimg };
    assert_ne!(k(&e), base, "zimg and swscale give other pixels");
    let nv = engine("key-b", &doc, Options { encoder: EncoderChoice::Fixed(EncoderKind::Nvenc), ..Options::default() });
    assert_eq!(nv.encode.encoder, EncoderKind::Nvenc);
    assert_ne!(k(&nv), base, "another encoder, another key");
}

/// Frame numbers of a file's keyframes.
fn keyframes(p: &Path) -> Vec<u64> {
    let out = Command::new("ffprobe")
        .args(["-v", "error", "-select_streams", "v:0", "-show_entries", "frame=key_frame", "-of", "csv=p=0"])
        .arg(p)
        .output()
        .unwrap();
    String::from_utf8_lossy(&out.stdout)
        .lines()
        .enumerate()
        .filter(|(_, l)| l.trim().trim_end_matches(',') == "1")
        .map(|(i, _)| i as u64)
        .collect()
}

#[test]
fn every_encoder_that_works_here_keys_every_gop_and_packages() {
    // Hardware encoders run when they work on this machine (RETAKE_TEST_HW=0 skips them).
    let hw = std::env::var("RETAKE_TEST_HW").map(|v| v != "0").unwrap_or(true);
    let doc = timeline(json!([{"id": "v", "type": "video", "asset": "proj:clip", "in": 0.0}]), json!([]), 5.0);
    for kind in EncoderKind::ALL {
        if kind.is_hw() && !(hw && caps::test_encode(kind)) {
            eprintln!("{}: not here, skipped", kind.name());
            continue;
        }
        let e = engine(
            &format!("enc-{}", kind.name()),
            &doc,
            Options { encoder: EncoderChoice::Fixed(kind), ..Options::default() },
        );
        let out = root().join(format!("enc-{}.mp4", kind.name()));
        let rep = pipeline::render(&e, Some(&out)).unwrap_or_else(|err| panic!("{}: {err:#}", kind.name()));
        assert_eq!(rep.encoder, kind.name());
        assert_eq!(rep.frames, 50);
        assert_eq!(retake_render::ffmpeg::count_frames(&out).unwrap(), 50, "{}", kind.name());
        // A GOP is 2 s (20 frames): keyframes exactly there, so streams cut where they're planned.
        assert_eq!(keyframes(&out), vec![0, 20, 40], "{}", kind.name());
    }
}
