//! Slice 4's picture features on generated media, against independent references where there is one (ffmpeg's
//! `perspective`, `crop,scale=…:flags=lanczos` and `delogo` filters, the W3C blend formulas) and against each other
//! where the same construct has two paths (graphics drawn by Skia, footage by the compositor). Frames are compared by
//! PSNR, not bytes, except where the result is a copy.

use retake_render::composite::Frame;
use retake_render::engine::{Engine, Item, Options, walk_items};
use retake_render::expr::Js;
use retake_render::graphics::Painter;
use retake_render::unit::{self, Graphics};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

const W: u32 = 320;
const H: u32 = 180;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("picture")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

/// A smooth, structured pattern (so sub-pixel differences cost little and real misplacements cost a lot), stored
/// losslessly as RGB (FFV1), its first frame as a PNG, a flat color clip, and a VO.
fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        let geq = "geq=r='128+100*sin(X/9)':g='128+100*sin(Y/7)':b='128+90*sin((X+Y)/11)'";
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            &format!("color=c=black:s={W}x{H}:r=10,{geq}"),
            "-t",
            "2",
            "-c:v",
            "ffv1",
            "-pix_fmt",
            "bgr0",
            &p("pattern.mkv"),
        ]);
        ffmpeg(&["-i", &p("pattern.mkv"), "-frames:v", "1", &p("pattern.png")]);
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            &format!("color=c=#3366cc:s={W}x{H}:r=10"),
            "-t",
            "2",
            "-c:v",
            "ffv1",
            "-pix_fmt",
            "bgr0",
            &p("blue.mkv"),
        ]);
        ffmpeg(&["-f", "lavfi", "-i", "sine=frequency=300:sample_rate=48000", "-t", "3", &p("vo.wav")]);
        d
    })
}

fn timeline(layers: Value) -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "pic", "rev": 0,
        "format": {"size": [W, H], "fps": 10},
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav")},
            "proj:pattern": {"path": format!("{m}/pattern.mkv")},
            "proj:still": {"path": format!("{m}/pattern.png")},
            "proj:blue": {"path": format!("{m}/blue.mkv")}
        },
        "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": 2}],
        "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a", "picture": {"background": "#000000", "layers": layers}}]}]
    })
}

fn engine(name: &str, doc: &Value) -> Engine {
    let d = root().join("tl").join(name);
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let opts = Options { cache: Some(root().join("cache")), jobs: 1, ..Options::default() };
    Engine::open(&path, opts).unwrap_or_else(|e| panic!("{name}: {e:#}"))
}

/// One frame through the real compositor (graphics drawn live).
fn frame(name: &str, layers: Value, t: f64) -> Frame {
    let e = engine(name, &timeline(layers));
    let f = e.res.frame(t);
    let u = e.res.unit_at(f).unwrap().clone();
    let p = e.plan_unit(&u).unwrap();
    let faces = e.resources.faces(p.runs.iter().flat_map(|r| r.layers.iter()));
    let mut painter = Painter::new(e.resources.clone(), &faces).unwrap();
    let mut js = Js::new().unwrap();
    let mut out = None;
    unit::compose(&p, f - u.f0, f - u.f0 + 1, Graphics::Live(&mut painter, &p.runs), &mut js, |fr| {
        out = Some(fr.clone());
        Ok(())
    })
    .unwrap();
    out.unwrap()
}

fn px(f: &Frame, x: u32, y: u32) -> [u8; 4] {
    let i = ((y * f.w + x) * 4) as usize;
    [f.px[i], f.px[i + 1], f.px[i + 2], f.px[i + 3]]
}

/// PSNR (RGB) of two frames over the pixels `keep` accepts.
fn psnr(a: &[u8], b: &[u8], w: u32, keep: impl Fn(u32, u32) -> bool) -> f64 {
    let (mut se, mut n) = (0f64, 0u64);
    for (i, (pa, pb)) in a.chunks(4).zip(b.chunks(4)).enumerate() {
        let (x, y) = (i as u32 % w, i as u32 / w);
        if !keep(x, y) {
            continue;
        }
        for c in 0..3 {
            let d = pa[c] as f64 - pb[c] as f64;
            se += d * d;
        }
        n += 3;
    }
    assert!(n > 0, "no pixels compared");
    let mse = se / n as f64;
    if mse == 0.0 { 99.0 } else { 10.0 * (255.0f64 * 255.0 / mse).log10() }
}

/// An ffmpeg filter graph's output on the pattern's frame `k`, as RGBA.
fn ffmpeg_rgba(src: &str, vf: &str, k: u32) -> Vec<u8> {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-v", "error", "-i"])
        .arg(media().join(src))
        .args(["-vf", &format!("select=eq(n\\,{k}),{vf},format=rgba"), "-frames:v", "1", "-f", "rawvideo", "-"])
        .output()
        .unwrap();
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    out.stdout
}

/// Is (x, y) inside the convex quad `q` (clockwise TL, TR, BR, BL), at least `m` pixels from every edge?
fn inside(q: &[f64; 8], x: u32, y: u32, m: f64) -> bool {
    let (px, py) = (x as f64 + 0.5, y as f64 + 0.5);
    (0..4).all(|i| {
        let (ax, ay, bx, by) = (q[2 * i], q[2 * i + 1], q[(2 * i + 2) % 8], q[(2 * i + 3) % 8]);
        let cross = (bx - ax) * (py - ay) - (by - ay) * (px - ax);
        cross / (bx - ax).hypot(by - ay) >= m
    })
}

#[test]
fn corner_pin_matches_ffmpegs_perspective() {
    let q = [40.0, 20.0, 290.0, 45.0, 260.0, 160.0, 60.0, 150.0];
    let ours = frame(
        "pin",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "transform": {"corners": q}}]),
        0.0,
    );
    // ffmpeg's corner order is TL, TR, BL, BR, in pixel centres.
    let vf = format!(
        "perspective=x0={}:y0={}:x1={}:y1={}:x2={}:y2={}:x3={}:y3={}:sense=destination:interpolation=linear",
        q[0], q[1], q[2], q[3], q[6], q[7], q[4], q[5]
    );
    let reference = ffmpeg_rgba("pattern.mkv", &vf, 0);
    let p = psnr(&ours.px, &reference, W, |x, y| inside(&q, x, y, 2.0));
    assert!(p > 30.0, "corner pin vs ffmpeg perspective: {p:.1} dB");
    // Outside the quad: the background.
    assert_eq!(px(&ours, 5, 5), [0, 0, 0, 255]);
}

#[test]
fn rotation_y_draws_graphics_and_footage_alike() {
    let tr = json!({"position": [160, 95], "scale": 0.8, "rotation_y": 30, "rotation_x": -12, "perspective": 600});
    let v = frame(
        "rot-v",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none", "transform": tr}]),
        0.0,
    );
    let i = frame("rot-i", json!([{"id": "i", "type": "image", "asset": "proj:still", "transform": tr}]), 0.0);
    let p = psnr(&v.px, &i.px, W, |_, _| true);
    assert!(p > 32.0, "Skia's perspective vs the compositor's: {p:.1} dB");
    // The box of drawn pixels is CSS's `perspective(600px) rotateX(-12deg) rotateY(30deg)` of the 256 x 144 box.
    let (sx, cx) = (-12f64).to_radians().sin_cos();
    let (sy, cy) = 30f64.to_radians().sin_cos();
    let xs: Vec<f64> = [(-128.0, -72.0), (128.0, -72.0), (128.0, 72.0), (-128.0, 72.0)]
        .iter()
        .map(|&(x, y): &(f64, f64)| {
            let (px, pz) = (cy * x, -cx * sy * x + sx * y);
            160.0 + 600.0 * px / (600.0 - pz)
        })
        .collect();
    let (left, right) = (xs.iter().cloned().fold(f64::MAX, f64::min), xs.iter().cloned().fold(f64::MIN, f64::max));
    assert!(xs[1] - xs[0] < 256.0 && right - 160.0 < 160.0 - left, "the right edge recedes");
    let cols: Vec<u32> = (0..W).filter(|&x| (0..H).any(|y| px(&v, x, y)[..3] != [0, 0, 0])).collect();
    let (x0, x1) = (*cols.first().unwrap() as f64, *cols.last().unwrap() as f64 + 1.0);
    assert!((x0 - left).abs() < 2.0 && (x1 - right).abs() < 2.0, "{x0}..{x1} vs {left:.1}..{right:.1}");
    // One decode for the warped footage, at its 2D size (0.8 of the source).
    let e = engine(
        "rot-plan",
        &timeline(json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "transform": tr}])),
    );
    let plan = e.plan().unwrap().remove(0);
    let vids: Vec<&Item> =
        walk_items(&plan.desc.items).into_iter().filter(|i| matches!(i, Item::Video { .. })).collect();
    assert_eq!(vids.len(), 1);
    let mut js = Js::new().unwrap();
    assert_eq!(unit::decode_size(vids[0], &e.canvas, e.fps(), &mut js).unwrap(), (256, 144));
}

/// The W3C separable blend of an opaque backdrop `b` and a source `s` at alpha `a` (0–1 values).
fn w3c(mode: &str, b: [f64; 3], s: [f64; 3], a: f64) -> [f64; 3] {
    let f = |cb: f64, cs: f64| match mode {
        "multiply" => cb * cs,
        "screen" => cb + cs - cb * cs,
        "overlay" => {
            if cb <= 0.5 {
                2.0 * cs * cb
            } else {
                1.0 - 2.0 * (1.0 - cs) * (1.0 - cb)
            }
        }
        "lighten" => cb.max(cs),
        "darken" => cb.min(cs),
        _ => cs,
    };
    [0, 1, 2].map(|c| if mode == "add" { (b[c] + a * s[c]).min(1.0) } else { (1.0 - a) * b[c] + a * f(b[c], s[c]) })
}

#[test]
fn blend_modes_match_the_w3c_formulas_on_footage_and_in_groups() {
    let fill = [0xcc as f64, 0x88 as f64, 0x44 as f64].map(|x| x / 255.0);
    let back = frame("blend-back", json!([{"id": "bg", "type": "video", "asset": "proj:blue", "fit": "cover"}]), 0.0);
    let b = px(&back, 160, 90);
    let bf = [b[0], b[1], b[2]].map(|x| x as f64 / 255.0);
    for mode in ["normal", "add", "screen", "multiply", "overlay", "lighten", "darken"] {
        let shape = json!({"id": "sh", "type": "shape", "shape": "rect", "size": [100, 60], "fill": "#cc8844",
            "blend": mode, "transform": {"opacity": 0.8}});
        // Over footage: the compositor blends.
        let f = frame(
            &format!("blend-{mode}"),
            json!([{"id": "bg", "type": "video", "asset": "proj:blue", "fit": "cover"}, shape]),
            0.0,
        );
        let want = w3c(mode, bf, fill, 0.8).map(|x| x * 255.0);
        let got = px(&f, 160, 90);
        for c in 0..3 {
            assert!((got[c] as f64 - want[c]).abs() <= 2.0, "{mode} over footage: {got:?} vs {want:?}");
        }
        // Inside a group over a graphics backdrop of the same color: Skia blends.
        let g = frame(
            &format!("blend-g-{mode}"),
            json!([{"id": "g", "type": "group", "layers": [
                {"id": "g/bg", "type": "shape", "shape": "rect", "size": [W, H], "fill": format!("#{:02x}{:02x}{:02x}", b[0], b[1], b[2])},
                shape.as_object().unwrap().clone().into_iter().map(|(k, v)| (k, if v == "sh" { json!("g/sh") } else { v })).collect::<serde_json::Map<_, _>>()
            ]}]),
            0.0,
        );
        let got = px(&g, 160, 90);
        for c in 0..3 {
            assert!((got[c] as f64 - want[c]).abs() <= 2.0, "{mode} in a group: {got:?} vs {want:?}");
        }
    }
}

#[test]
fn opacity_groups_hide_overlaps_with_or_without_footage() {
    let rects = |p: &str| {
        json!([
            {"id": format!("{p}/r"), "type": "shape", "shape": "rect", "size": [100, 100], "fill": "#ff0000", "transform": {"position": [130, 90]}},
            {"id": format!("{p}/g"), "type": "shape", "shape": "rect", "size": [100, 100], "fill": "#00ff00", "transform": {"position": [190, 90]}}
        ])
    };
    let grouped =
        frame("grp", json!([{"id": "g", "type": "group", "transform": {"opacity": 0.5}, "layers": rects("g")}]), 0.0);
    // The overlap shows the top rect at half opacity, no red through it.
    assert_eq!(px(&grouped, 160, 90)[..3], [0, 128, 0], "{:?}", px(&grouped, 160, 90));
    assert_eq!(px(&grouped, 100, 90)[..3], [128, 0, 0]);
    // A group with footage in it goes through the compositor and gives the same picture.
    let mut layers = rects("h");
    layers.as_array_mut().unwrap().insert(
        0,
        json!({"id": "h/v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "transform": {"position": [20, 20], "scale": 0.05}}),
    );
    let with_video =
        frame("grp-v", json!([{"id": "h", "type": "group", "transform": {"opacity": 0.5}, "layers": layers}]), 0.0);
    for (x, y) in [(160, 90), (100, 90), (220, 90), (300, 170)] {
        let (a, b) = (px(&grouped, x, y), px(&with_video, x, y));
        assert!((0..3).all(|c| (a[c] as i32 - b[c] as i32).abs() <= 1), "({x}, {y}): {a:?} vs {b:?}");
    }
    // A transformed group with footage: scaled down about its centre.
    let small = frame(
        "grp-s",
        json!([{"id": "k", "type": "group", "transform": {"scale": 0.5}, "layers": [
        {"id": "k/v", "type": "video", "asset": "proj:pattern", "fit": "none"}]}]),
        0.0,
    );
    assert_eq!(px(&small, 40, 40)[..3], [0, 0, 0], "outside the scaled group");
    let full = frame(
        "grp-f",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "transform": {"scale": 0.5}}]),
        0.0,
    );
    let p = psnr(&small.px, &full.px, W, |x, y| (85..235).contains(&x) && (50..130).contains(&y));
    assert!(p > 32.0, "a scaled group vs the scaled layer: {p:.1} dB");
}

#[test]
fn masks_cut_footage_and_graphics_alike() {
    let mask = json!({"radius": 40, "feather": 1.0, "stroke": {"color": "#ffd23f", "width": 3}});
    let tr = json!({"position": [160, 90], "scale": 0.8});
    let v = frame(
        "mask-v",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none", "transform": tr, "mask": mask}]),
        0.0,
    );
    let i = frame(
        "mask-i",
        json!([{"id": "i", "type": "image", "asset": "proj:still", "transform": tr, "mask": mask}]),
        0.0,
    );
    let p = psnr(&v.px, &i.px, W, |_, _| true);
    assert!(p > 32.0, "masked footage vs masked image: {p:.1} dB");
    // The content box is 256 x 144 at (32, 18): its corner is cut, its centre is the pattern, its edge the outline.
    assert_eq!(px(&v, 34, 20)[..3], [0, 0, 0], "outside the rounded corner");
    let edge = px(&v, 160, 19);
    assert!(edge[0] > 230 && edge[1] > 190 && edge[2] < 90, "outline on the top edge: {edge:?}");
    assert_ne!(px(&v, 160, 90)[..3], [0, 0, 0]);
    // Inverted: only outside the shape.
    let inv = frame(
        "mask-inv",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none", "transform": tr,
        "mask": {"radius": 40, "invert": true}}]),
        0.0,
    );
    assert_eq!(px(&inv, 160, 90)[..3], [0, 0, 0]);
    assert_ne!(px(&inv, 34, 20)[..3], [0, 0, 0]);
    // A path mask: a triangle.
    let tri = frame(
        "mask-path",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "mask": {"shape": "path", "path": "M0 0 L320 0 L0 180 Z"}}]),
        0.0,
    );
    assert_ne!(px(&tri, 20, 20)[..3], [0, 0, 0]);
    assert_eq!(px(&tri, 300, 170)[..3], [0, 0, 0]);
}

#[test]
fn track_mattes_on_footage_and_graphics() {
    let matte = json!({"id": "m", "type": "shape", "shape": "rect", "size": [100, 180], "fill": "#ffffff",
        "transform": {"anchor": [0, 0], "position": [0, 0]}});
    let cases = [("alpha", true), ("alpha-inverted", false)];
    for (mode, left) in cases {
        let f = frame(
            &format!("matte-{mode}"),
            json!([matte, {"id": "v", "type": "video", "asset": "proj:pattern",
            "fit": "none", "matte": {"layer": "m", "mode": mode}}]),
            0.0,
        );
        let (l, r) = (px(&f, 50, 90), px(&f, 250, 90));
        assert_eq!(l[..3] != [0, 0, 0], left, "{mode}: left {l:?}");
        assert_eq!(r[..3] != [0, 0, 0], !left, "{mode}: right {r:?}");
        assert!(f.px.chunks(4).all(|p| p[3] == 255));
    }
    // Luma: a grey matte lets through its luma; graphics targets work too.
    let grey = json!({"id": "m", "type": "shape", "shape": "rect", "size": [W, H], "fill": "#808080"});
    let f = frame(
        "matte-luma",
        json!([grey, {"id": "sw", "type": "shape", "shape": "rect", "size": [W, H], "fill": "#ffffff",
        "matte": {"layer": "m", "mode": "luma"}}]),
        0.0,
    );
    let c = px(&f, 160, 90);
    assert!((c[0] as i32 - 128).abs() <= 2, "{c:?}");
    // The matte layer itself never draws.
    let f = frame(
        "matte-hidden",
        json!([{"id": "m", "type": "shape", "shape": "rect", "size": [40, 40], "fill": "#ff0000",
        "transform": {"position": [300, 160]}}, {"id": "sg", "type": "shape", "shape": "rect", "size": [40, 40], "fill": "#00ff00",
        "transform": {"position": [20, 20]}, "matte": {"layer": "m"}}]),
        0.0,
    );
    assert_eq!(px(&f, 300, 160)[..3], [0, 0, 0]);
    assert_eq!(px(&f, 20, 20)[..3], [0, 0, 0], "the matte doesn't overlap the target: nothing shows");
}

#[test]
fn lanczos_footage_is_ffmpegs_crop_and_scale_exactly() {
    let ours = frame(
        "lanczos",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "crop": [100, 40, 160, 100], "resample": "lanczos",
        "transform": {"anchor": [0, 0], "position": [30, 20], "scale": 0.5}}]),
        0.0,
    );
    let reference = ffmpeg_rgba("pattern.mkv", "crop=160:100:100:40,scale=80:50:flags=lanczos", 0);
    let mut worst = 0;
    for y in 0..50u32 {
        for x in 0..80u32 {
            let a = px(&ours, 30 + x, 20 + y);
            let b = &reference[((y * 80 + x) * 4) as usize..][..4];
            for c in 0..3 {
                worst = worst.max((a[c] as i32 - b[c] as i32).abs());
            }
        }
    }
    assert!(worst <= 1, "a cropped Lanczos decode placed on whole pixels is a copy (worst {worst})");
    // Bilinear (the default) decodes the whole frame and resamples: close, not equal.
    let bil = frame(
        "bilinear",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "crop": [100, 40, 160, 100], "transform": {"anchor": [0, 0], "position": [30, 20], "scale": 0.5}}]),
        0.0,
    );
    assert_ne!(bil.px, ours.px);
}

#[test]
fn gradients_and_unfiltered_sampling() {
    let f = frame(
        "gradient",
        json!([{"id": "g", "type": "shape", "shape": "rect", "size": [200, 40],
        "fill": {"linear": [0, 0, 200, 0], "colors": ["#ff0000", "#0000ff"]}, "transform": {"position": [160, 90]}}]),
        0.0,
    );
    let (l, m, r) = (px(&f, 61, 90), px(&f, 160, 90), px(&f, 259, 90));
    assert!(l[0] > 245 && l[2] < 10 && r[2] > 245 && r[0] < 10, "{l:?} {r:?}");
    assert!((m[0] as i32 - 128).abs() <= 3 && (m[2] as i32 - 128).abs() <= 3, "{m:?}");
    // Unfiltered: decoded at full size whatever the scale, and sampled without mips (it aliases where it shrinks).
    let layer = |r: &str| {
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none", "resample": r,
        "transform": {"scale": 0.25, "rotation_y": 10}}])
    };
    let e = engine("unfiltered-plan", &timeline(layer("unfiltered")));
    let plan = e.plan().unwrap().remove(0);
    let v = walk_items(&plan.desc.items).into_iter().find(|i| matches!(i, Item::Video { .. })).unwrap();
    let mut js = Js::new().unwrap();
    assert_eq!(unit::decode_size(v, &e.canvas, e.fps(), &mut js).unwrap(), (W, H));
    let (u, b) = (frame("unf", layer("unfiltered"), 0.0), frame("bil", layer("bilinear"), 0.0));
    let p = psnr(&u.px, &b.px, W, |_, _| true);
    assert!(p > 20.0 && p < 60.0, "same picture, sampled differently: {p:.1} dB");
}

#[test]
fn delogo_matches_ffmpeg() {
    let ours = frame(
        "delogo",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none",
        "delogo": [[200, 20, 60, 30]]}]),
        0.0,
    );
    let reference = ffmpeg_rgba("pattern.mkv", "delogo=x=200:y=20:w=60:h=30", 0);
    let p = psnr(&ours.px, &reference, W, |_, _| true);
    assert!(p > 40.0, "{p:.1} dB");
    let plain = frame("nodelogo", json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none"}]), 0.0);
    assert!(psnr(&ours.px, &plain.px, W, |x, y| (200..260).contains(&x) && (20..50).contains(&y)) < 30.0);
}

#[test]
fn blur_and_shadow_on_footage_match_graphics() {
    let fx = json!({"position": [160, 90], "scale": 0.6});
    let v = frame(
        "blur-v",
        json!([{"id": "v", "type": "video", "asset": "proj:pattern", "fit": "none", "transform": fx,
        "blur": 3, "shadow": {"dx": 8, "dy": 10, "blur": 12, "color": "#ffffffa0"}}]),
        0.0,
    );
    let i = frame(
        "blur-i",
        json!([{"id": "i", "type": "image", "asset": "proj:still", "transform": fx,
        "blur": 3, "shadow": {"dx": 8, "dy": 10, "blur": 12, "color": "#ffffffa0"}}]),
        0.0,
    );
    let p = psnr(&v.px, &i.px, W, |_, _| true);
    assert!(p > 32.0, "{p:.1} dB");
    // The shadow shows below-right of the layer.
    assert_ne!(px(&v, 262, 150)[..3], [0, 0, 0]);
}

#[test]
fn expr_lib_feeds_expressions_and_keys() {
    let layers = json!([{"id": "t", "type": "shape", "shape": "rect", "size": [40, 40], "fill": "#ffffff",
        "transform": {"position": {"expr": "[at(t), 90]"}}}]);
    let mut doc = timeline(layers);
    doc["expr_lib"] = json!("const at = t => 40 + 100 * t;");
    doc["scenes"][0]["shots"][0]["picture"]["expr_lib"] = json!("const unused = 1;");
    let e = engine("lib", &doc);
    let k1 = e.plan().unwrap()[0].runs[0].key.clone();
    doc["expr_lib"] = json!("const at = t => 60 + 100 * t;");
    let e2 = engine("lib2", &doc);
    assert_ne!(k1, e2.plan().unwrap()[0].runs[0].key, "a changed library re-renders what uses it");
    let d = root().join("tl").join("lib3");
    std::fs::create_dir_all(&d).unwrap();
    std::fs::write(d.join("timeline.json"), doc.to_string()).unwrap();
    let f = {
        let e = engine("lib3", &doc);
        let u = e.res.units[0].clone();
        let p = e.plan_unit(&u).unwrap();
        let faces = e.resources.faces(p.runs.iter().flat_map(|r| r.layers.iter()));
        let mut painter = Painter::new(e.resources.clone(), &faces).unwrap();
        let mut js = Js::new().unwrap();
        let mut out = None;
        unit::compose(&p, 10, 11, Graphics::Live(&mut painter, &p.runs), &mut js, |fr| {
            out = Some(fr.clone());
            Ok(())
        })
        .unwrap();
        out.unwrap()
    };
    // t = 1: x = 160.
    assert_eq!(px(&f, 160, 90)[..3], [255, 255, 255]);
    assert_eq!(px(&f, 100, 90)[..3], [0, 0, 0]);
}
