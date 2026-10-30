//! ffmpeg as subprocesses: decode and encode through pipes, never linked (licensing and portability). `RETAKE_FFMPEG`
//! and `RETAKE_FFPROBE` override the binaries.

use anyhow::{Context, Result, anyhow, bail};
use serde::{Deserialize, Serialize};
use std::io::{Read, Write};
use std::path::Path;
use std::process::{Child, ChildStdin, ChildStdout, Command, Stdio};
use std::sync::OnceLock;

pub fn ffmpeg_bin() -> String {
    std::env::var("RETAKE_FFMPEG").unwrap_or_else(|_| "ffmpeg".into())
}

pub fn ffprobe_bin() -> String {
    std::env::var("RETAKE_FFPROBE").unwrap_or_else(|_| "ffprobe".into())
}

/// `ffmpeg -version`'s first line. Part of every encode key: another build may encode other bytes.
pub fn version() -> &'static str {
    static V: OnceLock<String> = OnceLock::new();
    V.get_or_init(|| {
        Command::new(ffmpeg_bin())
            .arg("-version")
            .output()
            .ok()
            .and_then(|o| String::from_utf8(o.stdout).ok())
            .and_then(|s| s.lines().next().map(|l| l.split(" Copyright").next().unwrap_or(l).trim().to_string()))
            .unwrap_or_else(|| "ffmpeg (unknown)".into())
    })
}

fn run(cmd: &mut Command, what: &str) -> Result<Vec<u8>> {
    let out = cmd.stdin(Stdio::null()).output().with_context(|| format!("running ffmpeg for {what}"))?;
    if !out.status.success() {
        let err = String::from_utf8_lossy(&out.stderr);
        bail!("ffmpeg failed ({what}): {}", err.lines().rev().take(6).collect::<Vec<_>>().join(" | "));
    }
    Ok(out.stdout)
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct MediaInfo {
    pub duration: f64,
    /// Width and height, if the file has a video stream (or is an image).
    pub size: Option<[u32; 2]>,
    pub fps: Option<f64>,
    pub audio: bool,
}

pub fn probe(path: &Path) -> Result<MediaInfo> {
    let out = run(
        Command::new(ffprobe_bin())
            .args(["-v", "error", "-print_format", "json", "-show_format", "-show_streams"])
            .arg(path),
        &format!("probing {}", path.display()),
    )?;
    let v: serde_json::Value = serde_json::from_slice(&out)?;
    let streams = v["streams"].as_array().cloned().unwrap_or_default();
    let video = streams.iter().find(|s| s["codec_type"] == "video");
    let audio = streams.iter().any(|s| s["codec_type"] == "audio");
    let duration = v["format"]["duration"].as_str().and_then(|d| d.parse().ok()).unwrap_or(0.0);
    let size = video.and_then(|s| Some([s["width"].as_u64()? as u32, s["height"].as_u64()? as u32]));
    let fps = video.and_then(|s| {
        let r = s["avg_frame_rate"].as_str().or(s["r_frame_rate"].as_str())?;
        let (n, d) = r.split_once('/')?;
        let (n, d): (f64, f64) = (n.parse().ok()?, d.parse().ok()?);
        if d > 0.0 && n > 0.0 { Some(n / d) } else { None }
    });
    Ok(MediaInfo { duration, size, fps, audio })
}

/// How a video is decoded: the size, an optional crop applied in the pipe (source pixels: x, y, w, h), the scaler,
/// and boxes `delogo` fills in first.
#[derive(Debug, Clone, PartialEq)]
pub struct Decode {
    pub w: u32,
    pub h: u32,
    pub crop: Option<[u32; 4]>,
    pub lanczos: bool,
    pub delogo: Vec<[u32; 4]>,
}

impl Decode {
    pub fn scaled(w: u32, h: u32) -> Decode {
        Decode { w, h, crop: None, lanczos: false, delogo: vec![] }
    }

    /// The ffmpeg filter chain.
    pub fn filters(&self, fps: u32) -> String {
        let mut f = vec![format!("fps={fps}")];
        for b in &self.delogo {
            f.push(format!("delogo=x={}:y={}:w={}:h={}", b[0], b[1], b[2], b[3]));
        }
        if let Some(c) = self.crop {
            f.push(format!("crop={}:{}:{}:{}", c[2], c[3], c[0], c[1]));
        }
        let flags = if self.lanczos { "lanczos" } else { "bicubic+accurate_rnd+full_chroma_int" };
        f.push(format!("scale={}:{}:flags={flags}", self.w, self.h));
        f.push("format=rgba".into());
        f.join(",")
    }
}

/// Sequential RGBA frames from a video, at the output fps, starting at `start` seconds. After the end of the file it
/// repeats the last frame.
pub struct VideoReader {
    child: Child,
    out: ChildStdout,
    pub w: u32,
    pub h: u32,
    frame: Vec<u8>,
    have: bool,
    eof: bool,
}

impl VideoReader {
    pub fn open(path: &Path, start: f64, fps: u32, w: u32, h: u32, looping: bool) -> Result<VideoReader> {
        VideoReader::open_with(path, start, fps, &Decode::scaled(w, h), looping)
    }

    pub fn open_with(path: &Path, start: f64, fps: u32, d: &Decode, looping: bool) -> Result<VideoReader> {
        let (w, h) = (d.w, d.h);
        let mut cmd = Command::new(ffmpeg_bin());
        cmd.args(["-nostdin", "-v", "error", "-threads", "2"]);
        if looping {
            cmd.args(["-stream_loop", "-1"]);
        }
        cmd.args(["-ss", &format!("{:.6}", start.max(0.0))]).arg("-i").arg(path).args([
            "-an",
            "-sn",
            "-dn",
            "-vf",
            &d.filters(fps),
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgba",
            "-",
        ]);
        let mut child = cmd
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .stdin(Stdio::null())
            .spawn()
            .with_context(|| format!("starting ffmpeg to read {}", path.display()))?;
        let out = child.stdout.take().ok_or_else(|| anyhow!("no ffmpeg stdout"))?;
        Ok(VideoReader { child, out, w, h, frame: vec![0; (w * h * 4) as usize], have: false, eof: false })
    }

    /// The last frame read (or black before any).
    pub fn current(&self) -> &[u8] {
        &self.frame
    }

    /// The next frame (RGBA, opaque).
    pub fn next_frame(&mut self) -> Result<&[u8]> {
        if !self.eof {
            let mut buf = vec![0u8; self.frame.len()];
            let mut got = 0;
            while got < buf.len() {
                let n = self.out.read(&mut buf[got..])?;
                if n == 0 {
                    break;
                }
                got += n;
            }
            if got == buf.len() {
                self.frame = buf;
                self.have = true;
            } else {
                self.eof = true;
                if !self.have {
                    // Nothing decoded at all (start past the end): opaque black.
                    for px in self.frame.as_chunks_mut::<4>().0 {
                        px.copy_from_slice(&[0, 0, 0, 255]);
                    }
                }
            }
        }
        Ok(&self.frame)
    }
}

impl Drop for VideoReader {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// Encoder settings: everything that can change the encoded bytes. Hashed into every unit key.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EncodeSettings {
    pub codec: String,
    pub preset: String,
    pub crf: u32,
    pub pix_fmt: String,
    pub gop: u32,
    pub threads: u32,
    pub color: String,
    pub ffmpeg: String,
}

impl EncodeSettings {
    pub fn new(draft: bool, fps: u32) -> EncodeSettings {
        EncodeSettings {
            codec: "libx264".into(),
            preset: if draft { "veryfast" } else { "medium" }.into(),
            crf: if draft { 23 } else { 18 },
            pix_fmt: "yuv420p".into(),
            gop: fps * 2,
            threads: 4,
            color: "bt709-tv".into(),
            ffmpeg: version().into(),
        }
    }
}

/// Encodes raw RGBA frames into one closed-GOP H.264 segment that starts on an IDR frame.
pub struct Encoder {
    child: Child,
    stdin: Option<ChildStdin>,
    frame_len: usize,
}

impl Encoder {
    pub fn open(out: &Path, w: u32, h: u32, fps: u32, frames: i64, s: &EncodeSettings) -> Result<Encoder> {
        let mut cmd = Command::new(ffmpeg_bin());
        cmd.args(["-nostdin", "-v", "error", "-y", "-f", "rawvideo", "-pix_fmt", "rgba"])
            .args(["-s", &format!("{w}x{h}"), "-r", &fps.to_string(), "-i", "-"])
            .args(["-frames:v", &frames.to_string(), "-an"])
            .args(["-vf", "scale=out_color_matrix=bt709:out_range=tv:flags=bicubic+accurate_rnd+full_chroma_int"])
            .args(["-c:v", &s.codec, "-preset", &s.preset, "-crf", &s.crf.to_string(), "-pix_fmt", &s.pix_fmt])
            .args(["-g", &s.gop.to_string(), "-bf", "2"])
            .args(["-x264-params", &format!("threads={}:open-gop=0:scenecut=0", s.threads)])
            .args(["-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv"])
            .args(["-fflags", "+bitexact", "-flags:v", "+bitexact", "-map_metadata", "-1", "-f", "mp4"])
            .arg(out);
        let mut child = cmd
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .context("starting the ffmpeg encoder")?;
        let stdin = child.stdin.take();
        Ok(Encoder { child, stdin, frame_len: (w * h * 4) as usize })
    }

    pub fn write(&mut self, rgba: &[u8]) -> Result<()> {
        debug_assert_eq!(rgba.len(), self.frame_len);
        self.stdin.as_mut().ok_or_else(|| anyhow!("encoder closed"))?.write_all(rgba).context("feeding the encoder")
    }

    pub fn finish(mut self) -> Result<()> {
        drop(self.stdin.take());
        let mut err = String::new();
        if let Some(mut e) = self.child.stderr.take() {
            let _ = e.read_to_string(&mut err);
        }
        let st = self.child.wait()?;
        if !st.success() {
            bail!("ffmpeg encoder failed: {}", err.trim());
        }
        Ok(())
    }
}

/// Decodes `dur` seconds of audio from `start`, as interleaved stereo f32 at `sr`. Empty if the file has no audio.
pub fn decode_audio(path: &Path, start: f64, dur: f64, sr: u32, looping: bool) -> Result<Vec<f32>> {
    let mut cmd = Command::new(ffmpeg_bin());
    cmd.args(["-nostdin", "-v", "error"]);
    if looping {
        cmd.args(["-stream_loop", "-1"]);
    }
    cmd.args(["-ss", &format!("{:.6}", start.max(0.0)), "-t", &format!("{:.6}", dur.max(0.0))])
        .arg("-i")
        .arg(path)
        .args(["-vn", "-ac", "2", "-ar", &sr.to_string(), "-f", "f32le", "-"]);
    let bytes = run(&mut cmd, &format!("decoding audio from {}", path.display()))?;
    Ok(bytes.as_chunks::<4>().0.iter().map(|b| f32::from_le_bytes(*b)).collect())
}

/// Encodes interleaved stereo f32 to AAC in an .m4a.
pub fn encode_aac(pcm: &[f32], sr: u32, out: &Path) -> Result<()> {
    let mut child = Command::new(ffmpeg_bin())
        .args(["-nostdin", "-v", "error", "-y", "-f", "f32le", "-ar", &sr.to_string(), "-ac", "2", "-i", "-"])
        .args(["-c:a", "aac", "-b:a", "192k", "-fflags", "+bitexact", "-flags:a", "+bitexact", "-map_metadata", "-1"])
        .arg(out)
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .context("starting the AAC encoder")?;
    {
        let mut stdin = child.stdin.take().ok_or_else(|| anyhow!("no stdin"))?;
        let mut bytes = Vec::with_capacity(pcm.len() * 4);
        for s in pcm {
            bytes.extend_from_slice(&s.to_le_bytes());
        }
        stdin.write_all(&bytes)?;
    }
    let out = child.wait_with_output()?;
    if !out.status.success() {
        bail!("AAC encode failed: {}", String::from_utf8_lossy(&out.stderr).trim());
    }
    Ok(())
}

/// Concatenates segments with `-c copy` (they share encoder settings and each starts on an IDR frame).
pub fn concat(segments: &[std::path::PathBuf], list: &Path, out: &Path) -> Result<()> {
    let mut s = String::new();
    for p in segments {
        s.push_str(&format!("file '{}'\n", p.display().to_string().replace('\'', "'\\''")));
    }
    std::fs::write(list, s)?;
    run(
        Command::new(ffmpeg_bin())
            .args(["-nostdin", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i"])
            .arg(list)
            .args(["-c", "copy", "-fflags", "+bitexact", "-map_metadata", "-1"])
            .arg(out),
        "concatenating segments",
    )?;
    Ok(())
}

/// Puts the video and the AAC audio in one MP4, both copied, faststart.
pub fn mux(video: &Path, audio: &Path, out: &Path) -> Result<()> {
    run(
        Command::new(ffmpeg_bin())
            .args(["-nostdin", "-v", "error", "-y", "-i"])
            .arg(video)
            .arg("-i")
            .arg(audio)
            .args(["-map", "0:v", "-map", "1:a", "-c", "copy", "-movflags", "+faststart"])
            .args(["-fflags", "+bitexact", "-map_metadata", "-1"])
            .arg(out),
        "muxing",
    )?;
    Ok(())
}

/// Counts the video frames in a file (decodes it).
pub fn count_frames(path: &Path) -> Result<u64> {
    let out = run(
        Command::new(ffprobe_bin())
            .args(["-v", "error", "-count_frames", "-select_streams", "v:0", "-show_entries", "stream=nb_read_frames"])
            .args(["-of", "csv=p=0"])
            .arg(path),
        "counting frames",
    )?;
    String::from_utf8_lossy(&out).trim().trim_end_matches(',').parse().context("frame count")
}
