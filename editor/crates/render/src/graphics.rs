//! Graphics layers drawn with Skia: text (shaped with HarfBuzz through Skia's paragraph module, with a vendored OFL
//! font and no system font fallback), shapes and paths, images, groups, masks, track mattes inside groups, drop
//! shadows and blur, keyframes evaluated per frame, all through a projective transform (perspective, corner pins).
//! Also the Skia helpers the compositor uses on footage: mask coverage, mask outlines and filters on a frame.

use crate::composite::{Coverage, Frame};
use crate::expr::Js;
use crate::transform::{self, Proj};
use retake_timeline::EditError;
use retake_timeline::keyframe::{ExprEval, Prop};
use retake_timeline::layer::{
    Align, Blend, Content, Hinting, Layer, Mask, MaskShape, MatteMode, Metrics, Resample, Shadow, Shape, ShapeKind,
    StrokeAlign, Text, TextValue,
};
use retake_timeline::model::Rgba;
use serde_json::Value;
use skia_safe::font_arguments::{VariationPosition, variation_position::Coordinate};
use skia_safe::textlayout::{
    FontCollection, Paragraph, ParagraphBuilder, ParagraphStyle, TextAlign, TextStyle, TypefaceFontProvider,
};
use skia_safe::{
    AlphaType, BlendMode, BlurStyle, Canvas, ClipOp, Color4f, ColorFilter, ColorType, Data, FontArguments, FontHinting,
    FontMgr, FourByteTag, ISize, Image, ImageInfo, MaskFilter, Matrix, Paint, PaintJoin, PaintStyle, Path, RRect, Rect,
    SamplingOptions, canvas::SaveLayerRec, font::Edging, image_filters, surfaces,
};
use std::collections::{BTreeMap, BTreeSet, HashMap, HashSet};
use std::sync::Arc;

/// The built-in `display` font: Baloo 2 (SIL OFL 1.1), vendored in editor/fonts/baloo2.
pub const DISPLAY_FONT: &[u8] = include_bytes!("../../../fonts/baloo2/Baloo2-Variable.ttf");
pub const DISPLAY_WEIGHT: f64 = 800.0;

/// Bumped when drawing code changes in a way that changes pixels: it's part of every layer key. graphics/3:
/// projective transforms, groups, masks, mattes, paths, text options, Lanczos images.
pub const GRAPHICS_VERSION: &str = "graphics/3";

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

/// Every layer of a list, a group's children included (depth first).
pub fn all_layers<'a>(layers: impl IntoIterator<Item = &'a Layer>) -> Vec<&'a Layer> {
    let mut out = vec![];
    fn walk<'a>(l: &'a Layer, out: &mut Vec<&'a Layer>) {
        out.push(l);
        if let Content::Group(g) = &l.content {
            for c in &g.layers {
                walk(c, out);
            }
        }
    }
    for l in layers {
        walk(l, &mut out);
    }
    out
}

impl Resources {
    pub fn text_weight(&self, t: &Text) -> u32 {
        let def = self.fonts.get(t.font_name()).map(|f| f.weight).unwrap_or(400.0);
        t.weight.unwrap_or(def).round() as u32
    }

    /// Every (font, weight) the given layers use, inside groups too.
    pub fn faces<'a>(&self, layers: impl IntoIterator<Item = &'a Layer>) -> BTreeSet<(String, u32)> {
        all_layers(layers)
            .into_iter()
            .filter_map(|l| match &l.content {
                Content::Text(t) => Some((t.font_name().to_string(), self.text_weight(t))),
                _ => None,
            })
            .collect()
    }
}

/// Byte ranges of words `[a, b)` of `s` (words are separated by whitespace, counted from 0).
pub fn word_ranges(s: &str, ab: &[f64]) -> Vec<(usize, usize)> {
    let (a, b) = match ab {
        [a, b] if a.is_finite() && b.is_finite() => (a.floor() as i64, b.floor() as i64),
        _ => return vec![],
    };
    if b <= a || b <= 0 {
        return vec![];
    }
    let mut out = vec![];
    let mut idx = 0i64;
    let mut start: Option<usize> = None;
    for (i, c) in s.char_indices().chain(std::iter::once((s.len(), ' '))) {
        if c.is_whitespace() {
            if let Some(st) = start.take() {
                if idx >= a && idx < b {
                    out.push((st, i));
                }
                idx += 1;
            }
        } else if start.is_none() {
            start = Some(i);
        }
    }
    out
}

fn family(name: &str, weight: u32) -> String {
    format!("{name}@{weight}")
}

fn color4f(s: Option<&str>, default: Rgba) -> Color4f {
    let c = s.and_then(Rgba::parse).unwrap_or(default).0;
    Color4f::new(c[0] as f32 / 255.0, c[1] as f32 / 255.0, c[2] as f32 / 255.0, c[3] as f32 / 255.0)
}

/// A projective map as a Skia matrix (same row-major layout).
pub fn sk(m: &Proj) -> Matrix {
    let p = m.0.map(|x| x as f32);
    Matrix::new_all(p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8])
}

pub fn blend_mode(b: Blend) -> BlendMode {
    match b {
        Blend::Normal => BlendMode::SrcOver,
        Blend::Add => BlendMode::Plus,
        Blend::Screen => BlendMode::Screen,
        Blend::Multiply => BlendMode::Multiply,
        Blend::Overlay => BlendMode::Overlay,
        Blend::Lighten => BlendMode::Lighten,
        Blend::Darken => BlendMode::Darken,
    }
}

fn eval1(p: &Option<Prop>, t: f64, props: &Value, ex: &mut dyn ExprEval) -> Result<Option<f64>, EditError> {
    match p {
        Some(p) => Ok(p.eval(t, props, ex)?.first().copied()),
        None => Ok(None),
    }
}

/// A shape's content box: its `size`, or a path's right and bottom extent.
pub fn shape_size(s: &Shape) -> [f64; 2] {
    if let Some(sz) = s.size {
        return sz;
    }
    match s.path.as_deref().and_then(skia_safe::utils::parse_path::from_svg) {
        Some(p) => {
            let b = p.bounds();
            [b.right.max(0.0) as f64, b.bottom.max(0.0) as f64]
        }
        None => [0.0, 0.0],
    }
}

fn svg_path(d: &str, what: &str) -> Result<Path, EditError> {
    skia_safe::utils::parse_path::from_svg(d)
        .ok_or_else(|| EditError::new("path_invalid", format!("{what}: `{d}` isn't SVG path data Skia can read")))
}

/// A mask's outline in content pixels.
pub fn mask_path(
    mask: &Mask,
    (w, h): (f64, f64),
    t: f64,
    props: &Value,
    ex: &mut dyn ExprEval,
) -> Result<Path, EditError> {
    let r = match &mask.rect {
        Some(p) => p.eval(t, props, ex)?,
        None => vec![0.0, 0.0, w, h],
    };
    let rect = match r.as_slice() {
        [x, y, rw, rh] => Rect::from_xywh(*x as f32, *y as f32, *rw as f32, *rh as f32),
        _ => Rect::from_xywh(0.0, 0.0, w as f32, h as f32),
    };
    Ok(match mask.shape {
        MaskShape::Rect => {
            let rad = eval1(&mask.radius, t, props, ex)?.unwrap_or(0.0).max(0.0) as f32;
            if rad > 0.0 { Path::rrect(RRect::new_rect_xy(rect, rad, rad), None) } else { Path::rect(rect, None) }
        }
        MaskShape::Ellipse => Path::oval(rect, None),
        MaskShape::Path => svg_path(mask.path.as_deref().unwrap_or(""), "mask path")?,
    })
}

/// Cuts what's drawn in the current layer out with a mask (in the current matrix's content pixels), then draws the
/// mask's outline over it.
fn apply_mask(
    canvas: &Canvas,
    mask: &Mask,
    wh: (f64, f64),
    t: f64,
    props: &Value,
    ex: &mut dyn ExprEval,
) -> Result<(), EditError> {
    let path = mask_path(mask, wh, t, props, ex)?;
    let feather = eval1(&mask.feather, t, props, ex)?.unwrap_or(0.0).max(0.0);
    let mut lp = Paint::default();
    lp.set_blend_mode(if mask.invert { BlendMode::DstOut } else { BlendMode::DstIn });
    canvas.save_layer(&SaveLayerRec::default().paint(&lp));
    let mut p = Paint::new(Color4f::new(1.0, 1.0, 1.0, 1.0), None);
    p.set_anti_alias(true);
    if feather > 0.0 {
        p.set_mask_filter(MaskFilter::blur(BlurStyle::Normal, feather as f32, true));
    }
    canvas.draw_path(&path, &p);
    canvas.restore();
    if let Some(st) = &mask.stroke {
        draw_mask_stroke(canvas, &path, st.width, &st.color, st.align);
    }
    Ok(())
}

fn draw_mask_stroke(canvas: &Canvas, path: &Path, width: f64, color: &str, align: StrokeAlign) {
    if width <= 0.0 {
        return;
    }
    let mut p = Paint::new(color4f(Some(color), Rgba::WHITE), None);
    p.set_anti_alias(true);
    p.set_style(PaintStyle::Stroke);
    canvas.save();
    match align {
        StrokeAlign::Inside => {
            canvas.clip_path(path, ClipOp::Intersect, true);
            p.set_stroke_width((width * 2.0) as f32);
        }
        StrokeAlign::Outside => {
            canvas.clip_path(path, ClipOp::Difference, true);
            p.set_stroke_width((width * 2.0) as f32);
        }
        StrokeAlign::Center => {
            p.set_stroke_width(width as f32);
        }
    }
    canvas.draw_path(path, &p);
    canvas.restore();
}

/// The image filter of a layer's `blur` and `shadow` (sigmas and offsets in the canvas's current pixels).
fn effects_filter(blur: f64, shadow: Option<&Shadow>, k: f32) -> Option<skia_safe::ImageFilter> {
    let mut filter = if blur > 0.0 { image_filters::blur((blur as f32 * k, blur as f32 * k), None, None, None) } else { None };
    if let Some(sh) = shadow {
        let sigma = (sh.blur / 2.0).max(0.0) as f32 * k;
        let col = color4f(sh.color.as_deref(), Rgba([0, 0, 0, 160]));
        filter = image_filters::drop_shadow(
            (sh.dx as f32 * k, sh.dy as f32 * k),
            (sigma, sigma),
            col,
            None,
            filter,
            None,
        );
    }
    filter
}

fn surface(w: u32, h: u32, ct: ColorType) -> Result<(skia_safe::Surface, ImageInfo), EditError> {
    let info = ImageInfo::new(ISize::new(w as i32, h as i32), ct, AlphaType::Premul, None);
    let s = surfaces::raster(&info, None, None)
        .ok_or_else(|| EditError::new("skia", format!("can't make a {w}x{h} surface")))?;
    Ok((s, info))
}

/// The coverage of a mask over a `fw` × `fh` frame, for a layer whose content pixels map to output pixels by `m`
/// (the compositor's masks on footage and groups).
#[allow(clippy::too_many_arguments)]
pub fn mask_coverage(
    mask: &Mask,
    wh: (f64, f64),
    m: &Proj,
    t: f64,
    props: &Value,
    ex: &mut dyn ExprEval,
    (fw, fh): (u32, u32),
) -> Result<Coverage, EditError> {
    let (mut s, info) = surface(fw, fh, ColorType::Alpha8)?;
    {
        let c = s.canvas();
        c.clear(Color4f::new(0.0, 0.0, 0.0, if mask.invert { 1.0 } else { 0.0 }));
        c.concat(&sk(m));
        let path = mask_path(mask, wh, t, props, ex)?;
        let feather = eval1(&mask.feather, t, props, ex)?.unwrap_or(0.0).max(0.0);
        let mut p = Paint::new(Color4f::new(1.0, 1.0, 1.0, 1.0), None);
        p.set_anti_alias(true);
        if mask.invert {
            p.set_blend_mode(BlendMode::Clear);
        }
        if feather > 0.0 {
            p.set_mask_filter(MaskFilter::blur(BlurStyle::Normal, feather as f32, true));
        }
        c.draw_path(&path, &p);
    }
    let mut a = vec![0u8; (fw * fh) as usize];
    if !s.read_pixels(&info, &mut a, fw as usize, (0, 0)) {
        return Err(EditError::new("skia", "reading the mask back failed"));
    }
    Ok(Coverage { x: 0, y: 0, w: fw, h: fh, a })
}

/// A mask's outline drawn alone into a transparent `fw` × `fh` frame (premultiplied), if it has one.
#[allow(clippy::too_many_arguments)]
pub fn mask_outline(
    mask: &Mask,
    wh: (f64, f64),
    m: &Proj,
    t: f64,
    props: &Value,
    ex: &mut dyn ExprEval,
    (fw, fh): (u32, u32),
) -> Result<Option<Frame>, EditError> {
    let Some(st) = &mask.stroke else { return Ok(None) };
    let (mut s, info) = surface(fw, fh, ColorType::RGBA8888)?;
    {
        let c = s.canvas();
        c.clear(Color4f::new(0.0, 0.0, 0.0, 0.0));
        c.concat(&sk(m));
        let path = mask_path(mask, wh, t, props, ex)?;
        draw_mask_stroke(c, &path, st.width, &st.color, st.align);
    }
    let mut f = Frame::new(fw, fh);
    if !s.read_pixels(&info, &mut f.px, (fw * 4) as usize, (0, 0)) {
        return Err(EditError::new("skia", "reading the outline back failed"));
    }
    Ok(Some(f))
}

/// A frame with `blur` and a drop `shadow` applied (in output pixels), as Skia's image filters do for graphics.
pub fn filter_frame(src: &Frame, blur: f64, shadow: Option<&Shadow>, k: f64) -> Result<Frame, EditError> {
    let Some(filter) = effects_filter(blur, shadow, k as f32) else { return Ok(src.clone()) };
    let (w, h) = (src.w, src.h);
    let info = ImageInfo::new(ISize::new(w as i32, h as i32), ColorType::RGBA8888, AlphaType::Premul, None);
    let img = skia_safe::images::raster_from_data(&info, Data::new_copy(&src.px), (w * 4) as usize)
        .ok_or_else(|| EditError::new("skia", "can't wrap the frame"))?;
    let (mut s, info) = surface(w, h, ColorType::RGBA8888)?;
    {
        let c = s.canvas();
        c.clear(Color4f::new(0.0, 0.0, 0.0, 0.0));
        let mut p = Paint::default();
        p.set_image_filter(filter);
        c.draw_image(&img, (0, 0), Some(&p));
    }
    let mut f = Frame::new(w, h);
    if !s.read_pixels(&info, &mut f.px, (w * 4) as usize, (0, 0)) {
        return Err(EditError::new("skia", "reading the filtered frame back failed"));
    }
    Ok(f)
}

/// What a layer draws, in its content box.
enum Body {
    /// Paragraphs (stroke first) and their vertical offset in the box.
    Text(Vec<Paragraph>, f32),
    Shape(Shape),
    Image(Image, Rect),
    Group(Vec<Layer>),
}

/// Draws graphics layers into RGBA frames. One per thread (Skia objects and the JS engine stay on their thread).
pub struct Painter {
    res: Arc<Resources>,
    fonts: FontCollection,
    faces: BTreeSet<(String, u32)>,
    images: HashMap<String, Image>,
    /// Lanczos-resized images by (asset, source box, size).
    resized: HashMap<(String, [u64; 4], u32, u32), Image>,
    /// Output pixels per composition pixel of the frame being drawn.
    scale: f64,
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
        Ok(Painter {
            res,
            fonts,
            faces: faces.clone(),
            images: HashMap::new(),
            resized: HashMap::new(),
            scale: 1.0,
            js: Js::new()?,
        })
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

    /// The source box of an image resized once with Lanczos-3 to `tw` × `th` (cached per frame size).
    fn lanczos(&mut self, asset: &str, img: &Image, src: Rect, tw: u32, th: u32) -> Result<Image, EditError> {
        let key = (asset.to_string(), [src.left, src.top, src.right, src.bottom].map(|x| x.to_bits() as u64), tw, th);
        if let Some(i) = self.resized.get(&key) {
            return Ok(i.clone());
        }
        let (w, h) = (img.width() as u32, img.height() as u32);
        let info = ImageInfo::new(ISize::new(w as i32, h as i32), ColorType::RGBA8888, AlphaType::Premul, None);
        let mut px = vec![0u8; (w * h * 4) as usize];
        if !img.read_pixels(&info, &mut px, (w * 4) as usize, (0, 0), skia_safe::image::CachingHint::Allow) {
            return Err(EditError::new("skia", format!("can't read the pixels of `{asset}`")));
        }
        let bx = [src.left as f64, src.top as f64, src.right as f64, src.bottom as f64];
        let out = crate::resample::lanczos_rgba(&px, w, h, bx, tw, th);
        let oi = ImageInfo::new(ISize::new(tw as i32, th as i32), ColorType::RGBA8888, AlphaType::Premul, None);
        let r = skia_safe::images::raster_from_data(&oi, Data::new_copy(&out), (tw * 4) as usize)
            .ok_or_else(|| EditError::new("skia", "can't wrap a resized image"))?;
        if self.resized.len() > 32 {
            self.resized.clear();
        }
        self.resized.insert(key, r.clone());
        Ok(r)
    }

    fn paragraph(&self, t: &Text, text: &str, paint: &Paint, width: f32) -> Result<Paragraph, EditError> {
        self.paragraph_hl(t, text, paint, width, None)
    }

    /// A paragraph whose byte ranges `hl.0` are drawn with the paint `hl.1` (text `highlight`).
    fn paragraph_hl(
        &self,
        t: &Text,
        text: &str,
        paint: &Paint,
        width: f32,
        hl: Option<(&[(usize, usize)], &Paint)>,
    ) -> Result<Paragraph, EditError> {
        let w = self.res.text_weight(t);
        if !self.faces.contains(&(t.font_name().to_string(), w)) {
            return Err(EditError::new("font_unknown", format!("font {}@{w} wasn't prepared", t.font_name())));
        }
        let pillow = t.metrics == Some(Metrics::Pillow);
        let mut ts = TextStyle::new();
        ts.set_font_families(&[family(t.font_name(), w)]);
        ts.set_font_size(t.size.unwrap_or(Text::DEFAULT_SIZE) as f32);
        ts.set_height(t.line_height.unwrap_or(1.12) as f32);
        ts.set_height_override(true);
        ts.set_half_leading(true);
        ts.set_foreground_paint(paint);
        let hinting = t.hinting.or(pillow.then_some(Hinting::Normal));
        if let Some(h) = hinting {
            ts.set_font_hinting(match h {
                Hinting::None => FontHinting::None,
                Hinting::Slight => FontHinting::Slight,
                Hinting::Normal => FontHinting::Normal,
                Hinting::Full => FontHinting::Full,
            });
            ts.set_font_edging(Edging::AntiAlias);
        }
        if let Some(sp) = t.subpixel.or(pillow.then_some(false)) {
            ts.set_subpixel(sp);
        }
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
        match hl {
            Some((ranges, hp)) if !ranges.is_empty() => {
                let mut hs = ts.clone();
                hs.set_foreground_paint(hp);
                let mut pos = 0;
                for &(a, z) in ranges {
                    if a > pos {
                        b.add_text(&text[pos..a]);
                    }
                    b.push_style(&hs);
                    b.add_text(&text[a..z]);
                    b.pop();
                    pos = z;
                }
                if pos < text.len() {
                    b.add_text(&text[pos..]);
                }
            }
            _ => {
                b.add_text(text);
            }
        }
        let mut p = b.build();
        p.layout(width);
        Ok(p)
    }

    /// Lays out a text layer: the paragraphs to paint (stroke first), their vertical offset, and the content box.
    fn text(&mut self, l: &Layer, t: &Text, time: f64, props: &Value) -> Result<(Vec<Paragraph>, f32, f64, f64), EditError> {
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
        let ranges = match &t.highlight {
            Some(h) => word_ranges(&s, &h.words.eval(time, props, &mut self.js)?),
            None => vec![],
        };
        let mut hp =
            Paint::new(color4f(t.highlight.as_ref().and_then(|h| h.fill.as_deref()), Rgba([255, 210, 63, 255])), None);
        hp.set_anti_alias(true);
        let p = self.paragraph_hl(t, &s, &fill, w, Some((&ranges, &hp)))?;
        let h = p.height() as f64;
        let dy = if t.metrics == Some(Metrics::Pillow) { self.pillow_dy(t, &p) } else { 0.0 };
        out.push(p);
        Ok((out, dy, w as f64, h))
    }

    /// Pillow's `anchor="mm"` puts a line's middle between the font's ascender and descender, which FreeType rounds
    /// to whole pixels (the ascender up, the descender down); Skia centres the unrounded ascent and descent in the
    /// line box. The difference, per line (the same for every line of one style), moves the paragraph.
    fn pillow_dy(&self, t: &Text, p: &Paragraph) -> f32 {
        let size = t.size.unwrap_or(Text::DEFAULT_SIZE) as f32;
        let Some(lm) = p.get_line_metrics().into_iter().next() else { return 0.0 };
        let (asc, desc) = (lm.ascent as f32, lm.descent as f32);
        let (asc_r, desc_r) = (asc.ceil(), desc.ceil());
        let _ = size;
        // Skia's middle is at baseline + (desc - asc) / 2; Pillow's at baseline + (desc_r - asc_r) / 2.
        ((desc - asc) - (desc_r - asc_r)) / 2.0
    }

    /// The body and content box of a non-video layer at `t`.
    fn body(&mut self, l: &Layer, t: f64, props: &Value, comp: (f64, f64)) -> Result<Option<(Body, f64, f64)>, EditError> {
        Ok(Some(match &l.content {
            Content::Text(tx) => {
                let (ps, dy, w, h) = self.text(l, tx, t, props)?;
                (Body::Text(ps, dy), w, h)
            }
            Content::Shape(s) => {
                let [w, h] = shape_size(s);
                (Body::Shape(s.clone()), w, h)
            }
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
            Content::Group(g) => (Body::Group(g.layers.clone()), comp.0, comp.1),
            Content::Video(_) => return Ok(None),
        }))
    }

    /// Draws one non-video layer at composition time `t` onto `canvas` (whose matrix maps composition pixels),
    /// combining it with what's already there in `blend`.
    pub fn draw_layer(
        &mut self,
        canvas: &Canvas,
        l: &Layer,
        t: f64,
        props: &Value,
        comp: (f64, f64),
        blend: Blend,
    ) -> Result<(), EditError> {
        if !l.visible_at(t) || l.is_video() {
            return Ok(());
        }
        let Some((body, w, h)) = self.body(l, t, props, comp)? else { return Ok(()) };
        let placed = transform::place(l, (w, h), comp, t, props, &mut self.js)?;
        if placed.opacity <= 0.0 {
            return Ok(());
        }
        let blur = eval1(&l.blur, t, props, &mut self.js)?.unwrap_or(0.0).max(0.0);
        let filter = effects_filter(blur, l.shadow.as_ref(), 1.0);
        let group = matches!(body, Body::Group(_));
        let layered =
            placed.opacity < 1.0 || filter.is_some() || l.mask.is_some() || blend != Blend::Normal || group;
        if layered {
            let mut lp = Paint::default();
            lp.set_alpha_f(placed.opacity as f32);
            lp.set_blend_mode(blend_mode(blend));
            if let Some(f) = filter {
                lp.set_image_filter(f);
            }
            canvas.save_layer(&SaveLayerRec::default().paint(&lp));
        } else {
            canvas.save();
        }
        canvas.concat(&sk(&placed.m));
        match body {
            Body::Text(ps, dy) => {
                for p in &ps {
                    p.paint(canvas, (0.0, dy));
                }
            }
            Body::Shape(s) => {
                let r = Rect::from_xywh(0.0, 0.0, w as f32, h as f32);
                let path = match s.shape {
                    ShapeKind::Path => Some(svg_path(s.path.as_deref().unwrap_or(""), &format!("layer {}", l.id))?),
                    _ => None,
                };
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
                    ShapeKind::Path => {
                        canvas.draw_path(path.as_ref().expect("parsed above"), paint);
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
                let lanczos = matches!(&l.content, Content::Image(i) if i.resample == Resample::Lanczos);
                let (tw, th) = (
                    (w * placed.k2d.0 * self.scale).round().max(1.0) as u32,
                    (h * placed.k2d.1 * self.scale).round().max(1.0) as u32,
                );
                if lanczos && (tw, th) != (src.width() as u32, src.height() as u32) {
                    let asset = match &l.content {
                        Content::Image(i) => i.asset.clone(),
                        _ => unreachable!(),
                    };
                    let r = self.lanczos(&asset, &img, src, tw, th)?;
                    let sampling = SamplingOptions::new(skia_safe::FilterMode::Linear, skia_safe::MipmapMode::None);
                    canvas.draw_image_rect_with_sampling_options(&r, None, dst, sampling, &p);
                } else {
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
            Body::Group(children) => {
                self.draw_list(canvas, &children, t, props, comp, true)?;
            }
        }
        if let Some(m) = &l.mask {
            apply_mask(canvas, m, (w, h), t, props, &mut self.js)?;
        }
        canvas.restore();
        Ok(())
    }

    /// Draws a list of layers in order. Layers used as a track matte are drawn only as mattes. Inside a group
    /// (`nested`), layers keep their blend modes and mattes; a top-level run never has either (the compositor applies
    /// them, since they combine with footage under the run).
    pub fn draw_list(
        &mut self,
        canvas: &Canvas,
        layers: &[Layer],
        t: f64,
        props: &Value,
        comp: (f64, f64),
        nested: bool,
    ) -> Result<(), EditError> {
        let mattes: HashSet<&str> = layers.iter().filter_map(|l| l.matte.as_ref().map(|m| m.layer.as_str())).collect();
        for l in layers {
            if mattes.contains(l.id.as_str()) {
                continue;
            }
            let blend = if nested { l.blend } else { Blend::Normal };
            match (&l.matte, nested) {
                (Some(m), true) => {
                    let Some(ml) = layers.iter().find(|x| x.id == m.layer) else { continue };
                    let mut outer = Paint::default();
                    outer.set_blend_mode(blend_mode(blend));
                    canvas.save_layer(&SaveLayerRec::default().paint(&outer));
                    self.draw_layer(canvas, l, t, props, comp, Blend::Normal)?;
                    self.draw_matte(canvas, ml, m.mode, t, props, comp)?;
                    canvas.restore();
                }
                _ => self.draw_layer(canvas, l, t, props, comp, blend)?,
            }
        }
        Ok(())
    }

    /// Multiplies what the current layer holds by a matte layer's alpha or luma.
    fn draw_matte(
        &mut self,
        canvas: &Canvas,
        ml: &Layer,
        mode: MatteMode,
        t: f64,
        props: &Value,
        comp: (f64, f64),
    ) -> Result<(), EditError> {
        let mut mp = Paint::default();
        mp.set_blend_mode(match mode {
            MatteMode::Alpha | MatteMode::Luma => BlendMode::DstIn,
            MatteMode::AlphaInverted | MatteMode::LumaInverted => BlendMode::DstOut,
        });
        if matches!(mode, MatteMode::Luma | MatteMode::LumaInverted) {
            mp.set_color_filter(ColorFilter::luma());
        }
        canvas.save_layer(&SaveLayerRec::default().paint(&mp));
        self.draw_layer(canvas, ml, t, props, comp, Blend::Normal)?;
        canvas.restore();
        Ok(())
    }

    /// Everything about `layers` that can change over time, evaluated at composition time `t`: visibility, the
    /// transform, crop, blur and mask props, expression text, and the same for a group's children. Two times with the
    /// same state draw the same pixels (the rest of a layer is constant), so an animated run skips a frame whose state
    /// equals the previous frame's.
    pub fn state(&mut self, layers: &[Layer], props: &Value, t: f64) -> Result<String, EditError> {
        let mut out = String::new();
        for l in layers {
            self.layer_state(l, props, t, &mut out)?;
        }
        Ok(out)
    }

    fn layer_state(&mut self, l: &Layer, props: &Value, t: f64, out: &mut String) -> Result<(), EditError> {
        if !l.visible_at(t) {
            out.push_str("-;");
            return Ok(());
        }
        let mut ps: Vec<&Option<Prop>> = l.transform.props().to_vec();
        ps.push(&l.crop);
        ps.push(&l.blur);
        if let Some(m) = &l.mask {
            ps.extend(m.props());
        }
        for p in ps {
            match p {
                Some(p) => out.push_str(&format!("{:?},", p.eval(t, props, &mut self.js)?)),
                None => out.push_str("_,"),
            }
        }
        if let Content::Text(Text { text: TextValue::Expr { expr }, .. }) = &l.content {
            let s = self.js.eval_text(expr, t, props).map_err(|e| e.at(format!("layer {}", l.id)))?;
            out.push_str(&format!("{s:?}"));
        }
        if let Content::Text(Text { highlight: Some(h), .. }) = &l.content {
            out.push_str(&format!("h{:?}", h.words.eval(t, props, &mut self.js)?));
        }
        if let Content::Group(g) = &l.content {
            out.push('[');
            for c in &g.layers {
                self.layer_state(c, props, t, out)?;
            }
            out.push(']');
        }
        out.push(';');
        Ok(())
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
        let (mut surface, info) = surface(w, h, ColorType::RGBA8888)?;
        self.scale = scale;
        {
            let canvas = surface.canvas();
            canvas.clear(Color4f::new(0.0, 0.0, 0.0, 0.0));
            canvas.save();
            canvas.scale((scale as f32, scale as f32));
            self.draw_list(canvas, layers, t, props, comp, false)?;
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
    fn word_ranges_count_whitespace_separated_words() {
        let s = "ONE  TWO\nTHREE";
        assert_eq!(word_ranges(s, &[1.0, 2.0]), vec![(5, 8)]);
        assert_eq!(word_ranges(s, &[0.0, 3.0]), vec![(0, 3), (5, 8), (9, 14)]);
        assert_eq!(word_ranges(s, &[1.7, 2.2]), vec![(5, 8)], "floored");
        assert!(word_ranges(s, &[-1.0, -1.0]).is_empty());
        assert!(word_ranges(s, &[2.0, 2.0]).is_empty());
        assert_eq!(word_ranges("é b", &[0.0, 1.0]), vec![(0, 2)], "byte ranges");
    }

    #[test]
    fn highlight_draws_words_in_another_fill() {
        let red = |px: &[u8]| px.chunks(4).filter(|c| c[0] > 200 && c[1] < 60 && c[2] < 60 && c[3] > 200).count();
        let base = json!({"id": "t", "type": "text", "text": "ONE TWO THREE", "size": 60, "fill": "#ffffff"});
        let mut hl = base.clone();
        hl["highlight"] = json!({"words": {"expr": "[Math.floor(t), Math.floor(t) + 1]"}, "fill": "#ff0000"});
        let (a, b) = (parse_layer(&base).unwrap(), parse_layer(&hl).unwrap());
        let mut p = painter(&[a.clone(), b.clone()]);
        let plain = p.render(std::slice::from_ref(&a), &json!({}), (640, 120), 1.0, (640.0, 120.0), 0.0).unwrap();
        assert_eq!(red(&plain), 0);
        let w0 = p.render(std::slice::from_ref(&b), &json!({}), (640, 120), 1.0, (640.0, 120.0), 0.0).unwrap();
        let w2 = p.render(std::slice::from_ref(&b), &json!({}), (640, 120), 1.0, (640.0, 120.0), 2.0).unwrap();
        assert!(red(&w0) > 100 && red(&w2) > 100);
        // The red moves right from word 0 to word 2; the layout (the alpha) stays put.
        let red_x = |px: &[u8]| {
            let xs: Vec<usize> = px
                .chunks(4)
                .enumerate()
                .filter(|(_, c)| c[0] > 200 && c[1] < 60 && c[3] > 200)
                .map(|(i, _)| i % 640)
                .collect();
            xs.iter().sum::<usize>() / xs.len().max(1)
        };
        assert!(red_x(&w2) > red_x(&w0) + 150, "{} vs {}", red_x(&w2), red_x(&w0));
        assert_eq!(alpha_bbox(&w0, 640, 120), alpha_bbox(&plain, 640, 120));
        assert_ne!(
            p.state(std::slice::from_ref(&b), &json!({}), 0.0).unwrap(),
            p.state(std::slice::from_ref(&b), &json!({}), 2.0).unwrap()
        );
    }

    #[test]
    fn png_round_trip_header() {
        let px = vec![255u8; 4 * 4 * 4];
        let b = png(&px, 4, 4).unwrap();
        assert_eq!(&b[1..4], b"PNG");
    }
}
