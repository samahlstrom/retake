//! Streamed renders (spec/bridge.md, "Streaming"): units render in timeline order on `jobs` threads while the audio
//! mixes on its own, and each unit is published into a growing HLS EVENT playlist as soon as it and every unit before
//! it are done. Segments are MPEG-TS cut from the units' encoded segments without re-encoding; the audio is the one
//! AAC encode of the whole mix, its frames handed out to the units they start in, so there are no seams in it.

use crate::engine::{Engine, RunSpec, UnitPlan};
use crate::expr::Js;
use crate::ffmpeg;
use crate::graphics::Painter;
use crate::pipeline::{self, AudioOut, RenderReport, StreamStats, unit_name};
use crate::unit;
use anyhow::{Context, Result, anyhow, bail};
use retake_timeline::resolve::round6;
use serde_json::{Value, json};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex, OnceLock};
use std::time::{Duration, Instant};

/// Every timestamp in the segments is the output time plus this, so the B-frames' decode times at 0 stay positive.
pub const TS_OFFSET: f64 = 10.0;
/// Segments are about this long (whole GOPs)...
pub const TARGET_S: f64 = 4.0;
/// ...and a unit is cut while more than this much of it remains, so segments run up to 6 s.
pub const MAX_S: f64 = 6.0;
/// AAC-LC frames hold 1024 samples, and ffmpeg's encoder is 1024 samples late (its priming frame).
const AAC_FRAME: i64 = 1024;
const AAC_PRIMING: i64 = 1024;

/// How a streamed render runs.
pub struct StreamOptions<'a> {
    /// The playlist to write; segments go beside it.
    pub playlist: PathBuf,
    /// Also package the whole render as an MP4 here at the end (with its manifest beside it).
    pub mp4: Option<PathBuf>,
    /// A test hook to watch a playlist grow: unit `i` (cached or not) takes at least `unit_delay[i]`, the last entry
    /// standing for the rest. Empty: no delay.
    pub unit_delay: Vec<Duration>,
    /// Called with each progress line (`{"progress": "plan" | "publish" | "end", …}`).
    pub progress: &'a (dyn Fn(Value) + Sync),
}

/// A unit's segments: frame ranges `[a, b)` from its start, cut at its keyframes (every `gop` frames).
pub fn pieces(frames: i64, gop: i64, fps: u32) -> Vec<[i64; 2]> {
    let gop = gop.max(1);
    let piece = ((TARGET_S * fps as f64 / gop as f64).round() as i64).max(1) * gop;
    let most = ((MAX_S * fps as f64).round() as i64).max(piece);
    let mut out = vec![];
    let mut a = 0;
    while frames - a > most {
        out.push([a, a + piece]);
        a += piece;
    }
    out.push([a, frames]);
    out
}

/// Writes a file so that a reader sees either the old or the new one, never half of it: a temporary file beside it
/// (hidden, so the server never serves it), renamed over it.
pub fn write_atomic(path: &Path, bytes: &[u8]) -> Result<()> {
    let dir = path.parent().unwrap_or(Path::new("."));
    let name = path.file_name().context("no file name")?.to_string_lossy();
    let tmp = dir.join(format!(".{name}.{}.tmp", std::process::id()));
    std::fs::write(&tmp, bytes).with_context(|| format!("writing {}", tmp.display()))?;
    std::fs::rename(&tmp, path).with_context(|| format!("replacing {}", path.display()))
}

/// An HLS EVENT playlist that only grows at its end.
#[derive(Debug, Clone, PartialEq)]
pub struct Playlist {
    pub target: u32,
    pub segments: Vec<(String, f64)>,
    pub ended: bool,
}

impl Playlist {
    pub fn new(target: u32) -> Playlist {
        Playlist { target, segments: vec![], ended: false }
    }

    pub fn text(&self) -> String {
        let mut s = format!(
            "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-TARGETDURATION:{}\n#EXT-X-MEDIA-SEQUENCE:0\n\
             #EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-START:TIME-OFFSET=0\n",
            self.target
        );
        for (name, d) in &self.segments {
            s.push_str(&format!("#EXTINF:{d:.6},\n{name}\n"));
        }
        if self.ended {
            s.push_str("#EXT-X-ENDLIST\n");
        }
        s
    }

    /// Playable seconds so far.
    pub fn seconds(&self) -> f64 {
        round6(self.segments.iter().map(|s| s.1).sum())
    }

    pub fn write(&self, path: &Path) -> Result<()> {
        write_atomic(path, self.text().as_bytes())
    }
}

/// A raw ADTS AAC stream and where each frame is in it.
pub struct Adts {
    pub bytes: Vec<u8>,
    /// Byte offset, length, and first sample (on the output clock: the priming frame starts before 0).
    pub frames: Vec<(usize, usize, i64)>,
}

impl Adts {
    pub fn parse(bytes: Vec<u8>) -> Result<Adts> {
        let mut frames = vec![];
        let (mut i, mut sample) = (0usize, -AAC_PRIMING);
        while i < bytes.len() {
            let h = bytes.get(i..i + 7).context("a cut-off ADTS header")?;
            if h[0] != 0xFF || h[1] & 0xF0 != 0xF0 {
                bail!("not an ADTS frame at byte {i}");
            }
            let len = (((h[3] & 3) as usize) << 11) | ((h[4] as usize) << 3) | ((h[5] as usize) >> 5);
            if len < 7 || i + len > bytes.len() {
                bail!("a bad ADTS frame length at byte {i}");
            }
            frames.push((i, len, sample));
            sample += AAC_FRAME * ((h[6] & 3) as i64 + 1);
            i += len;
        }
        Ok(Adts { bytes, frames })
    }

    /// The frames that start in samples `[s0, s1)` of the output (never the priming frame), and the first one's sample.
    pub fn slice(&self, s0: i64, s1: i64) -> (Vec<u8>, Option<i64>) {
        let s0 = s0.max(0);
        let mut out = vec![];
        let mut first = None;
        for &(o, l, s) in &self.frames {
            if s >= s0 && s < s1 {
                first.get_or_insert(s);
                out.extend_from_slice(&self.bytes[o..o + l]);
            }
        }
        (out, first)
    }
}

struct StreamAudio {
    out: AudioOut,
    adts: Adts,
}

fn stream_audio(e: &Engine, plans: &[UnitPlan]) -> Result<StreamAudio> {
    let out = pipeline::audio(e, plans)?;
    let tmp = e.store.tmp_path("aac");
    ffmpeg::to_adts(&e.store.blob_path(&out.aac), &tmp)?;
    let bytes = std::fs::read(&tmp)?;
    let _ = std::fs::remove_file(&tmp);
    Ok(StreamAudio { out, adts: Adts::parse(bytes)? })
}

/// What the threads share: which units are done, and the first error.
struct Board {
    done: Vec<bool>,
    error: Option<anyhow::Error>,
}

/// Renders the whole timeline, publishing each unit into the playlist in timeline order as soon as it can. The
/// playlist ends with `#EXT-X-ENDLIST`, and the manifest goes beside it (`<stem>.manifest.json`, `video` the playlist).
pub fn render_stream(e: &Engine, o: &StreamOptions) -> Result<RenderReport> {
    let start = Instant::now();
    let since = |t: Instant| (t.elapsed().as_secs_f64() * 1000.0).round() / 1000.0;
    if e.opts.range.is_some() {
        bail!("a streamed render covers the whole timeline: --range doesn't go with --hls");
    }
    let dir = o.playlist.parent().filter(|d| !d.as_os_str().is_empty()).unwrap_or(Path::new(".")).to_path_buf();
    std::fs::create_dir_all(&dir).with_context(|| format!("creating {}", dir.display()))?;
    let stem = o.playlist.file_stem().context("the playlist has no name")?.to_string_lossy().into_owned();
    e.store.clean_tmp();
    let mut timings = BTreeMap::new();
    let (plans, _) = pipeline::plan(e)?;
    timings.insert("plan".to_string(), since(start));
    let n = plans.len();
    let fps = e.fps();
    let layout: Vec<Vec<[i64; 2]>> =
        plans.iter().map(|p| pieces(p.span.frames(), p.desc.encode.gop as i64, fps)).collect();
    let longest = layout.iter().flatten().map(|x| x[1] - x[0]).max().unwrap_or(0);
    let target = ((longest as f64 / fps as f64) - 1e-6).ceil().max(1.0) as u32;
    let frames: i64 = plans.iter().map(|p| p.span.frames()).sum();
    let reused = plans.iter().filter(|p| p.cached).count();
    (o.progress)(json!({
        "progress": "plan", "units": n, "reused": reused, "segments": layout.iter().map(Vec::len).sum::<usize>(),
        "duration": round6(frames as f64 / fps as f64), "fps": fps, "size": [e.canvas.w, e.canvas.h],
        "target": target,
    }));

    // Graphics runs the missing units need, for the fonts each painter loads and for the report.
    let mut need: BTreeMap<String, RunSpec> = BTreeMap::new();
    let mut have = BTreeSet::new();
    for p in plans.iter().filter(|p| !p.cached) {
        for r in &p.runs {
            if e.store.get_index("layer", &r.key).is_some() {
                have.insert(r.key.clone());
            } else {
                need.entry(r.key.clone()).or_insert_with(|| r.clone());
            }
        }
    }
    let faces = e.resources.faces(need.values().flat_map(|r| r.layers.iter()));
    let run_locks: Mutex<HashMap<String, Arc<Mutex<bool>>>> = Mutex::new(HashMap::new());
    let (drawn, gframes) = (AtomicUsize::new(0), AtomicUsize::new(0));

    let board = Mutex::new(Board { done: vec![false; n], error: None });
    let bell = Condvar::new();
    let audio: OnceLock<StreamAudio> = OnceLock::new();
    let stop = AtomicBool::new(false);
    let next = AtomicUsize::new(0);
    let fail = |err: anyhow::Error| {
        board.lock().unwrap().error.get_or_insert(err);
        stop.store(true, Ordering::SeqCst);
        bell.notify_all();
    };
    let t_units = Instant::now();
    let units_s: Mutex<f64> = Mutex::new(0.0);

    // One unit: the graphics runs it needs (each drawn once, whichever thread gets to it first), then the unit.
    let render_one = |p: &UnitPlan, painter: &mut Option<Painter>, js: &mut Option<Js>| -> Result<()> {
        if p.cached {
            return Ok(());
        }
        for r in &p.runs {
            let lock = run_locks.lock().unwrap().entry(r.key.clone()).or_default().clone();
            let mut ready = lock.lock().unwrap();
            if !*ready {
                if e.store.get_index("layer", &r.key).is_none() {
                    if painter.is_none() {
                        *painter = Some(Painter::new(e.resources.clone(), &faces).map_err(|x| anyhow!(x))?);
                    }
                    let painter = painter.as_mut().unwrap();
                    let st = unit::render_run(r, painter, &e.canvas, fps, &e.store).context("graphics run")?;
                    drawn.fetch_add(st.drawn, Ordering::Relaxed);
                    gframes.fetch_add(st.frames, Ordering::Relaxed);
                }
                *ready = true;
            }
        }
        if js.is_none() {
            *js = Some(Js::new().map_err(|x| anyhow!(x))?);
        }
        unit::render_unit(p, &e.store, js.as_mut().unwrap())?;
        Ok(())
    };

    let mut pl = Playlist::new(target);
    let mut first_s: Option<f64> = None;
    let published = std::thread::scope(|sc| -> Result<()> {
        sc.spawn(|| match stream_audio(e, &plans) {
            Ok(a) => {
                let _ = audio.set(a);
                bell.notify_all();
            }
            Err(err) => fail(err.context("the audio")),
        });
        for _ in 0..e.opts.jobs.clamp(1, n.max(1)) {
            sc.spawn(|| {
                let (mut painter, mut js) = (None, None);
                loop {
                    if stop.load(Ordering::SeqCst) {
                        return;
                    }
                    let i = next.fetch_add(1, Ordering::SeqCst);
                    let Some(p) = plans.get(i) else { return };
                    let t = Instant::now();
                    if let Err(err) = render_one(p, &mut painter, &mut js) {
                        fail(err.context(format!("unit {}", unit_name(p))));
                        return;
                    }
                    let delay = o.unit_delay.get(i).or(o.unit_delay.last()).copied().unwrap_or_default();
                    if let Some(rest) = delay.checked_sub(t.elapsed()) {
                        std::thread::sleep(rest);
                    }
                    let mut b = board.lock().unwrap();
                    b.done[i] = true;
                    if b.done.iter().all(|d| *d) {
                        *units_s.lock().unwrap() = t_units.elapsed().as_secs_f64();
                    }
                    drop(b);
                    bell.notify_all();
                }
            });
        }
        let res = (|| -> Result<()> {
            for i in 0..n {
                let mut b = board.lock().unwrap();
                loop {
                    if let Some(err) = b.error.take() {
                        return Err(err);
                    }
                    if b.done[i] && audio.get().is_some() {
                        break;
                    }
                    b = bell.wait(b).unwrap();
                }
                drop(b);
                let au = audio.get().unwrap();
                let segs = publish_unit(e, &plans, i, &layout[i], au, &dir, &stem, pl.segments.len())
                    .with_context(|| format!("publishing unit {}", unit_name(&plans[i])))?;
                pl.segments.extend(segs);
                pl.write(&o.playlist)?;
                let el = since(start);
                first_s.get_or_insert(el);
                (o.progress)(json!({
                    "progress": "publish", "done": i + 1, "total": n, "segments": pl.segments.len(),
                    "seconds": pl.seconds(), "elapsed": el,
                }));
            }
            Ok(())
        })();
        if res.is_err() {
            stop.store(true, Ordering::SeqCst);
        }
        res
    });
    published?;
    pl.ended = true;
    pl.write(&o.playlist)?;
    let done_s = since(start);
    (o.progress)(json!({"progress": "end", "segments": pl.segments.len(), "seconds": pl.seconds(), "elapsed": done_s}));
    timings.insert("first_segment".to_string(), first_s.unwrap_or(done_s));
    timings.insert("stream".to_string(), done_s);
    let missing: Vec<&UnitPlan> = plans.iter().filter(|p| !p.cached).collect();
    let px_frames: f64 =
        missing.iter().map(|p| p.span.frames() as f64).sum::<f64>() * e.canvas.w as f64 * e.canvas.h as f64 / 1e6;
    if px_frames > 0.0 && o.unit_delay.iter().all(Duration::is_zero) {
        let par = e.opts.jobs.clamp(1, missing.len()) as f64;
        pipeline::record_speed(e, *units_s.lock().unwrap() * 1000.0 * par / px_frames);
    }

    let lap = Instant::now();
    let au = audio.into_inner().context("no audio")?;
    let indexes = pipeline::unit_indexes(e, &plans)?;
    let mut manifest = pipeline::build_manifest(e, &plans, &indexes, &au.out.digests)?;
    let mut output = None;
    if let Some(mp4) = &o.mp4 {
        pipeline::package_mp4(e, &indexes, &au.out.aac, &mut manifest, mp4)?;
        output = Some(mp4.clone());
    }
    manifest.video = Some(o.playlist.file_name().unwrap_or_default().to_string_lossy().into_owned());
    let mp = o.playlist.with_extension("manifest.json");
    write_atomic(&mp, serde_json::to_string_pretty(&manifest)?.as_bytes())?;
    e.store.clean_tmp();
    timings.insert("package".to_string(), since(lap));
    Ok(RenderReport {
        output,
        manifest_path: Some(mp),
        units: n,
        reused,
        rendered: n - reused,
        layers_rendered: need.len(),
        layers_reused: have.len(),
        graphics_frames: gframes.into_inner(),
        graphics_drawn: drawn.into_inner(),
        audio_reused: au.out.reused,
        frames,
        duration: round6(frames as f64 / fps as f64),
        timings,
        stream: Some(StreamStats {
            playlist: o.playlist.clone(),
            segments: pl.segments.len(),
            first_s: first_s.unwrap_or(done_s),
            done_s,
        }),
        manifest,
    })
}

/// Cuts unit `i` into its segments in `dir` (named `seg_NNNNN.ts` from `seq` on) with its share of the audio, and
/// returns their names and durations.
#[allow(clippy::too_many_arguments)]
fn publish_unit(
    e: &Engine,
    plans: &[UnitPlan],
    i: usize,
    cuts: &[[i64; 2]],
    au: &StreamAudio,
    dir: &Path,
    stem: &str,
    seq: usize,
) -> Result<Vec<(String, f64)>> {
    let p = &plans[i];
    let idx = e.store.get_index("unit", &p.key).ok_or_else(|| anyhow!("unit {} wasn't stored", unit_name(p)))?;
    let video = e.store.blob_path(idx["blob"].as_str().context("the unit's segment")?);
    let (fps, sr) = (e.fps() as i64, au.out.sr as i64);
    let (f0, f1) = (p.span.f0, p.span.f1);
    // The same sample for the same frame in both neighbors, so the units' audio neither gaps nor overlaps.
    let s0 = f0 * sr / fps;
    let s1 = if i + 1 == plans.len() { i64::MAX } else { f1 * sr / fps };
    let (bytes, first) = au.adts.slice(s0, s1);
    let t0 = f0 as f64 / fps as f64;
    let tag = format!(".{stem}-{}-{i}", std::process::id());
    let slice = dir.join(format!("{tag}.aac"));
    let list = dir.join(format!("{tag}.csv"));
    let cleanup = || {
        let _ = std::fs::remove_file(&slice);
        let _ = std::fs::remove_file(&list);
    };
    let audio = match first {
        Some(s) => {
            std::fs::write(&slice, &bytes)?;
            Some((slice.as_path(), s as f64 / sr as f64 - t0))
        }
        None => None,
    };
    let rel: Vec<f64> = cuts.iter().skip(1).map(|c| c[0] as f64 / fps as f64).collect();
    let made = ffmpeg::hls_unit(&video, audio, TS_OFFSET + t0, &rel, &dir.join(format!("{tag}-%03d.ts")), &list);
    cleanup();
    let made = made?;
    // The planned durations are exact (whole frames); ffmpeg's list says where it really cut. They agree unless the
    // keyframes weren't where the encoder settings put them, and then ffmpeg's cuts are the truth.
    let planned: Vec<f64> = cuts.iter().map(|c| (c[1] - c[0]) as f64 / fps as f64).collect();
    let mut prev = TS_OFFSET + t0;
    let actual: Vec<f64> = made
        .iter()
        .map(|(_, end)| {
            let d = end - prev;
            prev = *end;
            d
        })
        .collect();
    let exact = planned.len() == actual.len() && planned.iter().zip(&actual).all(|(a, b)| (a - b).abs() < 0.002);
    let mut out = vec![];
    for (k, (file, _)) in made.iter().enumerate() {
        let name = format!("seg_{:05}.ts", seq + k);
        std::fs::rename(file, dir.join(&name)).with_context(|| format!("naming {name}"))?;
        out.push((name, if exact { planned[k] } else { actual[k] }));
    }
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn units_cut_at_keyframes_into_two_to_six_second_segments() {
        // 30 fps, a keyframe every 60 frames (2 s).
        assert_eq!(pieces(15, 60, 30), vec![[0, 15]], "a short cut is one short segment");
        assert_eq!(pieces(150, 60, 30), vec![[0, 150]], "5 s: one segment");
        assert_eq!(pieces(180, 60, 30), vec![[0, 180]], "6 s: still one");
        assert_eq!(pieces(181, 60, 30), vec![[0, 120], [120, 181]]);
        assert_eq!(pieces(390, 60, 30), vec![[0, 120], [120, 240], [240, 390]], "13 s: 4 + 4 + 5");
        // 60 fps, a keyframe every 120 frames.
        let p = pieces(60 * 61, 120, 60);
        assert!(p.iter().all(|x| x[0] % 120 == 0), "every cut on a keyframe");
        assert!(p.iter().all(|x| (x[1] - x[0]) <= 360 && (x[1] - x[0]) >= 120), "{p:?}");
        assert_eq!(p.first().unwrap()[0], 0);
        assert_eq!(p.last().unwrap()[1], 60 * 61);
        assert!(p.windows(2).all(|w| w[0][1] == w[1][0]), "contiguous");
    }

    #[test]
    fn the_playlist_grows_at_its_end_and_ends() {
        let mut pl = Playlist::new(6);
        let empty = pl.text();
        assert!(empty.starts_with("#EXTM3U\n"));
        assert!(empty.contains("#EXT-X-PLAYLIST-TYPE:EVENT\n") && empty.contains("#EXT-X-TARGETDURATION:6\n"));
        assert!(empty.contains("#EXT-X-START:TIME-OFFSET=0\n") && !empty.contains("ENDLIST"));
        pl.segments.push(("seg_00000.ts".into(), 4.0));
        let one = pl.text();
        assert!(one.starts_with(&empty), "only grows at its end");
        pl.segments.push(("seg_00001.ts".into(), 2.533333));
        let two = pl.text();
        assert!(two.starts_with(&one));
        assert!(two.ends_with("#EXTINF:2.533333,\nseg_00001.ts\n"));
        assert_eq!(pl.seconds(), 6.533333);
        pl.ended = true;
        assert!(pl.text().starts_with(&two) && pl.text().ends_with("#EXT-X-ENDLIST\n"));
    }

    #[test]
    fn playlists_are_replaced_whole() {
        let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../target/tmp/stream-atomic");
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("x_v2.m3u8");
        // A reader opened on the old file keeps reading the old bytes; a new open sees the whole new file.
        write_atomic(&path, b"old playlist").unwrap();
        let mut held = std::fs::File::open(&path).unwrap();
        write_atomic(&path, b"the new, longer playlist").unwrap();
        let mut s = String::new();
        std::io::Read::read_to_string(&mut held, &mut s).unwrap();
        assert_eq!(s, "old playlist");
        assert_eq!(std::fs::read_to_string(&path).unwrap(), "the new, longer playlist");
        let left: Vec<_> = std::fs::read_dir(&dir).unwrap().flatten().map(|e| e.file_name()).collect();
        assert_eq!(left, vec![std::ffi::OsString::from("x_v2.m3u8")], "no temporary file left over");
    }

    #[test]
    fn adts_frames_go_to_the_unit_they_start_in() {
        // Three-byte payloads after a 7-byte header; one raw data block (1024 samples) per frame.
        let frame = |tag: u8| {
            let len = 10usize;
            vec![
                0xFF,
                0xF1,
                0x50,
                0x80 | ((len >> 11) as u8 & 3),
                (len >> 3) as u8,
                ((len & 7) << 5) as u8 | 0x1F,
                0xFC,
                tag,
                tag,
                tag,
            ]
        };
        let bytes: Vec<u8> = (0..6u8).flat_map(frame).collect();
        let a = Adts::parse(bytes).unwrap();
        assert_eq!(a.frames.iter().map(|f| f.2).collect::<Vec<_>>(), vec![-1024, 0, 1024, 2048, 3072, 4096]);
        let (b, first) = a.slice(0, 1500);
        assert_eq!(first, Some(0), "the priming frame is never handed out");
        assert_eq!(b.len(), 20);
        assert_eq!(b[7], 1);
        let (b2, first2) = a.slice(1500, 4096);
        assert_eq!(first2, Some(2048));
        assert_eq!(b2.len(), 20);
        let (_, none) = a.slice(5000, 6000);
        assert_eq!(none, None);
        assert!(Adts::parse(vec![0xFF, 0xF1, 0, 0]).is_err());
    }
}
