# Notes, questions and the agent loop, v1 (milestone 2)

This is the contract for milestone 2. The project store, notes engine, transcription, sidebar, image drops and agent
API all build against it. Change it here first.

## Project

- **Project root.** If the opened file's folder looks like a version folder (`v<N>`, or any folder that has sibling
  folders holding the same family), the root is that folder's parent. Otherwise it's the file's own folder.
  Example: `tutorial/v7/x.mp4` has the root `tutorial/`.
- **Store.** `<root>/.retake/project.db`, SQLite (pure-Go driver `modernc.org/sqlite`, WAL). It is created on demand.
  Takes stay where they are (`<version>.review/<take>/`). The DB indexes them and holds everything that changes over
  time.
- **Tables:**

  | Table | Holds |
  |---|---|
  | `notes` | `id` (the project-wide number, #1, #2, …), `key` (stable, unique), `version`, `take`, `seg`, `t0`, `t1`, `text`, `author`, `status`, `answered_in`, `change`, `detail` (JSON from the notes engine: `strokes`, `snapshot`), `created`, `updated` |
  | `threads` | `id`, `note` (nullable), `version`, `t`, `kind` (`question` or `answer`), `by` (`agent` or the author name), `text`, `options` (JSON list of version labels), `answer_to` (an answer's question), `key` (an answer's note key), `created` |
  | `versions` | `label`, `path`, `sha256`, `parent`, `by`, `created`, `changelog` (JSON `[{note, summary}]`) |
  | `jobs` | `id`, `kind`, `args`, `state` (`queued`, `running`, `done`, `failed`), `attempts`, `error`, `hint` (a failure's one-line fix), `created`, `updated` |
  | `events` | `seq` (monotonic), `kind`, `data`, `created`. This feeds SSE and `notes_wait` cursors. |

  Milestone 3a adds `tokens` and `hosts`, for sharing (spec/team.md).

- **Note keys.** A note key is `<take>/<seg>/<n>`, where n is the comment's index within that segment from the notes
  engine. `take` is the take's folder name, `seg` the segment's own id in that take (`S2`, not the S-number notes.md
  renumbers across takes; `no-audio` for marks outside every segment), and n counts from 1, before inherited ranges
  apply, so a carried-over comment keeps its key. Rebuilding notes keeps existing ids and states. A key the engine
  no longer produces is marked `retracted`, never deleted.

## Note states

`open`, `question`, `answered`, `accepted`, `retracted`.

- `open → answered` when a pushed version lists the note in its changelog.
- `open → question` when the agent asks about it.
- `question → open` when the reviewer answers.
- `answered → accepted` when the reviewer approves that version.
- `answered → open` when the reviewer reopens it.
- `open → retracted` when the engine flags a retraction ("scrap that") that covers it.
- `retracted → open` on a reinstatement.
- A key the engine no longer produces retracts its note only when it is `open`; `question`, `answered` and
  `accepted` notes keep their state.

**Push rule.** `version_push` is rejected with a list of the offending note ids unless every note that is `open` on
the parent version (recorded on it, or carried over to it by `inherit.json` inside its ranges) is either in the
changelog or asked about in the same push.

## What the sidebar shows (server-computed)

`GET /api/notes?v=<label>` returns `{version, newest: bool, items: [...], jobs: [...]}`. `jobs` lists the version's jobs
that aren't done (`{id, kind, state, attempts, error?, hint?, take?}`), so a page that loads after a failure sees it.

- **On the newest version:** only threads of `kind: question` that have no answer yet (time, text, options, the note
  they're about), plus notes recorded on this version in takes newer than its push. Nothing that's answered.
- **On an older version:** that version's notes in their final state. Each has `status`, `text`, `t0`, `t1`, `by`, and
  then `change` and `answered_in` for answered ones, or the question text for asked ones.
- **Item fields:** `{id, kind: "note"|"question", status, t0, t1, text, by, change?, answered_in?, question?, options?,
  images?: [{url, box}]}`. For a question item, `id` is the thread id (what `answer_to` takes), `text` is the
  question, and an optional `note` is the id of the note it's about.

## Take format additions (v1.1; readers ignore unknown fields)

- **Images.** `images.json` holds `[{id, seg, version, flip, file, mime, session_t, video_t, box: [x, y, w, h]}]`.
  The box is normalized 0–1 to the frame. Files are named `img_01.png` (or `.jpg`, `.webp`, `.gif`) in the take
  folder. An image can only be added while recording, like the pen. Z undoes the last stroke or image, whichever came
  last: an image's `id` comes from the same counter as stroke ids, so an `undo` event's `id` names either. The `/data`
  body carries the list as `images` (undone ones left out), and the server writes it to `images.json` like
  `strokes.json`. `/api/history` entries get `images: [{url, video_t, box}]`, where `url` is the `/api/takes/...` one.
- **Answers.** A segment recorded to answer a question carries `answer_to: <thread id>` in `segments.json`.
- **Done.** `session.json` gets `closed: true` when the reviewer presses Done. The page also sends it in the `/data`
  session object of a closed take.
- **notes.json** keeps the legacy structure. Each comment adds `key`, and when they apply `images`
  (`[{id, seg, file, path, mime, version, flip, video_t, box, n, session_t}]`), `answer_to` (from its segment),
  `retracted: true` (a flagged retraction voids it and no later reinstatement restores it) and `author`. notes.md
  tags them `image N` and `answer to question N`.
- **The reviewer's name.** Where notes.md and notes.json refer to the reviewer ("ask Ana rather than guess", a flag's
  "possibly wider than this clip: ask Ana"), they use the `author.name` of the takes when they all name the same
  person, else "the reviewer". Legacy wrote its owner's name there; this is the one intended difference from the
  legacy output, and the golden tests map it explicitly (`internal/notes/golden_test.go`).

## HTTP additions

| Method | Path | Does |
|---|---|---|
| POST | `/api/session/<take>/image?v=&name=img_NN.ext` | Stores one image. Max 25 MiB. Content type is checked against the extension. Same origin and lock rules as `/audio`. |
| POST | `/api/session/<take>/done?v=` | Marks the take closed and queues `transcribe` then `notes` jobs for it. Idempotent. |
| GET | `/api/notes?v=<label>` | The sidebar items (above). |
| POST | `/api/notes/<id>/reopen`, `/api/versions/<label>/approve` | Reviewer actions. Origin-checked. |
| GET | `/api/events?since=<seq>` | Server-sent events: `notes` (the sidebar should refresh), `job` (`{id, kind, state, error?, hint?}`; a failed job's `hint` is a one-line fix, such as how to set up transcription), `version` (the family changed). |
| GET | `/api/takes/<take>/<file>?v=` | Serves an image of a take, for the sidebar thumbnails. |

## Jobs

- Jobs run in the server process, one at a time per kind, and are retried up to 3 times with backoff. Jobs left
  `running` from a crashed run are re-queued at startup.
- `transcribe(take)` writes `transcript.json` in the legacy format:
  `{"files": {"seg_01.webm": {size, model, prompt, duration, words: [{w, s, e, p}]}}}`, where w is the word with its
  leading space, s and e are seconds into that file, and p is the probability. Files already transcribed with the same
  size, model and prompt are skipped.
- `notes(version)` runs the notes engine for that version (all its takes plus inherited ones). It writes
  `<stem>.review/notes.md` and `notes.json`, then syncs the `notes` table and emits a `notes` event.

## Go interfaces (shared between workers)

```go
// internal/transcribe
type Word struct { Text string; Start, End, Prob float64 }
type Transcriber interface {
    // Transcribe returns the words in the audio file, with times in seconds from the file's start.
    Transcribe(ctx context.Context, audioPath string, opts Options) ([]Word, error)
    Name() string // e.g. "whisper.cpp large-v3 (cuda)"; stored as "model" in transcript.json
}
type Options struct { Prompt string; Language string }

// internal/notes
// Build runs the notes engine for one version (like legacy `review notes <video>`) and returns the result that it
// also wrote to notes.md / notes.json. Transcripts must already exist; Build never transcribes.
func Build(ctx context.Context, video family.Version, fam []family.Version, opts Options) (*Result, error)
```

## Agent API (CLI and MCP)

Every command has `--json`. `retake mcp <video>` serves the same tools over stdio, using the official Go MCP SDK
(`github.com/modelcontextprotocol/go-sdk`). The project is found from the video path, as above.

| MCP tool | CLI | Does |
|---|---|---|
| `notes_wait` | `retake notes wait <video> [--since SEQ] [--timeout S]` | Blocks until there are new notes or answers. Returns them and the new cursor. |
| `notes_list` | `retake notes list <video> [--status open] [--version vN]` | Notes with text, times, author, strokes (shape, color, box), images, the snapshot path and the answers. |
| `note_ask` | `retake ask <video> <note> "text" [--option v8a ...]` | Asks a question about a note. |
| `note_set` | `retake note <video> <id> --status S [--in vN] [--change "..."]` | Changes one note. |
| `version_push` | `retake push <video> <file> --parent vN --change ID="summary"... --ask ID="question"...` | Copies or moves the file into the family as the next `v<N>`, or takes `--label vN` (labels are `v<N>`: the page finds versions by the `_v<N>` in their names). Records sha256, parent and changelog, enforces the push rule, and emits a `version` event. |
| `versions_list` | `retake versions <video>` | Versions, parents, changelogs. |

`notes_list` also returns the current `cursor` for `notes_wait`; `notes_wait` from cursor 0 returns every open note at
once. There is no approve tool. Approving is a human action.

`retake notes build <video>` runs the notes engine by hand, like legacy `review notes`. `retake notes sync <video>` transcribes the takes that need it, runs the engine for that version and syncs the store (what the jobs do after Done). `retake watch <video> --exec
"<cmd>"` runs `<cmd>` (through the shell, with `RETAKE_VIDEO`, `RETAKE_PROJECT` and `RETAKE_SINCE` set) every time new
notes or answers land, with debouncing, and never two at once.

## Config additions

```toml
[transcribe]
engine = "auto"      # auto | whisper.cpp | command
model = "large-v3"   # whisper.cpp model name; falls back to "small" on CPU when auto
device = "auto"      # auto | cuda | cpu
command = ""         # engine = "command": a program that reads an audio path and prints transcript words as JSON
whisper_cli = ""     # whisper-cli's path; "" = $PATH, then scripts/install-whisper.sh's $XDG_DATA_HOME/retake/whisper.cpp
```
