//! The resolver: spine layout, anchors → output seconds, compositions with local times, units in frames.
//!
//! Output time `T` is seconds from the start of the render. A composition (a shot's picture, an overlay, an insert's
//! picture) has its own clock that starts at 0 when it starts and pauses during inserts.

use crate::anchor::{self, AnchorKind};
use crate::error::{EditError, close_matches, ptr_escape};
use crate::keyframe::Prop;
use crate::layer::{Content, Layer, parse_layer};
use crate::model::{Ducks, SpineKind, Timeline, Under, gain_prop};
use crate::project::{Project, unknown_asset};
use crate::words::{self, Word};
use serde::Serialize;
use serde_json::{Value, json};
use std::collections::{BTreeMap, HashMap, HashSet};

/// Round resolved times to the microsecond so that a ripple (T + d) - (T0 + d) gives the same number as T - T0.
pub fn round6(x: f64) -> f64 {
    (x * 1e6).round() / 1e6
}

#[derive(Debug, Clone, Serialize)]
pub struct SpineSpan {
    pub id: String,
    pub ptr: String,
    pub kind: SpineKind,
    pub asset: String,
    pub start: f64,
    pub end: f64,
    pub src_in: f64,
    pub src_out: f64,
    #[serde(skip)]
    pub comp: Option<Comp>,
    /// `audio.gain_db` with key times in seconds from the item's start.
    #[serde(skip)]
    pub gain: Option<Prop>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ShotSpan {
    pub id: String,
    pub ptr: String,
    pub scene: String,
    pub start: f64,
    pub end: f64,
    #[serde(skip)]
    pub comp: Comp,
}

#[derive(Debug, Clone, Serialize)]
pub struct OverlaySpan {
    pub id: String,
    pub ptr: String,
    pub scene: String,
    pub from: f64,
    pub to: f64,
    pub z: i64,
    /// Document order among all overlays (ties on z draw in this order).
    pub order: usize,
    #[serde(skip)]
    pub comp: Comp,
}

#[derive(Debug, Clone, Serialize)]
pub struct AudioSpan {
    pub id: String,
    pub ptr: String,
    pub from: f64,
    pub to: f64,
    /// `gain_db` with key times in seconds from `from`.
    #[serde(skip)]
    pub gain: Option<Prop>,
}

#[derive(Debug, Clone, Serialize)]
pub struct SceneSpan {
    pub id: String,
    pub ptr: String,
    pub start: f64,
    pub end: f64,
}

/// A spine word placed on the output clock.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct PlacedWord {
    pub spine: String,
    pub t0: f64,
    pub t1: f64,
    pub text: String,
}

/// A composition with its anchors resolved: layer times are composition seconds.
#[derive(Debug, Clone, Serialize)]
pub struct Comp {
    pub owner: String,
    pub ptr: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub background: Option<String>,
    pub props: Value,
    /// Resolved layer JSON (what keys hash) and the typed layers, in draw order.
    pub layers_json: Vec<Value>,
    #[serde(skip)]
    pub layers: Vec<Layer>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum UnitKind {
    /// Part of a shot (shots are cut at insert edges).
    Shot,
    /// A spine insert.
    Insert,
    /// Before the first shot: the background only.
    Gap,
}

#[derive(Debug, Clone, Serialize)]
pub struct UnitSpan {
    pub index: usize,
    pub kind: UnitKind,
    /// The shot or insert id (empty for a gap).
    pub source: String,
    pub f0: i64,
    pub f1: i64,
}

impl UnitSpan {
    pub fn frames(&self) -> i64 {
        self.f1 - self.f0
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct Resolved {
    pub fps: u32,
    pub end: f64,
    pub frames: i64,
    pub spine: Vec<SpineSpan>,
    pub shots: Vec<ShotSpan>,
    pub overlays: Vec<OverlaySpan>,
    pub audio: Vec<AudioSpan>,
    pub scenes: Vec<SceneSpan>,
    pub words: Vec<PlacedWord>,
    pub units: Vec<UnitSpan>,
}

impl Resolved {
    pub fn frame(&self, t: f64) -> i64 {
        (t * self.fps as f64).round() as i64
    }

    /// Insert frame ranges, in order.
    pub fn insert_frames(&self) -> Vec<(i64, i64)> {
        self.spine
            .iter()
            .filter(|s| s.kind == SpineKind::Insert)
            .map(|s| (self.frame(s.start), self.frame(s.end)))
            .collect()
    }

    /// Frames in `[a, b)` that fall inside inserts.
    pub fn paused_frames(&self, a: i64, b: i64) -> i64 {
        if b <= a {
            return 0;
        }
        self.insert_frames().iter().map(|(s, e)| (b.min(*e) - a.max(*s)).max(0)).sum()
    }

    /// The composition-local frame at output frame `f`, for a composition that starts at frame `c0` and pauses
    /// during inserts.
    pub fn local_frame(&self, c0: i64, f: i64) -> i64 {
        if f >= c0 { (f - c0) - self.paused_frames(c0, f) } else { f - c0 }
    }

    pub fn unit_at(&self, frame: i64) -> Option<&UnitSpan> {
        self.units.iter().find(|u| frame >= u.f0 && frame < u.f1)
    }

    pub fn shot(&self, id: &str) -> Option<&ShotSpan> {
        self.shots.iter().find(|s| s.id == id)
    }

    pub fn spine_item(&self, id: &str) -> Option<&SpineSpan> {
        self.spine.iter().find(|s| s.id == id)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
enum Edge {
    Start,
    End,
}

#[derive(Debug, Clone)]
enum Elem {
    Spine(usize),
    Scene(usize),
    Shot(usize, usize),
    Overlay(usize, usize),
    Audio(usize),
    Layer,
}

/// Every object with an `id` inside a list, as (id, JSON pointer).
pub fn collect_ids(doc: &Value) -> Vec<(String, String)> {
    fn walk(v: &Value, ptr: &str, in_list: bool, out: &mut Vec<(String, String)>) {
        match v {
            Value::Object(m) => {
                if in_list && let Some(Value::String(id)) = m.get("id") {
                    out.push((id.clone(), ptr.to_string()));
                }
                for (k, c) in m {
                    if k == "meta" || k == "props" {
                        continue;
                    }
                    walk(c, &format!("{ptr}/{}", ptr_escape(k)), false, out);
                }
            }
            Value::Array(a) => {
                for (i, c) in a.iter().enumerate() {
                    walk(c, &format!("{ptr}/{i}"), true, out);
                }
            }
            _ => {}
        }
    }
    let mut out = vec![];
    walk(doc, "", false, &mut out);
    out
}

struct Resolver<'a> {
    p: &'a Project,
    tl: &'a Timeline,
    elems: HashMap<String, Elem>,
    memo: HashMap<(String, Edge), Result<f64, EditError>>,
    visiting: Vec<(String, Edge)>,
    words: HashMap<String, Result<Vec<Word>, EditError>>,
    spine: Vec<SpineSpan>,
    /// Shots in document order across scenes.
    shot_order: Vec<(usize, usize)>,
    end: Option<Result<f64, EditError>>,
    inserts: Vec<(f64, f64)>,
    /// The `expr_lib` of the composition being resolved (the timeline's, then its own).
    lib: String,
}

/// Wraps every expression of a resolved layer in its library: `(() => { lib; return (expr); })()`. A group's
/// children were wrapped when they were resolved; `meta` and `props` aren't expressions.
pub fn wrap_exprs(v: &mut Value, lib: &str) {
    match v {
        Value::Object(m) => {
            for (k, c) in m.iter_mut() {
                match (k.as_str(), &mut *c) {
                    ("layers" | "meta" | "props", _) => {}
                    ("expr", Value::String(e)) => *e = format!("(() => {{\n{lib}\nreturn ({e}\n);\n}})()"),
                    _ => wrap_exprs(c, lib),
                }
            }
        }
        Value::Array(a) => {
            for c in a {
                wrap_exprs(c, lib);
            }
        }
        _ => {}
    }
}

/// Track mattes name a layer of the same list that isn't the layer itself and has no matte of its own.
fn check_mattes(layers: &[Layer], ptrs: &[String], errs: &mut Vec<EditError>) {
    for (l, ptr) in layers.iter().zip(ptrs) {
        if let Some(m) = &l.matte {
            match layers.iter().find(|x| x.id == m.layer) {
                None => errs.push(
                    EditError::new("matte_unknown", format!("layer {}: no layer `{}` in the same list", l.id, m.layer))
                        .at(format!("{ptr}/matte/layer"))
                        .suggest(close_matches(&m.layer, layers.iter().map(|x| x.id.as_str()), 3))
                        .hint("a track matte is another layer of the same composition or group"),
                ),
                Some(x) if x.id == l.id => errs.push(
                    EditError::new("matte_self", format!("layer {} can't be its own matte", l.id))
                        .at(format!("{ptr}/matte/layer")),
                ),
                Some(x) if x.matte.is_some() => errs.push(
                    EditError::new("matte_chain", format!("matte layer {} has a matte of its own", x.id))
                        .at(format!("{ptr}/matte/layer"))
                        .hint("a matte layer can't be matted; group it instead"),
                ),
                Some(_) => {}
            }
        }
        if let Content::Group(g) = &l.content {
            let cp: Vec<String> = (0..g.layers.len()).map(|i| format!("{ptr}/layers/{i}")).collect();
            check_mattes(&g.layers, &cp, errs);
        }
    }
}

impl<'a> Resolver<'a> {
    fn new(p: &'a Project) -> Self {
        let tl = &p.tl;
        let mut elems = HashMap::new();
        for (i, s) in tl.spine.iter().enumerate() {
            elems.insert(s.id.clone(), Elem::Spine(i));
        }
        let mut shot_order = vec![];
        for (si, sc) in tl.scenes.iter().enumerate() {
            elems.insert(sc.id.clone(), Elem::Scene(si));
            for (ki, sh) in sc.shots.iter().enumerate() {
                elems.insert(sh.id.clone(), Elem::Shot(si, ki));
                shot_order.push((si, ki));
            }
            for (oi, ov) in sc.overlays.iter().enumerate() {
                elems.insert(ov.id.clone(), Elem::Overlay(si, oi));
            }
        }
        for (i, a) in tl.audio.iter().enumerate() {
            elems.insert(a.id.clone(), Elem::Audio(i));
        }
        for (id, _) in collect_ids(&p.doc) {
            elems.entry(id).or_insert(Elem::Layer);
        }
        // Spine layout: items end to end.
        let mut t = 0.0;
        let mut spine = vec![];
        let mut inserts = vec![];
        for (i, s) in tl.spine.iter().enumerate() {
            t += s.gap_before.unwrap_or(0.0);
            let d = s.duration(tl.format.fps);
            let span = SpineSpan {
                id: s.id.clone(),
                ptr: format!("/spine/{i}"),
                kind: s.kind,
                asset: s.asset.clone(),
                start: round6(t),
                end: round6(t + d),
                src_in: s.in_,
                src_out: s.in_ + d,
                comp: None,
                gain: None,
            };
            if s.kind == SpineKind::Insert {
                inserts.push((span.start, span.end));
            }
            t += d;
            spine.push(span);
        }
        Resolver {
            p,
            tl,
            elems,
            memo: HashMap::new(),
            visiting: vec![],
            words: HashMap::new(),
            spine,
            shot_order,
            end: None,
            inserts,
            lib: String::new(),
        }
    }

    fn timeline_end(&mut self) -> Result<f64, EditError> {
        if let Some(r) = &self.end {
            return r.clone();
        }
        let r = match self.tl.end.clone() {
            Some(a) => {
                if self.visiting.iter().any(|(id, _)| id == "$end") {
                    Err(EditError::new("anchor_cycle", "the timeline end depends on itself").at("/end"))
                } else {
                    self.visiting.push(("$end".into(), Edge::End));
                    let r = self.anchor(&a, "/end");
                    self.visiting.pop();
                    r
                }
            }
            None => Ok(self.spine.last().map(|s| s.end).unwrap_or(0.0)),
        };
        self.end = Some(r.clone());
        r
    }

    fn words_for(&mut self, asset: &str) -> Result<Vec<Word>, EditError> {
        if let Some(w) = self.words.get(asset) {
            return w.clone();
        }
        let r = (|| {
            let a = self.tl.assets.get(asset).ok_or_else(|| unknown_asset(self.p, asset))?;
            let wpath = a.words.as_ref().ok_or_else(|| {
                EditError::new("words_missing", format!("asset `{asset}` has no words file"))
                    .hint(format!("add \"words\": \"<transcript.json>\" to assets.{asset}"))
            })?;
            let path = self.p.resolve_path(wpath)?;
            let media = self.p.resolve_path(&a.path)?;
            let name = media.file_name().map(|n| n.to_string_lossy().into_owned()).unwrap_or_default();
            words::load_words(&path, &name)
        })();
        self.words.insert(asset.to_string(), r.clone());
        r
    }

    fn edge(&mut self, id: &str, e: Edge) -> Result<f64, EditError> {
        let key = (id.to_string(), e);
        if let Some(r) = self.memo.get(&key) {
            return r.clone();
        }
        if self.visiting.contains(&key) {
            let chain: Vec<String> = self
                .visiting
                .iter()
                .map(|(i, e)| format!("{}:{i}", if *e == Edge::Start { "start" } else { "end" }))
                .collect();
            return Err(EditError::new("anchor_cycle", format!("anchors form a cycle: {} → {id}", chain.join(" → ")))
                .hint("anchor one of these to a word or a spine item instead"));
        }
        self.visiting.push(key.clone());
        let r = self.edge_inner(id, e);
        self.visiting.pop();
        self.memo.insert(key, r.clone());
        r
    }

    fn edge_inner(&mut self, id: &str, e: Edge) -> Result<f64, EditError> {
        let elem = self.elems.get(id).cloned().ok_or_else(|| {
            EditError::new("anchor_unknown_id", format!("no element with id `{id}`")).suggest(close_matches(
                id,
                self.elems.keys().map(String::as_str),
                3,
            ))
        })?;
        match elem {
            Elem::Spine(i) => Ok(if e == Edge::Start { self.spine[i].start } else { self.spine[i].end }),
            Elem::Shot(si, ki) => match e {
                Edge::Start => {
                    let at = self.tl.scenes[si].shots[ki].at.clone();
                    let t = self.anchor(&at, &format!("/scenes/{si}/shots/{ki}/at"))?;
                    Ok(t.max(0.0))
                }
                Edge::End => {
                    let pos = self.shot_order.iter().position(|x| *x == (si, ki)).unwrap_or(0);
                    match self.shot_order.get(pos + 1).copied() {
                        Some((s2, k2)) => {
                            let next = self.tl.scenes[s2].shots[k2].id.clone();
                            self.edge(&next, Edge::Start)
                        }
                        None => self.timeline_end(),
                    }
                }
            },
            Elem::Overlay(si, oi) => {
                let o = &self.tl.scenes[si].overlays[oi];
                let (a, field) = if e == Edge::Start { (o.from.clone(), "from") } else { (o.to.clone(), "to") };
                self.anchor(&a, &format!("/scenes/{si}/overlays/{oi}/{field}"))
            }
            Elem::Audio(i) => {
                let a = &self.tl.audio[i];
                match (e, a.to.clone()) {
                    (Edge::Start, _) => {
                        let from = a.from.clone();
                        self.anchor(&from, &format!("/audio/{i}/from"))
                    }
                    (Edge::End, Some(to)) => self.anchor(&to, &format!("/audio/{i}/to")),
                    (Edge::End, None) => self.timeline_end(),
                }
            }
            Elem::Scene(si) => {
                let sc = &self.tl.scenes[si];
                let shot = if e == Edge::Start { sc.shots.first() } else { sc.shots.last() };
                let shot = shot.map(|s| s.id.clone()).ok_or_else(|| {
                    EditError::new("anchor_empty_scene", format!("scene `{id}` has no shots, so it has no edges"))
                })?;
                self.edge(&shot, e)
            }
            Elem::Layer => Err(EditError::new(
                "anchor_unsupported",
                format!("`{id}` is a layer; anchors can name spine items, scenes, shots, overlays and audio"),
            )),
        }
    }

    /// Resolves an anchor to output seconds. `ptr` locates it for errors.
    fn anchor(&mut self, raw: &str, ptr: &str) -> Result<f64, EditError> {
        let elems = &self.elems;
        let a = anchor::parse(raw, &|id| elems.contains_key(id)).map_err(|e| e.at(ptr))?;
        let base = match &a.kind {
            AnchorKind::T { t } => Ok(*t),
            AnchorKind::Start { id } => self.edge(id, Edge::Start),
            AnchorKind::End { id } => self.edge(id, Edge::End),
            AnchorKind::Word { spine, word, n, end } => self.word(raw, spine, word, *n, *end),
        };
        base.map(|t| round6(t + a.offset)).map_err(|e| e.at(ptr))
    }

    fn word(&mut self, raw: &str, spine_id: &str, query: &str, n: usize, end: bool) -> Result<f64, EditError> {
        let idx = match self.elems.get(spine_id) {
            Some(Elem::Spine(i)) => *i,
            Some(_) => {
                return Err(EditError::new(
                    "anchor_not_spine",
                    format!("anchor {raw}: `{spine_id}` isn't a spine item; word anchors name a spine item"),
                ));
            }
            None => {
                let ids: Vec<&str> = self.tl.spine.iter().map(|s| s.id.as_str()).collect();
                return Err(EditError::new("anchor_unknown_id", format!("anchor {raw}: no spine item `{spine_id}`"))
                    .suggest(close_matches(spine_id, ids, 3)));
            }
        };
        let span = self.spine[idx].clone();
        let all = self.words_for(&span.asset)?;
        let inside: Vec<&Word> = all.iter().filter(|w| w.mid() >= span.src_in && w.mid() < span.src_out).collect();
        let hits: Vec<&&Word> = inside.iter().filter(|w| words::matches(query, &w.text)).collect();
        if let Some(w) = hits.get(n - 1) {
            let src = if end { w.e } else { w.s };
            return Ok(span.start + (src - span.src_in));
        }
        if !hits.is_empty() {
            return Err(EditError::new(
                "anchor_word_occurrence",
                format!("anchor {raw}: spine item {spine_id} has only {} '{query}'", hits.len()),
            )
            .hint(format!("use #1 to #{}", hits.len())));
        }
        // Suggest the closest words in this item; mention other items that have it.
        let q = words::norm(query.trim_end_matches('*'));
        let mut scored: Vec<(f64, &str)> = inside
            .iter()
            .map(|w| (strsim::normalized_damerau_levenshtein(&q, &words::norm(&w.text)), w.text.as_str()))
            .filter(|(s, _)| *s >= 0.4)
            .collect();
        scored.sort_by(|a, b| b.0.total_cmp(&a.0));
        let mut sugg: Vec<String> = vec![];
        for (_, t) in scored {
            if !sugg.iter().any(|s| s == t) {
                sugg.push(t.to_string());
            }
            if sugg.len() == 3 {
                break;
            }
        }
        let elsewhere: Vec<String> = self
            .spine
            .iter()
            .filter(|s| s.id != spine_id && s.asset == span.asset)
            .filter(|s| {
                all.iter().any(|w| w.mid() >= s.src_in && w.mid() < s.src_out && words::matches(query, &w.text))
            })
            .map(|s| s.id.clone())
            .collect();
        let mut err = EditError::new(
            "anchor_word_not_found",
            format!("anchor {raw}: word '{query}' not found in spine item {spine_id}"),
        );
        if let Some(best) = sugg.first() {
            err = err.hint(format!("did you mean '{best}'?"));
        } else if !elsewhere.is_empty() {
            err = err.hint(format!("it is in spine item {}", elsewhere.join(", ")));
        }
        Err(err.suggest(sugg))
    }

    fn inserts_between(&self, a: f64, b: f64) -> f64 {
        self.inserts.iter().map(|(s, e)| (b.min(*e) - a.max(*s)).max(0.0)).sum()
    }

    /// Output time → composition seconds for a composition starting at `t0`.
    fn local(&self, t0: f64, t: f64, pausing: bool) -> f64 {
        if !pausing {
            return round6(t - t0);
        }
        let paused = if t >= t0 { self.inserts_between(t0, t) } else { -self.inserts_between(t, t0) };
        round6(t - t0 - paused)
    }

    /// A time inside a composition: seconds as is, an anchor converted to composition seconds.
    fn comp_time(&mut self, v: &Value, ptr: &str, t0: f64, pausing: bool) -> Result<Value, EditError> {
        match v {
            Value::String(a) => {
                let t = self.anchor(a, ptr)?;
                Ok(json!(self.local(t0, t, pausing)))
            }
            other => Ok(other.clone()),
        }
    }

    fn resolve_layer(
        &mut self,
        layer: &Value,
        ptr: &str,
        t0: f64,
        pausing: bool,
        errs: &mut Vec<EditError>,
    ) -> Option<(Value, Layer)> {
        let mut l = layer.clone();
        let obj = l.as_object_mut()?;
        obj.remove("meta");
        let before = errs.len();
        for f in ["from", "to"] {
            if let Some(v) = obj.get(f).cloned() {
                match self.comp_time(&v, &format!("{ptr}/{f}"), t0, pausing) {
                    Ok(nv) => {
                        obj.insert(f.into(), nv);
                    }
                    Err(e) => errs.push(e),
                }
            }
        }
        // (field path inside the layer, expected component counts)
        let props: [(&[&str], &[usize]); 15] = [
            (&["transform", "position"], &[2]),
            (&["transform", "scale"], &[1, 2]),
            (&["transform", "rotation"], &[1]),
            (&["transform", "rotation_x"], &[1]),
            (&["transform", "rotation_y"], &[1]),
            (&["transform", "perspective"], &[1]),
            (&["transform", "corners"], &[8]),
            (&["transform", "opacity"], &[1]),
            (&["crop"], &[4]),
            (&["blur"], &[1]),
            (&["highlight", "words"], &[2]),
            (&["mask", "rect"], &[4]),
            (&["mask", "radius"], &[1]),
            (&["mask", "feather"], &[1]),
            (&["audio", "gain_db"], &[1]),
        ];
        for (path, dims) in props {
            let pptr = format!("{ptr}/{}", path.join("/"));
            let prop = if path.len() == 1 {
                obj.get_mut(path[0])
            } else {
                obj.get_mut(path[0]).and_then(Value::as_object_mut).and_then(|t| t.get_mut(path[1]))
            };
            if let Some(prop) = prop {
                self.resolve_prop(prop, &pptr, dims, t0, pausing, errs);
            }
        }
        if obj.get("type").and_then(Value::as_str) == Some("video")
            && let Some(sync) = obj.remove("sync")
        {
            let src = sync.get("src").and_then(Value::as_f64).unwrap_or(0.0);
            let to = sync.get("to").and_then(Value::as_str).unwrap_or("");
            match self.anchor(to, &format!("{ptr}/sync/to")) {
                Ok(t) => {
                    let local = self.local(t0, t, pausing);
                    obj.insert("in".into(), json!(round6(src - local)));
                }
                Err(e) => errs.push(e),
            }
        }
        if let Some(Value::Object(tr)) = obj.get("transform")
            && tr.contains_key("corners")
        {
            let clash: Vec<&str> = ["anchor", "position", "scale", "rotation", "rotation_x", "rotation_y", "perspective"]
                .into_iter()
                .filter(|k| tr.contains_key(*k))
                .collect();
            if !clash.is_empty() {
                errs.push(
                    EditError::new(
                        "transform_conflict",
                        format!("`corners` places the layer by itself; it can't be combined with {}", clash.join(", ")),
                    )
                    .at(format!("{ptr}/transform"))
                    .hint("remove those props, or drop `corners` and use position, scale and rotation_x / rotation_y"),
                );
            }
        }
        // A group's children: the same clock, the same rules.
        if obj.get("type").and_then(Value::as_str) == Some("group")
            && let Some(Value::Array(children)) = obj.get("layers").cloned()
        {
            let mut out = vec![];
            for (i, c) in children.iter().enumerate() {
                if let Some((j, _)) = self.resolve_layer(c, &format!("{ptr}/layers/{i}"), t0, pausing, errs) {
                    out.push(j);
                }
            }
            obj.insert("layers".into(), Value::Array(out));
        }
        if errs.len() > before {
            return None;
        }
        if !self.lib.is_empty() {
            wrap_exprs(&mut l, &self.lib);
        }
        match parse_layer(&l) {
            Ok(typed) => {
                self.check_layer_refs(&typed, ptr, errs);
                Some((l, typed))
            }
            Err(e) => {
                errs.push(EditError::new("layer_invalid", format!("layer doesn't fit the model: {e}")).at(ptr));
                None
            }
        }
    }

    fn resolve_prop(
        &mut self,
        prop: &mut Value,
        ptr: &str,
        dims: &[usize],
        t0: f64,
        pausing: bool,
        errs: &mut Vec<EditError>,
    ) {
        let check_dims = |v: &Value, at: &str, errs: &mut Vec<EditError>| {
            let n = match v {
                Value::Number(_) => 1,
                Value::Array(a) => a.len(),
                _ => return,
            };
            if !dims.contains(&n) {
                let want: Vec<String> = dims.iter().map(|d| d.to_string()).collect();
                errs.push(
                    EditError::new("prop_shape", format!("this prop takes {} number(s), got {n}", want.join(" or ")))
                        .at(at),
                );
            }
        };
        match prop {
            Value::Object(m) => {
                if let Some(v) = m.get("value") {
                    check_dims(v, &format!("{ptr}/value"), errs);
                }
                if let Some(Value::Array(keys)) = m.get_mut("keys") {
                    let mut prev = f64::NEG_INFINITY;
                    for (i, k) in keys.iter_mut().enumerate() {
                        let kp = format!("{ptr}/keys/{i}");
                        if let Some(v) = k.get("v") {
                            check_dims(v, &format!("{kp}/v"), errs);
                        }
                        if let Some(t) = k.get("t").cloned() {
                            match self.comp_time(&t, &format!("{kp}/t"), t0, pausing) {
                                Ok(nt) => {
                                    let tv = nt.as_f64().unwrap_or(0.0);
                                    if tv < prev {
                                        errs.push(
                                            EditError::new(
                                                "keys_order",
                                                format!(
                                                    "key {i} at {tv:.3} s comes before the previous key ({prev:.3} s)"
                                                ),
                                            )
                                            .at(format!("{kp}/t"))
                                            .hint("keys must be in time order"),
                                        );
                                    }
                                    prev = tv;
                                    k["t"] = nt;
                                }
                                Err(e) => errs.push(e),
                            }
                        }
                    }
                    let lens: HashSet<usize> = keys
                        .iter()
                        .filter_map(|k| k.get("v"))
                        .map(|v| v.as_array().map(|a| a.len()).unwrap_or(1))
                        .collect();
                    if lens.len() > 1 {
                        errs.push(
                            EditError::new("prop_shape", "keys mix values of different lengths")
                                .at(format!("{ptr}/keys")),
                        );
                    }
                }
            }
            v => check_dims(v, ptr, errs),
        }
    }

    /// A `gain_db` (number or prop) with anchors in its keys turned into seconds from `t0` (no pause for inserts:
    /// sound elements run through them).
    fn gain(&mut self, v: Option<&Value>, ptr: &str, t0: f64, errs: &mut Vec<EditError>) -> Option<Prop> {
        let mut v = v?.clone();
        let before = errs.len();
        self.resolve_prop(&mut v, ptr, &[1], t0, false, errs);
        if errs.len() > before {
            return None;
        }
        match gain_prop(Some(&v)) {
            Ok(p) => p,
            Err(e) => {
                errs.push(EditError::new("prop_shape", e).at(ptr));
                None
            }
        }
    }

    /// `under` must be "vo", "inserts" or spine item ids.
    fn check_duck(&self, d: &Ducks, ptr: &str, errs: &mut Vec<EditError>) {
        let rules = d.rules();
        for (k, r) in rules.iter().enumerate() {
            let rp = if matches!(d, Ducks::Many(_)) { format!("{ptr}/{k}/under") } else { format!("{ptr}/under") };
            match &r.under {
                Under::Named(n) if n == "vo" || n == "inserts" => {}
                Under::Named(n) => errs.push(
                    EditError::new("duck_under", format!("duck under `{n}`: use \"vo\", \"inserts\" or spine ids"))
                        .at(rp),
                ),
                Under::Ids(ids) => {
                    for id in ids {
                        if !self.tl.spine.iter().any(|s| &s.id == id) {
                            let all: Vec<&str> = self.tl.spine.iter().map(|s| s.id.as_str()).collect();
                            errs.push(
                                EditError::new("duck_under", format!("duck under `{id}`: no spine item has that id"))
                                    .at(rp.clone())
                                    .suggest(close_matches(id, all, 3))
                                    .hint("duck under spine item ids, \"inserts\" or \"vo\""),
                            );
                        }
                    }
                }
            }
        }
    }

    fn check_layer_refs(&self, l: &Layer, ptr: &str, errs: &mut Vec<EditError>) {
        match &l.content {
            Content::Video(v) => {
                if !self.tl.assets.contains_key(&v.asset) {
                    errs.push(unknown_asset(self.p, &v.asset).at(format!("{ptr}/asset")));
                }
                if let Some(a) = &v.audio {
                    if let Some(d) = &a.duck {
                        self.check_duck(d, &format!("{ptr}/audio/duck"), errs);
                    }
                    if let Err(e) = gain_prop(a.gain_db.as_ref()) {
                        errs.push(EditError::new("prop_shape", e).at(format!("{ptr}/audio/gain_db")));
                    }
                }
            }
            Content::Image(i) => {
                if !self.tl.assets.contains_key(&i.asset) {
                    errs.push(unknown_asset(self.p, &i.asset).at(format!("{ptr}/asset")));
                }
            }
            Content::Text(t) => {
                let f = t.font_name();
                if f != "display" && !self.tl.fonts.contains_key(f) {
                    let mut names: Vec<&str> = self.tl.fonts.keys().map(String::as_str).collect();
                    names.push("display");
                    errs.push(
                        EditError::new("font_unknown", format!("font `{f}` isn't defined"))
                            .at(format!("{ptr}/font"))
                            .suggest(close_matches(f, names, 3))
                            .hint("define it under \"fonts\" or use \"display\""),
                    );
                }
            }
            Content::Shape(_) | Content::Raster(_) => {}
            Content::Group(g) => {
                for (i, c) in g.layers.iter().enumerate() {
                    self.check_layer_refs(c, &format!("{ptr}/layers/{i}"), errs);
                }
            }
        }
    }

    /// Resolves a composition: `pic` is any object with `layers` (and maybe `background`, `props`) found at `ptr`.
    fn comp(&mut self, owner: &str, ptr: &str, pic: &Value, t0: f64, pausing: bool, errs: &mut Vec<EditError>) -> Comp {
        let background = pic.get("background").and_then(Value::as_str).map(String::from);
        if let Some(bg) = &background
            && crate::model::Rgba::parse(bg).is_none()
        {
            errs.push(EditError::new("color", format!("bad color {bg}")).at(format!("{ptr}/background")));
        }
        let layers = pic.get("layers").and_then(Value::as_array).cloned().unwrap_or_default();
        let libs: Vec<&str> =
            [self.tl.expr_lib.as_deref(), pic.get("expr_lib").and_then(Value::as_str)].into_iter().flatten().collect();
        self.lib = libs.join("\n");
        let mut lj = vec![];
        let mut lt = vec![];
        let mut ptrs = vec![];
        for (i, l) in layers.iter().enumerate() {
            let lp = format!("{ptr}/layers/{i}");
            if let Some((j, t)) = self.resolve_layer(l, &lp, t0, pausing, errs) {
                lj.push(j);
                lt.push(t);
                ptrs.push(lp);
            }
        }
        self.lib.clear();
        check_mattes(&lt, &ptrs, errs);
        let mut props = pic.get("props").cloned().unwrap_or_else(|| json!({}));
        if let Some(m) = props.as_object_mut() {
            for (k, v) in m.iter_mut() {
                if let Some(spine) = v.as_str().and_then(|s| s.strip_prefix("$words:")).map(String::from) {
                    match self.words_prop(&spine, t0, pausing) {
                        Ok(w) => *v = w,
                        Err(e) => errs.push(e.at(format!("{ptr}/props/{}", ptr_escape(k)))),
                    }
                }
            }
        }
        Comp { owner: owner.into(), ptr: ptr.into(), background, props, layers_json: lj, layers: lt }
    }

    /// A spine item's words on a composition's clock: `[{"w", "s", "e"}]` (spec/components.md, "Words").
    fn words_prop(&mut self, spine_id: &str, t0: f64, pausing: bool) -> Result<Value, EditError> {
        let span = match self.elems.get(spine_id) {
            Some(Elem::Spine(i)) => self.spine[*i].clone(),
            _ => {
                let ids: Vec<&str> = self.tl.spine.iter().map(|s| s.id.as_str()).collect();
                return Err(EditError::new(
                    "words_spine_unknown",
                    format!("$words:{spine_id}: no spine item `{spine_id}`"),
                )
                .suggest(close_matches(spine_id, ids, 3)));
            }
        };
        let all = self.words_for(&span.asset)?;
        let out: Vec<Value> = all
            .iter()
            .filter(|w| w.mid() >= span.src_in && w.mid() < span.src_out)
            .map(|w| {
                let s = self.local(t0, span.start + (w.s - span.src_in), pausing);
                let e = self.local(t0, span.start + (w.e - span.src_in), pausing);
                json!({"w": w.text, "s": s, "e": e})
            })
            .collect();
        Ok(Value::Array(out))
    }
}

/// Semantic checks that don't need times: unique ids and asset references.
pub fn check_refs(p: &Project) -> Vec<EditError> {
    let mut errs = vec![];
    let mut seen: BTreeMap<String, String> = BTreeMap::new();
    for (id, ptr) in collect_ids(&p.doc) {
        if let Some(first) = seen.get(&id) {
            errs.push(
                EditError::new("duplicate_id", format!("id `{id}` is used twice (first at {first})"))
                    .at(ptr)
                    .hint("ids are unique across the whole timeline"),
            );
        } else {
            seen.insert(id, ptr);
        }
    }
    for (i, s) in p.tl.spine.iter().enumerate() {
        if !p.tl.assets.contains_key(&s.asset) {
            errs.push(unknown_asset(p, &s.asset).at(format!("/spine/{i}/asset")));
        }
        if let Some(o) = s.out
            && o <= s.in_
        {
            errs.push(
                EditError::new("empty_range", format!("spine item {} ends ({o}) before it starts ({})", s.id, s.in_))
                    .at(format!("/spine/{i}/out")),
            );
        }
        if let Some(h) = s.hold_from
            && s.kind != SpineKind::Insert
        {
            errs.push(
                EditError::new("hold_on_vo", format!("hold_from {h} only applies to inserts"))
                    .at(format!("/spine/{i}")),
            );
        }
    }
    for (i, a) in p.tl.audio.iter().enumerate() {
        if !p.tl.assets.contains_key(&a.asset) {
            errs.push(unknown_asset(p, &a.asset).at(format!("/audio/{i}/asset")));
        }
    }
    for (name, a) in &p.tl.assets {
        match p.resolve_path(&a.path) {
            Ok(path) if !path.exists() => errs.push(
                EditError::new("asset_missing", format!("asset `{name}`: {} doesn't exist", path.display()))
                    .at(format!("/assets/{}", ptr_escape(name))),
            ),
            Ok(_) => {}
            Err(e) => errs.push(e.at(format!("/assets/{}", ptr_escape(name)))),
        }
    }
    for (name, f) in &p.tl.fonts {
        match p.resolve_path(&f.path) {
            Ok(path) if !path.exists() => errs.push(
                EditError::new("font_missing", format!("font `{name}`: {} doesn't exist", path.display()))
                    .at(format!("/fonts/{name}")),
            ),
            Ok(_) => {}
            Err(e) => errs.push(e.at(format!("/fonts/{name}"))),
        }
    }
    if let Some(bg) = &p.tl.format.background
        && crate::model::Rgba::parse(bg).is_none()
    {
        errs.push(EditError::new("color", format!("bad color {bg}")).at("/format/background"));
    }
    let f = &p.tl.format;
    if !f.sample_rate.is_multiple_of(f.fps) {
        errs.push(
            EditError::new(
                "format_rates",
                format!("sample_rate {} isn't a whole number of samples per frame at {} fps", f.sample_rate, f.fps),
            )
            .at("/format/sample_rate")
            .hint("use 48000, which divides by 24, 25, 30, 50, 60 and 120"),
        );
    }
    errs
}

/// Resolves every time in the timeline. Returns everything it could resolve plus all the errors it found.
pub fn resolve(p: &Project) -> Result<Resolved, Vec<EditError>> {
    let mut errs = vec![];
    let mut r = Resolver::new(p);
    let tl = &p.tl;
    let end = match r.timeline_end() {
        Ok(e) => e,
        Err(e) => {
            errs.push(e);
            0.0
        }
    };
    if end <= 0.0 && errs.is_empty() {
        errs.push(EditError::new("empty_timeline", "the timeline ends at 0 s").at("/end"));
    }

    // Shots, in document order.
    let mut shots = vec![];
    let mut prev: Option<(f64, String)> = None;
    for &(si, ki) in &r.shot_order.clone() {
        let sh = &tl.scenes[si].shots[ki];
        let ptr = format!("/scenes/{si}/shots/{ki}");
        let (s, e) = match (r.edge(&sh.id, Edge::Start), r.edge(&sh.id, Edge::End)) {
            (Ok(s), Ok(e)) => (s.min(end), e.min(end)),
            (a, b) => {
                errs.extend(a.err());
                errs.extend(b.err());
                continue;
            }
        };
        if let Some((ps, pid)) = &prev
            && s < *ps
        {
            errs.push(
                EditError::new(
                    "shot_order",
                    format!("shot {} starts at {s:.3} s, before the previous shot {pid} ({ps:.3} s)", sh.id),
                )
                .at(format!("{ptr}/at"))
                .hint("shots run in document order; move the shot or fix its anchor"),
            );
        }
        prev = Some((s, sh.id.clone()));
        let comp = r.comp(&sh.id, &format!("{ptr}/picture"), &sh.picture, s, true, &mut errs);
        shots.push(ShotSpan { id: sh.id.clone(), ptr, scene: tl.scenes[si].id.clone(), start: s, end: e.max(s), comp });
    }

    // Overlays.
    let mut overlays = vec![];
    let mut order = 0;
    for (si, sc) in tl.scenes.iter().enumerate() {
        for (oi, o) in sc.overlays.iter().enumerate() {
            let ptr = format!("/scenes/{si}/overlays/{oi}");
            order += 1;
            let (from, to) = match (r.edge(&o.id, Edge::Start), r.edge(&o.id, Edge::End)) {
                (Ok(a), Ok(b)) => (a, b),
                (a, b) => {
                    errs.extend(a.err());
                    errs.extend(b.err());
                    continue;
                }
            };
            if to <= from {
                errs.push(
                    EditError::new(
                        "empty_range",
                        format!("overlay {} ends ({to:.3} s) before it starts ({from:.3} s)", o.id),
                    )
                    .at(format!("{ptr}/to")),
                );
                continue;
            }
            let raw = p.doc.pointer(&ptr).cloned().unwrap_or(Value::Null);
            let comp = r.comp(&o.id, &ptr, &raw, from, true, &mut errs);
            overlays.push(OverlaySpan { id: o.id.clone(), ptr, scene: sc.id.clone(), from, to, z: o.z, order, comp });
        }
    }

    // Audio elements.
    let mut audio = vec![];
    for (i, a) in tl.audio.iter().enumerate() {
        match (r.edge(&a.id, Edge::Start), r.edge(&a.id, Edge::End)) {
            (Ok(f), Ok(t)) => {
                if t <= f {
                    errs.push(
                        EditError::new(
                            "empty_range",
                            format!("audio {} ends ({t:.3} s) before it starts ({f:.3} s)", a.id),
                        )
                        .at(format!("/audio/{i}")),
                    );
                } else {
                    let gain = r.gain(a.gain_db.as_ref(), &format!("/audio/{i}/gain_db"), f, &mut errs);
                    if let Some(d) = &a.duck {
                        r.check_duck(d, &format!("/audio/{i}/duck"), &mut errs);
                    }
                    audio.push(AudioSpan { id: a.id.clone(), ptr: format!("/audio/{i}"), from: f, to: t, gain });
                }
            }
            (x, y) => {
                errs.extend(x.err());
                errs.extend(y.err());
            }
        }
    }

    // Scenes.
    let mut scenes = vec![];
    for (si, sc) in tl.scenes.iter().enumerate() {
        if let (Some(f), Some(l)) = (sc.shots.first(), sc.shots.last())
            && let (Ok(s), Ok(e)) = (r.edge(&f.id, Edge::Start), r.edge(&l.id, Edge::End))
        {
            scenes.push(SceneSpan { id: sc.id.clone(), ptr: format!("/scenes/{si}"), start: s, end: e });
        }
    }

    // Insert pictures, and spine items' gain keys.
    let mut spine = r.spine.clone();
    for (i, s) in tl.spine.iter().enumerate() {
        if let Some(a) = &s.audio {
            spine[i].gain = r.gain(a.gain_db.as_ref(), &format!("/spine/{i}/audio/gain_db"), spine[i].start, &mut errs);
        }
        if s.kind != SpineKind::Insert {
            continue;
        }
        let ptr = format!("/spine/{i}/picture");
        let pic = s.picture.clone().unwrap_or_else(|| {
            let mut l = json!({"id": format!("{}/picture", s.id), "type": "video", "asset": s.asset, "in": s.in_});
            if let Some(h) = s.hold_from {
                l["hold_from"] = json!(h);
            }
            json!({"layers": [l]})
        });
        let t0 = spine[i].start;
        spine[i].comp = Some(r.comp(&s.id, &ptr, &pic, t0, false, &mut errs));
    }

    // Words on the output clock (only for assets that declare words).
    let mut placed = vec![];
    for s in &spine {
        let has_words = tl.assets.get(&s.asset).is_some_and(|a| a.words.is_some());
        if !has_words || s.kind == SpineKind::Insert {
            continue;
        }
        match r.words_for(&s.asset) {
            Ok(ws) => {
                for w in ws.iter().filter(|w| w.mid() >= s.src_in && w.mid() < s.src_out) {
                    placed.push(PlacedWord {
                        spine: s.id.clone(),
                        t0: round6(s.start + w.s - s.src_in),
                        t1: round6(s.start + w.e - s.src_in),
                        text: w.text.clone(),
                    });
                }
            }
            Err(e) => errs.push(e.at(format!("{}/asset", s.ptr))),
        }
    }

    errs.sort_by(|a, b| a.path.cmp(&b.path).then(a.code.cmp(&b.code)));
    errs.dedup();
    if !errs.is_empty() {
        return Err(errs);
    }
    let mut res = Resolved {
        fps: tl.format.fps,
        end,
        frames: (end * tl.format.fps as f64).round() as i64,
        spine,
        shots,
        overlays,
        audio,
        scenes,
        words: placed,
        units: vec![],
    };
    res.units = build_units(&res);
    Ok(res)
}

/// Cuts the picture into units: shot parts (shots cut at insert edges), inserts, and a leading gap.
pub fn build_units(r: &Resolved) -> Vec<UnitSpan> {
    let total = r.frames;
    let ins = r.insert_frames();
    let mut spans: Vec<(UnitKind, String, i64, i64)> = vec![];
    // Picture owners before inserts are cut out: a gap, then shots end to end.
    let mut owners: Vec<(UnitKind, String, i64, i64)> = vec![];
    let first = r.shots.first().map(|s| r.frame(s.start).clamp(0, total)).unwrap_or(total);
    if first > 0 {
        owners.push((UnitKind::Gap, String::new(), 0, first));
    }
    for (i, s) in r.shots.iter().enumerate() {
        let a = r.frame(s.start).clamp(0, total);
        let b = r.shots.get(i + 1).map(|n| r.frame(n.start).clamp(0, total)).unwrap_or(total);
        if b > a {
            owners.push((UnitKind::Shot, s.id.clone(), a, b));
        }
    }
    for (kind, id, a, b) in owners {
        let mut cur = a;
        for &(s, e) in &ins {
            if e <= cur || s >= b {
                continue;
            }
            if s > cur {
                spans.push((kind, id.clone(), cur, s));
            }
            cur = cur.max(e);
        }
        if cur < b {
            spans.push((kind, id, cur, b));
        }
    }
    for (s, e) in &ins {
        let id = r
            .spine
            .iter()
            .find(|x| x.kind == SpineKind::Insert && r.frame(x.start) == *s && r.frame(x.end) == *e)
            .map(|x| x.id.clone())
            .unwrap_or_default();
        let (s, e) = ((*s).clamp(0, total), (*e).clamp(0, total));
        if e > s {
            spans.push((UnitKind::Insert, id, s, e));
        }
    }
    spans.sort_by_key(|x| x.2);
    spans
        .into_iter()
        .filter(|x| x.3 > x.2)
        .enumerate()
        .map(|(i, (kind, source, f0, f1))| UnitSpan { index: i, kind, source, f0, f1 })
        .collect()
}

/// Every error `validate` reports: references, ids, then anything resolution finds.
pub fn validate(p: &Project) -> Vec<EditError> {
    let mut errs = check_refs(p);
    if let Err(e) = resolve(p) {
        errs.extend(e);
    }
    errs.dedup();
    errs
}
