//! Audio DSP for the mix, on interleaved stereo f32: the ITU-R BS.1770-4 loudness meter (K-weighting, gated
//! integrated loudness), a Butterworth low-pass, 4× oversampled true-peak measurement and a look-ahead true-peak
//! limiter. Everything runs offline on the whole signal, so look-ahead costs no delay.

/// A biquad in transposed direct form II, run in f64.
#[derive(Debug, Clone, Copy)]
pub struct Biquad {
    b0: f64,
    b1: f64,
    b2: f64,
    a1: f64,
    a2: f64,
}

impl Biquad {
    /// Filters channel `ch` of an interleaved stereo buffer in place.
    pub fn run(&self, x: &mut [f32], ch: usize) {
        let (mut z1, mut z2) = (0.0f64, 0.0f64);
        for s in x.iter_mut().skip(ch).step_by(2) {
            let v = *s as f64;
            let y = self.b0 * v + z1;
            z1 = self.b1 * v - self.a1 * y + z2;
            z2 = self.b2 * v - self.a2 * y;
            *s = y as f32;
        }
    }

    /// Filters a mono f64 signal in place.
    fn run64(&self, x: &mut [f64]) {
        let (mut z1, mut z2) = (0.0f64, 0.0f64);
        for s in x.iter_mut() {
            let v = *s;
            let y = self.b0 * v + z1;
            z1 = self.b1 * v - self.a1 * y + z2;
            z2 = self.b2 * v - self.a2 * y;
            *s = y;
        }
    }

    /// A 2-pole Butterworth low-pass at `f` Hz (RBJ cookbook, Q = 1/√2), as ffmpeg's `lowpass` filter.
    pub fn lowpass(sr: u32, f: f64) -> Biquad {
        let w0 = 2.0 * std::f64::consts::PI * f / sr as f64;
        let (s, c) = w0.sin_cos();
        let alpha = s / (2.0 * std::f64::consts::FRAC_1_SQRT_2);
        let a0 = 1.0 + alpha;
        Biquad {
            b0: (1.0 - c) / 2.0 / a0,
            b1: (1.0 - c) / a0,
            b2: (1.0 - c) / 2.0 / a0,
            a1: -2.0 * c / a0,
            a2: (1.0 - alpha) / a0,
        }
    }
}

/// BS.1770's K-weighting at any sample rate: the high-shelf "pre-filter", then the RLB high-pass (the formulas
/// libebur128 uses; at 48 kHz they give the coefficients printed in the standard).
pub fn k_weighting(sr: u32) -> [Biquad; 2] {
    let fs = sr as f64;
    let (f0, g, q) = (1681.974450955533, 3.999843853973347, 0.7071752369554196);
    let k = (std::f64::consts::PI * f0 / fs).tan();
    let vh = 10f64.powf(g / 20.0);
    let vb = vh.powf(0.4996667741545416);
    let a0 = 1.0 + k / q + k * k;
    let shelf = Biquad {
        b0: (vh + vb * k / q + k * k) / a0,
        b1: 2.0 * (k * k - vh) / a0,
        b2: (vh - vb * k / q + k * k) / a0,
        a1: 2.0 * (k * k - 1.0) / a0,
        a2: (1.0 - k / q + k * k) / a0,
    };
    let (f0, q) = (38.13547087602444, 0.5003270373238773);
    let k = (std::f64::consts::PI * f0 / fs).tan();
    let d = 1.0 + k / q + k * k;
    let hp = Biquad { b0: 1.0, b1: -2.0, b2: 1.0, a1: 2.0 * (k * k - 1.0) / d, a2: (1.0 - k / q + k * k) / d };
    [shelf, hp]
}

/// Integrated loudness in LUFS of interleaved stereo (BS.1770-4: 400 ms blocks every 100 ms, an absolute gate at
/// −70 LUFS and a relative gate 10 LU under the gated mean). `-inf` for silence. A signal shorter than one block is
/// measured as one block.
pub fn integrated_lufs(x: &[f32], sr: u32) -> f64 {
    let n = x.len() / 2;
    if n == 0 {
        return f64::NEG_INFINITY;
    }
    let kw = k_weighting(sr);
    // Energy per 100 ms sub-block, summed over both channels (weights 1).
    let sub = (sr as usize / 10).max(1);
    let mut subs = vec![0f64; n.div_ceil(sub)];
    for ch in 0..2 {
        let mut y: Vec<f64> = x.iter().skip(ch).step_by(2).map(|v| *v as f64).collect();
        for f in &kw {
            f.run64(&mut y);
        }
        for (i, v) in y.iter().enumerate() {
            subs[i / sub] += v * v;
        }
    }
    let full = n / sub;
    let block = 4 * sub;
    let mut blocks: Vec<f64> = if full >= 4 {
        (0..=full - 4).map(|j| subs[j..j + 4].iter().sum::<f64>() / block as f64).collect()
    } else {
        vec![subs.iter().sum::<f64>() / n as f64]
    };
    let lufs = |z: f64| -0.691 + 10.0 * z.log10();
    blocks.retain(|z| *z > 0.0 && lufs(*z) > -70.0);
    if blocks.is_empty() {
        return f64::NEG_INFINITY;
    }
    let mean = blocks.iter().sum::<f64>() / blocks.len() as f64;
    let rel = lufs(mean) - 10.0;
    let gated: Vec<f64> = blocks.into_iter().filter(|z| lufs(*z) > rel).collect();
    if gated.is_empty() {
        return f64::NEG_INFINITY;
    }
    lufs(gated.iter().sum::<f64>() / gated.len() as f64)
}

const TP_HALF: usize = 12;

/// Polyphase taps for the values at n + 1/4, n + 2/4 and n + 3/4: a Hann-windowed sinc over x[n − 11 ..= n + 12].
fn tp_taps() -> [[f32; 2 * TP_HALF]; 3] {
    let mut out = [[0f32; 2 * TP_HALF]; 3];
    for (p, row) in out.iter_mut().enumerate() {
        let frac = (p + 1) as f64 / 4.0;
        for (k, tap) in row.iter_mut().enumerate() {
            let j = k as f64 - (TP_HALF as f64 - 1.0); // offset of x[n + j] from n
            let d = frac - j;
            let sinc =
                if d.abs() < 1e-12 { 1.0 } else { (std::f64::consts::PI * d).sin() / (std::f64::consts::PI * d) };
            let w = 0.5 + 0.5 * (std::f64::consts::PI * d / (TP_HALF as f64 + 0.5)).cos();
            *tap = (sinc * w) as f32;
        }
    }
    out
}

/// Per sample frame, the largest absolute value of either channel at it and at the three 4× oversampled points up to
/// the next sample (an inter-sample peak counts for both samples around it).
pub fn true_peaks(x: &[f32]) -> Vec<f32> {
    let n = x.len() / 2;
    let taps = tp_taps();
    let mut tp: Vec<f32> = (0..n).map(|i| x[2 * i].abs().max(x[2 * i + 1].abs())).collect();
    // Each channel zero-padded so every window is a plain slice: padded[i + k] is x[i + k − (TP_HALF − 1)].
    let pad = |ch: usize| {
        let mut v = vec![0f32; TP_HALF - 1];
        v.extend(x.iter().skip(ch).step_by(2));
        v.extend(std::iter::repeat_n(0f32, TP_HALF + 1));
        v
    };
    let chans = [pad(0), pad(1)];
    for i in 0..n {
        let mut m = 0f32;
        for row in &taps {
            for c in &chans {
                let w = &c[i..i + 2 * TP_HALF];
                let acc: f32 = w.iter().zip(row).map(|(a, b)| a * b).sum();
                m = m.max(acc.abs());
            }
        }
        if m > tp[i] {
            tp[i] = m;
        }
        if i + 1 < n && m > tp[i + 1] {
            tp[i + 1] = m;
        }
    }
    tp
}

/// The true peak of interleaved stereo, in dBTP.
pub fn true_peak_db(x: &[f32]) -> f64 {
    let m = true_peaks(x).into_iter().fold(0f32, f32::max);
    20.0 * (m as f64).max(1e-12).log10()
}

/// Limits interleaved stereo so its true peak stays at or under `ceiling_db` dBTP: the gain reaches each peak's
/// required value linearly over `attack` seconds before it (look-ahead) and recovers exponentially over `release`.
pub fn limit_true_peak(x: &mut [f32], sr: u32, ceiling_db: f64, attack: f64, release: f64) {
    let n = x.len() / 2;
    if n == 0 {
        return;
    }
    // A little headroom for meters whose interpolators differ from ours.
    let c = 10f64.powf((ceiling_db - 0.05) / 20.0) as f32;
    let a = ((attack * sr as f64) as usize).max(1);
    let r = 1.0 - (-1.0 / (release * sr as f64).max(1.0)).exp() as f32;
    // A pass rarely leaves anything over; a second (and third) catches what gain changes between samples let through.
    for _ in 0..3 {
        let tp = true_peaks(x);
        if !tp.iter().any(|p| *p > c) {
            return;
        }
        let mut g = vec![1f32; n];
        for (i, p) in tp.iter().enumerate() {
            if *p <= c {
                continue;
            }
            let req = c / p;
            for k in 0..=a.min(i) {
                let v = req + (1.0 - req) * k as f32 / (a + 1) as f32;
                if v < g[i - k] {
                    g[i - k] = v;
                }
            }
        }
        let mut cur = 1f32;
        for (i, gi) in g.iter_mut().enumerate() {
            cur = (cur + (1.0 - cur) * r).min(*gi);
            *gi = cur;
            x[2 * i] *= cur;
            x[2 * i + 1] *= cur;
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sine(sr: u32, f: f64, amp: f64, secs: f64) -> Vec<f32> {
        let n = (sr as f64 * secs) as usize;
        (0..n)
            .flat_map(|i| {
                let v = (amp * (2.0 * std::f64::consts::PI * f * i as f64 / sr as f64).sin()) as f32;
                [v, v]
            })
            .collect()
    }

    #[test]
    fn k_weighting_matches_the_standard_at_48k() {
        let [s, h] = k_weighting(48000);
        assert!((s.b0 - 1.53512485958697).abs() < 1e-9 && (s.a1 + 1.69065929318241).abs() < 1e-9);
        assert!((h.a1 + 1.99004745483398).abs() < 1e-9 && (h.a2 - 0.99007225036621).abs() < 1e-9);
    }

    #[test]
    fn a_full_scale_1k_sine_in_both_channels_is_about_0_lufs() {
        // BS.1770: a 0 dBFS 1 kHz sine in one channel reads −3.01 LKFS; in both, about 0.
        let x = sine(48000, 1000.0, 1.0, 5.0);
        let l = integrated_lufs(&x, 48000);
        assert!((l - 0.0).abs() < 0.2, "{l}");
        let q = sine(48000, 1000.0, 0.1, 5.0);
        assert!((integrated_lufs(&q, 48000) - (l - 20.0)).abs() < 0.05);
        assert_eq!(integrated_lufs(&vec![0.0; 96000], 48000), f64::NEG_INFINITY);
    }

    #[test]
    fn gating_ignores_silence() {
        let mut x = sine(48000, 1000.0, 0.1, 3.0);
        x.extend(vec![0.0f32; 2 * 48000 * 6]);
        let l = integrated_lufs(&x, 48000);
        assert!((l - integrated_lufs(&sine(48000, 1000.0, 0.1, 3.0), 48000)).abs() < 0.3, "{l}");
    }

    #[test]
    fn true_peak_finds_intersample_peaks() {
        // fs/4 at 45°: samples sit at ±0.707 of the amplitude, the peak is between them.
        let x: Vec<f32> = (0..4000)
            .flat_map(|i| {
                let v = (0.9 * (std::f64::consts::FRAC_PI_2 * i as f64 + std::f64::consts::FRAC_PI_4).sin()) as f32;
                [v, v]
            })
            .collect();
        let tp = true_peak_db(&x);
        assert!((tp - 20.0 * 0.9f64.log10()).abs() < 0.2, "{tp}");
    }

    #[test]
    fn the_limiter_holds_the_ceiling_and_leaves_quiet_parts_alone() {
        let mut x = sine(48000, 997.0, 0.2, 1.0);
        x.extend(sine(48000, 997.0, 1.0, 1.0));
        x.extend(sine(48000, 997.0, 0.2, 1.0));
        limit_true_peak(&mut x, 48000, -6.0, 0.003, 0.06);
        assert!(true_peak_db(&x) <= -6.0 + 1e-3, "{}", true_peak_db(&x));
        // The quiet first second is untouched (the look-ahead only reaches 3 ms back).
        let first = &x[..2 * 47000];
        assert!((first.iter().fold(0f32, |m, v| m.max(v.abs())) - 0.2).abs() < 1e-3);
        // Well after the loud part, the gain is back.
        let last = &x[2 * 48000 * 2 + 2 * 24000..];
        assert!(last.iter().fold(0f32, |m, v| m.max(v.abs())) > 0.199);
    }

    #[test]
    fn lowpass_cuts_highs() {
        let rms = |x: &[f32]| (x.iter().map(|v| (*v as f64).powi(2)).sum::<f64>() / x.len() as f64).sqrt();
        let lp = Biquad::lowpass(48000, 1000.0);
        let mut lo = sine(48000, 100.0, 0.5, 1.0);
        let mut hi = sine(48000, 10000.0, 0.5, 1.0);
        let (l0, h0) = (rms(&lo), rms(&hi));
        for ch in 0..2 {
            lp.run(&mut lo, ch);
            lp.run(&mut hi, ch);
        }
        assert!((rms(&lo) / l0 - 1.0).abs() < 0.01);
        assert!(rms(&hi) / h0 < 0.02, "10 kHz is ~40 dB down from a 1 kHz 2-pole low-pass");
    }
}
