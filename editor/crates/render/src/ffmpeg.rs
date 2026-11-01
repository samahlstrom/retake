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
    /// The audio streams in file order (`0:a:N` is `audio_streams[N]`).
    #[serde(default)]
    pub audio_streams: Vec<AudioStream>,
    /// The video stream's pixel format and colour tags, as ffprobe names them (`yuv420p`, `bt709`, `tv`).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub pix_fmt: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color_space: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color_range: Option<String>,
}

/// How a YUV source is read when zimg converts it (`zscale`'s `matrixin` and `rangein`), from its tags. Untagged
/// sources are read as BT.601 (`170m`) limited range, as ffmpeg's own scaler reads them, so footage looks the same
/// either way; `yuvj` formats are full range.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct SrcColor {
    /// A YUV pixel format (zimg converts it); RGB, grey and palette sources keep ffmpeg's scaler.
    pub yuv: bool,
    pub alpha: bool,
    pub matrix: &'static str,
    pub full: bool,
}

impl SrcColor {
    pub fn of(info: &MediaInfo) -> SrcColor {
        let pf = info.pix_fmt.as_deref().unwrap_or("");
        let yuv = ["yuv", "nv1", "nv2", "nv4", "p01", "p21", "p41"].iter().any(|p| pf.starts_with(p));
        let alpha = pf.starts_with("yuva");
        let matrix = match info.color_space.as_deref().unwrap_or("") {
            "bt709" => "709",
            "bt470bg" => "470bg",
            "bt2020nc" => "2020_ncl",
            "bt2020c" => "2020_cl",
            "fcc" => "fcc",
            "smpte240m" => "smpte240m",
            "ycgco" => "ycgco",
            _ => "170m",
        };
        let full = match info.color_range.as_deref() {
            Some("pc") | Some("jpeg") => true,
            Some("tv") | Some("mpeg") => false,
            _ => pf.starts_with("yuvj"),
        };
        SrcColor { yuv, alpha, matrix, full }
    }
}

/// How footage is turned from YUV into RGB in the decode pipe: zimg (`zscale`) when the ffmpeg in use has it, else
/// ffmpeg's scaler. Part of every unit key.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Convert {
    Zimg,
    Swscale,
}

impl Convert {
    pub fn name(self) -> &'static str {
        match self {
            Convert::Zimg => "zimg",
            Convert::Swscale => "swscale",
        }
    }

    /// zimg when ffmpeg has `zscale` (probed once, cached per machine), unless `RETAKE_CONVERT=swscale`.
    pub fn detect() -> Convert {
        match std::env::var("RETAKE_CONVERT").as_deref() {
            Ok("swscale") => Convert::Swscale,
            Ok("zimg") => Convert::Zimg,
            _ if crate::caps::has_zscale() => Convert::Zimg,
            _ => Convert::Swscale,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct AudioStream {
    pub channels: u32,
    /// The stream's title or language tag, if any (helps tell OBS tracks apart in errors).
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub title: Option<String>,
}

impl MediaInfo {
    /// "0: 2 ch (Mic), 1: 2 ch" for messages.
    pub fn describe_audio(&self) -> String {
        if self.audio_streams.is_empty() {
            return "no audio streams".into();
        }
        self.audio_streams
            .iter()
            .enumerate()
            .map(|(i, s)| match &s.title {
                Some(t) => format!("{i}: {} ch ({t})", s.channels),
                None => format!("{i}: {} ch", s.channels),
            })
            .collect::<Vec<_>>()
            .join(", ")
    }
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
    let audio_streams = streams
        .iter()
        .filter(|s| s["codec_type"] == "audio")
        .map(|s| AudioStream {
            channels: s["channels"].as_u64().unwrap_or(2) as u32,
            title: s["tags"]["title"]
                .as_str()
                .or(s["tags"]["language"].as_str().filter(|l| *l != "und"))
                .map(String::from),
        })
        .collect();
    let duration = v["format"]["duration"].as_str().and_then(|d| d.parse().ok()).unwrap_or(0.0);
    let size = video.and_then(|s| Some([s["width"].as_u64()? as u32, s["height"].as_u64()? as u32]));
    let fps = video.and_then(|s| {
        let r = s["avg_frame_rate"].as_str().or(s["r_frame_rate"].as_str())?;
        let (n, d) = r.split_once('/')?;
        let (n, d): (f64, f64) = (n.parse().ok()?, d.parse().ok()?);
        if d > 0.0 && n > 0.0 { Some(n / d) } else { None }
    });
    let tag =
        |k: &str| video.and_then(|s| s[k].as_str()).filter(|v| !v.is_empty() && *v != "unknown").map(String::from);
    Ok(MediaInfo {
        duration,
        size,
        fps,
        audio,
        audio_streams,
        pix_fmt: tag("pix_fmt"),
        color_space: tag("color_space"),
        color_range: tag("color_range"),
    })
}

/// How a video is decoded: the size, an optional crop applied in the pipe (source pixels: x, y, w, h), the scaler,
/// boxes `delogo` fills in first, and, for a YUV source when zimg converts, how to read its colour.
#[derive(Debug, Clone, PartialEq)]
pub struct Decode {
    pub w: u32,
    pub h: u32,
    pub crop: Option<[u32; 4]>,
    pub lanczos: bool,
    pub delogo: Vec<[u32; 4]>,
    /// zimg converts (and scales) this source; None: ffmpeg's scaler, straight to RGBA.
    pub zimg: Option<SrcColor>,
}

/// What a decoder writes into its pipe: packed RGBA (ffmpeg's scaler), or planar G, B, R (and A) from zimg, which
/// the reader packs into RGBA.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Pipe {
    Rgba,
    Gbrp,
    Gbrap,
}

impl Pipe {
    pub fn pix_fmt(self) -> &'static str {
        match self {
            Pipe::Rgba => "rgba",
            Pipe::Gbrp => "gbrp",
            Pipe::Gbrap => "gbrap",
        }
    }

    /// Bytes per frame in the pipe.
    pub fn frame_len(self, w: u32, h: u32) -> usize {
        let n = (w * h) as usize;
        match self {
            Pipe::Gbrp => n * 3,
            _ => n * 4,
        }
    }

    /// Packs planar G, B, R (, A) into RGBA (opaque without A). `raw` is one frame in the pipe's layout.
    pub fn unpack(self, raw: &[u8], out: &mut [u8]) {
        let n = out.len() / 4;
        match self {
            Pipe::Rgba => out.copy_from_slice(&raw[..n * 4]),
            Pipe::Gbrp | Pipe::Gbrap => {
                let (g, rest) = raw.split_at(n);
                let (b, rest) = rest.split_at(n);
                let r = &rest[..n];
                let px = out.as_chunks_mut::<4>().0.iter_mut().zip(r).zip(g).zip(b);
                if self == Pipe::Gbrap {
                    for ((((p, r), g), b), a) in px.zip(&rest[n..2 * n]) {
                        *p = [*r, *g, *b, *a];
                    }
                } else {
                    for (((p, r), g), b) in px {
                        *p = [*r, *g, *b, 255];
                    }
                }
            }
        }
    }
}

impl Decode {
    pub fn scaled(w: u32, h: u32) -> Decode {
        Decode { w, h, crop: None, lanczos: false, delogo: vec![], zimg: None }
    }

    pub fn pipe(&self) -> Pipe {
        match &self.zimg {
            None => Pipe::Rgba,
            Some(c) if c.alpha => Pipe::Gbrap,
            Some(_) => Pipe::Gbrp,
        }
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
        match &self.zimg {
            None => {
                let flags = if self.lanczos { "lanczos" } else { "bicubic+accurate_rnd+full_chroma_int" };
                f.push(format!("scale={}:{}:flags={flags}", self.w, self.h));
            }
            Some(c) => {
                let filter = if self.lanczos { "lanczos" } else { "spline36" };
                let range = if c.full { "full" } else { "limited" };
                f.push(format!(
                    "zscale=w={}:h={}:filter={filter}:dither=none:matrixin={}:rangein={range}",
                    self.w, self.h, c.matrix
                ));
            }
        }
        f.push(format!("format={}", self.pipe().pix_fmt()));
        f.join(",")
    }
}

/// Reads `buf.len()` bytes unless the stream ends first; true when it got them all.
fn read_full(r: &mut impl Read, buf: &mut [u8]) -> std::io::Result<bool> {
    let mut got = 0;
    while got < buf.len() {
        let n = r.read(&mut buf[got..])?;
        if n == 0 {
            return Ok(false);
        }
        got += n;
    }
    Ok(true)
}

/// Frames a threaded reader decodes ahead of the compositor.
pub const READ_AHEAD: usize = 2;

/// Where a reader's frames come from: its pipe, read when asked (`Inline`), or a thread that reads ahead into a
/// small bounded queue, packing as it goes, and gets used buffers back (`Threaded`).
enum Io {
    Inline {
        out: ChildStdout,
        pipe: Pipe,
        raw: Vec<u8>,
    },
    Threaded {
        rx: Option<std::sync::mpsc::Receiver<std::io::Result<Vec<u8>>>>,
        back: std::sync::mpsc::Sender<Vec<u8>>,
        join: Option<std::thread::JoinHandle<()>>,
    },
}

/// Sequential RGBA frames from a video, at the output fps, starting at `start` seconds. After the end of the file it
/// repeats the last frame. Read inline or by a thread of its own: the frames are the same.
pub struct VideoReader {
    child: Child,
    pub w: u32,
    pub h: u32,
    frame: Vec<u8>,
    have: bool,
    eof: bool,
    io: Io,
}

impl VideoReader {
    pub fn open(path: &Path, start: f64, fps: u32, w: u32, h: u32, looping: bool) -> Result<VideoReader> {
        VideoReader::open_with(path, start, fps, &Decode::scaled(w, h), looping, false)
    }

    pub fn open_with(
        path: &Path,
        start: f64,
        fps: u32,
        d: &Decode,
        looping: bool,
        threaded: bool,
    ) -> Result<VideoReader> {
        let (w, h) = (d.w, d.h);
        let pipe = d.pipe();
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
            pipe.pix_fmt(),
            "-",
        ]);
        let mut child = cmd
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .stdin(Stdio::null())
            .spawn()
            .with_context(|| format!("starting ffmpeg to read {}", path.display()))?;
        let mut out = child.stdout.take().ok_or_else(|| anyhow!("no ffmpeg stdout"))?;
        let len = (w * h * 4) as usize;
        let io = if threaded {
            let (tx, rx) = std::sync::mpsc::sync_channel::<std::io::Result<Vec<u8>>>(READ_AHEAD);
            let (back, back_rx) = std::sync::mpsc::channel::<Vec<u8>>();
            let join = std::thread::Builder::new()
                .name("retake-decode".into())
                .spawn(move || {
                    let mut raw = if pipe == Pipe::Rgba { vec![] } else { vec![0u8; pipe.frame_len(w, h)] };
                    loop {
                        let mut buf = back_rx.try_recv().unwrap_or_else(|_| vec![0u8; len]);
                        let got = if pipe == Pipe::Rgba {
                            read_full(&mut out, &mut buf)
                        } else {
                            read_full(&mut out, &mut raw).inspect(|&ok| {
                                if ok {
                                    pipe.unpack(&raw, &mut buf)
                                }
                            })
                        };
                        let msg = match got {
                            Ok(true) => Ok(buf),
                            Ok(false) => return, // the end: the channel closing says so
                            Err(e) => Err(e),
                        };
                        let stop = msg.is_err();
                        if tx.send(msg).is_err() || stop {
                            return;
                        }
                    }
                })
                .context("starting a decode thread")?;
            Io::Threaded { rx: Some(rx), back, join: Some(join) }
        } else {
            let raw = if pipe == Pipe::Rgba { vec![] } else { vec![0u8; pipe.frame_len(w, h)] };
            Io::Inline { out, pipe, raw }
        };
        Ok(VideoReader { child, w, h, frame: vec![0; len], have: false, eof: false, io })
    }

    /// The last frame read (or black before any).
    pub fn current(&self) -> &[u8] {
        &self.frame
    }

    /// The next frame (RGBA, opaque).
    pub fn next_frame(&mut self) -> Result<&[u8]> {
        if !self.eof {
            let next = match &mut self.io {
                Io::Inline { out, pipe, raw } => {
                    let mut buf = vec![0u8; self.frame.len()];
                    let ok = if *pipe == Pipe::Rgba {
                        read_full(out, &mut buf)?
                    } else {
                        let ok = read_full(out, raw)?;
                        if ok {
                            pipe.unpack(raw, &mut buf);
                        }
                        ok
                    };
                    ok.then_some(buf)
                }
                Io::Threaded { rx, .. } => match rx.as_ref().map(|r| r.recv()) {
                    Some(Ok(Ok(buf))) => Some(buf),
                    Some(Ok(Err(e))) => return Err(e).context("reading a decoded frame"),
                    _ => None,
                },
            };
            match next {
                Some(buf) => {
                    let old = std::mem::replace(&mut self.frame, buf);
                    if let Io::Threaded { back, .. } = &self.io {
                        let _ = back.send(old);
                    }
                    self.have = true;
                }
                None => {
                    self.eof = true;
                    if !self.have {
                        // Nothing decoded at all (start past the end): opaque black.
                        for px in self.frame.as_chunks_mut::<4>().0 {
                            px.copy_from_slice(&[0, 0, 0, 255]);
                        }
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
        if let Io::Threaded { rx, join, .. } = &mut self.io {
            // The thread is reading (the kill ends that) or waiting to hand a frame over (closing the queue ends that).
            drop(rx.take());
            if let Some(j) = join.take() {
                let _ = j.join();
            }
        }
        let _ = self.child.wait();
    }
}

/// The H.264 encoders a render can use. Hardware ones go through ffmpeg like x264 does; none needs code of its own.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum EncoderKind {
    X264,
    Nvenc,
    Videotoolbox,
    Qsv,
    Vaapi,
}

impl EncoderKind {
    pub const ALL: [EncoderKind; 5] =
        [EncoderKind::X264, EncoderKind::Nvenc, EncoderKind::Videotoolbox, EncoderKind::Qsv, EncoderKind::Vaapi];

    pub fn name(self) -> &'static str {
        match self {
            EncoderKind::X264 => "x264",
            EncoderKind::Nvenc => "nvenc",
            EncoderKind::Videotoolbox => "videotoolbox",
            EncoderKind::Qsv => "qsv",
            EncoderKind::Vaapi => "vaapi",
        }
    }

    pub fn parse(s: &str) -> Option<EncoderKind> {
        EncoderKind::ALL.into_iter().find(|k| k.name() == s.trim().to_ascii_lowercase())
    }

    pub fn is_hw(self) -> bool {
        self != EncoderKind::X264
    }
}

/// What `[render] encoder` (and `--encoder`, `RETAKE_ENCODER`) asks for: one encoder, or `auto`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EncoderChoice {
    /// NVENC on NVIDIA, VideoToolbox on macOS, else x264: a real one-frame test encode decides, cached per machine.
    Auto,
    Fixed(EncoderKind),
}

impl EncoderChoice {
    pub fn parse(s: &str) -> Result<EncoderChoice> {
        let s = s.trim().to_ascii_lowercase();
        if s == "auto" || s.is_empty() {
            return Ok(EncoderChoice::Auto);
        }
        EncoderKind::parse(&s)
            .map(EncoderChoice::Fixed)
            .ok_or_else(|| anyhow!("unknown encoder `{s}`: use auto, x264, nvenc, videotoolbox, qsv or vaapi"))
    }

    pub fn resolve(self) -> EncoderKind {
        match self {
            EncoderChoice::Fixed(k) => k,
            EncoderChoice::Auto => crate::caps::auto_encoder(),
        }
    }
}

/// Encoder settings: everything that can change the encoded bytes, down to the encoder's own options. Hashed into
/// every unit key, so switching encoders (or tuning one) re-renders each unit once and never mixes segments.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EncodeSettings {
    pub encoder: EncoderKind,
    pub codec: String,
    /// The encoder's options after `-c:v <codec>`: rate control, preset, GOP.
    pub args: Vec<String>,
    pub pix_fmt: String,
    /// Frames between keyframes (2 s): every unit is cut into stream segments at them.
    pub gop: u32,
    pub color: String,
    pub ffmpeg: String,
}

/// The VAAPI render node (`RETAKE_VAAPI_DEVICE` overrides it).
fn vaapi_device() -> String {
    std::env::var("RETAKE_VAAPI_DEVICE").unwrap_or_else(|_| "/dev/dri/renderD128".into())
}

impl EncodeSettings {
    /// x264 at `medium`, CRF 18 (drafts `veryfast`, CRF 23), or a hardware encoder tuned to match it on the item-shop
    /// pilot (editor/README.md, "Encoders"). One IDR every 2 s, closed GOPs, no scene cuts, two B-frames.
    pub fn new(draft: bool, fps: u32, kind: EncoderKind) -> EncodeSettings {
        let gop = fps * 2;
        let g = gop.to_string();
        let (codec, args, pix_fmt): (&str, Vec<&str>, &str) = match kind {
            EncoderKind::X264 => (
                "libx264",
                vec![
                    "-preset",
                    if draft { "veryfast" } else { "medium" },
                    "-crf",
                    if draft { "23" } else { "18" },
                    "-g",
                    g.as_str(),
                    "-bf",
                    "2",
                    "-x264-params",
                    "threads=4:open-gop=0:scenecut=0",
                ],
                "yuv420p",
            ),
            EncoderKind::Nvenc => (
                "h264_nvenc",
                vec![
                    "-preset",
                    if draft { "p3" } else { "p6" },
                    "-tune",
                    "hq",
                    "-rc",
                    "vbr",
                    "-cq",
                    if draft { "25" } else { "19" },
                    "-b:v",
                    "0",
                    "-profile:v",
                    "high",
                    "-g",
                    g.as_str(),
                    "-bf",
                    "2",
                    "-spatial-aq",
                    "1",
                    "-rc-lookahead",
                    "20",
                    "-no-scenecut",
                    "1",
                    "-strict_gop",
                    "1",
                    "-forced-idr",
                    "1",
                ],
                "yuv420p",
            ),
            EncoderKind::Videotoolbox => (
                "h264_videotoolbox",
                vec![
                    "-profile:v",
                    "high",
                    "-q:v",
                    if draft { "50" } else { "65" },
                    "-realtime",
                    "0",
                    // Hardware only: without it (a VM, an old Mac) the test encode fails and auto picks x264.
                    "-allow_sw",
                    "0",
                    "-g",
                    g.as_str(),
                    "-bf",
                    "2",
                ],
                "yuv420p",
            ),
            EncoderKind::Qsv => (
                "h264_qsv",
                vec![
                    "-preset",
                    if draft { "veryfast" } else { "slower" },
                    "-global_quality",
                    if draft { "25" } else { "19" },
                    "-profile:v",
                    "high",
                    "-g",
                    g.as_str(),
                    "-bf",
                    "2",
                    "-idr_interval",
                    "0",
                ],
                "nv12",
            ),
            EncoderKind::Vaapi => (
                "h264_vaapi",
                vec![
                    "-rc_mode",
                    "CQP",
                    "-qp",
                    if draft { "25" } else { "19" },
                    "-profile:v",
                    "high",
                    "-g",
                    g.as_str(),
                    "-bf",
                    "2",
                ],
                "vaapi",
            ),
        };
        EncodeSettings {
            encoder: kind,
            codec: codec.into(),
            args: args.into_iter().map(String::from).collect(),
            pix_fmt: pix_fmt.into(),
            gop,
            color: "bt709-tv".into(),
            ffmpeg: version().into(),
        }
    }

    /// Arguments that go before the input (a hardware device).
    pub fn input_args(&self) -> Vec<String> {
        match self.encoder {
            EncoderKind::Vaapi => vec!["-vaapi_device".into(), vaapi_device()],
            _ => vec![],
        }
    }

    /// Everything after the input up to the container: the RGB → YUV conversion (BT.709, limited range, by ffmpeg's
    /// scaler for every encoder, so colours never depend on the encoder), the codec and its options, a keyframe
    /// forced every GOP for hardware encoders (x264's fixed GOP does it already), the colour tags, bitexact flags.
    pub fn output_args(&self) -> Vec<String> {
        let mut vf =
            String::from("scale=out_color_matrix=bt709:out_range=tv:flags=bicubic+accurate_rnd+full_chroma_int");
        if self.encoder == EncoderKind::Vaapi {
            vf.push_str(",format=nv12,hwupload");
        }
        let mut a: Vec<String> = vec!["-vf".into(), vf, "-c:v".into(), self.codec.clone()];
        a.extend(self.args.iter().cloned());
        if self.encoder != EncoderKind::Vaapi {
            a.extend(["-pix_fmt".into(), self.pix_fmt.clone()]);
        }
        if self.encoder.is_hw() {
            a.extend(["-force_key_frames".into(), format!("expr:eq(mod(n,{}),0)", self.gop)]);
        }
        for x in ["-colorspace", "bt709", "-color_primaries", "bt709", "-color_trc", "bt709", "-color_range", "tv"] {
            a.push(x.into());
        }
        for x in ["-fflags", "+bitexact", "-flags:v", "+bitexact", "-map_metadata", "-1"] {
            a.push(x.into());
        }
        a
    }
}

/// An encoder that failed (it exited, or wouldn't take frames): with ffmpeg's own words. A hardware encoder can fail
/// for reasons that pass (every session in use), so a unit that hits this is tried again.
#[derive(Debug)]
pub struct EncodeError(pub String);

impl std::fmt::Display for EncodeError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "ffmpeg encoder failed: {}", self.0)
    }
}

impl std::error::Error for EncodeError {}

/// Hardware encode sessions one process holds at once (`RETAKE_HW_SESSIONS`, default 4): NVIDIA's consumer cards
/// allow a handful per machine, and two or three already outrun the compositors feeding them.
pub fn hw_sessions() -> usize {
    std::env::var("RETAKE_HW_SESSIONS").ok().and_then(|v| v.parse().ok()).filter(|n| *n > 0).unwrap_or(4)
}

/// A hardware encode session held: units wait for one, so a render never opens more than [`hw_sessions`].
pub struct HwSlot(());

static SLOTS: (std::sync::Mutex<usize>, std::sync::Condvar) = (std::sync::Mutex::new(0), std::sync::Condvar::new());

impl HwSlot {
    pub fn take() -> HwSlot {
        let limit = hw_sessions();
        let mut used = SLOTS.0.lock().unwrap();
        while *used >= limit {
            used = SLOTS.1.wait(used).unwrap();
        }
        *used += 1;
        HwSlot(())
    }
}

impl Drop for HwSlot {
    fn drop(&mut self) {
        *SLOTS.0.lock().unwrap() -= 1;
        SLOTS.1.notify_one();
    }
}

/// Encodes raw RGBA frames into one closed-GOP H.264 segment that starts on an IDR frame.
pub struct Encoder {
    child: Child,
    stdin: Option<ChildStdin>,
    frame_len: usize,
    done: bool,
}

impl Encoder {
    pub fn open(out: &Path, w: u32, h: u32, fps: u32, frames: i64, s: &EncodeSettings) -> Result<Encoder> {
        let mut cmd = Command::new(ffmpeg_bin());
        cmd.args(["-nostdin", "-v", "error", "-y"])
            .args(s.input_args())
            .args(["-f", "rawvideo", "-pix_fmt", "rgba"])
            .args(["-s", &format!("{w}x{h}"), "-r", &fps.to_string(), "-i", "-"])
            .args(["-frames:v", &frames.to_string(), "-an"])
            .args(s.output_args())
            .args(["-f", "mp4"])
            .arg(out);
        let mut child = cmd
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .context("starting the ffmpeg encoder")?;
        let stdin = child.stdin.take();
        Ok(Encoder { child, stdin, frame_len: (w * h * 4) as usize, done: false })
    }

    /// ffmpeg's stderr once it has exited (it has, when a write fails).
    fn failure(&mut self) -> anyhow::Error {
        drop(self.stdin.take());
        let mut err = String::new();
        if let Some(mut e) = self.child.stderr.take() {
            let _ = e.read_to_string(&mut err);
        }
        let _ = self.child.wait();
        self.done = true;
        let err = err.trim();
        anyhow::Error::new(EncodeError(if err.is_empty() { "it stopped taking frames".into() } else { err.into() }))
    }

    pub fn write(&mut self, rgba: &[u8]) -> Result<()> {
        debug_assert_eq!(rgba.len(), self.frame_len);
        let stdin = self.stdin.as_mut().ok_or_else(|| anyhow!("encoder closed"))?;
        if stdin.write_all(rgba).is_err() {
            return Err(self.failure());
        }
        Ok(())
    }

    pub fn finish(mut self) -> Result<()> {
        drop(self.stdin.take());
        let mut err = String::new();
        if let Some(mut e) = self.child.stderr.take() {
            let _ = e.read_to_string(&mut err);
        }
        let st = self.child.wait()?;
        self.done = true;
        if !st.success() {
            return Err(EncodeError(err.trim().into()).into());
        }
        Ok(())
    }
}

impl Drop for Encoder {
    fn drop(&mut self) {
        if !self.done {
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }
}

/// Which audio stream to decode, and how many channels it has.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub struct StreamSel {
    /// `0:a:N`.
    pub index: u32,
    pub channels: u32,
}

impl Default for StreamSel {
    fn default() -> Self {
        StreamSel { index: 0, channels: 2 }
    }
}

/// Decodes `dur` seconds of audio stream `sel` from `start`, as interleaved stereo f32 at `sr`. A mono stream is
/// duplicated into both channels at unity (ffmpeg's own mono upmix is −3 dB); more channels are downmixed.
pub fn decode_audio(path: &Path, start: f64, dur: f64, sr: u32, looping: bool, sel: StreamSel) -> Result<Vec<f32>> {
    let mut cmd = Command::new(ffmpeg_bin());
    cmd.args(["-nostdin", "-v", "error"]);
    if looping {
        cmd.args(["-stream_loop", "-1"]);
    }
    cmd.args(["-ss", &format!("{:.6}", start.max(0.0)), "-t", &format!("{:.6}", dur.max(0.0))])
        .arg("-i")
        .arg(path)
        .args(["-map", &format!("0:a:{}", sel.index), "-vn"]);
    if sel.channels == 1 {
        cmd.args(["-af", "pan=stereo|c0=c0|c1=c0"]);
    } else {
        cmd.args(["-ac", "2"]);
    }
    cmd.args(["-ar", &sr.to_string(), "-f", "f32le", "-"]);
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

/// Copies the AAC frames of an `.m4a` into a raw ADTS stream, as encoded: the encoder's priming frame first.
pub fn to_adts(m4a: &Path, out: &Path) -> Result<()> {
    run(
        Command::new(ffmpeg_bin())
            .args(["-nostdin", "-v", "error", "-y", "-i"])
            .arg(m4a)
            .args(["-map", "0:a:0", "-c", "copy", "-f", "adts"])
            .arg(out),
        "copying the AAC frames",
    )?;
    Ok(())
}

/// Remuxes one unit's segment, with the AAC frames of `audio` (raw ADTS), into a fragmented MP4 for HLS without
/// re-encoding: a fragment at every keyframe, signed composition offsets, no edit lists (crate::fmp4 then puts its
/// times on the stream's clock). The moov waits for the first fragment (`delay_moov`), so the audio's
/// AudioSpecificConfig, which only the first ADTS frame gives, is in it: players need it for the codec string.
pub fn fragment(video: &Path, audio: Option<&Path>, out: &Path) -> Result<()> {
    let mut cmd = Command::new(ffmpeg_bin());
    cmd.args(["-nostdin", "-v", "error", "-y", "-i"]).arg(video);
    if let Some(a) = audio {
        cmd.args(["-f", "aac", "-i"]).arg(a);
    }
    cmd.args(["-map", "0:v:0"]);
    if audio.is_some() {
        cmd.args(["-map", "1:a:0", "-bsf:a", "aac_adtstoasc"]);
    }
    cmd.args([
        "-c",
        "copy",
        "-movflags",
        "+frag_keyframe+empty_moov+delay_moov+default_base_moof+negative_cts_offsets+skip_trailer",
    ])
    .args(["-use_editlist", "0", "-fflags", "+bitexact", "-map_metadata", "-1", "-f", "mp4"])
    .arg(out);
    run(&mut cmd, "fragmenting a unit for HLS")?;
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

#[cfg(test)]
mod tests {
    use super::*;

    fn info(pix: &str, space: Option<&str>, range: Option<&str>) -> MediaInfo {
        MediaInfo {
            duration: 1.0,
            size: Some([16, 16]),
            fps: Some(30.0),
            audio: false,
            audio_streams: vec![],
            pix_fmt: Some(pix.into()),
            color_space: space.map(String::from),
            color_range: range.map(String::from),
        }
    }

    #[test]
    fn colour_tags_decide_how_zimg_reads_a_source() {
        let c = SrcColor::of(&info("yuv420p", Some("bt709"), Some("tv")));
        assert_eq!(c, SrcColor { yuv: true, alpha: false, matrix: "709", full: false });
        // Untagged: BT.601 limited, as ffmpeg's scaler reads it (so footage looks the same on either path).
        assert_eq!(SrcColor::of(&info("yuv420p", None, None)).matrix, "170m");
        assert!(!SrcColor::of(&info("yuv420p", None, None)).full);
        assert!(SrcColor::of(&info("yuvj420p", None, None)).full, "yuvj is full range");
        assert!(SrcColor::of(&info("yuv420p", Some("bt470bg"), Some("pc"))).full);
        assert_eq!(SrcColor::of(&info("yuv420p10le", Some("bt2020nc"), None)).matrix, "2020_ncl");
        assert!(SrcColor::of(&info("nv12", None, None)).yuv);
        assert!(SrcColor::of(&info("p010le", None, None)).yuv);
        let a = SrcColor::of(&info("yuva420p", None, None));
        assert!(a.yuv && a.alpha);
        // RGB, grey and palette sources keep ffmpeg's scaler.
        for pf in ["bgr0", "rgb24", "gbrp", "rgba", "gray", "pal8"] {
            assert!(!SrcColor::of(&info(pf, None, None)).yuv, "{pf}");
        }
    }

    #[test]
    fn decode_chains_for_both_converters() {
        let mut d = Decode::scaled(1280, 720);
        assert_eq!(d.filters(60), "fps=60,scale=1280:720:flags=bicubic+accurate_rnd+full_chroma_int,format=rgba");
        assert_eq!(d.pipe(), Pipe::Rgba);
        d.zimg = Some(SrcColor { yuv: true, alpha: false, matrix: "709", full: false });
        d.crop = Some([2, 4, 640, 360]);
        d.delogo = vec![[10, 10, 20, 20]];
        assert_eq!(
            d.filters(60),
            "fps=60,delogo=x=10:y=10:w=20:h=20,crop=640:360:2:4,\
             zscale=w=1280:h=720:filter=spline36:dither=none:matrixin=709:rangein=limited,format=gbrp"
        );
        assert_eq!(d.pipe(), Pipe::Gbrp);
        d.lanczos = true;
        d.zimg = Some(SrcColor { yuv: true, alpha: true, matrix: "170m", full: true });
        assert!(
            d.filters(30)
                .ends_with("zscale=w=1280:h=720:filter=lanczos:dither=none:matrixin=170m:rangein=full,format=gbrap")
        );
        assert_eq!(d.pipe(), Pipe::Gbrap);
    }

    #[test]
    fn planar_frames_pack_into_rgba() {
        // 2 pixels: planes G, B, R (, A).
        let raw = [10, 11, 20, 21, 30, 31];
        let mut out = [0u8; 8];
        Pipe::Gbrp.unpack(&raw, &mut out);
        assert_eq!(out, [30, 10, 20, 255, 31, 11, 21, 255]);
        let raw = [10, 11, 20, 21, 30, 31, 40, 41];
        Pipe::Gbrap.unpack(&raw, &mut out);
        assert_eq!(out, [30, 10, 20, 40, 31, 11, 21, 41]);
        Pipe::Rgba.unpack(&[1, 2, 3, 4, 5, 6, 7, 8], &mut out);
        assert_eq!(out, [1, 2, 3, 4, 5, 6, 7, 8]);
        assert_eq!((Pipe::Gbrp.frame_len(4, 2), Pipe::Gbrap.frame_len(4, 2), Pipe::Rgba.frame_len(4, 2)), (24, 32, 32));
    }

    #[test]
    fn encoder_names_and_choices() {
        for k in EncoderKind::ALL {
            assert_eq!(EncoderKind::parse(k.name()), Some(k));
            assert_eq!(EncoderChoice::parse(k.name()).unwrap(), EncoderChoice::Fixed(k));
        }
        assert_eq!(EncoderChoice::parse("auto").unwrap(), EncoderChoice::Auto);
        assert_eq!(EncoderChoice::parse("").unwrap(), EncoderChoice::Auto, "unset is auto");
        assert_eq!(EncoderChoice::parse(" NVENC ").unwrap(), EncoderChoice::Fixed(EncoderKind::Nvenc));
        assert!(EncoderChoice::parse("h265").unwrap_err().to_string().contains("auto, x264, nvenc"));
        assert!(!EncoderKind::X264.is_hw() && EncoderKind::Nvenc.is_hw());
    }

    fn cmd(kind: EncoderKind, draft: bool) -> String {
        let s = EncodeSettings::new(draft, 60, kind);
        assert_eq!(s.gop, 120);
        let mut a = s.input_args();
        a.extend(s.output_args());
        a.join(" ")
    }

    #[test]
    fn x264_command_line() {
        let c = cmd(EncoderKind::X264, false);
        assert!(
            c.starts_with("-vf scale=out_color_matrix=bt709:out_range=tv:flags=bicubic+accurate_rnd+full_chroma_int ")
        );
        assert!(c.contains(
            "-c:v libx264 -preset medium -crf 18 -g 120 -bf 2 -x264-params threads=4:open-gop=0:scenecut=0 -pix_fmt yuv420p"
        ));
        assert!(!c.contains("force_key_frames"), "x264's fixed GOP puts the keyframes");
        assert!(c.ends_with("-color_range tv -fflags +bitexact -flags:v +bitexact -map_metadata -1"));
        assert!(cmd(EncoderKind::X264, true).contains("-preset veryfast -crf 23"));
    }

    #[test]
    fn nvenc_command_line() {
        let c = cmd(EncoderKind::Nvenc, false);
        assert!(c.contains("-c:v h264_nvenc -preset "), "{c}");
        for want in ["-rc vbr", "-b:v 0", "-g 120", "-bf 2", "-no-scenecut 1", "-strict_gop 1", "-forced-idr 1"] {
            assert!(c.contains(want), "{want} in {c}");
        }
        assert!(c.contains("-pix_fmt yuv420p -force_key_frames expr:eq(mod(n,120),0)"), "{c}");
        assert!(c.contains("-colorspace bt709"), "VUI tags like x264's");
        assert_ne!(cmd(EncoderKind::Nvenc, true), c, "drafts are faster");
    }

    /// Needs a real Mac to run; here only the command line is checked.
    #[test]
    fn videotoolbox_command_line() {
        let c = cmd(EncoderKind::Videotoolbox, false);
        assert!(
            c.contains("-c:v h264_videotoolbox -profile:v high -q:v 65 -realtime 0 -allow_sw 0 -g 120 -bf 2"),
            "{c}"
        );
        assert!(c.contains("-pix_fmt yuv420p -force_key_frames expr:eq(mod(n,120),0)"), "{c}");
    }

    #[test]
    fn qsv_and_vaapi_command_lines() {
        let q = cmd(EncoderKind::Qsv, false);
        assert!(q.contains("-c:v h264_qsv") && q.contains("-pix_fmt nv12"), "{q}");
        let v = cmd(EncoderKind::Vaapi, false);
        assert!(v.starts_with("-vaapi_device /dev/dri/renderD128 -vf scale="), "{v}");
        assert!(v.contains(",format=nv12,hwupload -c:v h264_vaapi -rc_mode CQP"), "{v}");
        assert!(!v.contains("-pix_fmt"), "VAAPI takes hardware frames: no -pix_fmt");
    }

    #[test]
    fn settings_differ_per_encoder_so_their_keys_do() {
        let all: Vec<String> = EncoderKind::ALL
            .iter()
            .map(|k| serde_json::to_string(&EncodeSettings::new(false, 60, *k)).unwrap())
            .collect();
        for (i, a) in all.iter().enumerate() {
            assert!(all[i + 1..].iter().all(|b| a != b));
        }
        assert!(all[1].contains("\"encoder\":\"nvenc\""), "{}", all[1]);
    }

    #[test]
    fn hardware_slots_are_limited() {
        // SAFETY of the global: this is the only test that takes slots.
        let n = hw_sessions();
        let held: Vec<HwSlot> = (0..n).map(|_| HwSlot::take()).collect();
        let (tx, rx) = std::sync::mpsc::channel();
        let t = std::thread::spawn(move || {
            let _s = HwSlot::take();
            tx.send(()).unwrap();
        });
        assert!(rx.recv_timeout(std::time::Duration::from_millis(200)).is_err(), "waits while every slot is held");
        drop(held);
        rx.recv_timeout(std::time::Duration::from_secs(5)).expect("gets one once they're free");
        t.join().unwrap();
    }
}
