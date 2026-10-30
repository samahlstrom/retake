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
render the current timeline revision. A rendered push **streams** by default (see "Streaming" below): the version is
in the family within seconds and fills in as it renders. `--no-stream` (MCP `stream: false`) renders one MP4 first
and then moves it in, as the steps below say.

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

A streamed push registers the next `v<N>` at once as an HLS version, `v<N>/<stem>.m3u8`, an EVENT playlist that
grows at its end as units finish, and ends with `#EXT-X-ENDLIST` when the render is done. The reviewer can watch the
cached prefix right away and the rest as it renders. The final version stays the playlist and its segments.

**Where.** Streaming needs a family with version folders (`v6/clip_v6.mp4`): the playlist and its segments go in
the new version's folder, `v7/clip_v7.m3u8`, beside `v7/seg_00000.ts`, `v7/seg_00001.ts`, …. In a family without
them, a default push renders an MP4 as before (and says so), and an explicit `--stream` is refused.

**Steps.** Steps 1–3 above happen first; nothing renders when they fail. Then:

1. Retake writes the push record `<stem>.push.json` with `rendering` (below), takes the render lock (an exclusive
   `flock` on `.<stem>.lock` beside the playlist, held until the push ends), and writes an empty playlist header so
   the number is taken.
2. It runs `retake-edit render <timeline> --hls <dir>/<stem>.m3u8 --progress --json [--draft] [-o <stem>.export.mp4]`
   (`-o` with `--export`).
3. **Plan.** The renderer plans and prints the plan as a progress line. Retake puts the expected `duration`, `fps`,
   `size` and unit counts into the record's `rendering`.
4. **Render in timeline order.** The audio mix runs on its own thread (or comes from the cache) while units render
   on `--jobs` threads, which take units in timeline order: each renders the graphics runs it needs, then itself.
   Cached units are done at once, so the cached prefix is playable as soon as the audio is.
5. **Publish in order.** Unit `i` is published once it and every unit before it are done and the audio is ready: its
   segments are written, then the playlist is rewritten with them (a temporary file renamed over it, so a player
   never reads half a playlist). The playlist only ever grows at its end. After each publish the renderer prints a
   progress line.
6. **First publish:** Retake emits the SSE `version` event `{why: "rendering", label, parent, path}`; the version is
   in the family from then on. While it renders, `{why: "progress", label, progress}` at most once a second.
7. **End.** After the last unit the renderer writes `#EXT-X-ENDLIST`, then the manifest beside the playlist (`video`
   is the playlist's name), and the export MP4 when asked. Retake drops `rendering` from the record, adds `stream`
   (below), records the push (notes answered, questions asked; the usual `version` event with `why: "push"`), and
   releases the lock.
8. **Failure** (the render fails, the push rule fails at the end, or the push is interrupted): the playlist, its
   segments, manifest, record and lock are removed (the folder too when empty), and Retake emits
   `{why: "failed", label}`. Nothing stays in the family. A push killed outright leaves its files, but nobody holds
   its lock, so the family skips it (below) and its number stays taken.

The record while rendering, and after:

```json
{ "version": "v8", "from": "v7", "rev": 57, …,
  "rendering": { "playlist": "clip_v8.m3u8", "duration": 182.4, "fps": 60, "size": [1920, 1080],
                 "done": 12, "total": 50, "started": "2026-10-30T11:02:03Z" } }
{ "version": "v8", …, "units": { "total": 50, "reused": 41, "rendered": 9 },
  "stream": { "playlist": "clip_v8.m3u8", "segments": 61, "first_s": 1.8, "done_s": 74.2 },
  "export": "clip_v8.export.mp4" }
```

`first_s` is the time from the start of the render to the first published segment, `done_s` to `#EXT-X-ENDLIST`.

**The playlist:**

```
#EXTM3U
#EXT-X-VERSION:3
#EXT-X-PLAYLIST-TYPE:EVENT
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-INDEPENDENT-SEGMENTS
#EXT-X-START:TIME-OFFSET=0
#EXTINF:4.000000,
seg_00000.ts
#EXTINF:2.533333,
seg_00001.ts
#EXT-X-ENDLIST
```

`#EXT-X-START:TIME-OFFSET=0` makes players start at 0 instead of at the live edge. `TARGETDURATION` is the longest
planned segment, rounded up; it never changes while the playlist grows.

**Segments are MPEG-TS.** Each one is self-contained (H.264 parameter sets in band before every IDR, no init
segment that units would have to share), and carries absolute 90 kHz timestamps per packet, so segments that
separate ffmpeg runs write line up exactly, B-frames need no edit lists, and both hls.js and Safari's own player play
them. The video is the unit's encoded segment copied (never re-encoded). Units are encoded with an IDR every 2 s
(closed GOPs, no scene cuts), so a unit is cut at its keyframes into 4 s segments while more than 6 s of it remain:
segments run 2–6 s, except that a unit shorter than 2 s is one short segment. Timestamps are the output time plus
10 s, so the first B-frames' decode times stay positive; players start the first segment at 0.

**Audio has no seams.** The whole mix is encoded once to AAC (the same encode the MP4 export muxes, cached by its
plan), and each unit's segments carry the AAC frames that start inside the unit, copied. Across the segments the
audio is that one continuous encode, so there is no priming at the seams and nothing to click: decoded, it equals
the export's audio sample for sample. The encoder's priming frame is left out so audio and video both start at 0,
so the first 21 ms decode without it (a soft start at most).

**Progress lines** (`--progress`, one JSON object per line on stderr):

```json
{"progress": "plan", "units": 50, "reused": 41, "segments": 61, "duration": 182.4, "fps": 60, "size": [1920, 1080]}
{"progress": "publish", "done": 12, "total": 50, "segments": 14, "seconds": 41.3, "elapsed": 2.1}
{"progress": "end", "segments": 61, "seconds": 182.4, "elapsed": 74.2}
```

`done` counts published units, `seconds` the playable seconds so far, `elapsed` the seconds since the render
started. The test hook `RETAKE_EDIT_TEST_UNIT_DELAY_MS` makes every unit (cached or not) take at least that long,
so tests can watch a playlist grow.

**In the family** (spec/api.md, "Versions"): a playlist without `#EXT-X-ENDLIST` whose record has `rendering`, and
whose lock someone holds, is a rendering version once it lists a segment: it is not probed; it gets
`rendering: true`, the expected `duration`, `fps`, `width` and `height` from the record, and
`progress: {done, total, seconds}`. Once the playlist ends it's probed like any other version.

**The page.**

- A rendering version has a chip in the version list (`rendering 12/50`), and the page says so when one appears:
  "v8 is rendering, press ] to watch" (on a phone: "…, tap the version to watch").
- It plays from 0, not from the live edge (hls.js with `startPosition: 0`; Safari by `#EXT-X-START`). The timeline
  uses the expected duration, so the scale doesn't grow, and shows how much is rendered.
- When the playhead reaches the end of what's rendered, it waits (buffering) for the next segments instead of
  failing or ending.
- C, `[` and `]` work as for any version. Showing a rendering version at a moment it hasn't rendered yet is refused
  like a preview's end: "v8 is rendering: 0:41 of 3:02 so far".
- The time map and carried notes need the manifest, which exists only at the end; until then C, `[` and `]` keep the
  time and nothing carries. The `push` event at the end refreshes the family, the maps and the sidebar silently: the
  video keeps playing (its player sees `#EXT-X-ENDLIST` by itself) and the duration and chip update.
- A `failed` event drops the version (with a toast); if it was on screen, the page goes back to the parent.

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
