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
  processing. Once the final notes job succeeds, submitted open notes enter the editing queue when automation is
  enabled. The browser can also submit selected existing open comments explicitly. Submission is idempotent while
  an unfinished task owns a note. Reopened notes may be submitted again.
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
- Agent controls in the browser show worker availability, default agent, per-task assignment and history, questions,
  pause reasons, and available usage estimates. Comment cards show current task state and result links regardless
  of which version is selected.

## Required verification

Use disposable native timelines and generated media. Test persistence, single claim, lease fencing, cancellation
before switching, rate-limit handoff, final submission versus incremental transcription, questions and replies,
role restrictions, revoked tokens, bounded relay, path safety, server/worker reconnect, cached incremental
rendering and actual browser playback after source changes. Exercise both existing saved comments and new
submissions. Provider adapters are tested with deterministic executables; live paid-provider execution is reported
separately if it is not exercised.
