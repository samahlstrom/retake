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
| `find_agents` | `true` | Whether the editing worker offers Claude Code and Codex found on your `PATH`, besides `[[agents]]`. |
| `open_agents_in` | `"background"` | Where agents run. `background`: headless, you see the result. `herdr`: each video's agent is an interactive session in its own herdr workspace. `tmux`: a window per video in a tmux session `retake`. You can watch and type into those. [agents.md](agents.md#watch-and-talk-to-your-agents-herdr-tmux). |

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

## [[agents]]

The programs the editing worker may run when you press Send: `retake open` and `retake share` use them for every
timeline project they serve, and so does `retake worker` (unless `--profiles FILE` is given). Claude Code and Codex
found on `PATH` are added on their own with their own sign-in. An entry with the id `claude` or `codex` replaces
that one, so you can change how it runs without listing the other.

```toml
[[agents]]
id = "claude"                    # letters, digits, - and _; shown in the page's agent menu by name
name = "Claude Code (API key)"
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
env = { ANTHROPIC_API_KEY = "$ANTHROPIC_API_KEY" }
```

| Key | Meaning |
|---|---|
| `id` | Unique. Tasks and the page's agent menu refer to it. |
| `name` | What the page shows. Default: the id. |
| `model` | Optional, shown next to the name. It doesn't change the command. |
| `command` | The program and its arguments. It gets the task prompt on stdin and runs in the project folder. |
| `env` | Extra environment variables. A value may name your own environment's variables, `$VAR` or `${VAR}`, so keys stay out of this file. `""` sets a variable to empty. |
| `interactive` | The program and arguments when it opens in herdr or tmux: an interactive session, no `-p` or `exec`. In tmux it gets the task's line as its last argument; herdr starts the program by its name (`claude`, `codex`, ...) with these arguments. Without it, the agent runs in the background. |
| `open_in` | `background`, `herdr` or `tmux`, for this agent only. Default: `open_agents_in`. `herdr` and `tmux` need `interactive`. |

The agent also gets `RETAKE_CONTEXT` and `RETAKE_REPORT` ([agents.md](agents.md#how-the-worker-runs-an-agent)).
[agents.md](agents.md#use-another-provider-or-an-api-key) has OpenRouter and API-key examples.

## [server]

The settings of `retake serve`, the team server, and of its admin commands (`retake user`, `retake project`). Flags
win over the `RETAKE_*` environment variables, which win over this table. [spec/server.md](../spec/server.md) has
the details, and [deploy/README.md](../deploy/README.md) a Docker Compose setup.

```toml
[server]
data = "/srv/retake"                     # RETAKE_DATA, --data: required
listen = ":8080"                         # RETAKE_LISTEN, --listen
public_url = "https://review.example.com" # RETAKE_PUBLIC_URL, --public-url
trusted_proxies = ["127.0.0.1"]          # RETAKE_TRUSTED_PROXIES (comma-separated), --trust-proxy
database = ""                            # RETAKE_DATABASE_URL, --database: "" = SQLite, or postgres://...
s3_endpoint = ""                         # RETAKE_S3_ENDPOINT, --s3-endpoint: "" = local files, or host:port
s3_bucket = "retake"                     # RETAKE_S3_BUCKET, --s3-bucket
s3_access_key = ""                       # RETAKE_S3_ACCESS_KEY
s3_secret_key = ""                       # RETAKE_S3_SECRET_KEY
s3_region = "us-east-1"                  # RETAKE_S3_REGION, --s3-region
s3_insecure = false                      # RETAKE_S3_INSECURE=1, --s3-insecure: plain HTTP to the S3 endpoint
smtp_url = ""                            # RETAKE_SMTP_URL: smtp://user:pass@host:587 (STARTTLS) or smtps://...:465
smtp_from = ""                           # RETAKE_SMTP_FROM, --smtp-from: default retake@<public host>
relay_cache = "20GB"                     # RETAKE_RELAY_CACHE, --relay-cache: relayed video kept on the server; "0" = none
```

Without `public_url` any Host is accepted and the CLI makes links for `http://localhost:<port>`; set it in production.
Forwarding headers (`X-Forwarded-For`, `-Proto`, `-Host`) are believed only from `trusted_proxies`.

`retake remote add` keeps remotes (a project URL and an agent token) in `$XDG_CONFIG_HOME/retake/remotes.toml`, readable
only by you. `retake push --remote` remembers unfinished uploads in `$XDG_STATE_HOME/retake/uploads.json`
(`~/.local/state/retake/uploads.json`), so the next push of the same file resumes them.

## Environment variables

| Variable | Read by | Meaning |
|---|---|---|
| `XDG_CONFIG_HOME` | retake | Where `retake/config.toml` is. |
| `XDG_DATA_HOME` | retake | Where `scripts/install-whisper.sh` installs whisper.cpp, and where retake looks for it. |
| `XDG_CACHE_HOME` | retake | Where whisper models are downloaded (`retake/models`). |
| `RETAKE_WHISPER_THREADS` | retake | whisper.cpp's CPU threads. |
| `RETAKE_DATA`, `RETAKE_LISTEN`, `RETAKE_PUBLIC_URL`, `RETAKE_TRUSTED_PROXIES`, `RETAKE_DATABASE_URL`, `RETAKE_S3_*`, `RETAKE_SMTP_URL`, `RETAKE_SMTP_FROM`, `RETAKE_RELAY_CACHE` | retake serve, user, project | The team server's settings ([server] above). |
| `RETAKE_TOKEN` | retake remote add | The agent token, when `--token` isn't given. |
| `XDG_STATE_HOME` | retake push --remote | Where unfinished uploads are remembered (`retake/uploads.json`). |
| `RETAKE_PROMPT`, `RETAKE_LANGUAGE` | your command engine | Set by retake for the program it runs. |
| `RETAKE_CONTEXT`, `RETAKE_REPORT` | your agent | Set by the editing worker for the agent it runs in the background: the task's context file and where to write its report. |
| `RETAKE_VIDEO`, `RETAKE_PROJECT`, `RETAKE_SINCE` | your `retake watch` command | Set by retake for the command it runs ([agents.md](agents.md#retake-watch)). |
| `RETAKE_DIR` | retake-edit | Where the render cache and renders go, instead of `.retake/` next to the timeline. |
| `RETAKE_FFMPEG`, `RETAKE_FFPROBE` | retake-edit | The ffmpeg and ffprobe binaries, instead of the ones on `PATH`. |

## Files retake writes

| Where | What |
|---|---|
| `<video folder>/<video name>.review/<take>/` | One take: audio segments, `session.json`, `segments.json`, `events.json`, `strokes.json`, `voice.json`, images, `transcript.json`. |
| `<video folder>/<video name>.review/notes.md`, `notes.json`, `snaps/` | The notes engine's output for that version. |
| `<project>/.retake/project.db` | The project store: notes, questions, versions, jobs (SQLite). The project is the folder above the `v<N>` folders, or the video's own folder. |
| `<project>/.retake/worker/` | The editing worker's files: its identity and lock, each task's prompt, context, log and report (`tasks/`), the herdr or tmux session it uses (`view.json`), and each render's snapshot and video (`renders/`). Keep `renders/` for older versions to stay playable. |
| `<timeline folder>/.retake/` | retake-edit's render cache and renders. |
| `<data>/server.db`, `<data>/projects/<name>/`, `<data>/uploads/` | `retake serve`: the server store (SQLite), each project's workspace (a retake project folder), and uploads in progress. |

Retake never changes or deletes your videos. `retake push` copies the new file into the family, or moves it with
`--move`.
