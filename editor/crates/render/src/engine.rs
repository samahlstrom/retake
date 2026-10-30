//! Opening a timeline for rendering and planning it: assets probed and hashed, units described hermetically, and the
//! two cache keys (layer and unit) computed.

use crate::audio::{AUDIO_VERSION, AudioPlan, Clip, DuckPlan, GainCurve, Loudness, MasterPlan};
use crate::ffmpeg::{self, EncodeSettings, MediaInfo, StreamSel};
use crate::graphics::{DISPLAY_FONT, DISPLAY_WEIGHT, FontData, GRAPHICS_VERSION, ImageData, Resources};
use crate::store::{Store, hex_of};
use anyhow::{Context, Result};
use retake_timeline::keyframe::Prop;
use retake_timeline::layer::{Clock, Content, Layer, TextValue};
use retake_timeline::model::{Ducks, Rgba, SpineKind, Under, gain_prop};
use retake_timeline::resolve::{Comp, UnitKind, UnitSpan};
use retake_timeline::{EditError, Errors, Project, Resolved, canonical_json};
use serde::Serialize;
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::sync::Arc;

/// Part of every unit key: bump when compositing code changes pixels. compositor/2: graphics split into static and
/// animated runs, and video decoded at its displayed size.
pub const ENGINE_VERSION: &str = "retake-render/0.1 compositor/2";

#[derive(Debug, Clone)]
pub struct Options {
    pub draft: bool,
    /// Output scale; default 1, or 0.5 for drafts.
    pub scale: Option<f64>,
    /// Output seconds [a, b): renders the units that overlap it.
    pub range: Option<(f64, f64)>,
    /// Units (and graphics runs) rendered in parallel.
    pub jobs: usize,
    /// The cache directory; default `<project>/.retake/cache`.
    pub cache: Option<PathBuf>,
}

impl Default for Options {
    fn default() -> Self {
        Options { draft: false, scale: None, range: None, jobs: 3, cache: None }
    }
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq)]
pub struct Canvas {
    /// Output pixels.
    pub w: u32,
    pub h: u32,
    pub scale: f64,
    /// Composition pixels (`format.size`).
    pub comp: [u32; 2],
}

impl Canvas {
    pub fn comp_f(&self) -> (f64, f64) {
        (self.comp[0] as f64, self.comp[1] as f64)
    }
}

#[derive(Debug, Clone)]
pub struct Asset {
    pub id: String,
    pub path: PathBuf,
    pub hash: String,
    pub info: MediaInfo,
    /// Drafts: a short-GOP proxy made by `retake-edit proxy` (its path and content hash).
    pub proxy: Option<(PathBuf, String)>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Src {
    pub hash: String,
    pub size: [u32; 2],
    pub duration: f64,
    /// The proxy decoded instead of the source (drafts only): its content hash, so a draft made from a proxy has
    /// another key than one made from the source.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub proxy: Option<String>,
    /// The file decoded: the source, or its proxy.
    #[serde(skip)]
    pub path: PathBuf,
    #[serde(skip)]
    pub asset: String,
}

/// One thing the compositor draws in a unit. `active` is the unit-frame range where it shows; `k0` is the
/// composition frame at unit frame 0.
#[derive(Debug, Clone, Serialize)]
#[serde(tag = "kind", rename_all = "lowercase")]
#[allow(clippy::large_enum_variant)] // a handful per unit
pub enum Item {
    Fill {
        color: [u8; 4],
        active: [i64; 2],
    },
    Graphics {
        key: String,
        k0: i64,
        active: [i64; 2],
    },
    Video {
        layer: Value,
        src: Src,
        props: Value,
        k0: i64,
        active: [i64; 2],
        /// For `clock: timeline`: output seconds at unit frame 0 (an absolute-time input, so it's in the key).
        #[serde(skip_serializing_if = "Option::is_none")]
        t_out0: Option<f64>,
        #[serde(skip)]
        typed: Box<Layer>,
    },
}

impl Item {
    pub fn active(&self) -> [i64; 2] {
        match self {
            Item::Fill { active, .. } | Item::Graphics { active, .. } | Item::Video { active, .. } => *active,
        }
    }
}

/// The fully resolved description of a unit: the only input the compositor gets. Its hash is the unit key.
#[derive(Debug, Clone, Serialize)]
pub struct UnitDesc {
    pub engine: &'static str,
    pub canvas: Canvas,
    pub fps: u32,
    pub frames: i64,
    pub encode: EncodeSettings,
    pub items: Vec<Item>,
}

impl UnitDesc {
    pub fn key(&self) -> String {
        hex_of(canonical_json(&serde_json::to_value(self).expect("unit desc serializes")).as_bytes())
    }
}

/// Which timeline element an item draws (for the manifest; not part of any key).
#[derive(Debug, Clone, Serialize)]
pub struct Owner {
    pub element: String,
    pub kind: String,
    pub ptr: String,
    pub z: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub layer: Option<(String, String)>,
}

/// A run of consecutive graphics layers rendered as alpha frames, keyed independently of where it lands. A run is
/// either static (nothing in it changes over its range: drawn once, stored as one image, and keyed without its
/// range, so it is reused by a unit of any length) or animated (drawn per frame, skipping frames that look like the
/// one before).
#[derive(Debug, Clone)]
pub struct RunSpec {
    pub key: String,
    pub layers: Vec<Layer>,
    pub props: Value,
    /// Composition frames [a, b).
    pub range: [i64; 2],
    /// Nothing changes over the range: one image.
    pub still: bool,
}

fn prop_static(p: &Option<Prop>) -> bool {
    p.as_ref().is_none_or(|p| p.is_static())
}

/// True when a layer looks the same on every frame of composition seconds [a, b): constant props, no text
/// expression, and no edge (`from` / `to`) inside the range.
pub fn layer_is_static(l: &Layer, a: f64, b: f64) -> bool {
    let tr = &l.transform;
    let edges_outside = l.from.is_none_or(|f| f <= a || f >= b) && l.to.is_none_or(|e| e <= a || e >= b);
    let text_static = match &l.content {
        Content::Text(t) => {
            !matches!(t.text, TextValue::Expr { .. }) && t.highlight.as_ref().is_none_or(|h| h.words.is_static())
        }
        _ => true,
    };
    edges_outside
        && text_static
        && prop_static(&tr.position)
        && prop_static(&tr.scale)
        && prop_static(&tr.rotation)
        && prop_static(&tr.opacity)
        && prop_static(&l.crop)
        && prop_static(&l.blur)
}

/// True when a layer shows on no frame of composition seconds [a, b).
pub fn layer_never_visible(l: &Layer, a: f64, b: f64) -> bool {
    l.from.is_some_and(|f| f >= b) || l.to.is_some_and(|e| e <= a) || l.from.zip(l.to).is_some_and(|(f, e)| e <= f)
}

#[derive(Debug, Clone)]
pub struct UnitPlan {
    pub span: UnitSpan,
    pub desc: UnitDesc,
    pub owners: Vec<Owner>,
    pub key: String,
    pub runs: Vec<RunSpec>,
    pub cached: bool,
}

pub struct Engine {
    pub project: Project,
    pub res: Resolved,
    pub store: Store,
    pub opts: Options,
    pub canvas: Canvas,
    pub encode: EncodeSettings,
    pub assets: BTreeMap<String, Asset>,
    pub resources: Arc<Resources>,
}

fn even(x: f64) -> u32 {
    ((x / 2.0).round() as u32 * 2).max(2)
}

/// A layer's JSON as hashed: no id (renaming doesn't re-render), no meta, no audio (sound isn't pixels).
fn key_json(v: &Value) -> Value {
    let mut v = v.clone();
    if let Some(o) = v.as_object_mut() {
        o.remove("id");
        o.remove("meta");
        o.remove("audio");
    }
    v
}

/// Every `stream` an element asks for must exist in its asset (`audio_stream` errors list the streams there are).
fn check_streams(project: &Project, res: &Resolved, assets: &BTreeMap<String, Asset>) -> Result<()> {
    let mut errs = vec![];
    let mut check = |asset: &str, stream: Option<u32>, ptr: String| {
        let (Some(n), Some(a)) = (stream, assets.get(asset)) else { return };
        if n as usize >= a.info.audio_streams.len() {
            errs.push(
                EditError::new(
                    "audio_stream",
                    format!("asset `{asset}` has no audio stream {n} ({})", a.info.describe_audio()),
                )
                .at(ptr)
                .hint(match a.info.audio_streams.len() {
                    0 => "the file has no sound: drop `stream`".to_string(),
                    k => format!("streams count from 0: use 0 to {}", k - 1),
                }),
            );
        }
    };
    for (i, s) in project.tl.spine.iter().enumerate() {
        check(&s.asset, s.audio.as_ref().and_then(|a| a.stream), format!("/spine/{i}/audio/stream"));
    }
    for (i, a) in project.tl.audio.iter().enumerate() {
        check(&a.asset, a.stream, format!("/audio/{i}/stream"));
    }
    let mut comps: Vec<&Comp> = res.shots.iter().map(|s| &s.comp).collect();
    comps.extend(res.overlays.iter().map(|o| &o.comp));
    comps.extend(res.spine.iter().filter_map(|s| s.comp.as_ref()));
    for c in comps {
        for (li, l) in c.layers.iter().enumerate() {
            if let Content::Video(v) = &l.content {
                check(&v.asset, v.audio.as_ref().and_then(|a| a.stream), format!("{}/layers/{li}/audio/stream", c.ptr));
            }
        }
    }
    if errs.is_empty() { Ok(()) } else { Err(Errors(errs).into()) }
}

/// Measures what a plan's loudness targets need: each group's integrated loudness (its clips end to end, as
/// decoded), then, with those levels, the whole mix's for the master. `{"groups": {group: dB}, "master_gain_db"}`.
fn compute_levels(full: &AudioPlan) -> Result<Value> {
    let mut groups: BTreeMap<String, (f64, Vec<&Clip>)> = BTreeMap::new();
    for c in &full.clips {
        if let Some(l) = &c.loudness {
            groups.entry(l.group.clone()).or_insert_with(|| (l.target, vec![])).1.push(c);
        }
    }
    let to_target = |target: f64, measured: f64| {
        if measured.is_finite() { (target - measured).clamp(-60.0, 60.0) } else { 0.0 }
    };
    let mut out = serde_json::Map::new();
    for (g, (target, cs)) in groups {
        let m = crate::audio::measure(&cs, full.sr)?;
        out.insert(g, json!(to_target(target, m)));
    }
    let mut levels = json!({"groups": out, "master_gain_db": null});
    if let Some(t) = full.master.as_ref().and_then(|m| m.loudness) {
        let mut leveled = full.clone();
        apply_levels(&mut leveled, &levels);
        let raw = crate::audio::mix_raw(&leveled)?;
        levels["master_gain_db"] = json!(to_target(t, crate::dsp::integrated_lufs(&raw, full.sr)));
    }
    Ok(levels)
}

fn apply_levels(ap: &mut AudioPlan, levels: &Value) {
    for c in &mut ap.clips {
        if let Some(l) = &mut c.loudness {
            l.level_db = Some(levels["groups"][&l.group].as_f64().unwrap_or(0.0));
        }
    }
    if let Some(m) = &mut ap.master {
        m.gain_db = levels["master_gain_db"].as_f64();
    }
}

/// The stream an element plays, with its channel count.
fn stream_sel(a: &Asset, stream: Option<u32>) -> StreamSel {
    let index = stream.unwrap_or(0);
    let channels = a.info.audio_streams.get(index as usize).map(|s| s.channels).unwrap_or(2);
    StreamSel { index, channels }
}

/// A resolved `gain_db`: a constant folds into the clip's gain, keys and expressions become a curve.
fn gain_parts(p: Option<&Prop>, t0: f64, props: &Value) -> (f64, Option<GainCurve>) {
    match p {
        None => (0.0, None),
        Some(Prop::Const(v)) => (v.to_vec().first().copied().unwrap_or(0.0), None),
        Some(p) => (0.0, Some(GainCurve { prop: p.clone(), t0, props: props.clone() })),
    }
}

impl Engine {
    /// Loads, validates and resolves a timeline; probes and hashes the assets it uses.
    pub fn open(path: &Path, opts: Options) -> Result<Engine> {
        crate::init();
        Engine::from_project(Project::load(path)?, opts)
    }

    /// Like [`Engine::open`] for a timeline already in memory (a patch being tried, say).
    pub fn from_project(project: Project, opts: Options) -> Result<Engine> {
        let errs = retake_timeline::validate(&project);
        if !errs.is_empty() {
            return Err(Errors(errs).into());
        }
        let res = retake_timeline::resolve(&project).map_err(Errors)?;
        let cache = opts.cache.clone().unwrap_or_else(|| project.work_dir().join("cache"));
        let store = Store::open(&cache)?;
        let scale = opts.scale.unwrap_or(if opts.draft { 0.5 } else { 1.0 });
        let [cw, ch] = project.tl.format.size;
        let canvas = Canvas { w: even(cw as f64 * scale), h: even(ch as f64 * scale), scale, comp: [cw, ch] };
        let encode = EncodeSettings::new(opts.draft, project.tl.format.fps);

        // Assets the render reads.
        let mut used: BTreeSet<String> = BTreeSet::new();
        let mut images: BTreeSet<String> = BTreeSet::new();
        used.extend(project.tl.spine.iter().map(|s| s.asset.clone()));
        used.extend(project.tl.audio.iter().map(|a| a.asset.clone()));
        let mut comps: Vec<&Comp> = res.shots.iter().map(|s| &s.comp).collect();
        comps.extend(res.overlays.iter().map(|o| &o.comp));
        comps.extend(res.spine.iter().filter_map(|s| s.comp.as_ref()));
        for c in &comps {
            for l in &c.layers {
                match &l.content {
                    Content::Video(v) => {
                        used.insert(v.asset.clone());
                    }
                    Content::Image(i) => {
                        images.insert(i.asset.clone());
                    }
                    _ => {}
                }
            }
        }
        let mut assets = BTreeMap::new();
        for id in &used {
            let p = project.asset_path(id)?;
            let hash = store.asset_hash(&p)?;
            // probe2: probes that list the audio streams (older ones under "probe" don't).
            let info = match store.get_index("probe2", &hash).and_then(|v| serde_json::from_value(v).ok()) {
                Some(i) => i,
                None => {
                    let i = ffmpeg::probe(&p).with_context(|| format!("asset {id}"))?;
                    store.set_index("probe2", &hash, &serde_json::to_value(&i)?)?;
                    i
                }
            };
            let proxy = if opts.draft { crate::proxy::lookup(&store, &hash) } else { None };
            assets.insert(id.clone(), Asset { id: id.clone(), path: p, hash, info, proxy });
        }
        let mut resources = Resources::default();
        resources.fonts.insert(
            "display".into(),
            FontData { bytes: Arc::new(DISPLAY_FONT.to_vec()), hash: hex_of(DISPLAY_FONT), weight: DISPLAY_WEIGHT },
        );
        for (name, f) in &project.tl.fonts {
            let p = project.resolve_path(&f.path)?;
            let bytes = std::fs::read(&p).with_context(|| format!("font {name}: {}", p.display()))?;
            let hash = hex_of(&bytes);
            resources
                .fonts
                .insert(name.clone(), FontData { bytes: Arc::new(bytes), hash, weight: f.weight.unwrap_or(400.0) });
        }
        for id in &images {
            let p = project.asset_path(id)?;
            let bytes = std::fs::read(&p).with_context(|| format!("image {id}: {}", p.display()))?;
            let hash = hex_of(&bytes);
            resources.images.insert(id.clone(), ImageData { bytes: Arc::new(bytes), hash });
        }
        store.save_asset_hashes()?;
        check_streams(&project, &res, &assets)?;
        Ok(Engine { project, res, store, opts, canvas, encode, assets, resources: Arc::new(resources) })
    }

    pub fn fps(&self) -> u32 {
        self.res.fps
    }

    /// Units overlapping the requested range (all units without one).
    pub fn units_in_range(&self) -> Vec<UnitSpan> {
        let (a, b) = match self.opts.range {
            Some((a, b)) => (self.res.frame(a), self.res.frame(b).max(self.res.frame(a) + 1)),
            None => (0, self.res.frames),
        };
        self.res.units.iter().filter(|u| u.f0 < b && u.f1 > a).cloned().collect()
    }

    fn run_key(&self, layers: &[Value], typed: &[Layer], props: &Value, range: [i64; 2], still: bool) -> String {
        let fonts: BTreeMap<String, String> = self
            .resources
            .faces(typed)
            .into_iter()
            .map(|(n, w)| {
                let h = self.resources.fonts.get(&n).map(|f| f.hash.clone()).unwrap_or_default();
                (format!("{n}@{w}"), h)
            })
            .collect();
        let images: BTreeMap<String, String> = typed
            .iter()
            .filter_map(|l| match &l.content {
                Content::Image(i) => Some((
                    i.asset.clone(),
                    self.resources.images.get(&i.asset).map(|d| d.hash.clone()).unwrap_or_default(),
                )),
                _ => None,
            })
            .collect();
        let desc = json!({
            "v": GRAPHICS_VERSION,
            "layers": layers.iter().map(key_json).collect::<Vec<_>>(),
            "props": props,
            "canvas": [self.canvas.w, self.canvas.h],
            "scale": self.canvas.scale,
            "comp": self.canvas.comp,
            "fps": self.fps(),
            // A static run is one image whatever its length; an animated one is drawn per frame of its range.
            "range": if still { Value::Null } else { json!(range) },
            "static": still,
            "fonts": fonts,
            "images": images,
        });
        hex_of(canonical_json(&desc).as_bytes())
    }

    /// Adds a composition's items: a background fill, then graphics runs and video layers in draw order.
    #[allow(clippy::too_many_arguments)]
    fn comp_items(
        &self,
        comp: &Comp,
        owner: &Owner,
        k0: i64,
        active: [i64; 2],
        f0: i64,
        items: &mut Vec<Item>,
        owners: &mut Vec<Owner>,
        runs: &mut Vec<RunSpec>,
    ) -> Result<()> {
        if let Some(bg) = &comp.background {
            items.push(Item::Fill { color: Rgba::parse(bg).unwrap_or(Rgba::BLACK).0, active });
            owners.push(owner.clone());
        }
        let range = [k0 + active[0], k0 + active[1]];
        let (ra, rb) = (range[0] as f64 / self.fps() as f64, range[1] as f64 / self.fps() as f64);
        let mut pending: Vec<(Value, Layer)> = vec![];
        // Consecutive graphics layers become runs; a static stretch and an animated stretch are separate runs, so a
        // static card under a ticking clock is drawn once (draw order is kept: runs composite in layer order).
        let flush = |pending: &mut Vec<(Value, Layer)>,
                     items: &mut Vec<Item>,
                     owners: &mut Vec<Owner>,
                     runs: &mut Vec<RunSpec>| {
            let all: Vec<(Value, Layer)> = pending.drain(..).filter(|(_, l)| !layer_never_visible(l, ra, rb)).collect();
            let mut groups: Vec<(bool, Vec<(Value, Layer)>)> = vec![];
            for (j, l) in all {
                let still = layer_is_static(&l, ra, rb);
                match groups.last_mut() {
                    Some((s, g)) if *s == still => g.push((j, l)),
                    _ => groups.push((still, vec![(j, l)])),
                }
            }
            for (still, g) in groups {
                let (js, ls): (Vec<Value>, Vec<Layer>) = g.into_iter().unzip();
                let key = self.run_key(&js, &ls, &comp.props, range, still);
                runs.push(RunSpec { key: key.clone(), layers: ls, props: comp.props.clone(), range, still });
                items.push(Item::Graphics { key, k0, active });
                owners.push(owner.clone());
            }
        };
        for (li, (j, l)) in comp.layers_json.iter().zip(&comp.layers).enumerate() {
            match &l.content {
                Content::Video(v) => {
                    flush(&mut pending, items, owners, runs);
                    let a = self.assets.get(&v.asset).with_context(|| format!("asset {}", v.asset))?;
                    let size = a.info.size.ok_or_else(|| {
                        EditError::new("asset_not_video", format!("asset `{}` has no video stream", v.asset))
                            .at(comp.ptr.clone())
                    })?;
                    let t_out0 = (v.clock == Clock::Timeline).then(|| f0 as f64 / self.fps() as f64);
                    items.push(Item::Video {
                        layer: key_json(j),
                        src: Src {
                            hash: a.hash.clone(),
                            size,
                            duration: a.info.duration,
                            proxy: a.proxy.as_ref().map(|(_, h)| h.clone()),
                            path: a.proxy.as_ref().map(|(p, _)| p.clone()).unwrap_or_else(|| a.path.clone()),
                            asset: a.id.clone(),
                        },
                        props: comp.props.clone(),
                        k0,
                        active,
                        t_out0,
                        typed: Box::new(l.clone()),
                    });
                    let lptr = format!("{}/layers/{li}", comp.ptr);
                    owners.push(Owner { layer: Some((l.id.clone(), lptr)), ..owner.clone() });
                }
                _ => pending.push((j.clone(), l.clone())),
            }
        }
        flush(&mut pending, items, owners, runs);
        Ok(())
    }

    /// Describes one unit hermetically and computes its key.
    pub fn plan_unit(&self, u: &UnitSpan) -> Result<UnitPlan> {
        let n = u.frames();
        let fps = self.fps();
        let mut items = vec![];
        let mut owners = vec![];
        let mut runs = vec![];
        let bg = self.project.tl.format.background.as_deref().and_then(Rgba::parse).unwrap_or(Rgba::BLACK).0;
        items.push(Item::Fill { color: [bg[0], bg[1], bg[2], 255], active: [0, n] });
        owners.push(Owner {
            element: String::new(),
            kind: "background".into(),
            ptr: "/format".into(),
            z: 0,
            layer: None,
        });
        match u.kind {
            UnitKind::Shot => {
                let s = self.res.shot(&u.source).context("unit shot")?;
                let c0 = self.res.frame(s.start);
                let k0 = self.res.local_frame(c0, u.f0);
                let o = Owner { element: s.id.clone(), kind: "shot".into(), ptr: s.ptr.clone(), z: 0, layer: None };
                self.comp_items(&s.comp, &o, k0, [0, n], u.f0, &mut items, &mut owners, &mut runs)?;
            }
            UnitKind::Insert => {
                let s = self.res.spine_item(&u.source).context("unit insert")?;
                let c0 = self.res.frame(s.start);
                let o = Owner { element: s.id.clone(), kind: "insert".into(), ptr: s.ptr.clone(), z: 0, layer: None };
                if let Some(c) = &s.comp {
                    self.comp_items(c, &o, u.f0 - c0, [0, n], u.f0, &mut items, &mut owners, &mut runs)?;
                }
            }
            UnitKind::Gap => {}
        }
        if u.kind != UnitKind::Insert {
            let mut ovs: Vec<_> = self.res.overlays.iter().collect();
            ovs.sort_by_key(|o| (o.z, o.order));
            for (rank, o) in ovs.iter().enumerate() {
                let c0 = self.res.frame(o.from);
                let len = self.res.local_frame(c0, self.res.frame(o.to));
                let k0 = self.res.local_frame(c0, u.f0);
                let active = [(-k0).max(0), (len - k0).min(n)];
                if active[1] <= active[0] {
                    continue;
                }
                let ow = Owner {
                    element: o.id.clone(),
                    kind: "overlay".into(),
                    ptr: o.ptr.clone(),
                    z: rank as i64 + 1,
                    layer: None,
                };
                self.comp_items(&o.comp, &ow, k0, active, u.f0, &mut items, &mut owners, &mut runs)?;
            }
        }
        let desc = UnitDesc {
            engine: ENGINE_VERSION,
            canvas: self.canvas,
            fps,
            frames: n,
            encode: self.encode.clone(),
            items,
        };
        let key = desc.key();
        let cached = self
            .store
            .get_index("unit", &key)
            .and_then(|v| v["blob"].as_str().map(|b| self.store.has_blob(b)))
            .unwrap_or(false);
        Ok(UnitPlan { span: u.clone(), desc, owners, key, runs, cached })
    }

    pub fn plan(&self) -> Result<Vec<UnitPlan>> {
        self.units_in_range().iter().map(|u| self.plan_unit(u)).collect()
    }

    /// The audio for the planned units' span: spine VO and inserts, video layers with sound, audio elements.
    pub fn audio_plan(&self, units: &[UnitPlan]) -> AudioPlan {
        let sr = self.project.tl.format.sample_rate;
        let fps = self.fps();
        let spf = (sr / fps) as i64;
        let start = units.first().map(|u| u.span.f0 * spf).unwrap_or(0);
        let end = units.last().map(|u| u.span.f1 * spf).unwrap_or(0);
        let s = |t: f64| (t * sr as f64).round() as i64;
        let mut clips = vec![];
        let has_audio = |id: &str| self.assets.get(id).filter(|a| a.info.audio);
        let none = json!({});
        let loud = |target: Option<f64>, group: &str| {
            target.map(|t| Loudness { target: t, group: group.to_string(), level_db: None })
        };
        for (i, sp) in self.res.spine.iter().enumerate() {
            let item = &self.project.tl.spine[i];
            let opts = item.audio.clone().unwrap_or_default();
            let Some(a) = has_audio(&sp.asset) else { continue };
            if opts.mute {
                continue;
            }
            let (gain_db, gain) = gain_parts(sp.gain.as_ref(), 0.0, &none);
            clips.push(Clip {
                role: if sp.kind == SpineKind::Insert { "insert" } else { "vo" }.into(),
                hash: a.hash.clone(),
                path: a.path.clone(),
                owner: sp.id.clone(),
                src_in: sp.src_in,
                at: s(sp.start),
                len: s(sp.end) - s(sp.start),
                stream: stream_sel(a, opts.stream),
                gain_db,
                gain,
                fade_in: opts.fade_in,
                fade_out: opts.fade_out,
                fade_curve: opts.fade_curve,
                looping: false,
                duck: vec![],
                loudness: loud(opts.loudness, &sp.id),
            });
        }
        for u in units {
            for (it, ow) in u.desc.items.iter().zip(&u.owners) {
                let Item::Video { typed, src, props, k0, active, t_out0, .. } = it else { continue };
                let Content::Video(v) = &typed.content else { continue };
                let Some(opts) = v.audio.clone().filter(|o| !o.mute) else { continue };
                // The source's own sound (a draft may decode the picture from a proxy, which has none).
                let Some(asset) = self.assets.get(&src.asset).filter(|a| a.info.audio) else { continue };
                let t0 = match t_out0 {
                    Some(t) => v.in_ + t + active[0] as f64 / fps as f64,
                    None => v.in_ + (k0 + active[0]) as f64 / fps as f64,
                };
                // The layer's gain keys are in composition seconds.
                let prop = gain_prop(opts.gain_db.as_ref()).ok().flatten();
                let (gain_db, gain) = gain_parts(prop.as_ref(), (k0 + active[0]) as f64 / fps as f64, props);
                clips.push(Clip {
                    role: "layer".into(),
                    hash: src.hash.clone(),
                    path: asset.path.clone(),
                    owner: ow.element.clone(),
                    src_in: t0.max(0.0),
                    at: (u.span.f0 + active[0]) * spf,
                    len: (active[1] - active[0]) * spf,
                    stream: stream_sel(asset, opts.stream),
                    gain_db,
                    gain,
                    fade_in: opts.fade_in,
                    fade_out: opts.fade_out,
                    fade_curve: opts.fade_curve,
                    looping: v.loop_,
                    duck: self.duck_plans(opts.duck.as_ref()),
                    loudness: loud(opts.loudness, &format!("{}#{}", ow.element, typed.id)),
                });
            }
        }
        for (i, sp) in self.res.audio.iter().enumerate() {
            let el = &self.project.tl.audio[i];
            let Some(a) = has_audio(&el.asset) else { continue };
            let (gain_db, gain) = gain_parts(sp.gain.as_ref(), 0.0, &none);
            clips.push(Clip {
                role: "audio".into(),
                hash: a.hash.clone(),
                path: a.path.clone(),
                owner: sp.id.clone(),
                src_in: el.in_,
                at: s(sp.from),
                len: s(sp.to) - s(sp.from),
                stream: stream_sel(a, el.stream),
                gain_db,
                gain,
                fade_in: el.fade_in,
                fade_out: el.fade_out,
                fade_curve: el.fade_curve,
                looping: el.loop_,
                duck: self.duck_plans(el.duck.as_ref()),
                loudness: loud(el.loudness, &sp.id),
            });
        }
        let master = self.project.tl.master.as_ref().filter(|m| m.is_active()).map(|m| MasterPlan {
            loudness: m.loudness,
            gain_db: None,
            lowpass: m.lowpass,
            true_peak: m.true_peak,
        });
        AudioPlan { v: AUDIO_VERSION, sr, fps, start, len: end - start, clips, master }
    }

    /// Ducking rules as mixed: span rules get their spans in output samples.
    fn duck_plans(&self, d: Option<&Ducks>) -> Vec<DuckPlan> {
        let Some(d) = d else { return vec![] };
        let sr = self.project.tl.format.sample_rate as f64;
        let s = |t: f64| (t * sr).round() as i64;
        d.rules()
            .into_iter()
            .map(|r| {
                let spans: Option<Vec<[i64; 2]>> = match &r.under {
                    Under::Named(n) if n == "inserts" => Some(
                        self.res
                            .spine
                            .iter()
                            .filter(|x| x.kind == SpineKind::Insert)
                            .map(|x| [s(x.start), s(x.end)])
                            .collect(),
                    ),
                    Under::Named(_) => None,
                    Under::Ids(ids) => Some(
                        self.res.spine.iter().filter(|x| ids.contains(&x.id)).map(|x| [s(x.start), s(x.end)]).collect(),
                    ),
                };
                match spans {
                    None => DuckPlan::Vo { db: r.db, release: r.release.unwrap_or(0.35) },
                    Some(spans) => DuckPlan::Spans {
                        db: r.db,
                        spans,
                        attack: r.attack.unwrap_or(0.35),
                        release: r.release.unwrap_or(0.35),
                    },
                }
            })
            .collect()
    }

    /// Fills in the measured levels of a plan (clips' loudness targets, the master's loudness gain), measured on the
    /// whole timeline's mix and cached, so a range render uses the levels of the full one. With `compute` false it
    /// only looks in the cache, and returns false when they aren't there yet.
    pub fn audio_levels(&self, ap: &mut AudioPlan, compute: bool) -> Result<bool> {
        if !ap.needs_levels() {
            return Ok(true);
        }
        let spf = (ap.sr / ap.fps) as i64;
        let full = if ap.start == 0 && ap.len == self.res.frames * spf {
            ap.clone()
        } else {
            let all: Vec<UnitPlan> = self.res.units.iter().map(|u| self.plan_unit(u)).collect::<Result<_>>()?;
            self.audio_plan(&all)
        };
        let key = hex_of(canonical_json(&json!({"v": "levels/1", "plan": full})).as_bytes());
        let levels = match self.store.get_index("levels", &key) {
            Some(v) => v,
            None if !compute => return Ok(false),
            None => {
                let v = compute_levels(&full)?;
                self.store.set_index("levels", &key, &v)?;
                v
            }
        };
        apply_levels(ap, &levels);
        Ok(true)
    }

    pub fn audio_key(plan: &AudioPlan) -> String {
        hex_of(canonical_json(&serde_json::to_value(plan).expect("audio plan serializes")).as_bytes())
    }

    /// The hash of the timeline document as rendered.
    pub fn timeline_hash(&self) -> String {
        retake_timeline::b3(canonical_json(&self.project.doc).as_bytes())
    }
}
