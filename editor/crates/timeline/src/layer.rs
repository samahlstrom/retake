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
    #[serde(default, skip_serializing_if = "Blend::is_normal")]
    pub blend: Blend,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mask: Option<Mask>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub matte: Option<Matte>,
}

impl Layer {
    pub fn visible_at(&self, t: f64) -> bool {
        self.from.is_none_or(|f| t >= f) && self.to.is_none_or(|e| t < e)
    }
    pub fn is_video(&self) -> bool {
        matches!(self.content, Content::Video(_))
    }
    /// True when the layer or anything inside it (a group's children) is footage.
    pub fn has_video(&self) -> bool {
        match &self.content {
            Content::Video(_) => true,
            Content::Group(g) => g.layers.iter().any(Layer::has_video),
            _ => false,
        }
    }
    /// True when the layer is turned in 3D or corner-pinned (a projective transform).
    pub fn is_projective(&self) -> bool {
        let t = &self.transform;
        t.corners.is_some() || t.rotation_x.is_some() || t.rotation_y.is_some()
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
    Group(Group),
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
    pub rotation_x: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rotation_y: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub perspective: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub corners: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub opacity: Option<Prop>,
}

impl Transform {
    /// Every prop of the transform, for state signatures and static checks.
    pub fn props(&self) -> [&Option<Prop>; 8] {
        [
            &self.position,
            &self.scale,
            &self.rotation,
            &self.rotation_x,
            &self.rotation_y,
            &self.perspective,
            &self.corners,
            &self.opacity,
        ]
    }
}

/// How a layer combines with what's under it (spec/timeline.md, "Blend modes").
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default, Hash)]
#[serde(rename_all = "lowercase")]
pub enum Blend {
    #[default]
    Normal,
    Add,
    Screen,
    Multiply,
    Overlay,
    Lighten,
    Darken,
}

impl Blend {
    pub fn is_normal(&self) -> bool {
        *self == Blend::Normal
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum MaskShape {
    #[default]
    Rect,
    Ellipse,
    Path,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum StrokeAlign {
    #[default]
    Inside,
    Center,
    Outside,
}

/// An outline along a mask's edge.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct MaskStroke {
    pub color: String,
    pub width: f64,
    #[serde(default)]
    pub align: StrokeAlign,
}

/// A shape that cuts a layer out, in its content box's pixels (spec/timeline.md, "Masks and mattes").
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, Default)]
pub struct Mask {
    #[serde(default)]
    pub shape: MaskShape,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub rect: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub radius: Option<Prop>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub feather: Option<Prop>,
    #[serde(default)]
    pub invert: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stroke: Option<MaskStroke>,
}

impl Mask {
    pub fn props(&self) -> [&Option<Prop>; 3] {
        [&self.rect, &self.radius, &self.feather]
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default, Hash)]
#[serde(rename_all = "kebab-case")]
pub enum MatteMode {
    #[default]
    Alpha,
    AlphaInverted,
    Luma,
    LumaInverted,
}

/// A track matte: another layer of the same list, drawn only as this layer's matte.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Matte {
    pub layer: String,
    #[serde(default)]
    pub mode: MatteMode,
}

/// A group of layers drawn as one picture (an opacity group). Its content box is the composition frame.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Group {
    pub layers: Vec<Layer>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Resample {
    #[default]
    Bilinear,
    Lanczos,
    /// Bilinear from the full-size source, without pre-shrinking or mip levels (OpenCV's `INTER_LINEAR` warps).
    Unfiltered,
}

impl Resample {
    pub fn is_default(&self) -> bool {
        *self == Resample::Bilinear
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Hinting {
    #[default]
    None,
    Slight,
    Normal,
    Full,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Metrics {
    #[default]
    Skia,
    Pillow,
}

/// One box or several (`delogo`).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Boxes {
    One([f64; 4]),
    Many(Vec<[f64; 4]>),
}

impl Boxes {
    pub fn list(&self) -> Vec<[f64; 4]> {
        match self {
            Boxes::One(b) => vec![*b],
            Boxes::Many(v) => v.clone(),
        }
    }
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
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hinting: Option<Hinting>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub subpixel: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub metrics: Option<Metrics>,
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
    Path,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Shape {
    pub shape: ShapeKind,
    /// The content box. A path defaults to its right and bottom extent (the renderer measures it).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub size: Option<[f64; 2]>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub radius: Option<f64>,
    /// SVG path data, for `shape: "path"`.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub fill: Option<Fill>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stroke: Option<Stroke>,
}

/// A shape's fill: a color, or a linear gradient in the layer's own pixels.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(untagged)]
pub enum Fill {
    Color(String),
    Gradient(Gradient),
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Gradient {
    /// `[x0, y0, x1, y1]`: where the first and the last color are.
    pub linear: [f64; 4],
    pub colors: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stops: Option<Vec<f64>>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Image {
    pub asset: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub size: Option<[Option<f64>; 2]>,
    #[serde(default, skip_serializing_if = "Resample::is_default")]
    pub resample: Resample,
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
    #[serde(default, skip_serializing_if = "Resample::is_default")]
    pub resample: Resample,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub delogo: Option<Boxes>,
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
