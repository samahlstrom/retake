# The review bridge, v1 (editor track 3)

How the review side (`spec/notes.md`) and the editor (`spec/timeline.md`, `spec/manifest.md`) meet: a project can be
a timeline project; a push can render; notes resolve to timeline elements; versions map onto each other in time; and
notes carry over to the next version. Everything here reads render manifests, so versions made by another renderer
work as long as it writes one. Change this file first, then the code.

## Timeline projects

A project (its root as in `spec/notes.md`) is a *timeline project* when Retake finds its timeline:

1. `<root>/retake.toml`, if it has `[edit] timeline`, a path relative to the root (or absolute):

   ```toml
   [edit]
   timeline = "edit/timeline.json"
   ```

2. Otherwise `<root>/edit/timeline.json`, if it exists.

The timeline's patches go to its patch log, `<timeline stem>.patches.jsonl` beside it (`spec/timeline.md`). Its media
paths resolve as the editor resolves them (relative to the timeline file, absolute, `~/…` or `${VAR}`). The asset
catalog will later resolve `proj:`, `lib:` and `brand:` ids; until then the editor's own resolution applies, and the
one seam is the environment Retake gives `retake-edit` (catalog-provided variables such as `RETAKE_LIB`).

**Finding `retake-edit`:** `[edit] retake_edit` in the user config (`~/.config/retake/config.toml`), else
`$RETAKE_EDIT`, else `retake-edit` on `$PATH`, else `editor/target/release/retake-edit` in the repo the `retake`
binary was built in (`<bin>/../editor/…`) or under the current folder.

**Render manifest of a version.** A version file `X.mp4` has its manifest at `X.manifest.json` beside it (what
`retake-edit render -o X.mp4` writes). A version without one has none; everything below then falls back.

## Push with render

`retake push <video> --render [--draft] [--no-stream] [--export] [--parent vN] [--label vN] [--change ID="…"]…
[--ask ID="…"]…`, and MCP `version_push` with `render: true` (and `draft`, `stream`, `export`), take no file: they
render the current timeline revision. A rendered push **streams** by default (see "Streaming" below): it returns at
once with the version pending in the family, and the version renders when someone watches it, filling in as it goes.
`--no-stream` (MCP `stream: false`) renders one MP4 first and then moves it in, as the steps below say.

1. **The patches of this version** are the patch log's entries with `rev` above the parent's manifest's
   `timeline.rev` (all of them when the parent has no manifest).
2. **The changelog comes from them.** Each patch's `answers` name notes: `#5`, `5` or `v7#5` (the label is
   informational; the number is the project-wide note id). Every answered note gets one changelog entry
   `{note, summary, patches}`: the summaries of its patches joined with `; `, and their ids. An explicit `--change`
   for a note replaces the summary (the patches stay listed); an explicit `--ask` for it asks instead. Answers that
   name a note that isn't open are left out and reported as `skipped`. Patches with `answers: []` are *unrequested*.
3. **The push rule applies as for any push**, before anything renders: every note open on the parent must be in the
   changelog (from patches or `--change`) or asked about.
4. **Render:** `retake-edit render <timeline> -o <tmp>.mp4 --json` (`--draft` for drafts).
5. **Register:** the video is moved in as the next `v<N>` (or `--label`), named and placed like any push; its manifest
   goes beside it as `<stem>.manifest.json` (its `video` renamed), and a push record as `<stem>.push.json`:

   ```json
   { "version": "v8", "from": "v7", "rev": 57, "timeline": "edit/timeline.json", "draft": false,
     "patches": [ { "id": "p52", "rev": 52, "answers": ["v7#5"], "notes": [5], "summary": "DEFEAT on 'losses'" } ],
     "unrequested": [ { "id": "p55", "rev": 55, "summary": "Bed crossfade moved 0.2 s to follow the trim" } ],
     "skipped": [],
     "units": { "total": 50, "reused": 41, "rendered": 9 } }
   ```

The result is the push result plus `render` (that record) and `manifest` (its path). `versions_list` shows each
version's push record as `render`. A failed render leaves nothing in the family.

## Streaming

A streamed push registers the next `v<N>` at once as a *pending* HLS version, `v<N>/<stem>.m3u8`, and renders
nothing but its first frame. The render runs **on demand**: it starts when someone opens the version, publishes units
into a growing EVENT playlist in timeline order (the cached ones at once), stops when the last viewer leaves, and
resumes from the cache when someone opens it again. Once every unit is published the playlist ends with
`#EXT-X-ENDLIST`, and the version never renders again. The host never renders a version nobody watches. The final
version stays the playlist and its segments.

**Where.** Streaming needs a family with version folders (`v6/clip_v6.mp4`): everything goes in the new version's
folder, `v7/`. In a family without them, a default push renders one MP4 as before (and says so), and an explicit
`--stream` is refused.

**Push.** Steps 1–3 above happen first; nothing is written when they fail. Then, without rendering:

1. The timeline document as it is now is copied to `v7/clip_v7.timeline.json` (the *snapshot*): the version renders
   that revision whenever it renders, even after the timeline moves on. It renders as if it were the timeline file
   (`retake-edit … --doc <snapshot>`), so its relative media paths, components and cache stay the timeline's.
2. `retake-edit plan` gives the expected duration, frame rate, size and units; `retake-edit frames 0` renders the
   first frame, kept as the poster `v7/clip_v7.poster.jpg`.
3. The playlist is written with its header and no segment yet, and the push record with `stream` (below).
4. The push is recorded at once (the changelog's notes are answered, questions asked, the `version` event with
   `why: "push"`), and returns. Its manifest comes with the render.

**Watching** (`internal/stream`, run by the review server; `retake open`, `retake share` and `retake serve` alike):

- `POST /api/versions/<label>/watch?viewer=<id>` says a page shows that version: the page calls it when it shows a
  streamed version that isn't complete, then every 10 s while it does and the page is visible. `viewer` is a random
  id per page. The answer is the version's `stream` state (below). A viewer counts for 25 s after its last call.
- `POST /api/versions/<label>/unwatch?viewer=<id>` says it stopped (`navigator.sendBeacon` on page hide and when the
  page shows another version).
- Both are allowed for every role that can view (spec/team.md). Watching a non-streamed version is refused (`400`); unwatching one is a harmless `204`, as used when leaving the home screen's regular videos.
- While a version has a viewer and isn't complete, the server renders it: it takes the render lock (an exclusive
  `flock` on `.<stem>.lock` beside the playlist; if another process holds it, that one renders), and runs
  `retake-edit render <timeline> --doc <snapshot> --hls <playlist> --progress --json [--draft] [-o <export>]`.
- When the last viewer is gone (unwatched, or its lease ran out), the server stops the render: `retake-edit` gets
  SIGTERM (SIGKILL 5 s later). Units it finished are in the cache and their segments in the playlist; the one it
  was on is lost. The version is pending again, with its progress.
- Opening it again resumes: the renderer keeps the playlist's published units (`# unit <n>` comments after each
  unit's segments mark them), renders on from the first unit that isn't published, and the cached ones it publishes
  at once. A resumed render's playlist is the one a render without a stop would have written.
- Each state change is an SSE `version` event `{why, label, stream}`: `rendering` (the render started), `progress`
  (at most once a second, and on the last unit), `stopped` (no viewer), `complete` (`#EXT-X-ENDLIST`, the manifest
  is beside the playlist), and `failed` (with `error`; the version stays pending, and the next watch tries again).
  The family is the same in every process that watches, since the state lives in the record and the playlist.

**The record.** `<stem>.push.json` gets `stream`, which says how to render the version and how far it is:

```json
{ "version": "v8", "from": "v7", "rev": 57, …,
  "stream": { "state": "rendering", "playlist": "clip_v8.m3u8", "poster": "clip_v8.poster.jpg",
              "doc": "clip_v8.timeline.json", "timeline": "edit/timeline.json", "draft": false,
              "duration": 182.4, "fps": 60, "size": [1920, 1080], "done": 12, "total": 50, "seconds": 41.3,
              "renders": 2, "first_s": 1.8, "done_s": null, "error": null } }
```

`state` is `pending` (nothing rendering it), `rendering`, or `complete`. `done` counts published units of `total`;
`seconds` is what's playable. `renders` counts how often a render started; `first_s` is the last render's time
from its start to its first published segment, and `done_s` the time of the render that completed, from its start to
`#EXT-X-ENDLIST`. With `--export` the render that completes also packages `<stem>.export.mp4` (`export` names it).

**The playlist:**

```
#EXTM3U
#EXT-X-VERSION:7
#EXT-X-PLAYLIST-TYPE:EVENT
#EXT-X-TARGETDURATION:3
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-START:TIME-OFFSET=0
#EXT-X-MAP:URI="init_00000.mp4"
#EXTINF:2.000000,
seg_00000.m4s
#EXTINF:2.533333,
seg_00001.m4s
# unit 1
…
#EXT-X-ENDLIST
```

`#EXT-X-START:TIME-OFFSET=0` makes players start at 0 instead of at the live edge. `TARGETDURATION` is the longest
planned segment, rounded up; it never changes. Every file is written whole before the playlist names it, and the
playlist is replaced whole (a temporary file renamed over it), so a player never reads half of either.

**Segments are fragmented MP4** (CMAF-style: an init segment, then `moof`/`mdat` fragments). The video is the unit's
encoded segment remuxed (never re-encoded), with a fragment at every keyframe. Units are encoded with an IDR every
2 s (closed GOPs, no scene cuts), so a unit is cut at its keyframes into 2 s segments while more than 3 s of it
remain: segments run up to 3 s, except that a unit shorter than 2 s is one short segment. Units encoded alike share one
init segment (`EXT-X-MAP`); a new one comes only when a unit's differs.

**Timestamps are exact.** Each fragment's decode time (`tfdt`) is set to when it plays in the whole render, and the
video's composition offsets (signed) are shifted so the first frame of each unit has offset 0. Players start the
playlist at the lowest decode time, so a frame shows at exactly its output time: the same moment as in the MP4
export, and C between them compares the same frame. (MPEG-TS can't do that: with B-frames its first decode time is
before the first frame, and hls.js would show every frame two frames late.)

**Audio has no seams.** The whole mix is encoded once to AAC (the same encode the MP4 export muxes, cached by its
plan), and each unit's segments carry the AAC frames that start inside the unit, copied. Across the segments the
audio is that one continuous encode, so there is no priming at the seams and nothing to click: decoded, it equals
the export's audio sample for sample. The encoder's priming frame is left out so audio and video both start at 0,
so the first 21 ms decode without it (a soft start at most).

**Progress lines** (`--progress`, one JSON object per line on stderr):

```json
{"progress": "plan", "units": 50, "reused": 41, "published": 12, "segments": 61, "duration": 182.4, "fps": 60, "size": [1920, 1080]}
{"progress": "publish", "done": 13, "total": 50, "segments": 16, "seconds": 45.3, "elapsed": 2.1}
{"progress": "end", "segments": 61, "seconds": 182.4, "elapsed": 74.2}
```

`published` is how many units a resumed render found published, `done` counts published units, `seconds` the
playable seconds so far, `elapsed` the seconds since this render started. The test hook
`RETAKE_EDIT_TEST_UNIT_DELAY_MS` makes every unit (cached or not) take at least that long (a list, `900,0`, gives
each unit its own, the last standing for the rest), so tests can watch a playlist grow.

**In the family** (spec/api.md, "Versions"): a playlist with a push record that has `stream` is listed whatever its
state, without being probed until it's complete, with the expected `duration`, `fps`, `width` and `height` from the
record, and `stream: {state, done, total, seconds}` and `poster` (its URL, `video/<label>/<stem>.poster.jpg`). A
record that says `rendering` while nobody holds the lock (a server killed mid-render) reads as `pending`.

**The page.**

- A streamed version that isn't complete has a chip in the version list (`pending`, `rendering 12/50`), and the page
  says when one is pushed: "v8 is ready, press ] to watch" (on a phone: "…, tap the version to watch").
- Showing it (`]`, `[`, C, the list) shows its poster at once and starts watching. The video loads once the first
  segment is out, and plays from 0, not from the live edge (hls.js with `startPosition: 0`; Safari by
  `#EXT-X-START`). The timeline uses the expected duration, so the scale doesn't grow, and shows how much is
  rendered.
- When the playhead reaches the end of what's rendered, it waits (buffering) for the next segments.
- Streamed versions prefer hls.js when MediaSource is supported, even if the browser advertises native HLS.
  Chromium's native player can fail on a growing EVENT playlist. Browsers without MediaSource use native HLS.
- Landing on a moment that isn't rendered yet lands at 0 instead, with a toast ("v8 is rendering: 0:41 of 3:02
  so far, from the start").
- The page stops watching when it shows another version, when it's hidden, and when it's closed.
- The time map and carried notes need the manifest, which exists only once the version is complete; until then C, `[`
  and `]` keep the time and nothing carries. The `complete` event refreshes the family, the maps and the sidebar
  silently: the video keeps playing (its player sees `#EXT-X-ENDLIST` by itself) and the duration and chip update.
- `failed` shows a toast with the error.

## Notes resolve to elements

When the notes engine runs for a version (the `notes` job, `retake notes sync`, `retake notes build`), every note
whose version has a manifest gets **targets**: the timeline elements under its strokes, ranked by the rules of
`retake-edit resolve-note` (`spec/manifest.md`). Retake implements them in Go on the manifest (no editor binary
needed, any renderer's manifest works); a test checks it against `retake-edit resolve-note`.

- Each stroke resolves at its own `video_t` with its box; a note without strokes resolves at `t0` without a box.
- Several strokes merge by element: its best score, then the usual order (score, z topmost first, smaller box first,
  id).
- `(audio)` notes also get the spine word at `t0` (or the nearest within 0.5 s).

They're stored in the note's `detail` (`targets`, `unit`, `word`, `audio`) and returned by `notes_list` (`targets`,
`word`) and in `/api/notes` items (`targets`): `[{id, kind, ptr, score, z, box?, parent?}]`, best first. Without a
manifest there are none.

## The time map

From the manifests of versions A and B, a piecewise-linear map from A's time to B's. For a moment `t` of A, in its
unit `u`:

1. **Same pixels:** B has a unit with `u`'s `video_hash` (the one with the same shot first): `t` keeps its offset in
   the unit (`by: "video"`).
2. **Same voice:** a spine element live at `t` plays source second `s` of an asset, and B plays that second of that
   asset (the same spine id first): the moment B plays it (`by: "spine"`). A VO trim maps exactly this way.
3. **Same shot:** B shows `u`'s shot (the n-th unit of that shot in each): the same offset from the unit's start,
   clamped inside it (`by: "shot"`).
4. Otherwise the stretch is interpolated between the moments around it (`by: "gap"`).

`GET /api/timemap?from=v7&to=v8` returns `{from, to, segments: [{a: [a0, a1], b: [b0, b1], by}]}`. The segments
cover A from 0 to its duration in order; a time maps through the segment holding it (`b0 + (t − a0)·(b1 − b0)/(a1 −
a0)`). `segments` is empty when either version has no manifest, and an unknown version is 404. The map from B back to
A is its own request, not this one inverted.

**The page.** C, `[` `]`, a question's version chips and "see it in vN" all show the other version at the mapped
time, so they land on the matching moment instead of the same clock time. With no map they keep the time, as before.

## Carrying notes over

On the newest version V, with its parent P (the push's parent, else the version before it), when both have
manifests, the sidebar (`GET /api/notes?v=V`) also shows P's notes, at their mapped times, each with
`carried: {from: "v7", as, t0, t1}` (`t0` and `t1` are the times on P):

| `as` | When |
|---|---|
| `unchanged` | P's unit at the note's `t0` has a `video_hash` that B has too (and, for `(audio)` notes, an `audio_hash`). |
| `check` | Otherwise, and the note is answered: "answered, check it". |
| `changed` | Otherwise: "context changed". |

Carried are P's notes that are `open` or `answered`; asked ones (`question`) already show as V's question items,
which then move to the mapped time and get the same `carried`. Accepted and retracted notes don't carry. Without
both manifests nothing carries (notes stay on their own version); `inherit.json` keeps working as before.
