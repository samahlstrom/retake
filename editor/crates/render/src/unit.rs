//! Rendering: graphics runs into cached alpha frames, and units through the compositor into H.264 segments.

use crate::composite::{Affine, Frame, alpha_bbox, crop_px, draw_image};
use crate::engine::{Canvas, Item, RunSpec, UnitPlan};
use crate::expr::Js;
use crate::ffmpeg::{Encoder, VideoReader};
use crate::graphics::Painter;
use crate::store::Store;
use crate::transform;
use anyhow::{Context, Result, anyhow};
use retake_timeline::layer::{Content, Layer};
use serde_json::{Value, json};
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;

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
    for k in run.range[0]..run.range[1] {
        let t = k as f64 / fps as f64;
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

/// The size a video item is decoded at: its source size times the largest scale it's shown at in the unit (source
/// pixels to output pixels, per axis, over its active frames), never above the render scale. A picture-in-picture at
/// 30% is decoded at 30% instead of full size (ffmpeg scales in the pipe); a full-frame shot decodes as before.
pub fn decode_size(it: &Item, canvas: &Canvas, fps: u32, js: &mut Js) -> Result<(u32, u32)> {
    let Item::Video { typed, src, props, k0, active, .. } = it else { return Ok((2, 2)) };
    let Content::Video(v) = &typed.content else { return Ok((2, 2)) };
    let (sw, sh) = (src.size[0] as f64, src.size[1] as f64);
    let s = canvas.scale;
    let full = (((sw * s / 2.0).round() as u32 * 2).max(2), ((sh * s / 2.0).round() as u32 * 2).max(2));
    let (cw, ch) = canvas.comp_f();
    let (mut kx, mut ky) = (0.0f64, 0.0f64);
    for i in active[0]..active[1] {
        let t = (k0 + i) as f64 / fps as f64;
        if !typed.visible_at(t) {
            continue;
        }
        let c = transform::crop(typed, sw, sh, t, props, js)?;
        let (fx, fy) = transform::fit_scale(v.fit, c[2], c[3], cw, ch);
        let p = transform::place(typed, (c[2] * fx, c[3] * fy), (cw, ch), t, props, js)?;
        let m = Affine::scale(s, s).then(&p.m).then(&Affine::scale(fx, fy));
        kx = kx.max(m.a.hypot(m.b));
        ky = ky.max(m.c.hypot(m.d));
    }
    if kx <= 0.0 || ky <= 0.0 {
        return Ok((2, 2)); // never on screen: decode a token frame
    }
    let side = |k: f64, len: f64, full: u32| if k >= s - 1e-9 { full } else { even_up(len * k).min(full) };
    Ok((side(kx, sw, full.0), side(ky, sh, full.1)))
}

/// Where graphics frames come from: the cache (rendering units) or a painter (stills).
pub enum Graphics<'a> {
    Cache(&'a Store),
    Live(&'a mut Painter, &'a [RunSpec]),
}

/// Graphics pixels cropped to their bounds, and where they go: [x, y, w, h] in output pixels.
type Patch = (Arc<Vec<u8>>, [u32; 4]);

struct VState {
    reader: Option<VideoReader>,
    bw: u32,
    bh: u32,
}

/// Composites frames [from, to) of a unit and hands each to `sink`. Returns each item's box track: change points
/// `[i, x, y, w, h]` in output pixels (w = h = 0 when not drawn).
pub fn compose(
    plan: &UnitPlan,
    from: i64,
    to: i64,
    mut gfx: Graphics,
    js: &mut Js,
    mut sink: impl FnMut(&Frame) -> Result<()>,
) -> Result<Vec<Vec<[f64; 5]>>> {
    let d = &plan.desc;
    let (w, h) = (d.canvas.w, d.canvas.h);
    let (cw, ch) = d.canvas.comp_f();
    let s = d.canvas.scale;
    let fps = d.fps as f64;
    let mut runs: HashMap<String, RunData> = HashMap::new();
    if let Graphics::Cache(store) = &gfx {
        for it in &d.items {
            if let Item::Graphics { key, .. } = it
                && !runs.contains_key(key)
            {
                runs.insert(key.clone(), load_run(store, key)?);
            }
        }
    }
    // Live stills of a static run: drawn once.
    let mut live_still: HashMap<String, Option<Patch>> = HashMap::new();
    let mut patches: HashMap<String, Arc<Vec<u8>>> = HashMap::new();
    let mut vids: Vec<Option<VState>> = Vec::with_capacity(d.items.len());
    for it in &d.items {
        vids.push(match it {
            Item::Video { .. } => {
                let (bw, bh) = decode_size(it, &d.canvas, d.fps, js)?;
                Some(VState { reader: None, bw, bh })
            }
            _ => None,
        });
    }
    let mut tracks: Vec<Vec<[f64; 5]>> = vec![vec![]; d.items.len()];
    let push = |tr: &mut Vec<[f64; 5]>, i: i64, b: Option<[f64; 4]>| {
        let b = b.unwrap_or([0.0; 4]);
        let e = [i as f64, b[0], b[1], b[2], b[3]];
        if tr.last().is_none_or(|l| l[1..] != e[1..]) {
            tr.push(e);
        }
    };
    for i in from..to {
        let mut frame = Frame::new(w, h);
        for (idx, it) in d.items.iter().enumerate() {
            let [a0, a1] = it.active();
            if i < a0 || i >= a1 {
                push(&mut tracks[idx], i, None);
                continue;
            }
            match it {
                Item::Fill { color, .. } => {
                    frame.fill_over(*color);
                    push(&mut tracks[idx], i, Some([0.0, 0.0, w as f64, h as f64]));
                }
                Item::Graphics { key, k0, .. } => {
                    let k = k0 + i;
                    let placed: Option<Patch> = match &mut gfx {
                        Graphics::Cache(store) => match runs.get(key).context("run")?.at(k) {
                            Some((hex, r)) => {
                                let p = match patches.get(&hex) {
                                    Some(p) => p.clone(),
                                    None => {
                                        let raw = lz4_flex::decompress_size_prepended(&store.get_blob(&hex)?)
                                            .map_err(|e| anyhow!("layer frame {hex}: {e}"))?;
                                        let p = Arc::new(raw);
                                        if patches.len() > 64 {
                                            patches.clear();
                                        }
                                        patches.insert(hex.clone(), p.clone());
                                        p
                                    }
                                };
                                Some((p, r))
                            }
                            None => None,
                        },
                        Graphics::Live(painter, specs) => {
                            let run = specs.iter().find(|r| &r.key == key).context("run spec")?;
                            match live_still.get(key) {
                                Some(p) => p.clone(),
                                None => {
                                    let t = if run.still { run.range[0] } else { k } as f64 / fps;
                                    let px = painter.render(&run.layers, &run.props, (w, h), s, (cw, ch), t)?;
                                    let p = alpha_bbox(&px, w, h).map(|r| (Arc::new(crop_px(&px, w, r)), r));
                                    if run.still {
                                        live_still.insert(key.clone(), p.clone());
                                    }
                                    p
                                }
                            }
                        }
                    };
                    match placed {
                        Some((p, r)) => {
                            frame.over_patch(&p, r[0] as i32, r[1] as i32, r[2], r[3]);
                            push(&mut tracks[idx], i, Some([r[0] as f64, r[1] as f64, r[2] as f64, r[3] as f64]));
                        }
                        None => push(&mut tracks[idx], i, None),
                    }
                }
                Item::Video { typed, src, props, k0, t_out0, .. } => {
                    let Content::Video(v) = &typed.content else { continue };
                    let vs = vids[idx].as_mut().expect("video state");
                    let mut src_t = match t_out0 {
                        Some(t) => v.in_ + t + i as f64 / fps,
                        None => v.in_ + (k0 + i) as f64 / fps,
                    };
                    if v.loop_ && src.duration > 0.0 {
                        src_t = src_t.rem_euclid(src.duration);
                    }
                    let held = v.hold_from.is_some_and(|hf| src_t >= hf);
                    let buf: &[u8] = match &mut vs.reader {
                        None => {
                            let start = match v.hold_from {
                                Some(hf) if src_t > hf => hf,
                                _ => src_t,
                            };
                            let mut r = VideoReader::open(&src.path, start, d.fps, vs.bw, vs.bh, v.loop_)?;
                            r.next_frame()?;
                            vs.reader = Some(r);
                            vs.reader.as_mut().unwrap().current()
                        }
                        Some(r) if held => r.current(),
                        Some(r) => r.next_frame()?,
                    };
                    let t = (k0 + i) as f64 / fps;
                    if !typed.visible_at(t) {
                        push(&mut tracks[idx], i, None);
                        continue;
                    }
                    let (sw, sh) = (src.size[0] as f64, src.size[1] as f64);
                    let c = transform::crop(typed, sw, sh, t, props, js)?;
                    let (fx, fy) = transform::fit_scale(v.fit, c[2], c[3], cw, ch);
                    let p = transform::place(typed, (c[2] * fx, c[3] * fy), (cw, ch), t, props, js)?;
                    let m = Affine::scale(s, s)
                        .then(&p.m)
                        .then(&Affine::scale(fx, fy))
                        .then(&Affine::translate(-c[0], -c[1]));
                    let (kx, ky) = (vs.bw as f64 / sw, vs.bh as f64 / sh);
                    let mb = m.then(&Affine::scale(1.0 / kx, 1.0 / ky));
                    draw_image(
                        &mut frame,
                        buf,
                        vs.bw,
                        vs.bh,
                        [c[0] * kx, c[1] * ky, c[2] * kx, c[3] * ky],
                        &mb,
                        p.opacity,
                    );
                    let b = if p.opacity > 0.0 { transform::bbox(&m, c, w as f64, h as f64) } else { None };
                    push(&mut tracks[idx], i, b);
                }
            }
        }
        sink(&frame)?;
    }
    Ok(tracks)
}

/// Renders a unit into a segment in the store and records it under the unit key.
pub fn render_unit(plan: &UnitPlan, store: &Store, js: &mut Js) -> Result<Value> {
    let d = &plan.desc;
    let tmp = store.tmp_path("mp4");
    let mut enc = Encoder::open(&tmp, d.canvas.w, d.canvas.h, d.fps, d.frames, &d.encode)?;
    let tracks = compose(plan, 0, d.frames, Graphics::Cache(store), js, |f| enc.write(&f.px))?;
    enc.finish()?;
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
