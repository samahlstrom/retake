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

- **Paths.** Every file path the store keeps is relative to the project root, with forward slashes: a note's
  `snapshot` (`v6/clip_v6.review/snaps/02_00-20-1.png`), a pushed version's `path` and a transcribe job's take `dir`.
  So a project folder can be moved, renamed or copied and still find its files. Readers resolve them against the root
  where it is now (`Store.Abs`); the agent API gives them whole. A path outside the root is kept as given, and only a
  file inside the project is ever served. Opening an older store fixes the absolute paths it holds: one inside the
  root becomes relative; one outside it (the folder moved) becomes the longest tail of it, two parts at least, that
  names a file (a folder, for a take) in this project, so `/old/place/demo/v6/demo_v6.review/snaps/02.png` becomes
  `v6/demo_v6.review/snaps/02.png` when that file is here. Anything else stays as it was. Images, takes, threads,
  the workspace, the hosts and the automation state keep no paths (names and labels only).
- **A note's detail.** Each stroke is `{shape, color, box: [x, y, w, h], points?, t?}`: `points` is the line as drawn,
  normalized 0–1 to the frame (at most 64, evenly picked, ends included), and `t` the video time of the frame it was
  drawn on. Notes stored before these existed have neither.

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
  images?: [{url, box}], snapshot?, ask?, key?, strokes?}`. A typed comment (`doc/<n>`) has its `key`, and on a
  video its pen lines as `strokes: [{color, points, t}]` ("Typed comments on a video"). A note's `snapshot` is the URL of the still of its moment with its strokes
  drawn on it (`GET /api/notes/<id>/snapshot?m=<file time>`), only when that file is in the project. A comment card
  draws its drawing's thumbnail over it, else over the first frame of the version it was left on (that version's
  poster), else on a plain dark box. For a question item, `id` is the thread id (what `answer_to` takes), `text` is the
  question, and an optional `note` is the id of the note it's about.
- **A task's question.** A note in `question` whose newest question an agent asked from inside its editing task
  (`retake ask`) carries it as `ask: {id, task, t, from?, to?, version, text, visuals}`: the thread id, the task that
  asked, the question's time and range (mapped onto the shown version like the note's own times, else on
  `version`'s clock) and the agent's visuals (spec/automation.md, `TaskQuestion`). That question isn't also listed as
  a question item: it shows under its comment.
- `GET /api/questions/<id>/<file>` serves a task question's images (`img_N`, `option_N`, `frame.jpg`), with the
  viewing permissions of `/api/takes/…`.

## Take format additions (v1.1; readers ignore unknown fields)

- **Images.** `images.json` holds `[{id, seg, version, flip, file, mime, session_t, video_t, box: [x, y, w, h]}]`.
  The box is normalized 0–1 to the frame. Files are named `img_01.png` (or `.jpg`, `.webp`, `.gif`) in the take
  folder. An image can only be added while recording (the pen and a typed comment don't need it: "Typed comments
  on a video" below). Z undoes the last stroke or image, whichever came
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
| POST | `/api/session/<take>/done?v=` | Marks the take closed and queues `transcribe` then `notes` jobs for it. Idempotent. An optional JSON body `{"send": true}` also sends the take's notes to the agent once those jobs finish (spec/automation.md, "Submission and comment state"), with `"agent"` the agent it goes to ("Choosing the agent for one send"); an empty or other body doesn't. |
| GET | `/api/notes?v=<label>` | The sidebar items (above). |
| POST | `/api/notes/<id>/reopen`, `/api/versions/<label>/approve` | Reviewer actions. Origin-checked. |
| GET | `/api/events?since=<seq>` | Server-sent events: `notes` (the sidebar should refresh), `job` (`{id, kind, state, error?, hint?}`; a failed job's `hint` is a one-line fix, such as how to set up transcription), `version` (the family changed: `{why, label, parent?, path?, stream?, error?}`, where `why` is `push`, or for a streamed version `rendering`, `progress`, `stopped`, `complete` and `failed`, with its `stream` state; spec/bridge.md, "Streaming"). |
| GET | `/api/takes/<take>/<file>?v=` | Serves an image of a take, for the sidebar thumbnails. |
| GET | `/api/notes/<id>/snapshot` | Serves a note's snapshot, for its card's drawing. Only a regular file inside the project (through `os.Root`); else 404. Same viewing permissions as `/api/takes/…`. |

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
| | `retake ask [--note ID] [--at T] [--from T --to T] [--box …] [--arrow …] [--draw F] [--image F] [--option TEXT[@F]] "text"` | From inside a running editing task, without the video: asks about a moment of the video through the worker, with visuals (spec/automation.md, "Questions from inside a task"). |
| | `retake working <note> ["what I'm doing"]`, `retake done <note> "what changed"` | From inside a running editing task: the note the agent is on, and a note whose edit is in the live version (spec/automation.md, "Marking progress"). |
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

## Continuous review

The review page opens the newest playable cut. The version menu ("The version menu", below) exposes review checkpoints; choosing an older cut pins it until another is selected. The latest cut can still stream as it renders. Checkpoints do not require storing media in Git.

`GET /api/notes?v=<label>&scope=review` includes that cut's notes, earlier unresolved notes, and notes resolved in that cut, excluding future cuts. Each note exposes its originating `version`. It stays for integrations; the review page reads `scope=video` (below) and does the per-version split itself.

### Comments belong to their version

A comment belongs to the version it was recorded on. A draft's comments (`v5-draft2`, a revision of the live `v5`:
spec/automation.md, "Versions versus drafts") belong to the version the draft
heads for (`v5`), so they are still there once the draft becomes that version.

- **What a version shows.** The sidebar and the timeline show the shown version's comments only, plus the agent's
  questions about them (an item's `note`, else its own `version`), never another version's. The two tabs count that
  version: To do (Open, Working, Question) and Done (resolved, with the change and where it went, "in vN", which shows
  that version at that comment's moment). A comment the agent marks done while it works (spec/automation.md,
  "Marking progress") is in Done as soon as its edit is in the live vN, with the agent's words for its change; it
  moves there from To do by itself, fading out and letting the cards under it slide up (transform and opacity only,
  nothing jumps; nothing moves with `prefers-reduced-motion`). Switching tab, version or search never animates.
  While a version is live (spec/automation.md, "The live version"), its list also has the comments whose edits go
  into it, the running batch's, so following the latest to the live vN keeps them in view as they land. A Send never
  moves the list by itself; switching to the live version happens with following the latest (the video), as before. Timeline markers follow the tab, one per comment, in its status color, at the comment's own time on its own
  version; a comment's pen strokes and voice fold into its marker once it is transcribed. Transcription completion is
  independent of resolution. Switching the version, however it is switched (the version menu, C, a Done
  comment's "in vN"), switches the comments, the markers and the drawings on the video with it; flipped with C, the open take's own
  lines show only on the version they were drawn on (spec/bridge.md, "The page").
- **The page reads the whole family once.** `GET /api/notes?v=<label>&scope=video` returns every version's comments
  with their `version`, also those recorded on a draft the family no longer lists (an older draft, or a promoted
  draft's old label) while its version is listed (with `scope=review`, within the same version limit). The page keeps the shown version's for the list and the markers, and counts the rest per
  version for the version menu's rows and its button's dot. A switch shows the new version's comments at once, from the same answer.
- **The comments header.** One quiet row over the list: To do · Done (each with its count), then an extension's tabs,
  then search and hide at the right (hide on a computer only). No speech bubble and no version in it: the version is
  the version menu's (below), in one place per layout. While searching, the field takes the row's left part. On a
  kind's version with the agent chat (spec/chat.md) the row is the word "Chat" and hide.
- **The version menu.** One menu, the same everywhere, for every way the page is laid out, and one place to open it
  per layout: on a computer, the version button in the bar beside the timeline; on a phone in portrait, in the
  controls row under the video; on a phone in landscape, in the slim bar at the bottom. A phone in portrait on an
  extension's version has no controls row, so there it is in the drawer's header. It is nowhere else (not in the
  comments header). It has `data-part="version-picker"`, and its list `version-list`.
  - **The button** reads the shown version's name (`v8`; `Script` on an extension's version) and a chevron. It has at
    most one dot: red while the shown version is live (spec/automation.md, "The live version"), else the rendering
    dot of a streamed version that isn't complete, else a quiet dot while an earlier version still has comments to
    do, which its title names (`Open on earlier versions: v6 · 2, v3 · 91`). Compared with C, it shows the version on
    screen, marked as flipped. A single version shows no button (nothing to choose).
  - **The rows**, newest first, one per version: a version and its revisions (drafts) are one row, `v8`, which shows
    its newest listed revision, with a small red dot right after the name, on the same line, while the agent works
    on it. The version on screen is in bold; there is no check mark and no "Latest". Each row has at most one quiet
    detail at the right: a streamed version's state while it isn't complete and isn't live (`rendering 3/12`), else
    the number of its comments still to do (`3 to do`), else its length (`0:55`). Where its comments go isn't said
    here (the comment box and the cards say it). An extension's version (v0) is the last row, set apart by a thin
    line, named by its kind's label alone (`Script`, never `v0 · Script`), with its to-do count and no length.
  - A pick shows that version (pinned unless it is the newest), as C and "in vN" do. On a phone each row is at least
    44 px tall and the menu at least 240 px wide, always within the screen; nothing in it wraps.
  - Tests. Web unit: the rows (one per version, its revisions folded in, the live dot, the one detail, the bold one,
    `Script` last and set apart) and the button's one dot. e2e `24-version-menu.spec.ts` (Chromium: a computer and an
    iPhone 15 in both orientations) and `24-version-menu-webkit.spec.ts` (WebKit, iPhone 15): exactly one version
    button on screen in each layout and none in the comments header, no speech bubble while the comments are in view,
    the rows while v3 is live (no "draft", no "Latest", no "→"), 44 px rows inside the screen on the phone, and a pick
    switching the version.
- **The column's width.** On a computer (the desktop layout) the column, comments or a script's chat alike, has a
  thin handle on its left edge, as the home's folder tree has on its right (spec/home.md): drag it to make the column
  wider or narrower, or focus it and press ← (wider) or → (narrower), 16 px a press, 48 px with Shift; a double-click
  goes back to the default (`--sidebar-width`, spec/theme.md). It is a focusable WAI-ARIA window splitter
  (`role="separator"`, its value the width) and part `sidebar-grip`. The width is 300–760 px, and the video or the
  document always keeps at least 420 px of the window (a smaller window narrows the column for as long as it is
  small; the remembered width stays). The stage and the bar reflow beside it; nothing overlaps. The width is
  remembered per viewer in `localStorage` (`retake.sidebar-width`, only once dragged; a blocked storage just doesn't
  remember). Phones have no handle: their comments and the chat sheet keep their own sizes.
- **On a phone.** In portrait the comments always fill the space between the controls (under the video) and Record
  and Send: the header (with To do · Done in its row), the shown version's list, which scrolls inside that space, and
  the comment box at the bottom. Nothing between them is ever left empty, with no comments ("No comments yet" and how
  to make one, which replaces the touch hint over Record) or with many. Portrait is compact so the list gets the height:
  the controls row under the timeline is 40 px and sits just under the track, the comments header is one 44 px row,
  the box 44 px, and the cards tight, so at least three one-line comments are in view on an iPhone 15 (393×659) without
  touching the video's size. There is no speech bubble over the video then: the comments are always in view, and
  To do already counts them. In landscape (no room beside the video) they are an overlay the bubble opens and closes,
  and on an extension's version a drawer over its page, as before; the bubble's count shows only while they are
  hidden. On a computer the bubble shows only while the column is hidden, as before. Every control there is at least 40 px, and nothing reaches past the screen's edges.
- **Where a comment goes.** Comments on vN become edits in vN+1 (spec/automation.md): each comment's `target` is the
  version its edit goes in, made or updated in place (a comment on v7 goes into v8 even once v8 exists; one on v8
  makes v9), and `later` names a version after the target that exists already. The page says it in the same few
  words everywhere: the comment box `Comment at 0:18 · goes into v8…`, a card's `→ v8` next to its status until it's done (its title adds "v9 is newer" with `later`), `in v8` once it is,
  and after Send
  `Sent to v8.` Without `target` (an older server) the page takes the next number (a draft's comments, `v8-draft2`,
  go into v9). A typed comment on an extension's document has none.
- **Send** counts and sends the shown version's open comments (spec/automation.md, "HTTP and UI"), never a task per
  comment. An older version's
  open comments are sent from that version. The agent still gets every open comment of every version as context with
  each task (the poll answer's `notes`).

`GET /api/history?v=<label>&scope=review` includes actual strokes and images for earlier unresolved feedback, with timestamps mapped when manifests exist. Carried entries expose `source_version` and are read-only on the destination cut. The review page reads the version's own history (no `scope`), so another version's drawings never show on this one. History exposes `transcribed` only when all saved audio files have matching completed transcripts; completed recordings never regain waveforms after reload. Partially transcribed takes expose per-segment voice tracks when available.

The bar's one action, Send (spec/automation.md), stops and saves the current recording and queues final processing. When this page has no active take and nothing new to send, it says so, and that existing feedback is already saved; it must not claim there is no recording or resolve comments. Failed final requests remain retryable and never report successful completion.

The keyboard hint under the video shows until the first recording, once per browser (localStorage `retake.hint`); after that the keys are behind `?` (a key and a button in the bar).

Long recordings rotate into independently decodable clips at a pause after 10 seconds, or after 20 seconds of continuous speech, deferring rotation during a pen stroke. The microphone stays open; stopping or finishing during rotation stops both recorders safely. Completed clips begin processing while recording continues. Live notes builds skip snapshots and artifact writes so text can arrive promptly; final processing generates the full artifacts. This is incremental clip transcription, not word-by-word streaming.

`GET /api/notes?v=<label>&scope=video` includes the family’s discussion, including resolved comments. Items are deduplicated by kind/id and mapped onto the displayed cut when render manifests exist. Without a map, timestamps retain their original clock. The response includes `scope: "video"`. The original version-scoped API remains available to integrations.

Comments support text search and all/open/resolved filters. Timeline markers expose comments on hover or keyboard focus and seek on activation. Search fields do not consume player shortcuts.

After a voice clip stops and its final bytes are uploaded, the page sends `POST /api/session/<take>/transcribe?v=<label>`. This queues transcription and notes without closing the take. The server fingerprints completed segments and their byte sizes for idempotence; a later clip queues new work. In-progress segments are excluded. A stopped segment remains marked `recording` in saved metadata until its recorder has finalized and all buffered bytes are uploaded; this prevents autosave from exposing a truncated clip. Clip jobs carry a `revision` argument (the fingerprint), distinct from the final pass. Failed requests retry on autosave. Done still closes the take and runs the final pass, reusing cached completed audio. No coding agent is started automatically.

### Timeline feedback lanes

The upper lane first displays recorded audio coverage. When a clip’s transcribed comments arrive, their hoverable markers replace that clip’s audio coverage. The active recording remains visible even when earlier feedback exists at the same video time. Replacement is matched by note `take` and `seg`; history with a verified complete transcript hides the whole recording, while partial histories use per-segment coverage or matching take and comment time ranges. Failed or pending transcription keeps its audio visible.

Drawings keep a separate lower lane and do not turn into comment markers. Notes expose their originating `take`, `seg`, and `drawings` (pen colors) so historical annotations can remain represented below the current video. Drawing-only notes appear below, while comments with attached drawings occupy both lanes. Hover previews and click-to-seek remain available.

## Deleting a comment

Owners and approvers can choose **Delete comment** on a saved comment, including an empty or drawing-only
comment, and **Undo** immediately afterward. `POST /api/notes/<id>/delete` records a durable user deletion and
retracts the note; `POST /api/notes/<id>/restore` restores its prior status. Both actions are idempotent, need the
approve permission, check the origin, and emit notes events. A notes rebuild must not resurrect a deleted note.
The original audio, drawings and video files are retained. Deletion removes the comment from the sidebar and
comment markers; it does not undo an already-rendered video edit. Queued work for deleted feedback is cancelled,
and running work is asked to stop. Restoring feedback does not automatically restart cancelled editing work.

## Typed comments on a video

Draw, type or record a comment at any moment. Recording adds your voice (a take, as above); a typed comment needs
none.

- **The comment box.** At the bottom of the comments column: `Comment at 0:18…`, the shown version's time. Focusing
  it, typing in it, or drawing on the video while not recording pauses playback and starts a pending comment at that
  moment. While it has no lines, its time follows the playhead (drag it freely); once it has lines, it is the first
  line's time. Lines drawn while not recording (or while the box is in use without recording) belong to the pending
  comment, never to a take; Z undoes its last one, the eraser removes them. Under the box, one quiet line says when
  it is and how many drawings it has. Enter saves it (Shift+Enter is a new line), Ctrl+Enter (⌘+Enter) saves and
  sends it, Esc cancels it with its lines; either way the keys go back to the player (Space plays, R records). With
  lines but no words, Enter outside the box puts the cursor in it. A comment may be a drawing alone. Send with a pending comment saves it
  first, then sends.
- **While recording** the pen draws into the take (pinned to the audio, as always); the box still takes words.
- **`POST api/notes`** `{"version", "t", "text"?, "strokes"?, "frame"?, "snapshot"?}` (needs record, origin-checked)
  adds it: an open note with the next `doc/<n>` key, `t0 = t1 = t`, its `strokes` in its detail (each
  `{shape, color, box, points, t}`: the shape as the notes engine classifies it, `points` at most 64, evenly picked),
  and `wants: "version"` (only a new video version finishes it, as for any video note). `t` is seconds on that
  version (0 to its length plus a second). `text` is up to 4000 characters, and may be empty only with strokes.
  `strokes` is at most 50 of `{color: red|yellow|green|white, points: [[x, y], …] (1 to 2000, each 0–1), t}`.
  `frame` is `[w, h]`, the video's size, for the shape classifier (default 1920×1080). `snapshot` is an optional
  `data:image/jpeg` or `data:image/png` URL of the frame at `t` with the lines drawn on it, at most 8 MiB decoded,
  checked by its first bytes, kept as `.retake/typed/<key>.jpg` (or `.png`) in the project and served like any
  snapshot. Without it the worker makes the still (spec/automation.md, "A note's stills and images"). 201 with its
  item; 400 with the reason for anything invalid.
- **After it is saved** it is a comment like any other: Open, then Working, Question and Done; Send sends it; its
  card shows its words, its time and its drawing; its lines show on the video around its time; the ⋯ menu deletes
  it. A notes rebuild never retracts it. The eraser leaves a saved typed comment's lines alone (delete the comment
  instead).

## Typing on a phone

On a phone, raising the keyboard must not move the screen. The browser's own answer to a field near the bottom is to
pan the whole page up until the field shows, and everything jumps. The phone layout doesn't let it.

- **The page never pans.** The document never scrolls (the page is the screen). Whatever the browser still scrolls to
  reveal a field is undone at once (back to 0, on focus and on every visual-viewport resize or scroll), and what's
  left of a pan of the visual viewport is followed: the page's top is the visible area's top. The screen is laid out
  from the visual viewport instead.
- **One place tracks it** (`web/src/lib/viewport.svelte.ts`, its math in `lib/viewport.ts`): the visual viewport's
  offset and height and what covers the layout viewport's bottom, as CSS variables on `<html>`: `--vv-top` (the pan,
  normally 0), `--kb` (the on-screen keyboard's height: `innerHeight − (offsetTop + height)`, while a text field has
  the focus and it is 80 px or more; else 0) and `--vv-h` (the height left above the keyboard, or above a toolbar
  the browser lays over the page). `<html data-typing="comment|chat|doc|field">` says while a text field has the
  focus and which (the comment box, the chat's composer, the document in the version slot, any other), and
  `<html data-kb>` while the keyboard is up. Nothing else listens to the visual viewport, apart from an extension's
  own caret keeping (spec/scripts.md). The test hook has it as `viewport` (`{top, kb, height, up, typing, compose}`).
- **The keyboard's height is remembered** (`localStorage` `retake.kb`, by orientation and window height). The next
  time a field takes the focus the page lays out for that keyboard at once, while it is still coming up, so the field
  is never under it and the browser has nothing to pan. When no keyboard comes within a second (a hardware keyboard)
  the guess is dropped. The first time, the page waits for the keyboard and then moves once.
- **A stand-in field.** iOS raises the keyboard only for a field focused in the tap itself. A control that opens a
  field that isn't there yet (the chat's pill) focuses a hidden stand-in in the tap, and the real field takes the
  focus over when it shows; the keyboard stays up.
- **Record and Send** fade out while a field has the focus and come back in the same place when it loses it. Their
  lift over a toolbar Safari lays over the page's bottom is held while a field has the focus and while the keyboard
  goes down, so they never jump up over a closing keyboard.
- **Compose mode on a video.** While the comment box has the focus, the screen is only what typing needs:
  - the video stays where it is, on the comment's frame (the box paused it), and only shrinks (scaled from its top)
    when it wouldn't fit above the box;
  - the box sits directly on the keyboard, its line (the time, the drawings, Cancel and Save) above the field;
  - everything else fades out: the controls under the video (the timeline, play, the time, the version, the pen and
    undo), the comments header with To do · Done, the list, Record and Send, and back;
  - one transition, 150 ms, transform and opacity only (no movement at all with `prefers-reduced-motion: reduce`).
    Nothing is laid out again: the box moves by a transform and the rest only fades.

  Return (which saves the comment), Done on the keyboard, or a tap on the video (a tap draws nothing) ends it:
  everything is back exactly where it was, and the list keeps its scroll. A line drawn on the video while composing
  goes with the comment, and the box keeps the focus. `<html data-compose>` is set meanwhile.
- **Other fields** (searching the comments): the page stays still and Record and Send fade out.
- **A drawer on an extension's version** sits on the keyboard while a field in it has the focus, as the chat sheet
  does (spec/chat.md, "Where it shows").
- **Tests.** Web unit: the keyboard's height from `innerHeight` and the visual viewport (a toolbar is not a keyboard,
  pinch zoom is not either), the layout while typing (measured, remembered, dropped), the bar's lift held while the
  keyboard goes down, the remembered heights, which field is which, and compose mode's move and scale. e2e
  (`23-keyboard.spec.ts` in Chromium, `23-keyboard-webkit.spec.ts` on the iPhone 13 and 15 in WebKit): the keyboard
  is simulated (the visual viewport's height and offset stubbed and its events sent); on a video the comment box
  sits on the keyboard, the video and the top bar don't move, the rest is hidden, and leaving restores every place
  and the list's scroll; a pan is undone; on a script the chat sheet sits on the keyboard with nothing behind it
  moving, and typing in the document keeps the caret's line above the keyboard with the bar hidden.
