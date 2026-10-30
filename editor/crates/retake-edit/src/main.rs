//! `retake-edit`: the editor engine's CLI. An agent edits a timeline through patches; this validates, resolves,
//! plans, renders (re-rendering only what changed) and publishes a render manifest. `--json` everywhere.

mod synth;

use anyhow::{Context, Result, anyhow};
use clap::{Args, Parser, Subcommand};
use retake_render::{Engine, Options, pipeline};
use retake_timeline::manifest::{Manifest, resolve_note};
use retake_timeline::patch::{self, read_log};
use retake_timeline::{EditError, Errors, Project};
use serde_json::{Value, json};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::ExitCode;

#[derive(Parser)]
#[command(name = "retake-edit", version, about = "Code-only video editing: timeline in, video and render manifest out")]
struct Cli {
    #[command(subcommand)]
    cmd: Cmd,
}

#[derive(Args, Clone)]
struct Common {
    /// Print one JSON object (`ok`, then the result or `errors`).
    #[arg(long, global = true)]
    json: bool,
}

#[derive(Args, Clone)]
struct RenderArgs {
    /// Draft quality: half size and a fast encoder preset.
    #[arg(long)]
    draft: bool,
    /// Output scale (overrides the draft's 0.5).
    #[arg(long)]
    scale: Option<f64>,
    /// Output seconds `a-b`; renders the whole units that overlap it.
    #[arg(long, value_parser = parse_range)]
    range: Option<(f64, f64)>,
    /// Units rendered in parallel.
    #[arg(long, default_value_t = 3)]
    jobs: usize,
    /// Cache directory (default: `.retake/cache` next to the timeline).
    #[arg(long)]
    cache: Option<PathBuf>,
}

impl RenderArgs {
    fn options(&self) -> Options {
        Options { draft: self.draft, scale: self.scale, range: self.range, jobs: self.jobs, cache: self.cache.clone() }
    }
}

fn parse_range(s: &str) -> Result<(f64, f64), String> {
    let (a, b) = s.split_once('-').ok_or("use a-b, e.g. 10-25.5")?;
    let a: f64 = a.parse().map_err(|_| format!("bad start `{a}`"))?;
    let b: f64 = b.parse().map_err(|_| format!("bad end `{b}`"))?;
    if b <= a {
        return Err("the end must be after the start".into());
    }
    Ok((a, b))
}

#[derive(Subcommand)]
enum Cmd {
    /// Schema and semantic checks: ids, references, anchors.
    Validate {
        timeline: PathBuf,
        #[command(flatten)]
        c: Common,
    },
    /// Resolve every anchor to seconds and frames; list the units.
    Resolve {
        timeline: PathBuf,
        #[command(flatten)]
        c: Common,
    },
    /// Apply, list or revert patches (the only way an agent changes a timeline).
    Patch {
        #[command(subcommand)]
        cmd: PatchCmd,
    },
    /// Units, cache hits and misses, and a render-time estimate.
    Plan {
        timeline: PathBuf,
        #[command(flatten)]
        r: RenderArgs,
        #[command(flatten)]
        c: Common,
    },
    /// Render: only units whose description changed are drawn; the rest come from the cache.
    Render {
        timeline: PathBuf,
        /// Output MP4 (default: `.retake/renders/<id>-r<rev>[-draft].mp4`); the manifest goes next to it. With
        /// `--hls`, an MP4 packaged at the end as well (none by default).
        #[arg(short, long)]
        out: Option<PathBuf>,
        /// Stream into an HLS EVENT playlist that grows as units finish, in timeline order; segments go beside it,
        /// and the manifest once it ends (spec/bridge.md, "Streaming").
        #[arg(long)]
        hls: Option<PathBuf>,
        /// Print progress lines (one JSON object each) on stderr.
        #[arg(long)]
        progress: bool,
        #[command(flatten)]
        r: RenderArgs,
        #[command(flatten)]
        c: Common,
    },
    /// Stills at output times, as PNG (renders only those frames).
    Frames {
        timeline: PathBuf,
        #[arg(required = true)]
        times: Vec<f64>,
        /// Folder for the PNGs (default: `.retake/frames`).
        #[arg(short, long)]
        out: Option<PathBuf>,
        #[command(flatten)]
        r: RenderArgs,
        #[command(flatten)]
        c: Common,
    },
    /// The render manifest for the current revision (renders missing units first; doesn't package a video).
    Manifest {
        timeline: PathBuf,
        /// Also write it to this file.
        #[arg(short, long)]
        out: Option<PathBuf>,
        #[command(flatten)]
        r: RenderArgs,
        #[command(flatten)]
        c: Common,
    },
    /// Short-GOP, draft-size proxies of video assets (all the timeline's videos, or the given asset ids). Draft
    /// renders decode a proxy instead of its source once it exists.
    Proxy {
        timeline: PathBuf,
        /// Asset ids, e.g. proj:footage (default: every video asset the timeline uses).
        assets: Vec<String>,
        /// Proxy height in pixels (never above the source's).
        #[arg(long, default_value_t = retake_render::proxy::DEFAULT_HEIGHT)]
        height: u32,
        /// Make them again even if they exist.
        #[arg(long)]
        force: bool,
        /// Cache directory (default: `.retake/cache` next to the timeline).
        #[arg(long)]
        cache: Option<PathBuf>,
        #[command(flatten)]
        c: Common,
    },
    /// Rank the elements under a reviewer's stroke at time `t` (box normalized 0-1, or pixels with --px).
    ResolveNote {
        manifest: PathBuf,
        t: f64,
        /// x y w h
        #[arg(num_args = 0..=4)]
        bbox: Vec<f64>,
        #[arg(long)]
        px: bool,
        #[command(flatten)]
        c: Common,
    },
    /// Components: lock, upgrade, preview, hash (spec/components.md).
    Component {
        #[command(subcommand)]
        cmd: ComponentCmd,
    },
    /// Check a timeline against its kits' house rules; exits 1 on errors.
    Lint {
        timeline: PathBuf,
        /// Rules from this JSON file (a list, or an object with "rules") instead of the kits'.
        #[arg(long)]
        rules: Option<PathBuf>,
        /// Also print the elements the rules saw.
        #[arg(long)]
        elements: bool,
        #[command(flatten)]
        c: Common,
    },
    /// The timeline as rendered: kit tokens replaced, component uses expanded into layers.
    Expand {
        timeline: PathBuf,
        #[command(flatten)]
        c: Common,
    },
    /// Test media: a speech-like tone track from a words file (no TTS, no voice).
    #[command(hide = true)]
    SynthVo {
        words: PathBuf,
        #[arg(short, long)]
        out: PathBuf,
        #[arg(long, default_value_t = 48000)]
        sr: u32,
        #[arg(long)]
        dur: Option<f64>,
    },
}

#[derive(Subcommand)]
enum PatchCmd {
    /// Apply a patch (a file, or `-` for stdin).
    Apply {
        timeline: PathBuf,
        patch: PathBuf,
        /// Check and report without writing.
        #[arg(long)]
        dry_run: bool,
        #[command(flatten)]
        c: Common,
    },
    /// The patch log.
    Log {
        timeline: PathBuf,
        #[command(flatten)]
        c: Common,
    },
    /// Revert a patch by id (as a new patch).
    Revert {
        timeline: PathBuf,
        id: String,
        #[arg(long)]
        dry_run: bool,
        #[command(flatten)]
        c: Common,
    },
}

#[derive(Subcommand)]
enum ComponentCmd {
    /// Pin every package the timeline uses in its lockfile (`<stem>.lock`).
    Lock {
        timeline: PathBuf,
        /// Re-pin this package (name@version) to its current hash (repeatable).
        #[arg(long)]
        update: Vec<String>,
        /// Re-pin every package to its current hash.
        #[arg(long)]
        update_all: bool,
        #[command(flatten)]
        c: Common,
    },
    /// Move an element (or every element using a component) to another version, as one patch.
    Upgrade {
        timeline: PathBuf,
        /// An element id, or `all`.
        target: String,
        /// The version to use, e.g. 1.1.0.
        #[arg(long)]
        to: String,
        /// With `all`: the component to upgrade (needed when the timeline uses several).
        #[arg(long)]
        component: Option<String>,
        /// Note ids the upgrade answers (repeatable).
        #[arg(long)]
        answers: Vec<String>,
        #[arg(long)]
        dry_run: bool,
        #[command(flatten)]
        c: Common,
    },
    /// Render each example at five moments into previews/sheet.png, plus a 2-second WebP.
    Preview {
        dir: PathBuf,
        /// Output folder (default: <dir>/previews).
        #[arg(short, long)]
        out: Option<PathBuf>,
        /// Skip the animated WebP.
        #[arg(long)]
        no_webp: bool,
        #[command(flatten)]
        c: Common,
    },
    /// A package's content hash (what lockfiles and registries pin).
    Hash {
        dir: PathBuf,
        #[command(flatten)]
        c: Common,
    },
}

/// Set when lint found errors: the process exits 1 after printing the report.
static LINT_FAILED: std::sync::atomic::AtomicBool = std::sync::atomic::AtomicBool::new(false);

/// Turns any failure into typed errors an agent can act on.
fn to_errors(e: &anyhow::Error) -> Vec<EditError> {
    if let Some(es) = e.downcast_ref::<Errors>() {
        return es.0.clone();
    }
    if let Some(x) = e.downcast_ref::<EditError>() {
        return vec![x.clone()];
    }
    for cause in e.chain() {
        if let Some(es) = cause.downcast_ref::<Errors>() {
            return es.0.clone();
        }
        if let Some(x) = cause.downcast_ref::<EditError>() {
            let mut x = x.clone();
            x.message = format!("{e:#}");
            return vec![x];
        }
    }
    let msg = format!("{e:#}");
    let code = if msg.contains("ffmpeg") { "ffmpeg_failed" } else { "internal" };
    vec![EditError::new(code, msg)]
}

fn emit(json_mode: bool, v: Value, human: impl FnOnce() -> String) {
    if json_mode {
        let mut o = json!({"ok": true});
        if let (Some(m), Value::Object(r)) = (o.as_object_mut(), v) {
            m.extend(r);
        }
        println!("{}", serde_json::to_string_pretty(&o).unwrap_or_default());
    } else {
        println!("{}", human());
    }
}

fn read_json(p: &Path) -> Result<Value> {
    let text = if p == Path::new("-") {
        let mut s = String::new();
        std::io::stdin().read_to_string(&mut s)?;
        s
    } else {
        std::fs::read_to_string(p).with_context(|| format!("reading {}", p.display()))?
    };
    serde_json::from_str(&text)
        .map_err(|e| anyhow!(EditError::new("patch_json", format!("{} isn't valid JSON: {e}", p.display()))))
}

/// Units whose key a patch changes (independent of what's cached): what it will re-render.
fn affected_units(old: &Project, new: &Project) -> Option<Value> {
    let keys = |p: &Project| -> Option<Vec<(String, String)>> {
        let e = Engine::from_project(p.clone(), Options::default()).ok()?;
        Some(e.plan().ok()?.iter().map(|u| (format!("u{}", u.span.index + 1), u.key.clone())).collect())
    };
    let (a, b) = (keys(old)?, keys(new)?);
    let before: std::collections::HashSet<&String> = a.iter().map(|(_, k)| k).collect();
    let changed: Vec<&String> = b.iter().filter(|(_, k)| !before.contains(k)).map(|(n, _)| n).collect();
    Some(json!({"units": b.len(), "rerender": changed.len(), "changed": changed}))
}

fn run(cmd: Cmd) -> Result<(bool, Value, String)> {
    match cmd {
        Cmd::Validate { timeline, c } => {
            let p = Project::load(&timeline)?;
            let errs = retake_timeline::validate(&p);
            if !errs.is_empty() {
                return Err(Errors(errs).into());
            }
            let r = retake_timeline::resolve(&p).map_err(Errors)?;
            let human = format!(
                "ok: {} rev {}, {:.3} s, {} units, {} shots, {} overlays",
                p.tl.id,
                p.tl.rev,
                r.end,
                r.units.len(),
                r.shots.len(),
                r.overlays.len()
            );
            Ok((c.json, json!({"id": p.tl.id, "rev": p.tl.rev, "duration": r.end, "units": r.units.len()}), human))
        }
        Cmd::Resolve { timeline, c } => {
            let p = Project::load(&timeline)?;
            let r = retake_timeline::resolve(&p).map_err(Errors)?;
            let mut lines =
                vec![format!("{} rev {}: {:.3} s, {} frames at {} fps", p.tl.id, p.tl.rev, r.end, r.frames, r.fps)];
            for s in &r.spine {
                lines.push(format!("  spine {:<16} {:>8.3} {:>8.3}  {:?}", s.id, s.start, s.end, s.kind));
            }
            for s in &r.shots {
                lines.push(format!("  shot  {:<16} {:>8.3} {:>8.3}", s.id, s.start, s.end));
            }
            for o in &r.overlays {
                lines.push(format!("  over  {:<16} {:>8.3} {:>8.3}  z {}", o.id, o.from, o.to, o.z));
            }
            for a in &r.audio {
                lines.push(format!("  audio {:<16} {:>8.3} {:>8.3}", a.id, a.from, a.to));
            }
            for u in &r.units {
                lines.push(format!("  unit  u{:<15} {:>8} {:>8}  {:?} {}", u.index + 1, u.f0, u.f1, u.kind, u.source));
            }
            Ok((c.json, serde_json::to_value(&r)?, lines.join("\n")))
        }
        Cmd::Patch { cmd: PatchCmd::Apply { timeline, patch: pf, dry_run, c } } => {
            let p = Project::load(&timeline)?;
            let pj = read_json(&pf)?;
            let a = patch::apply(&p, &pj)?;
            if !dry_run {
                patch::commit(&a)?;
            }
            let affected = affected_units(&p, &a.project);
            let human = format!(
                "{} {} (rev {} → {}){}{}",
                if dry_run { "would apply" } else { "applied" },
                a.entry.id,
                a.entry.base,
                a.entry.rev,
                if a.entry.answers.is_empty() {
                    String::new()
                } else {
                    format!(", answers {}", a.entry.answers.join(" "))
                },
                affected
                    .as_ref()
                    .map(|v| format!("; re-renders {} of {} units", v["rerender"], v["units"]))
                    .unwrap_or_default()
            );
            Ok((c.json, json!({"applied": !dry_run, "patch": a.entry, "affected": affected}), human))
        }
        Cmd::Patch { cmd: PatchCmd::Log { timeline, c } } => {
            let p = Project::load(&timeline)?;
            let log = read_log(&p.log_path())?;
            let human = log
                .iter()
                .map(|e| {
                    format!(
                        "{:<5} rev {:<4} {} [{}]{}",
                        e.id,
                        e.rev,
                        e.summary,
                        if e.answers.is_empty() { "own initiative".to_string() } else { e.answers.join(" ") },
                        e.reverts.as_ref().map(|r| format!(" (reverts {r})")).unwrap_or_default()
                    )
                })
                .collect::<Vec<_>>()
                .join("\n");
            Ok((
                c.json,
                json!({"rev": p.tl.rev, "patches": log}),
                if human.is_empty() { "no patches".into() } else { human },
            ))
        }
        Cmd::Patch { cmd: PatchCmd::Revert { timeline, id, dry_run, c } } => {
            let p = Project::load(&timeline)?;
            let rp = patch::revert_patch(&p, &id)?;
            let a = patch::apply(&p, &rp)?;
            if !dry_run {
                patch::commit(&a)?;
            }
            let human = format!(
                "{} {} as {} (rev {})",
                if dry_run { "would revert" } else { "reverted" },
                id,
                a.entry.id,
                a.entry.rev
            );
            Ok((c.json, json!({"applied": !dry_run, "patch": a.entry}), human))
        }
        Cmd::Plan { timeline, r, c } => {
            let e = Engine::open(&timeline, r.options())?;
            let (_, rep) = pipeline::plan(&e)?;
            let mut lines = vec![format!(
                "{} rev {}: {} units, {} cached, {} to render ({} frames, {} graphics runs), audio {}, ~{:.1} s",
                rep.timeline.id,
                rep.timeline.rev,
                rep.units.len(),
                rep.hits,
                rep.misses,
                rep.frames_to_render,
                rep.layers_to_render,
                if rep.audio_cached { "cached" } else { "to mix" },
                rep.estimate_s
            )];
            for u in &rep.units {
                lines.push(format!(
                    "  {:<5} {:<6} {:<18} {:>8.3}-{:<8.3} {}  {}",
                    u.unit,
                    u.kind,
                    u.source,
                    u.t[0],
                    u.t[1],
                    if u.cached { "hit " } else { "MISS" },
                    &u.key[3..15]
                ));
            }
            Ok((c.json, serde_json::to_value(&rep)?, lines.join("\n")))
        }
        Cmd::Render { timeline, out, hls, progress, r, c } => {
            let e = Engine::open(&timeline, r.options())?;
            let (rep, shown) = match hls {
                Some(playlist) => {
                    // A test hook: every unit takes at least this long, so a test can watch the playlist grow. A list
                    // (`900,0`) gives each unit its own, the last one standing for the rest.
                    let delay: Vec<std::time::Duration> = std::env::var("RETAKE_EDIT_TEST_UNIT_DELAY_MS")
                        .unwrap_or_default()
                        .split(',')
                        .filter_map(|v| v.trim().parse::<u64>().ok())
                        .map(std::time::Duration::from_millis)
                        .collect();
                    let report = |v: Value| {
                        if progress {
                            eprintln!("{v}");
                        }
                    };
                    let o = retake_render::stream::StreamOptions {
                        playlist: playlist.clone(),
                        mp4: out,
                        unit_delay: delay,
                        progress: &report,
                    };
                    (retake_render::stream::render_stream(&e, &o)?, playlist)
                }
                None => {
                    let out = out.unwrap_or_else(|| pipeline::default_output(&e));
                    (pipeline::render(&e, Some(&out))?, out)
                }
            };
            let human = format!(
                "{}\n  {} units: {} reused, {} rendered; graphics runs: {} reused, {} rendered ({} of {} frames drawn); audio {}\n  {:.3} s, {} frames; {}",
                shown.display(),
                rep.units,
                rep.reused,
                rep.rendered,
                rep.layers_reused,
                rep.layers_rendered,
                rep.graphics_drawn,
                rep.graphics_frames,
                if rep.audio_reused { "reused" } else { "mixed" },
                rep.duration,
                rep.frames,
                rep.timings.iter().map(|(k, v)| format!("{k} {v:.2}s")).collect::<Vec<_>>().join(", ")
            );
            Ok((c.json, serde_json::to_value(&rep)?, human))
        }
        Cmd::Frames { timeline, times, out, r, c } => {
            let e = Engine::open(&timeline, r.options())?;
            let dir = out.unwrap_or_else(|| e.project.work_dir().join("frames"));
            let shots = pipeline::stills(&e, &times, &dir)?;
            let human = shots.iter().map(|(t, p)| format!("{t:>8.3}  {}", p.display())).collect::<Vec<_>>().join("\n");
            let v: Vec<Value> = shots.iter().map(|(t, p)| json!({"t": t, "path": p})).collect();
            Ok((c.json, json!({"frames": v}), human))
        }
        Cmd::Manifest { timeline, out, r, c } => {
            let e = Engine::open(&timeline, r.options())?;
            let rep = pipeline::render(&e, None)?;
            let text = serde_json::to_string_pretty(&rep.manifest)?;
            if let Some(o) = &out {
                std::fs::write(o, &text)?;
            }
            Ok((c.json, json!({"manifest": rep.manifest}), text))
        }
        Cmd::Proxy { timeline, assets, height, force, cache, c } => {
            let e = Engine::open(&timeline, Options { cache, ..Options::default() })?;
            let ids: Vec<String> = if assets.is_empty() {
                e.assets.values().filter(|a| a.info.size.is_some()).map(|a| a.id.clone()).collect()
            } else {
                assets
            };
            let mut done = vec![];
            for id in &ids {
                let a = e.assets.get(id).ok_or_else(|| {
                    EditError::new("asset_unknown", format!("the timeline renders no asset `{id}`"))
                        .suggest(e.assets.keys().cloned().collect())
                })?;
                done.push(retake_render::proxy::make(&e.store, a, height, force)?);
            }
            let human = done
                .iter()
                .map(|p| {
                    format!(
                        "{:<16} {}x{}, keyframe every {} frames{}  {}",
                        p.asset,
                        p.size[0],
                        p.size[1],
                        p.gop,
                        if p.made { format!(", made in {:.1} s", p.seconds) } else { ", already there".into() },
                        p.proxy.display()
                    )
                })
                .collect::<Vec<_>>()
                .join("\n");
            Ok((c.json, json!({"proxies": done}), if human.is_empty() { "no video assets".into() } else { human }))
        }
        Cmd::ResolveNote { manifest, t, bbox, px, c } => {
            let m: Manifest = serde_json::from_str(
                &std::fs::read_to_string(&manifest).with_context(|| format!("reading {}", manifest.display()))?,
            )
            .map_err(|e| EditError::new("manifest_invalid", format!("{}: {e}", manifest.display())))?;
            let stroke = match bbox.len() {
                0 => None,
                4 => {
                    let [x, y, w, h] = [bbox[0], bbox[1], bbox[2], bbox[3]];
                    Some(if px {
                        [x, y, w, h]
                    } else {
                        let (sw, sh) = (m.size[0] as f64, m.size[1] as f64);
                        [x * sw, y * sh, w * sw, h * sh]
                    })
                }
                _ => return Err(EditError::new("box_invalid", "the box is x y w h").into()),
            };
            let r = resolve_note(&m, t, stroke);
            let human = std::iter::once(format!(
                "t {t}: {}{}",
                r.unit.clone().unwrap_or_else(|| "no unit".into()),
                r.word.as_ref().map(|w| format!(", word '{}'", w.2)).unwrap_or_default()
            ))
            .chain(r.targets.iter().map(|x| format!("  {:.3} z{:<3} {:<8} {}", x.score, x.z, x.kind, x.id)))
            .collect::<Vec<_>>()
            .join("\n");
            Ok((c.json, serde_json::to_value(&r)?, human))
        }
        Cmd::SynthVo { words, out, sr, dur } => {
            let n = synth::synth_vo(&words, &out, sr, dur)?;
            Ok((false, Value::Null, format!("{}: {n} words", out.display())))
        }
        Cmd::Component { cmd } => component_cmd(cmd),
        Cmd::Lint { timeline, rules, elements, c } => {
            let rules = match rules {
                Some(f) => {
                    let v = read_json(&f)?;
                    let list = v.get("rules").cloned().unwrap_or(v);
                    Some(list.as_array().cloned().ok_or_else(|| {
                        EditError::new("lint_rules", format!("{} holds no list of rules", f.display()))
                    })?)
                }
                None => None,
            };
            let r = retake_render::lint::lint_timeline(&timeline, rules, elements)?;
            if r.report.errors > 0 {
                LINT_FAILED.store(true, std::sync::atomic::Ordering::Relaxed);
            }
            let mut lines = vec![format!(
                "{}: {} error(s), {} warning(s) from {} rule(s) over {} element(s){}",
                timeline.display(),
                r.report.errors,
                r.report.warnings,
                r.report.rules,
                r.report.elements,
                if r.boxes { "" } else { " (no boxes: safe-area rules skipped)" }
            )];
            for v in &r.report.violations {
                lines.push(format!("  {:<7} {:<24} {}", v.severity, v.id, v.message));
                if let Some(h) = &v.hint {
                    lines.push(format!("  {:<7} {:<24} → {h}", "", ""));
                }
            }
            for n in &r.notes {
                lines.push(format!("  note    {}", n.message));
            }
            Ok((c.json, serde_json::to_value(&r)?, lines.join("\n")))
        }
        Cmd::Expand { timeline, c } => {
            let p = Project::load(&timeline)?;
            let text = patch::pretty(&p.doc);
            Ok((
                c.json,
                json!({"timeline": p.doc, "uses": p.uses, "kits": p.kits, "kits_recorded": p.kits_recorded}),
                text.trim_end().to_string(),
            ))
        }
    }
}

fn component_cmd(cmd: ComponentCmd) -> Result<(bool, Value, String)> {
    use retake_timeline::component::{self, LockMode};
    match cmd {
        ComponentCmd::Lock { timeline, update, update_all, c } => {
            let mode = if update_all {
                LockMode::Repin(vec![])
            } else if !update.is_empty() {
                LockMode::Repin(update)
            } else {
                LockMode::PinNew
            };
            let p = Project::load_with(&timeline, mode)?;
            if p.lock_changed {
                p.lock.write(&p.lock_path())?;
            }
            let human = std::iter::once(format!(
                "{} {}",
                p.lock_path().display(),
                if p.lock_changed { "written" } else { "unchanged" }
            ))
            .chain(p.lock.packages.iter().map(|(k, e)| format!("  {k:<28} {:<9} {}", e.kind, e.hash)))
            .collect::<Vec<_>>()
            .join("\n");
            Ok((c.json, json!({"lock": p.lock_path(), "changed": p.lock_changed, "packages": p.lock.packages}), human))
        }
        ComponentCmd::Upgrade { timeline, target, to, component, answers, dry_run, c } => {
            if !component::is_semver(&to) {
                return Err(
                    EditError::new("version_invalid", format!("--to {to}: use a semver version, e.g. 1.1.0")).into()
                );
            }
            let p = Project::load(&timeline)?;
            let picked: Vec<&component::UseInfo> = if target == "all" {
                let names: std::collections::BTreeSet<&str> = p.uses.iter().map(|u| u.name.as_str()).collect();
                let name = match (&component, names.len()) {
                    (Some(n), _) => n.clone(),
                    (None, 1) => names.iter().next().unwrap().to_string(),
                    (None, 0) => {
                        return Err(EditError::new("upgrade_nothing", "the timeline uses no components").into());
                    }
                    (None, _) => {
                        return Err(EditError::new("upgrade_ambiguous", "the timeline uses several components")
                            .hint("name one with --component")
                            .suggest(names.iter().map(|s| s.to_string()).collect())
                            .into());
                    }
                };
                p.uses.iter().filter(|u| u.name == name).collect()
            } else {
                let u = p.use_of(&target).ok_or_else(|| {
                    EditError::new("upgrade_target", format!("`{target}` uses no component"))
                        .suggest(p.uses.iter().map(|u| u.element.clone()).collect())
                })?;
                vec![u]
            };
            let picked: Vec<&component::UseInfo> = picked.into_iter().filter(|u| u.version != to).collect();
            if picked.is_empty() {
                return Err(EditError::new("upgrade_nothing", format!("nothing to upgrade: already at {to}")).into());
            }
            let ops: Vec<Value> = picked
                .iter()
                .map(|u| {
                    let path = if u.kind == "overlay" { "use" } else { "picture/use" };
                    json!({"op": "set", "id": u.element, "path": path, "value": format!("{}@{to}", u.name)})
                })
                .collect();
            let from: std::collections::BTreeSet<&str> = picked.iter().map(|u| u.version.as_str()).collect();
            let summary = format!(
                "Upgrade {} {} → {to} on {}",
                picked[0].name,
                from.into_iter().collect::<Vec<_>>().join(", "),
                picked.iter().map(|u| u.element.as_str()).collect::<Vec<_>>().join(", ")
            );
            let pj = json!({"base": p.tl.rev, "answers": answers, "summary": summary, "ops": ops});
            let a = patch::apply(&p, &pj)?;
            if !dry_run {
                patch::commit(&a)?;
            }
            let affected = affected_units(&p, &a.project);
            let human = format!(
                "{} {} (rev {} → {}): {summary}{}",
                if dry_run { "would apply" } else { "applied" },
                a.entry.id,
                a.entry.base,
                a.entry.rev,
                affected
                    .as_ref()
                    .map(|v| format!("; re-renders {} of {} units", v["rerender"], v["units"]))
                    .unwrap_or_default()
            );
            Ok((c.json, json!({"applied": !dry_run, "patch": a.entry, "affected": affected}), human))
        }
        ComponentCmd::Preview { dir, out, no_webp, c } => {
            let r = retake_render::preview::preview(
                &dir,
                &retake_render::preview::PreviewOptions { out, webp: !no_webp, work: None },
            )?;
            let human = format!(
                "{}: {} ({} KB){}, {} example(s) in {:.1} s",
                r.component,
                r.sheet.display(),
                r.sheet_bytes / 1024,
                r.webp.as_ref().map(|w| format!(", {}", w.display())).unwrap_or_default(),
                r.examples.len(),
                r.seconds
            );
            Ok((c.json, serde_json::to_value(&r)?, human))
        }
        ComponentCmd::Hash { dir, c } => {
            let hash = component::package_hash(&dir)?;
            let files: Vec<String> = component::package_files(&dir)?.into_iter().map(|(r, _)| r).collect();
            Ok((c.json, json!({"hash": hash, "files": files}), hash))
        }
    }
}

fn wants_json(cmd: &Cmd) -> bool {
    match cmd {
        Cmd::Validate { c, .. }
        | Cmd::Resolve { c, .. }
        | Cmd::Plan { c, .. }
        | Cmd::Render { c, .. }
        | Cmd::Frames { c, .. }
        | Cmd::Manifest { c, .. }
        | Cmd::Proxy { c, .. }
        | Cmd::ResolveNote { c, .. } => c.json,
        Cmd::Patch { cmd: PatchCmd::Apply { c, .. } | PatchCmd::Log { c, .. } | PatchCmd::Revert { c, .. } } => c.json,
        Cmd::Component {
            cmd:
                ComponentCmd::Lock { c, .. }
                | ComponentCmd::Upgrade { c, .. }
                | ComponentCmd::Preview { c, .. }
                | ComponentCmd::Hash { c, .. },
        } => c.json,
        Cmd::Lint { c, .. } | Cmd::Expand { c, .. } => c.json,
        Cmd::SynthVo { .. } => false,
    }
}

fn main() -> ExitCode {
    retake_render::init();
    let cli = Cli::parse();
    let json_mode = wants_json(&cli.cmd);
    match run(cli.cmd) {
        Ok((j, v, human)) => {
            emit(j, v, || human);
            if LINT_FAILED.load(std::sync::atomic::Ordering::Relaxed) {
                return ExitCode::from(1);
            }
            ExitCode::SUCCESS
        }
        Err(e) => {
            let errs = to_errors(&e);
            if json_mode {
                println!("{}", serde_json::to_string_pretty(&json!({"ok": false, "errors": errs})).unwrap_or_default());
            } else {
                for x in &errs {
                    eprintln!("{x}");
                    if !x.suggestions.is_empty() {
                        eprintln!("  suggestions: {}", x.suggestions.join(", "));
                    }
                }
            }
            ExitCode::from(1)
        }
    }
}
