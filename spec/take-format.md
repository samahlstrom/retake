# Take format, v1

A *take* is one review session by one person on one version: everything between the first R and Done. It lives in
`<version dir>/<version stem>.review/<YYYY-MM-DD_HH-MM-SS>/`. This is the format the legacy player writes and the
legacy `review notes` reads. Milestone 1 writes it unchanged, plus the additions marked **new**. Readers must ignore
fields they don't know.

## Clocks

- `session_t`: seconds since the take started (the first R).
- `video_t`: the video's playhead time, in seconds.
- `events.json` maps one clock to the other: play/pause/seek events, plus a `tick` every 2 s while playing.

## Files

| File | Contents |
|---|---|
| `seg_01.webm`, `seg_02.webm`, … | One audio file per R-on/R-off segment. Each starts at that segment's `t_start`. The extension follows the recorder's type (`mime` in `segments.json`): Opus in `webm` or `ogg`, or AAC in mp4 as `m4a`, which is all Safari records (iOS included). |
| `segments.json` | `[{id, file, mime, version, flip, t_start, t_end, recording, video_start, video_end, video_min, video_max}]`. `id` is `S1`, `S2`, …; `t_*` are `session_t`; `video_*` are `video_t`; `version` is the label shown while recording; `flip` is true if it was the C-flipped partner; `recording` is true while the segment is still open. |
| `events.json` | `[{session_t, video_t, event, ...}]`. `event` is one of `start`, `rec_start`/`rec_stop` (+`seg`), `play`, `pause`, `ended`, `seek` (+`from`), `tick`, `draw_start`/`draw_end` (+`id`, `seg`; `draw_start` also `color`), `undo` (+`id`), `erase`/`unerase` (+`id`; v1.2: the eraser removed a stroke, and Z brought it back), `version` (+`shown`, `base`, `flip`). |
| `strokes.json` | `[{id, seg, version, flip, color, session_t, session_t_end, video_t, video_t_end, paused, points}]`. `points` are `[x, y]`, normalized 0–1 to the video frame (4 decimals). `color` is `red`, `yellow`, `green` or `white`. Undone and erased strokes are left out on save. |
| `voice.json` | `{bin, bins: [[i, level], ...]}`: voice coverage along the video timeline in bins of `bin` seconds (0.25). Drawn as the blue marks above the timeline. |
| `session.json` | From the page: `{format: 3, version, session, created, saved, clock, recorded, video: {duration, width, height}, display: {show_within}}`. The server then overwrites `version`, `versions` and `video`, and adds (**new**) `author`. |
| `transcript.json`, `notes.md`, `notes.json` | Written later by `review notes`, never by the player. |

`spec/take.schema.json` holds JSON Schemas for these files: one definition per file under `$defs` (`session`,
`segments`, `events`, `strokes`, `voice`), plus `dataBody` for the `/data` request body. The end-to-end test validates
every take it produces against them, and so must any new writer.

## Rules

- A take is append-only while open. Saving rewrites the JSON files atomically, and audio is appended by byte offset.
- Nothing outside an R-on/R-off segment is recorded in a take: no audio, and no strokes. Lines drawn while not
  recording belong to a typed comment (spec/notes.md, "Typed comments on a video"), never to a take.
- Nothing is ever deleted by the player.

## Erased lines (v1.2)

The eraser removes any line on screen, recording or not. A line of the take in progress is dropped from that take:
an `erase` event (+`id`) is logged and the stroke is left out of strokes.json; `unerase` brings it back. Takes are
never rewritten after the fact, so a line of an earlier take (history) is erased in the version's
`<stem>.review/erased.json` instead:

```json
{"erased": [{"take": "2026-10-29_01-05-21", "id": 3, "at": "2026-10-30T11:02:00-06:00", "by": "Sam"}]}
```

`/api/history` and the notes engine leave those lines out. The take's own files stay as they were.

### Incremental voice coverage

Segments may include optional `voice: {bin, bins}` coverage in the same sparse format as `voice.json`. It belongs only to that clip, allowing pending audio to stay visible while completed clips become comments, including after reload. Recording may rotate through short segments while the microphone stays open.
