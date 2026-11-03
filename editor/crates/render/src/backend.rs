//! Component backends (spec/components.md, "Backends"): a component's program draws its frames. The renderer runs
//! it with the `retake-raster/1` protocol: one JSON request line on stdin, then per frame a JSON header line on stdout
//! (`{"box": [x, y, w, h]}` and w·h·4 bytes of straight-alpha RGBA, `{"same": true}` or `{"empty": true}`).
//!
//! The program runs in its package folder with a cleared environment and a per-request timeout. Frames are parsed on a
//! reader thread and handed over one at a time, so a long request never holds more than a couple of frames.

use retake_timeline::EditError;
use retake_timeline::layer::RasterBackend;
use serde_json::Value;
use std::io::{BufRead, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{Receiver, RecvTimeoutError, sync_channel};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

pub const PROTOCOL: &str = "retake-raster/1";
/// Seconds a request may take when the component doesn't say.
pub const DEFAULT_TIMEOUT: f64 = 60.0;
/// The largest frame a backend may send, in bytes (a 8K RGBA frame is 133 MB).
const MAX_FRAME_BYTES: u64 = 512 << 20;
/// The longest header line, in bytes.
const MAX_HEADER: usize = 64 << 10;
/// How much of the program's stderr is kept for error messages.
const MAX_STDERR: usize = 16 << 10;

/// One frame from a backend.
#[derive(Debug, Clone, PartialEq)]
pub enum RasterFrame {
    /// Nothing drawn.
    Empty,
    /// The previous frame again.
    Same,
    /// Straight-alpha RGBA pixels placed at `bx` = [x, y, w, h] in output pixels.
    Image { bx: [i64; 4], rgba: Vec<u8> },
}

enum Msg {
    Frame(RasterFrame),
    /// Malformed or missing output (the message says what).
    Bad(String),
    /// Every frame arrived; `extra` bytes followed the last one.
    Done {
        extra: usize,
    },
}

/// A running request: frames come out in order with [`BackendStream::next_frame`]; [`BackendStream::finish`] checks the
/// program's exit. Dropping it kills the program.
pub struct BackendStream {
    child: Child,
    rx: Receiver<Msg>,
    stderr: Arc<Mutex<Vec<u8>>>,
    deadline: Instant,
    timeout: f64,
    left: i64,
    what: String,
    tmp: PathBuf,
    done: bool,
}

/// The program's path: a first argument with a `/` (or, on Windows, a `\`) is a path in the package, a bare name is
/// looked up on `PATH`.
fn program(b: &RasterBackend) -> PathBuf {
    let p = &b.command[0];
    let in_package = p.contains('/') || (cfg!(windows) && p.contains('\\'));
    if in_package && !Path::new(p).is_absolute() { Path::new(&b.dir).join(p) } else { PathBuf::from(p) }
}

/// What a backend keeps of the environment besides `PATH`: nothing on Unix. On Windows a program started without
/// `SystemRoot` can't even seed its random numbers (Python stops at once), so the system's own variables stay, and
/// `TEMP`/`TMP` are the scratch folder as `TMPDIR` is.
const KEEP_ENV: &[&str] = if cfg!(windows) { &["SYSTEMROOT", "WINDIR", "PATHEXT", "COMSPEC"] } else { &[] };

fn scratch_dir() -> PathBuf {
    static N: AtomicU64 = AtomicU64::new(0);
    let n = N.fetch_add(1, Ordering::Relaxed);
    std::env::temp_dir().join(format!("retake-backend-{}-{n}", std::process::id()))
}

impl BackendStream {
    /// Starts the program with `request` (one JSON line) for `frames` frames. `what` names the layer in errors.
    pub fn open(b: &RasterBackend, request: &Value, frames: i64, what: &str) -> Result<BackendStream, EditError> {
        let timeout = b.timeout.unwrap_or(DEFAULT_TIMEOUT);
        let fail = |m: String| EditError::new("backend_failed", format!("{what}: {m}"));
        if b.command.is_empty() {
            return Err(fail("the backend has no command".into()));
        }
        let tmp = scratch_dir();
        std::fs::create_dir_all(&tmp).map_err(|e| fail(format!("can't make its scratch folder: {e}")))?;
        let mut cmd = Command::new(program(b));
        cmd.args(&b.command[1..])
            .current_dir(&b.dir)
            .env_clear()
            .env("PATH", std::env::var_os("PATH").unwrap_or_default())
            .env("LANG", "C.UTF-8")
            .env("PYTHONDONTWRITEBYTECODE", "1")
            .env("TMPDIR", &tmp)
            .envs(KEEP_ENV.iter().filter_map(|k| std::env::var_os(k).map(|v| (*k, v))))
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        if cfg!(windows) {
            cmd.env("TEMP", &tmp).env("TMP", &tmp);
        }
        let mut child = match cmd.spawn() {
            Ok(c) => c,
            Err(e) => {
                let _ = std::fs::remove_dir_all(&tmp);
                return Err(fail(format!("can't start `{}` in {}: {e}", b.command.join(" "), b.dir)));
            }
        };
        let deadline = Instant::now() + Duration::from_secs_f64(timeout);
        // The request is one line, written on its own thread (a big one mustn't block us while the program writes
        // frames first); the pipe closes after it.
        if let Some(mut stdin) = child.stdin.take() {
            let mut line = request.to_string();
            line.push('\n');
            std::thread::spawn(move || {
                let _ = stdin.write_all(line.as_bytes());
            });
        }
        let stderr = Arc::new(Mutex::new(Vec::new()));
        if let Some(mut err) = child.stderr.take() {
            let keep = stderr.clone();
            std::thread::spawn(move || {
                let mut buf = [0u8; 4096];
                while let Ok(n) = err.read(&mut buf) {
                    if n == 0 {
                        break;
                    }
                    let mut k = keep.lock().unwrap();
                    k.extend_from_slice(&buf[..n]);
                    if k.len() > MAX_STDERR {
                        let cut = k.len() - MAX_STDERR;
                        k.drain(..cut);
                    }
                }
            });
        }
        let out = child.stdout.take().ok_or_else(|| fail("no stdout".into()))?;
        let (tx, rx) = sync_channel::<Msg>(2);
        std::thread::spawn(move || {
            let mut r = BufReader::with_capacity(1 << 20, out);
            for k in 0..frames.max(0) {
                let msg = match read_frame(&mut r) {
                    Ok(Some(f)) => Msg::Frame(f),
                    Ok(None) => Msg::Bad(format!("the output ended after {k} of {frames} frames")),
                    Err(m) => Msg::Bad(format!("frame {k}: {m}")),
                };
                let stop = matches!(msg, Msg::Bad(_));
                if tx.send(msg).is_err() || stop {
                    return;
                }
            }
            let mut rest = vec![];
            let _ = r.read_to_end(&mut rest);
            let extra = rest.iter().filter(|b| !b.is_ascii_whitespace()).count();
            let _ = tx.send(Msg::Done { extra });
        });
        Ok(BackendStream {
            child,
            rx,
            stderr,
            deadline,
            timeout,
            left: frames,
            what: what.to_string(),
            tmp,
            done: false,
        })
    }

    fn stderr_tail(&self) -> String {
        let s = String::from_utf8_lossy(&self.stderr.lock().unwrap()).into_owned();
        let lines: Vec<&str> = s.lines().filter(|l| !l.trim().is_empty()).collect();
        let tail = lines[lines.len().saturating_sub(6)..].join(" | ");
        if tail.is_empty() { String::new() } else { format!(" (stderr: {tail})") }
    }

    fn timed_out(&mut self) -> EditError {
        let _ = self.child.kill();
        let _ = self.child.wait();
        EditError::new(
            "backend_timeout",
            format!(
                "{}: the backend took longer than {} s and was stopped{}",
                self.what,
                self.timeout,
                self.stderr_tail()
            ),
        )
        .hint("raise backend.timeout in component.json, or make the program faster")
    }

    /// Waits for the program to exit (until the deadline); a non-zero status is `backend_failed`.
    fn exit_status(&mut self) -> Result<(), EditError> {
        loop {
            match self.child.try_wait() {
                Ok(Some(st)) if st.success() => return Ok(()),
                Ok(Some(st)) => {
                    // Let the stderr thread catch up with the last lines.
                    std::thread::sleep(Duration::from_millis(20));
                    return Err(EditError::new(
                        "backend_failed",
                        format!("{}: the backend exited with {st}{}", self.what, self.stderr_tail()),
                    ));
                }
                Ok(None) if Instant::now() >= self.deadline => return Err(self.timed_out()),
                Ok(None) => std::thread::sleep(Duration::from_millis(5)),
                Err(e) => return Err(EditError::new("backend_failed", format!("{}: {e}", self.what))),
            }
        }
    }

    /// Bad output: a crash explains it better when the program exited non-zero.
    fn bad_output(&mut self, m: String) -> EditError {
        let grace = Instant::now() + Duration::from_millis(500);
        loop {
            match self.child.try_wait() {
                Ok(Some(st)) if !st.success() => {
                    std::thread::sleep(Duration::from_millis(20));
                    return EditError::new(
                        "backend_failed",
                        format!("{}: the backend exited with {st}{}", self.what, self.stderr_tail()),
                    );
                }
                Ok(None) if Instant::now() < grace => std::thread::sleep(Duration::from_millis(5)),
                _ => break,
            }
        }
        let _ = self.child.kill();
        EditError::new("backend_output", format!("{}: {m}{}", self.what, self.stderr_tail()))
            .hint("each frame is a JSON header line, then w × h × 4 bytes for a box (spec/components.md, \"Backends\")")
    }

    /// The next frame.
    pub fn next_frame(&mut self) -> Result<RasterFrame, EditError> {
        if self.left <= 0 {
            return Err(EditError::new("backend_output", format!("{}: asked for a frame past the request", self.what)));
        }
        let wait = self.deadline.saturating_duration_since(Instant::now());
        match self.rx.recv_timeout(wait) {
            Ok(Msg::Frame(f)) => {
                self.left -= 1;
                Ok(f)
            }
            Ok(Msg::Bad(m)) => Err(self.bad_output(m)),
            Ok(Msg::Done { .. }) | Err(RecvTimeoutError::Disconnected) => {
                Err(self.bad_output("the output ended early".into()))
            }
            Err(RecvTimeoutError::Timeout) => Err(self.timed_out()),
        }
    }

    /// After the last frame: no trailing output, and a zero exit.
    pub fn finish(&mut self) -> Result<(), EditError> {
        if self.done {
            return Ok(());
        }
        self.done = true;
        let wait = self.deadline.saturating_duration_since(Instant::now());
        match self.rx.recv_timeout(wait) {
            Ok(Msg::Done { extra: 0 }) => {}
            Ok(Msg::Done { extra }) => {
                return Err(self.bad_output(format!("{extra} bytes after the last frame")));
            }
            Ok(Msg::Frame(_)) | Ok(Msg::Bad(_)) | Err(RecvTimeoutError::Disconnected) => {
                return Err(self.bad_output("unexpected output after the last frame".into()));
            }
            Err(RecvTimeoutError::Timeout) => return Err(self.timed_out()),
        }
        self.exit_status()
    }

    /// Frames not read yet.
    pub fn left(&self) -> i64 {
        self.left
    }
}

impl Drop for BackendStream {
    fn drop(&mut self) {
        if !self.done || self.child.try_wait().ok().flatten().is_none() {
            let _ = self.child.kill();
        }
        let _ = self.child.wait();
        let _ = std::fs::remove_dir_all(&self.tmp);
    }
}

/// Reads one frame: `None` at a clean end of output.
fn read_frame(r: &mut impl BufRead) -> Result<Option<RasterFrame>, String> {
    let mut line = Vec::new();
    let n = r.by_ref().take(MAX_HEADER as u64 + 1).read_until(b'\n', &mut line).map_err(|e| e.to_string())?;
    if n == 0 {
        return Ok(None);
    }
    if line.last() != Some(&b'\n') {
        return Err(if line.len() > MAX_HEADER {
            "a header line longer than 64 KiB".into()
        } else {
            "the output ended inside a header".into()
        });
    }
    let text = String::from_utf8_lossy(&line);
    let h: Value = serde_json::from_str(text.trim()).map_err(|e| {
        let shown: String = text.trim().chars().take(80).collect();
        format!("the header isn't JSON ({e}): `{shown}`")
    })?;
    if h.get("same").and_then(Value::as_bool) == Some(true) {
        return Ok(Some(RasterFrame::Same));
    }
    if h.get("empty").and_then(Value::as_bool) == Some(true) {
        return Ok(Some(RasterFrame::Empty));
    }
    let bx = h
        .get("box")
        .and_then(Value::as_array)
        .filter(|a| a.len() == 4)
        .and_then(|a| a.iter().map(|v| v.as_i64()).collect::<Option<Vec<i64>>>())
        .ok_or_else(|| format!("a header is {{\"box\": [x, y, w, h]}} (whole numbers), {{\"same\": true}} or {{\"empty\": true}}, not {h}"))?;
    let (w, hh) = (bx[2], bx[3]);
    if w < 0 || hh < 0 {
        return Err(format!("box {bx:?} has a negative size"));
    }
    let len = (w as u64) * (hh as u64) * 4;
    if len > MAX_FRAME_BYTES {
        return Err(format!("box {bx:?} is larger than a frame can be"));
    }
    let mut rgba = vec![0u8; len as usize];
    r.read_exact(&mut rgba)
        .map_err(|_| format!("box {bx:?} needs {len} bytes of RGBA; the output ended before that"))?;
    if w == 0 || hh == 0 {
        return Ok(Some(RasterFrame::Empty));
    }
    Ok(Some(RasterFrame::Image { bx: [bx[0], bx[1], w, hh], rgba }))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frames_parse() {
        let mut out = b"{\"box\": [1, 2, 2, 1]}\n".to_vec();
        out.extend_from_slice(&[1, 2, 3, 4, 5, 6, 7, 8]);
        out.extend_from_slice(b"{\"same\": true}\n{\"empty\": true}\n");
        let mut r = std::io::Cursor::new(out);
        assert_eq!(
            read_frame(&mut r).unwrap(),
            Some(RasterFrame::Image { bx: [1, 2, 2, 1], rgba: vec![1, 2, 3, 4, 5, 6, 7, 8] })
        );
        assert_eq!(read_frame(&mut r).unwrap(), Some(RasterFrame::Same));
        assert_eq!(read_frame(&mut r).unwrap(), Some(RasterFrame::Empty));
        assert_eq!(read_frame(&mut r).unwrap(), None);
    }

    #[test]
    fn bad_headers_and_short_frames() {
        let e = read_frame(&mut std::io::Cursor::new(b"hello\n".to_vec())).unwrap_err();
        assert!(e.contains("isn't JSON"), "{e}");
        let e = read_frame(&mut std::io::Cursor::new(b"{\"box\": [0, 0, 2, 2]}\nabc".to_vec())).unwrap_err();
        assert!(e.contains("16 bytes"), "{e}");
        let e = read_frame(&mut std::io::Cursor::new(b"{\"box\": [0, 0]}\n".to_vec())).unwrap_err();
        assert!(e.contains("a header is"), "{e}");
        let e = read_frame(&mut std::io::Cursor::new(b"{\"box\"".to_vec())).unwrap_err();
        assert!(e.contains("inside a header"), "{e}");
    }
}
