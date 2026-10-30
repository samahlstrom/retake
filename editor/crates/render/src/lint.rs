//! `retake-edit lint`: house rules (retake_timeline::lint) with each overlay's drawn box, measured with Skia.

use crate::composite::alpha_bbox;
use crate::engine::{Engine, Options};
use crate::graphics::Painter;
use anyhow::{Result, anyhow};
use retake_timeline::layer::Layer;
use retake_timeline::lint::{self, LintElement, Report, TimelineInfo};
use retake_timeline::{EditError, Errors, Project};
use serde::Serialize;
use serde_json::Value;
use std::collections::HashMap;
use std::path::Path;

/// Scale the boxes are measured at.
const BOX_SCALE: f64 = 0.25;

/// The box each overlay's graphics draw (the union over a few moments), in timeline pixels.
pub fn overlay_boxes(e: &Engine) -> Result<HashMap<String, [f64; 4]>> {
    let mut out = HashMap::new();
    let all: Vec<&Layer> = e.res.overlays.iter().flat_map(|o| o.comp.layers.iter()).filter(|l| !l.is_video()).collect();
    if all.is_empty() {
        return Ok(out);
    }
    let faces = e.resources.faces(all.iter().copied());
    let mut painter = Painter::new(e.resources.clone(), &faces).map_err(|x| anyhow!(x))?;
    let (w, h, s) = (e.canvas.w, e.canvas.h, e.canvas.scale);
    let comp = e.canvas.comp_f();
    let frame = 1.0 / e.fps() as f64;
    for o in &e.res.overlays {
        let layers: Vec<Layer> = o.comp.layers.iter().filter(|l| !l.is_video()).cloned().collect();
        if layers.is_empty() {
            continue;
        }
        let len = (o.to - o.from).max(frame);
        let mut acc: Option<[f64; 4]> = None;
        for t in [(len * 0.5).min(0.5), len * 0.5, (len - frame).max(0.0)] {
            for l in layers.iter().filter(|l| l.is_raster()) {
                let k = (t * e.fps() as f64).round() as i64;
                painter.raster_frame(l, "lint", &o.comp.props, &e.canvas, e.fps(), k, k + 1).map_err(|x| anyhow!(x))?;
            }
            let px = painter.render(&layers, &o.comp.props, (w, h), s, comp, t).map_err(|x| anyhow!(x))?;
            if let Some(b) = alpha_bbox(&px, w, h) {
                let b = [b[0] as f64 / s, b[1] as f64 / s, b[2] as f64 / s, b[3] as f64 / s];
                acc = Some(match acc {
                    None => b,
                    Some(a) => {
                        let (x0, y0) = (a[0].min(b[0]), a[1].min(b[1]));
                        let (x1, y1) = ((a[0] + a[2]).max(b[0] + b[2]), (a[1] + a[3]).max(b[1] + b[3]));
                        [x0, y0, x1 - x0, y1 - y0]
                    }
                });
            }
        }
        if let Some(b) = acc {
            out.insert(o.id.clone(), b);
        }
    }
    Ok(out)
}

#[derive(Debug, Clone, Serialize)]
pub struct LintResult {
    #[serde(flatten)]
    pub report: Report,
    /// Whether overlay boxes were measured (safe-area rules need them).
    pub boxes: bool,
    /// Rules that couldn't run, and why boxes are missing.
    pub notes: Vec<EditError>,
    /// The elements the rules saw.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub checked: Vec<LintElement>,
}

/// Lints a timeline with its kits' rules, or with `rules` when given.
pub fn lint_timeline(path: &Path, rules: Option<Vec<Value>>, with_elements: bool) -> Result<LintResult> {
    crate::init();
    let p = Project::load(path)?;
    let r = retake_timeline::resolve(&p).map_err(Errors)?;
    let mut els = lint::elements(&p, &r);
    let mut notes = vec![];
    let opts = Options { scale: Some(BOX_SCALE), jobs: 1, ..Options::default() };
    let boxes = match Engine::from_project(p.clone(), opts).and_then(|e| overlay_boxes(&e)) {
        Ok(b) => {
            for el in &mut els {
                el.bbox = b.get(&el.id).copied();
            }
            true
        }
        Err(e) => {
            notes.push(
                EditError::new("lint_no_boxes", format!("overlay boxes weren't measured: {e:#}"))
                    .hint("safe-area rules need the timeline's media and fonts; the other rules ran"),
            );
            false
        }
    };
    let rules = rules.unwrap_or_else(|| lint::kit_rules(&p));
    let info = TimelineInfo { id: p.tl.id.clone(), duration: r.end, fps: r.fps, size: p.tl.format.size };
    let mut ev = None;
    let (report, errs) = lint::run(&rules, &els, &info, &lint::kit_tokens(&p), &mut ev);
    notes.extend(errs);
    Ok(LintResult { report, boxes, notes, checked: if with_elements { els } else { vec![] } })
}
