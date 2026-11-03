//! The audio mix: the whole range in one pass (spine VO and inserts, video layers with sound, audio elements), with
//! gain (constant or keyframed), loudness targets, fades with curves, ducking under the VO or under spans, then the
//! master bus (loudness gain, low-pass, true-peak limiter). Per-unit audio hashes come from per-frame digests of the
//! mixed PCM, so a picture-only edit that moves unit boundaries still gets exact hashes without re-mixing.

use crate::dsp::{self, Biquad};
use crate::expr::Js;
use crate::ffmpeg::{self, StreamSel};
use anyhow::{Result, anyhow};
use retake_timeline::keyframe::Prop;
use retake_timeline::model::FadeCurve;
use serde::Serialize;
use serde_json::Value;
use std::path::PathBuf;

/// mix/2: stream selection, unity mono upmix, fade curves, span ducking, gain keys, loudness targets, the master bus.
pub const AUDIO_VERSION: &str = "mix/2";

/// A keyframed or expression `gain_db`.
#[derive(Debug, Clone, Serialize)]
pub struct GainCurve {
    pub prop: Prop,
    /// The element's clock (seconds) at the clip's first sample.
    pub t0: f64,
    /// What expressions see as `props` (a video layer's composition props; `{}` otherwise).
    pub props: Value,
}

/// A loudness target: the element's clips (one `group`) are measured together and brought to `target` LUFS.
#[derive(Debug, Clone, Serialize)]
pub struct Loudness {
    pub target: f64,
    pub group: String,
    /// The gain that reaches the target, once measured (`Engine::audio_levels`).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub level_db: Option<f64>,
}

/// A ducking rule as mixed: under the VO's speech, or under spans of output samples.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "under", rename_all = "lowercase")]
pub enum DuckPlan {
    Vo { db: f64, release: f64 },
    Spans { db: f64, spans: Vec<[i64; 2]>, attack: f64, release: f64 },
}

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
    pub stream: StreamSel,
    /// The constant gain (a constant `gain_db`).
    pub gain_db: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gain: Option<GainCurve>,
    pub fade_in: f64,
    pub fade_out: f64,
    #[serde(skip_serializing_if = "is_linear")]
    pub fade_curve: FadeCurve,
    pub looping: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub duck: Vec<DuckPlan>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub loudness: Option<Loudness>,
}

fn is_linear(c: &FadeCurve) -> bool {
    *c == FadeCurve::Linear
}

/// The master bus as mixed. `gain_db` comes from the loudness target, measured on the whole timeline's mix.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct MasterPlan {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub loudness: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub gain_db: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub lowpass: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub true_peak: Option<f64>,
}

/// The export's true-peak ceiling when the timeline sets none: under YouTube's -1 dBTP, with room for the AAC encode's
/// overshoot (spec/bridge.md, "Download").
pub const EXPORT_TRUE_PEAK: f64 = -1.5;

/// Brings an export's mix to `lufs` (spec/bridge.md, "Download"): as if the master had that loudness and, unless it
/// sets one, an [`EXPORT_TRUE_PEAK`] ceiling. A timeline whose master sets a loudness keeps it. Reports whether the
/// plan changed.
pub fn export_master(plan: &mut AudioPlan, lufs: f64) -> bool {
    if plan.master.as_ref().is_some_and(|m| m.loudness.is_some()) {
        return false;
    }
    let m = plan.master.get_or_insert(MasterPlan { loudness: None, gain_db: None, lowpass: None, true_peak: None });
    m.loudness = Some(lufs);
    m.gain_db = None;
    m.true_peak = m.true_peak.or(Some(EXPORT_TRUE_PEAK));
    true
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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub master: Option<MasterPlan>,
}

impl AudioPlan {
    /// True when the plan needs measured levels (a loudness target on a clip or on the master).
    pub fn needs_levels(&self) -> bool {
        self.clips.iter().any(|c| c.loudness.is_some()) || self.master.as_ref().is_some_and(|m| m.loudness.is_some())
    }
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

/// Gain envelope for ducking under the VO: 1 where the VO is silent, `10^(db/20)` while it speaks (50 ms RMS over
/// −42 dBFS, smoothed over `release` seconds).
pub fn duck_envelope(vo: &[f32], sr: u32, db_: f64, release: f64) -> Vec<f32> {
    let mono: Vec<f32> = vo.as_chunks::<2>().0.iter().map(|s| ((s[0] + s[1]) * 0.5).powi(2)).collect();
    let rms = movavg(&mono, (0.05 * sr as f64) as usize);
    let thr = 10f32.powf(-42.0 / 20.0).powi(2);
    let act: Vec<f32> = rms.iter().map(|r| if *r > thr { 1.0 } else { 0.0 }).collect();
    let act = movavg(&act, (release * sr as f64) as usize);
    act.iter().map(|a| 10f32.powf(db_ as f32 * (a * 1.6).min(1.0) / 20.0)).collect()
}

/// The duck gain under spans for output samples [a, a + n): ramps over `attack` before each span and `release`
/// after it, linear in amplitude; `db` ≤ −96 is silence.
pub fn span_envelope(spans: &[[i64; 2]], a: i64, n: usize, sr: u32, db_: f64, attack: f64, release: f64) -> Vec<f32> {
    let at = (attack * sr as f64).round() as i64;
    let rl = (release * sr as f64).round() as i64;
    let mut env = vec![0f32; n];
    for &[s, e] in spans {
        let lo = (s - at).max(a);
        let hi = (e + rl).min(a + n as i64);
        for p in lo..hi {
            let v = if p < s {
                1.0 - (s - p) as f32 / at.max(1) as f32
            } else if p < e {
                1.0
            } else {
                1.0 - (p - e + 1) as f32 / (rl + 1) as f32
            };
            let slot = &mut env[(p - a) as usize];
            if v > *slot {
                *slot = v;
            }
        }
    }
    let floor = if db_ <= -96.0 { 0.0 } else { db(db_) };
    env.iter().map(|e| 1.0 + e * (floor - 1.0)).collect()
}

#[allow(clippy::too_many_arguments)]
/// Gain and fades for samples [skip, skip + x.len()/2) of a clip `total` samples long: `gain(i)` is the linear gain of
/// slice sample `i`; fades follow `curve`, with a 5 ms ramp at every edge.
fn shape_with(
    x: &mut [f32],
    sr: u32,
    skip: usize,
    total: usize,
    gain: impl Fn(usize) -> f32,
    fade_in: f64,
    fade_out: f64,
    curve: FadeCurve,
) {
    let n = x.len() / 2;
    let edge = ((0.005 * sr as f64) as usize).min(total / 2).max(1);
    let fin = ((fade_in * sr as f64) as usize).min(total).max(edge);
    let fout = ((fade_out * sr as f64) as usize).min(total).max(edge);
    for i in 0..n {
        let pos = skip + i;
        let mut k = gain(i);
        if pos < fin {
            k *= curve.gain(pos as f64 / fin as f64) as f32;
        }
        if total.saturating_sub(pos) <= fout {
            k *= curve.gain(total.saturating_sub(pos + 1) as f64 / fout as f64) as f32;
        }
        x[2 * i] *= k;
        x[2 * i + 1] *= k;
    }
}

/// Applies gain and linear fades to a decoded clip.
pub fn shape(x: &mut [f32], sr: u32, gain_db: f64, fade_in: f64, fade_out: f64) {
    let n = x.len() / 2;
    let g = db(gain_db);
    shape_with(x, sr, 0, n, |_| g, fade_in, fade_out, FadeCurve::Linear);
}

/// Per slice sample, the linear gain of a clip: its constant gain and loudness level, times the keyframed gain
/// evaluated once per output frame and interpolated linearly in dB.
fn gain_fn(c: &Clip, skip: usize, n: usize, sr: u32, fps: u32, js: &mut Option<Js>) -> Result<Vec<f32>> {
    let base = c.gain_db + c.loudness.as_ref().and_then(|l| l.level_db).unwrap_or(0.0);
    let Some(g) = &c.gain else {
        return Ok(vec![db(base); n]);
    };
    let spf = (sr / fps).max(1) as usize;
    let steps = n / spf + 2;
    let mut pts = Vec::with_capacity(steps);
    for k in 0..steps {
        let t = g.t0 + (skip + k * spf) as f64 / sr as f64;
        let v = if g.prop.has_expr() {
            if js.is_none() {
                *js = Some(Js::new().map_err(|e| anyhow!(e))?);
            }
            g.prop.eval(t, &g.props, js.as_mut().unwrap()).map_err(|e| anyhow!(e))?
        } else {
            g.prop.base(t).unwrap_or_default()
        };
        pts.push(v.first().copied().unwrap_or(0.0));
    }
    Ok((0..n)
        .map(|i| {
            let (k, f) = (i / spf, (i % spf) as f64 / spf as f64);
            db(base + pts[k] + (pts[k + 1] - pts[k]) * f)
        })
        .collect())
}

/// Decodes the samples [skip, skip + n) of a clip, before gain.
fn decode(c: &Clip, skip: usize, n: usize, sr: u32) -> Result<Vec<f32>> {
    let s = skip as f64 / sr as f64;
    let mut x = ffmpeg::decode_audio(&c.path, c.src_in + s, n as f64 / sr as f64 + 0.05, sr, c.looping, c.stream)?;
    x.resize(n * 2, 0.0);
    Ok(x)
}

/// The integrated loudness (LUFS) of clips played end to end as decoded (no gain, no fades): what a loudness target
/// measures.
pub fn measure(clips: &[&Clip], sr: u32) -> Result<f64> {
    let mut all = vec![];
    for c in clips {
        all.extend(decode(c, 0, c.len.max(0) as usize, sr)?);
    }
    Ok(dsp::integrated_lufs(&all, sr))
}

/// The sum of every clip over the plan's range, before the master bus and without clipping.
pub fn mix_raw(plan: &AudioPlan) -> Result<Vec<f32>> {
    let len = plan.len.max(0) as usize;
    let mut vo = vec![0f32; len * 2];
    let mut rest = vec![0f32; len * 2];
    let mut ducked: Vec<(Vec<f32>, usize, i64, &[DuckPlan])> = vec![];
    let sr = plan.sr;
    let mut js = None;
    for c in &plan.clips {
        // Part of the clip inside the mixed range.
        let a = c.at.max(plan.start);
        let b = (c.at + c.len).min(plan.start + plan.len);
        if b <= a {
            continue;
        }
        let skip = (a - c.at) as usize;
        let n = (b - a) as usize;
        let mut x = decode(c, skip, n, sr)?;
        // Fades are relative to the whole clip: shape this slice by its place in the clip.
        let g = gain_fn(c, skip, n, sr, plan.fps, &mut js)?;
        shape_with(&mut x, sr, skip, c.len as usize, |i| g[i], c.fade_in, c.fade_out, c.fade_curve);
        let off = (a - plan.start) as usize * 2;
        if c.role == "vo" {
            for (i, v) in x.iter().enumerate() {
                vo[off + i] += v;
            }
        } else if !c.duck.is_empty() {
            ducked.push((x, off, a, &c.duck));
        } else {
            for (i, v) in x.iter().enumerate() {
                rest[off + i] += v;
            }
        }
    }
    let mut out: Vec<f32> = vo.iter().zip(&rest).map(|(a, b)| a + b).collect();
    let mut vo_envs: Vec<((u64, u64), Vec<f32>)> = vec![];
    for (mut x, off, a, rules) in ducked {
        let n = x.len() / 2;
        for r in rules {
            match r {
                DuckPlan::Vo { db, release } => {
                    let k = (db.to_bits(), release.to_bits());
                    if !vo_envs.iter().any(|(kk, _)| *kk == k) {
                        vo_envs.push((k, duck_envelope(&vo, sr, *db, *release)));
                    }
                    let env = &vo_envs.iter().find(|(kk, _)| *kk == k).unwrap().1;
                    for i in 0..n {
                        let e = env[off / 2 + i];
                        x[2 * i] *= e;
                        x[2 * i + 1] *= e;
                    }
                }
                DuckPlan::Spans { db, spans, attack, release } => {
                    let env = span_envelope(spans, a, n, sr, *db, *attack, *release);
                    for (i, e) in env.iter().enumerate() {
                        x[2 * i] *= e;
                        x[2 * i + 1] *= e;
                    }
                }
            }
        }
        for (i, v) in x.iter().enumerate() {
            out[off + i] += v;
        }
    }
    Ok(out)
}

/// The master bus (gain, low-pass, true-peak limiter), clipping at 0 dBFS, and the per-frame digests.
pub fn finish(mut out: Vec<f32>, plan: &AudioPlan) -> Mix {
    let sr = plan.sr;
    if let Some(m) = &plan.master {
        if let Some(g) = m.gain_db {
            let k = db(g);
            out.iter_mut().for_each(|v| *v *= k);
        }
        if let Some(f) = m.lowpass.filter(|f| *f < sr as f64 / 2.0) {
            let lp = Biquad::lowpass(sr, f);
            lp.run(&mut out, 0);
            lp.run(&mut out, 1);
        }
        if let Some(c) = m.true_peak {
            dsp::limit_true_peak(&mut out, sr, c, 0.003, 0.06);
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
    Mix { pcm: out, frame_digests }
}

pub fn mix(plan: &AudioPlan) -> Result<Mix> {
    Ok(finish(mix_raw(plan)?, plan))
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
    fn fade_curves_at_their_midpoints() {
        let mid = |curve: FadeCurve| {
            let mut x = vec![1.0f32; 2 * 1000];
            shape_with(&mut x, 1000, 0, 1000, |_| 1.0, 0.2, 0.0, curve);
            x[2 * 100]
        };
        assert!((mid(FadeCurve::Linear) - 0.5).abs() < 1e-3);
        assert!((mid(FadeCurve::EqualPower) - std::f32::consts::FRAC_1_SQRT_2).abs() < 1e-3);
        assert!((mid(FadeCurve::SCurve) - 0.5).abs() < 1e-3);
        assert!((mid(FadeCurve::Log) - 10f32.powf(-1.5)).abs() < 1e-3, "-30 dB halfway from -60");
        // Equal power: a crossfade's in² + out² stays 1; s-curve: in + out stays 1.
        for x in [0.1, 0.37, 0.8] {
            let (a, b) = (FadeCurve::EqualPower.gain(x), FadeCurve::EqualPower.gain(1.0 - x));
            assert!((a * a + b * b - 1.0).abs() < 1e-9);
            assert!((FadeCurve::SCurve.gain(x) + FadeCurve::SCurve.gain(1.0 - x) - 1.0).abs() < 1e-9);
        }
    }

    #[test]
    fn a_slice_fades_like_the_whole_clip() {
        let mut whole = vec![1.0f32; 2 * 1000];
        shape_with(&mut whole, 1000, 0, 1000, |_| 1.0, 0.3, 0.3, FadeCurve::SCurve);
        let mut part = vec![1.0f32; 2 * 500];
        shape_with(&mut part, 1000, 250, 1000, |_| 1.0, 0.3, 0.3, FadeCurve::SCurve);
        assert_eq!(&whole[500..1500], &part[..]);
    }

    #[test]
    fn ducking_follows_the_vo() {
        let sr = 1000;
        // 1 s silence, 1 s loud VO, 2 s silence.
        let mut vo = vec![0f32; 2 * 4000];
        for v in &mut vo[2 * 1000..2 * 2000] {
            *v = 0.5;
        }
        let env = duck_envelope(&vo, sr, -12.0, 0.35);
        assert!((env[200] - 1.0).abs() < 1e-6, "no VO: no ducking");
        assert!((env[1500] - db(-12.0)).abs() < 1e-3, "under the VO: -12 dB");
        assert!((env[3900] - 1.0).abs() < 1e-6, "released after the VO");
    }

    #[test]
    fn span_ducking_ramps_outside_the_span() {
        let sr = 1000;
        // A span [1000, 2000), 0.25 s attack, 0.5 s release, fully out.
        let env = span_envelope(&[[1000, 2000]], 0, 3000, sr, -96.0, 0.25, 0.5);
        assert_eq!(env[700], 1.0, "before the attack");
        assert!((env[875] - 0.5).abs() < 0.01, "halfway down the attack");
        assert_eq!(env[1000], 0.0, "out for the whole span");
        assert_eq!(env[1999], 0.0);
        assert!((env[2250] - 0.5).abs() < 0.01, "halfway back up");
        assert_eq!(env[2600], 1.0);
        // A partial duck, and a range that starts inside the span.
        let env = span_envelope(&[[1000, 2000]], 1500, 100, sr, -12.0, 0.25, 0.5);
        assert!((env[0] - db(-12.0)).abs() < 1e-6);
    }

    #[test]
    fn range_hashes_compose_from_frames() {
        let d = vec![[1u8; 32], [2u8; 32], [3u8; 32]];
        assert_eq!(range_hash(&d, 0, 2), range_hash(&d[..2], 0, 2));
        assert_ne!(range_hash(&d, 0, 2), range_hash(&d, 1, 3));
    }
}
