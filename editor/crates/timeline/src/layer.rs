//! Typed layers, after anchors in them have been resolved to composition seconds.

use crate::keyframe::Prop;
use crate::model::AudioOpts;
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Layer {
    pub id: String,
    #[serde(flatten)]
    pub content: Content,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub from: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub to: Option<f64>,
    #[serde(default)]
    pub transform: Transform,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub crop: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub blur: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shadow: Option<Shadow>,
}

impl Layer {
    pub fn visible_at(&self, t: f64) -> bool {
        self.from.is_none_or(|f| t >= f) && self.to.is_none_or(|e| t < e)
    }
    pub fn is_video(&self) -> bool {
        matches!(self.content, Content::Video(_))
    }
    pub fn is_raster(&self) -> bool {
        matches!(self.content, Content::Raster(_))
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "lowercase")]
pub enum Content {
    Text(Text),
    Shape(Shape),
    Image(Image),
    Video(Video),
    Raster(Raster),
}

/// Frames drawn by a component's program (spec/components.md, "Backends"). Only expansion makes one.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Raster {
    /// The component, `<name>@<version>`.
    pub component: String,
    /// The component's design canvas.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub canvas: Option<[u32; 2]>,
    pub backend: RasterBackend,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RasterBackend {
    pub command: Vec<String>,
    /// The package folder: the program's working directory, and where a relative program path resolves.
    pub dir: String,
    /// The package hash (it covers the program's bytes).
    pub hash: String,
    /// Seconds one request may take (default 60).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub timeout: Option<f64>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, Default)]
pub struct Transform {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub anchor: Option<[f64; 2]>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub position: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scale: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rotation: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub opacity: Option<Prop>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Stroke {
    pub color: String,
    pub width: f64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Shadow {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color: Option<String>,
    #[serde(default)]
    pub dx: f64,
    #[serde(default)]
    pub dy: f64,
    #[serde(default)]
    pub blur: f64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum TextValue {
    Str(String),
    Expr { expr: String },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Align {
    Left,
    #[default]
    Center,
    Right,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Text {
    pub text: TextValue,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub font: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub size: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub weight: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fill: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stroke: Option<Stroke>,
    #[serde(default)]
    pub align: Align,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub line_height: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_width: Option<f64>,
    #[serde(default)]
    pub uppercase: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub highlight: Option<Highlight>,
}

/// Words `[a, b)` of a text layer drawn in another fill (a karaoke caption's current word, an emphasized word).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Highlight {
    pub words: Prop,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fill: Option<String>,
}

impl Text {
    pub const DEFAULT_SIZE: f64 = 64.0;
    pub fn font_name(&self) -> &str {
        self.font.as_deref().unwrap_or("display")
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ShapeKind {
    Rect,
    Ellipse,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Shape {
    pub shape: ShapeKind,
    pub size: [f64; 2],
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub radius: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fill: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stroke: Option<Stroke>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Image {
    pub asset: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub size: Option<[Option<f64>; 2]>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Fit {
    #[default]
    Contain,
    Cover,
    Stretch,
    None,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Clock {
    #[default]
    Local,
    Timeline,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Video {
    pub asset: String,
    /// Source seconds at composition time 0 (a `sync` has been resolved into it).
    #[serde(rename = "in", default)]
    pub in_: f64,
    #[serde(default)]
    pub fit: Fit,
    #[serde(rename = "loop", default)]
    pub loop_: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hold_from: Option<f64>,
    #[serde(default)]
    pub clock: Clock,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub audio: Option<AudioOpts>,
}

/// Parses a resolved layer value (anchors already replaced by numbers).
pub fn parse_layer(v: &Value) -> Result<Layer, String> {
    serde_json::from_value(v.clone()).map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn layers_parse_by_type() {
        let t = parse_layer(&json!({"id": "a", "type": "text", "text": "HI", "size": 80,
            "transform": {"position": [960, 540], "scale": {"keys": [{"t": 0, "v": 1.2}, {"t": 0.1, "v": 1}]}}}))
        .unwrap();
        assert!(matches!(t.content, Content::Text(_)));
        let v = parse_layer(&json!({"id": "b", "type": "video", "asset": "x", "in": 2.5, "fit": "cover"})).unwrap();
        match v.content {
            Content::Video(v) => {
                assert_eq!(v.in_, 2.5);
                assert_eq!(v.fit, Fit::Cover);
            }
            _ => panic!(),
        }
        let s =
            parse_layer(&json!({"id": "c", "type": "shape", "shape": "rect", "size": [10, 20], "from": 1.0})).unwrap();
        assert!(s.visible_at(1.0) && !s.visible_at(0.5));
        assert!(parse_layer(&json!({"id": "d", "type": "blob"})).is_err());
    }

    #[test]
    fn layers_round_trip() {
        let src = json!({"id": "a", "type": "image", "asset": "logo", "size": [200, null], "from": 0.5, "to": 2.0,
            "transform": {"opacity": {"expr": "t"}}});
        let l = parse_layer(&src).unwrap();
        let back = serde_json::to_value(&l).unwrap();
        assert_eq!(parse_layer(&back).unwrap(), l);
    }
}
