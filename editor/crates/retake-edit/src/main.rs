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
        /// Output MP4 (default: `.retake/renders/<id>-r<rev>[-draft].mp4`); the manifest goes next to it.
        #[arg(short, long)]
        out: Option<PathBuf>,
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
        Cmd::Render { timeline, out, r, c } => {
            let e = Engine::open(&timeline, r.options())?;
            let out = out.unwrap_or_else(|| pipeline::default_output(&e));
            let rep = pipeline::render(&e, Some(&out))?;
            let human = format!(
                "{}\n  {} units: {} reused, {} rendered; graphics runs: {} reused, {} rendered ({} of {} frames drawn); audio {}\n  {:.3} s, {} frames; {}",
                out.display(),
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
        Cmd::SynthVo { .. } => false,
    }
}

fn main() -> ExitCode {
    let cli = Cli::parse();
    let json_mode = wants_json(&cli.cmd);
    match run(cli.cmd) {
        Ok((j, v, human)) => {
            emit(j, v, || human);
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
