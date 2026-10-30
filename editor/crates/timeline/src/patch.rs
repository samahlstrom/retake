//! Patches (spec/timeline.md, "Patches"): ops addressed by element id plus a path inside the element, applied
//! atomically, logged with their inverse, and reverted only when no later patch touched the same values.

use crate::component::LockMode;
use crate::error::{EditError, Errors, ptr_escape, ptr_unescape};
use crate::project::Project;
use crate::resolve::{collect_ids, validate};
use crate::schema;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::io::Write;
use std::path::Path;

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, Default)]
pub struct Dest {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub id: Option<String>,
    pub path: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub before: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub after: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub index: Option<usize>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(tag = "op", rename_all = "lowercase")]
pub enum Op {
    Set {
        #[serde(default, skip_serializing_if = "Option::is_none")]
        id: Option<String>,
        path: String,
        value: Value,
    },
    Insert {
        #[serde(default, skip_serializing_if = "Option::is_none")]
        id: Option<String>,
        path: String,
        value: Value,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        before: Option<String>,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        after: Option<String>,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        index: Option<usize>,
    },
    Remove {
        #[serde(default, skip_serializing_if = "Option::is_none")]
        id: Option<String>,
        #[serde(default, skip_serializing_if = "Option::is_none")]
        path: Option<String>,
    },
    Move {
        id: String,
        to: Dest,
    },
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct Patch {
    pub base: u64,
    pub answers: Vec<String>,
    pub summary: String,
    pub ops: Vec<Op>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reverts: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct LogEntry {
    pub id: String,
    pub rev: u64,
    pub base: u64,
    pub answers: Vec<String>,
    pub summary: String,
    pub ops: Vec<Op>,
    pub inverse: Vec<Op>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub reverts: Option<String>,
    pub applied: String,
}

fn err(code: &str, msg: impl Into<String>, at: &str) -> EditError {
    EditError::new(code, msg).at(at.to_string())
}

/// JSON pointer of the element with this id (`""` for the root when `id` is None).
fn element_ptr(doc: &Value, id: Option<&str>, at: &str) -> Result<String, EditError> {
    let Some(id) = id else { return Ok(String::new()) };
    let ids = collect_ids(doc);
    ids.iter().find(|(i, _)| i == id).map(|(_, p)| p.clone()).ok_or_else(|| {
        err("patch_target_not_found", format!("no element with id `{id}`"), at).suggest(crate::error::close_matches(
            id,
            ids.iter().map(|(i, _)| i.as_str()),
            3,
        ))
    })
}

fn split_path(path: &str) -> Vec<String> {
    path.trim_matches('/').split('/').filter(|s| !s.is_empty()).map(ptr_unescape).collect()
}

/// Walks `segs` from `base`. Every segment but the last must exist. Returns the parent's pointer and the last token
/// (an object key, or an array index found by item id or number).
fn navigate(doc: &Value, base: &str, segs: &[String], at: &str) -> Result<(String, String), EditError> {
    let mut ptr = base.to_string();
    for (i, s) in segs.iter().enumerate() {
        let cur = doc.pointer(&ptr).ok_or_else(|| err("patch_path_invalid", format!("{ptr} doesn't exist"), at))?;
        let tok = match cur {
            Value::Object(_) => ptr_escape(s),
            Value::Array(a) => {
                let by_id = a.iter().position(|x| x.get("id").and_then(Value::as_str) == Some(s.as_str()));
                match by_id {
                    Some(k) => k.to_string(),
                    None => match s.parse::<usize>() {
                        Ok(k) if k < a.len() || i == segs.len() - 1 => k.to_string(),
                        _ => {
                            return Err(err(
                                "patch_path_invalid",
                                format!("`{s}` is neither an item id nor an index in the list at {ptr}"),
                                at,
                            ));
                        }
                    },
                }
            }
            _ => return Err(err("patch_path_invalid", format!("{ptr} is not an object or a list"), at)),
        };
        if i == segs.len() - 1 {
            return Ok((ptr, tok));
        }
        ptr = format!("{ptr}/{tok}");
    }
    Err(err("patch_path_invalid", "empty path", at))
}

/// The element that contains the list at `list_ptr`, and the list's path relative to it.
fn container_of(doc: &Value, list_ptr: &str) -> (Option<String>, String) {
    let mut best: Option<(String, String)> = None;
    for (id, p) in collect_ids(doc) {
        if list_ptr.starts_with(&format!("{p}/")) && best.as_ref().is_none_or(|(_, bp)| p.len() > bp.len()) {
            best = Some((id, p));
        }
    }
    match best {
        Some((id, p)) => (Some(id), list_ptr[p.len() + 1..].to_string()),
        None => (None, list_ptr.trim_start_matches('/').to_string()),
    }
}

fn parent_list(ptr: &str) -> (String, usize) {
    let (parent, idx) = ptr.rsplit_once('/').unwrap_or(("", ptr));
    (parent.to_string(), idx.parse().unwrap_or(0))
}

/// Inserts into the list at (`id`, `path`). Returns the index used.
fn insert_at(
    doc: &mut Value,
    id: Option<&str>,
    path: &str,
    value: Value,
    pos: (&Option<String>, &Option<String>, &Option<usize>),
    at: &str,
) -> Result<usize, EditError> {
    let base = element_ptr(doc, id, at)?;
    let segs = split_path(path);
    if segs.is_empty() {
        return Err(err("patch_path_invalid", "insert needs a list path", at));
    }
    let (parent, tok) = navigate(doc, &base, &segs, at)?;
    let list_ptr = format!("{parent}/{tok}");
    let list = doc
        .pointer_mut(&list_ptr)
        .and_then(Value::as_array_mut)
        .ok_or_else(|| err("patch_path_invalid", format!("{list_ptr} is not a list"), at))?;
    let find = |sib: &str, list: &Vec<Value>| {
        list.iter()
            .position(|x| x.get("id").and_then(Value::as_str) == Some(sib))
            .ok_or_else(|| err("patch_target_not_found", format!("no item `{sib}` in the list at {list_ptr}"), at))
    };
    let idx = match pos {
        (Some(b), _, _) => find(b, list)?,
        (_, Some(a), _) => find(a, list)? + 1,
        (_, _, Some(i)) if *i <= list.len() => *i,
        (_, _, Some(i)) => {
            return Err(err(
                "patch_path_invalid",
                format!("index {i} is past the end of {list_ptr} ({})", list.len()),
                at,
            ));
        }
        _ => list.len(),
    };
    list.insert(idx, value);
    Ok(idx)
}

fn remove_element(doc: &mut Value, id: &str, at: &str) -> Result<(Value, Option<String>, String, usize), EditError> {
    let ptr = element_ptr(doc, Some(id), at)?;
    let (list_ptr, idx) = parent_list(&ptr);
    let (cid, rel) = container_of(doc, &list_ptr);
    let list = doc.pointer_mut(&list_ptr).and_then(Value::as_array_mut).expect("element ptr points into a list");
    let v = list.remove(idx);
    Ok((v, cid, rel, idx))
}

/// Applies one op; returns the op that undoes it.
fn apply_op(doc: &mut Value, op: &Op, at: &str) -> Result<Op, EditError> {
    match op {
        Op::Set { id, path, value } => {
            let base = element_ptr(doc, id.as_deref(), at)?;
            let segs = split_path(path);
            if segs.is_empty() {
                if id.is_none() {
                    return Err(err("patch_path_invalid", "set with no id needs a path", at));
                }
                if value.get("id") != doc.pointer(&base).and_then(|v| v.get("id")) {
                    return Err(err("patch_id_change", "replacing an element must keep its id", at)
                        .hint("remove it and insert a new one to change an id"));
                }
                let slot = doc.pointer_mut(&base).expect("element exists");
                let old = std::mem::replace(slot, value.clone());
                return Ok(Op::Set { id: id.clone(), path: path.clone(), value: old });
            }
            if id.is_none() && matches!(segs[0].as_str(), "rev" | "schema") {
                return Err(err("patch_path_invalid", format!("`{}` can't be patched", segs[0]), at));
            }
            let (parent, tok) = navigate(doc, &base, &segs, at)?;
            let slot = doc.pointer_mut(&parent).expect("navigate checked the parent");
            let old = match slot {
                Value::Object(m) => m.insert(ptr_unescape(&tok), value.clone()),
                Value::Array(a) => {
                    let k: usize = tok.parse().unwrap_or(usize::MAX);
                    if k >= a.len() {
                        return Err(err("patch_path_invalid", format!("index {k} is past the end of {parent}"), at)
                            .hint("use insert to add to a list"));
                    }
                    Some(std::mem::replace(&mut a[k], value.clone()))
                }
                _ => return Err(err("patch_path_invalid", format!("{parent} is not an object or a list"), at)),
            };
            Ok(match old {
                Some(v) => Op::Set { id: id.clone(), path: path.clone(), value: v },
                None => Op::Remove { id: id.clone(), path: Some(path.clone()) },
            })
        }
        Op::Insert { id, path, value, before, after, index } => {
            let idx = insert_at(doc, id.as_deref(), path, value.clone(), (before, after, index), at)?;
            Ok(match value.get("id").and_then(Value::as_str) {
                Some(vid) => Op::Remove { id: Some(vid.to_string()), path: None },
                None => Op::Remove { id: id.clone(), path: Some(format!("{}/{idx}", path.trim_end_matches('/'))) },
            })
        }
        Op::Remove { id, path: None } => {
            let id = id.as_deref().ok_or_else(|| err("patch_path_invalid", "remove needs an id or a path", at))?;
            let (v, cid, rel, idx) = remove_element(doc, id, at)?;
            Ok(Op::Insert { id: cid, path: rel, value: v, before: None, after: None, index: Some(idx) })
        }
        Op::Remove { id, path: Some(path) } => {
            let base = element_ptr(doc, id.as_deref(), at)?;
            let segs = split_path(path);
            if segs.is_empty() {
                return Err(err("patch_path_invalid", "empty path", at));
            }
            let (parent, tok) = navigate(doc, &base, &segs, at)?;
            let slot = doc.pointer_mut(&parent).expect("navigate checked the parent");
            match slot {
                Value::Object(m) => {
                    let old = m.shift_remove(&ptr_unescape(&tok)).ok_or_else(|| {
                        err("patch_path_invalid", format!("`{}` doesn't exist in {parent}", ptr_unescape(&tok)), at)
                    })?;
                    Ok(Op::Set { id: id.clone(), path: path.clone(), value: old })
                }
                Value::Array(a) => {
                    let k: usize = tok.parse().unwrap_or(usize::MAX);
                    if k >= a.len() {
                        return Err(err("patch_path_invalid", format!("index {k} is past the end of {parent}"), at));
                    }
                    let old = a.remove(k);
                    let ppath = segs[..segs.len() - 1].iter().map(|s| ptr_escape(s)).collect::<Vec<_>>().join("/");
                    Ok(Op::Insert {
                        id: id.clone(),
                        path: ppath,
                        value: old,
                        before: None,
                        after: None,
                        index: Some(k),
                    })
                }
                _ => Err(err("patch_path_invalid", format!("{parent} is not an object or a list"), at)),
            }
        }
        Op::Move { id, to } => {
            let (v, cid, rel, idx) = remove_element(doc, id, at)?;
            insert_at(doc, to.id.as_deref(), &to.path, v, (&to.before, &to.after, &to.index), at)?;
            Ok(Op::Move {
                id: id.clone(),
                to: Dest { id: cid, path: rel, before: None, after: None, index: Some(idx) },
            })
        }
    }
}

/// Applies ops to a document. Returns the new document and the inverse ops (in undo order).
pub fn apply_ops(doc: &Value, ops: &[Op]) -> Result<(Value, Vec<Op>), EditError> {
    let mut d = doc.clone();
    let mut inv = Vec::with_capacity(ops.len());
    for (i, op) in ops.iter().enumerate() {
        inv.push(apply_op(&mut d, op, &format!("/ops/{i}"))?);
    }
    inv.reverse();
    Ok((d, inv))
}

pub fn read_log(path: &Path) -> Result<Vec<LogEntry>, EditError> {
    let text = match std::fs::read_to_string(path) {
        Ok(t) => t,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(vec![]),
        Err(e) => return Err(EditError::new("log_unreadable", format!("can't read {}: {e}", path.display()))),
    };
    text.lines()
        .enumerate()
        .filter(|(_, l)| !l.trim().is_empty())
        .map(|(n, l)| {
            serde_json::from_str(l)
                .map_err(|e| EditError::new("log_invalid", format!("{} line {}: {e}", path.display(), n + 1)))
        })
        .collect()
}

/// UTC "YYYY-MM-DDTHH:MM:SSZ" without a date library (Howard Hinnant's civil_from_days).
pub fn utc_now() -> String {
    let secs =
        std::time::SystemTime::now().duration_since(std::time::UNIX_EPOCH).map(|d| d.as_secs()).unwrap_or(0) as i64;
    let (days, rem) = (secs.div_euclid(86400), secs.rem_euclid(86400));
    let z = days + 719468;
    let era = z.div_euclid(146097);
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    let y = yoe + era * 400 + if m <= 2 { 1 } else { 0 };
    format!("{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}Z", rem / 3600, rem % 3600 / 60, rem % 60)
}

/// Writes JSON readably: objects and long lists one item per line, short scalar lists and small objects inline.
pub fn pretty(v: &Value) -> String {
    fn inline(v: &Value) -> String {
        serde_json::to_string(v).unwrap_or_default().replace("\":", "\": ").replace(",\"", ", \"")
    }
    fn go(v: &Value, ind: usize, out: &mut String) {
        let pad = "  ".repeat(ind + 1);
        let short = inline(v);
        let small = short.len() + ind * 2 <= 100
            && match v {
                Value::Array(a) => a.iter().all(|x| !x.is_object() && !x.is_array()) || ind >= 3,
                Value::Object(_) => ind >= 3,
                _ => true,
            };
        if small {
            out.push_str(&short);
            return;
        }
        match v {
            Value::Object(m) => {
                out.push_str("{\n");
                for (i, (k, x)) in m.iter().enumerate() {
                    out.push_str(&pad);
                    out.push_str(&serde_json::to_string(k).unwrap_or_default());
                    out.push_str(": ");
                    go(x, ind + 1, out);
                    if i + 1 < m.len() {
                        out.push(',');
                    }
                    out.push('\n');
                }
                out.push_str(&"  ".repeat(ind));
                out.push('}');
            }
            Value::Array(a) => {
                out.push_str("[\n");
                for (i, x) in a.iter().enumerate() {
                    out.push_str(&pad);
                    go(x, ind + 1, out);
                    if i + 1 < a.len() {
                        out.push(',');
                    }
                    out.push('\n');
                }
                out.push_str(&"  ".repeat(ind));
                out.push(']');
            }
            _ => out.push_str(&short),
        }
    }
    let mut s = String::new();
    go(v, 0, &mut s);
    s.push('\n');
    s
}

fn write_atomic(path: &Path, text: &str) -> Result<(), EditError> {
    let tmp = path.with_extension("json.tmp");
    std::fs::write(&tmp, text)
        .and_then(|_| std::fs::rename(&tmp, path))
        .map_err(|e| EditError::new("timeline_write", format!("can't write {}: {e}", path.display())))
}

/// The result of applying a patch: the new project and its log entry.
pub struct Applied {
    pub project: Project,
    pub entry: LogEntry,
}

/// Applies a patch in memory: base check, ops, schema and semantic validation (only errors the patch introduces
/// count, so a missing media file elsewhere doesn't block an unrelated fix).
pub fn apply(p: &Project, patch_json: &Value) -> Result<Applied, Errors> {
    let errs = schema::check_patch(patch_json);
    if !errs.is_empty() {
        return Err(Errors(errs));
    }
    let patch: Patch = serde_json::from_value(patch_json.clone())
        .map_err(|e| EditError::new("patch_invalid", format!("patch doesn't fit the model: {e}")))?;
    if patch.base != p.tl.rev {
        return Err(EditError::new(
            "patch_base_mismatch",
            format!("patch base {} isn't the timeline's rev {}", patch.base, p.tl.rev),
        )
        .at("/base")
        .hint("re-read the timeline and rebase the patch")
        .into());
    }
    let (mut doc, inverse) = apply_ops(&p.src, &patch.ops).map_err(|e| component_layer_hint(p, e))?;
    let rev = p.tl.rev + 1;
    doc["rev"] = Value::from(rev);
    // A patch that starts using a package pins it (spec/components.md, "The lockfile").
    let np = Project::open(&p.path, doc, LockMode::PinNew, Some(p.lock.clone()))?;
    let before = validate(p);
    let new_errs: Vec<EditError> = validate(&np).into_iter().filter(|e| !before.contains(e)).collect();
    if !new_errs.is_empty() {
        return Err(Errors(new_errs));
    }
    let entry = LogEntry {
        id: format!("p{rev}"),
        rev,
        base: patch.base,
        answers: patch.answers,
        summary: patch.summary,
        ops: patch.ops,
        inverse,
        reverts: patch.reverts,
        applied: utc_now(),
    };
    Ok(Applied { project: np, entry })
}

/// A patch aimed at a layer a component made: say so, and point at the element's props.
fn component_layer_hint(p: &Project, e: EditError) -> EditError {
    if e.code != "patch_target_not_found" {
        return e;
    }
    let id = e.message.split('`').nth(1).unwrap_or_default().to_string();
    match p.uses.iter().find(|u| id.starts_with(&format!("{}/", u.element))) {
        Some(u) => {
            let element = u.element.clone();
            let reference = u.reference.clone();
            e.hint(format!(
                "`{id}` is a layer that component {reference} makes for `{element}`: set `props` on `{element}` \
                 instead (`retake-edit expand` shows the expansion)"
            ))
        }
        None => e,
    }
}

/// Writes an applied patch: the timeline (atomically), the lockfile when the patch pinned a package, then one line
/// in the log.
pub fn commit(a: &Applied) -> Result<(), EditError> {
    write_atomic(&a.project.path, &pretty(&a.project.src))?;
    if a.project.lock_changed {
        a.project.lock.write(&a.project.lock_path())?;
    }
    let line = serde_json::to_string(&a.entry).expect("log entry serializes");
    let log = a.project.log_path();
    let mut f = std::fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log)
        .map_err(|e| EditError::new("log_write", format!("can't open {}: {e}", log.display())))?;
    writeln!(f, "{line}").map_err(|e| EditError::new("log_write", format!("can't append to {}: {e}", log.display())))
}

/// Builds the patch that reverts `patch_id`. Refused when a later patch changed the same values: undoing then redoing
/// the patch on today's timeline must give today's timeline back.
pub fn revert_patch(p: &Project, patch_id: &str) -> Result<Value, EditError> {
    let log = read_log(&p.log_path())?;
    let e = log.iter().find(|e| e.id == patch_id).ok_or_else(|| {
        EditError::new("patch_not_found", format!("no patch `{patch_id}` in {}", p.log_path().display()))
            .suggest(log.iter().rev().take(5).map(|e| e.id.clone()).collect())
    })?;
    let conflict = || {
        let later: Vec<String> = log.iter().filter(|l| l.rev > e.rev).map(|l| l.id.clone()).collect();
        EditError::new(
            "patch_conflict",
            format!("a later patch changed what {patch_id} changed (later: {})", later.join(", ")),
        )
        .hint("revert the later patch first")
    };
    let (undone, _) = apply_ops(&p.src, &e.inverse).map_err(|_| conflict())?;
    let (redone, _) = apply_ops(&undone, &e.ops).map_err(|_| conflict())?;
    if redone != p.src {
        return Err(conflict());
    }
    Ok(serde_json::json!({
        "base": p.tl.rev,
        "answers": [],
        "summary": format!("revert {patch_id}: {}", e.summary),
        "ops": e.inverse,
        "reverts": patch_id,
    }))
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn doc() -> Value {
        json!({
            "schema": "timeline/0.1", "id": "t", "rev": 0,
            "assets": {"proj:a/b": {"path": "x"}},
            "spine": [{"id": "a", "in": 0, "out": 1}, {"id": "b", "in": 1, "out": 2}],
            "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a"}], "overlays": [
                {"id": "o", "from": "start:a", "to": "end:a", "layers": [{"id": "o/t", "type": "text", "text": "HI"}]}
            ]}]
        })
    }

    fn ops(v: Value) -> Vec<Op> {
        serde_json::from_value(v).unwrap()
    }

    fn roundtrip(ops: &[Op]) -> Value {
        let d = doc();
        let (n, inv) = apply_ops(&d, ops).unwrap();
        let (back, _) = apply_ops(&n, &inv).unwrap();
        assert_eq!(back, d, "inverse restores the document");
        n
    }

    #[test]
    fn set_by_id_and_path() {
        let n = roundtrip(&ops(json!([
            {"op": "set", "id": "o/t", "path": "text", "value": "YO"},
            {"op": "set", "id": "o/t", "path": "size", "value": 90},
            {"op": "set", "id": "s", "path": "overlays/o/to", "value": "end:b"},
            {"op": "set", "path": "assets/proj:a~1b/path", "value": "y"}
        ])));
        assert_eq!(n["scenes"][0]["overlays"][0]["layers"][0]["text"], "YO");
        assert_eq!(n["scenes"][0]["overlays"][0]["layers"][0]["size"], 90);
        assert_eq!(n["scenes"][0]["overlays"][0]["to"], "end:b");
        assert_eq!(n["assets"]["proj:a/b"]["path"], "y");
    }

    #[test]
    fn insert_remove_move() {
        let n = roundtrip(&ops(json!([
            {"op": "insert", "path": "spine", "value": {"id": "c", "in": 2, "out": 3}, "after": "a"},
            {"op": "insert", "id": "o", "path": "layers", "value": {"id": "o/bg", "type": "shape"}, "index": 0},
            {"op": "remove", "id": "b"},
            {"op": "move", "id": "o/t", "to": {"id": "o", "path": "layers", "index": 0}},
            {"op": "remove", "id": "o/t", "path": "text"}
        ])));
        let ids: Vec<&str> = n["spine"].as_array().unwrap().iter().map(|x| x["id"].as_str().unwrap()).collect();
        assert_eq!(ids, ["a", "c"]);
        assert_eq!(n["scenes"][0]["overlays"][0]["layers"][0]["id"], "o/t");
        assert!(n["scenes"][0]["overlays"][0]["layers"][0].get("text").is_none());
    }

    #[test]
    fn replace_whole_element_keeps_id() {
        roundtrip(&ops(json!([{"op": "set", "id": "b", "path": "", "value": {"id": "b", "in": 5, "out": 6}}])));
        let e =
            apply_ops(&doc(), &ops(json!([{"op": "set", "id": "b", "path": "", "value": {"id": "z"}}]))).unwrap_err();
        assert_eq!(e.code, "patch_id_change");
    }

    #[test]
    fn errors_are_typed() {
        let code = |v: Value| apply_ops(&doc(), &ops(v)).unwrap_err();
        let e = code(json!([{"op": "set", "id": "o/text", "path": "text", "value": 1}]));
        assert_eq!(e.code, "patch_target_not_found");
        assert!(e.suggestions.contains(&"o/t".to_string()), "{e:?}");
        assert_eq!(e.path.as_deref(), Some("/ops/0"));
        assert_eq!(code(json!([{"op": "set", "id": "o", "path": "nope/x", "value": 1}])).code, "patch_path_invalid");
        assert_eq!(code(json!([{"op": "set", "path": "rev", "value": 9}])).code, "patch_path_invalid");
        assert_eq!(
            code(json!([{"op": "insert", "path": "spine", "value": {}, "after": "zz"}])).code,
            "patch_target_not_found"
        );
    }

    #[test]
    fn pretty_is_valid_json() {
        let d = doc();
        let s = pretty(&d);
        assert_eq!(serde_json::from_str::<Value>(&s).unwrap(), d);
        assert!(s.contains("\"in\": 0"));
    }

    #[test]
    fn utc_format() {
        let s = utc_now();
        assert_eq!(s.len(), 20);
        assert!(s.ends_with('Z') && s.starts_with("20"));
    }
}
