//! Retake editor: the `timeline/0.1` format (spec/timeline.md) and everything about it that doesn't need pixels:
//! validation, the anchor resolver, units, patches, keyframes and easing, and the render manifest.

pub mod anchor;
pub mod component;
pub mod error;
pub mod fsx;
pub mod keyframe;
pub mod layer;
pub mod lint;
pub mod manifest;
pub mod model;
pub mod patch;
pub mod project;
pub mod resolve;
pub mod schema;
pub mod words;

pub use error::{EditError, Errors};
pub use project::Project;
pub use resolve::{Resolved, resolve, validate};

/// Canonical JSON: object keys sorted at every level, no whitespace. What every cache key hashes.
pub fn canonical_json(v: &serde_json::Value) -> String {
    fn sorted(v: &serde_json::Value) -> serde_json::Value {
        match v {
            serde_json::Value::Object(m) => {
                let mut keys: Vec<&String> = m.keys().collect();
                keys.sort();
                let mut out = serde_json::Map::new();
                for k in keys {
                    out.insert(k.clone(), sorted(&m[k]));
                }
                serde_json::Value::Object(out)
            }
            serde_json::Value::Array(a) => serde_json::Value::Array(a.iter().map(sorted).collect()),
            other => other.clone(),
        }
    }
    serde_json::to_string(&sorted(v)).expect("JSON values serialize")
}

/// `b3:<hex>` of some bytes.
pub fn b3(bytes: &[u8]) -> String {
    format!("b3:{}", blake3::hash(bytes).to_hex())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn canonical_json_sorts_keys_at_every_level() {
        let a = json!({"b": 1, "a": {"d": [1, {"z": 0, "y": 1}], "c": 2}});
        let b = json!({"a": {"c": 2, "d": [1, {"y": 1, "z": 0}]}, "b": 1});
        assert_eq!(canonical_json(&a), canonical_json(&b));
        assert_eq!(canonical_json(&a), r#"{"a":{"c":2,"d":[1,{"y":1,"z":0}]},"b":1}"#);
    }
}
