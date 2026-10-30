//! Layer transforms, shared by the Skia graphics and the Rust compositor so both place a layer the same way.
//!
//! In composition pixels: `M = T(position) · P · R(rotation) · S(scale) · T(-anchor · box)`, where `box` is the
//! layer's content size and `P` is the perspective projection of `rotation_x` / `rotation_y` (identity without them),
//! or the corner pin's homography alone. The render scale is applied on top by the caller.

use crate::composite::Affine;
use retake_timeline::EditError;
use retake_timeline::keyframe::{ExprEval, Prop};
use retake_timeline::layer::{Fit, Layer};
use serde_json::Value;

/// A projective map of the plane (a homography), row-major: `(x, y) ↦ ((a·x + b·y + c) / w, (d·x + e·y + f) / w)`
/// with `w = g·x + h·y + i`.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Proj(pub [f64; 9]);

impl Proj {
    pub const IDENTITY: Proj = Proj([1.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 1.0]);

    pub fn from_affine(m: &Affine) -> Proj {
        Proj([m.a, m.c, m.e, m.b, m.d, m.f, 0.0, 0.0, 1.0])
    }

    /// The affine map, when the bottom row is (0, 0, 1) (up to rounding).
    pub fn affine(&self) -> Option<Affine> {
        let m = &self.0;
        if m[6].abs() > 1e-12 || m[7].abs() > 1e-12 || (m[8] - 1.0).abs() > 1e-12 {
            return None;
        }
        Some(Affine { a: m[0], b: m[3], c: m[1], d: m[4], e: m[2], f: m[5] })
    }

    /// `self ∘ o`: apply `o` first.
    pub fn then(&self, o: &Proj) -> Proj {
        let (a, b) = (&self.0, &o.0);
        let mut r = [0.0; 9];
        for i in 0..3 {
            for j in 0..3 {
                r[i * 3 + j] = (0..3).map(|k| a[i * 3 + k] * b[k * 3 + j]).sum();
            }
        }
        Proj(r)
    }

    pub fn then_affine(&self, o: &Affine) -> Proj {
        self.then(&Proj::from_affine(o))
    }

    /// The image of a point, or None when it lands behind the viewer (w ≤ 0).
    pub fn apply(&self, x: f64, y: f64) -> Option<(f64, f64)> {
        let m = &self.0;
        let w = m[6] * x + m[7] * y + m[8];
        if w <= 1e-9 {
            return None;
        }
        Some(((m[0] * x + m[1] * y + m[2]) / w, (m[3] * x + m[4] * y + m[5]) / w))
    }

    pub fn invert(&self) -> Option<Proj> {
        let m = &self.0;
        let c00 = m[4] * m[8] - m[5] * m[7];
        let c01 = m[5] * m[6] - m[3] * m[8];
        let c02 = m[3] * m[7] - m[4] * m[6];
        let det = m[0] * c00 + m[1] * c01 + m[2] * c02;
        if det.abs() < 1e-18 {
            return None;
        }
        let inv = [
            c00,
            m[2] * m[7] - m[1] * m[8],
            m[1] * m[5] - m[2] * m[4],
            c01,
            m[0] * m[8] - m[2] * m[6],
            m[2] * m[3] - m[0] * m[5],
            c02,
            m[1] * m[6] - m[0] * m[7],
            m[0] * m[4] - m[1] * m[3],
        ];
        Some(Proj(inv.map(|x| x / det)))
    }

    /// The homography that maps the `w` × `h` box's corners (top-left, top-right, bottom-right, bottom-left) onto
    /// `q` (Heckbert's square-to-quad).
    pub fn box_to_quad(w: f64, h: f64, q: &[f64]) -> Option<Proj> {
        if q.len() != 8 || w <= 0.0 || h <= 0.0 {
            return None;
        }
        let (x0, y0, x1, y1, x2, y2, x3, y3) = (q[0], q[1], q[2], q[3], q[4], q[5], q[6], q[7]);
        let (sx, sy) = (x0 - x1 + x2 - x3, y0 - y1 + y2 - y3);
        let unit = if sx.abs() < 1e-12 && sy.abs() < 1e-12 {
            Proj([x1 - x0, x3 - x0, x0, y1 - y0, y3 - y0, y0, 0.0, 0.0, 1.0])
        } else {
            let (dx1, dx2, dy1, dy2) = (x1 - x2, x3 - x2, y1 - y2, y3 - y2);
            let den = dx1 * dy2 - dx2 * dy1;
            if den.abs() < 1e-12 {
                return None;
            }
            let g = (sx * dy2 - dx2 * sy) / den;
            let h = (dx1 * sy - sx * dy1) / den;
            Proj([x1 - x0 + g * x1, x3 - x0 + h * x3, x0, y1 - y0 + g * y1, y3 - y0 + h * y3, y0, g, h, 1.0])
        };
        Some(unit.then_affine(&Affine::scale(1.0 / w, 1.0 / h)))
    }

    /// The projection of the plane turned by `rx` then (applied first) `ry` degrees, seen from `d` pixels in front of
    /// it, about the origin (CSS `perspective(d) rotateX(rx) rotateY(ry)`).
    pub fn turn(rx: f64, ry: f64, d: f64) -> Proj {
        let (sx, cx) = rx.to_radians().sin_cos();
        let (sy, cy) = ry.to_radians().sin_cos();
        // R = Rx · Ry, with z toward the viewer.
        let r = [[cy, 0.0, sy], [sx * sy, cx, -sx * cy], [-cx * sy, sx, cx * cy]];
        let d = if d > 1e-6 { d } else { 1e-6 };
        // (x, y, 0) ↦ (X, Y, Z); projected: d·(X, Y) / (d − Z).
        Proj([r[0][0], r[0][1], 0.0, r[1][0], r[1][1], 0.0, -r[2][0] / d, -r[2][1] / d, 1.0])
    }

    /// The bounding box [x, y, w, h] of a rectangle's image, clipped to the frame (None when nothing shows).
    pub fn bbox(&self, r: [f64; 4], fw: f64, fh: f64) -> Option<[f64; 4]> {
        let pts: Vec<(f64, f64)> = [(r[0], r[1]), (r[0] + r[2], r[1]), (r[0], r[1] + r[3]), (r[0] + r[2], r[1] + r[3])]
            .iter()
            .filter_map(|&(x, y)| self.apply(x, y))
            .collect();
        if pts.len() < 4 {
            // A corner behind the viewer: the whole frame is the safe answer.
            return if pts.is_empty() { None } else { Some([0.0, 0.0, fw, fh]) };
        }
        let x0 = pts.iter().map(|p| p.0).fold(f64::INFINITY, f64::min).max(0.0);
        let y0 = pts.iter().map(|p| p.1).fold(f64::INFINITY, f64::min).max(0.0);
        let x1 = pts.iter().map(|p| p.0).fold(f64::NEG_INFINITY, f64::max).min(fw);
        let y1 = pts.iter().map(|p| p.1).fold(f64::NEG_INFINITY, f64::max).min(fh);
        if x1 <= x0 || y1 <= y0 { None } else { Some([x0, y0, x1 - x0, y1 - y0]) }
    }
}

pub struct Placed {
    /// Content pixels → composition pixels.
    pub m: Proj,
    pub opacity: f64,
    /// Content pixels → composition pixels per axis before any perspective: the size the content is resampled to
    /// (`resample: lanczos`) and decoded at.
    pub k2d: (f64, f64),
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
    let op = eval(&tr.opacity, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(1.0);
    let opacity = op.clamp(0.0, 1.0);
    if let Some(q) = eval(&tr.corners, t, props, ex).map_err(at)? {
        let m = Proj::box_to_quad(w, h, &q).ok_or_else(|| {
            EditError::new("corners_degenerate", format!("layer {}: its corners don't make a quad: {q:?}", l.id))
        })?;
        let len = |i: usize, j: usize| (q[2 * i] - q[2 * j]).hypot(q[2 * i + 1] - q[2 * j + 1]);
        let k2d = (len(0, 1).max(len(3, 2)) / w.max(1e-9), len(0, 3).max(len(1, 2)) / h.max(1e-9));
        return Ok(Placed { m, opacity, k2d });
    }
    let [ax, ay] = tr.anchor.unwrap_or([0.5, 0.5]);
    let pos = eval(&tr.position, t, props, ex).map_err(at)?.unwrap_or_else(|| vec![cw / 2.0, ch / 2.0]);
    let sc = eval(&tr.scale, t, props, ex).map_err(at)?.unwrap_or_else(|| vec![1.0]);
    let rot = eval(&tr.rotation, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(0.0);
    let rx = eval(&tr.rotation_x, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(0.0);
    let ry = eval(&tr.rotation_y, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or(0.0);
    let (sx, sy) = (sc[0], *sc.get(1).unwrap_or(&sc[0]));
    let (px, py) = (pos[0], *pos.get(1).unwrap_or(&0.0));
    let local = Affine::rotate(rot).then(&Affine::scale(sx, sy)).then(&Affine::translate(-ax * w, -ay * h));
    let m = if rx != 0.0 || ry != 0.0 {
        let d = eval(&tr.perspective, t, props, ex).map_err(at)?.map(|v| v[0]).unwrap_or_else(|| cw.hypot(ch));
        Proj::from_affine(&Affine::translate(px, py)).then(&Proj::turn(rx, ry, d)).then_affine(&local)
    } else {
        Proj::from_affine(&Affine::translate(px, py).then(&local))
    };
    Ok(Placed { m, opacity, k2d: (sx.abs(), sy.abs()) })
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
    Proj::from_affine(m).bbox(r, fw, fh)
}

#[cfg(test)]
mod tests {
    use super::*;
    use retake_timeline::keyframe::NoExpr;
    use retake_timeline::layer::parse_layer;
    use serde_json::json;

    fn close(a: (f64, f64), b: (f64, f64)) -> bool {
        (a.0 - b.0).abs() < 1e-6 && (a.1 - b.1).abs() < 1e-6
    }

    #[test]
    fn defaults_center_the_box() {
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [100, 50]})).unwrap();
        let p = place(&l, (100.0, 50.0), (1920.0, 1080.0), 0.0, &json!({}), &mut NoExpr).unwrap();
        assert_eq!(p.m.apply(0.0, 0.0), Some((910.0, 515.0)));
        assert_eq!(p.opacity, 1.0);
        assert!(p.m.affine().is_some());
    }

    #[test]
    fn anchor_position_scale_rotation() {
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [100, 50],
            "transform": {"anchor": [0, 0], "position": [10, 20], "scale": [2, 3], "rotation": 90,
                          "opacity": {"keys": [{"t": 0, "v": 0}, {"t": 1, "v": 1}]}}}))
        .unwrap();
        let p = place(&l, (100.0, 50.0), (1920.0, 1080.0), 0.25, &json!({}), &mut NoExpr).unwrap();
        assert_eq!(p.m.apply(0.0, 0.0), Some((10.0, 20.0)));
        let (x, y) = p.m.apply(1.0, 0.0).unwrap();
        assert!((x - 10.0).abs() < 1e-9 && (y - 22.0).abs() < 1e-9, "x scaled 2, then rotated to +y");
        assert!((p.opacity - 0.25).abs() < 1e-12);
        assert_eq!(p.k2d, (2.0, 3.0));
    }

    #[test]
    fn corners_pin_the_box() {
        let q = [100.0, 50.0, 400.0, 80.0, 380.0, 300.0, 120.0, 260.0];
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [200, 100],
            "transform": {"corners": q}}))
        .unwrap();
        let p = place(&l, (200.0, 100.0), (640.0, 360.0), 0.0, &json!({}), &mut NoExpr).unwrap();
        for (i, (x, y)) in [(0.0, 0.0), (200.0, 0.0), (200.0, 100.0), (0.0, 100.0)].into_iter().enumerate() {
            assert!(close(p.m.apply(x, y).unwrap(), (q[2 * i], q[2 * i + 1])), "corner {i}");
        }
        assert!(p.m.affine().is_none());
        // The inverse maps back.
        let inv = p.m.invert().unwrap();
        let (x, y) = p.m.apply(37.0, 61.0).unwrap();
        assert!(close(inv.apply(x, y).unwrap(), (37.0, 61.0)));
        // An affine quad stays affine.
        let par = Proj::box_to_quad(10.0, 10.0, &[0.0, 0.0, 20.0, 0.0, 30.0, 10.0, 10.0, 10.0]).unwrap();
        assert!(par.affine().is_some());
    }

    #[test]
    fn rotation_y_recedes_the_right_edge() {
        // CSS: perspective(1500px) rotateY(24deg) about the center of a 600 x 400 box at (700, 500).
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [600, 400],
            "transform": {"position": [700, 500], "rotation_y": 24, "perspective": 1500}}))
        .unwrap();
        let p = place(&l, (600.0, 400.0), (1920.0, 1080.0), 0.0, &json!({}), &mut NoExpr).unwrap();
        assert!(close(p.m.apply(300.0, 200.0).unwrap(), (700.0, 500.0)), "the anchor stays put");
        let (tl, tr) = (p.m.apply(0.0, 0.0).unwrap(), p.m.apply(600.0, 0.0).unwrap());
        let (bl, br) = (p.m.apply(0.0, 400.0).unwrap(), p.m.apply(600.0, 400.0).unwrap());
        let (left, right) = (bl.1 - tl.1, br.1 - tr.1);
        assert!(right < 400.0 && left > 400.0, "the right edge is farther, so shorter: {left} {right}");
        // The exact projection: x = 300 cos 24°, z = -300 sin 24° (away), x' = 1500 x / (1500 - z).
        let (s, c) = 24f64.to_radians().sin_cos();
        let x = 700.0 + 1500.0 * 300.0 * c / (1500.0 + 300.0 * s);
        assert!((tr.0 - x).abs() < 1e-6, "{} vs {x}", tr.0);
        // rotation_x: positive tips the top edge away (the top gets narrower).
        let l = parse_layer(&json!({"id": "a", "type": "shape", "shape": "rect", "size": [600, 400],
            "transform": {"position": [700, 500], "rotation_x": 20, "perspective": 1500}}))
        .unwrap();
        let p = place(&l, (600.0, 400.0), (1920.0, 1080.0), 0.0, &json!({}), &mut NoExpr).unwrap();
        let top = p.m.apply(600.0, 0.0).unwrap().0 - p.m.apply(0.0, 0.0).unwrap().0;
        let bottom = p.m.apply(600.0, 400.0).unwrap().0 - p.m.apply(0.0, 400.0).unwrap().0;
        assert!(top < 600.0 && bottom > 600.0, "{top} {bottom}");
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
