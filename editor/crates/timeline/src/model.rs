//! The typed `timeline/0.1` model (spec/timeline.md). Compositions and layers stay JSON values here: their times may
//! be anchors until `resolve` turns them into numbers; `layer::Layer` is the typed form after that.

use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;

pub const SCHEMA: &str = "timeline/0.1";

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Timeline {
    pub schema: String,
    pub id: String,
    pub rev: u64,
    pub format: Format,
    #[serde(default)]
    pub kits: Vec<String>,
    pub assets: BTreeMap<String, AssetDef>,
    #[serde(default)]
    pub fonts: BTreeMap<String, FontDef>,
    pub spine: Vec<SpineItem>,
    pub scenes: Vec<Scene>,
    #[serde(default)]
    pub audio: Vec<AudioElement>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub master: Option<Master>,
    #[serde(default)]
    pub end: Option<String>,
}

/// The master bus (spec/timeline.md, "The master bus").
#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
pub struct Master {
    /// Target integrated loudness of the whole mix, LUFS.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub loudness: Option<f64>,
    /// Ceiling, dBTP.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub true_peak: Option<f64>,
    /// 2-pole Butterworth low-pass, Hz.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lowpass: Option<f64>,
}

impl Master {
    pub fn is_active(&self) -> bool {
        self.loudness.is_some() || self.true_peak.is_some() || self.lowpass.is_some()
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Format {
    pub size: [u32; 2],
    pub fps: u32,
    #[serde(default = "default_sample_rate")]
    pub sample_rate: u32,
    #[serde(default)]
    pub background: Option<String>,
}

fn default_sample_rate() -> u32 {
    48000
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AssetDef {
    pub path: String,
    #[serde(default)]
    pub words: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FontDef {
    pub path: String,
    #[serde(default)]
    pub weight: Option<f64>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum SpineKind {
    #[default]
    Vo,
    Insert,
}

/// The shape of fades (spec/timeline.md, "Sound options").
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "kebab-case")]
pub enum FadeCurve {
    #[default]
    Linear,
    EqualPower,
    SCurve,
    Log,
}

impl FadeCurve {
    fn is_linear(&self) -> bool {
        *self == FadeCurve::Linear
    }

    /// The fade-in gain at `x` in [0, 1] (a fade-out reads it backwards).
    pub fn gain(&self, x: f64) -> f64 {
        let x = x.clamp(0.0, 1.0);
        match self {
            FadeCurve::Linear => x,
            FadeCurve::EqualPower => (x * std::f64::consts::FRAC_PI_2).sin(),
            FadeCurve::SCurve => (x * std::f64::consts::FRAC_PI_2).sin().powi(2),
            FadeCurve::Log => {
                if x <= 0.0 {
                    0.0
                } else {
                    10f64.powf(-3.0 * (1.0 - x))
                }
            }
        }
    }
}

/// What a ducking rule ducks under: `"vo"`, `"inserts"`, or spine item ids.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Under {
    Named(String),
    Ids(Vec<String>),
}

impl Default for Under {
    fn default() -> Self {
        Under::Named("vo".into())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct DuckRule {
    pub db: f64,
    #[serde(default)]
    pub under: Under,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub attack: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub release: Option<f64>,
}

/// `duck`: one rule or a list.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(untagged)]
pub enum Ducks {
    One(DuckRule),
    Many(Vec<DuckRule>),
}

impl Ducks {
    pub fn rules(&self) -> Vec<DuckRule> {
        match self {
            Ducks::One(r) => vec![r.clone()],
            Ducks::Many(rs) => rs.clone(),
        }
    }
}

/// Sound options of a spine item or a video layer (spec/timeline.md, "Sound options"). `gain_db` stays JSON until
/// the resolver has turned anchors in its keys into seconds; [`AudioOpts::gain`] parses it then.
#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
pub struct AudioOpts {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub gain_db: Option<Value>,
    #[serde(default)]
    pub fade_in: f64,
    #[serde(default)]
    pub fade_out: f64,
    #[serde(default, skip_serializing_if = "FadeCurve::is_linear")]
    pub fade_curve: FadeCurve,
    #[serde(default)]
    pub mute: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stream: Option<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub loudness: Option<f64>,
    /// Video layers only (the schema doesn't allow it on spine items).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub duck: Option<Ducks>,
}

/// A resolved `gain_db` (numbers for key times) as a prop; `None` for no gain.
pub fn gain_prop(v: Option<&Value>) -> Result<Option<crate::keyframe::Prop>, String> {
    match v {
        None => Ok(None),
        Some(v) => serde_json::from_value(v.clone()).map(Some).map_err(|e| format!("gain_db: {e}")),
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SpineItem {
    pub id: String,
    #[serde(default)]
    pub kind: SpineKind,
    pub asset: String,
    #[serde(rename = "in")]
    pub in_: f64,
    #[serde(default)]
    pub out: Option<f64>,
    #[serde(default)]
    pub dur: Option<f64>,
    #[serde(default)]
    pub frames: Option<u64>,
    #[serde(default)]
    pub gap_before: Option<f64>,
    #[serde(default)]
    pub hold_from: Option<f64>,
    #[serde(default)]
    pub audio: Option<AudioOpts>,
    #[serde(default)]
    pub picture: Option<Value>,
}

impl SpineItem {
    /// Length in seconds.
    pub fn duration(&self, fps: u32) -> f64 {
        if let Some(o) = self.out {
            o - self.in_
        } else if let Some(d) = self.dur {
            d
        } else if let Some(f) = self.frames {
            f as f64 / fps as f64
        } else {
            0.0
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Scene {
    pub id: String,
    #[serde(default)]
    pub title: Option<String>,
    pub shots: Vec<Shot>,
    #[serde(default)]
    pub overlays: Vec<Overlay>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Shot {
    pub id: String,
    pub at: String,
    pub picture: Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Overlay {
    pub id: String,
    pub from: String,
    pub to: String,
    #[serde(default)]
    pub z: i64,
    #[serde(default)]
    pub background: Option<String>,
    #[serde(default)]
    pub props: Option<Value>,
    /// After expansion, the layers (a component use has none as written).
    #[serde(default)]
    pub layers: Vec<Value>,
    /// The component this overlay uses, `<name>@<version>` (spec/components.md).
    #[serde(rename = "use", default, skip_serializing_if = "Option::is_none")]
    pub use_: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AudioElement {
    pub id: String,
    pub asset: String,
    pub from: String,
    #[serde(default)]
    pub to: Option<String>,
    #[serde(rename = "in", default)]
    pub in_: f64,
    #[serde(rename = "loop", default)]
    pub loop_: bool,
    /// A number or a prop; anchors in its keys are resolved relative to `from` (see [`AudioOpts`]).
    #[serde(default)]
    pub gain_db: Option<Value>,
    #[serde(default)]
    pub fade_in: f64,
    #[serde(default)]
    pub fade_out: f64,
    #[serde(default)]
    pub fade_curve: FadeCurve,
    #[serde(default)]
    pub stream: Option<u32>,
    #[serde(default)]
    pub loudness: Option<f64>,
    #[serde(default)]
    pub duck: Option<Ducks>,
}

/// An RGBA color parsed from `#rgb`, `#rrggbb` or `#rrggbbaa`.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub struct Rgba(pub [u8; 4]);

impl Rgba {
    pub const BLACK: Rgba = Rgba([0, 0, 0, 255]);
    pub const WHITE: Rgba = Rgba([255, 255, 255, 255]);

    pub fn parse(s: &str) -> Option<Rgba> {
        let h = s.strip_prefix('#')?;
        let hex = |i: usize, n: usize| u8::from_str_radix(&h[i..i + n], 16).ok();
        match h.len() {
            3 => {
                let r = hex(0, 1)?;
                let g = hex(1, 1)?;
                let b = hex(2, 1)?;
                Some(Rgba([r * 17, g * 17, b * 17, 255]))
            }
            6 => Some(Rgba([hex(0, 2)?, hex(2, 2)?, hex(4, 2)?, 255])),
            8 => Some(Rgba([hex(0, 2)?, hex(2, 2)?, hex(4, 2)?, hex(6, 2)?])),
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn colors() {
        assert_eq!(Rgba::parse("#fff"), Some(Rgba([255, 255, 255, 255])));
        assert_eq!(Rgba::parse("#102030"), Some(Rgba([16, 32, 48, 255])));
        assert_eq!(Rgba::parse("#10203080"), Some(Rgba([16, 32, 48, 128])));
        assert_eq!(Rgba::parse("102030"), None);
        assert_eq!(Rgba::parse("#12345"), None);
    }

    #[test]
    fn spine_durations() {
        let mut s: SpineItem =
            serde_json::from_value(serde_json::json!({"id": "a", "asset": "x", "in": 1.0, "out": 3.5})).unwrap();
        assert_eq!(s.duration(30), 2.5);
        s.out = None;
        s.frames = Some(45);
        assert_eq!(s.duration(30), 1.5);
    }
}
