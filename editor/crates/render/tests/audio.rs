//! The audio mix on generated media: stream selection, the unity mono upmix, fade curves, ducking under spans, gain
//! keys, loudness targets and the master bus (checked independently with ffmpeg's EBU R128 meter).

use retake_render::audio::{self, AudioPlan};
use retake_render::engine::{Engine, Options};
use serde_json::{Value, json};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::OnceLock;

const SR: usize = 48000;

fn root() -> PathBuf {
    PathBuf::from(env!("CARGO_TARGET_TMPDIR")).join("audio")
}

fn ffmpeg(args: &[&str]) {
    let st = Command::new("ffmpeg").args(["-nostdin", "-v", "error", "-y"]).args(args).status().expect("ffmpeg runs");
    assert!(st.success(), "ffmpeg {args:?}");
}

/// Generated media, made once: silence for the VO, a two-track file (440 Hz stereo, then 1 kHz mono), a clip with a
/// picture and the same two tracks, pink noise, a silent insert clip.
fn media() -> &'static Path {
    static M: OnceLock<PathBuf> = OnceLock::new();
    M.get_or_init(|| {
        let d = root().join("media");
        std::fs::create_dir_all(&d).unwrap();
        let p = |n: &str| d.join(n).to_string_lossy().into_owned();
        ffmpeg(&["-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono", "-t", "8", &p("vo.wav")]);
        let (a440, a1k) = ("sine=f=440:r=48000:d=8,pan=stereo|c0=c0|c1=c0", "sine=f=1000:r=48000:d=8");
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            a440,
            "-f",
            "lavfi",
            "-i",
            a1k,
            "-map",
            "0:a",
            "-map",
            "1:a",
            "-c:a",
            "pcm_s16le",
            &p("tracks.mkv"),
        ]);
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            "testsrc2=size=64x36:rate=10",
            "-f",
            "lavfi",
            "-i",
            a440,
            "-f",
            "lavfi",
            "-i",
            a1k,
            "-t",
            "8",
            "-map",
            "0:v",
            "-map",
            "1:a",
            "-map",
            "2:a",
            "-pix_fmt",
            "yuv420p",
            "-c:a",
            "pcm_s16le",
            &p("clip.mkv"),
        ]);
        ffmpeg(&[
            "-f",
            "lavfi",
            "-i",
            "anoisesrc=r=48000:c=pink:a=0.3:seed=7",
            "-t",
            "10",
            "-ac",
            "2",
            &p("noise.wav"),
        ]);
        ffmpeg(&["-f", "lavfi", "-i", "testsrc=size=64x36:rate=10", "-t", "3", "-pix_fmt", "yuv420p", &p("ins.mp4")]);
        d
    })
}

fn timeline(audio: Value) -> Value {
    let m = media().to_string_lossy().into_owned();
    json!({
        "schema": "timeline/0.1", "id": "aud", "rev": 0,
        "format": {"size": [160, 90], "fps": 10},
        "assets": {
            "proj:vo": {"path": format!("{m}/vo.wav")},
            "proj:tracks": {"path": format!("{m}/tracks.mkv")},
            "proj:clip": {"path": format!("{m}/clip.mkv")},
            "proj:noise": {"path": format!("{m}/noise.wav")},
            "proj:ins": {"path": format!("{m}/ins.mp4")}
        },
        "spine": [
            {"id": "a", "asset": "proj:vo", "in": 0, "out": 3},
            {"id": "b", "asset": "proj:vo", "in": 3, "out": 6}
        ],
        "scenes": [{"id": "s", "shots": [{"id": "s/1", "at": "start:a", "picture": {"layers": [
            {"id": "s/1/v", "type": "video", "asset": "proj:clip"}]}}]}],
        "audio": audio
    })
}

fn engine(name: &str, doc: &Value, opts: Options) -> anyhow::Result<Engine> {
    let d = root().join("tl").join(name);
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    Engine::open(&path, Options { cache: Some(root().join("cache")), ..opts })
}

fn mixed(e: &Engine) -> (Vec<f32>, AudioPlan) {
    let plans = e.plan().unwrap();
    let mut ap = e.audio_plan(&plans);
    e.audio_levels(&mut ap, true).unwrap();
    (audio::mix(&ap).unwrap().pcm, ap)
}

fn mix_of(name: &str, doc: &Value) -> Vec<f32> {
    mixed(&engine(name, doc, Options::default()).unwrap()).0
}

/// RMS of channel `ch` over seconds [a, b).
fn rms(x: &[f32], ch: usize, a: f64, b: f64) -> f64 {
    let (i0, i1) = ((a * SR as f64) as usize, (b * SR as f64) as usize);
    let s: f64 = (i0..i1).map(|i| (x[2 * i + ch] as f64).powi(2)).sum();
    (s / (i1 - i0) as f64).sqrt()
}

/// Power at frequency `f` of channel 0 over seconds [a, b) (Goertzel).
fn power(x: &[f32], f: f64, a: f64, b: f64) -> f64 {
    let (i0, i1) = ((a * SR as f64) as usize, (b * SR as f64) as usize);
    let w = 2.0 * std::f64::consts::PI * f / SR as f64;
    let (mut s1, mut s2) = (0.0, 0.0);
    for i in i0..i1 {
        let s = x[2 * i] as f64 + 2.0 * w.cos() * s1 - s2;
        s2 = s1;
        s1 = s;
    }
    (s1 * s1 + s2 * s2 - 2.0 * w.cos() * s1 * s2) / ((i1 - i0) as f64).powi(2)
}

fn db(x: f64) -> f64 {
    20.0 * x.log10()
}

/// ffmpeg's own EBU R128 reading of interleaved stereo f32: (integrated LUFS, true peak dBTP).
fn ffmpeg_r128(x: &[f32], name: &str) -> (f64, f64) {
    let p = root().join(format!("{name}.f32"));
    let bytes: Vec<u8> = x.iter().flat_map(|v| v.to_le_bytes()).collect();
    std::fs::write(&p, bytes).unwrap();
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-nostats", "-f", "f32le", "-ar", "48000", "-ac", "2", "-i"])
        .arg(&p)
        .args(["-af", "ebur128=peak=true", "-f", "null", "-"])
        .output()
        .unwrap();
    let err = String::from_utf8_lossy(&out.stderr);
    let summary = err.rsplit("Summary:").next().unwrap();
    let num = |key: &str| -> f64 {
        let s = summary.split(key).nth(1).unwrap().trim_start();
        s.split_whitespace().next().unwrap().parse().unwrap()
    };
    (num("I:"), num("Peak:"))
}

#[test]
fn stream_picks_the_track() {
    let first = mix_of("s0", &timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a", "to": "end:b"}])));
    assert!(power(&first, 440.0, 1.0, 2.0) > 100.0 * power(&first, 1000.0, 1.0, 2.0), "default: the first stream");
    let second = mix_of(
        "s1",
        &timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a", "to": "end:b", "stream": 1}])),
    );
    assert!(power(&second, 1000.0, 1.0, 2.0) > 100.0 * power(&second, 440.0, 1.0, 2.0), "stream 1: the 1 kHz track");
    // A video layer's sound picks its stream the same way.
    let mut d = timeline(json!([]));
    d["scenes"][0]["shots"][0]["picture"]["layers"][0]["audio"] = json!({"stream": 1});
    let layer = mix_of("s1-layer", &d);
    assert!(power(&layer, 1000.0, 1.0, 2.0) > 100.0 * power(&layer, 440.0, 1.0, 2.0));
    // A stream the file doesn't have.
    let bad = timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a", "stream": 2}]));
    let err = engine("s2", &bad, Options::default()).err().expect("stream 2 doesn't exist");
    let msg = format!("{err:#}");
    assert!(msg.contains("audio_stream") || msg.contains("no audio stream 2"), "{msg}");
    assert!(msg.contains("0: 2 ch") && msg.contains("1: 1 ch"), "lists the streams: {msg}");
}

#[test]
fn mono_plays_at_unity_in_both_channels() {
    // lavfi's sine is 1/8 full scale: RMS 0.125/√2. ffmpeg's own upmix would be 3 dB lower.
    let x = mix_of(
        "mono",
        &timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a", "to": "end:b", "stream": 1}])),
    );
    let want = 0.125 / 2f64.sqrt();
    for ch in 0..2 {
        let r = rms(&x, ch, 1.0, 2.0);
        assert!((db(r) - db(want)).abs() < 0.05, "channel {ch}: {:.3} dB vs {:.3} dB", db(r), db(want));
    }
}

#[test]
fn fade_curves_shape_the_fades() {
    let level_at = |curve: &str, t: f64| {
        let x = mix_of(
            &format!("fade-{curve}"),
            &timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a", "to": "end:b",
                "fade_in": 2.0, "fade_curve": curve}])),
        );
        rms(&x, 0, t - 0.05, t + 0.05) / rms(&x, 0, 3.0, 4.0)
    };
    assert!((level_at("linear", 1.0) - 0.5).abs() < 0.03);
    assert!((level_at("equal-power", 1.0) - std::f64::consts::FRAC_1_SQRT_2).abs() < 0.03);
    assert!((level_at("s-curve", 1.0) - 0.5).abs() < 0.03);
    assert!((db(level_at("log", 1.0)) + 30.0).abs() < 1.0, "log: -30 dB halfway from -60");
    assert!(level_at("s-curve", 0.4) < level_at("linear", 0.4), "an s-curve starts slower");
}

#[test]
fn ducking_under_inserts_and_spine_items() {
    let mut d = timeline(json!([{"id": "bed", "asset": "proj:tracks", "from": "start:a", "to": "end:b",
        "duck": {"db": -96, "under": "inserts", "attack": 0.4, "release": 0.4}}]));
    // a [0, 2), the insert [2, 3), b [3, 6).
    d["spine"] = json!([
        {"id": "a", "asset": "proj:vo", "in": 0, "out": 2},
        {"id": "ins", "kind": "insert", "asset": "proj:ins", "in": 0, "dur": 1.0},
        {"id": "b", "asset": "proj:vo", "in": 2, "out": 5}
    ]);
    let x = mix_of("duck-ins", &d);
    let full = rms(&x, 0, 0.5, 1.4);
    assert!((rms(&x, 0, 1.75, 1.85) / full - 0.5).abs() < 0.06, "halfway down the attack");
    assert!(rms(&x, 0, 2.02, 2.98) < full * 1e-3, "out for the whole insert");
    assert!((rms(&x, 0, 3.15, 3.25) / full - 0.5).abs() < 0.06, "halfway back up");
    assert!((rms(&x, 0, 3.5, 4.5) / full - 1.0).abs() < 0.01, "back after the release");
    // Under a spine item by id, partially, as a second rule (rules multiply).
    let mut d2 = d.clone();
    d2["audio"][0]["duck"] = json!([{"db": -96, "under": "inserts", "attack": 0, "release": 0},
                                    {"db": -12, "under": ["b"], "attack": 0, "release": 0}]);
    let y = mix_of("duck-ids", &d2);
    let full = rms(&y, 0, 0.5, 1.5);
    assert!((db(rms(&y, 0, 3.5, 4.5) / full) + 12.0).abs() < 0.1, "-12 dB under b");
    // An unknown id is a resolve error.
    let mut d3 = d.clone();
    d3["audio"][0]["duck"] = json!({"db": -6, "under": ["bb"]});
    let err = engine("duck-bad", &d3, Options::default()).err().unwrap();
    assert!(format!("{err:#}").contains("duck under `bb`"), "{err:#}");
}

#[test]
fn gain_keys_and_expressions_on_the_elements_clock() {
    // Keys from -20 dB at `from` to 0 dB at the anchor start:b (3 s later), then held.
    let x = mix_of(
        "gain-keys",
        &timeline(json!([{"id": "t", "asset": "proj:tracks", "from": "start:a+1.00", "to": "end:b",
            "gain_db": {"keys": [{"t": 0, "v": -20}, {"t": "start:b", "v": 0}]}}])),
    );
    let unity = rms(&x, 0, 4.5, 5.5);
    assert!((db(rms(&x, 0, 1.02, 1.08) / unity) + 19.5).abs() < 0.3, "starts near -20 dB");
    assert!((db(rms(&x, 0, 1.95, 2.05) / unity) + 10.0).abs() < 0.3, "halfway (1 s of 2): -10 dB");
    // An expression, on a video layer's sound: composition seconds.
    let mut d = timeline(json!([]));
    d["scenes"][0]["shots"][0]["picture"]["layers"][0]["audio"] = json!({"gain_db": {"expr": "t < 2 ? -6 : 0"}});
    let y = mix_of("gain-expr", &d);
    assert!((db(rms(&y, 0, 1.0, 1.9) / rms(&y, 0, 3.0, 4.0)) + 6.0).abs() < 0.05);
}

#[test]
fn loudness_targets_and_the_master_bus() {
    // A bed at -30 LUFS, the noise at -20: each element measured and levelled on its own.
    let doc = timeline(json!([
        {"id": "tone", "asset": "proj:tracks", "from": "start:a", "to": "end:b", "loudness": -30},
        {"id": "noise", "asset": "proj:noise", "from": "start:a", "to": "end:b", "loudness": -20, "gain_db": -3}
    ]));
    let e = engine("loud", &doc, Options::default()).unwrap();
    let (_, ap) = mixed(&e);
    for c in &ap.clips {
        if let Some(l) = &c.loudness {
            let mut one = ap.clone();
            one.clips = vec![c.clone()];
            let lufs = retake_render::dsp::integrated_lufs(&audio::mix_raw(&one).unwrap(), 48000);
            let want = l.target + c.gain_db;
            assert!((lufs - want).abs() < 0.5, "{}: {lufs:.2} LUFS, want {want}", c.owner);
        }
    }
    // The master: -10 LUFS integrated (loud enough that the limiter works), -1 dBTP, a 16.5 kHz low-pass; read back by ffmpeg's EBU R128 meter.
    let mut m = doc.clone();
    m["audio"][1]["gain_db"] = json!(12);
    m["master"] = json!({"loudness": -10, "true_peak": -1, "lowpass": 16500});
    // Two shots, so a range can cover one unit only.
    m["scenes"][0]["shots"].as_array_mut().unwrap().push(json!({"id": "s/2", "at": "start:b", "picture": {"layers": [
        {"id": "s/2/v", "type": "video", "asset": "proj:clip", "in": 3.0}]}}));
    let e = engine("master", &m, Options::default()).unwrap();
    let (x, ap) = mixed(&e);
    let (i, tp) = ffmpeg_r128(&x, "master");
    eprintln!("master, read by ffmpeg: {i} LUFS, {tp} dBTP");
    assert!((i + 10.0).abs() < 0.5, "integrated {i} LUFS");
    assert!(tp <= -1.0 + 0.1, "true peak {tp} dBTP");
    let gain = ap.master.as_ref().and_then(|m| m.gain_db).expect("measured");
    // A range render uses the whole mix's gain.
    let r = engine("master", &m, Options { range: Some((4.0, 5.0)), ..Options::default() }).unwrap();
    let (_, rp) = mixed(&r);
    assert!(rp.start > 0 && rp.len < ap.len);
    assert_eq!(rp.master.as_ref().and_then(|m| m.gain_db), Some(gain));
}

#[test]
fn a_render_with_a_master_caches_its_audio() {
    let mut doc = timeline(json!([{"id": "noise", "asset": "proj:noise", "from": "start:a", "to": "end:b",
        "loudness": -24, "duck": {"db": -6}}]));
    doc["master"] = json!({"loudness": -18, "true_peak": -2});
    let d = root().join("tl").join("render");
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let cache = root().join("cache-render");
    let _ = std::fs::remove_dir_all(&cache);
    let opts = Options { cache: Some(cache), jobs: 2, ..Options::default() };
    let e = Engine::open(&path, opts.clone()).unwrap();
    let (_, before) = retake_render::plan(&e).unwrap();
    assert!(!before.audio_cached);
    let out = d.join("out.mp4");
    let rep = retake_render::render(&e, Some(&out)).unwrap();
    assert!(!rep.audio_reused);
    let e = Engine::open(&path, opts).unwrap();
    let (_, after) = retake_render::plan(&e).unwrap();
    assert!(after.audio_cached, "levels and the mix come from the cache");
    // The packaged AAC is near the target too.
    let o = Command::new("ffmpeg")
        .args(["-nostdin", "-nostats", "-i"])
        .arg(&out)
        .args(["-af", "ebur128", "-f", "null", "-"])
        .output()
        .unwrap();
    let err = String::from_utf8_lossy(&o.stderr);
    let i: f64 = err.rsplit("I:").next().unwrap().split_whitespace().next().unwrap().parse().unwrap();
    assert!((i + 18.0).abs() < 0.7, "{i}");
}

/// ffmpeg's EBU R128 reading of a file's audio: (integrated LUFS, true peak dBTP).
fn file_r128(path: &Path) -> (f64, f64) {
    let out = Command::new("ffmpeg")
        .args(["-nostdin", "-nostats", "-i"])
        .arg(path)
        .args(["-map", "0:a:0", "-af", "ebur128=peak=true", "-f", "null", "-"])
        .output()
        .unwrap();
    let err = String::from_utf8_lossy(&out.stderr);
    let summary = err.rsplit("Summary:").next().unwrap();
    let num = |key: &str| -> f64 {
        let s = summary.split(key).nth(1).unwrap().trim_start();
        s.split_whitespace().next().unwrap().parse().unwrap()
    };
    (num("I:"), num("Peak:"))
}

#[test]
fn an_export_is_levelled_unless_the_master_sets_a_loudness() {
    // A quiet bed and no master: what a review render plays is about -33 LUFS.
    let mut doc = timeline(json!([{"id": "noise", "asset": "proj:noise", "from": "start:a", "to": "end:b",
        "gain_db": -20}]));
    let d = root().join("tl").join("export");
    std::fs::create_dir_all(&d).unwrap();
    let path = d.join("timeline.json");
    std::fs::write(&path, doc.to_string()).unwrap();
    let cache = root().join("cache-export");
    let _ = std::fs::remove_dir_all(&cache);
    let opts = Options { cache: Some(cache), jobs: 2, ..Options::default() };
    let (plain, _) = mixed(&Engine::open(&path, opts.clone()).unwrap());
    let (i, _) = ffmpeg_r128(&plain, "export-plain");
    assert!(i < -25.0, "the review mix isn't levelled: {i} LUFS");

    // The export (-o with the flag): -14 LUFS, true peak at or below -1 dBTP after the AAC encode.
    let ex = Options { export_loudness: Some(-14.0), ..opts.clone() };
    let out = d.join("export.mp4");
    retake_render::render(&Engine::open(&path, ex.clone()).unwrap(), Some(&out)).unwrap();
    let (i, tp) = file_r128(&out);
    eprintln!("export, read by ffmpeg: {i} LUFS, {tp} dBTP");
    assert!((i + 14.0).abs() < 1.0, "export at {i} LUFS");
    assert!(tp <= -1.0, "export true peak {tp} dBTP");
    // Tagged BT.709 for YouTube: primaries, transfer and matrix.
    let o = Command::new("ffprobe")
        .args(["-v", "error", "-select_streams", "v:0", "-show_entries"])
        .arg("stream=profile,pix_fmt,color_primaries,color_transfer,color_space,color_range")
        .args(["-of", "csv=p=0"])
        .arg(&out)
        .output()
        .unwrap();
    assert_eq!(String::from_utf8_lossy(&o.stdout).trim(), "High,yuv420p,tv,bt709,bt709,bt709");

    // Streamed with an export: the playlist keeps the review mix, only the MP4 is levelled (its own encode).
    let playlist = d.join("hls").join("clip.m3u8");
    std::fs::create_dir_all(playlist.parent().unwrap()).unwrap();
    let mp4 = d.join("hls").join("clip.export.mp4");
    let quiet = |_: Value| {};
    let so = retake_render::stream::StreamOptions {
        playlist: playlist.clone(),
        mp4: Some(mp4.clone()),
        unit_delay: vec![],
        progress: &quiet,
    };
    retake_render::stream::render_stream(&Engine::open(&path, ex.clone()).unwrap(), &so).unwrap();
    let (i, _) = file_r128(&mp4);
    assert!((i + 14.0).abs() < 1.0, "streamed export at {i} LUFS");
    let hashes = |p: &Path| -> Vec<String> {
        let m: Value = serde_json::from_str(&std::fs::read_to_string(p).unwrap()).unwrap();
        m["units"].as_array().unwrap().iter().map(|u| u["audio_hash"].as_str().unwrap().to_string()).collect()
    };
    let (review, export) =
        (hashes(&playlist.with_extension("manifest.json")), hashes(&mp4.with_extension("manifest.json")));
    assert_ne!(review, export, "the export has its own mix");

    // A master loudness of the timeline's own wins over the flag.
    doc["master"] = json!({"loudness": -18});
    std::fs::write(&path, doc.to_string()).unwrap();
    let out = d.join("export-master.mp4");
    retake_render::render(&Engine::open(&path, ex).unwrap(), Some(&out)).unwrap();
    let (i, _) = file_r128(&out);
    assert!((i + 18.0).abs() < 1.0, "the master's own -18, not {i} LUFS");
}

#[test]
fn export_master_keeps_the_timelines_own() {
    let mut ap = mixed(&engine("em", &timeline(json!([])), Options::default()).unwrap()).1;
    assert!(audio::export_master(&mut ap, -14.0));
    let m = ap.master.clone().unwrap();
    assert_eq!((m.loudness, m.true_peak), (Some(-14.0), Some(audio::EXPORT_TRUE_PEAK)));
    ap.master =
        Some(audio::MasterPlan { loudness: None, gain_db: None, lowpass: Some(16500.0), true_peak: Some(-2.0) });
    assert!(audio::export_master(&mut ap, -14.0));
    let m = ap.master.clone().unwrap();
    assert_eq!((m.loudness, m.true_peak, m.lowpass), (Some(-14.0), Some(-2.0), Some(16500.0)));
    ap.master = Some(audio::MasterPlan { loudness: Some(-18.0), gain_db: None, lowpass: None, true_peak: None });
    assert!(!audio::export_master(&mut ap, -14.0));
    assert_eq!(ap.master.unwrap().loudness, Some(-18.0));
}
