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

`retake push <video> --render [--draft] [--parent vN] [--label vN] [--change ID="…"]… [--ask ID="…"]…`, and MCP
`version_push` with `render: true` (and `draft`), take no file: they render the current timeline revision.

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
