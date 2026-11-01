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
  why in `blocked`, one short sentence for the page: no native timeline, `retake-edit` not found, no agent found
  ("No agent found. Install Claude Code or Codex."), or another worker already holds the project's lock. It checks
  again every 15 seconds, so installing the missing piece needs no restart.
- `retake worker --remote` (and a hosted server) keep the remote model below. One project folder is edited by one
  worker at a time: a local and a remote worker on the same folder share its lock, and the second one waits.

## Ownership and execution

- `retake worker --remote NAME --project DIR --name NAME` connects to a remote registered by `retake remote add`.
  The project must contain a native timeline. The worker advertises locally installed Claude Code and Codex
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

## Connect your computer: `retake connect`

```
retake connect [<server URL> --token rk_…] [--folder DIR] [--name NAME] [--profiles FILE]
```

One command connects a whole projects folder to a hosted server with a machine token (spec/server.md, "Machines";
the browser's Connect your computer card prints the line). It is `retake worker` for every project, in one process.

- The folder is `--folder`, else the current folder. It checks the token first (`GET /api/machine`; a bad token or
  URL stops it with exit 1).
- It finds the projects as `retake open <folder>` does (spec/home.md), and keeps those with a native timeline
  (spec/bridge.md). A candidate folder (the folder itself or one directly in it, not hidden) with a timeline but no
  version yet counts too, named by the same rule. Projects without a timeline are listed once as skipped: the worker
  streams timeline renders only, so their videos can't play from this computer.
- For each project it sends its name and title (`POST /api/machine/projects`, which makes it when it's missing) and,
  when it has a version, that version's first frame as the poster (`PUT .../poster`, the JPEG of spec/home.md's
  poster). Nothing else leaves the computer: no footage, source or renders. Video plays through the worker's relay.
- Then it runs one worker per project (as `retake worker`: the project URL is `<server URL>/p/<name>`, the machine
  token is its agent token, `--name` and `--profiles` as there). A worker that stops with an error (another worker
  holds the project, the renderer is missing) is started again at the next look, and the same error is printed once.
- It looks at the folder again every 10 s: a new project is sent and started; a project that is gone is stopped.
- It saves the server URL, the token and the folder in `$XDG_CONFIG_HOME/retake/connect.toml` (mode 0600), so a bare
  `retake connect`, from any folder, connects the same folder to the same server again. Ctrl+C stops every worker.
- `retake worker --remote NAME --project DIR` stays, for one project with that project's agent token.

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
- Browser media requests are relayed to the connected worker over worker-initiated HTTP requests. Only registered
  revision directories and relative, regular media files are readable. Traversal, symlink escapes, arbitrary URLs,
  and executable paths are rejected. The relay is bounded, cancellable, expires unanswered requests and returns
  an offline error when no worker is available. No original footage or project directory is exposed.
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
  attempt reports, render registration/progress, note artifacts and relay response endpoints. The poll answer has
  `config`, `task`, `notes` and `versions` (how many versions the family has, drafts not counted). In Local mode
  the in-process worker reaches these handlers without HTTP or a token.
- Existing video URLs serve remote versions through the relay. Existing viewing permissions apply to all media.
- The bar has one action for everyone who can record: Send (`Send N to agent`, N the open comments no task owns
  and no `sending` take holds). It finishes the take in progress with `send: true` on its `/done` and submits the
  other open comments. With no computer online it does the same: the comments show Working, "Waiting for the
  editing computer", and start when one connects; the owner's press also opens the agent popover with the connect
  command (`POST /api/connect`). In local mode, when the built-in worker can't edit (`blocked`), Send saves the take
  without `send`, and the first press opens the popover, which says what's missing. There is no other action.
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
with unchanged source, profile `env` expansion, and Local mode's in-process worker and `blocked` reasons. Provider adapters are tested with deterministic executables; live paid-provider execution is reported
separately if it is not exercised.
