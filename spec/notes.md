# Notes, questions and the agent loop, v1 (milestone 2)

This is the contract for milestone 2. The project store, notes engine, transcription, sidebar, image drops and agent
API all build against it. Change it here first.

## Project

- **Project root.** If the opened file's folder looks like a version folder (`v<N>`, or any folder that has sibling
  folders holding the same family), the root is that folder's parent. Otherwise it's the file's own folder.
  Example: `item-shop/v7/x.mp4` has the root `item-shop/`.
- **Store.** `<root>/.retake/project.db`, SQLite (pure-Go driver `modernc.org/sqlite`, WAL). It is created on demand.
  Takes stay where they are (`<version>.review/<take>/`). The DB indexes them and holds everything that changes over
  time.
- **Tables:**

  | Table | Holds |
  |---|---|
  | `notes` | `id` (the project-wide number, #1, #2, …), `key` (stable, unique), `version`, `take`, `seg`, `t0`, `t1`, `text`, `author`, `status`, `answered_in`, `change`, `created`, `updated` |
  | `threads` | `id`, `note` (nullable), `version`, `t`, `kind` (`question` or `answer`), `by` (`agent` or the author name), `text`, `options` (JSON list of version labels), `created` |
  | `versions` | `label`, `path`, `sha256`, `parent`, `by`, `created`, `changelog` (JSON `[{note, summary}]`) |
  | `jobs` | `id`, `kind`, `args`, `state` (`queued`, `running`, `done`, `failed`), `attempts`, `error`, `created`, `updated` |
  | `events` | `seq` (monotonic), `kind`, `data`, `created`. This feeds SSE and `notes_wait` cursors. |

- **Note keys.** A note key is `<take>/<seg>/<n>`, where n is the comment's index within that segment from the notes
  engine. Rebuilding notes keeps existing ids and states. A key the engine no longer produces is marked
  `retracted`, never deleted.

## Note states

`open`, `question`, `answered`, `accepted`, `retracted`.

- `open → answered` when a pushed version lists the note in its changelog.
- `open → question` when the agent asks about it.
- `question → open` when the reviewer answers.
- `answered → accepted` when the reviewer approves that version.
- `answered → open` when the reviewer reopens it.
- `open → retracted` when the engine flags a retraction ("scrap that") that covers it.
- `retracted → open` on a reinstatement.

**Push rule.** `version_push` is rejected with a list of the offending note ids unless every note that is `open` on
the parent version is either in the changelog or asked about in the same push.

## What the sidebar shows (server-computed)

`GET /api/notes?v=<label>` returns `{version, newest: bool, items: [...]}`.

- **On the newest version:** only threads of `kind: question` that have no answer yet (time, text, options, the note
  they're about), plus notes recorded on this version in takes newer than its push. Nothing that's answered.
- **On an older version:** that version's notes in their final state. Each has `status`, `text`, `t0`, `t1`, `by`, and
  then `change` and `answered_in` for answered ones, or the question text for asked ones.
- **Item fields:** `{id, kind: "note"|"question", status, t0, t1, text, by, change?, answered_in?, question?, options?,
  images?: [{url, box}]}`.

## Take format additions (v1.1; readers ignore unknown fields)

- **Images.** `images.json` holds `[{id, seg, version, flip, file, mime, session_t, video_t, box: [x, y, w, h]}]`.
  The box is normalized 0–1 to the frame. Files are named `img_01.png` (or `.jpg`, `.webp`, `.gif`) in the take
  folder. An image can only be added while recording, like the pen. Z undoes the last stroke or image, whichever came
  last.
- **Answers.** A segment recorded to answer a question carries `answer_to: <thread id>` in `segments.json`.
- **Done.** `session.json` gets `closed: true` when the reviewer presses Done.

## HTTP additions

| Method | Path | Does |
|---|---|---|
| POST | `/api/session/<take>/image?v=&name=img_NN.ext` | Stores one image. Max 25 MiB. Content type is checked against the extension. Same origin and lock rules as `/audio`. |
| POST | `/api/session/<take>/done?v=` | Marks the take closed and queues `transcribe` then `notes` jobs for it. Idempotent. |
| GET | `/api/notes?v=<label>` | The sidebar items (above). |
| POST | `/api/notes/<id>/reopen`, `/api/versions/<label>/approve` | Reviewer actions. Origin-checked. |
| GET | `/api/events?since=<seq>` | Server-sent events: `notes` (the sidebar should refresh), `job` (`{id, kind, state}`), `version` (the family changed). |
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
| `version_push` | `retake push <video> <file> --parent vN --change ID="summary"... --ask ID="question"...` | Copies or moves the file into the family as the next `v<N>`, or takes `--label`. Records sha256, parent and changelog, enforces the push rule, and emits a `version` event. |
| `versions_list` | `retake versions <video>` | Versions, parents, changelogs. |

There is no approve tool. Approving is a human action.

`retake notes build <video>` runs the notes engine by hand, like legacy `review notes`. `retake watch <video> --exec
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
