//! Test media: a speech-like tone track from a hand-written words file. Each word is a voiced tone burst (a few
//! harmonics, a pitch that falls through the word) at exactly the word's times, so the words file is its true
//! transcript. No TTS and nobody's voice.

use anyhow::{Context, Result};
use std::f64::consts::TAU;
use std::path::Path;

fn fnv(s: &str) -> u64 {
    s.bytes().fold(0xcbf29ce484222325u64, |h, b| (h ^ b as u64).wrapping_mul(0x100000001b3))
}

pub fn synth_vo(words: &Path, out: &Path, sr: u32, dur: Option<f64>) -> Result<usize> {
    let ws = retake_timeline::words::load_words(words, "").map_err(|e| anyhow::anyhow!(e))?;
    let end = dur.unwrap_or_else(|| ws.iter().map(|w| w.e).fold(0.0, f64::max) + 0.5);
    let n = (end * sr as f64).round() as usize;
    let mut x = vec![0f64; n];
    for w in &ws {
        let h = fnv(&retake_timeline::words::norm(&w.text));
        let f0 = 110.0 + (h % 90) as f64;
        let (a, b) = ((w.s * sr as f64) as usize, ((w.e * sr as f64) as usize).min(n));
        let len = (b - a).max(1) as f64;
        let mut phase = 0.0;
        for (i, v) in x[a..b].iter_mut().enumerate() {
            let p = i as f64 / len;
            let f = f0 * (1.0 - 0.12 * p);
            phase += TAU * f / sr as f64;
            let t = i as f64 / sr as f64;
            let env = (t / 0.012).min(1.0) * (((len - i as f64) / sr as f64) / 0.025).min(1.0);
            let mut s = 0.0;
            for k in 1..=6 {
                s += (phase * k as f64).sin() / k as f64;
            }
            *v += 0.3 * env * s;
        }
    }
    let mut bytes = Vec::with_capacity(44 + n * 2);
    let data_len = (n * 2) as u32;
    bytes.extend_from_slice(b"RIFF");
    bytes.extend_from_slice(&(36 + data_len).to_le_bytes());
    bytes.extend_from_slice(b"WAVEfmt ");
    bytes.extend_from_slice(&16u32.to_le_bytes());
    bytes.extend_from_slice(&1u16.to_le_bytes()); // PCM
    bytes.extend_from_slice(&1u16.to_le_bytes()); // mono
    bytes.extend_from_slice(&sr.to_le_bytes());
    bytes.extend_from_slice(&(sr * 2).to_le_bytes());
    bytes.extend_from_slice(&2u16.to_le_bytes());
    bytes.extend_from_slice(&16u16.to_le_bytes());
    bytes.extend_from_slice(b"data");
    bytes.extend_from_slice(&data_len.to_le_bytes());
    for v in x {
        bytes.extend_from_slice(&((v.clamp(-1.0, 1.0) * 32767.0).round() as i16).to_le_bytes());
    }
    std::fs::write(out, bytes).with_context(|| format!("writing {}", out.display()))?;
    Ok(ws.len())
}
