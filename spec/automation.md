# Hosted editing workers and live output

Retake owns durable tasks and review state. A designated machine connects outward to a project on the server,
executes locally configured agents, and serves its renderer's output through an authenticated HTTP relay. Source,
footage, agent credentials and render caches stay on that machine. The host does not require a completed media
upload or an inbound connection to the worker. This is an addition to the existing CLI/MCP integrations.

## Ownership and execution

- `retake worker --remote NAME --project DIR --name NAME` connects to a remote registered by `retake remote add`.
  The project must contain a native timeline. The worker advertises locally installed Claude Code and Codex
  profiles. Optional local configuration can define additional executable argv profiles; commands never come
  from a reviewer or the hosted service.
- A project owner chooses its worker and default agent in the browser. A task may override that agent. Credentials
  are supplied by the agent's own local configuration; they are never returned by the worker or shown to viewers.
  The first connected worker is selected as the initial stream source, so an empty hosted project can show its
  first video without an upload. Automatic feedback execution remains disabled until the owner enables it.
- There is at most one editing task running in a project. Task state, assignments, attempts, questions, answers
  and results survive server restart. A lease identifies each attempt; late reports from a replaced attempt are
  rejected. A local process lock prevents duplicate workers for one project.
- Switching an active task first requests cancellation. The worker stops the process tree, preserves files and
  its task record, then acknowledges the handoff. Only then may the replacement agent start. On lost connectivity
  the worker stops editing before its lease can expire. An offline worker does not imply task completion.
- Agent usage failures pause a task with an explanation and a Switch agent / Retry action. No silent change to
  billing method. Available structured token/cost reports are recorded as estimates; absent limits remain unknown.

## Submission and comment state

- Recording and incremental transcription do not launch editing. Finish review closes the take and queues its
  processing. Once the final notes job succeeds, submitted open notes enter the editing queue when automation is
  enabled. The browser can also submit selected existing open comments explicitly. Submission is idempotent while
  an unfinished task owns a note. Reopened notes may be submitted again.
- A comment shows one of four states: Open, Working, Question, Done (the page maps them in `web/src/lib/status.ts`).
  No task, a cancelled one, or a paused one is Open (a pause adds its reason once, such as "Paused: usage limit",
  with Retry); queued, running and stopping are Working; an agent's question is Question; completed, answered and
  accepted are Done. Existing approval and retraction remain intact. Working comes from a task. Done requires a
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
- The host registers each immutable render revision in the video family. Pending/progressive render metadata
  reaches browsers through the existing events API. Following latest switches to the new revision once it can
  play the current position; recording and an explicit historical selection remain pinned.
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

- `/api/automation`: GET for viewers, configuration changes for owners only.
- `/api/automation/tasks`: submit feedback as a reviewer; task assignment, cancellation, retry and switching are
  owner actions. Answers can be submitted by reviewers.
- `/api/worker/*`: Bearer agent token only, including local requests. Worker registration, polling/heartbeats,
  attempt reports, render registration/progress, note artifacts and relay response endpoints.
- Existing video URLs serve remote versions through the relay. Existing viewing permissions apply to all media.
- The bar has one action. The owner's is Send (`Send N to agent`, N the open comments no task owns): it finishes
  the take in progress with `send: true` on its `/done` and submits the other open comments. Other reviewers have
  Finish review. With no computer to send to, Send saves the take and opens the agent popover, which shows the
  connect command (`POST /api/connect`), or, in local mode, why the built-in worker can't edit. Next to the action,
  the agent's name with a dot (green: online) opens that popover: the agent, its fallback ("Pause and ask me" by
  default) and "Send other reviewers' comments automatically" (`config.enabled`). Per-comment actions (send one,
  retry, switch agent, pause, history, reopen, delete) are in the card's ⋯ menu. A worker's question takes a typed
  answer (`…/answer`); a question asked through the notes takes a recorded one (`answer_to`), in the same reply
  box. Comment cards show current task state and result links regardless of which version is selected.

## Required verification

Use disposable native timelines and generated media. Test persistence, single claim, lease fencing, cancellation
before switching, rate-limit handoff, final submission versus incremental transcription, questions and replies,
role restrictions, revoked tokens, bounded relay, path safety, server/worker reconnect, cached incremental
rendering and actual browser playback after source changes. Exercise both existing saved comments and new
submissions. Provider adapters are tested with deterministic executables; live paid-provider execution is reported
separately if it is not exercised.
