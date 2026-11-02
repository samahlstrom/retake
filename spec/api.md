# Local HTTP API, v1

This is the contract between the `retake` server (Go) and the review page (Svelte). The team server
(spec/server.md) serves the same routes under each project's `/p/<name>/`; the page uses relative URLs (`api/info`,
`video/v3`, `./assets/...`), so one build works at both. Milestone 1 keeps it
**byte-compatible with the legacy Python player** (`review.py` + `index.html`), so either half can be tested against
the other: the new page against the old server, and the old page against the new server.

The server binds `127.0.0.1` only. Default port 8777; if it is busy, the next free one up to 8796. A fixed port matters
because the browser remembers the mic permission per origin, port included.

## API versions

The HTTP API has a major version, **1**. Every answer of `retake open`, `retake share` and `retake serve` carries the
header `X-Retake-Api-Version: 1`, so a page of your own (`--ui-dir`, docs/theming.md "Going further") can check what
it talks to.

- Within a major version, routes, fields and values are only added. A client ignores fields it doesn't know.
- A route or field is removed, renamed or changes meaning only in a major release (`retake` 2.0.0), and the header
  says the new number. The release notes list what changed.
- The page's look has its own versioned contract, the Theme API (spec/theme.md).

## Versions

A *version family* is a set of video files that are versions of the same edit. The opened file is the *current*
version.

- **Detection** (no `--versions` flag): the stem must match `^(?P<prefix>.+?)[_\-]v(?P<n>\d{1,4})(?P<rest>(?:[_\-].*)?)$`
  (case-insensitive). A version number is 1 to 4 digits (at most 9999); a longer number is an ID, not a version, so
  `twitch_v2903906005_3480-4520.mp4` (a Twitch video's ID) has no version number. Neither has a name that is only a
  date and time, such as an OBS recording's `2026-10-17 21-30-05.mp4`: a folder of recordings holds no video
  (spec/home.md). A version folder is `v<N>` with the same N (`v7`, `V12`, not `v2903906005`). Search the opened file's folder and every sibling folder of it (folders under the parent),
  skipping folders that end in `.review`. Keep files with a video extension (`.mp4 .m4v .mov .webm .mkv`, plus `.m3u8`
  in the new server) whose prefix matches case-insensitively.
- **One file per version number.** For the opened version, the opened file wins. For the others, a plain
  `<name>_vN.mp4` (empty `rest`) wins over `<name>_vN_draft.mp4`. Ties go to the newest mtime, and the first candidate
  that probes with a duration is used.
- **Still rendering = skipped.** A non-current file whose probe has no duration is left out until it probes. The
  family is re-scanned on every `GET /api/info`.
- **Except a streamed version** (spec/bridge.md, "Streaming"): an `.m3u8` whose push record `<stem>.push.json` has
  `stream` is in the family in every state, and isn't probed until it's complete: its `duration`, `fps`, `width` and
  `height` are the expected ones from the record.
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
A streamed version also has `"stream": {"state": "rendering", "done": 12, "total": 50, "seconds": 41.3}` (`state` is
`pending`, `rendering` or `complete`; `done` counts published units of `total`, `seconds` is what's playable) and
`"poster": "video/v7/clip_v7.poster.jpg"`, the URL of its first frame; other versions have neither field.

## Routes

| Method | Path | Response |
|---|---|---|
| GET/HEAD | `/` or `/index.html` | The review page (embedded build, or `--ui-dir`), with the user's theme added when there is one (spec/theme.md). |
| GET/HEAD | `/theme/<path>` | A file of the user's theme folder: CSS, fonts and images only (spec/theme.md, "Serving theme files"). |
| GET/HEAD | `/assets/*` and other static files | The page's static files (new server only; the legacy page is a single file). |
| GET/HEAD | `/video` | The current version's file. |
| GET/HEAD | `/video/<label>` | That version's file. 404 `{"error":"no such version"}` if unknown. |
| GET/HEAD | `/video/<label>/<file>` | New, for `.m3u8` versions: the playlist itself (by its file name) or any file beside it, such as segments and init files. Only plain names inside that folder, never `..` or subfolders. The page loads `/video/<label>/<playlist name>`, so relative segment URLs resolve inside that folder. |
| GET | `/api/ping` | `{"ok": true}` |
| GET | `/api/info` | The current version object, plus `versions` (the re-scanned family), `stem`, `review_dir`, `sessions` and `history` (both for the current version). New in milestone 3a: `role` (spec/team.md). |
| GET | `/api/history?v=<label>` | `{"label": "...", "history": [...]}`. 404 if the label is unknown; no `v` means the current version. |
| POST | `/api/session/<take>/data?v=<label>` | Saves a take's JSON files. See below. |
| POST | `/api/session/<take>/audio?v=<label>&name=<file>&offset=<n>` | Appends audio bytes to one segment file. See below. |
| POST | `/api/erase?v=<label>` | The eraser on a line of an earlier take: `{"take", "id", "erased": true\|false}` adds it to (or removes it from) the version's `erased.json` (spec/take-format.md, "Erased lines"). 400 for a bad take id or stroke id, 404 for a take not shown on that version. Same origin and lock rules as `/data`. |
| POST | `/api/versions/<label>/watch?viewer=<id>` | New with streaming (spec/bridge.md, "Streaming"): a page shows that streamed version; it renders while one does. Every 10 s while shown; a viewer counts for 25 s. Answers `{"label", "stream"}`. Any role that can view; 404 for an unknown label, 400 for a version that isn't streamed. |
| POST | `/api/versions/<label>/unwatch?viewer=<id>` | The page stopped showing it (`navigator.sendBeacon`). The render stops when the last viewer is gone. Same rules as `watch`. |
| GET | `/favicon.ico` | 204 |
| GET | `/s/<token>` | New in milestone 3a: a share link. Sets the share cookie and bounces to `/` (spec/team.md). |
| GET/HEAD | `/api/poster?v=<label>` | New: a JPEG of the version's first frame (no `v`: the current one), for `<video poster>` and the home's cards (spec/home.md). 404 `{"error":"no poster"}` when there is none. |
| POST | `/api/versions/<label>/unwatch?viewer=<id>` | New: the page left that version (spec/home.md, "Close means close"). 204. Needs watch, not record. |
| GET | `/api/link-preview?url=<address>` | New with a script's embeds (spec/scripts.md, "Embeds"): what a public web page says about itself, for a link card: `{"url", "title", "site", "image", "description"}`. See "Link preview" below. |
| any | `/mcp` | New in milestone 3a: the agent tools over MCP's streamable HTTP transport, for a Bearer `agent` token (spec/team.md). |

Anything else is 404 `{"error": "not found"}`. Any request whose `Host` header isn't `127.0.0.1:<port>` or `localhost:<port>` gets 403 `{"error": "bad host"}` first, on every route. That blocks DNS rebinding, where another site points its own hostname at 127.0.0.1 to read local videos and takes. Refused requests don't count as contact for the idle timer. `--allow-host H` (repeatable) adds `H` to the accepted Host headers and `https://H` to the accepted origins. It's for a tunnel such as a Cloudflare quick tunnel. Since milestone 3a every request through such a host must carry a token, and each route checks the token's role (spec/team.md); local requests without a token keep full access. All JSON responses send `Cache-Control: no-store`.

**Video files** support single HTTP ranges: `bytes=a-b`, `bytes=a-` and `bytes=-n` give 206 with `Content-Range`, and
an unsatisfiable range gives 416 with `Content-Range: bytes */<size>`. They send `Accept-Ranges: bytes` and
`Cache-Control: no-cache`, and use the content type for the extension (`video/mp4`, `video/quicktime`, `video/webm`,
`video/x-matroska`, `application/vnd.apple.mpegurl`, `video/mp4` / `video/iso.segment` for `.m4s`, `video/mp2t` for
`.ts`). A playlist (`.m3u8`) can still be growing (spec/bridge.md, "Streaming"), so it also says no cache in between
may keep it: `Cache-Control: no-cache, private`, `CDN-Cache-Control: no-store` and
`Cloudflare-CDN-Cache-Control: no-store` (a tunnel such as Cloudflare's then always asks this server).

**Every request** counts as contact for the idle timer. With `--idle-exit M` (default 15, 0 = never), the server exits
M minutes after the last request, counting only once the page has made its first request. The page pings every 15 s.

### POST rules

- **Origin check.** Rejected with 403 `{"error":"bad origin"}` unless there is no `Origin` header, or it is exactly
  `http://127.0.0.1:<port>` or `http://localhost:<port>`.
- **Body limit.** A body over 1 GiB gets 413.
- **Take id.** `<take>` must match `^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}$`, else 404.
- **Version.** `?v=` must name a version in the family, else 400 `{"error":"bad version"}`. A missing or empty `v`
  means the current version, as in the legacy server.
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

`offset` is a 64-bit integer. After a reload, the page asks for a file's size by posting an empty body at offset
`9007199254740991` (2^53 − 1), which must get the 409 above and write nothing.

## Reads

- **`sessions`**: one entry per take folder of a version, sorted by name:
  `{id, narration, segments, strokes, notes, saved}`.
  - `narration` is the sum of `t_end - t_start` over `segments.json`. If that file is missing, it falls back to
    `session.segments` or `session.rec.duration`.
  - `notes` is true when `notes.md` exists in the folder.
- **`history`**: one entry per earlier take of that version, plus every take carried over by `inherit.json`:
  `{id, inherited_from, voice: {bin, bins: [[i, v], ...]}, strokes: [{id, video_t, color, points}]}` (a stroke's `id` when its take has one; lines in the version's `erased.json` are left out).
  - Carried-over takes keep only the strokes whose `video_t` is inside their `ranges`, and only the voice bins whose
    `i * bin` is inside them.
  - `inherit.json` sits in the version's `.review/` folder: `{"takes": [{dir, version, ranges, ...}]}`. Entries whose
    `dir` no longer exists are ignored. A relative `dir` is relative to that `.review/` folder; no `ranges` means all.

## Link preview

`GET /api/link-preview?url=<address>` reads one web page and says what it says about itself, for a script's link
cards (spec/scripts.md, "Embeds"). It needs watch, like every read; under a video it is
`/p/<name>/api/link-preview`, locally and on a team server alike, and the server that serves the page does the read.

- **The answer**: `{"url", "title", "site", "image", "description"}`. `title` is `og:title`, else `twitter:title`,
  else `<title>`. `site` is `og:site_name`, else the host without `www.`. `image` is `og:image` (else
  `twitter:image`), made absolute against the page, kept only when it is https. `description` is `og:description`,
  else `<meta name="description">`. Entities are decoded, spaces folded, and each is cut (300, 100, 2048 and 500
  characters); a missing one is `""`.
- **Only public pages.** The address is `http` or `https`, with a host, no user or password, at most 2048
  characters. The server never connects to a loopback, private, link-local (169.254.0.0/16, so the cloud metadata
  address 169.254.169.254), shared (100.64.0.0/10), unspecified, multicast, broadcast, documentation or other
  reserved address, IPv4 or IPv6 (`::1`, `fc00::/7`, `fe80::/10`, and an IPv4 address inside an IPv6 one is judged
  as that IPv4 address). It checks the address it is actually connecting to, after DNS, so a name that resolves to
  one is refused, and so is every redirect (at most 5, to http or https). Proxies from the environment aren't used.
  A refused address answers 400 `{"error": "not a public web address"}`.
- **Small and quick.** 5 s for the whole read, redirects included. Only the first 1 MiB of the body is read, and
  only `text/html` and `application/xhtml+xml` are parsed (another type answers with just the site). It sends
  `User-Agent: Mozilla/5.0 (compatible; Retake link preview)`. A page that can't be read answers 502
  `{"error": "can't read that page"}`. At most 4 pages are read at once.
- **Cached on disk**, one JSON file per address (named by its SHA-256) in `retake/link-preview/` under the user's
  cache folder (`$XDG_CACHE_HOME`, else `~/.cache`): an answer for 7 days, a page that couldn't be read for
  10 minutes, so asking again doesn't reach the site.

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
  notes,         // getter, new in milestone 2: {available, version, newest, items, jobs, job} from /api/notes and SSE
  sidebar,       // getter, new in milestone 2: {panel, toggle, phone, rows, active}, what the sidebar shows
  home,          // getter, new: {available, next} when the page is a project of a home (spec/home.md)
  draft,         // getter, new: the typed comment being written, {version, t, lines, text}, or null (spec/notes.md)
}
```

The toast element is `#toast`. It has the class `show` while visible, and its `textContent` is the message. The Done
button is `#done`. The pen canvas is `#ink`: its box is exactly the shown video frame, so the test draws in coordinates
normalized to it. The page has a single `<video>` element, and the test reads the playhead and play state from it.

New: on a phone the page has its own layout, with the same ids. `<html data-layout>` says which one is on
(`desktop`, `portrait` or `landscape`). The phone layout's controls are `#play`, `#verBtn` (the version list), `#undo`
(Z), `#pen` (its popover holds the pen colors `.sw[data-color]` and `#eraser`), `#rec` (R) and `#done` (Send). It has
no flip button: C is a keyboard key. The desktop bar has the same `#pen`, `#rec`, `#done` and `#keys` (?). The
comment box is `[data-part~="comment-line"] textarea`.

## CLI (milestone 1)

```
retake open <video> [--versions a.mp4 b.mp4 ...] [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--theme DIR] [--allow-host H]... [--verbose]
retake open <folder> [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--theme DIR] [--allow-host H]... [--verbose]
retake theme init [DIR] [--force]
retake theme check [DIR] [--json]
retake sessions <video> [--json]
retake version
```

- Without `--no-open`, the page opens with the OS opener: `xdg-open`, `open` or `start`.
- With a folder, every video in its tree of folders and projects is served, with a home screen at `/` (spec/home.md).
- `--theme DIR` (or `RETAKE_THEME`) is the user's theme folder, by default `$XDG_CONFIG_HOME/retake/theme`; `none`
  turns theming off (spec/theme.md).
- Exit code 2 for a missing file, a busy `--port`, or no free port.
- The startup lines match the legacy ones (video, versions, page URL, where takes are saved).

## Config file

`$XDG_CONFIG_HOME/retake/config.toml` (default `~/.config/retake/config.toml`), all keys optional:

```toml
name = "Ana"          # author name written into session.json
port = 8777           # tried first; if busy, the next free one of the next 19 (a --port flag is strict)
idle_exit_minutes = 15
open_browser = true
```

Command-line flags override the file.
