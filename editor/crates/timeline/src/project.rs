//! A timeline file on disk: loading, asset paths, the patch log path and the cache directory.

use crate::component::{self, KitInfo, Lock, LockMode, UseInfo};
use crate::error::{EditError, Errors};
use crate::model::Timeline;
use crate::schema;
use serde_json::Value;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone)]
pub struct Project {
    /// The timeline file.
    pub path: PathBuf,
    /// The folder relative asset paths are resolved against.
    pub dir: PathBuf,
    /// The document as written: what patches change and what is saved.
    pub src: Value,
    /// The document as rendered: kit tokens replaced and component uses expanded into layers (spec/components.md).
    /// Without kits and uses it equals `src`.
    pub doc: Value,
    /// The typed view of `doc`.
    pub tl: Timeline,
    /// The lockfile (`<stem>.lock`), with anything this load pinned.
    pub lock: Lock,
    /// True when `lock` differs from the file on disk (something was pinned).
    pub lock_changed: bool,
    /// The component uses, in document order.
    pub uses: Vec<UseInfo>,
    /// The kits the timeline lists that were found.
    pub kits: Vec<KitInfo>,
    /// `kits` entries that aren't kit packages (plug-ins, unversioned names, catalog kits not installed here):
    /// recorded, not loaded.
    pub kits_recorded: Vec<String>,
}

impl Project {
    /// Reads, parses, expands and schema-checks a timeline file. Every package must be locked.
    pub fn load(path: &Path) -> Result<Project, Errors> {
        Project::load_with(path, LockMode::Strict)
    }

    /// Like [`Project::load`], with another lock mode (`component lock` pins).
    pub fn load_with(path: &Path, mode: LockMode) -> Result<Project, Errors> {
        let text = std::fs::read_to_string(path)
            .map_err(|e| EditError::new("timeline_unreadable", format!("can't read {}: {e}", path.display())))?;
        let doc: Value = serde_json::from_str(&text).map_err(|e| {
            EditError::new("timeline_json", format!("{} isn't valid JSON: {e}", path.display()))
                .hint("fix the JSON syntax at the line and column given")
        })?;
        Project::open(path, doc, mode, None)
    }

    /// Builds a project from an in-memory document that claims to live at `path` (its lockfile is read from there).
    pub fn from_doc(path: &Path, doc: Value) -> Result<Project, Errors> {
        Project::open(path, doc, LockMode::Strict, None)
    }

    /// The general constructor: `lock` overrides the lockfile on disk, `mode` says what may be pinned.
    pub fn open(path: &Path, src: Value, mode: LockMode, lock: Option<Lock>) -> Result<Project, Errors> {
        let abs = std::path::absolute(path).unwrap_or_else(|_| path.to_path_buf());
        let dir = abs.parent().map(Path::to_path_buf).unwrap_or_else(|| PathBuf::from("."));
        Project::open_in(&abs, src, mode, lock, component::search_roots(&dir))
    }

    /// [`Project::open`] with explicit component search roots (previews use the component's own folder).
    pub fn open_in(
        path: &Path,
        src: Value,
        mode: LockMode,
        lock: Option<Lock>,
        roots: Vec<PathBuf>,
    ) -> Result<Project, Errors> {
        let abs = std::path::absolute(path).unwrap_or_else(|_| path.to_path_buf());
        let dir = abs.parent().map(Path::to_path_buf).unwrap_or_else(|| PathBuf::from("."));
        let on_disk = match lock {
            Some(l) => l,
            None => Lock::read(&component::lock_path(&abs))?,
        };
        // A document that isn't even an object with the basic shape fails the schema first, with its usual errors.
        if !src.is_object() {
            return Err(Errors(schema::check_timeline(&src)));
        }
        let x = component::expand_with_roots(&src, &dir, roots, on_disk.clone(), mode).map_err(Errors)?;
        let errs = schema::check_timeline(&x.doc);
        if !errs.is_empty() {
            return Err(Errors(errs.into_iter().map(|e| blame_component(e, &x.uses)).collect()));
        }
        let tl: Timeline = serde_json::from_value(x.doc.clone())
            .map_err(|e| EditError::new("timeline_model", format!("timeline doesn't fit the model: {e}")))?;
        let lock_changed = x.lock != on_disk;
        Ok(Project {
            path: abs,
            dir,
            src,
            doc: x.doc,
            tl,
            lock: x.lock,
            lock_changed,
            uses: x.uses,
            kits: x.kits,
            kits_recorded: x.recorded,
        })
    }

    /// The lockfile's path.
    pub fn lock_path(&self) -> PathBuf {
        component::lock_path(&self.path)
    }

    /// The component use of an element, if it has one.
    pub fn use_of(&self, element: &str) -> Option<&UseInfo> {
        self.uses.iter().find(|u| u.element == element)
    }

    /// Expands `~`, `$VAR` and `${VAR}` and resolves a relative path against the timeline's folder.
    pub fn resolve_path(&self, p: &str) -> Result<PathBuf, EditError> {
        let expanded = expand(p)?;
        let pb = PathBuf::from(expanded);
        Ok(if pb.is_absolute() { pb } else { self.dir.join(pb) })
    }

    /// The file behind an asset id.
    pub fn asset_path(&self, id: &str) -> Result<PathBuf, EditError> {
        let a = self.tl.assets.get(id).ok_or_else(|| unknown_asset(self, id))?;
        self.resolve_path(&a.path)
    }

    /// `<stem>.patches.jsonl` next to the timeline.
    pub fn log_path(&self) -> PathBuf {
        let stem = self.path.file_stem().map(|s| s.to_string_lossy().into_owned()).unwrap_or_else(|| "timeline".into());
        self.dir.join(format!("{stem}.patches.jsonl"))
    }

    /// `.retake/` next to the timeline (cache and renders), unless `RETAKE_DIR` says otherwise.
    pub fn work_dir(&self) -> PathBuf {
        match std::env::var_os("RETAKE_DIR") {
            Some(d) if !d.is_empty() => PathBuf::from(d),
            _ => self.dir.join(".retake"),
        }
    }
}

/// A schema error inside a component's expansion names the component: the template is at fault, not the use.
fn blame_component(e: EditError, uses: &[UseInfo]) -> EditError {
    match e.path.as_deref().and_then(|p| uses.iter().find(|u| p.starts_with(&format!("{}/layers", u.ptr)))) {
        Some(u) => EditError {
            code: "component_template".into(),
            message: format!("{} expands into an invalid layer: {}", u.reference, e.message),
            hint: Some(format!("fix the layer templates in {}", u.dir.join("component.json").display())),
            ..e
        },
        None => e,
    }
}

pub fn unknown_asset(p: &Project, id: &str) -> EditError {
    EditError::new("asset_unknown", format!("asset `{id}` isn't in the timeline's assets"))
        .suggest(crate::error::close_matches(id, p.tl.assets.keys().map(String::as_str), 3))
        .hint("add it under \"assets\" with a path, or use one of the suggestions")
}

/// `~/x` → `$HOME/x`; `$VAR` and `${VAR}` from the environment. An unset variable is an error, not "".
pub fn expand(p: &str) -> Result<String, EditError> {
    let mut s = p.to_string();
    if let Some(rest) = s.strip_prefix("~/") {
        let home = std::env::var("HOME").map_err(|_| EditError::new("path_env", "HOME isn't set"))?;
        s = format!("{home}/{rest}");
    }
    let mut out = String::with_capacity(s.len());
    let chars: Vec<char> = s.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        if chars[i] == '$' && i + 1 < chars.len() {
            let (name, next) = if chars[i + 1] == '{' {
                let end = chars[i + 2..]
                    .iter()
                    .position(|c| *c == '}')
                    .map(|e| e + i + 2)
                    .ok_or_else(|| EditError::new("path_env", format!("unclosed ${{ in path `{p}`")))?;
                (chars[i + 2..end].iter().collect::<String>(), end + 1)
            } else {
                let end = chars[i + 1..]
                    .iter()
                    .position(|c| !(c.is_ascii_alphanumeric() || *c == '_'))
                    .map(|e| e + i + 1)
                    .unwrap_or(chars.len());
                (chars[i + 1..end].iter().collect::<String>(), end)
            };
            if name.is_empty() {
                out.push('$');
                i += 1;
                continue;
            }
            let val = std::env::var(&name).map_err(|_| {
                EditError::new("path_env", format!("path `{p}` uses ${name}, which isn't set"))
                    .hint(format!("export {name}=… before running"))
            })?;
            out.push_str(&val);
            i = next;
        } else {
            out.push(chars[i]);
            i += 1;
        }
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn expands_env_and_home() {
        // SAFETY: tests in this module don't read these variables concurrently.
        unsafe {
            std::env::set_var("RETAKE_TEST_ROOT", "/media/x");
        }
        assert_eq!(expand("${RETAKE_TEST_ROOT}/a.mp4").unwrap(), "/media/x/a.mp4");
        assert_eq!(expand("$RETAKE_TEST_ROOT/a.mp4").unwrap(), "/media/x/a.mp4");
        assert_eq!(expand("plain/$").unwrap(), "plain/$");
        assert_eq!(expand("$RETAKE_TEST_UNSET_VAR/x").unwrap_err().code, "path_env");
        let home = std::env::var("HOME").unwrap();
        assert_eq!(expand("~/v.mp4").unwrap(), format!("{home}/v.mp4"));
    }
}
