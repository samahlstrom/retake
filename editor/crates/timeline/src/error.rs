//! Typed errors an agent can act on: a stable `code`, a message, the JSON pointer of the offending value, and a hint.

use serde::{Deserialize, Serialize};
use std::fmt;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EditError {
    /// Stable machine-readable code, e.g. `anchor_word_not_found`.
    pub code: String,
    pub message: String,
    /// JSON pointer into the timeline (or the patch) where the problem is.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    /// What to do about it.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub hint: Option<String>,
    /// Close matches for a name that wasn't found.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub suggestions: Vec<String>,
}

impl EditError {
    pub fn new(code: &str, message: impl Into<String>) -> Self {
        EditError { code: code.to_string(), message: message.into(), path: None, hint: None, suggestions: vec![] }
    }
    pub fn at(mut self, path: impl Into<String>) -> Self {
        let p = path.into();
        if self.path.is_none() && !p.is_empty() {
            self.path = Some(p);
        }
        self
    }
    pub fn hint(mut self, hint: impl Into<String>) -> Self {
        self.hint = Some(hint.into());
        self
    }
    pub fn suggest(mut self, s: Vec<String>) -> Self {
        self.suggestions = s;
        self
    }
}

impl fmt::Display for EditError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "error[{}]", self.code)?;
        if let Some(p) = &self.path {
            write!(f, " at {p}")?;
        }
        write!(f, ": {}", self.message)?;
        if let Some(h) = &self.hint {
            write!(f, " ({h})")?;
        }
        Ok(())
    }
}

impl std::error::Error for EditError {}

/// Several errors at once (validation reports everything it finds).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Errors(pub Vec<EditError>);

impl fmt::Display for Errors {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        for (i, e) in self.0.iter().enumerate() {
            if i > 0 {
                writeln!(f)?;
            }
            write!(f, "{e}")?;
        }
        Ok(())
    }
}

impl std::error::Error for Errors {}

impl From<EditError> for Errors {
    fn from(e: EditError) -> Self {
        Errors(vec![e])
    }
}

/// Escape one JSON pointer token (RFC 6901).
pub fn ptr_escape(s: &str) -> String {
    s.replace('~', "~0").replace('/', "~1")
}

/// Unescape one JSON pointer token.
pub fn ptr_unescape(s: &str) -> String {
    s.replace("~1", "/").replace("~0", "~")
}

/// Close matches for `name` among `candidates`, best first.
pub fn close_matches<'a>(name: &str, candidates: impl IntoIterator<Item = &'a str>, max: usize) -> Vec<String> {
    let mut scored: Vec<(f64, &str)> = candidates
        .into_iter()
        .map(|c| (strsim::normalized_damerau_levenshtein(&name.to_lowercase(), &c.to_lowercase()), c))
        .filter(|(s, _)| *s >= 0.5)
        .collect();
    scored.sort_by(|a, b| b.0.total_cmp(&a.0).then(a.1.cmp(b.1)));
    let mut out: Vec<String> = Vec::new();
    for (_, c) in scored {
        if !out.iter().any(|o| o == c) {
            out.push(c.to_string());
        }
        if out.len() == max {
            break;
        }
    }
    out
}
