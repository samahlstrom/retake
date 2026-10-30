# Configuration

`retake` reads one optional file: `$XDG_CONFIG_HOME/retake/config.toml`, which is `~/.config/retake/config.toml` by
default. Every key is optional. Command-line flags override the file. An unknown key is reported as a warning and
otherwise ignored.

```toml
name = "Ana"
port = 8777
idle_exit_minutes = 15
open_browser = true

[transcribe]
engine = "auto"
model = "large-v3"
device = "auto"
command = ""
whisper_cli = ""
```

## Top-level keys

| Key | Default | Meaning |
|---|---|---|
| `name` | your OS user name | The author name written into each take's `session.json`. Notes show it, and `notes.md` names you with it. |
| `port` | `8777` | The port `retake open` tries first. If it's busy, the next free one of the following 19 is used. `--port N` is strict instead: that port or exit code 2. |
| `idle_exit_minutes` | `15` | The server exits this many minutes after the page's last request. It counts only once the page has loaded. `0` means never. Flag: `--idle-exit MIN`. |
| `open_browser` | `true` | Whether `retake open` opens the page in your browser. Flag: `--no-open`. |

The port matters more than it seems: the browser remembers the microphone permission per origin, port included.

## [transcribe]

Transcription turns each recorded segment into words with times. Without it, notes have strokes and times but no
text. The server runs it after Done, and so does `retake notes sync`.

| Key | Default | Meaning |
|---|---|---|
| `engine` | `"auto"` | `auto`: a `command` if one is set, else whisper.cpp. `whisper.cpp`: always whisper.cpp. `command`: always your program. |
| `model` | `"large-v3"` on a GPU, `"small"` on the CPU | A whisper.cpp model name (`tiny`, `base`, `small`, `medium`, `large-v3-turbo`, `large-v3`) or the path of a ggml `.bin` file. With `device = "auto"`, a model larger than `small` falls back to `small` on the CPU. Named models are downloaded on first use and checked against a pinned SHA-256. |
| `device` | `"auto"` | `auto`: CUDA when whisper-cli was built with it and a GPU has room for the model, else the CPU. `cuda` or `cpu` forces one. |
| `command` | `""` | For `engine = "command"`: a program that transcribes one audio file. See below. |
| `whisper_cli` | `""` | The whisper-cli binary. Empty: `whisper-cli` on `PATH`, then the one `scripts/install-whisper.sh` installed. |

**whisper.cpp.** `scripts/install-whisper.sh` builds whisper-cli from a pinned release, with CUDA when `nvcc` is
found. It installs into `$XDG_DATA_HOME/retake/whisper.cpp` (`~/.local/share/retake/whisper.cpp`). Models go to
`$XDG_CACHE_HOME/retake/models` (`~/.cache/retake/models`).

**A command engine.** The program gets the path of a 16 kHz mono WAV, as the last argument or in place of an
`{audio}` argument. It prints the words as JSON on stdout: a list, or `{"words": [...]}`, of
`{"w", "s", "e", "p"}` (or `word`/`text`, `start`, `end`, `probability`/`prob`), with times in seconds from the start
of the file. `RETAKE_PROMPT` and `RETAKE_LANGUAGE` are set in its environment. `scripts/faster-whisper-words.py` is an
example:

```toml
[transcribe]
engine = "command"
command = "python3 /path/to/faster-whisper-words.py {audio}"
```

A segment is transcribed again only when its file, the engine's name or the prompt changed.
`retake transcribe <take folder | audio file>` runs the transcriber by hand, for debugging.

## Environment variables

| Variable | Read by | Meaning |
|---|---|---|
| `XDG_CONFIG_HOME` | retake | Where `retake/config.toml` is. |
| `XDG_DATA_HOME` | retake | Where `scripts/install-whisper.sh` installs whisper.cpp, and where retake looks for it. |
| `XDG_CACHE_HOME` | retake | Where whisper models are downloaded (`retake/models`). |
| `RETAKE_WHISPER_THREADS` | retake | whisper.cpp's CPU threads. |
| `RETAKE_PROMPT`, `RETAKE_LANGUAGE` | your command engine | Set by retake for the program it runs. |
| `RETAKE_VIDEO`, `RETAKE_PROJECT`, `RETAKE_SINCE` | your `retake watch` command | Set by retake for the command it runs ([agents.md](agents.md#retake-watch)). |
| `RETAKE_DIR` | retake-edit | Where the render cache and renders go, instead of `.retake/` next to the timeline. |
| `RETAKE_FFMPEG`, `RETAKE_FFPROBE` | retake-edit | The ffmpeg and ffprobe binaries, instead of the ones on `PATH`. |

## Files retake writes

| Where | What |
|---|---|
| `<video folder>/<video name>.review/<take>/` | One take: audio segments, `session.json`, `segments.json`, `events.json`, `strokes.json`, `voice.json`, images, `transcript.json`. |
| `<video folder>/<video name>.review/notes.md`, `notes.json`, `snaps/` | The notes engine's output for that version. |
| `<project>/.retake/project.db` | The project store: notes, questions, versions, jobs (SQLite). The project is the folder above the `v<N>` folders, or the video's own folder. |
| `<timeline folder>/.retake/` | retake-edit's render cache and renders. |

Retake never changes or deletes your videos. `retake push` copies the new file into the family, or moves it with
`--move`.
