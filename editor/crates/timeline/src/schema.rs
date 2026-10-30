//! JSON Schema checks against spec/timeline.schema.json (embedded at build time: the spec is the source of truth).

use crate::error::EditError;
use serde_json::Value;
use std::sync::OnceLock;

pub const TIMELINE_SCHEMA: &str = include_str!("../../../../spec/timeline.schema.json");

fn root_schema() -> Value {
    serde_json::from_str(TIMELINE_SCHEMA).expect("spec/timeline.schema.json is valid JSON")
}

/// A validator for `#/$defs/<def>` of the timeline schema (or the root when `def` is None).
fn build(def: Option<&str>) -> jsonschema::Validator {
    let mut s = root_schema();
    if let Some(d) = def {
        let defs = s["$defs"].clone();
        s = serde_json::json!({
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$ref": format!("#/$defs/{d}"),
            "$defs": defs,
        });
    }
    jsonschema::validator_for(&s).expect("spec/timeline.schema.json compiles")
}

fn timeline_validator() -> &'static jsonschema::Validator {
    static V: OnceLock<jsonschema::Validator> = OnceLock::new();
    V.get_or_init(|| build(None))
}

fn patch_validator() -> &'static jsonschema::Validator {
    static V: OnceLock<jsonschema::Validator> = OnceLock::new();
    V.get_or_init(|| build(Some("patch")))
}

fn log_entry_validator() -> &'static jsonschema::Validator {
    static V: OnceLock<jsonschema::Validator> = OnceLock::new();
    V.get_or_init(|| build(Some("patchLogEntry")))
}

fn collect(v: &jsonschema::Validator, doc: &Value, what: &str) -> Vec<EditError> {
    let mut out: Vec<EditError> = v
        .iter_errors(doc)
        .map(|e| {
            let path = e.instance_path().to_string();
            EditError::new("schema", format!("{what}: {e}")).at(path)
        })
        .collect();
    out.sort_by(|a, b| a.path.cmp(&b.path));
    out.dedup();
    out
}

pub fn check_timeline(doc: &Value) -> Vec<EditError> {
    collect(timeline_validator(), doc, "timeline")
}

pub fn check_patch(doc: &Value) -> Vec<EditError> {
    collect(patch_validator(), doc, "patch")
}

pub fn check_log_entry(doc: &Value) -> Vec<EditError> {
    collect(log_entry_validator(), doc, "patch log entry")
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn minimal() -> Value {
        json!({
            "schema": "timeline/0.1", "id": "t", "rev": 0,
            "format": {"size": [320, 180], "fps": 30},
            "assets": {"proj:vo": {"path": "vo.wav", "words": "vo.words.json"}},
            "spine": [{"id": "a", "asset": "proj:vo", "in": 0, "out": 2}],
            "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a", "picture": {"layers": [
                {"id": "s/1/t", "type": "text", "text": "HI", "transform": {"scale": {"keys": [
                    {"t": 0, "v": 1.2, "ease": "out-back"}, {"t": "word:a/hi+0.10", "v": 1, "ease": [0.2, 0, 0.2, 1]}]}}}
            ]}}]}]
        })
    }

    #[test]
    fn minimal_timeline_passes() {
        assert_eq!(check_timeline(&minimal()), vec![]);
    }

    #[test]
    fn schema_errors_point_at_the_value() {
        let mut d = minimal();
        d["format"]["fps"] = json!(0);
        d["spine"][0]["colour"] = json!("red");
        d["scenes"][0]["shots"][0]["picture"]["layers"][0]["sise"] = json!(3);
        let errs = check_timeline(&d);
        let paths: Vec<_> = errs.iter().map(|e| e.path.clone().unwrap_or_default()).collect();
        assert!(paths.contains(&"/format/fps".to_string()), "{errs:?}");
        assert!(paths.iter().any(|p| p == "/spine/0"), "{errs:?}");
        assert!(paths.iter().any(|p| p == "/scenes/0/shots/0/picture/layers/0"), "{errs:?}");
        assert!(errs.iter().all(|e| e.code == "schema"));
    }

    #[test]
    fn spine_needs_exactly_one_length() {
        let mut d = minimal();
        d["spine"][0]["dur"] = json!(2.0);
        assert!(!check_timeline(&d).is_empty());
        d["spine"][0].as_object_mut().unwrap().remove("out");
        assert!(check_timeline(&d).is_empty());
    }

    #[test]
    fn bad_anchor_and_ease_rejected() {
        let mut d = minimal();
        d["scenes"][0]["shots"][0]["at"] = json!("frame:3");
        assert!(!check_timeline(&d).is_empty());
        let mut d = minimal();
        d["scenes"][0]["shots"][0]["picture"]["layers"][0]["transform"]["scale"]["keys"][0]["ease"] = json!("bouncy");
        assert!(!check_timeline(&d).is_empty());
    }

    #[test]
    fn patches() {
        let ok = json!({"base": 0, "answers": ["v1#2"], "summary": "x", "ops": [
            {"op": "set", "id": "s/1/t", "path": "text", "value": "YO"},
            {"op": "insert", "path": "spine", "value": {"id": "b"}, "after": "a"},
            {"op": "remove", "id": "b"},
            {"op": "move", "id": "s/1", "to": {"id": "s", "path": "shots", "index": 0}}
        ]});
        assert_eq!(check_patch(&ok), vec![]);
        let bad = json!({"base": 0, "answers": [], "summary": "x", "ops": [{"op": "frob", "id": "a"}]});
        assert!(!check_patch(&bad).is_empty());
        let no_summary = json!({"base": 0, "answers": [], "ops": [{"op": "remove", "id": "a"}]});
        assert!(!check_patch(&no_summary).is_empty());
    }
}
