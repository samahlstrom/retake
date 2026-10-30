//! The audio mix: the whole range in one pass (spine VO and inserts, video layers with sound, audio elements), with
//! gain, fades and ducking under the VO. Per-unit audio hashes come from per-frame digests of the mixed PCM, so a
//! picture-only edit that moves unit boundaries still gets exact hashes without re-mixing.

use crate::ffmpeg;
use anyhow::Result;
use serde::Serialize;
use std::path::PathBuf;

pub const AUDIO_VERSION: &str = "mix/1";

#[derive(Debug, Clone, Serialize)]
pub struct Clip {
    /// `vo`, `insert`, `layer` or `audio`.
    pub role: String,
    pub hash: String,
    #[serde(skip)]
    pub path: PathBuf,
    #[serde(skip)]
    pub owner: String,
    /// Source seconds where the clip starts.
    pub src_in: f64,
    /// Output sample where it starts, and its length in samples.
    pub at: i64,
    pub len: i64,
    pub gain_db: f64,
    pub fade_in: f64,
    pub fade_out: f64,
    pub looping: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub duck_db: Option<f64>,
}

#[derive(Debug, Clone, Serialize)]
pub struct AudioPlan {
    pub v: &'static str,
    pub sr: u32,
    pub fps: u32,
    /// The mixed range, in output samples.
    pub start: i64,
    pub len: i64,
    pub clips: Vec<Clip>,
}

pub struct Mix {
    /// Interleaved stereo f32.
    pub pcm: Vec<f32>,
    /// BLAKE3 of each output frame's PCM (f32le), from `start`.
    pub frame_digests: Vec<[u8; 32]>,
}

fn db(g: f64) -> f32 {
    10f64.powf(g / 20.0) as f32
}

/// Centred moving average via prefix sums.
fn movavg(x: &[f32], n: usize) -> Vec<f32> {
    let n = n.max(1);
    let mut c = vec![0f64; x.len() + 1];
    for (i, v) in x.iter().enumerate() {
        c[i + 1] = c[i] + *v as f64;
    }
    (0..x.len())
        .map(|i| {
            let lo = i.saturating_sub(n / 2);
            let hi = (lo + n).min(x.len());
            ((c[hi] - c[lo]) / (hi - lo).max(1) as f64) as f32
        })
        .collect()
}

/// Gain envelope for ducking: 1 where the VO is silent, `10^(db/20)` while it speaks (50 ms RMS over -42 dBFS,
/// 0.35 s release).
pub fn duck_envelope(vo: &[f32], sr: u32, db_: f64) -> Vec<f32> {
    let mono: Vec<f32> = vo.as_chunks::<2>().0.iter().map(|s| ((s[0] + s[1]) * 0.5).powi(2)).collect();
    let rms = movavg(&mono, (0.05 * sr as f64) as usize);
    let thr = 10f32.powf(-42.0 / 20.0).powi(2);
    let act: Vec<f32> = rms.iter().map(|r| if *r > thr { 1.0 } else { 0.0 }).collect();
    let act = movavg(&act, (0.35 * sr as f64) as usize);
    act.iter().map(|a| 10f32.powf(db_ as f32 * (a * 1.6).min(1.0) / 20.0)).collect()
}

/// Applies gain and linear fades to a decoded clip.
pub fn shape(x: &mut [f32], sr: u32, gain_db: f64, fade_in: f64, fade_out: f64) {
    let n = x.len() / 2;
    let g = db(gain_db);
    let fi = ((fade_in * sr as f64) as usize).min(n);
    let fo = ((fade_out * sr as f64) as usize).min(n);
    // A 5 ms ramp at every clip edge avoids clicks even without fades.
    let edge = ((0.005 * sr as f64) as usize).min(n / 2).max(1);
    for i in 0..n {
        let mut k = g;
        let fin = fi.max(edge);
        if i < fin {
            k *= i as f32 / fin as f32;
        }
        let fout = fo.max(edge);
        if n - i <= fout {
            k *= (n - i - 1) as f32 / fout as f32;
        }
        x[2 * i] *= k;
        x[2 * i + 1] *= k;
    }
}

pub fn mix(plan: &AudioPlan) -> Result<Mix> {
    let len = plan.len.max(0) as usize;
    let mut vo = vec![0f32; len * 2];
    let mut rest = vec![0f32; len * 2];
    let mut ducked: Vec<(Vec<f32>, usize, f64)> = vec![];
    let sr = plan.sr;
    for c in &plan.clips {
        // Part of the clip inside the mixed range.
        let a = c.at.max(plan.start);
        let b = (c.at + c.len).min(plan.start + plan.len);
        if b <= a {
            continue;
        }
        let skip = (a - c.at) as f64 / sr as f64;
        let n = (b - a) as usize;
        let mut x = ffmpeg::decode_audio(&c.path, c.src_in + skip, n as f64 / sr as f64 + 0.05, sr, c.looping)?;
        x.resize(n * 2, 0.0);
        // Fades are relative to the whole clip, so shape the full clip's view of this slice.
        let full_fade_in = (c.fade_in - skip).max(0.0);
        let tail = (c.at + c.len - b) as f64 / sr as f64;
        let full_fade_out = (c.fade_out - tail).max(0.0);
        shape(&mut x, sr, c.gain_db, full_fade_in, full_fade_out);
        let off = (a - plan.start) as usize * 2;
        match (c.role.as_str(), c.duck_db) {
            ("vo", _) => {
                for (i, v) in x.iter().enumerate() {
                    vo[off + i] += v;
                }
            }
            (_, Some(d)) => ducked.push((x, off, d)),
            _ => {
                for (i, v) in x.iter().enumerate() {
                    rest[off + i] += v;
                }
            }
        }
    }
    let mut out: Vec<f32> = vo.iter().zip(&rest).map(|(a, b)| a + b).collect();
    if !ducked.is_empty() {
        let mut envs: Vec<(f64, Vec<f32>)> = vec![];
        for (x, off, d) in ducked {
            if !envs.iter().any(|(k, _)| *k == d) {
                envs.push((d, duck_envelope(&vo, sr, d)));
            }
            let env = &envs.iter().find(|(k, _)| *k == d).unwrap().1;
            for (i, v) in x.iter().enumerate() {
                out[off + i] += v * env[(off + i) / 2];
            }
        }
    }
    for v in &mut out {
        *v = v.clamp(-1.0, 1.0);
    }
    let spf = (sr / plan.fps) as usize;
    let frame_digests = out
        .chunks(spf * 2)
        .map(|ch| {
            let mut h = blake3::Hasher::new();
            for s in ch {
                h.update(&s.to_le_bytes());
            }
            *h.finalize().as_bytes()
        })
        .collect();
    Ok(Mix { pcm: out, frame_digests })
}

/// The audio hash of output frames [a, b) relative to the mix start: BLAKE3 over the frames' PCM digests.
pub fn range_hash(digests: &[[u8; 32]], a: usize, b: usize) -> String {
    let mut h = blake3::Hasher::new();
    for d in &digests[a.min(digests.len())..b.min(digests.len())] {
        h.update(d);
    }
    format!("b3:{}", h.finalize().to_hex())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fades_and_gain() {
        let mut x = vec![1.0f32; 2 * 1000];
        shape(&mut x, 1000, -6.0206, 0.1, 0.2);
        assert!(x[0].abs() < 1e-6, "starts silent");
        assert!((x[2 * 500] - 0.5).abs() < 1e-3, "middle: -6 dB");
        assert!((x[2 * 50] - 0.25).abs() < 0.01, "halfway through the fade in");
        assert!(x[2 * 999].abs() < 1e-6, "ends silent");
    }

    #[test]
    fn ducking_follows_the_vo() {
        let sr = 1000;
        // 1 s silence, 1 s loud VO, 2 s silence.
        let mut vo = vec![0f32; 2 * 4000];
        for v in &mut vo[2 * 1000..2 * 2000] {
            *v = 0.5;
        }
        let env = duck_envelope(&vo, sr, -12.0);
        assert!((env[200] - 1.0).abs() < 1e-6, "no VO: no ducking");
        assert!((env[1500] - db(-12.0)).abs() < 1e-3, "under the VO: -12 dB");
        assert!((env[3900] - 1.0).abs() < 1e-6, "released after the VO");
    }

    #[test]
    fn range_hashes_compose_from_frames() {
        let d = vec![[1u8; 32], [2u8; 32], [3u8; 32]];
        assert_eq!(range_hash(&d, 0, 2), range_hash(&d[..2], 0, 2));
        assert_ne!(range_hash(&d, 0, 2), range_hash(&d, 1, 3));
    }
}
