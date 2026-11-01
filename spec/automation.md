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
- There is at most one editing task running in a project. Task state, assignments, attempts, questions, answers
  and results survive server restart. A lease identifies each attempt; late reports from a replaced attempt are
  rejected. A local process lock prevents duplicate workers for one project.
- Switching an active task first requests cancellation. The worker stops the process tree, preserves files and
  its task record, then acknowledges the handoff. Only then may the replacement agent start. On lost connectivity
  the worker stops editing before its lease can expire. An offline worker does not imply task completion.
- Agent usage failures pause a task with an explanation and a Switch agent / Retry action. No silent change to
  billing method. Available structured token/cost reports are recorded as estimates; absent limits remain unknown.

## Watching the agents: background, herdr or tmux

Every project (one video family) has its own worker, and so its own agent: ten projects with work run ten tasks at
the same time, one per project. Nothing serializes tasks across projects.

Where a worker runs its agent is a user setting: `open_agents_in` in the user config, or `open_in` on one
`[[agents]]` entry, which wins (docs/config.md). Values: `"background"` (the default), `"herdr"`, `"tmux"`.
`--profiles FILE` entries take `open_in` and `interactive` too.

- `background`: as above. The agent's headless command (`command`) runs in the project folder with the prompt on
  stdin, its output goes to `tasks/<task>/agent-<attempt>.log`, and its JSON stream gives usage and usage limits.
- `herdr` and `tmux`: the agent runs as a normal interactive session, which the person can watch and type into at any
  time. Its command is the profile's `interactive` argv: the built-in `claude` is `claude --permission-mode
  acceptEdits` and `codex` is `codex --sandbox workspace-write -c approval_policy="never"`. A configured agent without
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
- The session stays open after the report, so the person can scroll back and keep talking. The project's next task
  reuses it while it's there and runs the same agent: the line goes into the running agent, or the agent starts again
  in the same pane when it had exited. A different agent replaces it (herdr: its pane is closed and a new tab opens in
  the workspace; tmux: the pane is respawned). The pane, and in herdr the workspace and agent name, are kept in
  `.retake/worker/view.json`.
- Completion is unchanged: the report file plus a verified render. Nothing is read from the terminal. A session that
  ends before a report (the pane is closed, the agent quits) pauses the task: "The agent's session ended without a
  report". An agent that stops at a question or a usage limit in the terminal, without a report, leaves the task
  Working until it reports, its session ends, or someone pauses it.
- Stopping a task before its report (pause, switch agent, lost connection, retake quitting) closes the agent's pane:
  `herdr pane close` or `tmux kill-pane`. After the report it's left open.
- No usage is recorded in these modes: there's no JSON stream to read it from. Usage limits show in the session.
- An agent that stops at a startup question in herdr (such as "trust this folder?") makes `herdr agent start` answer
  `agent_not_ready`: the worker logs that the agent is waiting for the person and waits (`herdr agent wait`) before
  sending the line. A missing `herdr` or `tmux` binary, or a herdr or tmux error, pauses the task with that error.

## Connect your computer: `retake connect`

```
retake connect [<server URL> --token rk_…] [--folder DIR] [--name NAME] [--profiles FILE]
```

One command connects a whole projects folder to a hosted server with a machine token (spec/server.md, "Machines";
the browser's Connect your computer card prints the line). It is `retake worker` for every project, in one process.

- The folder is `--folder`, else the current folder. It checks the token first (`GET /api/machine`; a bad token or
  URL stops it with exit 1).
- It finds the projects as `retake open <folder>` does (spec/home.md): every folder with a versioned video, with or
  without a native timeline (spec/bridge.md), so the server lists what `retake open <folder>` lists. A candidate
  folder (the folder itself or one directly in it, not hidden) with a timeline but no version yet counts too, named
  by the same rule.
- For each project it sends its name and title (`POST /api/machine/projects`, which makes it when it's missing) and,
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
- A task's prompt says the project has no timeline and names its newest version; the task context lists every
  version under `family`, each with its path relative to the project. The prompt asks the agent to make the change
  with the tools it has (ffmpeg, say), starting from the version the notes were made on, and to write the result as
  one video file to `$RETAKE_OUTPUT` (`.retake/worker/tasks/<id>/output.mp4`). It never edits or deletes the
  existing versions.
- When the agent reports `completed`, the worker pushes the result as the next version, by the rule of
  `version_push` (spec/notes.md): named after the newest version with the next `v<N>`, in a sibling `v<N>/` folder
  when the family uses version folders. A video the agent wrote as that next version itself counts as well. The
  version must probe with a duration; otherwise the task pauses ("The agent's video doesn't play"). Without a
  result the task pauses too ("The agent finished without writing a new version").
- Then the worker sends its files (hosted), and reports `completed` with `"version": "<label>"` instead of a render.
  The server takes it once that label is a version file of its family that isn't a render: the task's notes become
  Done, answered in that version, with the agent's message as the change. Questions work as for timeline projects.
- The agent doesn't run `retake push`: the worker's push puts the file into the project folder, and the server only
  gets its metadata.

## Submission and comment state

- Recording and incremental transcription do not launch editing. Send closes the take (`/done` with
  `"send": true`) and queues its processing. Once the final notes job succeeds, the take's open notes enter the
  editing queue: right away, in the process that ran the job, whether or not a worker is connected. A `/done`
  without `send` (an older page) queues them only when `config.enabled` is on. Each finished take is considered
  once: if neither applies, its notes stay Open until someone sends them, and turning `enabled` on later doesn't
  sweep up old reviews. Finishing again with `"send": true` sends a take that was already processed. Send also
  submits the other open comments (`POST /api/automation/tasks`); that always queues. Submission is idempotent
  while an unfinished task owns a note. Reopened notes may be submitted again.
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
  completes feedback.
- A question and its answer stay on the task/comment. Answering resumes the same task. Operational pauses (usage
  limits, offline, failed render) are displayed separately from creative questions.
- Every task records the original note IDs and versions. Completion links the original notes to the resulting
  version. The agent receives the current source, original feedback, prior task events, and a local durable task
  directory. Switching providers does not pretend to resume another provider's session.

## Live rendering and transport

- The worker monitors native source inputs, debounces stable changes, and snapshots each valid source revision.
  `retake-edit` incrementally renders HLS using its existing dependency cache. A newer source revision cancels
  obsolete work and starts the latest revision; the last playable result stays available while that happens.
  Cancelled revisions retain their playable portion and display as stopped, rather than continuing to claim that
  they are rendering. Starting an agent alone does not create an unchanged intermediate version.
- Only a task renders. Outside a task the worker renders just once, for a project with no version yet (the poll
  answer's `versions` is 0), so an empty project gets its first video; plain saves render nothing. Each render's
  `state.json` stays in `.retake/worker/renders/<id>/`, and on start the worker takes the newest complete one as
  its last render, so a restart with unchanged source renders and registers nothing.
- Versions versus drafts. A new label `v<N>` is created only when a task completes, for its verified final render,
  or for that first render of an empty project. Every other render is a draft, labelled `v<N>-draft<K>`, where
  `v<N>` is the version it is heading for and `K` counts that version's drafts. Completion promotes the task's
  final draft to `v<N>`: same media, new label, and the old draft label keeps resolving to it. The family lists
  each version, plus the newest draft and the newest complete draft while they're ahead of the newest version, so
  following latest keeps showing the agent's work live. Older drafts aren't listed but stay playable by URL, and
  takes recorded on them still save.
- The host registers each immutable render revision in the video family as above. Pending/progressive render
  metadata reaches browsers through the existing events API. Following latest switches to the new revision once
  it can play the current position; recording and an explicit historical selection remain pinned.
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
- `POST /api/automation/tasks` (submit feedback), `.../tasks/<id>/answer` and `.../tasks/<id>/retry`: any role that
  can record. A retry's `agent` counts only from the owner. Switching, pausing and the config are owner actions.
- `/api/worker/*`: Bearer agent token only, including local requests. Worker registration, polling/heartbeats,
  attempt reports, render registration/progress, version files (`files`), note artifacts and relay response
  endpoints. An attempt's `completed` report names a render (`result`) or, in a plain project, a version file
  (`version`). The poll answer has
  `config`, `task`, `notes` and `versions` (how many versions the family has, drafts not counted). In Local mode
  the in-process worker reaches these handlers without HTTP or a token.
- Existing video URLs serve remote versions through the relay. Existing viewing permissions apply to all media.
- The bar has one action for everyone who can record: Send (`Send N to agent`, N the open comments no task owns
  and no `sending` take holds). It finishes the take in progress with `send: true` on its `/done` and submits the
  other open comments. With no computer online it does the same: the comments show Working, "Waiting for the
  editing computer", and start when one connects; the owner's press also opens the agent popover with the connect
  command (`POST /api/connect`). In local mode, when the built-in worker can't edit (`blocked`), Send saves the take
  without `send`, and the popover says what's missing (it doesn't open by itself). There is no other action.
  Next to it, the agent's name with a dot (green: online) opens that popover. The owner chooses the agent, its
  fallback ("Pause and ask me" by default) and the computer there; everyone else sees the agent, or the one line
  "Waiting for the editing computer". Per-comment actions are in the card's ⋯ menu: send one, retry, history and
  delete as the role allows; switch agent and pause for the owner; reopen for approvers. A worker's question takes
  a typed answer (`…/answer`); a question asked through the notes takes a recorded one (`answer_to`), in the same
  reply box. Comment cards show current task state and result links regardless of which version is selected.

## Required verification

Use disposable native timelines and generated media. Test persistence, single claim, lease fencing, cancellation
before switching, rate-limit handoff, final submission versus incremental transcription, questions and replies,
role restrictions, revoked tokens, bounded relay, path safety, server/worker reconnect, cached incremental
rendering and actual browser playback after source changes. Exercise both existing saved comments and new
submissions, Send by every recording role (a share link too) with no worker online and with automation off,
`/done` with and without `send`, a sent take queuing without a worker poll, drafts versus versions, a worker restart
with unchanged source, profile `env` expansion, Local mode's in-process worker and `blocked` reasons, the exact herdr
and tmux commands (fake binaries on `PATH` that log their argv), completion by report with no agent output, stop
closing the pane, and two projects' tasks running at the same time. Provider adapters are tested with deterministic executables; live paid-provider execution is reported
separately if it is not exercised.
