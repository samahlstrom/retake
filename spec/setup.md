# First run: `retake start` and the setup page (v1)

The contract for starting Retake without knowing anything: `retake start` (the Mac app and the Linux app-menu entry
run it) opens your videos folder, or, the first time, a setup page in the browser. Change it here first. The install
side (what ships, per OS) is docs/install-plan.md.

## `retake start`

```
retake start [--port N] [--no-open] [--idle-exit MIN] [--app] [--verbose]
```

- **Your videos folder** is `folder` in config.toml (docs/config.md). When it is set and is a folder, `retake start`
  serves it exactly as `retake open <folder>` does (spec/home.md), at `/`.
- **The first time** (`folder` unset, or not a folder any more), it serves the setup page at `/` instead. When the
  person finishes it, the same address becomes the folder's home without a restart, so the browser keeps the page's
  origin (and its microphone permission).
- **One at a time.** It records itself in `$XDG_STATE_HOME/retake/start.json` (`~/.local/state/retake/start.json`):
  `{"pid", "url"}`. When that URL answers `GET api/ping`, a second `retake start` only opens the browser there (unless
  `--no-open`), prints `retake: already running at <url>` and exits 0. The file is removed on exit.
- Port, `--no-open`, `--idle-exit` and the config keys behind them work as for `retake open`.
- `--app`: started by an app (the Mac app, the Linux menu entry). It reads the login shell's `PATH` first (below),
  and never exits on its own (`--idle-exit 0`) on macOS, where the menu bar has Quit. On Linux, with no menu bar, the
  configured idle exit applies, so closing the page ends it after 15 minutes; opening it again starts it again.
- **From an app bundle.** The `retake` binary run as `Retake.app/Contents/MacOS/Retake` with no arguments (or only
  macOS's `-psn_…`) is `retake start --app`. On macOS it also starts the menu-bar item: `osascript -l JavaScript
  Contents/Resources/menubar.js <pid> <url> <retake>`, stopped when Retake stops. Its menu: **Open Retake** (the
  browser at `<url>`), **Start at login** (a check mark; `retake autostart on|off`), **Quit Retake** (stops Retake;
  agents keep working, docs/agents.md).

`retake autostart [on|off]` turns starting at login on or off and prints `on` or `off` (no argument: just prints it).
macOS: `~/Library/LaunchAgents/io.github.samahlstrom.retake.plist`, which runs `open -a <the app>` at login (or
`retake start --app` when not in an app). Linux: `$XDG_CONFIG_HOME/autostart/retake.desktop` running `retake start
--app`.

## Retake's own tools come first

A release puts `retake`, `retake-edit`, `herdr`, `ffmpeg` and `ffprobe` in one folder with `retake-bundle.json` (in
`Contents/Resources/` of the Mac app). When the `retake` binary (its links resolved) runs from such a folder, it puts
that folder first on its own `PATH` at start, so it, `retake-edit`, the renders and the agents it starts use those
copies (and `retake ask` is found). The person's shell is never changed. Without `retake-bundle.json` nothing
changes.

`retake-bundle.json`: `{"bundle": 1, "version": "<retake version>", "tools": {"herdr": {"version": "0.8.2", "license":
"Apache-2.0", "source": "<url>"}, "ffmpeg": {...}}}`.

herdr is found next to the `retake` binary first (with or without the manifest), then on `PATH`, when an agent opens
in herdr.

**The login shell's `PATH`.** An app started from Finder or a desktop menu gets a short `PATH` (`/usr/bin:/bin:…`),
so `claude` in `~/.local/bin` or `codex` in `/opt/homebrew/bin` would not be found. With `--app`, and on every
**Check again**, Retake runs `$SHELL -ilc` (else `/bin/sh -lc`) to print `PATH` (5 s at most, no input), and makes its
own `PATH`: the bundle folder, the shell's, its own, then these when they exist: `~/.local/bin`, `/opt/homebrew/bin`,
`/usr/local/bin`, `~/.npm-global/bin`, `~/.bun/bin`, `~/.volta/bin`, `~/.claude/local`. Each folder once, in that
order.

## The setup page

One page, three questions, each with a choice made already, and one button. Everything on it can be changed later in
Settings (spec/home.md). Plain words; no paths the person didn't choose, no config keys.

1. **Your videos.** The folder (`~/Movies` on a Mac, else `~/Videos`, which Done makes when it's missing), shown as
   `~/Movies`, and how many videos are in it ("3 videos", "No videos yet, that's fine"). **Change** opens a list of the folder's
   folders, with **Up** and **Use this folder**; it never shows hidden folders.
2. **Your agent.** The agents found (Claude Code, Codex, and the `[[agents]]` of config.toml), the default one
   selected, each with **Test**. Test runs it once with a one-line prompt and says "Works" with how long it took, or
   what's wrong in one line ("Not signed in: run claude once in Terminal"). With no agent: what an agent is in one
   sentence, the official install line for Claude Code and for Codex, each with **Copy**, the sign-in step, and
   **Check again**.
3. **Where agents work.** In herdr (comes with Retake: watch each agent and type to it), in tmux (shown only when
   tmux is installed), or in the background (simplest). The default: herdr when it's there, else the background.

The button, **Open my videos**, saves the three answers and lands on the folder's home. With no agent it still works:
Send then says what's missing, as today. When ffmpeg isn't installed (a build from source), one line says so, with
the install command, since nothing plays without it.

Phone and desktop: one column, at most 640 px wide; buttons at least 44 px tall on a phone.

### Routes (setup only, local addresses only)

The setup server answers on `127.0.0.1:<port>` and `localhost:<port>` only (403 `{"error":"bad host"}` otherwise),
and refuses a POST whose `Origin` is present and isn't its own (403). Every answer is `Cache-Control: no-store`.

| Method | Path | Does |
|---|---|---|
| GET | `/`, `/index.html` | The page (`setup.html`). |
| GET | `/api/ping` | `{"ok": true, "setup": true}` |
| GET | `/api/setup` | The state (below). |
| POST | `/api/setup` | Save any of `{"folder", "agent", "open_in"}`; answers the state. |
| GET | `/api/setup/folders?path=P` | The folders in P (below). |
| POST | `/api/setup/check` | Read the login shell's `PATH` again and find the agents again; answers the state. |
| POST | `/api/setup/test` | `{"agent": "<id>"}`: run the test (below). |
| POST | `/api/setup/done` | Save `{"folder", "agent", "open_in"}` (all optional) and switch to the folder: `{"ok": true}`. |
| GET | `/assets/*`, other page files | The page's files. |

Anything else is 404 `{"error": "not found"}`; `/api/home` is 503 `{"error": "setting up"}`, so a page that waits for
the home after Done can tell.

`GET /api/setup`:

```json
{
  "folder": "~/Movies", "folder_path": "/Users/ana/Movies", "folder_exists": true, "videos": 3,
  "agents": [ { "id": "claude", "name": "Claude Code" } ], "agent": "claude",
  "open_in": "herdr",
  "places": [ { "id": "herdr", "available": true, "bundled": true }, { "id": "tmux", "available": false },
              { "id": "background", "available": true } ],
  "ffmpeg": true, "os": "darwin", "config": "~/.config/retake/config.toml"
}
```

- `folder` is shown with `~`; `folder_path` is the absolute path. Before anything is saved it is the suggestion.
  `videos` counts the videos in it (the home's discovery, stopping at 99; `null` when it isn't a folder).
- `agents` are the agents the worker would offer now; `agent` the saved default when offered, else the first, else "".
- `open_in` is the saved place when it's available, else the default (herdr when available, else background).
- `places`: herdr's `bundled` is true when it's the copy next to `retake`.
- `os` is `darwin`, `linux` or `windows`, so the page shows the right install lines.

`POST /api/setup` and `/api/setup/done` check everything before writing anything (400 `{"error"}` and nothing
written): `folder` must be absolute or start with `~`, and be a folder, or (Done only) one whose parent exists, which
Done then makes; `agent` must be offered ("" means none chosen); `open_in` must be `background`, `herdr` or `tmux`,
and available. They write `folder`, `default_agent` and `open_agents_in` with config.SetTop (the rest of the file
stays as it is). A `folder` under the home folder is written as `~/…`.

`GET /api/setup/folders?path=~/Movies`: `{"path": "~/Movies", "parent": "~", "folders": ["Clips", "Old"]}`, the
folder's non-hidden folders by name (case ignored, at most 500). `parent` is "" at the top of the disk. A path that
isn't a folder is 404. It never lists a file.

### The agent test

`POST /api/setup/test {"agent": "claude"}` runs the agent once in a new empty temporary folder, with its profile's
environment, and answers within 90 s:

```json
{ "ok": true, "reply": "ready", "seconds": 4.2 }
{ "ok": false, "error": "Not signed in: run claude once in Terminal and sign in.", "seconds": 1.1 }
```

| Agent | Command |
|---|---|
| Claude Code (`claude` is the program) | `claude -p --model haiku --tools "" --no-session-persistence --output-format json "<prompt>"` |
| Codex (`codex`) | `codex exec --skip-git-repo-check --ephemeral --sandbox read-only --color never -o <tmp>/reply.txt "<prompt>"` |
| Any other `[[agents]]` | Its `command`, the prompt on stdin, `RETAKE_REPORT` set; it passes when it exits 0. |

The prompt is `Reply with the single word: ready`. `reply` is the answer's first line, at most 200 characters. A
failure's `error` is one plain line: not signed in (the output mentions logging in, `/login`, an API key or
authentication), not found, timed out ("No answer in 90 s"), or the program's last line of output. The test runs
only when the person presses Test, one at a time per agent.

## Settings additions (spec/home.md)

- **Your videos** (top of Settings): the folder. Under `retake start` it has **Change** (the same folder list), and a
  change switches the home to the new folder at once (`POST api/settings {"folder"}`, then the page reloads). Under
  `retake open <folder>` it shows the opened folder, read-only ("Opened with retake open").
- **Test** next to the default agent, as on the setup page (`POST api/settings/test`).
- `GET api/settings` gains `"folder": "~/Movies"` and `"folder_editable": bool`; `GET api/settings/folders?path=` is
  the setup page's folder list.

## Tests

- Go: `folder` in config.toml (SetTop, parse); start.json (a second start opens the first, a stale file is ignored);
  setup routes (host and origin checks, state, save and its checks, folders, Done switching the listener to the home
  on the same port); the agent test with fake `claude`, `codex` and custom agents (works, not signed in, timeout); the
  login-shell `PATH` (a fake `$SHELL`, the order, no duplicates, a shell that hangs); the bundle manifest putting the
  folder first on `PATH`; herdr found next to the binary; `retake autostart` on Linux and macOS files; the app-bundle
  detection.
- Web unit: the setup page's pure parts (install lines per OS, the default place, the test's line).
- e2e `e2e/tests/40-setup.spec.ts`: a scratch HOME with a folder of generated videos and a fake `claude`: the setup
  page shows the folder and the agent, Test says Works, the folder list changes the folder, Open my videos lands on
  the home with the videos; on a phone too. With no agent: the install lines and Check again finding a fake one.
