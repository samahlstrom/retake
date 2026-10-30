//! Graphics layers drawn with Skia: text (shaped with HarfBuzz through Skia's paragraph module, with a vendored OFL
//! font and no system font fallback), shapes, images, drop shadows and blur, keyframes evaluated per frame.

use crate::composite::Affine;
use crate::expr::Js;
use crate::transform;
use retake_timeline::EditError;
use retake_timeline::keyframe::{ExprEval, Prop};
use retake_timeline::layer::{Align, Content, Layer, Shape, ShapeKind, Text, TextValue};
use retake_timeline::model::Rgba;
use serde_json::Value;
use skia_safe::font_arguments::{VariationPosition, variation_position::Coordinate};
use skia_safe::textlayout::{
    FontCollection, Paragraph, ParagraphBuilder, ParagraphStyle, TextAlign, TextStyle, TypefaceFontProvider,
};
use skia_safe::{
    AlphaType, Canvas, Color4f, ColorType, Data, FontArguments, FontMgr, FourByteTag, ISize, Image, ImageInfo, Matrix,
    Paint, PaintJoin, PaintStyle, RRect, Rect, SamplingOptions, canvas::SaveLayerRec, image_filters, surfaces,
};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::sync::Arc;

/// The built-in `display` font: Baloo 2 (SIL OFL 1.1), vendored in editor/fonts/baloo2.
pub const DISPLAY_FONT: &[u8] = include_bytes!("../../../fonts/baloo2/Baloo2-Variable.ttf");
pub const DISPLAY_WEIGHT: f64 = 800.0;

/// Bumped when drawing code changes in a way that changes pixels: it's part of every layer key.
pub const GRAPHICS_VERSION: &str = "graphics/2";

#[derive(Clone)]
pub struct FontData {
    pub bytes: Arc<Vec<u8>>,
    pub hash: String,
    pub weight: f64,
}

#[derive(Clone)]
pub struct ImageData {
    pub bytes: Arc<Vec<u8>>,
    pub hash: String,
}

/// Fonts and images the graphics may use, loaded once and shared by every painter.
#[derive(Clone, Default)]
pub struct Resources {
    pub fonts: BTreeMap<String, FontData>,
    pub images: BTreeMap<String, ImageData>,
}

impl Resources {
    pub fn text_weight(&self, t: &Text) -> u32 {
        let def = self.fonts.get(t.font_name()).map(|f| f.weight).unwrap_or(400.0);
        t.weight.unwrap_or(def).round() as u32
    }

    /// Every (font, weight) the given layers use.
    pub fn faces<'a>(&self, layers: impl IntoIterator<Item = &'a Layer>) -> BTreeSet<(String, u32)> {
        layers
            .into_iter()
            .filter_map(|l| match &l.content {
                Content::Text(t) => Some((t.font_name().to_string(), self.text_weight(t))),
                _ => None,
            })
            .collect()
    }
}

fn family(name: &str, weight: u32) -> String {
    format!("{name}@{weight}")
}

fn color4f(s: Option<&str>, default: Rgba) -> Color4f {
    let c = s.and_then(Rgba::parse).unwrap_or(default).0;
    Color4f::new(c[0] as f32 / 255.0, c[1] as f32 / 255.0, c[2] as f32 / 255.0, c[3] as f32 / 255.0)
}

fn sk(m: &Affine) -> Matrix {
    Matrix::new_all(m.a as f32, m.c as f32, m.e as f32, m.b as f32, m.d as f32, m.f as f32, 0.0, 0.0, 1.0)
}

/// Draws graphics layers into RGBA frames. One per thread (Skia objects and the JS engine stay on their thread).
pub struct Painter {
    res: Arc<Resources>,
    fonts: FontCollection,
    faces: BTreeSet<(String, u32)>,
    images: HashMap<String, Image>,
    pub js: Js,
}

impl Painter {
    pub fn new(res: Arc<Resources>, faces: &BTreeSet<(String, u32)>) -> Result<Painter, EditError> {
        let mgr = FontMgr::custom_empty().unwrap_or_default();
        let mut provider = TypefaceFontProvider::new();
        for (name, weight) in faces {
            let fd = res
                .fonts
                .get(name)
                .ok_or_else(|| EditError::new("font_unknown", format!("font `{name}` isn't defined")))?;
            let base = mgr
                .new_from_data(Data::new_copy(&fd.bytes), None)
                .ok_or_else(|| EditError::new("font_invalid", format!("font `{name}` isn't a font Skia can read")))?;
            let coords = [Coordinate { axis: FourByteTag::from_chars('w', 'g', 'h', 't'), value: *weight as f32 }];
            let args = FontArguments::new().set_variation_design_position(VariationPosition { coordinates: &coords });
            let face = base.clone_with_arguments(&args).unwrap_or(base);
            provider.register_typeface(face, Some(family(name, *weight).as_str()));
        }
        let mut fonts = FontCollection::new();
        fonts.set_asset_font_manager(Some(provider.into()));
        fonts.disable_font_fallback();
        Ok(Painter { res, fonts, faces: faces.clone(), images: HashMap::new(), js: Js::new()? })
    }

    fn image(&mut self, asset: &str) -> Result<Image, EditError> {
        if let Some(i) = self.images.get(asset) {
            return Ok(i.clone());
        }
        let d = self
            .res
            .images
            .get(asset)
            .ok_or_else(|| EditError::new("asset_unknown", format!("image asset `{asset}` wasn't loaded")))?;
        let img = Image::from_encoded(Data::new_copy(&d.bytes)).ok_or_else(|| {
            EditError::new("image_invalid", format!("asset `{asset}` isn't an image Skia can decode"))
        })?;
        let img = img.with_default_mipmaps().unwrap_or(img);
        self.images.insert(asset.to_string(), img.clone());
        Ok(img)
    }

    fn paragraph(&self, t: &Text, text: &str, paint: &Paint, width: f32) -> Result<Paragraph, EditError> {
        let w = self.res.text_weight(t);
        if !self.faces.contains(&(t.font_name().to_string(), w)) {
            return Err(EditError::new("font_unknown", format!("font {}@{w} wasn't prepared", t.font_name())));
        }
        let mut ts = TextStyle::new();
        ts.set_font_families(&[family(t.font_name(), w)]);
        ts.set_font_size(t.size.unwrap_or(Text::DEFAULT_SIZE) as f32);
        ts.set_height(t.line_height.unwrap_or(1.12) as f32);
        ts.set_height_override(true);
        ts.set_half_leading(true);
        ts.set_foreground_paint(paint);
        let mut ps = ParagraphStyle::new();
        ps.set_text_align(match t.align {
            Align::Left => TextAlign::Left,
            Align::Center => TextAlign::Center,
            Align::Right => TextAlign::Right,
        });
        ps.set_apply_rounding_hack(false);
        ps.set_text_style(&ts);
        let mut b = ParagraphBuilder::new(&ps, self.fonts.clone());
        b.push_style(&ts);
        b.add_text(text);
        let mut p = b.build();
        p.layout(width);
        Ok(p)
    }

    /// Lays out a text layer: the paragraphs to paint (stroke first) and the content box.
    fn text(&mut self, l: &Layer, t: &Text, time: f64, props: &Value) -> Result<(Vec<Paragraph>, f64, f64), EditError> {
        let mut s = match &t.text {
            TextValue::Str(s) => s.clone(),
            TextValue::Expr { expr } => {
                self.js.eval_text(expr, time, props).map_err(|e| e.at(format!("layer {}", l.id)))?
            }
        };
        if t.uppercase {
            s = s.to_uppercase();
        }
        let mut fill = Paint::new(color4f(t.fill.as_deref(), Rgba::WHITE), None);
        fill.set_anti_alias(true);
        let max_w = t.max_width.map(|w| w as f32).unwrap_or(1.0e6);
        let probe = self.paragraph(t, &s, &fill, max_w)?;
        let w = probe.longest_line().ceil().max(1.0);
        let mut out = vec![];
        if let Some(st) = &t.stroke
            && st.width > 0.0
        {
            let mut sp = Paint::new(color4f(Some(&st.color), Rgba::BLACK), None);
            sp.set_anti_alias(true);
            sp.set_style(PaintStyle::Stroke);
            sp.set_stroke_width((st.width * 2.0) as f32);
            sp.set_stroke_join(PaintJoin::Round);
            out.push(self.paragraph(t, &s, &sp, w)?);
        }
        let p = self.paragraph(t, &s, &fill, w)?;
        let h = p.height() as f64;
        out.push(p);
        Ok((out, w as f64, h))
    }

    fn eval1(&mut self, p: &Option<Prop>, t: f64, props: &Value) -> Result<Option<f64>, EditError> {
        match p {
            Some(p) => Ok(p.eval(t, props, &mut self.js)?.first().copied()),
            None => Ok(None),
        }
    }

    /// Draws one non-video layer at composition time `t` onto `canvas` (whose matrix maps composition pixels).
    pub fn draw_layer(
        &mut self,
        canvas: &Canvas,
        l: &Layer,
        t: f64,
        props: &Value,
        comp: (f64, f64),
    ) -> Result<(), EditError> {
        if !l.visible_at(t) || l.is_video() {
            return Ok(());
        }
        enum Body {
            Text(Vec<Paragraph>),
            Shape(Shape),
            Image(Image, Rect),
        }
        let (body, w, h) = match &l.content {
            Content::Text(tx) => {
                let (ps, w, h) = self.text(l, tx, t, props)?;
                (Body::Text(ps), w, h)
            }
            Content::Shape(s) => (Body::Shape(s.clone()), s.size[0], s.size[1]),
            Content::Image(im) => {
                let img = self.image(&im.asset)?;
                let (iw, ih) = (img.width() as f64, img.height() as f64);
                let c = transform::crop(l, iw, ih, t, props, &mut self.js)?;
                let (w, h) = match im.size {
                    Some([Some(w), Some(h)]) => (w, h),
                    Some([Some(w), None]) => (w, w * c[3] / c[2].max(1e-9)),
                    Some([None, Some(h)]) => (h * c[2] / c[3].max(1e-9), h),
                    _ => (c[2], c[3]),
                };
                (Body::Image(img, Rect::from_xywh(c[0] as f32, c[1] as f32, c[2] as f32, c[3] as f32)), w, h)
            }
            Content::Video(_) => return Ok(()),
        };
        let placed = transform::place(l, (w, h), comp, t, props, &mut self.js)?;
        if placed.opacity <= 0.0 {
            return Ok(());
        }
        let blur = self.eval1(&l.blur, t, props)?.unwrap_or(0.0).max(0.0);
        let mut filter =
            if blur > 0.0 { image_filters::blur((blur as f32, blur as f32), None, None, None) } else { None };
        if let Some(sh) = &l.shadow {
            let sigma = (sh.blur / 2.0).max(0.0) as f32;
            let col = color4f(sh.color.as_deref(), Rgba([0, 0, 0, 160]));
            filter = image_filters::drop_shadow((sh.dx as f32, sh.dy as f32), (sigma, sigma), col, None, filter, None);
        }
        let layered = placed.opacity < 1.0 || filter.is_some();
        if layered {
            let mut lp = Paint::default();
            lp.set_alpha_f(placed.opacity as f32);
            if let Some(f) = filter {
                lp.set_image_filter(f);
            }
            canvas.save_layer(&SaveLayerRec::default().paint(&lp));
        } else {
            canvas.save();
        }
        canvas.concat(&sk(&placed.m));
        match body {
            Body::Text(ps) => {
                for p in &ps {
                    p.paint(canvas, (0.0, 0.0));
                }
            }
            Body::Shape(s) => {
                let r = Rect::from_xywh(0.0, 0.0, s.size[0] as f32, s.size[1] as f32);
                let draw = |paint: &Paint| match s.shape {
                    ShapeKind::Rect => match s.radius {
                        Some(rad) if rad > 0.0 => {
                            canvas.draw_rrect(RRect::new_rect_xy(r, rad as f32, rad as f32), paint);
                        }
                        _ => {
                            canvas.draw_rect(r, paint);
                        }
                    },
                    ShapeKind::Ellipse => {
                        canvas.draw_oval(r, paint);
                    }
                };
                if let Some(f) = &s.fill {
                    let mut p = Paint::new(color4f(Some(f), Rgba::WHITE), None);
                    p.set_anti_alias(true);
                    draw(&p);
                }
                if let Some(st) = &s.stroke
                    && st.width > 0.0
                {
                    let mut p = Paint::new(color4f(Some(&st.color), Rgba::BLACK), None);
                    p.set_anti_alias(true);
                    p.set_style(PaintStyle::Stroke);
                    p.set_stroke_width(st.width as f32);
                    draw(&p);
                }
            }
            Body::Image(img, src) => {
                let mut p = Paint::default();
                p.set_anti_alias(true);
                let dst = Rect::from_xywh(0.0, 0.0, w as f32, h as f32);
                let sampling = SamplingOptions::new(skia_safe::FilterMode::Linear, skia_safe::MipmapMode::Linear);
                canvas.draw_image_rect_with_sampling_options(
                    &img,
                    Some((&src, skia_safe::canvas::SrcRectConstraint::Strict)),
                    dst,
                    sampling,
                    &p,
                );
            }
        }
        canvas.restore();
        Ok(())
    }

    /// Everything about `layers` that can change over time, evaluated at composition time `t`: visibility, the
    /// transform, crop and blur, and expression text. Two times with the same state draw the same pixels (the rest of
    /// a layer is constant), so an animated run skips a frame whose state equals the previous frame's.
    pub fn state(&mut self, layers: &[Layer], props: &Value, t: f64) -> Result<String, EditError> {
        let mut out = String::new();
        for l in layers {
            if !l.visible_at(t) {
                out.push_str("-;");
                continue;
            }
            let tr = &l.transform;
            for p in [&tr.position, &tr.scale, &tr.rotation, &tr.opacity, &l.crop, &l.blur] {
                match p {
                    Some(p) => out.push_str(&format!("{:?},", p.eval(t, props, &mut self.js)?)),
                    None => out.push_str("_,"),
                }
            }
            if let Content::Text(Text { text: TextValue::Expr { expr }, .. }) = &l.content {
                let s = self.js.eval_text(expr, t, props).map_err(|e| e.at(format!("layer {}", l.id)))?;
                out.push_str(&format!("{s:?}"));
            }
            out.push(';');
        }
        Ok(out)
    }

    /// Renders a run of graphics layers at composition time `t` into a `w` × `h` premultiplied RGBA frame. `scale`
    /// maps composition pixels to output pixels.
    pub fn render(
        &mut self,
        layers: &[Layer],
        props: &Value,
        (w, h): (u32, u32),
        scale: f64,
        comp: (f64, f64),
        t: f64,
    ) -> Result<Vec<u8>, EditError> {
        let info = ImageInfo::new(ISize::new(w as i32, h as i32), ColorType::RGBA8888, AlphaType::Premul, None);
        let mut surface = surfaces::raster(&info, None, None)
            .ok_or_else(|| EditError::new("skia", format!("can't make a {w}x{h} surface")))?;
        {
            let canvas = surface.canvas();
            canvas.clear(Color4f::new(0.0, 0.0, 0.0, 0.0));
            canvas.save();
            canvas.scale((scale as f32, scale as f32));
            for l in layers {
                self.draw_layer(canvas, l, t, props, comp)?;
            }
            canvas.restore();
        }
        let mut out = vec![0u8; (w * h * 4) as usize];
        if !surface.read_pixels(&info, &mut out, (w * 4) as usize, (0, 0)) {
            return Err(EditError::new("skia", "reading pixels back failed"));
        }
        Ok(out)
    }
}

/// Encodes a premultiplied RGBA frame (opaque) as PNG.
pub fn png(px: &[u8], w: u32, h: u32) -> Result<Vec<u8>, EditError> {
    let info = ImageInfo::new(ISize::new(w as i32, h as i32), ColorType::RGBA8888, AlphaType::Premul, None);
    let img = skia_safe::images::raster_from_data(&info, Data::new_copy(px), (w * 4) as usize)
        .ok_or_else(|| EditError::new("skia", "can't wrap the frame"))?;
    #[allow(deprecated)]
    let data = img
        .encode(None, skia_safe::EncodedImageFormat::PNG, None)
        .ok_or_else(|| EditError::new("skia", "PNG encoding failed"))?;
    Ok(data.as_bytes().to_vec())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::composite::alpha_bbox;
    use retake_timeline::layer::parse_layer;
    use serde_json::json;

    fn res() -> Arc<Resources> {
        let mut r = Resources::default();
        r.fonts.insert(
            "display".into(),
            FontData { bytes: Arc::new(DISPLAY_FONT.to_vec()), hash: "x".into(), weight: DISPLAY_WEIGHT },
        );
        Arc::new(r)
    }

    fn painter(layers: &[Layer]) -> Painter {
        let r = res();
        let faces = r.faces(layers);
        Painter::new(r, &faces).unwrap()
    }

    #[test]
    fn text_draws_centered_and_scales() {
        let l = parse_layer(&json!({"id": "t", "type": "text", "text": "HELLO", "size": 80,
            "stroke": {"color": "#000", "width": 6}, "shadow": {"dx": 4, "dy": 6, "blur": 8}}))
        .unwrap();
        let mut p = painter(std::slice::from_ref(&l));
        let px = p.render(std::slice::from_ref(&l), &json!({}), (640, 360), 1.0, (640.0, 360.0), 0.0).unwrap();
        let b = alpha_bbox(&px, 640, 360).unwrap();
        // Centered within a few pixels (the shadow pushes the box right and down).
        let cx = b[0] as f64 + b[2] as f64 / 2.0;
        let cy = b[1] as f64 + b[3] as f64 / 2.0;
        assert!((cx - 320.0).abs() < 12.0 && (cy - 180.0).abs() < 16.0, "{b:?}");
        assert!(b[2] > 200 && b[2] < 400, "five 80 px glyphs are a few hundred px wide: {b:?}");
        // Half scale: half the box.
        let half = p.render(std::slice::from_ref(&l), &json!({}), (320, 180), 0.5, (640.0, 360.0), 0.0).unwrap();
        let hb = alpha_bbox(&half, 320, 180).unwrap();
        assert!((hb[2] as f64 - b[2] as f64 / 2.0).abs() <= 3.0, "{hb:?} vs {b:?}");
    }

    #[test]
    fn rendering_is_deterministic_and_keyframed() {
        let l = parse_layer(&json!({"id": "t", "type": "text", "text": {"expr": "'N=' + Math.round(t * 10)"},
            "transform": {"scale": {"keys": [{"t": 0, "v": 2.0, "ease": "out-back"}, {"t": 1, "v": 1.0}]}}}))
        .unwrap();
        let mut p = painter(std::slice::from_ref(&l));
        let ls = std::slice::from_ref(&l);
        let a = p.render(ls, &json!({}), (320, 180), 1.0, (320.0, 180.0), 0.5).unwrap();
        let _ = p.render(ls, &json!({}), (320, 180), 1.0, (320.0, 180.0), 0.9).unwrap();
        let b = p.render(ls, &json!({}), (320, 180), 1.0, (320.0, 180.0), 0.5).unwrap();
        assert_eq!(a, b, "same t, same pixels, whatever came before");
        let mut q = painter(ls);
        assert_eq!(q.render(ls, &json!({}), (320, 180), 1.0, (320.0, 180.0), 0.5).unwrap(), a, "fresh painter");
        let c = p.render(ls, &json!({}), (320, 180), 1.0, (320.0, 180.0), 1.0).unwrap();
        assert_ne!(a, c);
    }

    #[test]
    fn shapes_opacity_and_visibility() {
        let l = parse_layer(&json!({"id": "s", "type": "shape", "shape": "rect", "size": [100, 50], "fill": "#ff0000",
            "from": 1.0, "transform": {"position": [60, 40], "opacity": 0.5}}))
        .unwrap();
        let mut p = painter(&[]);
        let ls = std::slice::from_ref(&l);
        let before = p.render(ls, &json!({}), (200, 100), 1.0, (200.0, 100.0), 0.5).unwrap();
        assert!(alpha_bbox(&before, 200, 100).is_none(), "not visible before `from`");
        let px = p.render(ls, &json!({}), (200, 100), 1.0, (200.0, 100.0), 1.0).unwrap();
        assert_eq!(alpha_bbox(&px, 200, 100), Some([10, 15, 100, 50]));
        let c = &px[((40 * 200 + 60) * 4) as usize..][..4];
        assert!((c[3] as i32 - 128).abs() <= 1 && (c[0] as i32 - 128).abs() <= 1, "{c:?}");
    }

    #[test]
    fn png_round_trip_header() {
        let px = vec![255u8; 4 * 4 * 4];
        let b = png(&px, 4, 4).unwrap();
        assert_eq!(&b[1..4], b"PNG");
    }
}
