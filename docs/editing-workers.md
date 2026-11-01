# Live editing from a hosted review

Retake can host the review while your editing machine keeps the project's source, footage, renderer and agents.
The machine connects outward. Reviewers use the normal project link and need no local files, Git checkout or
agent installation. Native timeline projects are supported; the worker currently runs on Linux and macOS.

## Connect an editing machine

Create a project and an agent token on the Retake host. An empty project is fine; its first video can arrive as a
live stream without a media upload. On the machine holding the native project:

```sh
retake remote add team-video https://review.example.com/p/team-video --token YOUR_AGENT_TOKEN
retake worker --remote team-video --project /path/to/video-project --name "Editing workstation"
```

`retake-edit` must be installed or configured with `[edit] retake_edit` in Retake's user configuration. The project
must name its native timeline in `retake.toml` (`[edit] timeline = "timeline.json"`), or use `edit/timeline.json`.
Install and sign in to Claude Code or Codex on this machine. Their existing model and authentication settings are
used. Retake does not copy those credentials to the host.

The first connected machine becomes the stream source. Open the project's review link and use **Agent** to choose
the machine and default agent. Enable **Start edits when feedback is submitted** to authorize automatic editing.
You can also select a fallback agent for usage limits; the default is to pause and let you choose.

## Review and follow the edit

Record feedback normally and choose **Finish review**. Once final transcription and note processing finish, the
comments enter the queue. Incremental draft transcripts do not start an editor. Existing comments have a **Send to
agent** action, and the Agent panel can submit the visible open comments together.

Each comment shows **Commented**, **In progress**, **Question**, or **Completed**. A question has a reply field;
replying continues the same task. A usage limit or connection failure appears as a pause reason. Owners can pause,
retry or switch the assigned agent on the comment. A switch stops the old process before launching the new one,
preserving the working files, original feedback, drawings and task history.

Source saves trigger incremental HLS rendering. The worker reuses cached sections, and the browser follows the
new revision once it can play the current position. Selecting a historical version pins it; recording also stays
on its original version. Rendered revisions retain their source snapshot and output on the editing machine.
Completion requires both an agent change summary and a verified finished render. Reviewer approval remains separate.

The host relays playlists and segments on demand through the worker's outbound connections. It does not need a
completed export or access to the source footage. Keep the worker running while reviewing; uncached media is
unavailable while that machine is offline. Configure a reverse proxy to allow responses lasting at least 30 seconds
and disable buffering for the existing `/api/events` SSE route.

## Agent profiles and diagnostics

For specific models or local wrappers, pass `--profiles /path/to/profiles.json`. This file is local configuration;
commands are never supplied by reviewers or the host. For example:

```json
[
  {
    "id": "codex",
    "name": "Codex",
    "command": ["codex", "exec", "--json", "--sandbox", "workspace-write", "-c", "approval_policy=\"never\"", "--skip-git-repo-check", "-"]
  },
  {
    "id": "claude",
    "name": "Claude Code",
    "command": ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
  }
]
```

Profiles receive the task prompt on stdin. Local context and report paths are also in `RETAKE_CONTEXT` and
`RETAKE_REPORT`. Agents must write a JSON report with `state` (`completed` or `question`) and `message`. The built-in
prompt explains this contract. Agent permissions remain those of the local CLI; Retake does not bypass its sandbox
or approval settings. Set any additional trusted tool permissions locally when your workflow needs them.

Task context, capped provider logs and reports are under `.retake/worker/tasks/`. Revisions are under
`.retake/worker/renders/`. Keep that directory to retain historical playback. Reported tokens and costs appear in
task history; costs are estimates, and unavailable subscription balances are not guessed. A disconnected task pauses
after its lease expires; reconnect and retry it to continue from the preserved source.

The current relay is held by one Retake server process. Route a project's requests to that same process (sticky
routing if there are multiple replicas). Durable state supports SQLite and Postgres; distributing media relay queues
across server replicas is not implemented.
