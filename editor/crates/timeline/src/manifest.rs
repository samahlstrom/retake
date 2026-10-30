//! The render manifest (spec/manifest.md): what a render published, unit by unit, and `resolve_note`, which ranks the
//! elements under a reviewer's stroke.

use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const SCHEMA: &str = "manifest/0.1";

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Manifest {
    pub schema: String,
    pub timeline: TimelineRef,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub video: Option<String>,
    pub size: [u32; 2],
    pub fps: u32,
    pub duration: f64,
    pub frames: i64,
    /// The output time range this file covers (a `--range` render covers whole units).
    pub range: [f64; 2],
    #[serde(default)]
    pub draft: bool,
    pub units: Vec<Unit>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stats: Option<Stats>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct TimelineRef {
    pub id: String,
    pub rev: u64,
    /// BLAKE3 of the timeline document as rendered.
    pub hash: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Stats {
    pub total: usize,
    pub reused: usize,
    pub rendered: usize,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Unit {
    pub unit: String,
    /// The unit key (hash of the resolved unit description).
    pub key: String,
    pub kind: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub scene: Option<String>,
    /// The shot or insert this unit shows.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shot: Option<String>,
    pub t: [f64; 2],
    pub frames: [i64; 2],
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub video_hash: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub audio_hash: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub segment: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reused: Option<bool>,
    pub elements: Vec<Element>,
    /// Spine words in this unit: [start, end, text] in output seconds.
    #[serde(default)]
    pub words: Vec<(f64, f64, String)>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Element {
    pub id: String,
    /// JSON pointer into the timeline.
    pub ptr: String,
    /// `shot`, `overlay`, `insert`, `layer`, `spine` or `audio`.
    pub kind: String,
    /// Draw order within the unit (higher is on top). Audio-only elements have -1.
    pub z: i64,
    /// When the element is live within this unit (output seconds).
    pub t: [f64; 2],
    /// On-screen box over time: [t, x, y, w, h] in `size` pixels, each valid until the next. w = h = 0: hidden.
    #[serde(rename = "box", default, skip_serializing_if = "Vec::is_empty")]
    pub boxes: Vec<[f64; 5]>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub sources: Vec<Source>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub anchors: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent: Option<String>,
    /// The component the element was expanded from, `<name>@<version>`.
    #[serde(rename = "use", default, skip_serializing_if = "Option::is_none")]
    pub use_: Option<String>,
}

impl Element {
    /// The box at output time `t`, if visible.
    pub fn box_at(&self, t: f64) -> Option<[f64; 4]> {
        let b = self.boxes.iter().rev().find(|b| b[0] <= t + 1e-9)?;
        if b[3] <= 0.0 || b[4] <= 0.0 { None } else { Some([b[1], b[2], b[3], b[4]]) }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Source {
    pub asset: String,
    #[serde(rename = "in")]
    pub in_: f64,
    pub out: f64,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Target {
    pub id: String,
    pub kind: String,
    pub ptr: String,
    /// The overlap of the element's box and the stroke box over the smaller of the two (0 without a box).
    pub score: f64,
    pub z: i64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    #[serde(rename = "box")]
    pub bbox: Option<[f64; 4]>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub parent: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct NoteResolution {
    pub t: f64,
    pub unit: Option<String>,
    pub key: Option<String>,
    /// The spine word at `t` (or the nearest within 0.5 s), for (audio) notes.
    pub word: Option<(f64, f64, String)>,
    pub targets: Vec<Target>,
}

fn area(b: [f64; 4]) -> f64 {
    b[2].max(0.0) * b[3].max(0.0)
}

fn overlap(a: [f64; 4], b: [f64; 4]) -> f64 {
    let x0 = a[0].max(b[0]);
    let y0 = a[1].max(b[1]);
    let x1 = (a[0] + a[2]).min(b[0] + b[2]);
    let y1 = (a[1] + a[3]).min(b[1] + b[3]);
    (x1 - x0).max(0.0) * (y1 - y0).max(0.0)
}

/// The overlap that makes an element "under" a stroke: half of the smaller of the two boxes.
pub const UNDER: f64 = 0.5;

/// Ranks the elements live at `t` under `stroke` (pixels of `size`). An element's score is the overlap of its box and
/// the stroke box over the smaller of the two, so a mark inside an element and a loop around one both score 1. The
/// elements under the stroke (score at least [`UNDER`]) come first, topmost (z) first: a loop around a label resolves
/// to the label, ahead of the shot under it. Then the rest by score and z; ties go to the smaller element. Without a
/// stroke, visible elements rank by z.
pub fn resolve_note(m: &Manifest, t: f64, stroke: Option<[f64; 4]>) -> NoteResolution {
    let last = m.units.len().saturating_sub(1);
    let unit = m
        .units
        .iter()
        .enumerate()
        .find(|(i, u)| t >= u.t[0] && (t < u.t[1] || (*i == last && t <= u.t[1])))
        .map(|(_, u)| u);
    let Some(u) = unit else {
        return NoteResolution { t, unit: None, key: None, word: None, targets: vec![] };
    };
    let mut targets: Vec<(Target, f64)> = u
        .elements
        .iter()
        .filter(|e| t >= e.t[0] - 1e-9 && t <= e.t[1] + 1e-9)
        .filter_map(|e| {
            let bx = e.box_at(t);
            let score = match (stroke, bx) {
                (Some(s), Some(b)) if area(s) > 0.0 => (overlap(s, b) / area(s).min(area(b)).max(1e-9)).min(1.0),
                // A point stroke: inside or not.
                (Some(s), Some(b)) if s[0] >= b[0] && s[0] <= b[0] + b[2] && s[1] >= b[1] && s[1] <= b[1] + b[3] => 1.0,
                _ => 0.0,
            };
            if bx.is_none() && e.kind != "spine" && e.kind != "audio" {
                return None; // not on screen right now
            }
            let tgt = Target {
                id: e.id.clone(),
                kind: e.kind.clone(),
                ptr: e.ptr.clone(),
                score: (score * 1e4).round() / 1e4,
                z: e.z,
                bbox: bx,
                parent: e.parent.clone(),
            };
            Some((tgt, bx.map(area).unwrap_or(f64::INFINITY)))
        })
        .collect();
    targets.sort_by(|(a, aa), (b, ba)| {
        let (ua, ub) = (a.score >= UNDER, b.score >= UNDER);
        let order = if ua && ub {
            b.z.cmp(&a.z).then(b.score.total_cmp(&a.score))
        } else {
            b.score.total_cmp(&a.score).then(b.z.cmp(&a.z))
        };
        ub.cmp(&ua).then(order).then(aa.total_cmp(ba)).then(a.id.cmp(&b.id))
    });
    let word = u
        .words
        .iter()
        .find(|w| t >= w.0 && t < w.1)
        .or_else(|| {
            u.words
                .iter()
                .map(|w| (w, if t < w.0 { w.0 - t } else { t - w.1 }))
                .filter(|(_, d)| *d <= 0.5)
                .min_by(|a, b| a.1.total_cmp(&b.1))
                .map(|(w, _)| w)
        })
        .cloned();
    NoteResolution {
        t,
        unit: Some(u.unit.clone()),
        key: Some(u.key.clone()),
        word,
        targets: targets.into_iter().map(|(t, _)| t).collect(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn el(id: &str, kind: &str, z: i64, b: Option<[f64; 4]>) -> Element {
        Element {
            id: id.into(),
            ptr: format!("/x/{id}"),
            kind: kind.into(),
            z,
            t: [10.0, 20.0],
            boxes: b.map(|b| vec![[10.0, b[0], b[1], b[2], b[3]]]).unwrap_or_default(),
            sources: vec![],
            anchors: BTreeMap::new(),
            parent: None,
            use_: None,
        }
    }

    fn manifest() -> Manifest {
        Manifest {
            schema: SCHEMA.into(),
            timeline: TimelineRef { id: "t".into(), rev: 1, hash: "b3:0".into() },
            video: None,
            size: [1920, 1080],
            fps: 30,
            duration: 20.0,
            frames: 600,
            range: [0.0, 20.0],
            draft: false,
            units: vec![Unit {
                unit: "u1".into(),
                key: "k1".into(),
                kind: "shot".into(),
                scene: Some("s".into()),
                shot: Some("shot".into()),
                t: [10.0, 20.0],
                frames: [300, 600],
                video_hash: None,
                audio_hash: None,
                segment: None,
                reused: None,
                elements: vec![
                    el("shot", "shot", 0, Some([0.0, 0.0, 1920.0, 1080.0])),
                    el("pip", "overlay", 2, Some([1300.0, 100.0, 500.0, 300.0])),
                    el("pop", "overlay", 1, Some([600.0, 800.0, 700.0, 150.0])),
                    el("vo", "spine", -1, None),
                ],
                words: vec![(14.0, 14.4, "Hello".into()), (14.6, 15.0, "world".into())],
            }],
            stats: None,
        }
    }

    #[test]
    fn stroke_on_the_pip_ranks_it_first() {
        let r = resolve_note(&manifest(), 15.0, Some([1400.0, 150.0, 200.0, 100.0]));
        let ids: Vec<&str> = r.targets.iter().map(|t| t.id.as_str()).collect();
        assert_eq!(ids[0], "pip");
        assert_eq!(ids[1], "shot", "the shot under it is next (same overlap, lower z)");
        assert_eq!(r.targets[0].score, 1.0);
        assert_eq!(r.word.as_ref().unwrap().2, "world", "nearest word within 0.5 s");
    }

    #[test]
    fn a_loose_loop_around_an_overlay_ranks_it_ahead_of_the_shot() {
        // The pop is 700x150 at (600, 800); a loop drawn around it reaches well past its edges and the frame's.
        let r = resolve_note(&manifest(), 15.0, Some([560.0, 760.0, 800.0, 260.0]));
        let ids: Vec<&str> = r.targets.iter().map(|t| t.id.as_str()).collect();
        assert_eq!(ids[..2], ["pop", "shot"], "{:?}", r.targets);
        assert_eq!(r.targets[0].score, 1.0, "the loop covers all of the pop");
        // A loop that clips a corner of the pip (less than half of either box) doesn't make it the target.
        let r = resolve_note(&manifest(), 15.0, Some([1700.0, 300.0, 200.0, 200.0]));
        assert_eq!(r.targets[0].id, "shot");
        assert!(r.targets[1].id == "pip" && r.targets[1].score < UNDER, "{:?}", r.targets);
    }

    #[test]
    fn stroke_on_empty_area_ranks_the_shot() {
        let r = resolve_note(&manifest(), 12.0, Some([100.0, 100.0, 50.0, 50.0]));
        assert_eq!(r.targets[0].id, "shot");
        assert_eq!(r.targets[0].score, 1.0);
        assert!(r.word.is_none());
    }

    #[test]
    fn no_stroke_ranks_by_z() {
        let r = resolve_note(&manifest(), 14.2, None);
        let ids: Vec<&str> = r.targets.iter().map(|t| t.id.as_str()).collect();
        assert_eq!(ids, ["pip", "pop", "shot", "vo"]);
        assert_eq!(r.word.unwrap().2, "Hello");
    }

    #[test]
    fn outside_every_unit() {
        let r = resolve_note(&manifest(), 3.0, None);
        assert!(r.unit.is_none() && r.targets.is_empty());
    }

    #[test]
    fn boxes_change_over_time() {
        let mut e = el("p", "overlay", 1, None);
        e.boxes = vec![[10.0, 0.0, 0.0, 10.0, 10.0], [12.0, 0.0, 0.0, 0.0, 0.0], [13.0, 5.0, 5.0, 10.0, 10.0]];
        assert_eq!(e.box_at(11.0), Some([0.0, 0.0, 10.0, 10.0]));
        assert_eq!(e.box_at(12.5), None);
        assert_eq!(e.box_at(13.0), Some([5.0, 5.0, 10.0, 10.0]));
        assert_eq!(e.box_at(9.0), None);
    }

    #[test]
    fn manifest_round_trips() {
        let m = manifest();
        let s = serde_json::to_string(&m).unwrap();
        let back: Manifest = serde_json::from_str(&s).unwrap();
        assert_eq!(back, m);
    }

    #[test]
    fn manifest_and_note_resolution_match_the_schema() {
        let mut m = manifest();
        m.timeline.hash = "b3:00ff".into();
        m.units[0].key = "b3:0a".into();
        let v = serde_json::to_value(&m).unwrap();
        assert_eq!(crate::schema::check_manifest(&v), vec![]);
        let r = serde_json::to_value(resolve_note(&m, 15.0, Some([1400.0, 150.0, 200.0, 100.0]))).unwrap();
        assert_eq!(crate::schema::check_note_resolution(&r), vec![]);
        let mut bad = v.clone();
        bad["units"][0]["elements"][0]["kind"] = serde_json::json!("sprite");
        assert!(!crate::schema::check_manifest(&bad).is_empty());
    }
}
