//! Word times for spine assets, in the transcript.json word format of spec/notes.md.

use crate::error::EditError;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::path::Path;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Word {
    /// The word as transcribed, without the leading space.
    pub text: String,
    /// Seconds into the asset.
    pub s: f64,
    pub e: f64,
}

impl Word {
    pub fn mid(&self) -> f64 {
        (self.s + self.e) / 2.0
    }
}

/// Lowercase, letters, digits and `%` only: "Eternus," and "eternus" match.
pub fn norm(s: &str) -> String {
    s.chars().filter(|c| c.is_alphanumeric() || *c == '%').flat_map(|c| c.to_lowercase()).collect()
}

/// Does `query` (as written in an anchor, maybe with a trailing `*`) match this word?
pub fn matches(query: &str, word: &str) -> bool {
    match query.strip_suffix('*') {
        Some(prefix) => {
            let p = norm(prefix);
            !p.is_empty() && norm(word).starts_with(&p)
        }
        None => {
            let q = norm(query);
            !q.is_empty() && norm(word) == q
        }
    }
}

fn parse_list(v: &Value) -> Option<Vec<Word>> {
    let arr = v.as_array()?;
    let mut out = Vec::with_capacity(arr.len());
    for w in arr {
        let text = w.get("w")?.as_str()?.trim().to_string();
        let s = w.get("s")?.as_f64()?;
        let e = w.get("e")?.as_f64()?;
        out.push(Word { text, s, e });
    }
    Some(out)
}

/// Reads words from a transcript file: `{"files": {name: {"words": [...]}}}` (the entry named like the asset's file,
/// or the only entry), `{"words": [...]}`, or a bare array.
pub fn parse_words(v: &Value, asset_file_name: &str) -> Result<Vec<Word>, String> {
    let list = if let Some(files) = v.get("files").and_then(|f| f.as_object()) {
        let entry = files.get(asset_file_name).or_else(|| if files.len() == 1 { files.values().next() } else { None });
        let entry = entry.ok_or_else(|| {
            format!(
                "transcript has {} files and none is named {asset_file_name}: {}",
                files.len(),
                files.keys().cloned().collect::<Vec<_>>().join(", ")
            )
        })?;
        entry.get("words").ok_or("transcript entry has no words")?
    } else if let Some(w) = v.get("words") {
        w
    } else {
        v
    };
    let mut words = parse_list(list).ok_or("words must be [{w, s, e}] with w a string and s, e numbers")?;
    words.sort_by(|a, b| a.s.total_cmp(&b.s));
    Ok(words)
}

pub fn load_words(path: &Path, asset_file_name: &str) -> Result<Vec<Word>, EditError> {
    let text = std::fs::read_to_string(path)
        .map_err(|e| EditError::new("words_unreadable", format!("can't read words file {}: {e}", path.display())))?;
    let v: Value = serde_json::from_str(&text)
        .map_err(|e| EditError::new("words_invalid", format!("words file {} isn't JSON: {e}", path.display())))?;
    parse_words(&v, asset_file_name)
        .map_err(|e| EditError::new("words_invalid", format!("words file {}: {e}", path.display())))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn normalizes() {
        assert_eq!(norm(" Eternus,"), "eternus");
        assert_eq!(norm("100%!"), "100%");
        assert!(matches("Eternus", " eternus,"));
        assert!(matches("explain*", "explained."));
        assert!(!matches("explain", "explained"));
        assert!(!matches("*", "anything"));
    }

    #[test]
    fn transcript_formats() {
        let ws = json!([{"w": " Hello", "s": 0.5, "e": 0.9, "p": 0.99}, {"w": " world.", "s": 1.0, "e": 1.4}]);
        let a = parse_words(&json!({"files": {"vo.wav": {"size": 1, "model": "m", "words": ws}}}), "vo.wav").unwrap();
        assert_eq!(a[0].text, "Hello");
        assert_eq!(a.len(), 2);
        // The only entry is used whatever its name.
        assert_eq!(parse_words(&json!({"files": {"seg_01.webm": {"words": ws}}}), "vo.wav").unwrap(), a);
        assert_eq!(parse_words(&json!({"words": ws}), "x").unwrap(), a);
        assert_eq!(parse_words(&ws, "x").unwrap(), a);
        let two = json!({"files": {"a.wav": {"words": ws}, "b.wav": {"words": ws}}});
        assert!(parse_words(&two, "c.wav").unwrap_err().contains("none is named c.wav"));
        assert!(parse_words(&json!([{"w": 1}]), "x").is_err());
    }
}
