//! Layer transforms, shared by the Skia graphics and the Rust compositor so both place a layer the same way.
//!
//! In composition pixels: `M = T(position) · R(rotation) · S(scale) · T(-anchor · box)`, where `box` is the layer's
//! content size. The render scale is applied on top by the caller.

use crate::composite::Affine;
use retake_timeline::EditError;
use retake_timeline::keyframe::{ExprEval, Prop};
use retake_timeline::layer::{Fit, Layer};
use serde_json::Value;

pub struct Placed {
    /// Content pixels → composition pixels.
    pub m: Affine,
    pub opacity: f64,
}

fn eval(p: &Option<Prop>, t: f64, props: &Value, ex: &mut dyn ExprEval) -> Result<Option<Vec<f64>>, EditError> {
    match p {
        Some(p) => p.eval(t, props, ex).map(Some),
        None => Ok(None),
    }
}

/// Places a layer whose content box is `w` × `h` on a `cw` × `ch` composition at time `t`.
pub fn place(
    l: &Layer,
    (w, h): (f64, f64),
    (cw, ch): (f64, f64),
    t: f64,
    props: &Value,
    ex: &mut dyn ExprEval,
) -> Result<Placed, EditError> {
    let tr = &l.transform;
    let at = |e: EditError| e.at(format!("layer {}", l.id));
    let [ax, ay] = tr.anchor.unwrap_or([0.5, 0.5]);
    let pos = eval(&tr.position, t, props, ex).map_err(at)?.unwrap_or_else(|| vec![cw / 2.0, ch / 2.0]);
    let sc = eval(&tr.scale, t, props, ex).map_err(at)?.unwrap_or_else(|| vec![1.0]);
    let rot = eval(&tr.rotation, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(0.0);
    let op = eval(&tr.opacity, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(1.0);
    let (sx, sy) = (sc[0], *sc.get(1).unwrap_or(&sc[0]));
    let (px, py) = (pos[0], *pos.get(1).unwrap_or(&0.0));
    let m = Affine::translate(px, py)
        .then(&Affine::rotate(rot))
        .then(&Affine::scale(sx, sy))
        .then(&Affine::translate(-ax * w, -ay * h));
    Ok(Placed { m, opacity: op.clamp(0.0, 1.0) })
}

/// The scale a video's cropped source (`sw` × `sh`) gets to fit a `cw` × `ch` composition.
pub fn fit_scale(fit: Fit, sw: f64, sh: f64, cw: f64, ch: f64) -> (f64, f64) {
    if sw <= 0.0 || sh <= 0.0 {
        return (1.0, 1.0);
    }
    match fit {
        Fit::Contain => {
            let f = (cw / sw).min(ch / sh);
            (f, f)
        }
        Fit::Cover => {
            let f = (cw / sw).max(ch / sh);
            (f, f)
        }
        Fit::Stretch => (cw / sw, ch / sh),
        Fit::None => (1.0, 1.0),
    }
}

/// The crop rectangle of a layer at `t`, defaulting to the whole `sw` × `sh` source, clamped inside it.
pub fn crop(l: &Layer, sw: f64, sh: f64, t: f64, props: &Value, ex: &mut dyn ExprEval) -> Result<[f64; 4], EditError> {
    let c = eval(&l.crop, t, props, ex).map_err(|e| e.at(format!("layer {}", l.id)))?;
    Ok(match c {
        Some(v) if v.len() == 4 => {
            let x = v[0].clamp(0.0, sw);
            let y = v[1].clamp(0.0, sh);
            [x, y, v[2].clamp(0.0, sw - x), v[3].clamp(0.0, sh - y)]
        }
        _ => [0.0, 0.0, sw, sh],
    })
}

/// Axis-aligned bounding box of a rectangle under `m`, clipped to the frame: [x, y, w, h].
pub fn bbox(m: &Affine, r: [f64; 4], fw: f64, fh: f64) -> Option<[f64; 4]> {
    let pts = [
        m.apply(r[0], r[1]),
        m.apply(r[0] + r[2], r[1]),
        m.apply(r[0], r[1] + r[3]),
        m.apply(r[0] + r[2], r[1] + r[3]),
    ];
    let x0 = pts.iter().map(|p| p.0).fold(f64::INFINITY, f64::min).max(0.0);
    let y0 = pts.iter().map(|p| p.1).fold(f64::INFINITY, f64::min).max(0.0);
    let x1 = pts.iter().map(|p| p.0).fold(f64::NEG_INFINITY, f64::max).min(fw);
    let y1 = pts.iter().map(|p| p.1).fold(f64::NEG_INFINITY, f64::max).min(fh);
    if x1 <= x0 || y1 <= y0 { None } else { Some([x0, y0, x1 - x0, y1 - y0]) }
}

#[cfg(test)]
mod tests {
    use super::*;
    use retake_timeline::keyframe::NoExpr;
    use retake_timeline::layer::parse_layer;
    use serde_json::json;

    #[test]
    fn defaults_center_the_box() {
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [100, 50]})).unwrap();
        let p = place(&l, (100.0, 50.0), (1920.0, 1080.0), 0.0, &json!({}), &mut NoExpr).unwrap();
        assert_eq!(p.m.apply(0.0, 0.0), (910.0, 515.0));
        assert_eq!(p.opacity, 1.0);
    }

    #[test]
    fn anchor_position_scale_rotation() {
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [100, 50],
            "transform": {"anchor": [0, 0], "position": [10, 20], "scale": [2, 3], "rotation": 90,
                          "opacity": {"keys": [{"t": 0, "v": 0}, {"t": 1, "v": 1}]}}}))
        .unwrap();
        let p = place(&l, (100.0, 50.0), (1920.0, 1080.0), 0.25, &json!({}), &mut NoExpr).unwrap();
        assert_eq!(p.m.apply(0.0, 0.0), (10.0, 20.0));
        let (x, y) = p.m.apply(1.0, 0.0);
        assert!((x - 10.0).abs() < 1e-9 && (y - 22.0).abs() < 1e-9, "x scaled 2, then rotated to +y");
        assert!((p.opacity - 0.25).abs() < 1e-12);
    }

    #[test]
    fn fits() {
        assert_eq!(fit_scale(Fit::Contain, 1920.0, 1080.0, 960.0, 960.0), (0.5, 0.5));
        assert_eq!(fit_scale(Fit::Cover, 1920.0, 1080.0, 960.0, 960.0), (960.0 / 1080.0, 960.0 / 1080.0));
        assert_eq!(fit_scale(Fit::Stretch, 100.0, 100.0, 200.0, 50.0), (2.0, 0.5));
        assert_eq!(fit_scale(Fit::None, 100.0, 100.0, 200.0, 50.0), (1.0, 1.0));
    }

    #[test]
    fn crop_clamps_and_bbox_clips() {
        let l = parse_layer(&json!({"id": "v", "type": "video", "asset": "x", "crop": [-10, 10, 5000, 50]})).unwrap();
        assert_eq!(crop(&l, 640.0, 360.0, 0.0, &json!({}), &mut NoExpr).unwrap(), [0.0, 10.0, 640.0, 50.0]);
        let b = bbox(&Affine::translate(-5.0, 5.0), [0.0, 0.0, 20.0, 10.0], 100.0, 100.0).unwrap();
        assert_eq!(b, [0.0, 5.0, 15.0, 10.0]);
        assert!(bbox(&Affine::translate(500.0, 0.0), [0.0, 0.0, 20.0, 10.0], 100.0, 100.0).is_none());
    }
}
