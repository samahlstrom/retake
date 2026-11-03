# Agents

An agent is any program that reads review notes and makes the next version: Claude Code, Codex, another MCP client,
or a script. Retake gives it the notes, takes its questions, and checks its pushes. How the agent edits the video is
up to it. It can patch a timeline with `retake-edit`, run ffmpeg, or drive another editor.

## Send your notes to an agent

```bash
retake open ~/Videos
```

Open a video, press R, talk and draw over it, and press R again. Then press **Send to agent**. Claude Code or Codex on this computer edits the project, the page plays the edit as it renders, and the
comment turns Done when the new version is ready. If the agent is unsure, the comment shows its question; answer it
and the agent carries on. There is nothing else to start: `retake open` runs the editing worker itself, one per
project, named after this computer.

**The agent has full access to this computer.** It runs unattended, so it never stops to ask permission: Claude
Code runs with `--dangerously-skip-permissions` and Codex with `--dangerously-bypass-approvals-and-sandbox`, in the
background and in herdr or tmux alike. It can run any command, read and write any file your user can, and use the
network, with no sandbox. Run retake as a normal user (Claude Code refuses that flag as root), and only send notes
from people you'd let use your computer that way. To keep tighter permissions, add your own agent in
[config.md](config.md#agents) with the flags you want; that one may stop at a prompt nobody answers.

What it needs: Claude Code or Codex installed and signed in. A timeline project (`edit/timeline.json`, or
`[edit] timeline` in the project's `retake.toml`) also needs `retake-edit` ([timeline.md](timeline.md)). When
something is missing, one line over Send says what, and it picks the fix up within 15 seconds without a
restart.

A folder of finished videos with no timeline (`clip_v1.mp4`, `clip_v2.mp4`) works too: the agent makes the change
however it can (ffmpeg, say) and writes one video file, and Retake adds it as the next version (`clip_v3.mp4`, next
to the others). The comment turns Done once that video plays.

- **Comments on vN become edits in vN+1.** Comment on `v7` and the agent makes `v8`. Comment on `v7` again, while
  `v8` is being made or after it's done, and the agent edits `v8`: it updates in place, still `v8`. Comment on `v8`
  and you get `v9`. Pressing Send never makes a version by itself. If you comment on `v7` when `v9` already exists,
  the change still goes into `v8`, and the comment says that `v9` was made before it.
- One agent per video. Everything you send goes to it, one target version at a time (a Send with comments on `v7` and
  `v8` updates `v8`, then makes `v9`). Comments for the version it's working on join its work: in herdr or tmux as a
  new message in its session, in the background as a follow-up round of the same agent once its run ends.
- The agent works the comments one at a time and says so as it goes (`retake working`, `retake done`, below): the one
  it's on has a soft light sweeping across it and a line in its own words, and each moves to Done as soon as its edit
  is in. A slow red light runs around the video's edge while it works.
- Each video (project) has its own agent. Different videos edit at the same time, so ten videos with work means ten
  agents working at once. The agent works in the project folder with its own sign-in and settings.
- The version it's making is live from the start: `v4` shows up at once (at first it's the version your comments were
  on) and updates in place as each comment's edit goes in, so you can watch every change as it lands. Tap a Done
  comment's "in v4" to see it at that moment. Comments you make on the live `v4` go into `v5`. When the task finishes,
  `v4` is final; restarting `retake open` makes nothing new. (Under the hood each update is a revision, `v4-draft2`
  and so on, playable by its URL.)
- **Which agent.** The default agent is in Settings. To use another one for one video, open the video's ⋯ (its card,
  or the top left of its page) and pick it under **Agent for this video**; **Use my default** goes back. To use
  another one for a single send, hold Send and pick it. A held Send wins over the video's agent, which wins over the
  default.
- Anyone you share the review with (`retake share`) has the same Send button, and their comments go to this agent.
- Each task's prompt, context, log and report are in `.retake/worker/tasks/<task>/` (no log in herdr or tmux: the
  session is the log).

## Restarting or updating Retake never stops your agents

An agent's work is just files: its prompt and report in the task's folder, and the project it edits. So you can quit,
restart or update Retake (Ctrl+C, a crash, `--idle-exit`, a new build) whenever you like, and the agents keep working:
herdr and tmux sessions stay open, and background agents and chat replies keep running on their own.

When Retake starts again it picks each one back up: the same task, no new prompt. A comment that was Working stays
Working, and if the agent finished while Retake was down, its report is read then (the new version is made, the
comments turn Done). If its agent is gone with no report, the comment shows Paused, with Retry. On a hosted review the
comment being worked on says "Reconnecting to the agent" while your computer is away; after 10 minutes it shows Paused, and the
agent is still picked back up when your computer reconnects.

Only you stop an agent: Pause or switching agents on a comment, Stop in the chat, or taking all of its comments back.

## Chat with the agent about a script

On a script (Scripts, v0 of a video), the column beside the document is a chat with the same agent: ask it to shape
the script, pull in a link ("put this video under the cold open: https://…"), find or make an image, tighten the
hook. It edits the document while you watch and answers in a few plain words, like a person replying. What it's doing
shows as one quiet line over its reply that changes as it goes ("Reading youtube.com/…", "Editing the script…") and,
when it's done, sums up ("Edited the script · ran 3 commands"). When it wants you to choose, it asks with choices to
tap, pictures side by side. Drag the column's left edge to make the chat wider. Press R (or the mic) to say it instead of typing; images you paste, drop or attach go in `v0/images/`. ⋯ →
"Undo the agent's edit" undoes a reply's edit. When you like the script, press **Send**: the agent builds v1 from it
(or, once cuts exist, brings the next version in line with the script's changes).

- Each message is one run of the agent in the video's folder, on this computer (or the connected one, hosted). Claude
  Code runs as `claude -p --output-format stream-json --verbose --include-partial-messages --dangerously-skip-permissions
  --disallowedTools=AskUserQuestion`, then with `--resume <session>` for the next message, so it remembers the chat;
  Codex runs `codex exec --json …` and then `codex exec resume … <thread> -`. Your own `[[agents]]` run once per message
  with the recent chat in the prompt. It is always in the background, even with `open_agents_in` set.
- Messages you send while it works wait for its next turn. **Stop** (the square in place of the send arrow) stops
  that reply's process only.
- One agent per video: while Send's build of v1 runs, a chat message waits ("The agent is busy with v1's edits"), and
  the build waits while the chat replies.
- The chat is kept with the review's data (`.retake/` on the server), never in `v0/`; its instructions are the Scripts
  extension's plus the chat's own rules (spec/chat.md).

## When the agent is unsure

The agent never asks in its terminal. When a note is unclear, it asks about the moment of the video it's unsure of,
and carries on with the rest of its work. It can draw to show what it means:

```bash
retake ask --note 12 --from 1:20 --to 1:26 --at 1:23 \
  --box 0.1,0.6,0.4,0.3:"this logo" --arrow 0.8,0.2,0.55,0.45:"or this one?" \
  --option "Old logo@old.png" --option "New logo@new.png" \
  "Which logo should stay here?"
```

The question shows on the review page under comment 12 (which turns Question), with a still of that frame and the
agent's drawing on it, and as a marker on the timeline at 1:23. Click the marker to jump there: the agent's boxes,
arrows and images show on the video, dashed and in the Question violet with an "Agent" chip, so you never mistake them
for your own pen. Answer under the comment: tap a choice, type, or answer out loud (and draw over the video while you
do, as in any recording). The answer goes back to the same task:

- In herdr or tmux, if the agent is still working, it gets the answer as a new message in its session: "Answer to
  your question on note 12 at 1:23: New logo". If it had already finished, the task starts again in the same session
  with the answer.
- In the background, the task starts again with the answer in its context once the agent's current run ends.

A task can finish with some notes Done and others still waiting on their answer. The command needs no video or
token: the worker gives each task its own local address and secret (`RETAKE_ASK_URL` and `RETAKE_ASK_TOKEN`, or
`.retake/worker/ask.json` in herdr and tmux), and passes the question on to the review, images and all, so it works
the same with a hosted server.

| Flag | Means |
|---|---|
| `--note ID` | The comment it's about. Can be left out when the task has one. |
| `--at T`, `--from T --to T` | The moment, and the shot: seconds (`83.5`) or `m:ss`, on the version the note was made on (`--version v4-draft2` for a draft). |
| `--box X,Y,W,H[:LABEL]` | A box, from 0 to 1 from the top left of the frame (`--px` for pixels). Up to 10. |
| `--arrow X1,Y1,X2,Y2[:LABEL]` | An arrow from the first point to the second. Up to 10. |
| `--draw FILE` | Freehand lines: `[{"points": [[x, y], ...]}]`, like a take's `strokes.json`. |
| `--image FILE[@X,Y,W,H]` | A PNG, JPEG, WebP or GIF (8 MiB at most): placed on the frame with a box, else shown in the card. Up to 4. |
| `--option TEXT[@FILE]` | A choice to answer with one tap, with an optional image. Up to 6. |

A background agent that writes `{"state": "question", ...}` as its report still works too.

## One comment at a time

The task prompt asks the agent to work its comments one at a time and to say so, through the same local address as
`retake ask`:

```bash
retake working 12 "slowing the mask fade"     # the comment it's on now, in a few words
# ... the edit (a project without a timeline: write the whole video so far to $RETAKE_OUTPUT) ...
retake done 12 "Slowed the mask fade to 1.2 s"  # what changed
```

`done` puts the edit in the version being made before the comment shows Done: in a timeline project once a render has
it (only the changed parts render, so that's quick), in a project without one once the video so far is copied in as
the version's newest revision. A comment shows Done with these words unless the final report says something else
for it. The report still decides: a comment the agent asked about stays Question, and if the task pauses, the
comments go back to To do.

Reviewing on a hosted server instead? Run `retake connect` and the same worker connects out to it: [hosting.md](hosting.md).

## Watch and talk to your agents (herdr, tmux)

By default the agents run in the background, and you only see what they did. To watch each one work and type into it
whenever you like, open them in [herdr](https://herdr.dev) or tmux instead. In `~/.config/retake/config.toml`:

```toml
open_agents_in = "herdr"   # or "tmux"; "background" is the default
```

The setup page of `retake start` asks this the first time (spec/setup.md), and Settings changes it. herdr comes with
the Retake app and the Linux install: Retake uses the copy next to its own binary first, then the one on `PATH`. Run
`herdr` in a terminal to watch.

Each video then gets its own interactive Claude Code or Codex session (not `claude -p` or `codex exec`), named after
the video:

- **herdr**: a workspace labelled `retake · item-shop` in the herdr session you started `retake` from (or herdr's
  default one). It opens without taking focus or switching your view.
- **tmux**: a window `item-shop` in a tmux session `retake`, made detached. `tmux attach -t retake` to look.

Retake writes the task prompt to `.retake/worker/tasks/<task>/prompt-<n>.md` and sends the agent one line: "New Retake
task for item-shop (task …, attempt 1): follow the instructions in …/prompt-1.md". From there it's a normal session:
watch it, answer it, interrupt it, ask it something. When the agent writes its report, the comment moves on exactly as
in the background (a completed report still waits for a checked render), and the session stays open so you can scroll
back and keep talking. The video's next task goes to the same session if it's still there. Comments sent while the
agent works arrive there too, as one more message for the same task ("More comments for Retake task …"), with a new
report file that covers them all, and they go in the same version.

What's different from the background:

- Retake reads nothing from the terminal. Only the report file finishes a task. The agent is told to ask with
  `retake ask` (above), never in the terminal. If it asks there anyway, or hits a usage limit, the comment stays
  Working until it reports; answer it there, or pause the comment from the page.
- Closing the session (or quitting the agent) before it reports pauses the comment. Pausing or switching agents from
  the page closes the agent's pane. After the report, the pane is left alone. Quitting or restarting Retake never
  closes it.
- No usage or cost is recorded: an interactive session has no JSON stream to read it from.
- The agent's environment is herdr's or tmux's, plus the agent's `env` from your config. `RETAKE_CONTEXT` and
  `RETAKE_REPORT` aren't set; the prompt names both files.
- The first time an agent runs in a folder it may ask whether to trust it, and the first time Claude Code runs with
  `--dangerously-skip-permissions` on this computer it asks you to accept bypass-permissions mode (once). In herdr
  Retake waits for you to answer that in the workspace before it sends the task, and says so in its log. In tmux the
  task is the agent's first message, so it starts once you've answered (`tmux attach -t retake`). Answering "no"
  quits the agent, which pauses the comment.
- An agent from `[[agents]]` needs an `interactive` command to open in herdr or tmux; without one it stays in the
  background ([config.md](config.md#agents)). In herdr, the program's name must be one herdr knows (`claude`, `codex`,
  `gemini`, ...), because herdr starts it by that name.

The built-in agents run as `claude --dangerously-skip-permissions --disallowedTools=AskUserQuestion` and
`codex --dangerously-bypass-approvals-and-sandbox`, the same permissions as in the background. A `[[agents]]`
entry can set `open_in` to put just that agent somewhere else.

These are the commands Retake runs. In herdr, for a video's first task (`<name>` is `retake-<video>-<4 hex>`):

```
herdr workspace create --cwd <project> --label "retake · <video>" --no-focus [--env KEY=VALUE ...]
herdr agent start <name> --kind claude --pane <pane> --timeout 60000 -- --dangerously-skip-permissions --disallowedTools=AskUserQuestion
herdr agent prompt <name> "New Retake task for <video> (...): follow the instructions in <prompt file>"
herdr agent get <name>        # every 2 s until the report: is the session still there?
herdr agent prompt <name> "Answer to your question on note 12 at 1:23: ..."   # an answer while it works
herdr agent prompt <name> "More comments for Retake task ... : follow the instructions in <prompt file>"   # sent while it works
herdr pane close <pane>       # only on pause or switch, before the report
```

The next task checks `herdr pane get <pane>` and `herdr agent get <name>`, then sends `herdr agent prompt` again. If the
agent had quit, it runs `herdr agent start` in the same pane first; if the pane is gone, `herdr tab create --workspace
<workspace> ...` (or a new workspace when that's gone too). A startup question makes `agent start` answer
`agent_not_ready`, and Retake runs `herdr agent wait <name> --until idle --until done` before the prompt. After every
prompt Retake waits up to 15 s for the agent to start working, and sends the line once more if it's still idle.

In tmux:

```
tmux has-session -t =retake
tmux new-session -d -s retake -P -F '#{pane_id}' -n <video> -c <project> [-e KEY=VALUE ...] -- claude --dangerously-skip-permissions --disallowedTools=AskUserQuestion "<line>"
    (or: tmux new-window -d -t =retake: -P -F '#{pane_id}' -n <video> -c <project> ... when the session exists)
tmux set-option -w -t <pane> remain-on-exit on
tmux list-panes -a -F '#{pane_id} #{pane_dead}'    # every 2 s until the report
tmux send-keys -t <pane> -l "<line>"; tmux send-keys -t <pane> Enter    # the next task, or an answer, into the running agent
tmux respawn-pane -k -t <pane> -c <project> ... -- <agent> "<line>"     # the next task when the agent had quit, or another agent
tmux kill-pane -t <pane>                                               # pause or switch
```

## How the worker runs an agent

In the background, the worker runs the agent's command in the project folder, on its own (so it outlives Retake), with
the task prompt on stdin and its output in the task's `agent-<n>.log`. `RETAKE_CONTEXT` names a
JSON file with the task and its notes (text, times, strokes, snapshots and images; a note's `missing` says which of
its files couldn't be given and what the agent has instead, such as a still the worker made itself), and the agent
writes its answer to `RETAKE_REPORT`: `{"state": "completed", "message": "what changed"}` or `{"state": "question", "message": "..."}`.
A completed report may add what changed for each comment, `"notes": {"12": "Cut the intro to 3 s"}`; a comment
without an entry gets the message.
`RETAKE_ASK_URL` and `RETAKE_ASK_TOKEN` are what `retake ask` uses to reach the worker (above).
A completed task still waits for a checked, playable render of the agent's final source before the comment is Done.
In a project without a timeline, `RETAKE_OUTPUT` names the file the agent writes its video to
(`.retake/worker/tasks/<task>/output.mp4`), and the context lists the versions under `family`; the worker moves that
file in as the next version once it plays.

The built-in agents are these commands, with your installed CLI's own sign-in and model settings:

| id | command |
|---|---|
| `claude` | `claude -p --output-format stream-json --verbose --dangerously-skip-permissions --disallowedTools=AskUserQuestion` |
| `codex` | `codex exec --json --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check -` |

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
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"]
env = { ANTHROPIC_BASE_URL = "https://openrouter.ai/api", ANTHROPIC_AUTH_TOKEN = "$OPENROUTER_API_KEY", ANTHROPIC_API_KEY = "" }
```

Model choices go in the same `env`, with the variables the guide lists (`ANTHROPIC_DEFAULT_SONNET_MODEL` and so on).

**Claude Code with an Anthropic API key.** Keep the key under another name, so the built-in `claude` agent stays on
your subscription and this one bills the key:

```toml
[[agents]]
id = "claude-api"
name = "Claude Code (API key)"
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"]
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
command = ["codex", "exec", "--json", "--dangerously-bypass-approvals-and-sandbox",
  "-c", "model_provider=\"openrouter\"", "-c", "model=\"openai/gpt-5.6-sol\"", "--skip-git-repo-check", "-"]
```

`find_agents = false` hides the agents found on `PATH`, so only yours are offered.

To open one of these in herdr or tmux too (`open_agents_in` above), give it `interactive`, the same command without the
headless flags, for example `interactive = ["claude", "--dangerously-skip-permissions"]` for the Claude Code ones.

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
  The project store keeps it relative to the project folder, so moving or copying the folder keeps it.
- A stroke may also have `points` (the line as drawn, up to 64, normalized like `box`) and `t` (the video time it was
  drawn at).
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
