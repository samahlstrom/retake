# Local HTTP API, v1

This is the contract between the `retake` server (Go) and the review page (Svelte). Milestone 1 keeps it
**byte-compatible with the legacy Python player** (`review.py` + `index.html`), so either half can be tested against
the other: the new page against the old server, and the old page against the new server.

The server binds `127.0.0.1` only. Default port 8777; if it is busy, the next free one up to 8796. A fixed port matters
because the browser remembers the mic permission per origin, port included.

## Versions

A *version family* is a set of video files that are versions of the same edit. The opened file is the *current*
version.

- **Detection** (no `--versions` flag): the stem must match `^(?P<prefix>.+?)[_\-]v(?P<n>\d+)(?P<rest>(?:[_\-].*)?)$`
  (case-insensitive). Search the opened file's folder and every sibling folder of it (folders under the parent),
  skipping folders that end in `.review`. Keep files with a video extension (`.mp4 .m4v .mov .webm .mkv`, plus `.m3u8`
  in the new server) whose prefix matches case-insensitively.
- **One file per version number.** For the opened version, the opened file wins. For the others, a plain
  `<name>_vN.mp4` (empty `rest`) wins over `<name>_vN_draft.mp4`. Ties go to the newest mtime, and the first candidate
  that probes with a duration is used.
- **Still rendering = skipped.** A non-current file whose probe has no duration is left out until it probes. The
  family is re-scanned on every `GET /api/info`.
- **Labels** are `v<N>`, sorted by N. A file without a version number gets the label `current`.
- **Explicit** (`--versions a.mp4 b.mp4`): the opened file plus the listed ones. The label is `v<N>` when the name has
  a version number, else the stem. A duplicate label gets `-<index>` appended. Files without a number sort after the
  numbered ones, in argument order.

A version object:

```json
{ "label": "v6", "n": 6, "path": "/abs/path/clip_v6.mp4", "name": "clip_v6.mp4", "current": true,
  "duration": 40.0, "fps": 30.0, "width": 1920, "height": 1080 }
```

`duration` is null if ffprobe can't read one. `fps` defaults to 30.0. Probe results are cached per (path, mtime, size).

## Routes

| Method | Path | Response |
|---|---|---|
| GET/HEAD | `/` or `/index.html` | The review page (embedded build, or `--ui-dir`). |
| GET/HEAD | `/assets/*` and other static files | The page's static files (new server only; the legacy page is a single file). |
| GET/HEAD | `/video` | The current version's file. |
| GET/HEAD | `/video/<label>` | That version's file. 404 `{"error":"no such version"}` if unknown. |
| GET/HEAD | `/video/<label>/<file>` | New: a file beside an `.m3u8` version's playlist (its segments). Only plain names inside that folder, never `..`. |
| GET | `/api/ping` | `{"ok": true}` |
| GET | `/api/info` | The current version object, plus `versions` (the re-scanned family), `stem`, `review_dir`, `sessions` and `history` (both for the current version). |
| GET | `/api/history?v=<label>` | `{"label": "...", "history": [...]}`. 404 if the label is unknown. |
| POST | `/api/session/<take>/data?v=<label>` | Saves a take's JSON files. See below. |
| POST | `/api/session/<take>/audio?v=<label>&name=<file>&offset=<n>` | Appends audio bytes to one segment file. See below. |
| GET | `/favicon.ico` | 204 |

Anything else is 404 `{"error": "not found"}`. All JSON responses send `Cache-Control: no-store`.

**Video files** support single HTTP ranges: `bytes=a-b`, `bytes=a-` and `bytes=-n` give 206 with `Content-Range`, and
an unsatisfiable range gives 416 with `Content-Range: bytes */<size>`. They send `Accept-Ranges: bytes` and
`Cache-Control: no-cache`, and use the content type for the extension (`video/mp4`, `video/quicktime`, `video/webm`,
`video/x-matroska`, `application/vnd.apple.mpegurl`, and `video/mp4` / `video/iso.segment` for `.m4s`).

**Every request** counts as contact for the idle timer. With `--idle-exit M` (default 15, 0 = never), the server exits
M minutes after the last request, counting only once the page has made its first request. The page pings every 15 s.

### POST rules

- **Origin check.** Rejected with 403 `{"error":"bad origin"}` unless there is no `Origin` header, or it is exactly
  `http://127.0.0.1:<port>` or `http://localhost:<port>`.
- **Body limit.** A body over 1 GiB gets 413.
- **Take id.** `<take>` must match `^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`, else 404.
- **Version.** `?v=` must name a version in the family, else 400 `{"error":"bad version"}`.
- **Location.** The take folder is `<dir of that version's file>/<stem of that version's file>.review/<take>/`,
  created on demand.
- **Writes are serialized** by one lock.

**`/data`**: the body is `{"session": {...}, "segments": [...], "events": [...], "strokes": [...], "voice": {...}}`.
Invalid JSON gives 400 `{"error":"bad json"}`. The server writes, each atomically (temp file plus rename, pretty JSON,
UTF-8 kept as is):

- `session.json`: the client's `session` object, plus server-set fields:
  - `version`: the label;
  - `versions`: `[{label, path, duration}]` of the whole family;
  - `video`: `{path, name, duration, fps, width, height}` of that version;
  - new in v1: `author`: `{name}` from the config file, or the OS user.
- `segments.json`, `events.json`, `strokes.json`: the arrays, `[]` if missing.
- `voice.json`: the object, `{}` if missing.

The response is `{"ok": true, "dir": "<take folder>"}`.

**`/audio`**: `name` must match `^seg_\d{2,3}\.(webm|ogg|m4a)$`, else 400 `{"error":"bad name"}`. A non-integer
`offset` gives 400 `{"error":"bad offset"}`. Let `size` be the file's current size (0 if missing):

- if `offset < 0` or `offset > size`: respond 409 `{"size": size, "error": "offset past end"}`. The client resumes
  from `size`.
- otherwise write the body at `offset`, truncate the file after it, and respond `{"size": offset + len(body)}`.

## Reads

- **`sessions`**: one entry per take folder of a version, sorted by name:
  `{id, narration, segments, strokes, notes, saved}`.
  - `narration` is the sum of `t_end - t_start` over `segments.json`. If that file is missing, it falls back to
    `session.segments` or `session.rec.duration`.
  - `notes` is true when `notes.md` exists in the folder.
- **`history`**: one entry per earlier take of that version, plus every take carried over by `inherit.json`:
  `{id, inherited_from, voice: {bin, bins: [[i, v], ...]}, strokes: [{video_t, color, points}]}`.
  - Carried-over takes keep only the strokes whose `video_t` is inside their `ranges`, and only the voice bins whose
    `i * bin` is inside them.
  - `inherit.json` sits in the version's `.review/` folder: `{"takes": [{dir, version, ranges, ...}]}`. Entries whose
    `dir` no longer exists are ignored.

## The page's test hook

The end-to-end test drives the page through the Chrome DevTools protocol, and reads its state through
`window.__review`. The Svelte page must expose the same shape:

```js
window.__review = {
  take,          // getter: the current take object (id, version, closed, saving, dirty, strokes[{..., undone}], ...)
  seg,           // getter: the recording segment in progress, or null
  view,          // getter: {base, shown, flipped, switching}
  info,          // getter: the /api/info response
  hist,          // the loaded history ({strokes: [...], ...})
  serialize,     // (take) => {session, segments, events, strokes, voice}   (the /data body)
  save,          // (take) => Promise
  vad, bars, mic // getters, as in the legacy page
}
```

The toast element is `#toast`. It has the class `show` while visible, and its `textContent` is the message. The Done
button is `#done`.

## CLI (milestone 1)

```
retake open <video> [--versions a.mp4 b.mp4 ...] [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--verbose]
retake sessions <video> [--json]
retake version
```

- Without `--no-open`, the page opens with the OS opener: `xdg-open`, `open` or `start`.
- Exit code 2 for a missing file, a busy `--port`, or no free port.
- The startup lines match the legacy ones (video, versions, page URL, where takes are saved).

## Config file

`$XDG_CONFIG_HOME/retake/config.toml` (default `~/.config/retake/config.toml`), all keys optional:

```toml
name = "Sam"          # author name written into session.json
port = 8777
idle_exit_minutes = 15
open_browser = true
```

Command-line flags override the file.
