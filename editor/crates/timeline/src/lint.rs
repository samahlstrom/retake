//! House rules (spec/components.md, "House rules and `retake-edit lint`"): a timeline's elements with their output
//! times, kinds and boxes, checked against a kit's declarative rules, plus `custom` rules in the expression sandbox.

use crate::component::{lookup_token, substitute_tokens};
use crate::error::EditError;
use crate::keyframe::ExprEval;
use crate::project::Project;
use crate::resolve::Resolved;
use serde::Serialize;
use serde_json::{Value, json};
use std::collections::BTreeMap;

/// A timeline element as house rules see it.
#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct LintElement {
    pub id: String,
    pub ptr: String,
    /// `shot`, `overlay` or `insert`.
    pub element: String,
    /// What rules match: the element kind, the component's category and name, and `meta.kind`.
    pub kinds: Vec<String>,
    #[serde(rename = "use", skip_serializing_if = "Option::is_none")]
    pub use_: Option<String>,
    /// Output seconds [from, to).
    pub t: [f64; 2],
    /// The box its graphics draw, in timeline pixels (overlays, when known).
    #[serde(rename = "box")]
    pub bbox: Option<[f64; 4]>,
    pub props: Value,
}

impl LintElement {
    pub fn is(&self, kind: &str) -> bool {
        kind == "*" || self.kinds.iter().any(|k| k == kind)
    }
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Violation {
    /// The rule type (`no-overlap`, …).
    pub rule: String,
    /// The rule's id in the kit.
    pub id: String,
    pub severity: String,
    pub code: String,
    pub message: String,
    pub elements: Vec<String>,
    pub ptrs: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub t: Option<[f64; 2]>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub hint: Option<String>,
}

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct Report {
    pub clean: bool,
    pub errors: usize,
    pub warnings: usize,
    pub rules: usize,
    pub elements: usize,
    pub violations: Vec<Violation>,
}

/// `m:ss.s` (or `h:mm:ss.s`).
pub fn clock(t: f64) -> String {
    let t = (t.max(0.0) * 10.0).round() / 10.0;
    let h = (t / 3600.0).floor() as i64;
    let m = ((t % 3600.0) / 60.0).floor() as i64;
    let s = t % 60.0;
    if h > 0 { format!("{h}:{m:02}:{s:04.1}") } else { format!("{m}:{s:04.1}") }
}

/// The timeline's shots, overlays and inserts as lint elements (without boxes).
pub fn elements(p: &Project, r: &Resolved) -> Vec<LintElement> {
    let mut out = vec![];
    let mut push = |id: &str, ptr: &str, element: &str, t: [f64; 2], site: Option<&Value>| {
        let mut kinds = vec![element.to_string()];
        let u = p.use_of(id);
        if let Some(u) = u {
            if let Some(c) = &u.category {
                kinds.push(c.clone());
            }
            kinds.push(u.name.clone());
        }
        let owner = p.doc.pointer(ptr);
        if let Some(k) = owner.and_then(|o| o.pointer("/meta/kind")).and_then(Value::as_str) {
            kinds.push(k.to_string());
        }
        kinds.dedup();
        out.push(LintElement {
            id: id.to_string(),
            ptr: ptr.to_string(),
            element: element.to_string(),
            kinds,
            use_: u.map(|u| u.reference.clone()),
            t,
            bbox: None,
            props: site.and_then(|s| s.get("props")).cloned().unwrap_or_else(|| json!({})),
        });
    };
    for s in &r.shots {
        let pic = p.doc.pointer(&format!("{}/picture", s.ptr));
        push(&s.id, &s.ptr, "shot", [s.start, s.end], pic);
    }
    for o in &r.overlays {
        push(&o.id, &o.ptr, "overlay", [o.from, o.to], p.doc.pointer(&o.ptr));
    }
    for s in r.spine.iter().filter(|s| s.kind == crate::model::SpineKind::Insert) {
        let pic = p.doc.pointer(&format!("{}/picture", s.ptr));
        push(&s.id, &s.ptr, "insert", [s.start, s.end], pic);
    }
    out.sort_by(|a, b| a.t[0].total_cmp(&b.t[0]).then(a.id.cmp(&b.id)));
    out
}

/// The rules of the timeline's kits, in order, each with the kit's tokens resolved.
pub fn kit_rules(p: &Project) -> Vec<Value> {
    let mut out = vec![];
    let tokens: Vec<&Value> = p.kits.iter().map(|k| &k.kit.tokens).collect();
    for k in &p.kits {
        for r in &k.kit.rules {
            let mut r = r.clone();
            if r.get("kit").is_none() {
                r["kit"] = json!(format!("{}@{}", k.name, k.version));
            }
            let mut errs = vec![];
            substitute_tokens(&mut r, &tokens, "", false, &mut errs);
            out.push(r);
        }
    }
    out
}

/// The kits' tokens merged (earlier kits win), for custom rules.
pub fn kit_tokens(p: &Project) -> Value {
    let mut out = json!({});
    for k in p.kits.iter().rev() {
        if let (Some(o), Some(t)) = (out.as_object_mut(), k.kit.tokens.as_object()) {
            for (key, v) in t {
                o.insert(key.clone(), v.clone());
            }
        }
    }
    out
}

struct Rule<'a> {
    v: &'a Value,
    id: String,
    rule: String,
    severity: String,
    message: Option<String>,
}

impl Rule<'_> {
    fn str(&self, k: &str) -> Option<&str> {
        self.v.get(k).and_then(Value::as_str)
    }
    fn num(&self, k: &str, tokens: &Value) -> Option<f64> {
        match self.v.get(k)? {
            Value::Number(n) => n.as_f64(),
            Value::String(s) => {
                s.strip_prefix("kit:").and_then(|p| lookup_token(&[tokens], p)).and_then(|v| v.as_f64())
            }
            _ => None,
        }
    }
    fn tail(&self) -> String {
        self.message.as_ref().map(|m| format!("; {m}")).unwrap_or_default()
    }
    fn violation(&self, message: String, els: &[&LintElement], t: Option<[f64; 2]>, hint: Option<String>) -> Violation {
        Violation {
            rule: self.rule.clone(),
            id: self.id.clone(),
            severity: self.severity.clone(),
            code: format!("lint_{}", self.rule.replace('-', "_")),
            message,
            elements: els.iter().map(|e| e.id.clone()).collect(),
            ptrs: els.iter().map(|e| e.ptr.clone()).collect(),
            t,
            hint,
        }
    }
}

/// The timeline a custom rule sees.
#[derive(Debug, Clone, Serialize)]
pub struct TimelineInfo {
    pub id: String,
    pub duration: f64,
    pub fps: u32,
    pub size: [u32; 2],
}

/// Runs `rules` over `elements`. Rule errors (a bad rule, a failing expression) are returned separately.
pub fn run(
    rules: &[Value],
    els: &[LintElement],
    info: &TimelineInfo,
    tokens: &Value,
    ev: &mut Option<Box<dyn ExprEval>>,
) -> (Report, Vec<EditError>) {
    let mut out = vec![];
    let mut errs = vec![];
    for (i, rv) in rules.iter().enumerate() {
        let rule = Rule {
            v: rv,
            id: rv.get("id").and_then(Value::as_str).map(String::from).unwrap_or_else(|| format!("rule-{}", i + 1)),
            rule: rv.get("rule").and_then(Value::as_str).unwrap_or_default().to_string(),
            severity: rv.get("severity").and_then(Value::as_str).unwrap_or("error").to_string(),
            message: rv.get("message").and_then(Value::as_str).map(String::from),
        };
        let bad = |m: String| EditError::new("lint_rule", format!("rule {}: {m}", rule.id)).at(format!("/rules/{i}"));
        if rule.severity != "error" && rule.severity != "warning" {
            errs.push(bad(format!("severity `{}` is error or warning", rule.severity)));
            continue;
        }
        let kind = rule.str("kind").unwrap_or("*").to_string();
        let of = |k: &str| -> Vec<&LintElement> { els.iter().filter(|e| e.is(k)).collect() };
        match rule.rule.as_str() {
            "no-overlap" => {
                let with = rule.str("with").unwrap_or(&kind).to_string();
                let tol = rule.num("tolerance", tokens).unwrap_or(0.0);
                let (a, b) = (of(&kind), of(&with));
                for (i, x) in a.iter().enumerate() {
                    for y in &b {
                        if x.id == y.id || (kind == with && b.iter().position(|z| z.id == y.id) <= Some(i)) {
                            continue;
                        }
                        let (s, e) = (x.t[0].max(y.t[0]), x.t[1].min(y.t[1]));
                        if e - s > tol + 1e-6 {
                            let (first, second) = if x.t[0] <= y.t[0] { (x, y) } else { (y, x) };
                            out.push(rule.violation(
                                format!(
                                    "{kind} overlaps {with} at {}–{} ({}, {}){}",
                                    clock(s),
                                    clock(e),
                                    first.id,
                                    second.id,
                                    rule.tail()
                                ),
                                &[first, second],
                                Some([s, e]),
                                Some(format!(
                                    "end {} before {} or start {} after {}",
                                    first.id,
                                    clock(second.t[0]),
                                    second.id,
                                    clock(first.t[1])
                                )),
                            ));
                        }
                    }
                }
            }
            "min-gap" => {
                let Some(gap) = rule.num("seconds", tokens) else {
                    errs.push(bad("min-gap needs seconds".into()));
                    continue;
                };
                let a = of(&kind);
                for w in a.windows(2) {
                    let (x, y) = (w[0], w[1]);
                    let g = y.t[0] - x.t[1];
                    if g < gap - 1e-6 {
                        let what = if g < 0.0 {
                            format!("{} starts {:.2} s before {} ends", y.id, -g, x.id)
                        } else {
                            format!("{} starts {:.2} s after {} ends", y.id, g, x.id)
                        };
                        out.push(rule.violation(
                            format!("{what} at {}; keep {gap} s between {kind}s{}", clock(y.t[0]), rule.tail()),
                            &[x, y],
                            Some([x.t[1].min(y.t[0]), y.t[0].max(x.t[1])]),
                            Some(format!("start {} at {} or later", y.id, clock(x.t[1] + gap))),
                        ));
                    }
                }
            }
            "max-count-per-window" => {
                let (Some(max), Some(window)) = (rule.num("max", tokens), rule.num("window", tokens)) else {
                    errs.push(bad("max-count-per-window needs max and window".into()));
                    continue;
                };
                let a = of(&kind);
                let mut reported_until = f64::NEG_INFINITY;
                for (i, x) in a.iter().enumerate() {
                    if x.t[0] < reported_until {
                        continue;
                    }
                    let group: Vec<&LintElement> =
                        a[i..].iter().filter(|y| y.t[0] < x.t[0] + window - 1e-9).copied().collect();
                    if group.len() as f64 > max {
                        let last = group.last().expect("non-empty");
                        let ids: Vec<&str> = group.iter().map(|g| g.id.as_str()).collect();
                        out.push(rule.violation(
                            format!(
                                "{} {kind}s start within {window} s ({}–{}: {}); at most {max}{}",
                                group.len(),
                                clock(x.t[0]),
                                clock(last.t[0]),
                                ids.join(", "),
                                rule.tail()
                            ),
                            &group,
                            Some([x.t[0], last.t[0]]),
                            Some(format!("drop or move {} past {}", last.id, clock(x.t[0] + window))),
                        ));
                        reported_until = x.t[0] + window;
                    }
                }
            }
            "safe-area" => {
                let area: Option<Vec<f64>> = match rv.get("area") {
                    Some(Value::Array(a)) => a.iter().map(Value::as_f64).collect(),
                    Some(Value::String(s)) => s
                        .strip_prefix("kit:")
                        .and_then(|p| lookup_token(&[tokens], p))
                        .and_then(|v| v.as_array().map(|a| a.iter().filter_map(Value::as_f64).collect())),
                    _ => None,
                };
                let Some(area) = area.filter(|a| a.len() == 4) else {
                    errs.push(bad("safe-area needs area: [x, y, w, h] as fractions of the frame".into()));
                    continue;
                };
                let (w, h) = (info.size[0] as f64, info.size[1] as f64);
                let px = [area[0] * w, area[1] * h, area[2] * w, area[3] * h];
                for x in of(&kind) {
                    let Some(b) = x.bbox else { continue };
                    let inside = b[0] >= px[0] - 1.0
                        && b[1] >= px[1] - 1.0
                        && b[0] + b[2] <= px[0] + px[2] + 1.0
                        && b[1] + b[3] <= px[1] + px[3] + 1.0;
                    if !inside {
                        let r = |v: f64| v.round() as i64;
                        out.push(rule.violation(
                            format!(
                                "{} at {} draws outside the safe area: [{}, {}, {}, {}] is not inside [{}, {}, {}, {}]{}",
                                x.id,
                                clock(x.t[0]),
                                r(b[0]),
                                r(b[1]),
                                r(b[2]),
                                r(b[3]),
                                r(px[0]),
                                r(px[1]),
                                r(px[2]),
                                r(px[3]),
                                rule.tail()
                            ),
                            &[x],
                            Some(x.t),
                            Some(format!(
                                "keep it within x {}–{} and y {}–{}",
                                r(px[0]),
                                r(px[0] + px[2]),
                                r(px[1]),
                                r(px[1] + px[3])
                            )),
                        ));
                    }
                }
            }
            "min-duration" | "max-duration" => {
                let Some(secs) = rule.num("seconds", tokens) else {
                    errs.push(bad(format!("{} needs seconds", rule.rule)));
                    continue;
                };
                let min = rule.rule == "min-duration";
                for x in of(&kind) {
                    let d = x.t[1] - x.t[0];
                    if (min && d < secs - 1e-6) || (!min && d > secs + 1e-6) {
                        out.push(rule.violation(
                            format!(
                                "{} shows for {d:.2} s at {}; {} {secs} s{}",
                                x.id,
                                clock(x.t[0]),
                                if min { "at least" } else { "at most" },
                                rule.tail()
                            ),
                            &[x],
                            Some(x.t),
                            Some(format!("make it {} {secs} s", if min { "at least" } else { "at most" })),
                        ));
                    }
                }
            }
            "custom" => {
                let Some(expr) = rule.str("expr") else {
                    errs.push(bad("custom needs expr".into()));
                    continue;
                };
                if ev.is_none() {
                    match crate::component::evaluator() {
                        Ok(x) => *ev = Some(x),
                        Err(e) => {
                            errs.push(bad(e.message));
                            continue;
                        }
                    }
                }
                let scope = json!({"elements": els, "timeline": info, "kit": tokens, "rule": rv});
                match ev.as_mut().expect("engine").eval_json(expr, &scope) {
                    Ok(Value::Array(vs)) => {
                        let by_id: BTreeMap<&str, &LintElement> = els.iter().map(|e| (e.id.as_str(), e)).collect();
                        for v in vs {
                            let msg = v.get("message").and_then(Value::as_str).unwrap_or("custom rule").to_string();
                            let named: Vec<&LintElement> = v
                                .get("elements")
                                .and_then(Value::as_array)
                                .map(|a| a.iter().filter_map(|x| x.as_str().and_then(|s| by_id.get(s).copied())).collect())
                                .unwrap_or_default();
                            let t = v.get("t").and_then(Value::as_array).and_then(|a| Some([a.first()?.as_f64()?, a.get(1)?.as_f64()?]));
                            let hint = v.get("hint").and_then(Value::as_str).map(String::from);
                            out.push(rule.violation(format!("{msg}{}", rule.tail()), &named, t, hint));
                        }
                    }
                    Ok(Value::Null) => {}
                    Ok(other) => errs.push(bad(format!("expr returned {other}, not a list of violations"))),
                    Err(e) => errs.push(bad(e.message)),
                }
            }
            other => errs.push(bad(format!(
                "unknown rule `{other}`: no-overlap, min-gap, max-count-per-window, safe-area, min-duration, max-duration or custom"
            ))),
        }
    }
    out.sort_by(|a, b| {
        let ta = a.t.map(|t| t[0]).unwrap_or(f64::INFINITY);
        let tb = b.t.map(|t| t[0]).unwrap_or(f64::INFINITY);
        ta.total_cmp(&tb).then(a.id.cmp(&b.id))
    });
    let errors = out.iter().filter(|v| v.severity == "error").count();
    let warnings = out.len() - errors;
    (Report { clean: out.is_empty(), errors, warnings, rules: rules.len(), elements: els.len(), violations: out }, errs)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn el(id: &str, kinds: &[&str], t: [f64; 2], b: Option<[f64; 4]>) -> LintElement {
        LintElement {
            id: id.into(),
            ptr: format!("/x/{id}"),
            element: "overlay".into(),
            kinds: kinds.iter().map(|s| s.to_string()).collect(),
            use_: None,
            t,
            bbox: b,
            props: json!({}),
        }
    }

    fn info() -> TimelineInfo {
        TimelineInfo { id: "t".into(), duration: 60.0, fps: 30, size: [1920, 1080] }
    }

    #[test]
    fn clock_format() {
        assert_eq!(clock(12.34), "0:12.3");
        assert_eq!(clock(72.0), "1:12.0");
        assert_eq!(clock(3725.25), "1:02:05.3");
    }

    #[test]
    fn declarative_rules() {
        let els = vec![
            el("pop/a", &["overlay", "pop"], [12.0, 12.5], Some([100.0, 100.0, 200.0, 100.0])),
            el("pop/b", &["overlay", "pop"], [12.3, 13.0], Some([10.0, 900.0, 300.0, 200.0])),
            el("pop/c", &["overlay", "pop"], [13.4, 14.0], None),
            el("zoom/1", &["shot", "zoom"], [1.0, 2.0], None),
            el("zoom/2", &["shot", "zoom"], [4.0, 5.0], None),
            el("zoom/3", &["shot", "zoom"], [8.0, 9.0], None),
            el("cap", &["overlay", "caption"], [12.4, 20.0], None),
        ];
        let tokens = json!({"pacing": {"pop_min_gap": 0.8}, "safe_area": {"action": [0.05, 0.05, 0.9, 0.9]}});
        let rules = vec![
            json!({"id": "one-pop", "rule": "no-overlap", "kind": "pop", "message": "one pop at a time"}),
            json!({"id": "gap", "rule": "min-gap", "kind": "pop", "seconds": "kit:pacing.pop_min_gap", "severity": "warning"}),
            json!({"id": "zooms", "rule": "max-count-per-window", "kind": "zoom", "max": 2, "window": 10}),
            json!({"id": "safe", "rule": "safe-area", "kind": "overlay", "area": "kit:safe_area.action"}),
            json!({"id": "short", "rule": "min-duration", "kind": "pop", "seconds": 0.6}),
            json!({"id": "pop-caption", "rule": "no-overlap", "kind": "pop", "with": "caption"}),
        ];
        let (rep, errs) = run(&rules, &els, &info(), &tokens, &mut None);
        assert!(errs.is_empty(), "{errs:?}");
        let msgs: Vec<&str> = rep.violations.iter().map(|v| v.message.as_str()).collect();
        assert!(msgs.contains(&"pop overlaps pop at 0:12.3–0:12.5 (pop/a, pop/b); one pop at a time"), "{msgs:#?}");
        let gap = rep.violations.iter().find(|v| v.id == "gap" && v.elements == ["pop/b", "pop/c"]).unwrap();
        assert_eq!(gap.severity, "warning");
        assert!(gap.message.starts_with("pop/c starts 0.40 s after pop/b ends"), "{}", gap.message);
        assert_eq!(gap.hint.as_deref(), Some("start pop/c at 0:13.8 or later"));
        let z = rep.violations.iter().find(|v| v.id == "zooms").unwrap();
        assert_eq!(z.elements, ["zoom/1", "zoom/2", "zoom/3"]);
        assert_eq!(z.code, "lint_max_count_per_window");
        let s = rep.violations.iter().find(|v| v.id == "safe").unwrap();
        assert_eq!(s.elements, ["pop/b"], "pop/a is inside; pop/c has no box");
        assert!(rep.violations.iter().any(|v| v.id == "short" && v.elements == ["pop/a"]));
        assert_eq!(rep.violations.iter().filter(|v| v.id == "pop-caption").count(), 3, "every pop over the caption");
        assert!(!rep.clean && rep.warnings >= 1 && rep.errors >= 5);
        // Ordered by time.
        let ts: Vec<f64> = rep.violations.iter().filter_map(|v| v.t.map(|t| t[0])).collect();
        assert!(ts.windows(2).all(|w| w[0] <= w[1]), "{ts:?}");
    }

    #[test]
    fn bad_rules_are_reported_not_fatal() {
        let rules =
            vec![json!({"id": "x", "rule": "frobnicate"}), json!({"id": "y", "rule": "min-gap", "kind": "pop"})];
        let (rep, errs) = run(&rules, &[], &info(), &json!({}), &mut None);
        assert!(rep.clean);
        assert_eq!(errs.len(), 2);
        assert!(errs.iter().all(|e| e.code == "lint_rule"));
    }
}
