//! The compositor: premultiplied RGBA frames, affine transforms with sub-pixel bilinear sampling (box-filtered mip
//! levels when shrinking), crop, opacity and alpha "over". Rows run in parallel.

use rayon::prelude::*;

/// A premultiplied RGBA8 frame.
#[derive(Clone, Debug, PartialEq)]
pub struct Frame {
    pub w: u32,
    pub h: u32,
    pub px: Vec<u8>,
}

/// Premultiplies a straight-alpha color.
pub fn premul(c: [u8; 4]) -> [u8; 4] {
    let a = c[3] as u32;
    let m = |x: u8| ((x as u32 * a + 127) / 255) as u8;
    [m(c[0]), m(c[1]), m(c[2]), c[3]]
}

impl Frame {
    pub fn new(w: u32, h: u32) -> Frame {
        Frame { w, h, px: vec![0; (w * h * 4) as usize] }
    }

    pub fn solid(w: u32, h: u32, color: [u8; 4]) -> Frame {
        let mut f = Frame::new(w, h);
        f.fill_over(color);
        f
    }

    /// Composites a solid straight-alpha color over the whole frame.
    pub fn fill_over(&mut self, color: [u8; 4]) {
        let c = premul(color);
        let ia = 255 - c[3] as u32;
        self.px.par_chunks_mut(4).for_each(|d| {
            for k in 0..4 {
                d[k] = (c[k] as u32 + (d[k] as u32 * ia + 127) / 255) as u8;
            }
        });
    }

    /// Composites a premultiplied patch (`pw` × `ph`) over the frame at (x0, y0), clipped.
    pub fn over_patch(&mut self, patch: &[u8], x0: i32, y0: i32, pw: u32, ph: u32) {
        let (fw, fh) = (self.w as i32, self.h as i32);
        let xs = x0.max(0);
        let xe = (x0 + pw as i32).min(fw);
        if xe <= xs {
            return;
        }
        let rows = (y0.max(0)..(y0 + ph as i32).min(fh)).collect::<Vec<_>>();
        let stride = (self.w * 4) as usize;
        let ptr = SendPtr(self.px.as_mut_ptr());
        rows.par_iter().for_each(|&y| {
            let p = &ptr;
            // SAFETY: each row index is visited once, so the row slices don't overlap.
            let row = unsafe { std::slice::from_raw_parts_mut(p.0.add(y as usize * stride), stride) };
            let sy = (y - y0) as usize;
            for x in xs..xe {
                let s = &patch[(sy * pw as usize + (x - x0) as usize) * 4..][..4];
                let sa = s[3] as u32;
                if sa == 0 {
                    continue;
                }
                let d = &mut row[x as usize * 4..][..4];
                if sa == 255 {
                    d.copy_from_slice(s);
                } else {
                    let ia = 255 - sa;
                    for k in 0..4 {
                        d[k] = (s[k] as u32 + (d[k] as u32 * ia + 127) / 255).min(255) as u8;
                    }
                }
            }
        });
    }
}

struct SendPtr(*mut u8);
unsafe impl Sync for SendPtr {}
unsafe impl Send for SendPtr {}

/// A 2D affine map: x' = a·x + c·y + e, y' = b·x + d·y + f.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Affine {
    pub a: f64,
    pub b: f64,
    pub c: f64,
    pub d: f64,
    pub e: f64,
    pub f: f64,
}

impl Affine {
    pub const IDENTITY: Affine = Affine { a: 1.0, b: 0.0, c: 0.0, d: 1.0, e: 0.0, f: 0.0 };

    pub fn translate(x: f64, y: f64) -> Affine {
        Affine { e: x, f: y, ..Affine::IDENTITY }
    }
    pub fn scale(sx: f64, sy: f64) -> Affine {
        Affine { a: sx, d: sy, ..Affine::IDENTITY }
    }
    /// Clockwise, in degrees (y points down).
    pub fn rotate(deg: f64) -> Affine {
        let (s, c) = deg.to_radians().sin_cos();
        Affine { a: c, b: s, c: -s, d: c, e: 0.0, f: 0.0 }
    }
    /// `self ∘ other`: apply `other` first, then `self`.
    pub fn then(&self, o: &Affine) -> Affine {
        // self * o
        Affine {
            a: self.a * o.a + self.c * o.b,
            b: self.b * o.a + self.d * o.b,
            c: self.a * o.c + self.c * o.d,
            d: self.b * o.c + self.d * o.d,
            e: self.a * o.e + self.c * o.f + self.e,
            f: self.b * o.e + self.d * o.f + self.f,
        }
    }
    pub fn apply(&self, x: f64, y: f64) -> (f64, f64) {
        (self.a * x + self.c * y + self.e, self.b * x + self.d * y + self.f)
    }
    pub fn det(&self) -> f64 {
        self.a * self.d - self.b * self.c
    }
    pub fn invert(&self) -> Option<Affine> {
        let det = self.det();
        if det.abs() < 1e-12 {
            return None;
        }
        let (a, b, c, d) = (self.d / det, -self.b / det, -self.c / det, self.a / det);
        Some(Affine { a, b, c, d, e: -(a * self.e + c * self.f), f: -(b * self.e + d * self.f) })
    }
    /// True for an integer translation with no scale or rotation.
    pub fn is_integer_translation(&self) -> bool {
        self.a == 1.0
            && self.d == 1.0
            && self.b == 0.0
            && self.c == 0.0
            && self.e.fract() == 0.0
            && self.f.fract() == 0.0
    }
}

/// Halves an RGBA image with a 2×2 box filter.
fn half(src: &[u8], w: u32, h: u32) -> (Vec<u8>, u32, u32) {
    let (nw, nh) = ((w / 2).max(1), (h / 2).max(1));
    let mut out = vec![0u8; (nw * nh * 4) as usize];
    out.par_chunks_mut((nw * 4) as usize).enumerate().for_each(|(y, row)| {
        for x in 0..nw as usize {
            for k in 0..4 {
                let px = |xx: usize, yy: usize| {
                    let xx = xx.min(w as usize - 1);
                    let yy = yy.min(h as usize - 1);
                    src[(yy * w as usize + xx) * 4 + k] as u32
                };
                let s = px(2 * x, 2 * y) + px(2 * x + 1, 2 * y) + px(2 * x, 2 * y + 1) + px(2 * x + 1, 2 * y + 1);
                row[x * 4 + k] = ((s + 2) / 4) as u8;
            }
        }
    });
    (out, nw, nh)
}

/// Draws `src` (premultiplied RGBA, `sw` × `sh`) onto `dst`. `crop` is the source rectangle [x, y, w, h] in source
/// pixels; `m` maps source pixels to destination pixels; `opacity` scales alpha. Outside the crop the source is
/// transparent, so edges are anti-aliased by the bilinear filter.
pub fn draw_image(dst: &mut Frame, src: &[u8], sw: u32, sh: u32, crop: [f64; 4], m: &Affine, opacity: f64) {
    let opacity = opacity.clamp(0.0, 1.0);
    if opacity <= 0.0 || crop[2] <= 0.0 || crop[3] <= 0.0 {
        return;
    }
    // Fast path: an exact, whole-frame-aligned copy.
    if opacity >= 1.0 && m.is_integer_translation() && crop == [0.0, 0.0, sw as f64, sh as f64] {
        dst.over_patch(src, m.e as i32, m.f as i32, sw, sh);
        return;
    }
    let Some(inv) = m.invert() else { return };
    // Destination bounding box of the crop rectangle.
    let corners = [
        m.apply(crop[0], crop[1]),
        m.apply(crop[0] + crop[2], crop[1]),
        m.apply(crop[0], crop[1] + crop[3]),
        m.apply(crop[0] + crop[2], crop[1] + crop[3]),
    ];
    let x0 = corners.iter().map(|c| c.0).fold(f64::INFINITY, f64::min).floor().max(0.0) as i64 - 1;
    let x1 = corners.iter().map(|c| c.0).fold(f64::NEG_INFINITY, f64::max).ceil().min(dst.w as f64) as i64 + 1;
    let y0 = corners.iter().map(|c| c.1).fold(f64::INFINITY, f64::min).floor().max(0.0) as i64 - 1;
    let y1 = corners.iter().map(|c| c.1).fold(f64::NEG_INFINITY, f64::max).ceil().min(dst.h as f64) as i64 + 1;
    let (x0, x1) = (x0.max(0), x1.min(dst.w as i64));
    let (y0, y1) = (y0.max(0), y1.min(dst.h as i64));
    if x1 <= x0 || y1 <= y0 {
        return;
    }
    // Mip level: shrink the source with a box filter while the map shrinks it by more than 2×.
    let scale = m.det().abs().sqrt();
    let mut level: (std::borrow::Cow<[u8]>, u32, u32) = (std::borrow::Cow::Borrowed(src), sw, sh);
    let mut k = 1.0f64;
    while scale * k < 0.5 && level.1 > 1 && level.2 > 1 {
        let (b, w, h) = half(&level.0, level.1, level.2);
        level = (std::borrow::Cow::Owned(b), w, h);
        k *= 2.0;
    }
    let (buf, lw, lh) = (&level.0, level.1 as i64, level.2 as i64);
    let (kx, ky) = (sw as f64 / lw as f64, sh as f64 / lh as f64);
    // Crop rectangle in level pixels.
    let (cx0, cy0) = (crop[0] / kx, crop[1] / ky);
    let (cx1, cy1) = ((crop[0] + crop[2]) / kx, (crop[1] + crop[3]) / ky);
    let op = opacity as f32;
    let stride = (dst.w * 4) as usize;
    let ptr = SendPtr(dst.px.as_mut_ptr());
    (y0..y1).into_par_iter().for_each(|y| {
        let p = &ptr;
        // SAFETY: one row per task; rows don't overlap.
        let row = unsafe { std::slice::from_raw_parts_mut(p.0.add(y as usize * stride), stride) };
        for x in x0..x1 {
            let (u, v) = inv.apply(x as f64 + 0.5, y as f64 + 0.5);
            // Continuous level coordinates, then pixel-center convention.
            let (lu, lv) = (u / kx, v / ky);
            if lu < cx0 - 1.0 || lv < cy0 - 1.0 || lu > cx1 + 1.0 || lv > cy1 + 1.0 {
                continue;
            }
            let fx = lu - 0.5;
            let fy = lv - 0.5;
            let ix = fx.floor();
            let iy = fy.floor();
            let (tx, ty) = ((fx - ix) as f32, (fy - iy) as f32);
            let (ix, iy) = (ix as i64, iy as i64);
            let mut acc = [0f32; 4];
            for (dy, wy) in [(0i64, 1.0 - ty), (1, ty)] {
                for (dx, wx) in [(0i64, 1.0 - tx), (1, tx)] {
                    let w = wx * wy;
                    if w == 0.0 {
                        continue;
                    }
                    let (sx, sy) = (ix + dx, iy + dy);
                    // Texel centers outside the crop (or the image) are transparent.
                    let cxf = sx as f64 + 0.5;
                    let cyf = sy as f64 + 0.5;
                    if sx < 0 || sy < 0 || sx >= lw || sy >= lh || cxf < cx0 || cyf < cy0 || cxf > cx1 || cyf > cy1 {
                        continue;
                    }
                    let s = &buf[((sy * lw + sx) * 4) as usize..][..4];
                    for c in 0..4 {
                        acc[c] += s[c] as f32 * w;
                    }
                }
            }
            let sa = acc[3] * op;
            if sa <= 0.0 {
                continue;
            }
            let ia = 1.0 - sa / 255.0;
            let d = &mut row[x as usize * 4..][..4];
            for c in 0..4 {
                let v = acc[c] * op + d[c] as f32 * ia;
                d[c] = v.round().clamp(0.0, 255.0) as u8;
            }
        }
    });
}

/// Bounding box [x, y, w, h] of the non-transparent pixels of a premultiplied frame, if any.
pub fn alpha_bbox(px: &[u8], w: u32, h: u32) -> Option<[u32; 4]> {
    let rows: Vec<Option<(u32, u32)>> = px
        .par_chunks(w as usize * 4)
        .map(|r| {
            let first = r.as_chunks::<4>().0.iter().position(|p| p[3] != 0)?;
            let last = r.as_chunks::<4>().0.iter().rposition(|p| p[3] != 0)?;
            Some((first as u32, last as u32))
        })
        .collect();
    let ys: Vec<usize> = rows.iter().enumerate().filter(|(_, r)| r.is_some()).map(|(i, _)| i).collect();
    let (y0, y1) = (*ys.first()?, *ys.last()?);
    let x0 = rows.iter().flatten().map(|r| r.0).min()?;
    let x1 = rows.iter().flatten().map(|r| r.1).max()?;
    let _ = h;
    Some([x0, y0 as u32, x1 - x0 + 1, (y1 - y0 + 1) as u32])
}

/// Copies a rectangle out of a frame.
pub fn crop_px(px: &[u8], w: u32, r: [u32; 4]) -> Vec<u8> {
    let mut out = Vec::with_capacity((r[2] * r[3] * 4) as usize);
    for y in r[1]..r[1] + r[3] {
        let s = ((y * w + r[0]) * 4) as usize;
        out.extend_from_slice(&px[s..s + (r[2] * 4) as usize]);
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn solid(w: u32, h: u32, c: [u8; 4]) -> Vec<u8> {
        let p = premul(c);
        (0..w * h).flat_map(|_| p).collect()
    }

    #[test]
    fn over_math() {
        let mut f = Frame::solid(2, 1, [0, 0, 255, 255]);
        f.over_patch(&solid(1, 1, [255, 0, 0, 128]), 0, 0, 1, 1);
        assert_eq!(&f.px[0..4], &[128, 0, 127, 255]);
        assert_eq!(&f.px[4..8], &[0, 0, 255, 255], "outside the patch: untouched");
        // Fully transparent background, half-red over it.
        let mut g = Frame::new(1, 1);
        g.over_patch(&solid(1, 1, [255, 0, 0, 128]), 0, 0, 1, 1);
        assert_eq!(&g.px[..], &[128, 0, 0, 128]);
    }

    #[test]
    fn patches_clip_at_the_edges() {
        let mut f = Frame::solid(4, 4, [0, 0, 0, 255]);
        f.over_patch(&solid(3, 3, [255, 255, 255, 255]), -1, 2, 3, 3);
        let white: Vec<usize> = (0..16).filter(|i| f.px[i * 4] == 255).collect::<Vec<_>>();
        assert_eq!(white, vec![8, 9, 12, 13]);
    }

    #[test]
    fn integer_translation_is_exact() {
        let src: Vec<u8> = (0..16u8).flat_map(|i| [i * 10, i, 255 - i, 255]).collect();
        let mut a = Frame::solid(8, 8, [0, 0, 0, 255]);
        draw_image(&mut a, &src, 4, 4, [0.0, 0.0, 4.0, 4.0], &Affine::translate(2.0, 3.0), 1.0);
        for y in 0..4 {
            for x in 0..4 {
                let d = &a.px[(((y + 3) * 8 + x + 2) * 4) as usize..][..4];
                assert_eq!(d, &src[((y * 4 + x) * 4) as usize..][..4]);
            }
        }
        // The general path with an identity-like map gives the same pixels as the fast path.
        let mut b = Frame::solid(8, 8, [0, 0, 0, 255]);
        let almost = Affine { a: 1.0 + 1e-12, ..Affine::translate(2.0, 3.0) };
        draw_image(&mut b, &src, 4, 4, [0.0, 0.0, 4.0, 4.0], &almost, 1.0);
        for y in 0..4 {
            for x in 0..4 {
                let i = (((y + 3) * 8 + x + 2) * 4) as usize;
                for c in 0..3 {
                    assert!((a.px[i + c] as i32 - b.px[i + c] as i32).abs() <= 1);
                }
            }
        }
    }

    #[test]
    fn subpixel_translation_blends_neighbours() {
        let src = solid(1, 1, [255, 255, 255, 255]);
        let mut f = Frame::solid(3, 1, [0, 0, 0, 255]);
        draw_image(&mut f, &src, 1, 1, [0.0, 0.0, 1.0, 1.0], &Affine::translate(0.5, 0.0), 1.0);
        // Half of the pixel lands on x = 0 and half on x = 1.
        assert!((f.px[0] as i32 - 128).abs() <= 1, "{}", f.px[0]);
        assert!((f.px[4] as i32 - 128).abs() <= 1, "{}", f.px[4]);
        assert_eq!(f.px[8], 0);
    }

    #[test]
    fn opacity_crop_and_scale() {
        let src = solid(4, 4, [255, 0, 0, 255]);
        let mut f = Frame::solid(8, 8, [0, 0, 0, 255]);
        draw_image(&mut f, &src, 4, 4, [0.0, 0.0, 4.0, 4.0], &Affine::scale(2.0, 2.0), 0.5);
        assert!((f.px[(4 * 8 + 4) * 4] as i32 - 128).abs() <= 1, "half-opaque red over black");
        // Crop keeps only the left half of the source.
        let mut g = Frame::solid(4, 4, [0, 0, 0, 255]);
        draw_image(&mut g, &src, 4, 4, [0.0, 0.0, 2.0, 4.0], &Affine::IDENTITY, 1.0);
        assert_eq!(g.px[(4 + 1) * 4], 255);
        assert_eq!(g.px[(4 + 3) * 4], 0);
    }

    #[test]
    fn shrinking_uses_mips_and_keeps_the_average() {
        // A checkerboard shrunk 8×: mip box filtering gives mid-gray, not aliasing.
        let w = 64;
        let src: Vec<u8> = (0..w * w)
            .flat_map(|i| if (i % w + i / w) % 2 == 0 { [255, 255, 255, 255] } else { [0, 0, 0, 255] })
            .collect();
        let mut f = Frame::solid(8, 8, [0, 0, 0, 255]);
        draw_image(&mut f, &src, w, w, [0.0, 0.0, w as f64, w as f64], &Affine::scale(0.125, 0.125), 1.0);
        let center = f.px[(4 * 8 + 4) * 4] as i32;
        assert!((center - 128).abs() <= 3, "{center}");
    }

    #[test]
    fn rotation_and_inverse() {
        let m = Affine::translate(10.0, 5.0).then(&Affine::rotate(90.0)).then(&Affine::scale(2.0, 3.0));
        let (x, y) = m.apply(1.0, 1.0);
        let back = m.invert().unwrap().apply(x, y);
        assert!((back.0 - 1.0).abs() < 1e-9 && (back.1 - 1.0).abs() < 1e-9);
        // Rotating by 90° clockwise maps +x to +y.
        let (rx, ry) = Affine::rotate(90.0).apply(1.0, 0.0);
        assert!(rx.abs() < 1e-12 && (ry - 1.0).abs() < 1e-12);
    }

    #[test]
    fn bbox() {
        let mut px = vec![0u8; 5 * 4 * 4];
        px[(2 * 5 + 1) * 4 + 3] = 9;
        px[(3 * 5 + 3) * 4 + 3] = 9;
        assert_eq!(alpha_bbox(&px, 5, 4), Some([1, 2, 3, 2]));
        assert_eq!(alpha_bbox(&[0u8; 16], 2, 2), None);
        let c = crop_px(&px, 5, [1, 2, 3, 2]);
        assert_eq!(c.len(), 3 * 2 * 4);
        assert_eq!(c[3], 9);
    }
}
