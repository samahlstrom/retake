//! The render pipeline: plan → render missing graphics runs → render missing units → mix audio → package → manifest.

use crate::audio::{self, range_hash};
use crate::engine::{Engine, Item, RunSpec, UnitPlan};
use crate::expr::Js;
use crate::ffmpeg;
use crate::graphics::Painter;
use crate::unit;
use anyhow::{Context, Result, anyhow};
use retake_timeline::manifest::{self, Element, Manifest, Source, Stats, TimelineRef, Unit};
use retake_timeline::resolve::{UnitKind, round6};
use serde::Serialize;
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet};
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Instant;

/// Runs `f` over `items` on `jobs` threads, each with its own state from `init` (Skia and QuickJS objects stay on
/// their thread). Stops at the first error.
fn pool<T: Sync, S>(
    items: &[T],
    jobs: usize,
    init: impl Fn() -> Result<S> + Sync,
    f: impl Fn(&mut S, &T) -> Result<()> + Sync,
) -> Result<()> {
    if items.is_empty() {
        return Ok(());
    }
    let next = AtomicUsize::new(0);
    let err: Mutex<Option<anyhow::Error>> = Mutex::new(None);
    std::thread::scope(|sc| {
        for _ in 0..jobs.clamp(1, items.len()) {
            sc.spawn(|| {
                let mut st = match init() {
                    Ok(s) => s,
                    Err(e) => {
                        err.lock().unwrap().get_or_insert(e);
                        return;
                    }
                };
                loop {
                    if err.lock().unwrap().is_some() {
                        return;
                    }
                    let i = next.fetch_add(1, Ordering::SeqCst);
                    let Some(it) = items.get(i) else { return };
                    if let Err(e) = f(&mut st, it) {
                        err.lock().unwrap().get_or_insert(e);
                        return;
                    }
                }
            });
        }
    });
    match err.into_inner().unwrap() {
        Some(e) => Err(e),
        None => Ok(()),
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct PlanUnit {
    pub unit: String,
    pub key: String,
    pub kind: String,
    pub source: String,
    pub t: [f64; 2],
    pub frames: [i64; 2],
    pub cached: bool,
    pub layers: usize,
    pub layers_cached: usize,
}

#[derive(Debug, Clone, Serialize)]
pub struct PlanReport {
    pub timeline: TimelineRef,
    pub size: [u32; 2],
    pub fps: u32,
    pub draft: bool,
    pub range: [f64; 2],
    pub units: Vec<PlanUnit>,
    pub hits: usize,
    pub misses: usize,
    pub frames_to_render: i64,
    pub layers_to_render: usize,
    pub audio_cached: bool,
    /// Rough seconds to render the misses, from measured throughput on this machine.
    pub estimate_s: f64,
}

fn unit_name(p: &UnitPlan) -> String {
    format!("u{}", p.span.index + 1)
}

fn kind_name(k: UnitKind) -> &'static str {
    match k {
        UnitKind::Shot => "shot",
        UnitKind::Insert => "insert",
        UnitKind::Gap => "gap",
    }
}

fn ms_per_mpx(e: &Engine) -> f64 {
    std::fs::read_to_string(e.store.root().join("stats.json"))
        .ok()
        .and_then(|s| serde_json::from_str::<Value>(&s).ok())
        .and_then(|v| v["ms_per_mpx_frame"].as_f64())
        .unwrap_or(15.0)
}

fn record_speed(e: &Engine, ms: f64) {
    let old = ms_per_mpx(e);
    let v = json!({"ms_per_mpx_frame": (old + ms) / 2.0});
    let _ = std::fs::write(e.store.root().join("stats.json"), v.to_string());
}

pub fn plan(e: &Engine) -> Result<(Vec<UnitPlan>, PlanReport)> {
    let plans = e.plan()?;
    let fps = e.fps() as f64;
    let mut units = vec![];
    let mut layer_keys: BTreeSet<&str> = BTreeSet::new();
    for p in &plans {
        let cached_layers = p.runs.iter().filter(|r| e.store.get_index("layer", &r.key).is_some()).count();
        if !p.cached {
            for r in &p.runs {
                if e.store.get_index("layer", &r.key).is_none() {
                    layer_keys.insert(&r.key);
                }
            }
        }
        units.push(PlanUnit {
            unit: unit_name(p),
            key: format!("b3:{}", p.key),
            kind: kind_name(p.span.kind).into(),
            source: p.span.source.clone(),
            t: [round6(p.span.f0 as f64 / fps), round6(p.span.f1 as f64 / fps)],
            frames: [p.span.f0, p.span.f1],
            cached: p.cached,
            layers: p.runs.len(),
            layers_cached: cached_layers,
        });
    }
    let misses: Vec<&UnitPlan> = plans.iter().filter(|p| !p.cached).collect();
    let frames_to_render: i64 = misses.iter().map(|p| p.span.frames()).sum();
    let mpx = e.canvas.w as f64 * e.canvas.h as f64 / 1e6;
    let par = e.opts.jobs.clamp(1, misses.len().max(1)) as f64;
    let estimate_s = (frames_to_render as f64 * mpx * ms_per_mpx(e) / 1000.0 / par * 10.0).round() / 10.0;
    let audio_cached = e.store.get_index("audio", &Engine::audio_key(&e.audio_plan(&plans))).is_some();
    let range = [units.first().map(|u| u.t[0]).unwrap_or(0.0), units.last().map(|u| u.t[1]).unwrap_or(0.0)];
    let report = PlanReport {
        timeline: TimelineRef { id: e.project.tl.id.clone(), rev: e.project.tl.rev, hash: e.timeline_hash() },
        size: [e.canvas.w, e.canvas.h],
        fps: e.fps(),
        draft: e.opts.draft,
        range,
        hits: plans.len() - misses.len(),
        misses: misses.len(),
        units,
        frames_to_render,
        layers_to_render: layer_keys.len(),
        audio_cached,
        estimate_s,
    };
    Ok((plans, report))
}

#[derive(Debug, Clone, Serialize)]
pub struct RenderReport {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub output: Option<PathBuf>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub manifest_path: Option<PathBuf>,
    pub units: usize,
    pub reused: usize,
    pub rendered: usize,
    pub layers_rendered: usize,
    pub layers_reused: usize,
    /// Graphics frames the rendered runs cover, and how many of them were drawn (static runs draw one image;
    /// animated runs skip frames that look like the one before).
    pub graphics_frames: usize,
    pub graphics_drawn: usize,
    pub audio_reused: bool,
    pub frames: i64,
    pub duration: f64,
    /// Seconds per phase.
    pub timings: BTreeMap<String, f64>,
    #[serde(skip)]
    pub manifest: Manifest,
}

/// The default output path: `.retake/renders/<id>-r<rev>[-draft][-<a>-<b>].mp4`.
pub fn default_output(e: &Engine) -> PathBuf {
    let mut name = format!("{}-r{}", e.project.tl.id, e.project.tl.rev);
    if e.opts.draft {
        name.push_str("-draft");
    }
    if let Some((a, b)) = e.opts.range {
        name.push_str(&format!("-{a}-{b}"));
    }
    e.project.work_dir().join("renders").join(format!("{name}.mp4"))
}

/// Renders what's missing; with `out` set, packages an MP4 there and writes `<out stem>.manifest.json` next to it.
pub fn render(e: &Engine, out: Option<&Path>) -> Result<RenderReport> {
    let mut timings = BTreeMap::new();
    let mut lap = Instant::now();
    let mut tick = |name: &str, timings: &mut BTreeMap<String, f64>| {
        timings.insert(name.to_string(), (lap.elapsed().as_secs_f64() * 1000.0).round() / 1000.0);
        lap = Instant::now();
    };
    e.store.clean_tmp();
    let (plans, _) = plan(e)?;
    tick("plan", &mut timings);

    // Graphics runs the missing units need.
    let mut need: BTreeMap<String, RunSpec> = BTreeMap::new();
    let mut have = BTreeSet::new();
    for p in plans.iter().filter(|p| !p.cached) {
        for r in &p.runs {
            if e.store.get_index("layer", &r.key).is_some() {
                have.insert(r.key.clone());
            } else {
                need.entry(r.key.clone()).or_insert_with(|| r.clone());
            }
        }
    }
    let runs: Vec<RunSpec> = need.into_values().collect();
    let faces = e.resources.faces(runs.iter().flat_map(|r| r.layers.iter()));
    let (drawn, gframes) = (AtomicUsize::new(0), AtomicUsize::new(0));
    pool(
        &runs,
        e.opts.jobs,
        || Painter::new(e.resources.clone(), &faces).map_err(|x| anyhow!(x)),
        |painter, r| {
            let st = unit::render_run(r, painter, &e.canvas, e.fps(), &e.store).context("graphics run")?;
            drawn.fetch_add(st.drawn, Ordering::Relaxed);
            gframes.fetch_add(st.frames, Ordering::Relaxed);
            Ok(())
        },
    )?;
    tick("graphics", &mut timings);

    // Units.
    let missing: Vec<&UnitPlan> = plans.iter().filter(|p| !p.cached).collect();
    let t_units = Instant::now();
    pool(
        &missing,
        e.opts.jobs,
        || Js::new().map_err(|x| anyhow!(x)),
        |js, p| unit::render_unit(p, &e.store, js).map(|_| ()).with_context(|| format!("unit {}", unit_name(p))),
    )?;
    let px_frames: f64 =
        missing.iter().map(|p| p.span.frames() as f64).sum::<f64>() * e.canvas.w as f64 * e.canvas.h as f64 / 1e6;
    if px_frames > 0.0 {
        let par = e.opts.jobs.clamp(1, missing.len()) as f64;
        record_speed(e, t_units.elapsed().as_secs_f64() * 1000.0 * par / px_frames);
    }
    tick("units", &mut timings);

    // Audio: one mix for the whole range, cached by its plan.
    let ap = e.audio_plan(&plans);
    let akey = Engine::audio_key(&ap);
    let (aac, digests, audio_reused) = match e.store.get_index("audio", &akey) {
        Some(v) if v["aac"].as_str().is_some_and(|h| e.store.has_blob(h)) => {
            let d = e.store.get_blob(v["digests"].as_str().context("digests")?)?;
            let digests: Vec<[u8; 32]> = d.as_chunks::<32>().0.to_vec();
            (v["aac"].as_str().unwrap().to_string(), digests, true)
        }
        _ => {
            let m = audio::mix(&ap)?;
            let tmp = e.store.tmp_path("m4a");
            ffmpeg::encode_aac(&m.pcm, ap.sr, &tmp)?;
            let aac = e.store.put_file(&tmp)?;
            let flat: Vec<u8> = m.frame_digests.iter().flatten().copied().collect();
            let dh = e.store.put_bytes(&flat)?;
            e.store.set_index("audio", &akey, &json!({"aac": aac, "digests": dh, "clips": ap.clips.len()}))?;
            (aac, m.frame_digests, false)
        }
    };
    tick("audio", &mut timings);

    let indexes: Vec<Value> = plans
        .iter()
        .map(|p| e.store.get_index("unit", &p.key).ok_or_else(|| anyhow!("unit {} wasn't stored", unit_name(p))))
        .collect::<Result<_>>()?;
    let mut manifest = build_manifest(e, &plans, &indexes, &digests)?;
    let (mut output, mut manifest_path) = (None, None);
    if let Some(out) = out {
        if let Some(d) = out.parent() {
            std::fs::create_dir_all(d)?;
        }
        let segs: Vec<PathBuf> =
            indexes.iter().map(|v| e.store.blob_path(v["blob"].as_str().unwrap_or_default())).collect();
        let video = e.store.tmp_path("mp4");
        ffmpeg::concat(&segs, &e.store.tmp_path("txt"), &video)?;
        ffmpeg::mux(&video, &e.store.blob_path(&aac), out)?;
        let _ = std::fs::remove_file(&video);
        manifest.video = out.file_name().map(|n| n.to_string_lossy().into_owned());
        let mp = out.with_extension("manifest.json");
        std::fs::write(&mp, serde_json::to_string_pretty(&manifest)?)?;
        output = Some(out.to_path_buf());
        manifest_path = Some(mp);
    }
    e.store.clean_tmp();
    tick("package", &mut timings);
    let reused = plans.iter().filter(|p| p.cached).count();
    Ok(RenderReport {
        output,
        manifest_path,
        units: plans.len(),
        reused,
        rendered: plans.len() - reused,
        layers_rendered: runs.len(),
        layers_reused: have.len(),
        graphics_frames: gframes.into_inner(),
        graphics_drawn: drawn.into_inner(),
        audio_reused,
        frames: plans.iter().map(|p| p.span.frames()).sum(),
        duration: round6(plans.iter().map(|p| p.span.frames()).sum::<i64>() as f64 / e.fps() as f64),
        timings,
        manifest,
    })
}

/// Per element: its box on each frame, its active frames, and its first item.
type ElementAcc = (Vec<Option<[f64; 4]>>, [i64; 2], usize);

fn tracks_of(v: &Value) -> Vec<Vec<[f64; 5]>> {
    v["boxes"]
        .as_array()
        .map(|a| {
            a.iter()
                .map(|t| {
                    t.as_array()
                        .map(|es| {
                            es.iter()
                                .filter_map(|e| {
                                    let x: Vec<f64> = e.as_array()?.iter().filter_map(Value::as_f64).collect();
                                    (x.len() == 5).then(|| [x[0], x[1], x[2], x[3], x[4]])
                                })
                                .collect()
                        })
                        .unwrap_or_default()
                })
                .collect()
        })
        .unwrap_or_default()
}

/// Per-frame boxes from change points.
fn expand(track: &[[f64; 5]], n: i64) -> Vec<Option<[f64; 4]>> {
    let mut out = vec![None; n as usize];
    let mut cur: Option<[f64; 4]> = None;
    let mut j = 0;
    for (i, slot) in out.iter_mut().enumerate() {
        while j < track.len() && track[j][0] as usize <= i {
            let b = track[j];
            cur = if b[3] > 0.0 && b[4] > 0.0 { Some([b[1], b[2], b[3], b[4]]) } else { None };
            j += 1;
        }
        *slot = cur;
    }
    out
}

fn union(a: Option<[f64; 4]>, b: Option<[f64; 4]>) -> Option<[f64; 4]> {
    match (a, b) {
        (Some(a), Some(b)) => {
            let x0 = a[0].min(b[0]);
            let y0 = a[1].min(b[1]);
            let x1 = (a[0] + a[2]).max(b[0] + b[2]);
            let y1 = (a[1] + a[3]).max(b[1] + b[3]);
            Some([x0, y0, x1 - x0, y1 - y0])
        }
        (x, None) | (None, x) => x,
    }
}

/// Change points `[t, x, y, w, h]` in composition pixels and output seconds.
fn compress(per_frame: &[Option<[f64; 4]>], f0: i64, fps: f64, scale: f64) -> Vec<[f64; 5]> {
    let r1 = |x: f64| (x / scale * 10.0).round() / 10.0;
    let mut out: Vec<[f64; 5]> = vec![];
    for (i, b) in per_frame.iter().enumerate() {
        let b = b.map(|b| [r1(b[0]), r1(b[1]), r1(b[2]), r1(b[3])]).unwrap_or([0.0; 4]);
        if out.last().is_none_or(|l| l[1..] != b) {
            out.push([round6((f0 + i as i64) as f64 / fps), b[0], b[1], b[2], b[3]]);
        }
    }
    out
}

fn build_manifest(e: &Engine, plans: &[UnitPlan], indexes: &[Value], digests: &[[u8; 32]]) -> Result<Manifest> {
    let fps = e.fps() as f64;
    let start_frame = plans.first().map(|p| p.span.f0).unwrap_or(0);
    let tl = &e.project.tl;
    let raw =
        |ptr: &str, f: &str| e.project.doc.pointer(&format!("{ptr}/{f}")).and_then(Value::as_str).map(String::from);
    let mut units = vec![];
    for (p, idx) in plans.iter().zip(indexes) {
        let (f0, f1) = (p.span.f0, p.span.f1);
        let n = f1 - f0;
        let (t0, t1) = (round6(f0 as f64 / fps), round6(f1 as f64 / fps));
        let tracks = tracks_of(idx);
        let mut elements: Vec<Element> = vec![];
        let mut order: Vec<String> = vec![];
        let mut per: BTreeMap<String, ElementAcc> = BTreeMap::new();
        for (k, (it, ow)) in p.desc.items.iter().zip(&p.owners).enumerate() {
            if ow.kind == "background" {
                continue;
            }
            let fr = tracks.get(k).map(|t| expand(t, n)).unwrap_or_else(|| vec![None; n as usize]);
            let act = it.active();
            let ent = per.entry(ow.element.clone()).or_insert_with(|| {
                order.push(ow.element.clone());
                (vec![None; n as usize], act, k)
            });
            for (i, b) in fr.iter().enumerate() {
                ent.0[i] = union(ent.0[i], *b);
            }
            ent.1 = [ent.1[0].min(act[0]), ent.1[1].max(act[1])];
            if let (Item::Video { typed, src, k0, t_out0, .. }, Some((lid, lptr))) = (it, &ow.layer) {
                let retake_timeline::layer::Content::Video(v) = &typed.content else { continue };
                let st = |i: i64| match t_out0 {
                    Some(t) => v.in_ + t + i as f64 / fps,
                    None => v.in_ + (k0 + i) as f64 / fps,
                };
                elements.push(Element {
                    id: lid.clone(),
                    ptr: lptr.clone(),
                    kind: "layer".into(),
                    z: ow.z,
                    t: [round6((f0 + act[0]) as f64 / fps), round6((f0 + act[1]) as f64 / fps)],
                    boxes: compress(&fr, f0, fps, e.canvas.scale),
                    sources: vec![Source {
                        asset: src.asset.clone(),
                        in_: round6(st(act[0])),
                        out: round6(st(act[1])),
                    }],
                    anchors: BTreeMap::new(),
                    parent: Some(ow.element.clone()),
                    use_: e.project.use_of(&ow.element).map(|u| u.reference.clone()),
                });
            }
        }
        for el in &order {
            let (fr, act, k) = &per[el];
            let ow = &p.owners[*k];
            let mut anchors = BTreeMap::new();
            match ow.kind.as_str() {
                "shot" => {
                    if let Some(a) = raw(&ow.ptr, "at") {
                        anchors.insert("at".to_string(), a);
                    }
                }
                "overlay" => {
                    for f in ["from", "to"] {
                        if let Some(a) = raw(&ow.ptr, f) {
                            anchors.insert(f.to_string(), a);
                        }
                    }
                }
                _ => {}
            }
            let sources: Vec<Source> =
                elements.iter().filter(|x| x.parent.as_deref() == Some(el)).flat_map(|x| x.sources.clone()).collect();
            elements.push(Element {
                id: el.clone(),
                ptr: ow.ptr.clone(),
                kind: ow.kind.clone(),
                z: ow.z,
                t: [round6((f0 + act[0]) as f64 / fps), round6((f0 + act[1]) as f64 / fps)],
                boxes: compress(fr, f0, fps, e.canvas.scale),
                sources,
                anchors,
                parent: None,
                use_: e.project.use_of(el).map(|u| u.reference.clone()),
            });
        }
        // Audio-only elements: spine items and audio elements that sound in this unit.
        for sp in &e.res.spine {
            let (a, b) = (sp.start.max(t0), sp.end.min(t1));
            if b <= a || (p.span.kind == UnitKind::Insert && sp.id == p.span.source) {
                continue;
            }
            elements.push(Element {
                id: sp.id.clone(),
                ptr: sp.ptr.clone(),
                kind: "spine".into(),
                z: -1,
                t: [round6(a), round6(b)],
                boxes: vec![],
                sources: vec![Source {
                    asset: sp.asset.clone(),
                    in_: round6(sp.src_in + a - sp.start),
                    out: round6(sp.src_in + b - sp.start),
                }],
                anchors: BTreeMap::new(),
                parent: None,
                use_: None,
            });
        }
        for (i, au) in e.res.audio.iter().enumerate() {
            let (a, b) = (au.from.max(t0), au.to.min(t1));
            if b <= a {
                continue;
            }
            let el = &tl.audio[i];
            elements.push(Element {
                id: au.id.clone(),
                ptr: au.ptr.clone(),
                kind: "audio".into(),
                z: -1,
                t: [round6(a), round6(b)],
                boxes: vec![],
                sources: vec![Source {
                    asset: el.asset.clone(),
                    in_: round6(el.in_ + a - au.from),
                    out: round6(el.in_ + b - au.from),
                }],
                anchors: [("from".to_string(), el.from.clone())].into_iter().collect(),
                parent: None,
                use_: None,
            });
        }
        elements.sort_by(|a, b| b.z.cmp(&a.z).then(a.kind.cmp(&b.kind)).then(a.id.cmp(&b.id)));
        let words =
            e.res.words.iter().filter(|w| w.t0 >= t0 && w.t0 < t1).map(|w| (w.t0, w.t1, w.text.clone())).collect();
        let blob = idx["blob"].as_str().unwrap_or_default();
        units.push(Unit {
            unit: unit_name(p),
            key: format!("b3:{}", p.key),
            kind: kind_name(p.span.kind).into(),
            scene: e.res.shot(&p.span.source).map(|s| s.scene.clone()),
            shot: (!p.span.source.is_empty()).then(|| p.span.source.clone()),
            t: [t0, t1],
            frames: [f0, f1],
            video_hash: Some(format!("b3:{blob}")),
            audio_hash: Some(range_hash(digests, (f0 - start_frame) as usize, (f1 - start_frame) as usize)),
            segment: Some(format!("seg/{blob}.mp4")),
            reused: Some(p.cached),
            elements,
            words,
        });
    }
    let reused = plans.iter().filter(|p| p.cached).count();
    Ok(Manifest {
        schema: manifest::SCHEMA.into(),
        timeline: TimelineRef { id: tl.id.clone(), rev: tl.rev, hash: e.timeline_hash() },
        video: None,
        size: tl.format.size,
        fps: tl.format.fps,
        duration: e.res.end,
        frames: e.res.frames,
        range: [units.first().map(|u| u.t[0]).unwrap_or(0.0), units.last().map(|u| u.t[1]).unwrap_or(0.0)],
        draft: e.opts.draft,
        units,
        stats: Some(Stats { total: plans.len(), reused, rendered: plans.len() - reused }),
    })
}

/// Renders stills at output times; returns (time, path) pairs.
pub fn stills(e: &Engine, times: &[f64], dir: &Path) -> Result<Vec<(f64, PathBuf)>> {
    std::fs::create_dir_all(dir)?;
    let mut out = vec![];
    for &t in times {
        let f = e.res.frame(t).clamp(0, (e.res.frames - 1).max(0));
        let u = e.res.unit_at(f).with_context(|| format!("no unit at {t} s"))?;
        let p = e.plan_unit(u)?;
        let faces = e.resources.faces(p.runs.iter().flat_map(|r| r.layers.iter()));
        let mut painter = Painter::new(e.resources.clone(), &faces).map_err(|x| anyhow!(x))?;
        let path = dir.join(format!("frame-{:07.3}.png", f as f64 / e.fps() as f64));
        unit::render_still(&p, f - u.f0, &mut painter, &path)?;
        out.push((t, path));
    }
    Ok(out)
}
