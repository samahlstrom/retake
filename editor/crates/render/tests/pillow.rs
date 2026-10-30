//! Text and resampling fidelity against Pillow, the tool many existing drawing scripts use. Pillow renders the same
//! strings (tests/pillow_text.py) and resizes the same pixels (tests/pillow_resize.py); the tests measure how far the
//! engine's default text and its `metrics: "pillow"` text land from Pillow's glyphs, and check that `resample:
//! lanczos` is Pillow's LANCZOS. Skipped (with a message) where no Python with Pillow is found: set
//! RETAKE_PILLOW_PYTHON to a Python that has it. Pillow is only a tool here; nothing of it is vendored.

use retake_render::graphics::{DISPLAY_FONT, DISPLAY_WEIGHT, FontData, Painter, Resources};
use retake_render::resample::lanczos_rgba;
use retake_timeline::layer::parse_layer;
use serde_json::json;
use std::path::PathBuf;
use std::process::Command;
use std::sync::Arc;

fn python() -> Option<String> {
    let cands = [std::env::var("RETAKE_PILLOW_PYTHON").ok(), Some("python3".into())];
    cands
        .into_iter()
        .flatten()
        .find(|p| Command::new(p).args(["-c", "import PIL"]).output().map(|o| o.status.success()).unwrap_or(false))
}

fn scratch() -> PathBuf {
    let d = PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("pillow");
    std::fs::create_dir_all(&d).unwrap();
    d
}

fn font_path() -> PathBuf {
    let p = scratch().join("Baloo2-Variable.ttf");
    std::fs::write(&p, DISPLAY_FONT).unwrap();
    p
}

fn helper(name: &str) -> String {
    format!("{}/tests/{name}", env!("CARGO_MANIFEST_DIR"))
}

const W: u32 = 900;
const H: u32 = 240;

/// Pillow's alpha for a line of text centred ("mm") at (x, y).
fn pillow_alpha(py: &str, text: &str, size: u32, stroke: u32, x: f64, y: f64) -> Vec<u8> {
    let out = scratch().join("text.rgba");
    let st = Command::new(py)
        .arg(helper("pillow_text.py"))
        .arg(font_path())
        .args([&DISPLAY_WEIGHT.to_string(), &size.to_string(), &stroke.to_string(), &W.to_string(), &H.to_string()])
        .args([&x.to_string(), &y.to_string(), text])
        .arg(&out)
        .status()
        .unwrap();
    assert!(st.success());
    std::fs::read(&out).unwrap().chunks(4).map(|p| p[3]).collect()
}

/// The engine's alpha for the same line, its box centred at (x, y).
fn engine_alpha(text: &str, size: u32, stroke: u32, x: f64, y: f64, metrics: Option<&str>) -> Vec<u8> {
    let mut l = json!({"id": "t", "type": "text", "text": text, "size": size, "line_height": 1.0,
        "transform": {"position": [x, y]}});
    if stroke > 0 {
        l["stroke"] = json!({"color": "#000000", "width": stroke});
    }
    if let Some(m) = metrics {
        l["metrics"] = json!(m);
    }
    let l = parse_layer(&l).unwrap();
    let mut r = Resources::default();
    r.fonts.insert(
        "display".into(),
        FontData { bytes: Arc::new(DISPLAY_FONT.to_vec()), hash: "x".into(), weight: DISPLAY_WEIGHT },
    );
    let r = Arc::new(r);
    let faces = r.faces(std::slice::from_ref(&l));
    let mut p = Painter::new(r, &faces).unwrap();
    let px = p.render(std::slice::from_ref(&l), &json!({}), (W, H), 1.0, (W as f64, H as f64), 0.0).unwrap();
    px.chunks(4).map(|p| p[3]).collect()
}

/// Alpha-weighted centroid.
fn centroid(a: &[u8]) -> (f64, f64) {
    let (mut sx, mut sy, mut s) = (0.0, 0.0, 0.0);
    for (i, v) in a.iter().enumerate() {
        let v = *v as f64;
        sx += (i as u32 % W) as f64 * v;
        sy += (i as u32 / W) as f64 * v;
        s += v;
    }
    (sx / s, sy / s)
}

fn psnr(a: &[u8], b: &[u8]) -> f64 {
    let mse = a.iter().zip(b).map(|(x, y)| (*x as f64 - *y as f64).powi(2)).sum::<f64>() / a.len() as f64;
    if mse == 0.0 { 99.0 } else { 10.0 * (255.0f64 * 255.0 / mse).log10() }
}

#[test]
fn text_lands_where_pillow_puts_it() {
    let Some(py) = python() else {
        eprintln!("skipped: no Python with Pillow (set RETAKE_PILLOW_PYTHON)");
        return;
    };
    let cases =
        [("FASTER", 120, 0), ("THE BIG 3", 96, 0), ("Hello, Wörld", 80, 0), ("ÉCLAIR 42%", 64, 0), ("GUN", 80, 6)];
    let (mut worst_default, mut worst_pillow) = (0f64, 0f64);
    let (mut sum_default, mut sum_pillow) = (0f64, 0f64);
    let mut worst_dy = 0f64;
    for (text, size, stroke) in cases {
        let (x, y) = (450.0, 120.0);
        let pil = pillow_alpha(&py, text, size, stroke, x, y);
        let (px, pyy) = centroid(&pil);
        for (metrics, worst, sum) in
            [(None, &mut worst_default, &mut sum_default), (Some("pillow"), &mut worst_pillow, &mut sum_pillow)]
        {
            let ours = engine_alpha(text, size, stroke, x, y, metrics);
            let (ox, oy) = centroid(&ours);
            let (dx, dy) = (ox - px, oy - pyy);
            let p = psnr(&ours, &pil);
            eprintln!(
                "{text:>14} {size:>3}px {:>7}: offset ({dx:+.2}, {dy:+.2}) px, alpha PSNR {p:.1} dB",
                metrics.unwrap_or("skia")
            );
            *worst = worst.max(dx.abs()).max(dy.abs());
            *sum += p;
            if metrics.is_some() {
                worst_dy = worst_dy.max(dy.abs());
            }
        }
    }
    let n = cases.len() as f64;
    eprintln!(
        "worst offset: default {worst_default:.2} px, pillow {worst_pillow:.2} px; mean alpha PSNR: default {:.1} dB, \
         pillow {:.1} dB",
        sum_default / n,
        sum_pillow / n
    );
    // Measured (Pillow 13, FreeType 2.14, raqm): the vertical placement is Pillow's to a few hundredths of a pixel;
    // horizontally a string can still drift by up to ~1.4 px (advances: Pillow shapes with HarfBuzz over FreeType,
    // Skia with its own font functions), so the line is centred like Pillow's but not every glyph lands on its pixel.
    assert!(worst_dy <= 0.15, "metrics: pillow puts lines where Pillow does vertically ({worst_dy:.2} px)");
    assert!(worst_pillow <= 1.5, "and within a pixel and a half horizontally ({worst_pillow:.2} px)");
    assert!(sum_pillow >= sum_default + 2.0 * n, "and draws at least 2 dB closer to Pillow than the default");
}

#[test]
fn lanczos_is_pillows() {
    let Some(py) = python() else {
        eprintln!("skipped: no Python with Pillow (set RETAKE_PILLOW_PYTHON)");
        return;
    };
    let (w, h) = (97u32, 61u32);
    let rgb: Vec<u8> = (0..w * h)
        .flat_map(|i| {
            let (x, y) = ((i % w) as f64, (i / w) as f64);
            [
                (128.0 + 120.0 * (x / 3.1).sin()) as u8,
                (128.0 + 120.0 * (y / 2.3).cos()) as u8,
                ((x * 7.0 + y * 13.0) as u32 % 256) as u8,
            ]
        })
        .collect();
    let src = scratch().join("src.rgb");
    std::fs::write(&src, &rgb).unwrap();
    let rgba: Vec<u8> = rgb.chunks(3).flat_map(|p| [p[0], p[1], p[2], 255]).collect();
    for (bx, (ow, oh)) in
        [([0.0, 0.0, 97.0, 61.0], (40, 25)), ([10.5, 3.0, 80.0, 58.25], (150, 77)), ([0.0, 0.0, 97.0, 61.0], (97, 30))]
    {
        let out = scratch().join("out.rgb");
        let st = Command::new(&py)
            .arg(helper("pillow_resize.py"))
            .arg(&src)
            .args([w, h].map(|x| x.to_string()))
            .args(bx.map(|x: f64| x.to_string()))
            .args([ow, oh].map(|x: u32| x.to_string()))
            .arg(&out)
            .status()
            .unwrap();
        assert!(st.success());
        let pil = std::fs::read(&out).unwrap();
        let ours: Vec<u8> = lanczos_rgba(&rgba, w, h, bx, ow, oh).chunks(4).flat_map(|p| [p[0], p[1], p[2]]).collect();
        let worst = pil.iter().zip(&ours).map(|(a, b)| (*a as i32 - *b as i32).abs()).max().unwrap();
        assert_eq!(worst, 0, "box {bx:?} -> {ow}x{oh}: Pillow's LANCZOS to the byte");
    }
}
