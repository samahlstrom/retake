# Chat with the agent (core, for an extension's kind)

This is the contract for the chat. Change it here first.

On a script (spec/scripts.md) the person doesn't want to edit their own document. They talk to the agent instead:
"pull this link in", "put an image of a neon sign under the cold open", "make the hook shorter". The agent edits the
document while they watch, answers in a few words, and asks with choices when it should. When they like the script,
Send makes the video. Everything is in one place.

The chat is core's, like the comments column it replaces. A kind of version opts in (`chat = true`), and the agent's
instructions come from that kind's extension.

## Where it shows

- A kind with `chat = true` (spec/extensions.md, "Version kinds") has a chat in place of the comments column on its
  version: no To do, no Done, no comment line. On a phone it is the sheet the comments use.
- Other versions keep their comments. A video's chat is one thread for the whole video, shown on its kind's version.

## The thread

One thread per video (project), with one agent session.

- **Stored** in the project's store (spec/notes.md, "Project"), so it survives reloads and restarts: the `chat` table
  (schema step 7) holds the messages, and the automation state (spec/automation.md) holds the turn and the session
  under `chat`. Nothing goes in the video's own folders, `v0/` included; hosted, the server keeps it as it keeps the
  comments.
- **A message** is `{id, role, by, text, created, turn?, images?, voice?, options?, answer?, anchor?, streaming?,
  waiting?}`:

  | role | What | Fields |
  |---|---|---|
  | `person` | What the person typed or said | `by` (their name), `images` (paths in the video's folder, `v0/images/x.png`), `voice` (the recording's path), `answer` (the question it answers), `waiting` while no turn has it yet |
  | `agent` | The agent's words, Markdown | `streaming` while the turn still adds to it |
  | `tool` | One quiet line for what the agent is doing ("Editing the script") | |
  | `question` | `retake ask` from inside a turn | `options` (`[{text, image?}]`), `images`, `anchor`, `answer` once picked; its images are URLs (`api/chat/files/…`) |
  | `info` | One quiet line from Retake ("Stopped", "Usage limit reached") | |

- A text is at most 20000 characters, a message at most 8 images. Image paths are owned files of the kind's
  extension (`v0/images/<name>`), checked like the files routes' paths (no `..`, no hidden segments).

## Turns

A **turn** is one run of the agent for the person's messages.

- `POST api/chat` adds a `person` message. With no turn, it makes one, `queued`, carrying it. While a turn is
  `queued` the message joins it. While one runs, the message is `waiting`, and when that turn ends a new turn takes
  every waiting message. So nothing typed is lost, and the agent gets it next.
- **One agent per video.** A turn runs on the video's editing worker (spec/automation.md, "Ownership and execution"),
  never next to an editing task: the worker takes a queued turn only while no task of the video runs, and takes no
  task while a turn runs. A turn waiting on a task says so: "The agent is busy with v3's edits" (the task's target).
  A queued turn goes before queued tasks (turns are short, and the person is waiting on the reply).
- **The agent** is the one picked in Settings (`config.agent`), or the one a message names (`agent`, as Send's
  long-press, when the editing computer offers it).
- **Where it runs**: on the editing computer, always. Locally the in-process worker; hosted the computer's worker
  over its existing connection. The browser never runs or reaches the agent. With no computer online the turn waits
  and the page says "The editing computer is offline"; locally, the worker's `blocked` reason.
- **Stop** (`POST api/chat/stop`) stops the running turn's process (its process group) and nothing else: no task, no
  other video. Its text so far stays, with an `info` "Stopped". Waiting messages go in the next turn.
- **Retake quitting or restarting** stops nothing: the turn's agent runs detached and writes its output to files, and
  the restarted worker takes the turn back and streams on from where the server has its words (spec/automation.md,
  "Agents outlive Retake").
- **A lost worker** (its lease runs out and the 10-minute grace after it, spec/automation.md) ends the turn with "The
  editing computer went offline"; waiting messages go in the next turn when it's back.

### The agent's command

The worker runs the profile's headless `command` (spec/automation.md, "Permissions") in the video's folder, with
the turn's prompt on stdin (`chat/<turn>-<i>.md`), detached: its stdout goes to `chat/<turn>-<i>.out`, which the worker
reads as it grows, and its stderr to `chat/<turn>-<i>.log`. The turn's record is `chat/turn.json` (spec/automation.md,
"Agents outlive Retake"). The environment is the worker's, plus the profile's `env`, plus `RETAKE_ASK_URL` and
`RETAKE_ASK_TOKEN` (a fresh secret per turn) so `retake ask` lands in the chat. No `RETAKE_CONTEXT`,
`RETAKE_REPORT` or `RETAKE_OUTPUT`: a turn has no report. herdr and tmux (`open_in`) don't apply: a turn is always a
background process.

- **Claude Code** (a command whose program is `claude` with `-p`): `--include-partial-messages` is added when it
  writes `stream-json`, so the text arrives as it's written. The stream's `session_id` is kept, and the next turn
  adds `--resume <session_id>`.
- **Codex** (`codex exec`): the stream's `thread.started` `thread_id` is kept, and the next turn runs
  `codex exec resume <the same flags> <thread_id> -`.
- **Other profiles** run once per turn, with the recent thread (up to 20 messages, 12 KiB) in the prompt.
- A session belongs to its agent: switching agents starts a new session, with the recent thread in the prompt. A
  resumed run that fails before it says anything is run again once as a new session, the same way.

### The prompt

The first turn of a session: a short preamble (the chat, the video's folder), then `## From the <name> extension`
with the kind's extension's `context` and `instructions` (spec/extensions.md, "Agent contributions"), then the chat's
rules, which win:

- Do what the person asks, then answer in a few plain sentences.
- The document is theirs: edit it in place, keep their own words, keep headings stable.
- A link alone on its own line shows as an embed. Fetch a page when you need what's in it.
- Images go in `v0/images/` and into the document as `![caption](images/<name>)`; the person's attached images are
  there already, and their paths are in the message.
- To have the person choose, `retake ask --option "…" … "question"` (no `--note`) and carry on; the answer comes as
  their next message.
- Don't render, push or change any video version, and don't touch `.retake/`: making the video is the person's Send.
- Nobody is in the terminal: never wait for input or use your own question tools.

Then `The person's message:` and the message (several, oldest first, when several waited), with its images' paths.
A resumed turn gets just a one-line reminder of the rules and the messages.

### What the page sees of a turn

The worker reads the agent's JSON stream and sends the chat only words and quiet lines, never raw JSON or tool
output:

- The agent's text as it arrives (Claude's `text_delta`s; Codex's `agent_message` items whole), as `agent` messages.
  Text after a tool is a new message.
- Each tool use as ONE `tool` line, the same line twice in a row only once: an edit of the kind's document "Editing the
  script" (the kind's label, lower case), a file written under `v0/images/` "Saving an image", another file
  "Editing <name>"; reading the document "Reading the script", another file "Reading <name>"; a web fetch "Reading
  youtube.com/…" (host and a short path); a web search "Searching for "<query>""; a shell command that downloads
  (`curl`, `wget`, `yt-dlp`) "Downloading from <host>"; `retake ask` nothing (the question shows itself); any other
  command "Running a command"; listing or searching files "Looking through the files". Thinking, todo lists and
  sub-agents add nothing.
- At the end, a usage limit is an `info` "Usage limit reached. Pick another agent in Settings or try later.", a
  process that failed is "The agent stopped without finishing", and a stop is "Stopped".

## Undo

Before every turn the worker keeps a copy of the extensions' owned files, as before an editing attempt
(spec/extensions.md, "Agent edit history"), with `task` = `chat-<turn id>`. So ⋯ → "Undo the agent's edit" undoes a
chat turn's edit, and undoing again goes one further back.

## Questions in the chat

`retake ask` inside a turn (the same command, spec/automation.md "Questions from inside a task"): the worker sends
it to `POST /api/worker/chat/ask` with the turn and its lease. Only the words, `--option`s (with their images),
`--image`s and `--file`/`--heading` are kept; times and frame visuals are refused, as for a document question. It
becomes a `question` message; its images are kept with the store (`.retake/chat/<message>/option_1.png`, ...) and
served at `GET api/chat/files/<message>/<file>`. The turn carries on. The CLI prints `asked in the chat (question
<id>): <text>`.

The page shows it as the agent's message with its choices to tap: image choices side by side, stacked on a phone.
Tapping one posts `{"text": "<choice>", "answer": <question id>}`: the question gets its `answer` (and no longer
offers choices) and the words start the next turn. Typing a reply works too.

## Images and voice

- Pasting, dropping or picking images in the composer saves each one in `v0/images/` with the kind's extension's
  files route (`PUT api/ext/<id>/files/v0/images/<name>`, `If-Match: *`, a new name when it's taken, 25 MiB at most),
  so it works hosted too, and the message carries their paths. The chat shows them inline.
- R or Record on a chat version records a **voice message**: the audio is saved as
  `v0/voice/chat/<yyyymmdd-hhmmss>.webm` through the same files route (apart from talk-outs, which a kind without a
  chat puts into its document itself), transcribed with the computer's `[transcribe]` (`POST api/ext/<id>/transcribe`), and
  its words are sent as the message (`voice` names the audio). It makes no comment. Without transcription the audio
  is kept and the job's hint shows as a toast.

## Send on a chat version: "I like it, make the video"

Send in the bar on a chat version sends the document to the agent to make the video. It never asks for confirmation;
the agent's status says what started ("Building v1…").

- `POST api/chat/send` `{"file": "v0/<video>_v0.md", "agent"?}` (needs record). With no video version yet it adds the
  typed comment "Build the first cut (v1) from the script." on the kind's version, wanting a version (spec/extensions.md):
  it makes v1. Once versions exist it adds a typed comment on the newest version at 0:00, "Bring the video in line
  with the script's changes (<file>).", which goes in the next version by the batch rule (spec/automation.md,
  "Comments on vN become edits in vN+1"): it joins a task already making that version, or starts one. Then it sends
  that comment (as `POST api/automation/tasks`). It answers `{"note", "target"}`.
- The send is remembered with the file's sha256 (`sent`). A second Send with the same sha256 answers 409 "Nothing
  changed since the last Send", unless that comment is open again with no task running it (a paused batch): then it
  is sent again, which resumes it.
- The page disables Send with a short reason, "Nothing changed since the last Send", while the document's sha256 is
  the sent one and that comment is working or done. It is never a silent no-op.

## HTTP

Under a video (`/p/<name>/…` on a server), relative to the page:

| Method | Path | Needs | Does |
|---|---|---|---|
| GET | `api/chat` | watch | `{"messages": [...], "turn", "busy", "offline", "blocked", "sent"}`: the newest 500 messages, oldest first; `turn` `{id, state, agent}` or null (`state` `queued`, `running` or `stopping`); `busy` "The agent is busy with v3's edits" or ""; `offline` while the selected computer hasn't been seen for 10 s; `blocked` the local worker's reason; `sent` `{file, sha256, note, at, open}` or null |
| POST | `api/chat` | record | `{"text", "images"?, "voice"?, "answer"?, "agent"?}`: 201 `{"message"}`. 400 for an empty text, a bad image path or an unknown question |
| POST | `api/chat/stop` | record | Stops the running turn: 200 `{"ok": true}`, 409 when none runs |
| POST | `api/chat/send` | record | Send on a chat version (above): 200 `{"note", "target"}`, 409 with the reason |
| GET | `api/chat/files/<message>/<file>` | watch | A question's image (`option_N.<ext>`, `img_N.<ext>`) |

**Events.** The page's event stream (`GET api/events`) gains `chat`:

- `{"why": "message", "message": {...}}`: a message was added or changed (an answer, the end of its streaming);
- `{"why": "text", "id": 12, "at": 120, "text": "…"}`: words added to message 12 at character `at` (the page appends
  them when it has exactly `at` characters, else reads `api/chat` again);
- `{"why": "turn", "turn": {...} | null}`: the turn started, changed or ended.

The worker sends text at most every 250 ms, so a reply is a few events a second.

**The worker's side** (spec/automation.md, "HTTP and UI"; agent token):

- The poll carries the running turn, `"chat": "<turn id>", "chat_lease": "<lease>"`, which renews its lease. Its
  answer has `chat`: the turn the worker now owns, `{id, lease, agent, session, messages, history, fresh, stop}`
  (`messages` the person's messages it carries; `history` the recent thread, for a new session; `stop` once Stop
  was pressed).
- `POST /api/worker/chat` `{"turn", "lease", "session"?, "events": [{"type": "text" | "tool", "text"}], "end"?:
  {"state": "done" | "stopped" | "failed", "info"?}}`: the stream so far and, last, its end. 200 `{"stop": bool}`;
  409 for a stale lease (the worker stops the turn).
- `POST /api/worker/chat/ask`: a question, `retake ask`'s body with `"task"` = the turn and its lease. 200 with the
  question as a `TaskQuestion` (`note` 0).

## Local and hosted

The same everywhere: the thread on the server's store, the agent on the editing computer, the words through the
worker's connection, the images through the files relay. Locally the server and the worker are one process and the
same routes are called in memory.

## Roles

A viewer reads the chat. A reviewer (and up) chats, stops a turn, answers and sends. The agent's permissions are the
profile's own (docs/agents.md: full access, unattended). As with comments, only share a video with people you'd let
use the editing computer that way.

## Tests

- Go: the store (messages, waiting, the turn's claim, one agent per video both ways, lease loss and its grace, stop,
  the session per agent, `sent` and Send's 409); the routes and roles; the worker with a fake agent (a script that
  prints Claude's stream-json, edits the document and runs `retake ask`): streamed text, one line per tool, `--resume`
  with the session on the next turn, a turn queued behind a task, stop killing only that process, a turn that
  outlives its worker and streams on from the next one, the snapshot that undoes a turn, the hosted path through HTTP;
  the stream parsers on the captured Claude Code and Codex formats (`internal/worker/testdata/`).
- Web unit: the thread's events (text at the right offset, a gap reads again), the tool and info lines, choices.
- e2e (`e2e/tests/24-chat.spec.ts`), desktop and phone (Chromium, and WebKit iPhone in Docker): chat with a fake
  agent, the document reloads as it edits, a choice is tapped, Stop, Send builds v1 and is then disabled with its
  reason.
