//! Lanczos-3 resampling of 8-bit RGBA, the way Pillow's `Image.resize(…, Image.LANCZOS)` does it: separable,
//! horizontal pass first, coefficients normalized per output pixel and rounded to 22-bit fixed point, an 8-bit
//! intermediate. On opaque images (and on premultiplied ones, which is what Pillow resizes RGBA as) the output is
//! Pillow's to the byte.

use rayon::prelude::*;

const PRECISION_BITS: u32 = 32 - 8 - 2;
const SUPPORT: f64 = 3.0;

fn sinc(x: f64) -> f64 {
    if x == 0.0 {
        return 1.0;
    }
    let x = x * std::f64::consts::PI;
    x.sin() / x
}

fn lanczos(x: f64) -> f64 {
    if (-SUPPORT..SUPPORT).contains(&x) { sinc(x) * sinc(x / SUPPORT) } else { 0.0 }
}

/// Per output pixel: the first input pixel, how many it reads, and their fixed-point weights.
struct Coeffs {
    bounds: Vec<(usize, usize)>,
    k: Vec<i32>,
    ksize: usize,
}

fn coeffs(in_size: usize, in0: f64, in1: f64, out_size: usize) -> Coeffs {
    let scale = (in1 - in0) / out_size as f64;
    let filterscale = scale.max(1.0);
    let support = SUPPORT * filterscale;
    let ksize = support.ceil() as usize * 2 + 1;
    let mut bounds = Vec::with_capacity(out_size);
    let mut k = vec![0i32; out_size * ksize];
    let ss = 1.0 / filterscale;
    for xx in 0..out_size {
        let center = in0 + (xx as f64 + 0.5) * scale;
        // C's (int) cast truncates toward zero.
        let xmin = ((center - support + 0.5) as i64).max(0) as usize;
        let xmax = ((center + support + 0.5) as i64).min(in_size as i64).max(xmin as i64) as usize - xmin;
        let mut w: Vec<f64> = (0..xmax).map(|x| lanczos((x as f64 + xmin as f64 - center + 0.5) * ss)).collect();
        let ww: f64 = w.iter().sum();
        if ww != 0.0 {
            for v in &mut w {
                *v /= ww;
            }
        }
        for (x, v) in w.iter().enumerate() {
            let f = v * (1u64 << PRECISION_BITS) as f64;
            k[xx * ksize + x] = if *v < 0.0 { (-0.5 + f) as i32 } else { (0.5 + f) as i32 };
        }
        bounds.push((xmin, xmax));
    }
    Coeffs { bounds, k, ksize }
}

fn clip8(v: i64) -> u8 {
    if v >= (1i64 << PRECISION_BITS << 8) {
        255
    } else if v <= 0 {
        0
    } else {
        (v >> PRECISION_BITS) as u8
    }
}

/// Resizes the box `[x0, y0, x1, y1]` (source pixels, fractions allowed) of a `w` × `h` RGBA image to `ow` × `oh`.
pub fn lanczos_rgba(src: &[u8], w: u32, h: u32, bx: [f64; 4], ow: u32, oh: u32) -> Vec<u8> {
    let (w, h, ow, oh) = (w as usize, h as usize, ow.max(1) as usize, oh.max(1) as usize);
    let need_h = ow != w || bx[0] != 0.0 || bx[2] != w as f64;
    let need_v = oh != h || bx[1] != 0.0 || bx[3] != h as f64;
    let ch = coeffs(w, bx[0], bx[2], ow);
    let cv = coeffs(h, bx[1], bx[3], oh);
    let half = 1i64 << (PRECISION_BITS - 1);
    // Horizontal pass, over the rows the vertical pass reads.
    let (tmp, tw, row0) = if need_h {
        let first = cv.bounds.first().map(|b| b.0).unwrap_or(0);
        let last = cv.bounds.last().map(|b| b.0 + b.1).unwrap_or(h);
        let rows = last.saturating_sub(first);
        let mut out = vec![0u8; ow * rows * 4];
        out.par_chunks_mut(ow * 4).enumerate().for_each(|(r, orow)| {
            let irow = &src[(first + r) * w * 4..][..w * 4];
            for xx in 0..ow {
                let (xmin, n) = ch.bounds[xx];
                let k = &ch.k[xx * ch.ksize..][..n];
                let mut ss = [half; 4];
                for (x, &kx) in k.iter().enumerate() {
                    let p = &irow[(xmin + x) * 4..][..4];
                    for c in 0..4 {
                        ss[c] += p[c] as i64 * kx as i64;
                    }
                }
                for c in 0..4 {
                    orow[xx * 4 + c] = clip8(ss[c]);
                }
            }
        });
        (std::borrow::Cow::Owned(out), ow, first)
    } else {
        (std::borrow::Cow::Borrowed(src), w, 0)
    };
    if !need_v {
        return tmp.into_owned();
    }
    let mut out = vec![0u8; tw * oh * 4];
    out.par_chunks_mut(tw * 4).enumerate().for_each(|(yy, orow)| {
        let (ymin, n) = cv.bounds[yy];
        let k = &cv.k[yy * cv.ksize..][..n];
        for xx in 0..tw {
            let mut ss = [half; 4];
            for (y, &ky) in k.iter().enumerate() {
                let p = &tmp[((ymin - row0 + y) * tw + xx) * 4..][..4];
                for c in 0..4 {
                    ss[c] += p[c] as i64 * ky as i64;
                }
            }
            for c in 0..4 {
                orow[xx * 4 + c] = clip8(ss[c]);
            }
        }
    });
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn identity_and_flat_colors() {
        let src: Vec<u8> = (0..16 * 9).flat_map(|i| [(i * 7 % 256) as u8, 30, 200, 255]).collect();
        assert_eq!(lanczos_rgba(&src, 16, 9, [0.0, 0.0, 16.0, 9.0], 16, 9), src, "same size: untouched");
        let flat: Vec<u8> = (0..64 * 64).flat_map(|_| [90u8, 140, 210, 255]).collect();
        let out = lanczos_rgba(&flat, 64, 64, [0.0, 0.0, 64.0, 64.0], 23, 17);
        assert!(out.chunks(4).all(|p| p == [90, 140, 210, 255]), "normalized weights keep a flat color");
    }

    #[test]
    fn downscale_averages_and_crops() {
        // A 1-px checkerboard halved is mid-grey; a box picks a region.
        let src: Vec<u8> =
            (0..32 * 32).flat_map(|i| if (i % 32 + i / 32) % 2 == 0 { [255u8; 4] } else { [0, 0, 0, 255] }).collect();
        let out = lanczos_rgba(&src, 32, 32, [0.0, 0.0, 32.0, 32.0], 8, 8);
        let mid = out[(4 * 8 + 4) * 4] as i32;
        assert!((mid - 128).abs() <= 2, "{mid}");
        let half: Vec<u8> = (0..8 * 4).flat_map(|i| if i % 8 < 4 { [255u8, 0, 0, 255] } else { [0, 0, 255, 255] }).collect();
        let left = lanczos_rgba(&half, 8, 4, [0.0, 0.0, 4.0, 4.0], 4, 4);
        assert!(left.chunks(4).all(|p| p[0] >= 250 && p[2] <= 5), "{left:?}");
    }
}
