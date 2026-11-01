# Agents

An agent is any program that reads review notes and makes the next version: Claude Code, Codex, another MCP client,
or a script. Retake gives it the notes, takes its questions, and checks its pushes. How the agent edits the video is
up to it. It can patch a timeline with `retake-edit`, run ffmpeg, or drive another editor.

## Send your notes to an agent

```bash
retake open ~/Videos
```

Open a video, press R, talk and draw over it, and press R again. Then press **Send** (or **Finish review** and
send). Claude Code or Codex on this computer edits the project, the page plays the edit as it renders, and the
comment turns Done when the new version is ready. If the agent is unsure, the comment shows its question; answer it
and the agent carries on. There is nothing else to start: `retake open` runs the editing worker itself, one per
project, named after this computer.

What it needs: a timeline project (`edit/timeline.json`, or `[edit] timeline` in the project's `retake.toml`),
`retake-edit` ([timeline.md](timeline.md)), and Claude Code or Codex installed and signed in. When something is
missing, the Agent panel says what, and it picks the fix up within 15 seconds without a restart.

- One task runs at a time; each Send queues one. The agent works in the project folder with its own sign-in and
  settings.
- While it works, the page follows its drafts (`v4-draft1`, `v4-draft2`, ...). Only a finished task makes a version
  (`v4`), and restarting `retake open` makes none.
- **Start edits when feedback is submitted** only sends finished reviews that nobody sent. Send always works.
- Each task's prompt, context, log and report are in `.retake/worker/tasks/<task>/`.

Reviewing on a hosted server instead? Run `retake connect` and the same worker connects out to it: [hosting.md](hosting.md).

## How the worker runs an agent

The worker runs the agent's command in the project folder, with the task prompt on stdin. `RETAKE_CONTEXT` names a
JSON file with the task and its notes (text, times, strokes, snapshots and images), and the agent writes its answer
to `RETAKE_REPORT`: `{"state": "completed", "message": "what changed"}` or `{"state": "question", "message": "..."}`.
A completed task still waits for a checked, playable render of the agent's final source before the comment is Done.

The built-in agents are these commands, with your installed CLI's own sign-in and model settings:

| id | command |
|---|---|
| `claude` | `claude -p --output-format stream-json --verbose --permission-mode acceptEdits` |
| `codex` | `codex exec --json --sandbox workspace-write -c approval_policy="never" --skip-git-repo-check -` |

The agent inherits retake's environment. An `ANTHROPIC_API_KEY` exported in that shell makes Claude Code bill the key
instead of your subscription ("In non-interactive mode (`-p`), the key is always used when present").

## Use another provider or an API key

Add agents in `~/.config/retake/config.toml` ([config.md](config.md#agents)). Each one shows up in the page's agent
menu. `env` adds variables to the agent's environment, and a value can name one of yours, `$VAR` or `${VAR}`, so the
key stays in your shell profile or secret manager rather than in the file. Start `retake` from a shell where those
variables are set.

**Claude Code through OpenRouter.** OpenRouter's Claude Code guide sets `ANTHROPIC_BASE_URL` to
`https://openrouter.ai/api`, `ANTHROPIC_AUTH_TOKEN` to your OpenRouter key, and `ANTHROPIC_API_KEY` to an explicit
empty string (unset, Claude Code may fall back to Anthropic's servers):

```toml
[[agents]]
id = "claude-openrouter"
name = "Claude Code (OpenRouter)"
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
env = { ANTHROPIC_BASE_URL = "https://openrouter.ai/api", ANTHROPIC_AUTH_TOKEN = "$OPENROUTER_API_KEY", ANTHROPIC_API_KEY = "" }
```

Model choices go in the same `env`, with the variables the guide lists (`ANTHROPIC_DEFAULT_SONNET_MODEL` and so on).

**Claude Code with an Anthropic API key.** Keep the key under another name, so the built-in `claude` agent stays on
your subscription and this one bills the key:

```toml
[[agents]]
id = "claude-api"
name = "Claude Code (API key)"
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
env = { ANTHROPIC_API_KEY = "$RETAKE_ANTHROPIC_API_KEY" }
```

**Codex through OpenRouter.** Codex keeps providers in its own `~/.codex/config.toml`. Add OpenRouter there as
OpenRouter's Codex guide shows (`wire_api = "responses"` is required), but leave your default provider alone:

```toml
[model_providers.openrouter]
name = "OpenRouter"
base_url = "https://openrouter.ai/api/v1"
wire_api = "responses"

[model_providers.openrouter.auth]
command = "sh"
args = ["-c", "echo $OPENROUTER_API_KEY"]
```

Then a retake agent picks that provider and an OpenRouter model slug for its tasks only:

```toml
[[agents]]
id = "codex-openrouter"
name = "Codex (OpenRouter)"
command = ["codex", "exec", "--json", "--sandbox", "workspace-write", "-c", "approval_policy=\"never\"",
  "-c", "model_provider=\"openrouter\"", "-c", "model=\"openai/gpt-5.6-sol\"", "--skip-git-repo-check", "-"]
```

`find_agents = false` hides the agents found on `PATH`, so only yours are offered.

## Drive an agent yourself (MCP and the CLI)

The rest of this page is for running your own agent loop: the agent reads the notes over MCP or the CLI and pushes
each new version itself. The contract is [spec/notes.md](../spec/notes.md).

## The loop

1. The reviewer records a take on a version and presses Done.
2. The server transcribes the take and runs the notes engine. Each new note is `open`.
3. The agent reads the notes (`notes_list`), or blocks until there are new ones (`notes_wait`).
4. For each open note, the agent either changes the video or asks the reviewer a question (`note_ask`).
5. The agent pushes the new version (`version_push`). The push lists each answered note with a one-line summary of
   the change, and it can ask questions too.
6. The reviewer opens the new version. Its sidebar shows the agent's open questions.
7. The reviewer answers a question by recording (the **Answer** button). The answer reopens the note, and
   `notes_wait` returns it.
8. Back to step 3.

The agent never approves anything. Approving a version is the reviewer's action.

## What a note looks like

`retake notes list <video> --json` and the `notes_list` tool return the notes with everything the agent needs:

```json
{
  "id": 1,
  "key": "2026-01-15_10-00-00/S1/1",
  "version": "v2",
  "t0": 11, "t1": 11,
  "text": "The labels at the bottom are too small, make them bigger, please.",
  "author": "Sample Reviewer",
  "status": "open",
  "strokes": [ { "shape": "circle", "color": "red", "box": [0.011, 0.833, 0.446, 0.14] } ],
  "images": [],
  "snapshot": "/…/how-to/v2/how-to_v2.review/snaps/01_00-11-0.png",
  "threads": []
}
```

- `t0` and `t1` are seconds in that version. A note said while the video was paused has `t0 == t1`.
- A stroke's `box` is `[x, y, w, h]`, normalized 0–1 to the frame, with (0, 0) at the top left.
- `snapshot` is a PNG of the frame with the strokes drawn on it and numbered. A multimodal agent should look at it.
- `threads` holds the questions about the note and the reviewer's answers.
- The engine also writes `<video>.review/notes.md` for each version: the same notes as readable text, with rules for
  applying them (later statements override earlier ones, and "scrap that" retracts what came before).

## Note states

| State | Means | Next |
|---|---|---|
| `open` | Waiting for the agent. | `answered` by a push that lists it, `question` when the agent asks. |
| `question` | The agent asked about it. | `open` again when the reviewer answers. |
| `answered` | A pushed version changed it. | `accepted` when the reviewer approves that version, `open` when they reopen it. |
| `accepted` | Done. | |
| `retracted` | The reviewer took it back ("scrap that"). | `open` on a reinstatement ("actually, add that back"). |

## The push rule

A push is refused unless every note that's open on the parent version is either in the changelog or asked about in
the same push. Notes carried over to the parent from an older version count too. So nothing the reviewer said is
silently dropped.

```
$ retake push how-to/v3/how-to_v3.mp4 render.mp4 --parent v3
retake push: push rejected: note #2 is open on v3; list each in the changelog (what this version changes for it) or ask the reviewer about it in the same push
```

With `--json`, the refusal names the notes:

```json
{ "code": "push_rule", "error": "push rejected: note #2 is open on v3; …", "notes": [2] }
```

Fix it with `--change 2="what changed"`, or `--ask 2="your question"`, or ask first with `retake ask`.

## Connect Claude Code

```bash
claude mcp add retake -- retake mcp /path/to/how-to/v2/how-to_v2.mp4
```

The video can be any version of the family. Add `--scope project` to put the server in the folder's `.mcp.json`, so
everyone who works in that folder gets it. Then ask Claude Code to answer the notes, for example:

> Answer the open review notes on how-to_v2.mp4. Read each note's snapshot. Change the timeline only through
> retake-edit patches, render, and push the result as the next version with a changelog line per note. Ask me about
> any note you're unsure of instead of guessing.

## Connect Codex

Add the server to `~/.codex/config.toml`:

```toml
[mcp_servers.retake]
command = "retake"
args = ["mcp", "/path/to/how-to/v2/how-to_v2.mp4"]
```

## Connect any MCP client

`retake mcp <video>` speaks MCP over stdin and stdout. Most clients take a JSON entry like this:

```json
{ "mcpServers": { "retake": { "command": "retake", "args": ["mcp", "/path/to/how-to/v2/how-to_v2.mp4"] } } }
```

## The tools, and the same thing on the command line

Agents that run shell commands can use the CLI instead of MCP. Every command takes `--json`.

| MCP tool | CLI | Does |
|---|---|---|
| `notes_list` | `retake notes list <video> [--status open] [--version vN]` | The notes, and a `cursor` for `notes_wait`. |
| `notes_wait` | `retake notes wait <video> [--since CURSOR] [--timeout S]` | Blocks until notes open or answers arrive after the cursor. Cursor 0 returns every open note at once. |
| `note_ask` | `retake ask <video> <note> "question" [--option vN]...` | Asks the reviewer about a note. Note 0 asks about the version as a whole. |
| `note_set` | `retake note <video> <id> --status answered --in vN --change "…"` | Marks a note answered by a version that's already in the family. |
| `version_push` | `retake push <video> <file> --parent vN --change ID="…"... --ask ID="…"...` | Adds the file to the family as the next `v<N>`, with its changelog. |
| `version_push` with `render: true` | `retake push <video> --render [--draft] --parent vN ...` | In a timeline project: renders the timeline and pushes that; each patch's `answers` and `summary` become the changelog ([timeline.md](timeline.md#editing-with-the-timeline)). |
| `versions_list` | `retake versions <video>` | The versions, their parents and changelogs. |
| `component_list` | `retake lib list` | Components and kits the project can use, which the timeline uses, whether they're locked. |
| `component_describe` | `retake lib describe <name>` | Props schema, defaults, examples, the storybook sheet, the changelog and a use to patch in. |
| `lib_search` / `lib_add` | `retake lib search "…"` / `retake lib add <name>` | Finds packages in a registry and the user library; adds one to the project, pinned in the lockfile. |
| `lint` | `retake lib lint` (`retake-edit lint`) | The timeline against its kits' house rules: typed violations with times and hints. |

Two more commands help outside the loop:

- `retake notes sync <video>` transcribes what needs it, runs the notes engine and updates the store. The server does
  this after Done; run it by hand when no server is running.
- `retake notes build <video>` only runs the notes engine and writes `notes.md` and `notes.json`.

## retake watch

`retake watch` runs a command each time new notes or answers land, so an agent can work without being asked:

```bash
retake watch how-to/v2/how-to_v2.mp4 --exec 'claude -p "New review notes on $RETAKE_VIDEO. Answer or ask about every open note, then push."'
```

- The command runs through the shell, with `RETAKE_VIDEO` (the video), `RETAKE_PROJECT` (the project folder) and
  `RETAKE_SINCE` (the cursor before what triggered it) set.
- It waits until things have been quiet for 2 seconds (`--debounce S`), so one take triggers one run.
- Runs never overlap. What lands during a run triggers the next one.
- `--since CURSOR` skips what came before that cursor. Ctrl+C stops it.

For Codex, use `codex exec "…"` as the command. The agent needs the MCP server configured as above, or it can use the
CLI.

## Instructions to give your agent

Put something like this in the project's `AGENTS.md` or `CLAUDE.md`:

```markdown
## Review notes (retake)
- Read open notes with `retake notes list <video> --json`, or the retake MCP tools. Look at each note's snapshot.
- Later notes override earlier ones. A retracted note is void.
- Change the video for each note, or ask the reviewer with `retake ask` when the note is unclear. Don't guess.
- Push with `retake push <video> <file> --parent vN --change ID="what changed"` for every note you answered.
  A push that leaves an open note out is refused.
- Never approve a version; that's the reviewer's job.
```
