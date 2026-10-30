//! Components, brand kits and the lockfile (spec/components.md): finding and hashing packages, kit tokens, and the
//! expansion of `use` sites into plain layers. The renderer only ever sees the expansion.

use crate::error::{EditError, close_matches, ptr_escape};
use crate::keyframe::ExprEval;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::path::{Path, PathBuf};
use std::sync::OnceLock;

pub const COMPONENT_SCHEMA: &str = "retake-component/0.1";
pub const KIT_SCHEMA: &str = "retake-kit/0.1";
pub const LOCK_SCHEMA: &str = "retake-lock/0.1";
/// The design canvas when a component doesn't say.
pub const DEFAULT_CANVAS: [u32; 2] = [1920, 1080];
/// Licenses a package may carry (SPDX ids).
pub const ALLOWED_LICENSES: &[&str] =
    &["CC0-1.0", "MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "OFL-1.1", "CC-BY-4.0"];

// ---------------------------------------------------------------------------------------------------------------
// The expression engine for `$expr` templates and lint rules. The timeline crate has none of its own (it stays free
// of JavaScript); the renderer registers QuickJS at startup.

/// Makes an expression engine on the calling thread.
pub type EvalFactory = fn() -> Result<Box<dyn ExprEval>, EditError>;

static EVALUATOR: OnceLock<EvalFactory> = OnceLock::new();

/// Registers the engine `$expr` templates and custom lint rules run in. The first registration wins.
pub fn set_evaluator(f: EvalFactory) {
    let _ = EVALUATOR.set(f);
}

/// A new engine, or `expr_unavailable` when none was registered.
pub fn evaluator() -> Result<Box<dyn ExprEval>, EditError> {
    match EVALUATOR.get() {
        Some(f) => f(),
        None => Err(EditError::new("expr_unavailable", "no expression engine is registered")
            .hint("call retake_render::init() (retake-edit does) before loading a timeline that uses $expr")),
    }
}

// ---------------------------------------------------------------------------------------------------------------
// Packages.

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct FontInput {
    pub path: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub weight: Option<f64>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct ImageInput {
    pub path: String,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Inputs {
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub fonts: BTreeMap<String, FontInput>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub images: BTreeMap<String, ImageInput>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Example {
    pub name: String,
    #[serde(default)]
    pub props: Value,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub duration: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
}

/// A `component.json`.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Component {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub schema: Option<String>,
    pub name: String,
    pub version: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub description: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub license: Option<String>,
    #[serde(default)]
    pub authors: Vec<String>,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub category: Option<String>,
    #[serde(default = "default_use_as")]
    pub use_as: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub canvas: Option<[u32; 2]>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub duration: Option<f64>,
    #[serde(default = "empty_object")]
    pub props: Value,
    #[serde(default = "empty_object")]
    pub tokens: Value,
    #[serde(default)]
    pub inputs: Inputs,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub background: Option<Value>,
    #[serde(default)]
    pub layers: Vec<Value>,
    #[serde(default)]
    pub examples: Vec<Example>,
    #[serde(default)]
    pub changelog: Vec<Value>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub meta: Option<Value>,
}

fn default_use_as() -> Vec<String> {
    vec!["overlay".into()]
}

fn empty_object() -> Value {
    json!({})
}

impl Component {
    pub fn reference(&self) -> String {
        format!("{}@{}", self.name, self.version)
    }
    pub fn canvas(&self) -> [u32; 2] {
        self.canvas.unwrap_or(DEFAULT_CANVAS)
    }
    /// The props' defaults, from the schema's `properties.*.default`.
    pub fn defaults(&self) -> Map<String, Value> {
        let mut out = Map::new();
        if let Some(ps) = self.props.get("properties").and_then(Value::as_object) {
            for (k, v) in ps {
                if let Some(d) = v.get("default") {
                    out.insert(k.clone(), d.clone());
                }
            }
        }
        out
    }
    /// Props that take an asset id of the timeline: name → `video`, `image` or `audio`.
    pub fn asset_inputs(&self) -> BTreeMap<String, String> {
        let mut out = BTreeMap::new();
        if let Some(ps) = self.props.get("properties").and_then(Value::as_object) {
            for (k, v) in ps {
                if let Some(kind) = v.get("x-retake-input").and_then(Value::as_str) {
                    out.insert(k.clone(), kind.to_string());
                }
            }
        }
        out
    }
}

/// A `kit.json` (the catalog's kit format plus `components` and `rules`). Unknown fields are kept for the catalog.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Kit {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub schema: Option<String>,
    pub name: String,
    pub version: String,
    #[serde(default)]
    pub title: String,
    #[serde(default)]
    pub description: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub license: Option<Value>,
    #[serde(default = "empty_object")]
    pub tokens: Value,
    #[serde(default)]
    pub components: Vec<String>,
    #[serde(default)]
    pub rules: Vec<Value>,
    #[serde(flatten)]
    pub extra: BTreeMap<String, Value>,
}

impl Kit {
    /// The SPDX id, whether `license` is a string or the catalog's `{status, credit}` object (then its `spdx`).
    pub fn spdx(&self) -> Option<String> {
        match &self.license {
            Some(Value::String(s)) => Some(s.clone()),
            Some(Value::Object(m)) => m.get("spdx").and_then(Value::as_str).map(String::from),
            _ => None,
        }
    }
}

/// `<name>@<version>` split, with the version checked as semver.
pub fn parse_ref(r: &str) -> Option<(String, String)> {
    let (n, v) = r.split_once('@')?;
    let name_ok = n.chars().next().is_some_and(|c| c.is_ascii_lowercase())
        && n.chars().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-');
    (name_ok && is_semver(v)).then(|| (n.to_string(), v.to_string()))
}

/// `MAJOR.MINOR.PATCH`, maybe with a `-pre` suffix.
pub fn is_semver(v: &str) -> bool {
    let core = v.split_once('-').map(|(c, p)| (c, Some(p))).unwrap_or((v, None));
    let nums: Vec<&str> = core.0.split('.').collect();
    nums.len() == 3
        && nums.iter().all(|n| !n.is_empty() && n.chars().all(|c| c.is_ascii_digit()))
        && core.1.is_none_or(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-'))
}

/// Compares two semver strings (pre-releases sort before their release).
pub fn cmp_semver(a: &str, b: &str) -> std::cmp::Ordering {
    let parts = |v: &str| -> (Vec<u64>, Option<String>) {
        let (core, pre) = v.split_once('-').map(|(c, p)| (c, Some(p.to_string()))).unwrap_or((v, None));
        (core.split('.').map(|x| x.parse().unwrap_or(0)).collect(), pre)
    };
    let (ca, pa) = parts(a);
    let (cb, pb) = parts(b);
    ca.cmp(&cb).then_with(|| match (pa, pb) {
        (None, None) => std::cmp::Ordering::Equal,
        (None, Some(_)) => std::cmp::Ordering::Greater,
        (Some(_), None) => std::cmp::Ordering::Less,
        (Some(x), Some(y)) => x.cmp(&y),
    })
}

/// The content hash of a package folder (spec/components.md, "The lockfile"): for every file sorted by relative path,
/// skipping `previews/` and hidden files and folders, the path, NUL, the file's BLAKE3 in hex, a newline.
pub fn package_hash(dir: &Path) -> Result<String, EditError> {
    let files = package_files(dir)?;
    let mut h = blake3::Hasher::new();
    for (rel, path) in files {
        let bytes = std::fs::read(&path)
            .map_err(|e| EditError::new("package_unreadable", format!("can't read {}: {e}", path.display())))?;
        h.update(rel.as_bytes());
        h.update(b"\0");
        h.update(blake3::hash(&bytes).to_hex().as_bytes());
        h.update(b"\n");
    }
    Ok(format!("b3:{}", h.finalize().to_hex()))
}

/// A package's files: (relative path with `/`, absolute path), sorted, without `previews/`, hidden entries and links.
pub fn package_files(dir: &Path) -> Result<Vec<(String, PathBuf)>, EditError> {
    fn walk(root: &Path, dir: &Path, out: &mut Vec<(String, PathBuf)>) -> Result<(), EditError> {
        let rd = std::fs::read_dir(dir)
            .map_err(|e| EditError::new("package_unreadable", format!("can't list {}: {e}", dir.display())))?;
        for ent in rd {
            let ent = ent.map_err(|e| EditError::new("package_unreadable", e.to_string()))?;
            let name = ent.file_name().to_string_lossy().into_owned();
            if name.starts_with('.') {
                continue;
            }
            let path = ent.path();
            let ft = ent.file_type().map_err(|e| EditError::new("package_unreadable", e.to_string()))?;
            let rel = path.strip_prefix(root).unwrap_or(&path).to_string_lossy().replace('\\', "/");
            if ft.is_dir() {
                if rel == "previews" {
                    continue;
                }
                walk(root, &path, out)?;
            } else if ft.is_file() {
                out.push((rel, path));
            }
        }
        Ok(())
    }
    let mut out = vec![];
    walk(dir, dir, &mut out)?;
    out.sort_by(|a, b| a.0.cmp(&b.0));
    Ok(out)
}

/// A component package on disk.
#[derive(Debug, Clone)]
pub struct Package {
    pub dir: PathBuf,
    pub comp: Component,
    pub hash: String,
}

/// Reads and checks a component folder.
pub fn load_component(dir: &Path) -> Result<Package, EditError> {
    let f = dir.join("component.json");
    let text = std::fs::read_to_string(&f)
        .map_err(|e| EditError::new("component_unreadable", format!("can't read {}: {e}", f.display())))?;
    let comp: Component = serde_json::from_str(&text).map_err(|e| {
        EditError::new("component_invalid", format!("{}: {e}", f.display()))
            .hint("component.json fields: spec/components.md, \"A component\"")
    })?;
    check_component(&comp).map_err(|e| EditError { message: format!("{}: {}", f.display(), e.message), ..e })?;
    Ok(Package { dir: dir.to_path_buf(), hash: package_hash(dir)?, comp })
}

/// The structural rules of a component.json that serde doesn't check.
pub fn check_component(c: &Component) -> Result<(), EditError> {
    let bad = |m: String| Err(EditError::new("component_invalid", m));
    if let Some(s) = &c.schema
        && s != COMPONENT_SCHEMA
    {
        return bad(format!("schema `{s}`; this editor reads {COMPONENT_SCHEMA}"));
    }
    if parse_ref(&format!("{}@{}", c.name, c.version)).is_none() {
        return bad(format!("`{}@{}`: the name is a-z, 0-9 and -, the version semver (1.0.0)", c.name, c.version));
    }
    for u in &c.use_as {
        if u != "overlay" && u != "picture" {
            return bad(format!("use_as `{u}`: overlay or picture"));
        }
    }
    if c.props.get("type").and_then(Value::as_str).is_some_and(|t| t != "object") {
        return bad("props must be a JSON Schema of type object".into());
    }
    if let Some([w, h]) = c.canvas
        && (w < 2 || h < 2)
    {
        return bad("canvas is [w, h] in pixels".into());
    }
    let mut ids = BTreeSet::new();
    for (i, l) in c.layers.iter().enumerate() {
        let id = l.get("id").and_then(Value::as_str).unwrap_or_default();
        if id.is_empty() || !ids.insert(id.to_string()) {
            return bad(format!("layers/{i}: every layer needs an id, unique in the component"));
        }
    }
    Ok(())
}

/// Reads a kit folder.
pub fn load_kit(dir: &Path) -> Result<(Kit, String), EditError> {
    let f = dir.join("kit.json");
    let text = std::fs::read_to_string(&f)
        .map_err(|e| EditError::new("kit_unreadable", format!("can't read {}: {e}", f.display())))?;
    let kit: Kit =
        serde_json::from_str(&text).map_err(|e| EditError::new("kit_invalid", format!("{}: {e}", f.display())))?;
    if let Some(s) = &kit.schema
        && s != KIT_SCHEMA
    {
        return Err(EditError::new("kit_invalid", format!("{}: schema `{s}`, not {KIT_SCHEMA}", f.display())));
    }
    Ok((kit, package_hash(dir)?))
}

// ---------------------------------------------------------------------------------------------------------------
// Where packages are found.

/// The user's library: `$RETAKE_LIBRARY`, else `$XDG_DATA_HOME/retake/library`, else `~/.local/share/retake/library`.
pub fn library_dir() -> Option<PathBuf> {
    let var = |k: &str| std::env::var_os(k).filter(|v| !v.is_empty()).map(PathBuf::from);
    var("RETAKE_LIBRARY")
        .or_else(|| var("XDG_DATA_HOME").map(|d| d.join("retake").join("library")))
        .or_else(|| var("HOME").map(|h| h.join(".local").join("share").join("retake").join("library")))
}

/// The folders packages are looked for in: `components/` next to the timeline, `$RETAKE_COMPONENT_PATH`, then the
/// user library's `components/`.
pub fn search_roots(timeline_dir: &Path) -> Vec<PathBuf> {
    let mut out = vec![timeline_dir.join("components")];
    if let Some(p) = std::env::var_os("RETAKE_COMPONENT_PATH") {
        out.extend(std::env::split_paths(&p).filter(|p| !p.as_os_str().is_empty()));
    }
    if let Some(l) = library_dir() {
        out.push(l.join("components"));
    }
    out
}

/// The version a package folder holds (`component.json` or `kit.json`), if any.
fn folder_version(dir: &Path, file: &str) -> Option<String> {
    let text = std::fs::read_to_string(dir.join(file)).ok()?;
    let v: Value = serde_json::from_str(&text).ok()?;
    v.get("version").and_then(Value::as_str).map(String::from)
}

/// Finds `<name>@<version>` in the roots: `<root>/<name>@<version>/`, then `<root>/<name>/` holding that version.
pub fn find_package(roots: &[PathBuf], name: &str, version: &str, file: &str) -> Option<PathBuf> {
    for r in roots {
        let exact = r.join(format!("{name}@{version}"));
        if exact.join(file).is_file() {
            return Some(exact);
        }
        let plain = r.join(name);
        if plain.join(file).is_file() && folder_version(&plain, file).as_deref() == Some(version) {
            return Some(plain);
        }
    }
    None
}

/// Every `<name>@<version>` with a `file` in the roots (for suggestions and listings).
pub fn available(roots: &[PathBuf], file: &str) -> Vec<(String, PathBuf)> {
    let mut out = vec![];
    for r in roots {
        let Ok(rd) = std::fs::read_dir(r) else { continue };
        let mut ents: Vec<PathBuf> = rd.filter_map(|e| e.ok().map(|e| e.path())).collect();
        ents.sort();
        for d in ents {
            if !d.join(file).is_file() {
                continue;
            }
            let Ok(text) = std::fs::read_to_string(d.join(file)) else { continue };
            let Ok(v) = serde_json::from_str::<Value>(&text) else { continue };
            if let (Some(n), Some(ver)) = (v["name"].as_str(), v["version"].as_str()) {
                out.push((format!("{n}@{ver}"), d));
            }
        }
    }
    out
}

// ---------------------------------------------------------------------------------------------------------------
// The lockfile.

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct LockEntry {
    #[serde(rename = "type")]
    pub kind: String,
    pub hash: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub license: Option<String>,
    #[serde(flatten)]
    pub extra: BTreeMap<String, Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Lock {
    pub schema: String,
    #[serde(default)]
    pub packages: BTreeMap<String, LockEntry>,
}

impl Default for Lock {
    fn default() -> Self {
        Lock { schema: LOCK_SCHEMA.into(), packages: BTreeMap::new() }
    }
}

/// `<stem>.lock` next to a timeline.
pub fn lock_path(timeline: &Path) -> PathBuf {
    let stem = timeline.file_stem().map(|s| s.to_string_lossy().into_owned()).unwrap_or_else(|| "timeline".into());
    timeline.with_file_name(format!("{stem}.lock"))
}

impl Lock {
    /// Reads a lockfile; a missing one is empty.
    pub fn read(path: &Path) -> Result<Lock, EditError> {
        match std::fs::read_to_string(path) {
            Ok(t) => serde_json::from_str(&t)
                .map_err(|e| EditError::new("lock_invalid", format!("{} isn't a lockfile: {e}", path.display()))),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(Lock::default()),
            Err(e) => Err(EditError::new("lock_unreadable", format!("can't read {}: {e}", path.display()))),
        }
    }

    pub fn write(&self, path: &Path) -> Result<(), EditError> {
        let text = serde_json::to_string_pretty(self).expect("lock serializes") + "\n";
        let tmp = path.with_extension("lock.tmp");
        std::fs::write(&tmp, text)
            .and_then(|_| std::fs::rename(&tmp, path))
            .map_err(|e| EditError::new("lock_write", format!("can't write {}: {e}", path.display())))
    }
}

/// How a timeline's packages are checked against its lockfile.
#[derive(Debug, Clone, Default, PartialEq)]
pub enum LockMode {
    /// Every package must be locked with its current hash (validate, render).
    #[default]
    Strict,
    /// Unlocked packages are pinned (patch apply, `component lock`); changed ones are still errors.
    PinNew,
    /// Like `PinNew`, and these packages (or all, when empty) are re-pinned to their current hash.
    Repin(Vec<String>),
}

// ---------------------------------------------------------------------------------------------------------------
// Tokens.

/// A kit the timeline loaded.
#[derive(Debug, Clone, Serialize)]
pub struct KitInfo {
    /// As written in `kits`.
    pub entry: String,
    pub name: String,
    pub version: String,
    pub dir: PathBuf,
    pub hash: String,
    #[serde(skip)]
    pub kit: Kit,
}

/// Looks a token path (`color.accent`) up in token objects, in order; a group may be spelled singular or plural.
pub fn lookup_token(sources: &[&Value], path: &str) -> Option<Value> {
    let segs: Vec<&str> = path.split('.').filter(|s| !s.is_empty()).collect();
    let (first, rest) = segs.split_first()?;
    let alt = match first.strip_suffix('s') {
        Some(s) => s.to_string(),
        None => format!("{first}s"),
    };
    for src in sources {
        for g in [first.to_string(), alt.clone()] {
            let mut cur = match src.get(&g) {
                Some(v) => v,
                None => continue,
            };
            let mut ok = true;
            for s in rest {
                match cur {
                    Value::Object(m) => match m.get(*s) {
                        Some(v) => cur = v,
                        None => {
                            ok = false;
                            break;
                        }
                    },
                    Value::Array(a) => match s.parse::<usize>().ok().and_then(|i| a.get(i)) {
                        Some(v) => cur = v,
                        None => {
                            ok = false;
                            break;
                        }
                    },
                    _ => {
                        ok = false;
                        break;
                    }
                }
            }
            if ok {
                return Some(cur.clone());
            }
        }
    }
    None
}

/// Every token path in the sources (for suggestions).
pub fn token_paths(sources: &[&Value]) -> Vec<String> {
    fn walk(v: &Value, pre: &str, out: &mut Vec<String>) {
        if let Value::Object(m) = v {
            for (k, x) in m {
                let p = if pre.is_empty() { k.clone() } else { format!("{pre}.{k}") };
                if x.is_object() {
                    walk(x, &p, out);
                } else {
                    out.push(p);
                }
            }
        }
    }
    let mut out = vec![];
    for s in sources {
        walk(s, "", &mut out);
    }
    out.sort();
    out.dedup();
    out
}

fn is_token(s: &str) -> Option<&str> {
    let p = s.strip_prefix("kit:")?;
    (!p.is_empty() && p.chars().all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '.' || c == '-')).then_some(p)
}

fn token_error(path: &str, sources: &[&Value], ptr: &str) -> EditError {
    let all = token_paths(sources);
    let mut e = EditError::new("kit_token_unknown", format!("kit token `kit:{path}` isn't defined")).at(ptr);
    e = if sources.iter().all(|s| s.as_object().is_none_or(|m| m.is_empty())) {
        e.hint("the timeline uses no kit that defines tokens: add one to \"kits\" (e.g. \"clean@1.0.0\")")
    } else {
        e.hint("use a token the kits define")
    };
    e.suggest(close_matches(path, all.iter().map(String::as_str), 3).into_iter().map(|s| format!("kit:{s}")).collect())
}

/// Replaces every `kit:<path>` string in `v` (but under `meta`, and `expr` strings). `skip_use_props` leaves the
/// `props` of component uses alone (they resolve with the component's own tokens too).
pub fn substitute_tokens(
    v: &mut Value,
    sources: &[&Value],
    ptr: &str,
    skip_use_props: bool,
    errs: &mut Vec<EditError>,
) {
    match v {
        Value::String(s) => {
            if let Some(path) = is_token(s) {
                match lookup_token(sources, path) {
                    Some(t) => *v = t,
                    None => errs.push(token_error(path, sources, ptr)),
                }
            }
        }
        Value::Array(a) => {
            for (i, x) in a.iter_mut().enumerate() {
                substitute_tokens(x, sources, &format!("{ptr}/{i}"), skip_use_props, errs);
            }
        }
        Value::Object(m) => {
            let is_use = skip_use_props && m.contains_key("use");
            for (k, x) in m.iter_mut() {
                if k == "meta" || (k == "expr" && x.is_string()) || (is_use && k == "props") {
                    continue;
                }
                substitute_tokens(x, sources, &format!("{ptr}/{}", ptr_escape(k)), skip_use_props, errs);
            }
        }
        _ => {}
    }
}

// ---------------------------------------------------------------------------------------------------------------
// Templates.

/// What a template can refer to.
struct Scope<'a> {
    id: &'a str,
    props: &'a Value,
    fonts: &'a BTreeMap<String, String>,
    images: &'a BTreeMap<String, String>,
    tokens: &'a [&'a Value],
    canvas: [u32; 2],
    component: &'a str,
}

fn lookup_path<'v>(v: &'v Value, path: &str) -> Option<&'v Value> {
    let mut cur = v;
    for s in path.split('.').filter(|s| !s.is_empty()) {
        cur = match cur {
            Value::Object(m) => m.get(s)?,
            Value::Array(a) => a.get(s.parse::<usize>().ok()?)?,
            _ => return None,
        };
    }
    Some(cur)
}

fn ident_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || c == '_'
}

fn text_of(v: &Value) -> String {
    match v {
        Value::String(s) => s.clone(),
        Value::Null => String::new(),
        other => other.to_string(),
    }
}

/// Substitutes a template value. `None` removes the key it is under.
fn subst(
    v: &Value,
    sc: &Scope,
    ptr: &str,
    ev: &mut Option<Box<dyn ExprEval>>,
    errs: &mut Vec<EditError>,
) -> Option<Value> {
    match v {
        Value::Object(m) => {
            if m.len() == 1
                && let Some(Value::String(e)) = m.get("$expr")
            {
                let scope = json!({
                    "props": sc.props, "id": sc.id,
                    "canvas": {"w": sc.canvas[0], "h": sc.canvas[1]},
                    "kit": merged_tokens(sc.tokens),
                });
                let r = match ev {
                    Some(x) => x.eval_json(e, &scope),
                    None => match evaluator() {
                        Ok(mut x) => {
                            let r = x.eval_json(e, &scope);
                            *ev = Some(x);
                            r
                        }
                        Err(err) => Err(err),
                    },
                };
                return match r {
                    Ok(Value::Null) => None,
                    Ok(x) => Some(x),
                    Err(err) => {
                        errs.push(
                            EditError::new(
                                "component_template",
                                format!("{}: $expr `{e}`: {}", sc.component, err.message),
                            )
                            .at(ptr),
                        );
                        None
                    }
                };
            }
            let mut out = Map::new();
            for (k, x) in m {
                if k == "meta" || (k == "expr" && x.is_string()) {
                    out.insert(k.clone(), x.clone());
                    continue;
                }
                if let Some(nx) = subst(x, sc, &format!("{ptr}/{}", ptr_escape(k)), ev, errs) {
                    out.insert(k.clone(), nx);
                }
            }
            Some(Value::Object(out))
        }
        Value::Array(a) => Some(Value::Array(
            a.iter()
                .enumerate()
                .map(|(i, x)| subst(x, sc, &format!("{ptr}/{i}"), ev, errs).unwrap_or(Value::Null))
                .collect(),
        )),
        Value::String(s) => subst_str(s, sc, ptr, errs),
        other => Some(other.clone()),
    }
}

fn subst_str(s: &str, sc: &Scope, ptr: &str, errs: &mut Vec<EditError>) -> Option<Value> {
    let fail = |errs: &mut Vec<EditError>, msg: String| {
        errs.push(EditError::new("component_template", format!("{}: {msg}", sc.component)).at(ptr));
        None
    };
    if let Some(path) = is_token(s) {
        return match lookup_token(sc.tokens, path) {
            Some(t) => Some(t),
            None => {
                errs.push(token_error(path, sc.tokens, ptr));
                None
            }
        };
    }
    if s == "$id" {
        return Some(json!(sc.id));
    }
    if let Some(p) = s.strip_prefix("$props.")
        && p.chars().all(|c| ident_char(c) || c == '.')
    {
        return lookup_path(sc.props, p).filter(|v| !v.is_null()).cloned();
    }
    if let Some(n) = s.strip_prefix("$font.") {
        return match sc.fonts.get(n) {
            Some(f) => Some(json!(f)),
            None => fail(errs, format!("`$font.{n}` isn't in inputs.fonts")),
        };
    }
    if let Some(n) = s.strip_prefix("$image.") {
        return match sc.images.get(n) {
            Some(f) => Some(json!(f)),
            None => fail(errs, format!("`$image.{n}` isn't in inputs.images")),
        };
    }
    if !s.contains('$') {
        return Some(json!(s));
    }
    // Interpolation: `$id`, `$props.<path>` and `$$` inside a longer string.
    let chars: Vec<char> = s.chars().collect();
    let mut out = String::new();
    let mut i = 0;
    while i < chars.len() {
        if chars[i] != '$' {
            out.push(chars[i]);
            i += 1;
            continue;
        }
        let rest: String = chars[i + 1..].iter().collect();
        if rest.starts_with('$') {
            out.push('$');
            i += 2;
        } else if rest.starts_with("id") && !rest[2..].starts_with(ident_char) {
            out.push_str(sc.id);
            i += 3;
        } else if let Some(p) = rest.strip_prefix("props.") {
            let len = p.chars().take_while(|c| ident_char(*c) || *c == '.').count();
            let path = p[..len].trim_end_matches('.');
            match lookup_path(sc.props, path) {
                Some(v) => out.push_str(&text_of(v)),
                None => return fail(errs, format!("`$props.{path}` in \"{s}\" has no value")),
            }
            i += 1 + "props.".len() + path.chars().count();
        } else {
            out.push('$');
            i += 1;
        }
    }
    Some(json!(out))
}

fn merged_tokens(sources: &[&Value]) -> Value {
    // Earlier sources win: merge from the last to the first.
    fn merge(into: &mut Value, from: &Value) {
        match (into, from) {
            (Value::Object(a), Value::Object(b)) => {
                for (k, v) in b {
                    match a.get_mut(k) {
                        Some(x) if x.is_object() && v.is_object() => merge(x, v),
                        _ => {
                            a.insert(k.clone(), v.clone());
                        }
                    }
                }
            }
            (slot, v) => *slot = v.clone(),
        }
    }
    let mut out = json!({});
    for s in sources.iter().rev() {
        merge(&mut out, s);
    }
    out
}

// ---------------------------------------------------------------------------------------------------------------
// Fitting the design canvas into the timeline's.

/// `x' = x·k + ox`, `y' = y·k + oy`, sizes `·k`.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Fit {
    pub k: f64,
    pub ox: f64,
    pub oy: f64,
}

impl Fit {
    pub fn new(design: [u32; 2], canvas: [u32; 2]) -> Fit {
        let (w, h) = (canvas[0] as f64, canvas[1] as f64);
        let (dw, dh) = (design[0] as f64, design[1] as f64);
        let k = (w / dw).min(h / dh);
        Fit { k, ox: (w - dw * k) / 2.0, oy: (h - dh * k) / 2.0 }
    }
    pub fn is_identity(&self) -> bool {
        self.k == 1.0 && self.ox == 0.0 && self.oy == 0.0
    }
}

fn num(x: f64) -> String {
    let s = format!("{x}");
    if s.contains('.') || s.contains('e') { s } else { format!("{s}.0") }
}

fn scale_value(v: &mut Value, k: f64) {
    match v {
        Value::Number(n) => {
            if let Some(x) = n.as_f64() {
                *v = json!(x * k);
            }
        }
        Value::Array(a) => {
            for x in a {
                scale_value(x, k);
            }
        }
        _ => {}
    }
}

/// Scales a numeric prop (a constant, keys or an expression) by `k`.
fn scale_prop(p: &mut Value, k: f64) {
    match p {
        Value::Object(m) => {
            if let Some(Value::String(e)) = m.get("expr").cloned() {
                m.insert(
                    "expr".into(),
                    json!(format!("((v) => Array.isArray(v) ? v.map((x) => x * {k}) : v * {k})(({e}\n))", k = num(k))),
                );
            } else if let Some(Value::Array(keys)) = m.get_mut("keys") {
                for key in keys {
                    if let Some(v) = key.get_mut("v") {
                        scale_value(v, k);
                    }
                }
            }
        }
        other => scale_value(other, k),
    }
}

/// Maps a position prop (`[x, y]` constants, keys or an expression's result).
fn map_position(p: &mut Value, f: &Fit) {
    let map = |v: &mut Value| {
        if let Value::Array(a) = v
            && a.len() == 2
            && let (Some(x), Some(y)) = (a[0].as_f64(), a[1].as_f64())
        {
            *v = json!([x * f.k + f.ox, y * f.k + f.oy]);
        }
    };
    match p {
        Value::Object(m) => {
            if let Some(Value::String(e)) = m.get("expr").cloned() {
                m.insert(
                    "expr".into(),
                    json!(format!(
                        "((p) => [p[0] * {k} + {ox}, p[1] * {k} + {oy}])(({e}\n))",
                        k = num(f.k),
                        ox = num(f.ox),
                        oy = num(f.oy)
                    )),
                );
            } else if let Some(Value::Array(keys)) = m.get_mut("keys") {
                for key in keys {
                    if let Some(v) = key.get_mut("v") {
                        map(v);
                    }
                }
            }
        }
        other => map(other),
    }
}

fn scale_field(m: &mut Map<String, Value>, k: &str, f: f64) {
    if let Some(v) = m.get_mut(k) {
        scale_value(v, f);
    }
}

/// Fits one expanded layer into the timeline's canvas (spec/components.md, "Fitting the canvas").
pub fn fit_layer(l: &mut Value, f: &Fit) {
    let Some(m) = l.as_object_mut() else { return };
    let ty = m.get("type").and_then(Value::as_str).unwrap_or_default().to_string();
    let tr = m.entry("transform").or_insert_with(|| json!({}));
    if let Some(t) = tr.as_object_mut() {
        match t.get_mut("position") {
            Some(p) => map_position(p, f),
            None if ty != "video" => {
                // The default position is the frame's center, which the mapping keeps.
            }
            None => {}
        }
    }
    if ty == "video" {
        return;
    }
    let k = f.k;
    match ty.as_str() {
        "text" => {
            if !m.contains_key("size") {
                m.insert("size".into(), json!(crate::layer::Text::DEFAULT_SIZE));
            }
            scale_field(m, "size", k);
            scale_field(m, "max_width", k);
            if let Some(Value::Object(s)) = m.get_mut("stroke") {
                scale_field(s, "width", k);
            }
        }
        "shape" => {
            scale_field(m, "size", k);
            scale_field(m, "radius", k);
            if let Some(Value::Object(s)) = m.get_mut("stroke") {
                scale_field(s, "width", k);
            }
        }
        "image" => {
            if m.contains_key("size") {
                scale_field(m, "size", k);
            } else if let Some(Value::Object(t)) = m.get_mut("transform") {
                let s = t.entry("scale").or_insert_with(|| json!(1.0));
                scale_prop(s, k);
            }
        }
        _ => {}
    }
    if let Some(Value::Object(s)) = m.get_mut("shadow") {
        for key in ["dx", "dy", "blur"] {
            scale_field(s, key, k);
        }
    }
    if let Some(b) = m.get_mut("blur") {
        scale_prop(b, k);
    }
}

// ---------------------------------------------------------------------------------------------------------------
// Expansion.

/// One component use in a timeline.
#[derive(Debug, Clone, Serialize)]
pub struct UseInfo {
    /// The element: an overlay, a shot, or a spine item (an insert's picture).
    pub element: String,
    /// The element's pointer.
    pub ptr: String,
    /// `overlay`, `shot` or `insert`.
    pub kind: String,
    #[serde(rename = "use")]
    pub reference: String,
    pub name: String,
    pub version: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub category: Option<String>,
    pub hash: String,
    pub dir: PathBuf,
}

/// The result of expanding a timeline document.
pub struct Expansion {
    pub doc: Value,
    pub uses: Vec<UseInfo>,
    pub kits: Vec<KitInfo>,
    /// The lock after pinning (equal to the one given unless the mode pins).
    pub lock: Lock,
}

/// A package as the lockfile sees it.
struct Pinned<'a> {
    reference: &'a str,
    /// `component` or `kit`.
    kind: &'a str,
    dir: &'a Path,
    hash: &'a str,
    license: Option<String>,
}

/// Checks a package against the lock (and pins it when the mode allows).
fn check_lock(lock: &mut Lock, mode: &LockMode, pkg: Pinned, tl_dir: &Path, ptr: &str) -> Result<(), EditError> {
    let Pinned { reference, kind, dir, hash, license } = pkg;
    let rel = dir.strip_prefix(tl_dir).map(|p| p.to_string_lossy().replace('\\', "/"));
    let entry = || LockEntry {
        kind: kind.into(),
        hash: hash.into(),
        path: Some(rel.clone().unwrap_or_else(|_| dir.to_string_lossy().into_owned())),
        source: None,
        license: license.clone(),
        extra: BTreeMap::new(),
    };
    let repin = match mode {
        LockMode::Repin(list) => list.is_empty() || list.iter().any(|r| r == reference),
        _ => false,
    };
    match lock.packages.get_mut(reference) {
        Some(e) if e.hash == hash => Ok(()),
        Some(e) if repin => {
            e.hash = hash.to_string();
            Ok(())
        }
        Some(e) => Err(EditError::new(
            "component_changed",
            format!(
                "{reference} ({}) changed since it was locked: {} now, {} in the lockfile",
                dir.display(),
                hash,
                e.hash
            ),
        )
        .at(ptr)
        .hint(format!(
            "a released version is immutable: copy it to a new version and `retake-edit component upgrade`, or re-pin \
             it with `retake-edit component lock <timeline> --update {reference}`"
        ))),
        None if *mode != LockMode::Strict => {
            lock.packages.insert(reference.to_string(), entry());
            Ok(())
        }
        None => Err(EditError::new("component_unlocked", format!("{reference} isn't in the lockfile")).at(ptr).hint(
            "pin it: `retake-edit component lock <timeline>` (retake lib add and patch apply pin what they add)",
        )),
    }
}

struct Expander<'a> {
    tl_dir: &'a Path,
    roots: Vec<PathBuf>,
    canvas: [u32; 2],
    lock: Lock,
    mode: LockMode,
    kit_tokens: Vec<Value>,
    asset_ids: BTreeSet<String>,
    packages: HashMap<String, Result<Package, EditError>>,
    reported: BTreeSet<String>,
    ev: Option<Box<dyn ExprEval>>,
    fonts: Map<String, Value>,
    assets: Map<String, Value>,
    uses: Vec<UseInfo>,
    errs: Vec<EditError>,
}

impl Expander<'_> {
    fn package(&mut self, reference: &str, ptr: &str) -> Result<Package, EditError> {
        if let Some(p) = self.packages.get(reference) {
            return p.clone();
        }
        let r = (|| {
            let (name, version) = parse_ref(reference).ok_or_else(|| {
                EditError::new("component_ref", format!("`{reference}` isn't <name>@<version> (pop-text@1.0.0)"))
            })?;
            let dir = find_package(&self.roots, &name, &version, "component.json").ok_or_else(|| {
                let all = available(&self.roots, "component.json");
                let refs: Vec<&str> = all.iter().map(|(r, _)| r.as_str()).collect();
                let same: Vec<String> =
                    refs.iter().filter(|r| r.starts_with(&format!("{name}@"))).map(|s| s.to_string()).collect();
                // Close names, each with the versions there are.
                let names: Vec<&str> = refs.iter().filter_map(|r| r.split('@').next()).collect();
                let close = close_matches(&name, names, 3);
                let suggestions: Vec<String> = refs
                    .iter()
                    .filter(|r| close.iter().any(|n| r.split('@').next() == Some(n.as_str())))
                    .map(|s| s.to_string())
                    .collect();
                let e = EditError::new(
                    "component_not_found",
                    format!(
                        "component {reference} isn't in {}",
                        self.roots.iter().map(|r| r.display().to_string()).collect::<Vec<_>>().join(", ")
                    ),
                );
                if same.is_empty() {
                    e.suggest(suggestions).hint("add it with `retake lib add <name>@<version>`")
                } else {
                    e.hint(format!("versions here: {}", same.join(", "))).suggest(same)
                }
            })?;
            let pkg = load_component(&dir)?;
            if pkg.comp.name != name || pkg.comp.version != version {
                return Err(EditError::new(
                    "component_invalid",
                    format!("{} holds {}, not {reference}", dir.display(), pkg.comp.reference()),
                ));
            }
            Ok(pkg)
        })();
        let r = match r.map_err(|e| e.at(ptr)) {
            Ok(p) => {
                let pin = Pinned {
                    reference,
                    kind: "component",
                    dir: &p.dir,
                    hash: &p.hash,
                    license: p.comp.license.clone(),
                };
                check_lock(&mut self.lock, &self.mode, pin, self.tl_dir, ptr).map(|_| p)
            }
            Err(e) => Err(e),
        };
        self.packages.insert(reference.to_string(), r.clone());
        r
    }

    /// Expands the use site `site` (an overlay, or a composition object) at `ptr`, owned by element `id`.
    fn expand(&mut self, site: &mut Value, ptr: &str, id: &str, kind: &str) {
        let Some(reference) = site.get("use").and_then(Value::as_str).map(String::from) else { return };
        let pkg = match self.package(&reference, &format!("{ptr}/use")) {
            Ok(p) => p,
            Err(e) => {
                // A package's own problem (missing, changed, unlocked) is reported once, at its first use.
                if self.reported.insert(reference.clone()) {
                    self.errs.push(e);
                }
                return;
            }
        };
        let c = &pkg.comp;
        let target = if kind == "overlay" { "overlay" } else { "picture" };
        if !c.use_as.iter().any(|u| u == target) {
            self.errs.push(
                EditError::new(
                    "component_target",
                    format!("{reference} is used as {target} here, but it is for {}", c.use_as.join(" and ")),
                )
                .at(format!("{ptr}/use")),
            );
            return;
        }
        if site.get("layers").is_some_and(|l| l.as_array().is_some_and(|a| !a.is_empty())) {
            self.errs.push(
                EditError::new("component_layers", format!("{id} uses {reference} and has layers of its own"))
                    .at(format!("{ptr}/layers"))
                    .hint("a use takes props, not layers: drop \"layers\", or drop \"use\""),
            );
            return;
        }
        let before = self.errs.len();
        // Props: defaults, overlaid by the given ones, tokens resolved (the kits, then the component's own).
        let mut props = Value::Object(c.defaults());
        if let Some(Value::Object(given)) = site.get("props") {
            for (k, v) in given {
                props[k] = v.clone();
            }
        } else if site.get("props").is_some_and(|p| !p.is_null()) {
            self.errs.push(EditError::new("component_props", "props must be an object").at(format!("{ptr}/props")));
            return;
        }
        let mut sources: Vec<&Value> = self.kit_tokens.iter().collect();
        sources.push(&c.tokens);
        let mut perrs = vec![];
        substitute_tokens(&mut props, &sources, &format!("{ptr}/props"), false, &mut perrs);
        for e in perrs.iter_mut() {
            if let Some(p) = &e.path
                && !p.starts_with(&format!("{ptr}/props"))
            {
                e.path = Some(format!("{ptr}/props"));
            }
        }
        self.errs.extend(perrs);
        // The props schema.
        if c.props.is_object() && c.props.as_object().is_some_and(|m| !m.is_empty()) {
            match jsonschema::validator_for(&c.props) {
                Ok(v) => {
                    for e in v.iter_errors(&props) {
                        let at = format!("{ptr}/props{}", e.instance_path());
                        let field = e.instance_path().to_string().trim_start_matches('/').to_string();
                        let desc = c.props["properties"]
                            .get(field.split('/').next().unwrap_or_default())
                            .and_then(|p| p.get("description"))
                            .and_then(Value::as_str)
                            .map(String::from);
                        let mut err = EditError::new("component_props", format!("{reference} props: {e}")).at(at);
                        if let Some(d) = desc {
                            err = err.hint(format!("{field}: {d}"));
                        } else if field.is_empty() {
                            let names: Vec<String> = c.props["properties"]
                                .as_object()
                                .map(|m| m.keys().cloned().collect())
                                .unwrap_or_default();
                            err = err.hint(format!("props are: {}", names.join(", ")));
                        }
                        self.errs.push(err);
                    }
                }
                Err(e) => self.errs.push(
                    EditError::new("component_invalid", format!("{reference}: props isn't a valid JSON Schema: {e}"))
                        .at(format!("{ptr}/use")),
                ),
            }
        }
        for (prop, akind) in c.asset_inputs() {
            match props.get(&prop) {
                Some(Value::String(a)) if self.asset_ids.contains(a) => {}
                Some(Value::String(a)) => self.errs.push(
                    EditError::new(
                        "component_input_unknown",
                        format!(
                            "{reference} prop `{prop}` names {akind} asset `{a}`, which the timeline doesn't define"
                        ),
                    )
                    .at(format!("{ptr}/props/{prop}"))
                    .suggest(close_matches(a, self.asset_ids.iter().map(String::as_str), 3))
                    .hint("add it under \"assets\", or use one of the suggestions"),
                ),
                _ => {}
            }
        }
        if self.errs.len() > before {
            return;
        }
        // Inputs: package files the expansion adds to the timeline's fonts and assets.
        let tag = format!("{}-{}", c.name, c.version.replace(['.', '+'], "-"));
        let mut fonts = BTreeMap::new();
        for (n, fi) in &c.inputs.fonts {
            let fname: String =
                format!("{tag}-{n}").chars().map(|ch| if ident_char(ch) || ch == '-' { ch } else { '-' }).collect();
            let mut def = json!({"path": pkg.dir.join(&fi.path).to_string_lossy()});
            if let Some(w) = fi.weight {
                def["weight"] = json!(w);
            }
            self.fonts.insert(fname.clone(), def);
            fonts.insert(n.clone(), fname);
        }
        let mut images = BTreeMap::new();
        for (n, ii) in &c.inputs.images {
            let aid = format!("cmp:{reference}/{n}");
            self.assets.insert(aid.clone(), json!({"path": pkg.dir.join(&ii.path).to_string_lossy()}));
            images.insert(n.clone(), aid);
        }
        let sc = Scope {
            id,
            props: &props,
            fonts: &fonts,
            images: &images,
            tokens: &sources,
            canvas: c.canvas(),
            component: &reference,
        };
        let mut ev = self.ev.take();
        let mut errs = vec![];
        let mut layers = vec![];
        for (i, l) in c.layers.iter().enumerate() {
            if let Some(mut nl) = subst(l, &sc, &format!("{ptr}/layers/{i}"), &mut ev, &mut errs) {
                if let Some(local) = nl.get("id").and_then(Value::as_str) {
                    nl["id"] = json!(format!("{id}/{local}"));
                }
                layers.push(nl);
            }
        }
        let bg = c.background.as_ref().and_then(|b| subst(b, &sc, &format!("{ptr}/background"), &mut ev, &mut errs));
        self.ev = ev;
        self.errs.extend(errs);
        let fit = Fit::new(c.canvas(), self.canvas);
        if !fit.is_identity() {
            for l in &mut layers {
                fit_layer(l, &fit);
            }
        }
        let obj = site.as_object_mut().expect("use sites are objects");
        obj.insert("layers".into(), Value::Array(layers));
        obj.insert("props".into(), props);
        if let Some(b) = bg
            && !obj.contains_key("background")
        {
            obj.insert("background".into(), b);
        }
        self.uses.push(UseInfo {
            element: id.to_string(),
            ptr: ptr.to_string(),
            kind: kind.to_string(),
            reference: reference.clone(),
            name: c.name.clone(),
            version: c.version.clone(),
            category: c.category.clone(),
            hash: pkg.hash.clone(),
            dir: pkg.dir.clone(),
        });
    }
}

/// Expands a timeline document: kits loaded and locked, `kit:` tokens replaced, component uses turned into layers.
/// Errors are collected; on any error the expansion is unusable.
pub fn expand(src: &Value, tl_dir: &Path, lock: Lock, mode: LockMode) -> Result<Expansion, Vec<EditError>> {
    expand_with_roots(src, tl_dir, search_roots(tl_dir), lock, mode)
}

/// [`expand`] with explicit search roots.
pub fn expand_with_roots(
    src: &Value,
    tl_dir: &Path,
    roots: Vec<PathBuf>,
    lock: Lock,
    mode: LockMode,
) -> Result<Expansion, Vec<EditError>> {
    let mut doc = src.clone();
    let canvas = [
        doc.pointer("/format/size/0").and_then(Value::as_u64).unwrap_or(1920) as u32,
        doc.pointer("/format/size/1").and_then(Value::as_u64).unwrap_or(1080) as u32,
    ];
    let asset_ids: BTreeSet<String> =
        doc.get("assets").and_then(Value::as_object).map(|m| m.keys().cloned().collect()).unwrap_or_default();
    let mut x = Expander {
        tl_dir,
        roots,
        canvas,
        lock,
        mode,
        kit_tokens: vec![],
        asset_ids,
        packages: HashMap::new(),
        reported: BTreeSet::new(),
        ev: None,
        fonts: Map::new(),
        assets: Map::new(),
        uses: vec![],
        errs: vec![],
    };
    // Kits.
    let mut kits = vec![];
    let entries: Vec<String> = doc
        .get("kits")
        .and_then(Value::as_array)
        .map(|a| a.iter().filter_map(|v| v.as_str().map(String::from)).collect())
        .unwrap_or_default();
    for (i, entry) in entries.iter().enumerate() {
        let ptr = format!("/kits/{i}");
        let catalog = entry.starts_with("brand:");
        let bare = entry.strip_prefix("brand:").unwrap_or(entry);
        let Some((name, version)) = bare.split_once('@').map(|(a, b)| (a.to_string(), b.to_string())) else {
            if !catalog {
                x.errs.push(EditError::new("kit_ref", format!("kit `{entry}` isn't <name>@<version>")).at(ptr));
            }
            continue;
        };
        let Some(dir) = find_package(&x.roots, &name, &version, "kit.json") else {
            if !catalog {
                let all = available(&x.roots, "kit.json");
                x.errs.push(
                    EditError::new("kit_not_found", format!("kit {bare} isn't in the component folders"))
                        .at(ptr)
                        .suggest(close_matches(bare, all.iter().map(|(r, _)| r.as_str()), 3))
                        .hint("add it with `retake lib add <kit>`"),
                );
            }
            continue;
        };
        match load_kit(&dir) {
            Ok((kit, hash)) => {
                let reference = format!("{name}@{version}");
                let pin = Pinned { reference: &reference, kind: "kit", dir: &dir, hash: &hash, license: kit.spdx() };
                if let Err(e) = check_lock(&mut x.lock, &x.mode, pin, tl_dir, &ptr) {
                    // Still load its tokens: the lock error is the one to fix, not every token after it.
                    x.errs.push(e);
                }
                x.kit_tokens.push(kit.tokens.clone());
                kits.push(KitInfo { entry: entry.clone(), name, version, dir, hash, kit });
            }
            Err(e) => x.errs.push(e.at(ptr)),
        }
    }
    // Tokens everywhere but in the props of uses.
    {
        let sources: Vec<&Value> = x.kit_tokens.iter().collect();
        let mut errs = vec![];
        for key in ["assets", "fonts", "spine", "scenes", "audio", "format", "end"] {
            if let Some(v) = doc.get_mut(key) {
                substitute_tokens(v, &sources, &format!("/{key}"), true, &mut errs);
            }
        }
        x.errs.extend(errs);
    }
    // Uses.
    if let Some(scenes) = doc.get_mut("scenes").and_then(Value::as_array_mut) {
        for (si, sc) in scenes.iter_mut().enumerate() {
            if let Some(shots) = sc.get_mut("shots").and_then(Value::as_array_mut) {
                for (ki, sh) in shots.iter_mut().enumerate() {
                    let id = sh.get("id").and_then(Value::as_str).unwrap_or_default().to_string();
                    if let Some(pic) = sh.get_mut("picture").filter(|p| p.get("use").is_some()) {
                        x.expand(pic, &format!("/scenes/{si}/shots/{ki}/picture"), &id, "shot");
                    }
                }
            }
            if let Some(ovs) = sc.get_mut("overlays").and_then(Value::as_array_mut) {
                for (oi, ov) in ovs.iter_mut().enumerate() {
                    if ov.get("use").is_some() {
                        let id = ov.get("id").and_then(Value::as_str).unwrap_or_default().to_string();
                        x.expand(ov, &format!("/scenes/{si}/overlays/{oi}"), &id, "overlay");
                    }
                }
            }
        }
    }
    if let Some(spine) = doc.get_mut("spine").and_then(Value::as_array_mut) {
        for (i, s) in spine.iter_mut().enumerate() {
            let id = s.get("id").and_then(Value::as_str).unwrap_or_default().to_string();
            if let Some(pic) = s.get_mut("picture").filter(|p| p.get("use").is_some()) {
                x.expand(pic, &format!("/spine/{i}/picture"), &id, "insert");
            }
        }
    }
    if !x.fonts.is_empty() {
        let f = doc.as_object_mut().expect("timelines are objects").entry("fonts").or_insert_with(|| json!({}));
        if let Some(m) = f.as_object_mut() {
            m.extend(std::mem::take(&mut x.fonts));
        }
    }
    if !x.assets.is_empty()
        && let Some(m) = doc.get_mut("assets").and_then(Value::as_object_mut)
    {
        m.extend(std::mem::take(&mut x.assets));
    }
    if !x.errs.is_empty() {
        let mut e = x.errs;
        e.sort_by(|a, b| a.path.cmp(&b.path).then(a.code.cmp(&b.code)));
        e.dedup();
        return Err(e);
    }
    Ok(Expansion { doc, uses: x.uses, kits, lock: x.lock })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn semver_and_refs() {
        assert!(is_semver("1.4.0") && is_semver("0.1.0-beta.2") && !is_semver("1.4") && !is_semver("v1.0.0"));
        assert_eq!(parse_ref("pop-text@1.4.0"), Some(("pop-text".into(), "1.4.0".into())));
        assert_eq!(parse_ref("Pop@1.0.0"), None);
        assert_eq!(parse_ref("pop-text"), None);
        assert_eq!(cmp_semver("1.10.0", "1.9.9"), std::cmp::Ordering::Greater);
        assert_eq!(cmp_semver("1.0.0-rc.1", "1.0.0"), std::cmp::Ordering::Less);
    }

    #[test]
    fn tokens_lookup_singular_or_plural() {
        let a = json!({"colors": {"accent": "#ff0000"}, "size": {"title": 96}});
        let b = json!({"color": {"accent": "#00ff00", "bg": "#000"}});
        assert_eq!(lookup_token(&[&a, &b], "color.accent"), Some(json!("#ff0000")));
        assert_eq!(lookup_token(&[&b, &a], "colors.accent"), Some(json!("#00ff00")));
        assert_eq!(lookup_token(&[&a, &b], "color.bg"), Some(json!("#000")));
        assert_eq!(lookup_token(&[&a], "size.title"), Some(json!(96)));
        assert_eq!(lookup_token(&[&a], "size.nope"), None);
        let mut v = json!({"fill": "kit:color.accnt", "meta": {"x": "kit:nope"}, "expr": "'kit:x'"});
        let mut errs = vec![];
        substitute_tokens(&mut v, &[&a], "", false, &mut errs);
        assert_eq!(errs.len(), 1, "{errs:?}");
        assert_eq!(errs[0].code, "kit_token_unknown");
        assert_eq!(errs[0].path.as_deref(), Some("/fill"));
        assert_eq!(errs[0].suggestions, vec!["kit:colors.accent".to_string()]);
    }

    #[test]
    fn templates_substitute_and_interpolate() {
        let props = json!({"text": "HI", "y": 800, "pair": [1, 2], "none": null});
        let fonts = BTreeMap::new();
        let images = BTreeMap::new();
        let tok = json!({"color": {"accent": "#abcdef"}});
        let toks = [&tok];
        let sc = Scope {
            id: "pop/a",
            props: &props,
            fonts: &fonts,
            images: &images,
            tokens: &toks,
            canvas: [1920, 1080],
            component: "t@1.0.0",
        };
        let tpl = json!({"id": "text", "text": "$props.text", "fill": "kit:color.accent", "size": "$props.none",
            "transform": {"position": [960, "$props.y"], "opacity": {"keys": [{"t": "end:$id-0.12", "v": 1}]},
                          "rotation": {"expr": "props.y * 0 + $props.y"}},
            "meta": {"keep": "$props.text"}, "label": "$$5 for $props.text at $id"});
        let mut errs = vec![];
        let mut ev = None;
        let out = subst(&tpl, &sc, "", &mut ev, &mut errs).unwrap();
        assert!(errs.is_empty(), "{errs:?}");
        assert_eq!(out["text"], "HI");
        assert_eq!(out["fill"], "#abcdef");
        assert!(out.get("size").is_none(), "a missing prop removes its key");
        assert_eq!(out["transform"]["position"], json!([960, 800]));
        assert_eq!(out["transform"]["opacity"]["keys"][0]["t"], "end:pop/a-0.12");
        assert_eq!(out["transform"]["rotation"]["expr"], "props.y * 0 + $props.y", "runtime expressions untouched");
        assert_eq!(out["meta"]["keep"], "$props.text");
        assert_eq!(out["label"], "$5 for HI at pop/a");
        // $expr without a registered engine is a typed error.
        let mut errs = vec![];
        let _ = subst(&json!({"$expr": "1 + 1"}), &sc, "/x", &mut None, &mut errs);
        assert!(errs.is_empty() || errs[0].code == "component_template", "{errs:?}");
    }

    #[test]
    fn fit_maps_positions_and_sizes() {
        let f = Fit::new([1920, 1080], [1280, 720]);
        assert!((f.k - 2.0 / 3.0).abs() < 1e-12 && f.ox == 0.0 && f.oy == 0.0);
        let mut l = json!({"id": "a", "type": "text", "text": "x", "size": 120, "stroke": {"color": "#000", "width": 6},
            "shadow": {"dx": 3, "dy": 6, "blur": 9},
            "transform": {"position": {"keys": [{"t": 0, "v": [960, 1080]}, {"t": 1, "v": [0, 0]}]}}});
        fit_layer(&mut l, &f);
        assert_eq!(l["size"], json!(80.0));
        assert_eq!(l["stroke"]["width"], json!(4.0));
        assert_eq!(l["shadow"]["dy"], json!(4.0));
        assert_eq!(l["transform"]["position"]["keys"][0]["v"], json!([640.0, 720.0]));
        // Vertical: letterboxed in the middle band.
        let v = Fit::new([1920, 1080], [1080, 1920]);
        assert!((v.k - 0.5625).abs() < 1e-12 && v.ox == 0.0 && (v.oy - 656.25).abs() < 1e-9);
        let mut e = json!({"id": "b", "type": "shape", "shape": "rect", "size": [100, 50],
            "transform": {"position": {"expr": "[props.x, 540]"}}});
        fit_layer(&mut e, &v);
        assert_eq!(e["size"], json!([56.25, 28.125]));
        assert!(e["transform"]["position"]["expr"].as_str().unwrap().contains("p[0] * 0.5625 + 0.0"));
        let mut vid =
            json!({"id": "c", "type": "video", "asset": "x", "transform": {"position": [960, 540], "scale": 0.3}});
        fit_layer(&mut vid, &f);
        assert_eq!(vid["transform"]["scale"], json!(0.3), "video only moves");
        assert_eq!(vid["transform"]["position"], json!([640.0, 360.0]));
    }

    #[test]
    fn package_hash_skips_previews_and_hidden_files() {
        let d = std::env::temp_dir().join(format!("retake-pkg-hash-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(d.join("previews")).unwrap();
        std::fs::create_dir_all(d.join("fonts")).unwrap();
        std::fs::write(d.join("component.json"), "{}").unwrap();
        std::fs::write(d.join("fonts/OFL.txt"), "ofl").unwrap();
        let h1 = package_hash(&d).unwrap();
        std::fs::write(d.join("previews/sheet.png"), "png").unwrap();
        std::fs::write(d.join(".DS_Store"), "x").unwrap();
        assert_eq!(package_hash(&d).unwrap(), h1);
        std::fs::write(d.join("fonts/OFL.txt"), "ofl 2").unwrap();
        assert_ne!(package_hash(&d).unwrap(), h1);
        // The definition, spelled out.
        let mut h = blake3::Hasher::new();
        for (p, c) in [("component.json", "{}"), ("fonts/OFL.txt", "ofl 2")] {
            h.update(format!("{p}\0{}\n", blake3::hash(c.as_bytes()).to_hex()).as_bytes());
        }
        assert_eq!(package_hash(&d).unwrap(), format!("b3:{}", h.finalize().to_hex()));
        let _ = std::fs::remove_dir_all(&d);
    }
}
