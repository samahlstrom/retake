//! Rendering: graphics runs into cached alpha frames, and units through the compositor into H.264 segments.

use crate::composite::{Affine, Frame, alpha_bbox, crop_px, warp_image, warp_image_with};
use crate::engine::{Canvas, Item, RunSpec, UnitPlan, walk_items};
use crate::expr::Js;
use crate::ffmpeg::{Convert, Decode, EncodeError, Encoder, HwSlot, VideoReader};
use crate::graphics::{self, Painter};
use crate::store::Store;
use crate::transform::{self, Proj};
use anyhow::{Context, Result, anyhow};
use retake_timeline::layer::{Blend, Content, Layer, MatteMode, Resample};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::mpsc;

/// One cached graphics frame: the blob with its pixels and where they go, or nothing (fully transparent).
pub type RunFrame = Option<(String, [u32; 4])>;

/// True when every layer of a run looks the same on every frame of its range, so it's drawn once.
pub fn run_is_static(run: &RunSpec, fps: u32) -> bool {
    let (a, b) = (run.range[0] as f64 / fps as f64, run.range[1] as f64 / fps as f64);
    run.layers.iter().all(|l: &Layer| crate::engine::layer_is_static(l, a, b))
}

/// What rendering a graphics run did: frames in its range, and how many of them were drawn (a static run draws one;
/// an animated run skips frames whose layers are in the same state as the frame before).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct RunStats {
    pub frames: usize,
    pub drawn: usize,
}

fn store_frame(px: &[u8], canvas: &Canvas, store: &Store) -> Result<Value> {
    Ok(match alpha_bbox(px, canvas.w, canvas.h) {
        None => Value::Null,
        Some(r) => {
            let patch = crop_px(px, canvas.w, r);
            let hex = store.put_bytes(&lz4_flex::compress_prepend_size(&patch))?;
            json!([hex, r[0], r[1], r[2], r[3]])
        }
    })
}

/// Renders a graphics run into the store under its layer key. A static run is stored as one image
/// (`{"static": true, "frame": …}`); an animated run as one entry per frame of its range, where a frame whose layers
/// evaluate to the same state as the previous frame's reuses its image without drawing.
pub fn render_run(run: &RunSpec, painter: &mut Painter, canvas: &Canvas, fps: u32, store: &Store) -> Result<RunStats> {
    let n = (run.range[1] - run.range[0]).max(0) as usize;
    let draw = |painter: &mut Painter, t: f64| {
        painter.render(&run.layers, &run.props, (canvas.w, canvas.h), canvas.scale, canvas.comp_f(), t)
    };
    if run.still {
        let px = draw(painter, run.range[0] as f64 / fps as f64)?;
        let frame = store_frame(&px, canvas, store)?;
        store.set_index("layer", &run.key, &json!({"static": true, "frame": frame}))?;
        return Ok(RunStats { frames: n, drawn: 1 });
    }
    let mut frames: Vec<Value> = Vec::with_capacity(n);
    let mut last: Option<(String, Value)> = None;
    let mut drawn = 0;
    // A raster layer (alone in its run): one backend request for the whole range, read a frame at a time.
    let raster = run.layers.iter().find(|l| l.is_raster());
    for k in run.range[0]..run.range[1] {
        let t = k as f64 / fps as f64;
        if let Some(l) = raster {
            painter.raster_frame(l, &run.key, &run.props, canvas, fps, k, run.range[1])?;
        }
        let sig = painter.state(&run.layers, &run.props, t)?;
        if let Some((s, v)) = &last
            && *s == sig
        {
            frames.push(v.clone());
            continue;
        }
        let v = store_frame(&draw(painter, t)?, canvas, store)?;
        drawn += 1;
        frames.push(v.clone());
        last = Some((sig, v));
    }
    store.set_index("layer", &run.key, &json!({"range": run.range, "static": false, "frames": frames}))?;
    Ok(RunStats { frames: n, drawn })
}

/// A cached graphics run: one image for a static run, else one per composition frame from `start`.
pub struct RunData {
    pub start: i64,
    pub frames: Vec<RunFrame>,
    pub still: Option<RunFrame>,
}

impl RunData {
    /// The image at composition frame `k`.
    pub fn at(&self, k: i64) -> RunFrame {
        match &self.still {
            Some(f) => f.clone(),
            None => self.frames.get((k - self.start).max(0) as usize).cloned().flatten(),
        }
    }
}

fn run_frame(f: &Value) -> RunFrame {
    let a = f.as_array()?;
    let r = |i: usize| a.get(i).and_then(Value::as_u64).unwrap_or(0) as u32;
    Some((a.first()?.as_str()?.to_string(), [r(1), r(2), r(3), r(4)]))
}

pub fn load_run(store: &Store, key: &str) -> Result<RunData> {
    let v = store.get_index("layer", key).ok_or_else(|| anyhow!("layer {key} isn't cached"))?;
    if v["static"] == true && v.get("frame").is_some() {
        return Ok(RunData { start: 0, frames: vec![], still: Some(run_frame(&v["frame"])) });
    }
    let start = v["range"][0].as_i64().unwrap_or(0);
    let frames = v["frames"].as_array().map(|a| a.iter().map(run_frame).collect()).unwrap_or_default();
    Ok(RunData { start, frames, still: None })
}

fn even_up(x: f64) -> u32 {
    (((x / 2.0).ceil()) as u32 * 2).max(2)
}

/// How a video item is decoded, and where its buffer sits in source pixels: buffer pixel = (source − `off`) × `k`.
#[derive(Debug, Clone)]
pub struct DecodePlan {
    pub d: Decode,
    pub off: [f64; 2],
    pub k: [f64; 2],
    /// It shows on none of its active frames: nothing is decoded for it.
    pub idle: bool,
}

/// The 2D scale (before perspective) a group gives its children at most over the unit: their footage is decoded
/// that much larger.
fn group_scale(it: &Item, canvas: &Canvas, fps: u32, js: &mut Js) -> Result<f64> {
    let Item::Group { typed, props, k0, active, .. } = it else { return Ok(1.0) };
    let comp = canvas.comp_f();
    let mut k = 0.0f64;
    for i in active[0]..active[1] {
        let t = (k0 + i) as f64 / fps as f64;
        let p = transform::place(typed, comp, comp, t, props, js)?;
        k = k.max(p.k2d.0).max(p.k2d.1);
    }
    Ok(if k > 0.0 { k } else { 1.0 })
}

/// The decode of a video item: its source times the largest 2D scale it's shown at in the unit (source pixels to
/// output pixels, per axis, over its active frames; `gk` for an enclosing group). With `resample: bilinear` never
/// above the render scale, so a picture-in-picture at 30% is decoded at 30% and a full-frame shot as before. With
/// `lanczos` exactly the displayed size, cropped in the pipe when the crop is constant (and the source, not a proxy,
/// is decoded), with ffmpeg's Lanczos scaler.
pub fn decode_plan(it: &Item, canvas: &Canvas, fps: u32, js: &mut Js, gk: f64) -> Result<DecodePlan> {
    let token = DecodePlan { d: Decode::scaled(2, 2), off: [0.0, 0.0], k: [2.0, 2.0], idle: false };
    let Item::Video { typed, src, props, k0, active, .. } = it else { return Ok(token) };
    let Content::Video(v) = &typed.content else { return Ok(token) };
    let (sw, sh) = (src.size[0] as f64, src.size[1] as f64);
    let s = canvas.scale;
    let (cw, ch) = canvas.comp_f();
    let (mut kx, mut ky) = (0.0f64, 0.0f64);
    let mut crops: Vec<[f64; 4]> = vec![];
    let mut shown = false;
    for i in active[0]..active[1] {
        let t = (k0 + i) as f64 / fps as f64;
        if !typed.visible_at(t) {
            continue;
        }
        shown = true;
        let c = transform::crop(typed, sw, sh, t, props, js)?;
        let (fx, fy) = transform::fit_scale(v.fit, c[2], c[3], cw, ch);
        let p = transform::place(typed, (c[2] * fx, c[3] * fy), (cw, ch), t, props, js)?;
        kx = kx.max(p.k2d.0 * fx * s * gk);
        ky = ky.max(p.k2d.1 * fy * s * gk);
        if !crops.contains(&c) {
            crops.push(c);
        }
    }
    if !shown {
        return Ok(DecodePlan { idle: true, ..token }); // never on screen: no decode at all
    }
    if kx <= 0.0 || ky <= 0.0 {
        return Ok(token); // shown at no size: decode a token frame
    }
    let delogo: Vec<[u32; 4]> = match (&v.delogo, &src.proxy) {
        (Some(b), None) => b
            .list()
            .iter()
            .filter_map(|b| {
                // delogo needs a band of a pixel around the box inside the frame.
                let x = b[0].round().max(1.0);
                let y = b[1].round().max(1.0);
                let w = (b[0] + b[2]).round().min(sw - 1.0) - x;
                let h = (b[1] + b[3]).round().min(sh - 1.0) - y;
                (w >= 1.0 && h >= 1.0).then_some([x as u32, y as u32, w as u32, h as u32])
            })
            .collect(),
        _ => vec![],
    };
    let limit = |x: f64, frame: u32| (x.round() as u32).clamp(2, frame * 4);
    if v.resample == Resample::Lanczos {
        let whole = |c: &[f64; 4]| c.iter().all(|x| x.fract() == 0.0 && (*x as i64) % 2 == 0);
        if let [c] = crops.as_slice()
            && src.proxy.is_none()
            && whole(c)
            && c[2] > 0.0
            && c[3] > 0.0
        {
            let (w, h) = (limit(c[2] * kx, canvas.w), limit(c[3] * ky, canvas.h));
            let crop = (*c != [0.0, 0.0, sw, sh]).then(|| c.map(|x| x as u32));
            return Ok(DecodePlan {
                d: Decode { w, h, crop, lanczos: true, delogo, zimg: None },
                off: [c[0], c[1]],
                k: [w as f64 / c[2], h as f64 / c[3]],
                idle: false,
            });
        }
        let (w, h) = (limit(sw * kx, canvas.w), limit(sh * ky, canvas.h));
        return Ok(DecodePlan {
            d: Decode { w, h, crop: None, lanczos: true, delogo, zimg: None },
            off: [0.0, 0.0],
            k: [w as f64 / sw, h as f64 / sh],
            idle: false,
        });
    }
    let full = (((sw * s / 2.0).round() as u32 * 2).max(2), ((sh * s / 2.0).round() as u32 * 2).max(2));
    let unfiltered = v.resample == Resample::Unfiltered;
    let side =
        |k: f64, len: f64, full: u32| if unfiltered || k >= s - 1e-9 { full } else { even_up(len * k).min(full) };
    let (w, h) = (side(kx, sw, full.0), side(ky, sh, full.1));
    Ok(DecodePlan {
        d: Decode { w, h, crop: None, lanczos: false, delogo, zimg: None },
        off: [0.0, 0.0],
        k: [w as f64 / sw, h as f64 / sh],
        idle: false,
    })
}

/// The size a video item is decoded at (see [`decode_plan`]).
pub fn decode_size(it: &Item, canvas: &Canvas, fps: u32, js: &mut Js) -> Result<(u32, u32)> {
    let p = decode_plan(it, canvas, fps, js, 1.0)?;
    Ok((p.d.w, p.d.h))
}

/// Where graphics frames come from: the cache (rendering units) or a painter (stills).
pub enum Graphics<'a> {
    Cache(&'a Store),
    Live(&'a mut Painter, &'a [RunSpec]),
}

/// Graphics pixels cropped to their bounds, and where they go: [x, y, w, h] in output pixels.
type Patch = (Arc<Vec<u8>>, [u32; 4]);

/// A video item's decode: its plan (sized like its feed's) and the feed it reads, if it shows at all.
struct VState {
    plan: DecodePlan,
    feed: Option<usize>,
}

/// What makes two video items read the same frames: the same file, decoded the same way (crop, delogo, scaler,
/// colour; a Lanczos decode's exact size), with the same timing (`in`, clock, loop, hold) over the same active frames.
/// Bilinear decodes of one file share whatever their sizes: the feed decodes at the largest.
#[derive(Debug, Clone, PartialEq)]
struct FeedKey {
    path: PathBuf,
    lanczos: bool,
    crop: Option<[u32; 4]>,
    delogo: Vec<[u32; 4]>,
    zimg: Option<crate::ffmpeg::SrcColor>,
    /// A Lanczos decode is exactly its displayed size, so only equal sizes share.
    size: Option<(u32, u32)>,
    in_: u64,
    t_out0: Option<u64>,
    k0: i64,
    looping: bool,
    duration: u64,
    hold_from: Option<u64>,
    active: [i64; 2],
}

/// One ffmpeg decode, shared by every item that reads the same frames of the same file.
struct Feed {
    key: FeedKey,
    d: Decode,
    in_: f64,
    t_out0: Option<f64>,
    hold_from: Option<f64>,
    duration: f64,
    reader: Option<VideoReader>,
    /// A frame has been read (the first advance always reads one, even on hold).
    fed: bool,
}

impl Feed {
    /// Source seconds at unit frame `i`, as each of its items computes them.
    fn src_t(&self, i: i64, fps: f64) -> f64 {
        let mut t = match self.t_out0 {
            Some(t) => self.in_ + t + i as f64 / fps,
            None => self.in_ + (self.key.k0 + i) as f64 / fps,
        };
        if self.key.looping && self.duration > 0.0 {
            t = t.rem_euclid(self.duration);
        }
        t
    }

    /// Where the decode starts when its first frame is unit frame `i`.
    fn start(&self, i: i64, fps: f64) -> f64 {
        let t = self.src_t(i, fps);
        match self.hold_from {
            Some(hf) if t > hf => hf,
            _ => t,
        }
    }
}

/// Frames before its first active frame that a threaded feed opens (its ffmpeg starts and seeks meanwhile).
const PREFETCH: i64 = 15;

/// How a unit's decoders are read: in the compositor's own thread when it needs a frame (stills, previews), or by a
/// thread per decoder that reads ahead (renders). The frames are the same.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Feeds {
    Inline,
    Threaded,
}

/// A track matte's value (0–255) at a premultiplied pixel.
fn matte_value(mode: MatteMode, p: &[u8]) -> u32 {
    let luma = || (2126 * p[0] as u32 + 7152 * p[1] as u32 + 722 * p[2] as u32 + 5000) / 10000;
    match mode {
        MatteMode::Alpha => p[3] as u32,
        MatteMode::AlphaInverted => 255 - p[3] as u32,
        MatteMode::Luma => luma().min(255),
        MatteMode::LumaInverted => 255 - luma().min(255),
    }
}

/// The compositor's state over a unit's frames.
struct Ctx<'a, 'g> {
    canvas: Canvas,
    fps: f64,
    gfx: Graphics<'g>,
    runs: HashMap<String, RunData>,
    patches: HashMap<String, Arc<Vec<u8>>>,
    live_still: HashMap<String, Option<Patch>>,
    vids: HashMap<usize, VState>,
    feeds: Vec<Feed>,
    threaded: bool,
    /// Each slot's box this frame (output pixels).
    cur: Vec<Option<[f64; 4]>>,
    /// The unit frame composing stops at (stills ask a backend for frames up to it).
    to: i64,
    _items: std::marker::PhantomData<&'a ()>,
}

impl Ctx<'_, '_> {
    fn wh(&self) -> (u32, u32) {
        (self.canvas.w, self.canvas.h)
    }

    /// The graphics patch of a run item at unit frame `i`.
    fn patch(&mut self, key: &str, k0: i64, i: i64) -> Result<Option<Patch>> {
        let k = k0 + i;
        let (w, h) = self.wh();
        let s = self.canvas.scale;
        let comp = self.canvas.comp_f();
        Ok(match &mut self.gfx {
            Graphics::Cache(store) => match self.runs.get(key).context("run")?.at(k) {
                Some((hex, r)) => {
                    let p = match self.patches.get(&hex) {
                        Some(p) => p.clone(),
                        None => {
                            let raw = lz4_flex::decompress_size_prepended(&store.get_blob(&hex)?)
                                .map_err(|e| anyhow!("layer frame {hex}: {e}"))?;
                            let p = Arc::new(raw);
                            if self.patches.len() > 64 {
                                self.patches.clear();
                            }
                            self.patches.insert(hex.clone(), p.clone());
                            p
                        }
                    };
                    Some((p, r))
                }
                None => None,
            },
            Graphics::Live(painter, specs) => {
                let run = specs.iter().find(|r| r.key == key).context("run spec")?;
                match self.live_still.get(key) {
                    Some(p) => p.clone(),
                    None => {
                        let t = if run.still { run.range[0] } else { k } as f64 / self.fps;
                        if let Some(l) = run.layers.iter().find(|l| l.is_raster()) {
                            // Frames to the end of what's composed: sequential stills share a request.
                            let end = (k0 + self.to).min(run.range[1]);
                            painter.raster_frame(l, key, &run.props, &self.canvas, self.fps as u32, k, end)?;
                        }
                        let px = painter.render(&run.layers, &run.props, (w, h), s, comp, t)?;
                        let p = alpha_bbox(&px, w, h).map(|r| (Arc::new(crop_px(&px, w, r)), r));
                        if run.still {
                            self.live_still.insert(key.to_string(), p.clone());
                        }
                        p
                    }
                }
            }
        })
    }

    /// Advances every decode active at unit frame `i` by one frame (or holds it), whether or not anything on it draws.
    /// Decoders due now start first and are read after, so they start together; threaded ones start a little early.
    fn advance(&mut self, i: i64) -> Result<()> {
        let fps = self.fps;
        for f in &mut self.feeds {
            let [a0, a1] = f.key.active;
            if f.reader.is_some() || i >= a1 {
                continue;
            }
            let at = if i >= a0 {
                i
            } else if self.threaded && a0 - i <= PREFETCH && a0 < self.to {
                a0
            } else {
                continue;
            };
            let r =
                VideoReader::open_with(&f.key.path, f.start(at, fps), fps as u32, &f.d, f.key.looping, self.threaded)?;
            f.reader = Some(r);
        }
        for f in &mut self.feeds {
            let [a0, a1] = f.key.active;
            if i < a0 || i >= a1 {
                continue;
            }
            let held = f.hold_from.is_some_and(|hf| f.src_t(i, fps) >= hf);
            if !f.fed || !held {
                f.reader.as_mut().context("video reader")?.next_frame()?;
                f.fed = true;
            }
        }
        Ok(())
    }

    /// Draws `items` at unit frame `i` onto `frame`. `to_out` maps this frame's pixels to the output's (a group's
    /// picture is transformed afterwards), for the boxes.
    fn draw_items(&mut self, frame: &mut Frame, items: &[Item], i: i64, js: &mut Js, to_out: &Proj) -> Result<()> {
        for it in items {
            let [a0, a1] = it.active();
            if i < a0 || i >= a1 {
                continue;
            }
            if let Item::Fill { color, slot, .. } = it {
                frame.fill_over(*color);
                self.cur[*slot] = Some([0.0, 0.0, frame.w as f64, frame.h as f64]);
                continue;
            }
            self.draw_item(frame, it, i, js, to_out)?;
        }
        Ok(())
    }

    /// Draws one item with its blend mode and track matte.
    fn draw_item(&mut self, target: &mut Frame, it: &Item, i: i64, js: &mut Js, to_out: &Proj) -> Result<()> {
        let fx = it.fx().expect("drawable items have effects");
        let (w, h) = self.wh();
        if fx.matte.is_none() {
            match it {
                Item::Graphics { .. } => return self.draw_plain(target, it, i, js, to_out, fx.blend),
                _ if fx.blend == Blend::Normal => return self.draw_plain(target, it, i, js, to_out, Blend::Normal),
                _ => {}
            }
        }
        let mut layer = Frame::new(w, h);
        self.draw_plain(&mut layer, it, i, js, to_out, Blend::Normal)?;
        if let (Some(m), Some(mode)) = (fx.matte.as_deref(), fx.matte_mode) {
            let mut mf = Frame::new(w, h);
            let [a0, a1] = m.active();
            if i >= a0 && i < a1 {
                self.draw_plain(&mut mf, m, i, js, to_out, Blend::Normal)?;
            }
            for (d, s) in layer.px.chunks_mut(4).zip(mf.px.chunks(4)) {
                if d[3] == 0 {
                    continue;
                }
                let v = matte_value(mode, s);
                if v < 255 {
                    for c in d.iter_mut() {
                        *c = ((*c as u32 * v + 127) / 255) as u8;
                    }
                }
            }
        }
        if let Some(b) = layer.bbox() {
            target.blend_frame(&layer, b, fx.blend);
        }
        Ok(())
    }

    /// Draws an item onto `target` in `blend` (graphics patches only; anything else is drawn "over"), ignoring its
    /// matte, and records its box.
    fn draw_plain(
        &mut self,
        target: &mut Frame,
        it: &Item,
        i: i64,
        js: &mut Js,
        to_out: &Proj,
        blend: Blend,
    ) -> Result<()> {
        let b = match it {
            Item::Fill { color, .. } => {
                target.fill_over(*color);
                Some([0.0, 0.0, target.w as f64, target.h as f64])
            }
            Item::Graphics { key, k0, .. } => match self.patch(key, *k0, i)? {
                Some((p, r)) => {
                    target.blend_patch(&p, r[0] as i32, r[1] as i32, r[2], r[3], blend);
                    Some([r[0] as f64, r[1] as f64, r[2] as f64, r[3] as f64])
                }
                None => None,
            },
            Item::Video { .. } => self.draw_video(target, it, i, js)?,
            Item::Group { .. } => self.draw_group(target, it, i, js, to_out)?,
        };
        let (w, h) = self.wh();
        self.cur[it.slot()] = b.and_then(|b| to_out.bbox(b, w as f64, h as f64));
        Ok(())
    }

    /// Masks, outlines and filters a layer's own picture (footage or a group) in place.
    #[allow(clippy::too_many_arguments)]
    fn layer_effects(
        &self,
        tmp: &mut Frame,
        l: &Layer,
        content: (f64, f64),
        m: &Proj,
        opacity: f64,
        t: f64,
        props: &Value,
        js: &mut Js,
    ) -> Result<()> {
        let (w, h) = self.wh();
        let s = self.canvas.scale;
        let to_out = Proj::from_affine(&Affine::scale(s, s)).then(m);
        if let Some(mask) = &l.mask {
            let cov = graphics::mask_coverage(mask, content, &to_out, t, props, js, (w, h))?;
            tmp.apply_coverage(&cov);
            if let Some(mut o) = graphics::mask_outline(mask, content, &to_out, t, props, js, (w, h))? {
                if opacity < 1.0 {
                    for v in &mut o.px {
                        *v = (*v as f64 * opacity).round() as u8;
                    }
                }
                if let Some(b) = o.bbox() {
                    tmp.blend_frame(&o, b, Blend::Normal);
                }
            }
        }
        let blur = match &l.blur {
            Some(p) => p.eval(t, props, js)?.first().copied().unwrap_or(0.0).max(0.0),
            None => 0.0,
        };
        if blur > 0.0 || l.shadow.is_some() {
            *tmp = graphics::filter_frame(tmp, blur, l.shadow.as_ref(), s)?;
        }
        Ok(())
    }

    fn needs_layer(l: &Layer) -> bool {
        l.mask.is_some() || l.shadow.is_some() || l.blur.is_some()
    }

    /// Draws a video item through its (affine or projective) transform. Returns its box.
    fn draw_video(&mut self, target: &mut Frame, it: &Item, i: i64, js: &mut Js) -> Result<Option<[f64; 4]>> {
        let Item::Video { typed, src, props, k0, slot, .. } = it else { return Ok(None) };
        let Content::Video(v) = &typed.content else { return Ok(None) };
        let t = (k0 + i) as f64 / self.fps;
        if !typed.visible_at(t) {
            return Ok(None);
        }
        let (w, h) = self.wh();
        let (cw, ch) = self.canvas.comp_f();
        let s = self.canvas.scale;
        let (sw, sh) = (src.size[0] as f64, src.size[1] as f64);
        let c = transform::crop(typed, sw, sh, t, props, js)?;
        let (fx, fy) = transform::fit_scale(v.fit, c[2], c[3], cw, ch);
        let p = transform::place(typed, (c[2] * fx, c[3] * fy), (cw, ch), t, props, js)?;
        let src_to_out = Proj::from_affine(&Affine::scale(s, s))
            .then(&p.m)
            .then_affine(&Affine::scale(fx, fy).then(&Affine::translate(-c[0], -c[1])));
        let vs = self.vids.get(slot).expect("video state");
        let Some(feed) = vs.feed else { return Ok(None) }; // never shows (see `DecodePlan::idle`)
        let (off, k) = (vs.plan.off, vs.plan.k);
        let (bw, bh) = (vs.plan.d.w, vs.plan.d.h);
        let buf_to_out =
            src_to_out.then_affine(&Affine::translate(off[0], off[1]).then(&Affine::scale(1.0 / k[0], 1.0 / k[1])));
        let bcrop = [(c[0] - off[0]) * k[0], (c[1] - off[1]) * k[1], c[2] * k[0], c[3] * k[1]];
        let buf = self.feeds[feed].reader.as_ref().map(|r| r.current()).context("video reader")?;
        let mips = v.resample != Resample::Unfiltered;
        if Ctx::needs_layer(typed) {
            let mut tmp = Frame::new(w, h);
            warp_image_with(&mut tmp, buf, bw, bh, bcrop, &buf_to_out, p.opacity, mips);
            self.layer_effects(&mut tmp, typed, (c[2] * fx, c[3] * fy), &p.m, p.opacity, t, props, js)?;
            if let Some(b) = tmp.bbox() {
                target.blend_frame(&tmp, b, Blend::Normal);
            }
        } else {
            warp_image_with(target, buf, bw, bh, bcrop, &buf_to_out, p.opacity, mips);
        }
        Ok(if p.opacity > 0.0 { src_to_out.bbox(c, w as f64, h as f64) } else { None })
    }

    /// Draws a group with footage: its items into the group's own picture, then that picture through the group's
    /// transform, mask, filters and opacity. Returns its box.
    fn draw_group(
        &mut self,
        target: &mut Frame,
        it: &Item,
        i: i64,
        js: &mut Js,
        to_out: &Proj,
    ) -> Result<Option<[f64; 4]>> {
        let Item::Group { typed, props, k0, items, .. } = it else { return Ok(None) };
        let t = (k0 + i) as f64 / self.fps;
        if !typed.visible_at(t) {
            return Ok(None);
        }
        let (w, h) = self.wh();
        let comp = self.canvas.comp_f();
        let s = self.canvas.scale;
        let p = transform::place(typed, comp, comp, t, props, js)?;
        // The group's picture is in output pixels of the composition: buffer → comp (÷ s) → transform → output (× s).
        let g = Proj::from_affine(&Affine::scale(s, s)).then(&p.m).then_affine(&Affine::scale(1.0 / s, 1.0 / s));
        let mut child = Frame::new(w, h);
        let inner = to_out.then(&g);
        self.draw_items(&mut child, items, i, js, &inner)?;
        let identity = g.affine().is_some_and(|a| {
            let near = |x: f64, y: f64| (x - y).abs() < 1e-9;
            near(a.a, 1.0) && near(a.d, 1.0) && near(a.b, 0.0) && near(a.c, 0.0) && near(a.e, 0.0) && near(a.f, 0.0)
        });
        if identity && p.opacity >= 1.0 && !Ctx::needs_layer(typed) {
            let b = child.bbox();
            if let Some(b) = b {
                target.blend_frame(&child, b, Blend::Normal);
            }
            return Ok(b.map(|b| [b[0] as f64, b[1] as f64, b[2] as f64, b[3] as f64]));
        }
        let mut tmp = Frame::new(w, h);
        warp_image(&mut tmp, &child.px, w, h, [0.0, 0.0, w as f64, h as f64], &g, p.opacity);
        self.layer_effects(&mut tmp, typed, comp, &p.m, p.opacity, t, props, js)?;
        let b = tmp.bbox();
        if let Some(b) = b {
            target.blend_frame(&tmp, b, Blend::Normal);
        }
        Ok(b.map(|b| [b[0] as f64, b[1] as f64, b[2] as f64, b[3] as f64]))
    }
}

/// Composites frames [from, to) of a unit and hands each to `sink`. Returns each item slot's box track: change points
/// `[i, x, y, w, h]` in output pixels (w = h = 0 when not drawn).
pub fn compose(
    plan: &UnitPlan,
    from: i64,
    to: i64,
    gfx: Graphics,
    js: &mut Js,
    mut sink: impl FnMut(&Frame) -> Result<()>,
) -> Result<Vec<Vec<[f64; 5]>>> {
    let (w, h) = (plan.desc.canvas.w, plan.desc.canvas.h);
    compose_with(plan, from, to, gfx, js, Feeds::Inline, || Frame::new(w, h), |f| sink(&f))
}

/// The decode plan of every video item (footage in a group is decoded at the group's scale too), and the feeds they
/// share: items that read the same frames of one file get one decoder, bilinear ones at the largest size any needs.
fn feeds(d: &crate::engine::UnitDesc, js: &mut Js) -> Result<(HashMap<usize, VState>, Vec<Feed>)> {
    fn plans<'a>(
        items: &'a [Item],
        gk: f64,
        canvas: &Canvas,
        fps: u32,
        js: &mut Js,
        out: &mut Vec<(&'a Item, DecodePlan)>,
    ) -> Result<()> {
        for it in items {
            match it {
                Item::Video { .. } => out.push((it, decode_plan(it, canvas, fps, js, gk)?)),
                Item::Group { items: inner, .. } => {
                    let k = gk * group_scale(it, canvas, fps, js)?;
                    plans(inner, k, canvas, fps, js, out)?;
                }
                _ => {}
            }
            if let Some(m) = it.fx().and_then(|f| f.matte.as_deref()) {
                plans(std::slice::from_ref(m), gk, canvas, fps, js, out)?;
            }
        }
        Ok(())
    }
    let mut all = vec![];
    plans(&d.items, 1.0, &d.canvas, d.fps, js, &mut all)?;
    let mut vids: HashMap<usize, VState> = HashMap::new();
    let mut feeds: Vec<Feed> = vec![];
    for (it, mut plan) in all {
        let Item::Video { typed, src, k0, active, t_out0, slot, .. } = it else { continue };
        let Content::Video(v) = &typed.content else { continue };
        if plan.idle {
            vids.insert(*slot, VState { plan, feed: None });
            continue;
        }
        if d.convert == Convert::Zimg && src.color.yuv {
            plan.d.zimg = Some(src.color.clone());
        }
        let bits = |x: f64| x.to_bits();
        let key = FeedKey {
            path: src.path.clone(),
            lanczos: plan.d.lanczos,
            crop: plan.d.crop,
            delogo: plan.d.delogo.clone(),
            zimg: plan.d.zimg.clone(),
            size: plan.d.lanczos.then_some((plan.d.w, plan.d.h)),
            in_: bits(v.in_),
            t_out0: t_out0.map(bits),
            k0: if t_out0.is_some() { 0 } else { *k0 },
            looping: v.loop_,
            duration: bits(src.duration),
            hold_from: v.hold_from.map(bits),
            active: *active,
        };
        let fi = match feeds.iter().position(|f| f.key == key) {
            Some(fi) => {
                let f = &mut feeds[fi];
                f.d.w = f.d.w.max(plan.d.w);
                f.d.h = f.d.h.max(plan.d.h);
                fi
            }
            None => {
                feeds.push(Feed {
                    key,
                    d: plan.d.clone(),
                    in_: v.in_,
                    t_out0: *t_out0,
                    hold_from: v.hold_from,
                    duration: src.duration,
                    reader: None,
                    fed: false,
                });
                feeds.len() - 1
            }
        };
        vids.insert(*slot, VState { plan, feed: Some(fi) });
    }
    // Every item reads its feed's buffer: a bilinear one may be larger than it asked for.
    for (slot, vs) in vids.iter_mut() {
        let Some(fi) = vs.feed else { continue };
        let fd = &feeds[fi].d;
        if fd.w != vs.plan.d.w || fd.h != vs.plan.d.h {
            let Some(Item::Video { src, .. }) = walk_items(&d.items).into_iter().find(|it| it.slot() == *slot) else {
                continue;
            };
            vs.plan.k = [fd.w as f64 / src.size[0] as f64, fd.h as f64 / src.size[1] as f64];
            vs.plan.d = fd.clone();
        }
    }
    Ok((vids, feeds))
}

/// [`compose`] with its frames from `frame` (each `w` × `h`, all zero: renders hand buffers round instead of
/// allocating one per frame) and handed over to `sink`, and its decoders read as `mode` says.
#[allow(clippy::too_many_arguments)]
pub fn compose_with(
    plan: &UnitPlan,
    from: i64,
    to: i64,
    gfx: Graphics,
    js: &mut Js,
    mode: Feeds,
    mut frame: impl FnMut() -> Frame,
    mut sink: impl FnMut(Frame) -> Result<()>,
) -> Result<Vec<Vec<[f64; 5]>>> {
    let d = &plan.desc;
    let all = walk_items(&d.items);
    let slots = all.iter().map(|it| it.slot() + 1).max().unwrap_or(0).max(plan.owners.len());
    let mut runs: HashMap<String, RunData> = HashMap::new();
    if let Graphics::Cache(store) = &gfx {
        for it in &all {
            if let Item::Graphics { key, .. } = it
                && !runs.contains_key(key)
            {
                runs.insert(key.clone(), load_run(store, key)?);
            }
        }
    }
    let (vids, feeds) = feeds(d, js)?;
    let mut ctx = Ctx {
        canvas: d.canvas,
        fps: d.fps as f64,
        gfx,
        runs,
        patches: HashMap::new(),
        live_still: HashMap::new(),
        vids,
        feeds,
        threaded: mode == Feeds::Threaded,
        cur: vec![None; slots],
        to,
        _items: std::marker::PhantomData,
    };
    let mut tracks: Vec<Vec<[f64; 5]>> = vec![vec![]; slots];
    let push = |tr: &mut Vec<[f64; 5]>, i: i64, b: Option<[f64; 4]>| {
        let b = b.unwrap_or([0.0; 4]);
        let e = [i as f64, b[0], b[1], b[2], b[3]];
        if tr.last().is_none_or(|l| l[1..] != e[1..]) {
            tr.push(e);
        }
    };
    for i in from..to {
        ctx.advance(i)?;
        ctx.cur.iter_mut().for_each(|c| *c = None);
        let mut f = frame();
        ctx.draw_items(&mut f, &d.items, i, js, &Proj::IDENTITY)?;
        for (slot, tr) in tracks.iter_mut().enumerate() {
            push(tr, i, ctx.cur[slot]);
        }
        sink(f)?;
    }
    Ok(tracks)
}

/// How many decoders a unit runs, and how many video items read them (the difference is what sharing saved).
pub fn decoder_count(plan: &UnitPlan, js: &mut Js) -> Result<(usize, usize)> {
    let (vids, feeds) = feeds(&plan.desc, js)?;
    Ok((feeds.len(), vids.len()))
}

/// Renders a unit into a segment in the store and records it under the unit key, pipelined: each decoder on a
/// thread of its own, the compositor on this one, and the encoder fed from another, with frame buffers handed round.
/// A hardware encoder that fails is tried twice more (its sessions may all be taken for a moment).
pub fn render_unit(plan: &UnitPlan, store: &Store, js: &mut Js) -> Result<Value> {
    let mut tries = 0;
    loop {
        match render_unit_with(plan, store, js, true) {
            Err(e) if plan.desc.encode.encoder.is_hw() && e.downcast_ref::<EncodeError>().is_some() && tries < 2 => {
                tries += 1;
                std::thread::sleep(std::time::Duration::from_millis(1500 * tries));
            }
            r => return r,
        }
    }
}

/// [`render_unit`], pipelined or in lockstep (one thread reads, composites and writes in turn; the same bytes).
pub fn render_unit_with(plan: &UnitPlan, store: &Store, js: &mut Js, pipelined: bool) -> Result<Value> {
    let d = &plan.desc;
    let _slot = d.encode.encoder.is_hw().then(HwSlot::take);
    let tmp = store.tmp_path("mp4");
    let (w, h) = (d.canvas.w, d.canvas.h);
    let mut enc = Encoder::open(&tmp, w, h, d.fps, d.frames, &d.encode)?;
    let tracks = if pipelined {
        let (tx, rx) = mpsc::sync_channel::<Frame>(2);
        let (back, spare) = mpsc::channel::<Frame>();
        std::thread::scope(|sc| -> Result<_> {
            let writer = std::thread::Builder::new()
                .name("retake-encode".into())
                .spawn_scoped(sc, move || -> Result<Encoder> {
                    for mut f in rx {
                        enc.write(&f.px)?;
                        f.px.fill(0);
                        let _ = back.send(f);
                    }
                    Ok(enc)
                })
                .context("starting the encoder thread")?;
            let composed = compose_with(
                plan,
                0,
                d.frames,
                Graphics::Cache(store),
                js,
                Feeds::Threaded,
                || spare.try_recv().unwrap_or_else(|_| Frame::new(w, h)),
                // Owns the sender, so the queue closes when composing ends, however it ends.
                move |f| tx.send(f).map_err(|_| anyhow!("the encoder stopped")),
            );
            // The sink (and its sender) is gone: the writer drains the queue and hands the encoder back.
            let written = writer.join().map_err(|_| anyhow!("the encoder thread panicked"))?;
            match (composed, written) {
                (_, Err(e)) => Err(e),
                (Err(e), Ok(_)) => Err(e),
                (Ok(t), Ok(enc)) => {
                    enc.finish()?;
                    Ok(t)
                }
            }
        })?
    } else {
        let t = compose(plan, 0, d.frames, Graphics::Cache(store), js, |f| enc.write(&f.px))?;
        enc.finish()?;
        t
    };
    let bytes = std::fs::metadata(&tmp)?.len();
    let blob = store.put_file(&tmp)?;
    let idx = json!({"blob": blob, "frames": d.frames, "bytes": bytes, "boxes": tracks});
    store.set_index("unit", &plan.key, &idx)?;
    Ok(idx)
}

/// Renders one still (frame `i` of the unit), with graphics drawn live.
pub fn render_still(plan: &UnitPlan, i: i64, painter: &mut Painter, out: &Path) -> Result<()> {
    let mut js = Js::new()?;
    let mut png = None;
    let d = &plan.desc;
    compose(plan, i, i + 1, Graphics::Live(painter, &plan.runs), &mut js, |f| {
        png = Some(crate::graphics::png(&f.px, d.canvas.w, d.canvas.h)?);
        Ok(())
    })?;
    std::fs::write(out, png.context("no frame")?).with_context(|| format!("writing {}", out.display()))
}
