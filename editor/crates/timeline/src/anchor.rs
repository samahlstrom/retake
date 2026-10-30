//! The anchor grammar: `word:<spine-id>/<word>[#n][@end][±s]`, `start:<id>[±s]`, `end:<id>[±s]`, `t:<s>[±s]`.

use crate::error::EditError;
use serde::Serialize;

#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(tag = "kind", rename_all = "lowercase")]
pub enum AnchorKind {
    /// The n-th (1-based) word matching `word` in spine item `spine`; `end` picks the word's end time.
    Word {
        spine: String,
        word: String,
        n: usize,
        end: bool,
    },
    Start {
        id: String,
    },
    End {
        id: String,
    },
    T {
        t: f64,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct Anchor {
    #[serde(flatten)]
    pub kind: AnchorKind,
    pub offset: f64,
}

/// Splits a trailing `+0.30` / `-0.12` offset off `s`. The offset needs a decimal point, so `shot-2` keeps its `-2`.
fn split_offset(s: &str) -> (&str, f64) {
    let Some(i) = s.rfind(['+', '-']) else { return (s, 0.0) };
    let num = &s[i + 1..];
    let ok = !num.is_empty()
        && num.matches('.').count() == 1
        && num.chars().all(|c| c.is_ascii_digit() || c == '.')
        && !num.ends_with('.');
    if !ok || i == 0 {
        return (s, 0.0);
    }
    match num.parse::<f64>() {
        Ok(v) => (&s[..i], if &s[i..=i] == "-" { -v } else { v }),
        Err(_) => (s, 0.0),
    }
}

/// Parses an anchor. `is_id` says whether a string is an existing element id; it settles `start:shot-2.5`-style
/// ambiguity in favour of the whole string being an id.
pub fn parse(s: &str, is_id: &dyn Fn(&str) -> bool) -> Result<Anchor, EditError> {
    let bad = |why: &str| {
        EditError::new("anchor_syntax", format!("bad anchor `{s}`: {why}"))
            .hint("use word:<spine-id>/<word>[#n][@end][±s], start:<id>, end:<id> or t:<seconds>, offsets like +0.30")
    };
    let (scheme, rest) = s.split_once(':').ok_or_else(|| bad("missing scheme"))?;
    match scheme {
        "t" => {
            let (body, off) = split_offset(rest);
            let t: f64 = body.parse().map_err(|_| bad("t: needs seconds"))?;
            Ok(Anchor { kind: AnchorKind::T { t }, offset: off })
        }
        "start" | "end" => {
            if rest.is_empty() {
                return Err(bad("missing id"));
            }
            let (id, off) = if is_id(rest) { (rest, 0.0) } else { split_offset(rest) };
            let id = id.to_string();
            let kind = if scheme == "start" { AnchorKind::Start { id } } else { AnchorKind::End { id } };
            Ok(Anchor { kind, offset: off })
        }
        "word" => {
            let (body, off) = split_offset(rest);
            let (body, end) = match body.strip_suffix("@end") {
                Some(b) => (b, true),
                None => (body, false),
            };
            let (body, n) = match body.rsplit_once('#') {
                Some((b, n)) => (b, n.parse::<usize>().map_err(|_| bad("#n needs a positive integer"))?),
                None => (body, 1),
            };
            if n == 0 {
                return Err(bad("#n is 1-based"));
            }
            let (spine, word) = body.rsplit_once('/').ok_or_else(|| bad("word: needs <spine-id>/<word>"))?;
            if spine.is_empty() || word.is_empty() {
                return Err(bad("word: needs <spine-id>/<word>"));
            }
            Ok(Anchor {
                kind: AnchorKind::Word { spine: spine.to_string(), word: word.to_string(), n, end },
                offset: off,
            })
        }
        _ => Err(bad(&format!("unknown scheme `{scheme}`"))),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn p(s: &str) -> Anchor {
        parse(s, &|id| id == "shot-2" || id == "a-0.5").unwrap()
    }

    #[test]
    fn words() {
        assert_eq!(
            p("word:why_b/Everest"),
            Anchor {
                kind: AnchorKind::Word { spine: "why_b".into(), word: "Everest".into(), n: 1, end: false },
                offset: 0.0
            }
        );
        let a = p("word:open_b/deck#2@end-0.12");
        assert_eq!(a.kind, AnchorKind::Word { spine: "open_b".into(), word: "deck".into(), n: 2, end: true });
        assert!((a.offset + 0.12).abs() < 1e-12);
        let b = p("word:scene/one/explain*+0.3");
        assert_eq!(b.kind, AnchorKind::Word { spine: "scene/one".into(), word: "explain*".into(), n: 1, end: false });
        assert!((b.offset - 0.3).abs() < 1e-12);
    }

    #[test]
    fn edges_and_offsets() {
        assert_eq!(p("start:why_a-0.25").kind, AnchorKind::Start { id: "why_a".into() });
        assert!((p("start:why_a-0.25").offset + 0.25).abs() < 1e-12);
        assert_eq!(p("end:open_c+0.30").offset, 0.3);
        // No decimal point: part of the id.
        assert_eq!(p("start:shot-2").kind, AnchorKind::Start { id: "shot-2".into() });
        assert_eq!(p("start:shot-2").offset, 0.0);
        // A whole-string id wins over an offset reading.
        assert_eq!(p("end:a-0.5").kind, AnchorKind::End { id: "a-0.5".into() });
        assert_eq!(p("t:12.5").kind, AnchorKind::T { t: 12.5 });
        assert_eq!(p("t:3-0.5").offset, -0.5);
    }

    #[test]
    fn syntax_errors() {
        for bad in ["", "word:", "word:noslash", "word:a/b#0", "word:a/b#x", "start:", "t:abc", "frame:3", "nocolon"] {
            let e = parse(bad, &|_| false).unwrap_err();
            assert_eq!(e.code, "anchor_syntax", "{bad}");
        }
    }
}
