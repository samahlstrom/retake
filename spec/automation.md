# Hosted editing workers and live output

Retake owns durable tasks and review state. A designated machine connects outward to a project on the server,
executes locally configured agents, and serves its renderer's output through an authenticated HTTP relay. Source,
footage, agent credentials and render caches stay on that machine. The host does not require a completed media
upload or an inbound connection to the worker. This is an addition to the existing CLI/MCP integrations.

## Local mode

- `retake open <folder>`, `retake open <video>` and `retake share` run the editing worker inside the same process,
  one per project they serve. There is no token, remote or second command: the worker calls its project's
  `/api/worker/*` handlers in memory as the agent role, and its media goes through the same relay in memory. Its
  name is the machine's hostname. Its agents are the user config's `[[agents]]` plus Claude Code and Codex found
  on `PATH` (docs/config.md). The first poll selects it and its first agent, as for any first worker.
- A project that can't be edited doesn't fail the server. The worker doesn't start, and `GET /api/automation` says
  why in `blocked`, one short sentence for the page: `retake-edit` not found (a timeline project), no agent found
  ("No agent found. Install Claude Code or Codex."), or another worker already holds the project's lock. It checks
  again every 15 seconds, so installing the missing piece needs no restart. A project without a timeline isn't
  blocked: its worker runs as a plain project's ("Plain projects" below).
- `retake worker --remote` (and a hosted server) keep the remote model below. One project folder is edited by one
  worker at a time: a local and a remote worker on the same folder share its lock, and the second one waits.

## Ownership and execution

- `retake worker --remote NAME --project DIR --name NAME` connects to a remote registered by `retake remote add`.
  The project is a folder with a native timeline or with versioned videos ("Plain projects" below). The worker
  advertises locally installed Claude Code and Codex
  profiles. Local configuration (`[[agents]]` in the user config, or `--profiles FILE`) can define additional
  executable argv profiles, each with an optional `env` map whose values may name the worker's own environment
  variables (`$VAR`, `${VAR}`), so keys need not be written into files. A configured profile replaces a built-in
  one with the same id. Commands never come from a reviewer or the hosted service.
- A project owner chooses its worker and default agent in the browser. A task may override that agent. Credentials
  are supplied by the agent's own local configuration; they are never returned by the worker or shown to viewers.
  The first connected worker is selected as the initial stream source, so an empty hosted project can show its
  first video without an upload. Sending feedback (below) always reaches the selected worker, and anyone who can
  record a review sends. `config.enabled` is kept for older pages: it sends finished reviews that weren't sent, it
  is off until set, and turning it off doesn't stop a running task. The page no longer shows it.
- There is at most one editing task running in a project. Comments on vN become edits in vN+1: a task is the batch of
  comments of one target version, which it makes or updates in place ("Comments on vN become edits in vN+1" below). Task state, assignments, attempts, questions, answers
  and results survive server restart. A lease identifies each attempt; late reports from a replaced attempt are
  rejected. A local process lock prevents duplicate workers for one project.
- Switching an active task first requests cancellation. The worker stops the process tree, preserves files and
  its task record, then acknowledges the handoff. Only then may the replacement agent start. Lost connectivity, a
  worker that quits or a server restart never stop an agent ("Agents outlive Retake" below). An offline worker does
  not imply task completion.
- Agent usage failures pause a task with an explanation and a Switch agent / Retry action. No silent change to
  billing method. Available structured token/cost reports are recorded as estimates; absent limits remain unknown.

## Watching the agents: background, herdr or tmux

Every project (one video family) has its own worker, and so its own agent: ten projects with work run ten tasks at
the same time, one per project. Nothing serializes tasks across projects.

Where a worker runs its agent is a user setting: `open_agents_in` in the user config, or `open_in` on one
`[[agents]]` entry, which wins (docs/config.md). Values: `"background"` (the default), `"herdr"`, `"tmux"`.
`--profiles FILE` entries take `open_in` and `interactive` too.

- `background`: as above. The agent's headless command (`command`) runs detached in the project folder with the
  prompt on stdin, its output goes to `tasks/<task>/agent-<attempt>.log`, and its JSON stream gives usage and usage
  limits ("Agents outlive Retake").
- `herdr` and `tmux`: the agent runs as a normal interactive session, which the person can watch and type into at any
  time. Its command is the profile's `interactive` argv: the built-in `claude` is `claude
  --dangerously-skip-permissions --disallowedTools=AskUserQuestion` and `codex` is `codex
  --dangerously-bypass-approvals-and-sandbox` ("Permissions" below). A configured agent without
  `interactive` runs in the background, and the worker says so once in its log; an entry whose own `open_in` asks for
  herdr or tmux without `interactive` is a config error.
- One session per project, named after the project folder (the video): in herdr a workspace labelled
  `retake · <project>`, in tmux a window `<project>` in a session `retake`. Creating it never takes focus or
  switches the person's view (herdr `--no-focus`, tmux `-d`). Its working folder is the project folder, and its
  environment is herdr's or tmux's plus the profile's `env` (given on creation; `$VAR` reads the worker's own).
  `RETAKE_CONTEXT` and `RETAKE_REPORT` aren't set there: the prompt names both files.
- The prompt is written to `tasks/<task>/prompt-<attempt>.md` (in every mode), and the session gets one line:
  `New Retake task for <project> (task <id>, attempt <n>): follow the instructions in <prompt file>`. In herdr the
  agent is started with `herdr agent start` and gets the line with `herdr agent prompt`; in tmux the line is the
  command's last argument, or typed into a session that's still running (`send-keys -l`, then Enter).
- Comments sent while the agent works, for the same target version, join its run here as a new message in the same
  session ("Comments on vN become edits in vN+1" below): `More comments for Retake task <id> (attempt <n>): follow the instructions in <prompt
  file>`.
- The session stays open after the report, so the person can scroll back and keep talking. The project's next task
  reuses it while it's there and runs the same agent: the line goes into the running agent, or the agent starts again
  in the same pane when it had exited. A different agent replaces it (herdr: its pane is closed and a new tab opens in
  the workspace; tmux: the pane is respawned). The pane, and in herdr the workspace and agent name, are kept in
  `.retake/worker/view.json`.
- Completion is unchanged: the report file plus a verified render. Nothing is read from the terminal. A session that
  ends before a report (the pane is closed, the agent quits) pauses the task: "The agent's session ended without a
  report". An agent that stops at a question or a usage limit in the terminal, without a report, leaves the task
  Working until it reports, its session ends, or someone pauses it.
- Stopping a task before its report (pause, switch agent, its comments withdrawn) closes the agent's pane: `herdr
  pane close` or `tmux kill-pane`. After the report it's left open. Retake quitting or restarting, or a lost
  connection, never closes it ("Agents outlive Retake").
- No usage is recorded in these modes: there's no JSON stream to read it from. Usage limits show in the session.
- An agent that stops at a startup question in herdr (such as "trust this folder?", or Claude Code's one-time "accept
  bypass permissions mode?") makes `herdr agent start` answer `agent_not_ready`: the worker logs that the agent is
  waiting for the person and waits (`herdr agent wait`) before sending the line. Every line sent in herdr is checked:
  the worker waits up to 15 s for the agent to be working, blocked or done (`herdr agent wait --until working --until
  blocked --until done --timeout 15000`), and sends the line once more if it isn't, since an agent just past a startup
  question can drop what's typed while it draws its prompt box. In tmux the line is the command's
  argument, so the agent reads it once the person has answered; the worker logs, once per session, that a new agent
  may stop at such a question. A missing `herdr` or `tmux` binary, or a herdr or tmux error, pauses the task with that
  error.

## Agents outlive Retake

An agent's work is files: its prompt, context and report in the task's folder, and the project it edits. Retake reads
them; it doesn't hold the agent. Restarting or updating Retake never stops an agent.

- **Quitting stops nothing.** SIGTERM, Ctrl+C, `--idle-exit`, a crash or SIGKILL of `retake open`, `retake worker` or
  `retake connect` leaves every agent running: herdr and tmux panes stay open, background agents and chat turns keep
  going. Only a person's action stops one: pause, switch agent, Stop in the chat, a task whose comments were all
  withdrawn, or a retry that replaces the attempt. The worker tells these apart from its own shutdown explicitly.
- **Detached processes.** A background agent and a chat turn's agent run in their own session and process group
  (`setsid`, never a parent-death signal). Their stdin is the prompt's file, and their output goes to files, never
  pipes: an attempt's stdout and stderr to `tasks/<task>/agent-<n>[-<k>].log`, a turn's stdout to
  `chat/<turn>-<i>.out` and its stderr to `chat/<turn>-<i>.log`. The worker reads them from where it had got to: a
  turn's words and tool lines as they come, an attempt's usage and usage limit from the end of its log.
- **The record.** While an attempt runs, `tasks/<task>/run.json` (0600) says what a later worker needs to take it
  back: task, attempt, lease, agent, mode (and pane), the report file that counts now, the round, the notes the agent
  has had, the per-note changes and messages of its finished rounds, its `retake ask` secret, what to put back at the
  end (an older target's checkout, the extensions' undo snapshots), and for a background agent its pid and start time
  (Linux: the boot id and `/proc/<pid>/stat` start ticks; elsewhere `ps -o lstart=`), so a reused pid is never taken
  for it. A turn's is `chat/turn.json`, with how far into its output the server has the words. A record goes once its
  end reached the server.
- **Taking work back.** A worker, before its first poll, reads its records: the newest attempt's `run.json`. A folder
  from before records has none: then the newest task folder's newest prompt (`prompt-<n>[-<k>].md`), its context
  (which holds the task, attempt and lease) and `view.json` make one, and it's taken back only when its agent is alive
  or its report is there. The agent is alive when herdr's `agent get` finds it in its pane, tmux lists its pane not
  dead, or its pid has the recorded start time. The poll carries `adopt: {"task", "attempt", "lease", "notes",
  "alive"}` (`alive`: the agent is alive or its report is written). When the server gives the attempt back (below),
  the worker carries on with it: the same attempt and report file, no new prompt or line, the lease renewed. An answer
  that came meanwhile reaches a herdr or tmux session as a message, a background agent in its next round; `ask.json`
  is written again (the worker listens on the port it had when it's free, so a background agent's `RETAKE_ASK_URL`
  still works; `retake ask` falls back to `ask.json` when that URL doesn't answer). A report written while Retake was
  down is read as if the worker had watched: the render, the version push, questions, Done. An agent that's gone with
  no report pauses the task as when the worker watched it end ("The agent's session ended without a report", "Agent
  exited without a completion report"). A record the server refuses goes, and its background agent is stopped (the
  attempt isn't running any more: it was cancelled or retried); a herdr or tmux session is left open.
- **Turns too.** A chat turn's record is taken back the same way (the poll's `chat` and `chat_lease`): the worker reads
  its output on from where the server has it, and ends the turn when the process has (a turn that ended while Retake
  was down just sends its last words and its end). A turn the server no longer has is stopped.
- **One agent per video.** A new background attempt or turn first stops a background agent of an earlier record of the
  video that is still running.
- **The lease.** A worker renews its attempt's lease with every poll (each second). When it lapses (30 s without one)
  the task stays `running` (or `stopping`) for a grace of 10 minutes, still Working on the page, where the line under
  the comment being worked on says "Reconnecting to the agent" while the computer is offline. A worker that comes back in the grace takes the task
  back with its lease, with nothing shown. After the grace the task pauses as before ("Worker disconnected; retry or
  switch agent", with `lost` set). A chat turn's lease has the same grace; after it the turn ends with "The editing
  computer went offline". Reports, questions and renders with a lapsed lease are taken within the grace.
- **On the server**, a poll's `adopt` gives the attempt back (the poll answer's `task`, with its notes and questions,
  as for a claim) when the task is this worker's and the same attempt, and either `running` or `stopping` with that
  lease (renewed; "Reconnected to the agent" when it had lapsed), or `paused` and `lost` (or the older error "Worker
  disconnected; retry or switch agent") with `alive`, nothing else running in the video, no chat turn running, and no
  note to do that isn't in `notes`: then it's `running` again with a new lease, "Reconnected to the agent after a
  restart". Otherwise the answer has no task, and that poll claims nothing else. A poll whose `task` and `lease`, or
  `adopt`, or chat turn proves it owns a running attempt is let in even when the worker's previous session was seen
  less than 30 s ago (a hosted worker's restart gets a new session).
- **Start.** `retake open <folder>` starts, at once, the workers of the projects with work in flight (a `run.json`,
  `chat/turn.json` or `ask.json` under `.retake/worker/`), without waiting for a page to open them.
- **Rendering stays file-driven.** A restarted server lists what's on disk (a version the agent's task pushed, a
  render's `state.json`), and a taken-back timeline task renders its source as before. Nothing else waits on the agent.
- Hosted, the same: a restarted `retake worker` or `retake connect` takes its attempts and turns back.

## Permissions: agents never stop to ask

An agent runs unattended, so the built-in profiles never stop for a permission prompt, in every mode:

| id | background (`command`) | herdr, tmux (`interactive`) |
|---|---|---|
| `claude` | `claude -p --output-format stream-json --verbose --dangerously-skip-permissions --disallowedTools=AskUserQuestion` | `claude --dangerously-skip-permissions --disallowedTools=AskUserQuestion` |
| `codex` | `codex exec --json --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check -` | `codex --dangerously-bypass-approvals-and-sandbox` |

- The agent can run any command as the user who runs retake, with no sandbox: full access to the editing machine.
  The docs say so where they name the agents (docs/agents.md). A `[[agents]]` entry is the person's own argv and
  keeps whatever permissions it names.
- `--disallowedTools=AskUserQuestion` takes Claude Code's own question tool away (the `=` keeps the tool list from
  swallowing the line that follows it in tmux). Codex has no such flag; the prompt covers it.
- Claude Code refuses `--dangerously-skip-permissions` as root; run retake as a normal user.
- Every task prompt says: never wait for input, and never ask the person in the terminal or with the agent's own
  question tools. When unsure about a note, ask with `retake ask` ("Questions from inside a task") and carry on with
  the rest of the task. It also says to work the notes one at a time, `retake working` before each and `retake done`
  after it ("Marking progress").

## A note's stills and images

- Before an attempt starts, the worker saves each assigned note's still as `note-<id>.png` and its attached images as
  `note-<id>-<file>` in the task folder, from `/api/worker/notes/<id>/snapshot` and `…/images/<file>`.
- Nothing about them stops the task. When the server has no still to give (the note has strokes but no snapshot, or
  the snapshot is missing or outside the project), the worker makes one: the frame of the note's version on this
  computer at the note's moment (the middle one of its strokes' times, else its `t0`), at most 1280 px wide, with the
  note's strokes drawn and numbered as the notes engine draws them (a stroke stored without its points is the outline
  of its box: an ellipse for a circle, else a rectangle).
- Each note in the context may carry `missing`: plain sentences saying which of its files the agent didn't get and
  what it has instead ("Its saved still couldn't be fetched (host returned 404): note-3.png is a still this computer
  made of v6 at 20.10 s, with the note's pen lines drawn on it."). A still that can't be made either, or an image
  that can't be fetched, is said there and left out; the task runs on.

## Questions from inside a task: `retake ask`

An agent that is unsure about a note asks about a moment of the video, and keeps working. It can draw to make the
question clear: boxes and arrows with labels, freehand lines, images it made, and choices to tap. The question shows on
the review page under its comment and on the timeline at that moment; the answer comes back to the same task.

```
retake ask [--note ID] [--at TIME] [--from TIME --to TIME] [--version LABEL] [--px]
           [--box X,Y,W,H[:LABEL]]... [--arrow X1,Y1,X2,Y2[:LABEL]]... [--draw FILE]
           [--image FILE[@X,Y,W,H]]... [--option TEXT[@FILE]]... [--json] "question"
```

- With one positional argument it asks from inside the running task: no video path and no token. (Three, `retake ask
  <video> <note> "question"`, is the project-level command of spec/notes.md, unchanged; `--option` there is still a
  version label.)
- `--note` is one of the task's notes (`task.notes`); it may be left out when the task has one. Times are seconds
  (`83`, `83.5`) or `m:ss` (`1:23`) on `--version`, which defaults to the note's version (the times in the task
  context; a draft label such as `v4-draft2` points at a render of this task). `--from` and `--to` (both or neither,
  `from < to`) are the shot the question is about; `--at` is its moment, by default `--from`, else the note's start.
- **Visuals** (all optional, all repeatable but `--draw`). Coordinates are normalized 0–1 to the frame with (0, 0) at
  the top left, or pixels of the version's frame with `--px` (the server divides by its width and height, and refuses
  `--px` when it doesn't know them).
  - `--box X,Y,W,H[:LABEL]`: a rectangle, inside the frame, with a size. At most 10.
  - `--arrow X1,Y1,X2,Y2[:LABEL]`: from the first point to the second, both inside the frame. At most 10.
  - `--draw FILE`: freehand lines, a JSON list in the take's stroke format (spec/take-format.md, `strokes.json`):
    `[{"points": [[x, y], ...]}, ...]`; other fields are ignored. At most 50 lines and 5000 points.
  - `--image FILE[@X,Y,W,H]`: a PNG, JPEG, WebP or GIF the agent made (a mockup, a still), 8 MiB at most, its type
    checked from its bytes. With a rectangle it is placed on the frame; without, it shows in the question card. At
    most 4.
  - `--option TEXT[@FILE]`: a choice the reviewer can answer with one tap, optionally with an image of it (as
    `--image`). At most 6; `@FILE` is read as an image only when FILE ends in `.png`, `.jpg`, `.jpeg`, `.webp` or
    `.gif`.
  - A label is at most 40 characters, an option's text 120, the question 2000.
- It prints `asked about #12 at 1:23 (question 7)` (with `--json`, the question) and exits 0 at once. It never waits
  for the answer. Exit 1 when the question is refused, with the reason; exit 2 for a usage error (a bad flag, a
  missing or oversized file), or when it isn't run inside a task ("not inside a Retake task: …").
- **Finding the worker.** `RETAKE_ASK_URL` and `RETAKE_ASK_TOKEN` in its environment, else the file
  `.retake/worker/ask.json` (`{"url", "token"}`) in the current folder or the nearest parent that has one. A
  background agent gets both variables. An agent in herdr or tmux gets the file: its session's environment is fixed
  when the session opens, and outlives the task.
- **The worker's endpoint.** At its first task a worker listens on `127.0.0.1` (a free port) for its whole life.
  Each attempt gets a fresh random secret (32 hex), which ends with the attempt; the worker writes `ask.json` (mode
  0600) when the attempt starts and deletes it when it ends. `POST <url>/ask` with `Authorization: Bearer <secret>`
  and `{"note", "at", "from", "to", "version", "px", "text", "boxes": [{"box", "label"}], "arrows": [{"from", "to",
  "label"}], "strokes": [{"points"}], "images": [{"name", "data", "box"}], "options": [{"text", "image": {"name",
  "data"}}]}` (`data` base64, 48 MiB in all) answers 200 with the question (`TaskQuestion` below), 403 for a wrong or
  stale secret or when no task is running, 400 for a malformed request, and the server's status and reason when it
  refuses. The worker's own host token never reaches the agent.
- **The frame.** The worker adds a still of the question's moment, `frame` (a JPEG at most 480 px wide, from the
  version's file or render on this machine, made with ffmpeg in at most 15 s). Without ffmpeg, or for a moment it
  can't read, there is none, and the page draws the visuals on a plain frame.
- The worker forwards it as `POST /api/worker/ask` (the same body plus `"task"`, `"lease"` and `"frame"`) with its own
  credentials (in process in Local mode, over HTTP to a hosted server), fenced like a report. The server checks the
  note is the task's (`note` 0 with one note means that one), the version is in the family or is a render, the times,
  the visuals and their limits, the images' types (from their bytes, against their names) and sizes, and the text,
  and refuses with 409 and the reason. It stores a question thread (spec/notes.md; `by` "agent", `t` = `at`), records
  it on the task (`asked: [{"id", "note"}]`), moves the note to `question`, and keeps the visuals with the question on
  the server: `.retake/questions/<id>/visuals.json` and its images `img_1.png`, ..., `option_1.png`, ...,
  `frame.jpg` beside it, served at `GET /api/questions/<id>/<file>` with the viewing permissions of the take images.
  So they work the same in Local mode and hosted. The attempt keeps running: asking doesn't end it.
- `TaskQuestion` (the endpoint's answer, and the poll's `questions`): `{"id", "note", "version", "at", "from", "to",
  "text", "visuals": {"boxes", "arrows", "strokes", "images": [{"url", "box"}], "options": [{"text", "image"}],
  "frame"}, "answer": {"id", "by", "text"}}`, normalized, with `url`s on the server.

**Answers.**

- The comment shows the question under its text, with the reply box: typed, a tap on one of its choices, or recorded
  with the existing voice reply. A typed or tapped answer is `POST /api/automation/tasks/<id>/answer` `{"question":
  <thread id>, "message"}` (a tap sends the choice's text; any role that can record); a recorded one is a segment
  whose `answer_to` is the thread id, as for any question, and the reviewer may draw over the agent's visuals while
  recording it, as on any take. Either way it becomes an answer thread (`by` the person's name), and the note goes
  back to `open`, so the comment is Working again (its task owns it). The answer's strokes stay the take's, as
  today; the agent's visuals never become a reviewer's note or stroke.
- The poll answer lists the task's questions in `questions`: `[{"id", "note", "version", "at", "box", "text",
  "answer": {"id", "by", "text"}}]` (`answer` once there is one). The worker says which answers reached the agent
  with `told: [<answer thread id>, ...]` in its next poll; the task keeps them in `told`.
- In herdr or tmux, while the attempt is running, the worker sends each new answer into the live session as a new
  message, the way it sends a task's line: `Answer to your question on note 12 at 1:23: <answer>` (whitespace folded
  to single spaces). That answer is told.
- Otherwise the answer waits for the next attempt (a background agent can't be given anything while it runs). The
  attempt's context has it in `questions`, and every answer there is told. In herdr or tmux the attempt's line then
  starts with the answers: `Answer to your question on note 12 at 1:23: <answer>. Carry on with Retake task <id>
  (attempt <n>): follow the instructions in <prompt file>`.

**What the page shows.** The agent's visuals are drawn in one style of their own, never in a pen colour: the Question
violet, dashed outlines, a small chip on each box and arrow with its label (or "Agent"), freehand lines dashed, and
its images in a framed card. They show on the video while the playhead is inside the question's range (`from`–`to`,
else 1.5 s around `at`) and whenever the question is selected (clicking its marker or its time seeks to `at`
and selects it, until the reviewer selects something else or plays out of the range). Entering the range highlights
them once, briefly. The question's card under its comment shows a thumbnail: the frame still with the visuals drawn
on it, then the question, its unplaced images and choices (with their images, in framed cards), and the reply box.

**How a task ends.** A note of a task is *held* while its newest question from that task has no answer (waiting for
the person), or has one that wasn't told to an agent yet.

- A `completed` report finishes the task's notes that aren't held: they become Done, with the render (or new
  version) required as before. When every note not yet Done is held, the report needs no render or version: the
  worker reports at once, without one, and the task's drafts stay drafts.
- If a note is still held, the task isn't completed. It waits in `question` while one of its questions is
  unanswered, else it is queued again at once, so the next attempt gets the answers. The notes it finished stay Done
  (`done` lists them), and later attempts leave them alone. So a task can end an attempt with some notes Done and
  some still Question.
- Answering the last unanswered question of a task waiting in `question` queues it again ("Reviewer answered").
- A completed report without a render or version while a note isn't held (the agent was told an answer just before
  it reported) queues the task again rather than failing it.
- Withdrawn feedback: a note of a task that is Question stays in it. A note that was answered outside the task (not
  in its `done`) or retracted drops out of the task ("Comments on vN become edits in vN+1" below); only a task with none of its notes left is
  cancelled, or stopped while it runs.
- The report's `question` state stays as the fallback: the task waits in `question` with the report's message,
  answered with a typed `…/answer` without `question`.

## Marking progress: `retake working` and `retake done`

The agent works a batch one note at a time and says so as it goes, so the person watches the notes finish one by one
instead of all at once when the report arrives.

```
retake working <note> ["what I'm doing"] [--json]
retake done <note> "what changed" [--json]
```

- **Where they work.** Inside a running task only, through the same endpoint as `retake ask` (`RETAKE_ASK_URL` and
  `RETAKE_ASK_TOKEN`, else `.retake/worker/ask.json`; "Finding the worker" above), so they work the same in the
  background, herdr, tmux and hosted (the worker relays them like a question). `<note>` is one of the task's notes
  (`#12` or `12`). They print `working on #12: slowing the mask fade` and `#12 done → v5` (with `--json`, the
  server's answer) and exit 0 at once. Exit 1 when refused, with the reason; exit 2 for a usage error, or when it isn't
  run inside a task.
- **`working`** says the agent has started on that note, with a few words of its own ("slowing the mask fade", at most
  200 characters, optional). A task has at most one working note: a new `working` replaces the one before.
- **`done`** says that note's edit is in, with what changed (1 to 4000 characters, required): from then on it is in
  the live target, so the person can watch it ("The live version" below). In a plain project the agent first writes
  the video so far to its output file; `done` without one that plays is refused ("write the video so far to <file>
  first"). It clears the working note when it's that one. Saying `done` again replaces the mark; `working` on a note
  marked done takes the mark back.
- **The endpoint.** `POST <url>/progress` with the attempt's secret and `{"note", "state": "working" | "done",
  "text"}`. The worker forwards `POST /api/worker/progress` with `"task"`, `"lease"` and `"join"` (notes of the next
  batch it gave the session that the server hasn't heard of yet, as a poll's), fenced like a report. For `done` it adds
  `"render"`, the revision that has the edit, once there is one: a timeline project's `done` goes first without it
  (the note shows it is being put in) and again with it when that render is complete; a plain project's goes once,
  with the revision it just registered; a document's comment needs none (`"ready": true`). The worker keeps the marks
  still waiting for their render in the attempt's record, so a later worker finishes them. The server checks
  the attempt is running and not stopping, the note is one of its notes still to do (`task.notes` minus `done` and
  `dropped`), and the text; `done` also needs the note `open` (a note in `question` waits for its answer:
  "note #12 waits for the answer to your question"), and its `render` a complete render of this task. It answers 200
  `{"note", "state", "target", "ready"}`, 409 with the reason when it refuses, and emits `automation`, so every page
  updates at once. A chat turn's secret is refused (a chat has
  no notes to mark).
- **On the task.** `working: {"note", "text", "at"}` and `marked: [{"note", "change", "at", "ready", "render"}]`, in
  the order marked
  (`GET /api/automation` shows both). A question about a note (`retake ask`) takes back its mark and, when it's the
  working one, clears `working`. A note that drops out of the task (retracted, answered elsewhere) leaves both. A new
  attempt starts with neither.
- **What it changes.** What the page shows while the attempt runs: a note marked done shows Done, with its change,
  "in v5", once its mark is `ready` (its edit is in the live v5); until then it stays Working, its line saying "Putting
  it in v5…". The note itself stays `open` in the store, and no version is made or finished by a mark.
- **The report is the source of truth.** When the attempt ends it reconciles everything, and `working` and `marked`
  are cleared. A `completed` report finishes the task's notes as before (every note not held, with the render or
  version). A finished note's change is the report's own entry for it (`notes`), else its `retake done` text, else the
  message of the round that did it; the worker says which notes the agent wrote an entry for (`said` on the report).
  A note marked done and then held by a question isn't finished: it shows Question. Any other end (paused, stopped,
  a `question` report) finishes nothing, so the notes it marked show what the task shows (Open with Retry, Question).
  Notes the agent never marked are finished by the report all the same.
- The worker logs each one (`the agent is on #12: slowing the mask fade`, `the agent finished #12`).

## Connect your computer: `retake connect`

```
retake connect [<server URL> --token rk_…] [--folder DIR] [--name NAME] [--profiles FILE]
```

One command connects a whole projects folder to a hosted server with a machine token (spec/server.md, "Machines";
the browser's Connect your computer card prints the line). It is `retake worker` for every project, in one process.

- The folder is `--folder`, else the current folder. It checks the token first (`GET /api/machine`; a bad token or
  URL stops it with exit 1).
- It finds the projects as `retake open <folder>` does (spec/home.md, "Folders, projects and videos"): the same scan
  of the folder's tree, every video with or without a native timeline (spec/bridge.md), so the server lists what
  `retake open <folder>` lists. A project with a timeline but no version yet counts too (one video, named by the
  same rule, so its worker can render the first version).
- For each video it sends its name, title, `path` and `project` (`POST /api/machine/projects`, which makes it when
  it's missing; the two paths put it in the same place of the home's tree as `retake open` does) and,
  when it has a version, that version's first frame as the poster (`PUT .../poster`, the JPEG of spec/home.md's
  poster). Nothing else is uploaded: no footage, source or renders. The worker then tells the server which version
  files it has (metadata only, "Version files" below), and their video plays through the worker's relay.
- Then it runs one worker per project (as `retake worker`: the project URL is `<server URL>/p/<name>`, the machine
  token is its agent token, `--name` and `--profiles` as there). A worker that stops with an error (another worker
  holds the project, the renderer is missing for a timeline project) is started again at the next look, and the
  same error is printed once.
- It looks at the folder again every 10 s: a new project is sent and started; a project that is gone is stopped.
- It saves the server URL, the token and the folder in `$XDG_CONFIG_HOME/retake/connect.toml` (mode 0600), so a bare
  `retake connect`, from any folder, connects the same folder to the same server again. Ctrl+C stops every worker.
- `retake worker --remote NAME --project DIR` stays, for one project with that project's agent token.

## Version files

A connected computer's version files (the family of spec/api.md, "Versions": `clip_v1.mp4`, `v2/clip_v2.mov`, an
`.m3u8` playlist with its segments) play on the server as they do in `retake open`, without being uploaded.

- A worker that talks to a server over HTTP (not Local mode, whose server reads the folder itself) detects its
  project's family from the project's anchor (spec/home.md) every 2 seconds, and sends it when it changed, and at
  least every minute: `POST /api/worker/files` `{"files": [{"label", "n", "name", "size", "modified", "duration",
  "fps", "width", "height"}]}`. `name` is the file's name, `size` its bytes, `modified` its mtime (Unix ms). Only
  versions that probe with a duration are sent (as `retake open` lists them); a streamed version
  (spec/bridge.md, "Streaming") only once it's complete. At most 1000; a label is `v<N>`, a name a plain file name
  with a family video extension. Nothing but this metadata is sent.
- The server keeps the list per worker in the automation state (`files`, each with its `worker`) and answers 200
  `{"ok": true}`. When it changed, the page gets the `push` version event for each new label. The family lists the
  files of the selected worker (`config.worker`), else of the worker seen last that has files, as versions with
  `"relay": true`, unless the server has a version of that label itself (an uploaded one wins). Their takes live on
  the server, under `.retake/remote/<label>/`.
- They count as versions everywhere: the numbering of renders (`v<N>` continues after them), the poll answer's
  `versions`, and the page's "follow latest".

## The media relay

Relayed versions (a worker's renders and its version files) play from the worker on request. The page uses the
usual URLs: `/video/<label>`, and `/video/<label>/<file>` for a playlist and the files beside it.

- The server asks the worker (`GET /api/worker/media`, a long poll of up to 15 s that answers 204 when there is
  nothing) with `{"id", "render" | "version", "file", "range", "head"}`: `render` is a render's id, `version` a
  version file's label; `file` is the render's media file, or for a version file `""`/its own name, or a plain name
  beside its playlist. `range` is the aligned chunk the server wants (below); for a HEAD, the browser's `Range` as
  sent.
- The worker answers `POST /api/worker/media/<id>` with the bytes as the body and `X-Media-Status` (200, 206, 404,
  416 or 503), `Content-Type`, `Content-Range`, `X-Media-Length` (the length the browser is told: the body's, or
  the file's for a HEAD) and, for a version file, `X-Media-Modified` (its mtime, Unix ms). The body streams through
  to the browser as it arrives; the server keeps only what its cache keeps (below). The server sets the content type
  from the file's name, not the worker's.
- **Chunks.** No response carries more than 8 MiB. The worker reads one range: `bytes=a-b`, `bytes=a-` or
  `bytes=-n` (anything else, or none, is `bytes=0-`). A range that starts past the end is 416 with `bytes */<size>`.
  Otherwise it sends at most 8 MiB from its start: 206 with `Content-Range: bytes a-b/<size>`, or 200 when no range
  was asked and the whole file fits. So an open-ended `bytes=0-` of a 2 GB file gets its first 8 MiB, and the
  browser asks for the rest as it plays or seeks. The server sends the browser what the worker sent, with
  `Accept-Ranges: bytes` and `Content-Length`. A request without a range for a file larger than a chunk is answered
  200 with the whole file: the server asks for one chunk after another and streams them in order.
- **What a worker reads.** Only a render's own directory (`.retake/worker/renders/<id>/`, flat media names), or a
  file of its project's current family: the version's file itself, or for a playlist version a regular file
  directly beside it with a media extension (`.m3u8`, `.m4s`, `.mp4`, `.ts`). Never a path with a separator or
  `..`, a symlink beside a playlist, or anything else in the project: those are 404. The server only asks for
  labels it lists for that worker.
- **The server's cache.** A hosted server (`retake serve`) keeps what it relayed once and won't change: a render's
  media files (its segments, init and other files; its playlist only once the render is complete), and every file
  of a version file except a playlist. They are cached in aligned 8 MiB chunks (chunk `k` is bytes `k·8 MiB` to
  `(k+1)·8 MiB − 1`; a version file's chunks are keyed by its size and mtime too, so a changed file is new). The
  server always asks the worker for whole aligned chunks and cuts the browser's range out of them, so a later
  viewer of the same bytes is answered from the server's disk without asking the worker, and 5 viewers cost the
  worker's upload once. Concurrent misses of one chunk make one worker request; the others wait for it. A growing
  playlist (a draft's, or any render's before it completes) is never cached. The cache is
  `DIR/relay-cache/<project>/` under the data folder, least recently used first out once it's over its size
  (`--relay-cache SIZE`, `RETAKE_RELAY_CACHE`, `relay_cache`; default `20GB`, `0` turns it off). Deleting a project
  deletes its cache. It holds rendered and exported video only, never source footage, and it sits behind the same
  viewing permissions as the video URLs. A cached chunk is served even while the worker is offline.
- **Offline.** When the worker hasn't polled for 10 s, the server answers 503 `{"error":"editing machine is
  offline"}` with `Retry-After: 3`, without asking. A request no worker takes within 10 s, or that its worker
  doesn't start answering within 30 s, gets the browser a 503 too. The page then shows the version's poster with "The editing computer is offline" on the video (not a broken
  player), checks again every 5 s, and plays once it's back.

## Plain projects

A project without a native timeline is a folder of finished videos. Its worker runs without a renderer: it relays
its version files and runs the agent, and the agent makes the next version however it can.

- The worker needs no `retake-edit`, and renders nothing. A worker without any agent (in any project) still
  connects and relays the project's video, so it plays on the server as in `retake open`; it never takes a task.
- A task's prompt says the project has no timeline and names the file to start from: the task's target when it
  exists, else the version its notes were made on ("Comments on vN become edits in vN+1"); the task context lists
  every version under `family`, each with its path relative to the project. The prompt asks the agent to make the
  change with the tools it has (ffmpeg, say), and to write the result as one video file to `$RETAKE_OUTPUT`
  (`.retake/worker/tasks/<id>/output.mp4`). It never edits or deletes the existing versions.
- When the agent reports `completed`, the worker puts the result in as the task's target: a new version named by the
  rule of `version_push` (spec/notes.md), after the newest version with that `v<N>`, in a sibling `v<N>/` folder when
  the family uses version folders; or, when the target exists, in its place, the earlier file moved to
  `.retake/worker/history/<label>-<unix ms><ext>`. A video the agent wrote as a target that didn't exist counts as
  well. The version must probe with a duration; otherwise the task pauses ("The agent's video doesn't play"). Without
  a result the task pauses too ("The agent finished without writing a new version").
- Then the worker sends its files (hosted), and reports `completed` with `"version": "<label>"` instead of a render.
  The server takes it once that label is the task's target and a version file of its family that isn't a render: the
  task's notes become Done, answered in that version, with the agent's message as the change. Questions work as for timeline projects.
- The agent doesn't run `retake push`: the worker's push puts the file into the project folder, and the server only
  gets its metadata.

## Comments on vN become edits in vN+1

That is the whole rule, for the person and for the agent. A send never makes a version by itself; versions exist
because a comment on the one before asked for changes.

- **The target.** A comment's target is the version after the one it was made on: a comment on `v7` targets `v8`, one
  on `v8` targets `v9`. A comment on a draft (`v8-draft2`, a revision of the live `v8`) targets `v9`, as one on `v8`
  does ("The live version"). A typed
  comment on an extension's version (spec/extensions.md) that doesn't want a version is finished in its own version,
  as before; one that wants a version targets the next (`v0` → `v1`). The notes API gives each comment its `target`,
  and `later` when a version after its target already exists (below); a task carries its `target`.
- **Made or updated.** When the target doesn't exist yet, the agent makes it. When it exists, the agent edits it, and
  it updates in place: the list still says `v8`, not a new label. A comment on `v7` goes into `v8` while `v8` is being
  made and after it is done, so commenting again on `v7` never makes `v9`; a comment on `v8` makes (or updates)
  `v9`. The earlier media of an updated version is kept as its history: a timeline project's earlier render of `v8`
  becomes a draft of `v8` (`v8-draft<K>`, playable by URL, not listed), and a plain project's earlier `clip_v8.mp4` is
  moved to `.retake/worker/history/` before the new one takes its place.
- **Where the agent starts.** From the target when it exists (it already has the earlier changes), else from the
  version the comment was made on. In a timeline project that is the current source when the target is the newest
  version or the next one. A target older than the newest version (below) starts from the target's own source (its
  render's `source.json`), and the worker puts the newest version's source back once that task ends.
- **A version after the target exists already** (rare: a comment on `v7` while `v9` exists). It still goes into `v8`,
  as the rule says; `v9` isn't touched or remade. The comment's `later` says so (`"later": "v9"`), so the page can tell
  the person that `v9` was made before this change.
- **One agent per video.** At most one task runs in a project (one video). A task is a batch: the comments of one
  target. A Send with comments on `v7` and on `v8` is two tasks, `v8` then `v9`, done one after the other by the same
  agent. Sends queue to that agent: the queued tasks run in the order of their targets (lowest first), and at most one
  task per target waits in `queued`: notes sent for a target that has one join it.
- **Sent while a task runs, for its target.** Notes for the running task's target join it, before its version is made
  or updated:
  - In herdr or tmux, as a new message in the live session, while it works or after its report.
  - In the background (where a running agent can't be given anything), as a follow-up round once its run ends: the
    worker runs the same agent again, in the same task and attempt, on the same working files (the source and drafts,
    or a plain project's `output.mp4`), with the new notes, and with any answers to its questions that came meanwhile.
  - The version is made (or updated) when the agent has nothing left for that target: its last report is in, and no
    note for it waits. Every note of the task is then Done, or Question (it asked and waits for the person). A completed
    task makes or updates exactly one version, its target, however many sends joined it. Notes for another target wait
    in their own queued task.
  - A task that ends without a version (stopped, paused, every note waiting on a question) leaves the notes sent
    meanwhile in their queued task, which runs next. A note answered after its task's version was made goes in the
    task's next run, which updates the same target.
- **How notes join a run.** While an attempt is running and not stopping, the poll answer has `next`: the queued task
  of the same target (`{"id", "notes"}`), when it has never run (`attempt` 0). The worker gives its notes to the agent:
  it saves their stills and images as for an attempt, writes `context-<n>-<k>.json` (the attempt's context with every
  note of the task in `task.notes`, and the questions with their answers) and `prompt-<n>-<k>.md` (`k` counts the
  rounds, from 2) with a new report file `report-<n>-<k>.json`, which replaces the earlier one: the worker waits for
  that file only, so a report written before the agent read the new notes never finishes the run without them.
  - herdr or tmux: the session gets `More comments for Retake task <id> (attempt <n>): follow the instructions in
    <prompt file>` (`Answers for Retake task …` when it's only answers, after a report).
  - background: the prompt is the whole task prompt, after a few lines saying what is new and that the earlier notes'
    changes are on disk to keep (a plain project's video continues from `output.mp4`); the agent's log is
    `agent-<n>-<k>.log`.
  - Before it makes the version, the worker waits for one more poll (3 s at most), so a send from just before the run
    ended joins it too.
  - The worker then says `join: [ids]` in its next poll (and in its report, which counts them before it decides). The
    server moves those notes from the queued task to the running one, with an event `Added #4, #5 in the agent's
    session`, and drops the queued task once it is empty. A note that is no longer in a queued task of that target is
    skipped. A task of only typed comments (spec/extensions.md) takes only typed comments that way, so a report alone
    still finishes it.
- **Per comment.** Each note still ends on its own: Done from the completed report (shown as soon as the agent marks
  it with `retake done`, "Marking progress"), Question from `retake ask`. A
  report may say what changed for each note: `{"state": "completed", "message": "summary", "notes": {"12": "Cut the
  intro to 3 s"}}` (keys are note ids; at most 1000, each at most 4000 characters). A note's `change` is its entry
  (the latest round's, when several say one), else its `retake done` text, else the message of the round that did it.
- **Merging queued tasks.** Whenever a second task of a target would wait in `queued` (a send, a retry, an answer that
  queues a task again, a switch, a fallback), they become one: the oldest keeps its id, agent and history and takes the
  others' notes, questions (`asked`), told answers and done notes, with an event `Joined task <id>: #4, #5`; the others
  are removed. A retry or switch that names an agent sets that task's agent. Per-comment actions (send one, retry) add
  to their target's task the same way. Different projects still run at the same time ("Watching the agents"). Tasks
  waiting in `paused` merge the same way, per target, so a target has at most one paused task (its paused batch).
- **Send resumes a paused batch.** A paused task's comments show Open, and Send sends them (they count in Send). When
  a send (Send, send one, a take sent with `send: true`) has notes for a target with a paused batch, or notes that
  batch already holds, the notes go in that batch and it starts again: `queued`, its error cleared, with an event
  `Resumed by a send: #4, #5`. An agent picked for the send becomes its agent; otherwise it keeps its own. A retry does
  the same without new notes. A send never makes a second task beside a paused batch, and never skips a note because a
  paused task holds it. The resumed batch then merges with a task of its target already waiting in `queued`, as
  above, so it is still one task and one agent.
- **A Send's take goes with it.** A queued task doesn't start while a take sent with `send: true` is still being
  processed (one of its jobs queued or running): the take's notes join their targets' tasks once processed, then they
  start. A failed job ends the wait, and so does an hour since the job was queued (a stuck job never holds the agent
  back).
- **Older stores.** Opening a project's store gives each unfinished task without a target the target of its first
  note, and merges the tasks waiting in `queued` (not running) per target, and the ones in `paused` per target, as
  above, so the one task per note an older version queued (or that someone paused) becomes one task per target: 75
  paused single-note tasks for `v8` are one paused batch of 75 notes, and the next Send resumes it as one task.
- **Withdrawn notes.** A note of a task that is retracted or answered outside it is dropped from the task (`dropped`,
  with an event); the rest carry on. A task with no notes left is cancelled, or stopped while it runs.

## Submission and comment state

- Recording and incremental transcription do not launch editing. Send closes the take (`/done` with
  `"send": true`) and queues its processing. Once the final notes job succeeds, the take's open notes join the
  queued task of their target ("Comments on vN become edits in vN+1"): right away, in the process that ran the job, whether or not a worker is connected. A `/done`
  without `send` (an older page) queues them only when `config.enabled` is on. Each finished take is considered
  once: if neither applies, its notes stay Open until someone sends them, and turning `enabled` on later doesn't
  sweep up old reviews. Finishing again with `"send": true` sends a take that was already processed. Send also
  submits the shown version's other open comments (`POST /api/automation/tasks`); that always queues. An older
  version's open comments are sent from that version (spec/notes.md, "Comments belong to their version"). Submission is idempotent
  while a queued, running or question task owns a note; a note of a paused task resumes that task ("Send resumes a
  paused batch"). Reopened notes may be submitted again.
- Any role that can record (reviewer, approver, owner; signed in or through a share link, local or hosted) sends
  and retries. A queued task waits for the selected worker, or for the first worker to connect when none was ever
  selected, and starts by itself when it polls: no worker online is not an error. A retry by a reviewer or an
  approver keeps the task's agent; choosing an agent, switching and pausing stay with the owner.
- A comment shows one of four states: Open, Working, Question, Done (the page maps them in `web/src/lib/status.ts`).
  No task, a cancelled one, or a paused one is Open (a pause adds its reason once, such as "Paused: usage limit",
  with Retry); queued, running and stopping are Working, and so is an open note of a take that was sent and is
  still being processed (`sending`, below); an agent's question is Question; completed, answered and accepted are
  Done. A Working comment whose task hasn't started says why in one short line: "Waiting for the editing computer"
  while no worker is online, "Waiting for the agent" while Local mode is `blocked`. Existing approval and retraction remain intact. Working comes from a task. Done requires a
  successfully rendered result plus an explicit per-note change description; an agent process exit alone never
  completes feedback. A note the running attempt's agent marked done (`retake done`) shows Done before that, going
  into its target ("Marking progress"); the store finishes it only with the report.
- A question and its answer stay on the task/comment. Answering resumes the same task ("Questions from inside a
  task"). A Working comment whose task asked about it shows Question until it is answered. Operational pauses (usage
  limits, offline, failed render) are displayed separately from creative questions.
- Every task records the original note IDs and versions. Completion links the original notes to the resulting
  version. The agent receives the current source, original feedback, prior task events, and a local durable task
  directory. Switching providers does not pretend to resume another provider's session.

## Live rendering and transport

- The worker monitors native source inputs, debounces stable changes, and snapshots each valid source revision.
  `retake-edit` incrementally renders HLS using its existing dependency cache. A newer source revision cancels
  obsolete work and starts the latest revision; the last playable result stays available while that happens.
  Cancelled revisions retain their playable portion and display as stopped, rather than continuing to claim that
  they are rendering. Starting an agent creates no version, only the target's first live revision (below).
- Only a task renders. Outside a task the worker renders just once, for a project with no version yet (the poll
  answer's `versions` is 0), so an empty project gets its first video; plain saves render nothing. Each render's
  `state.json` stays in `.retake/worker/renders/<id>/`, and on start the worker takes the newest complete one as
  its last render, so a restart with unchanged source renders and registers nothing.
- Versions versus drafts. A label `v<N>` is created or updated only when a task completes, for its verified final
  render, or for that first render of an empty project. Every other render is a draft, labelled `v<N>-draft<K>`, where
  `v<N>` is the version it is heading for (the task's target) and `K` counts that version's drafts. Completion
  promotes the task's final draft to its target `v<N>`: same media, new label, and the old draft label keeps resolving
  to it; when `v<N>` existed, its earlier render becomes the draft `v<N>-draft<K>`. The family lists each version,
  plus, per version number, the newest draft and the newest complete draft while they're ahead of the newest version
  or their task is unfinished, so following latest keeps showing the agent's work live. Older drafts aren't listed but stay playable by URL, and
  takes recorded on them still save.
- **The live version.** During a batch its target `v<N>` is live: people see it as `v<N>`, updated in place as the
  agent finishes each note, and never the word "draft". The drafts are its revisions, internal (their labels stay in
  URLs and the API); the page shows a draft `v<N>-draft<K>` as `v<N>` everywhere (the version menu, toasts, the page's title, the home's
  cards and folder tree: one label helper, `verName`), the version menu has one `v<N>` row that plays its newest
  revision, with a small red live dot while the task runs, and the red edge light ("HTTP and UI") is on the video.
  - **At once.** A task's attempt makes its target's first revision as it starts, so `v<N>` exists before any edit: in
    a timeline project the worker renders the task's source as soon as it has no render of this task (it is the
    starting version's source, so every unit is reused from the cache); in a plain project it registers the version
    the agent starts from as a revision (a hard link of that file, else a copy, as `video.mp4` in the render's folder).
  - **Per note.** `retake done` puts the note's edit in a new revision before the note shows Done ("Marking
    progress"). In a timeline project the worker waits for a complete render of this task whose source is the one at
    the `done`, or one started after it (the render loop renders every stable save of the source within about a
    second, only the changed units; a render that's already complete for the current source counts at once). In a
    plain project the agent writes the video so far to its output file (`$RETAKE_OUTPUT`) before `retake done`; the
    worker checks that it plays, copies it (a reflink where the file system has them) to a new render folder as
    `video.mp4` and registers it, a complete render with `"media": "video.mp4"`, before it tells the server. It keeps
    the media of a task's newest two plain revisions and deletes the older ones' (only their `state.json` stays).
  - Comments recorded on the live `v<N>` (on any of its revisions) belong to `v<N>` and go into `v<N+1>`, as for any
    comment on `v<N>`. Completion makes `v<N>` final as before: the final render is promoted (timeline), or the agent's
    video is put in as the version file (plain).
- The host registers each immutable render revision in the video family as above. Pending/progressive render
  metadata reaches browsers through the existing events API. Following latest switches to the new revision once
  it can play the current position; recording, comparing with C and an explicit historical selection remain
  pinned (C back catches up: spec/bridge.md, "The page").
- Browser media requests are relayed to the connected worker over worker-initiated HTTP requests ("The media
  relay" above). Only registered revision directories, the project's version files, and relative, regular media
  files beside a playlist version are readable. Traversal, symlink escapes, arbitrary URLs, and executable paths
  are rejected. The relay is bounded (8 MiB per response), cancellable, expires unanswered requests and returns an
  offline error when no worker is available. No source or other project file is exposed.
- Metadata and version identity persist at the host. Rendered segments remain on the worker. Historical output is
  retained there; offline machines cannot serve uncached media. A server restart reconnects the same worker and
  continues serving retained revisions.
  The in-flight relay queue belongs to one server process; deployments with multiple replicas must keep a project's
  requests on the same process. Source saves are detected by content hashing source files and statting explicitly
  referenced media (path, size, nanosecond mtime); the renderer validates its own dependency cache.

## HTTP and UI

- `/api/automation`: GET for viewers, configuration changes for owners only. GET returns the state (`config`,
  `workers`, `tasks`, `renders`; a render has `draft` while it is one) plus `"local": true` when this process runs
  the project's worker itself (Local mode) and `false` on a hosted server, and, in Local mode only,
  `"blocked": "<why>"` while that worker can't run. Then `workers` may not list it. `sending` lists the takes
  finished with `"send": true` whose processing hasn't finished yet (and hasn't failed): `[{"version", "take"}]`.
- `POST /api/automation/tasks` (submit feedback; an optional `agent` picks the agent for the tasks it makes, when the
  selected worker offers it), `.../tasks/<id>/answer` and `.../tasks/<id>/retry`: any role that can record. A
  retry's `agent` counts only from the owner. Switching, pausing and the config are owner actions.
- `/api/worker/*`: Bearer agent token only, including local requests. Worker registration, polling/heartbeats,
  attempt reports, questions (`ask`), the agent's progress marks (`progress`), render registration/progress, version files (`files`), note artifacts and
  relay response endpoints. An attempt's `completed` report names a render (`result`) or, in a plain project, a version file
  (`version`). The poll answer has
  `config`, `task`, `notes` (the task's notes, plus every open note of every version as context), `questions` (the
  task's, with their answers), `versions` (how many versions the family has, drafts not counted) and, for a running
  attempt, `next` (the queued task of its target it may take in, "Comments on vN become edits in vN+1"). A poll may carry `told` (answers that
  reached the agent) and `join` (notes of `next` given to the agent's session); a report may carry both. A poll
  may carry `adopt` instead of `task` ("Agents outlive Retake"). A poll also
  carries the chat turn the worker runs (`chat`, `chat_lease`) and its answer the turn it owns (`chat`): a turn and
  a task never run at once (spec/chat.md). In Local mode
  the in-process worker reaches these handlers without HTTP or a token.
- Existing video URLs serve remote versions through the relay. Existing viewing permissions apply to all media.
- The bar has one action for everyone who can record: **Send**. It says just "Send", never an agent's name; how many
  comments it sends (the shown version's open comments no task owns, or a paused one does, and no `sending` take
  holds) is in its tooltip
  and its accessible name ("Send 3 comments to the agent"). It finishes the take in progress with `send: true` on its
  `/done`, saves a pending typed comment (spec/notes.md, "Typed comments on a video"), and submits the shown
  version's other open comments. With no computer online it does the same: the comments show Working, "Waiting for
  the editing computer", and start when one connects. In local mode, when the built-in worker can't edit
  (`blocked`), Send saves the take without `send`. There is no other action. Its toast says where the comments went
  ("Sent to v8.": their `target`).
- **Choosing the agent for one send.** Holding Send (about 500 ms, mouse or touch; a right-click too) opens a small
  list of the agents the editing computer offers, when it offers more than one: the pick goes as `agent` on that
  send's `/done` body and its `POST /api/automation/tasks` (any role that can record; the server takes it only when
  the selected worker offers it, else the configured agent; a send that joins a task already waiting keeps that
  task's agent). With one agent, holding Send is a plain Send.
- **No agent menu in the bar.** The agent, its fallback, where agents run and connecting a computer are in Settings
  (the home's left nav, spec/home.md). Only where Send can't reach an agent does one quiet line say so, over the bar's
  right end: "No editing computer connected · Settings" for the owner of a hosted project with no computer online,
  "The editing computer is offline. Send still queues." for anyone else, and in local mode the `blocked` reason.
- **While the agent works** ("Marking progress"), on the computer and the phone alike. There is no line or pill over
  the comments (it covered the list on a phone).
  - The comment being worked on (the task's `working`) has a soft light sweeping across its card and, under its
    words, one quiet italic line in the agent's words ("slowing the mask fade…"). One comment at a time. Its marker on
    the timeline pulses softly. The list never scrolls to it by itself.
  - A comment marked done fades and moves out of To do, and into Done, as its mark arrives: short, transform and
    opacity only, the cards under it sliding up into its place (nothing jumps). In Done it shows its change and "in
    v5", which plays v5 (its newest revision while it's live) at that comment's moment. A comment the report finishes
    without a mark moves the same way then.
  - While a task of this video runs, a slow light in the record red travels around the edge of the video's frame
    (part `agent-edge`, token `--agent-edge`, spec/theme.md): a moving segment of light, never a steady border, so it
    can't be taken for recording. It is the live version's mark ("Versions versus drafts"). It stops when the task
    ends, when every comment it still has to do waits on a question, while you record, and on an extension's version. With `prefers-reduced-motion` it is a faint, still red
    hairline instead. While the editing computer is offline, the working comment's line says "Reconnecting to the
    agent".
  - Each comment's `target` (and `later`) comes from the notes API, and the page says it as "→ v8" (spec/notes.md,
    "Where a comment goes").
- Per-comment actions are in the card's ⋯ menu: send one, retry, history and delete as the role allows (send one and
  retry add to their target's task, as any send); switch agent and pause for the owner; reopen for approvers. A
  question an agent asked from inside its task (`retake ask`) shows under its comment, with a reply box that takes
  typed words or a tapped choice (`…/answer` with `question`) or a recorded answer (`answer_to`), and as a marker on
  the timeline at its moment, in the Question color; clicking the marker (or the question's time) seeks there and
  shows its visuals ("What the page shows"). A report's question (the fallback) takes a typed answer (`…/answer`); a
  question asked through the notes takes a recorded one (`answer_to`), in the same reply box. Comment cards show
  current task state and result links regardless of which version is selected.

## Required verification

Use disposable native timelines and generated media. Test persistence, single claim, lease fencing, cancellation
before switching, rate-limit handoff, final submission versus incremental transcription, questions and replies,
role restrictions, revoked tokens, bounded relay, path safety, server/worker reconnect, cached incremental
rendering and actual browser playback after source changes. Exercise both existing saved comments and new
submissions, Send by every recording role (a share link too) with no worker online and with automation off,
`/done` with and without `send`, a sent take queuing without a worker poll, drafts versus versions, a worker restart
with unchanged source, profile `env` expansion, Local mode's in-process worker and `blocked` reasons, the exact herdr
and tmux commands (fake binaries on `PATH` that log their argv), completion by report with no agent output, stop
closing the pane, and two projects' tasks running at the same time. For "Agents outlive Retake" (fake herdr, tmux and agent): a
worker shut down mid-task leaves the pane and the process running; a new worker takes a live herdr task back without a
new line; a report written while it was down is processed; a dead agent pauses the task; a detached background agent
survives its worker and is taken back with its usage; a chat turn survives and keeps streaming; a person's stop still
kills or closes; the lease's grace and `adopt` on the server; and no test leaves a process behind. For "Comments on vN become edits in vN+1": comments on v7 make v8;
another comment on v7 after v8 is done updates v8 (still the same number of versions, the earlier media kept); a
comment on v8 makes v9; one Send with comments on v7 and v8 updates v8 then makes v9, by the same agent, one after the
other; a comment on v7 while v9 exists updates v8 and says `later`; a send of 3 notes is one task, one agent process
(a fake agent counting its launches) and one version; 20 sent, then 1 more mid-run for the same target, is one task
and one version with all 21 (a follow-up round of the same agent in the background; a message in the same session in
herdr and tmux, fake binaries); 5 queued single-note tasks of an older store become one task when it opens; 75 paused
single-note tasks for one target become one paused batch, and a Send (with one more note) resumes it as one task; a
retry and a send while a task is queued join it; a withdrawn note drops out of its task; a queued task waits for a sent take
still being processed; two videos run at the same time; and an e2e test where Send with several comments yields one
version. For `retake ask`: the built-in profiles' argv, the
CLI's parsing and validation of visuals, their persistence and the hosted relay of their images, a fake agent that
asks mid-task and then completes (Local mode and hosted), a wrong or stale secret refused, an answer reaching a herdr
and a tmux session (fake binaries) and a background attempt's context, a task ending with some notes Done and some
Question, a note whose still the server can't give (a fake agent that gets the worker's own still, its pen line on it),
an attached image that can't be fetched, the agent's visuals drawn in their own style (a web unit test), and an e2e test where an agent's question
with a box, an arrow and two image choices is answered with one tap. For "Marking progress": `retake working` and
`retake done` through the endpoint (Local mode and hosted), a wrong or stale secret and a chat's secret refused, one
working note at a time, a mark taken back by `working` and by `retake ask`, a retracted note leaving its marks, and the
report reconciling (a note never marked gets the report's words, a marked one its own unless the report has an entry,
a marked note held by a question stays Question, a paused attempt finishes nothing); a web unit test of the list's
states and moves; and an e2e test (Chromium on a computer and a phone, WebKit on a phone) where a fake agent marks
notes working and done one by one with pauses: they move to Done one at a time, the sweep and the line are on the right
card, there is no pill, and the edge light is on while it works. Provider adapters are tested with deterministic
executables; live paid-provider execution is reported separately if it is not exercised.
