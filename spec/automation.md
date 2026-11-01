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
  first video without an upload. Sending feedback (below) always reaches the selected worker; `config.enabled`
  only decides whether finished reviews that weren't sent are sent automatically, and it is off until the owner
  turns it on. Turning it off doesn't stop a running task (pause the task for that).
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

- Recording and incremental transcription do not launch editing. Finish review closes the take and queues its
  processing. Once the final notes job succeeds, the take's open notes enter the editing queue when the request
  said `"send": true`, or else when `config.enabled` is on. Each finished take is considered once: if neither
  applies, its notes stay Commented until someone sends them, and turning `enabled` on later doesn't sweep up old
  reviews. Finishing again with `"send": true` sends a take that was already processed. The browser can also
  submit selected existing open comments explicitly (Send); that always queues, whatever `config.enabled` says.
  Submission is idempotent while an unfinished task owns a note. Reopened notes may be submitted again.
- Public comment states are Commented, In progress, Question, Completed. Existing approval and retraction remain
  intact. In progress comes from a live task claim. Completed requires a successfully rendered result plus an
  explicit per-note change description; an agent process exit alone never completes feedback.
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
  `"blocked": "<why>"` while that worker can't run. Then `workers` may not list it.
- `/api/automation/tasks`: submit feedback as a reviewer; task assignment, cancellation, retry and switching are
  owner actions. Answers can be submitted by reviewers.
- `/api/worker/*`: Bearer agent token only, including local requests. Worker registration, polling/heartbeats,
  attempt reports, render registration/progress, note artifacts and relay response endpoints. The poll answer has
  `config`, `task`, `notes` and `versions` (how many versions the family has, drafts not counted). In Local mode
  the in-process worker reaches these handlers without HTTP or a token.
- Existing video URLs serve remote versions through the relay. Existing viewing permissions apply to all media.
- Agent controls in the browser show worker availability, default agent, per-task assignment and history, questions,
  pause reasons, and available usage estimates. Comment cards show current task state and result links regardless
  of which version is selected.

## Required verification

Use disposable native timelines and generated media. Test persistence, single claim, lease fencing, cancellation
before switching, rate-limit handoff, final submission versus incremental transcription, questions and replies,
role restrictions, revoked tokens, bounded relay, path safety, server/worker reconnect, cached incremental
rendering and actual browser playback after source changes. Exercise both existing saved comments and new
submissions, Send with automation off, Finish with and without `send`, drafts versus versions, a worker restart
with unchanged source, profile `env` expansion, and Local mode's in-process worker and `blocked` reasons. Provider adapters are tested with deterministic executables; live paid-provider execution is reported
separately if it is not exercised.
