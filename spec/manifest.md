# Render manifest, `manifest/0.1`

A render manifest is a JSON sidecar that says what a rendered video is made of, unit by unit: time ranges, the
timeline elements on screen with their boxes over time, their sources, content hashes, and the spine's words. The
review side uses it to resolve a note to elements (`resolve-note`), to map one version's times to another's, and to
carry notes over to units that didn't change. Without a manifest the review side falls back to frame matching, so
versions made by other tools still work. `spec/manifest.schema.json` is the JSON Schema.

It's deliberately small, so any renderer can emit one. `retake-edit render` writes `<video stem>.manifest.json` next
to the video; `retake-edit manifest` prints it.

## Example

```json
{
  "schema": "manifest/0.1",
  "timeline": { "id": "demo", "rev": 1, "hash": "b3:6d0e…" },
  "video": "demo-r1.mp4",
  "size": [1920, 1080], "fps": 30, "duration": 16.1, "frames": 483,
  "range": [0.0, 16.1],
  "draft": false,
  "stats": { "total": 6, "reused": 5, "rendered": 1 },
  "units": [
    { "unit": "u5", "key": "b3:1e5a…", "kind": "shot", "scene": "body", "shot": "body/footage",
      "t": [5.7, 11.066667], "frames": [171, 332],
      "video_hash": "b3:1709…", "audio_hash": "b3:7d1b…", "segment": "seg/1709….mp4", "reused": false,
      "elements": [
        { "id": "pop/faster", "ptr": "/scenes/1/overlays/0", "kind": "overlay", "z": 3, "t": [7.666667, 8.3],
          "box": [[5.7, 0, 0, 0, 0], [7.666667, 556, 782, 822, 202], [7.7, 616, 796, 702, 178]],
          "anchors": { "from": "word:b/faster#2", "to": "word:b/Watch-0.10" } },
        { "id": "pip/v", "ptr": "/scenes/1/overlays/1/layers/1", "kind": "layer", "parent": "pip", "z": 1,
          "t": [8.4, 10.8], "box": [[5.7, 0, 0, 0, 0], [8.433333, 1785.7, 638, 134.3, 324]],
          "sources": [ { "asset": "proj:clip", "in": 5.0, "out": 7.4 } ] },
        { "id": "body/footage", "ptr": "/scenes/1/shots/0", "kind": "shot", "z": 0, "t": [5.7, 11.066667],
          "box": [[5.7, 0, 0, 1920, 1080]], "sources": [ { "asset": "proj:footage", "in": 10.0, "out": 15.366667 } ],
          "anchors": { "at": "start:b" } },
        { "id": "b", "ptr": "/spine/2", "kind": "spine", "z": -1, "t": [5.7, 10.8],
          "sources": [ { "asset": "proj:vo", "in": 4.3, "out": 9.4 } ] }
      ],
      "words": [ [5.9, 6.1, "This"], [6.14, 6.38, "pop"] ] }
  ]
}
```

## Fields

| Field | Meaning |
|---|---|
| `timeline` | The timeline's `id`, its `rev`, and `hash`: BLAKE3 of the timeline document (canonical JSON). |
| `video` | The video file's name, next to the manifest. |
| `size`, `fps` | The output's composition size and frame rate. Boxes are in pixels of `size`, even for a draft rendered smaller. |
| `duration`, `frames` | The whole timeline's length. |
| `range` | The output seconds this video covers. A `--range` render covers whole units, so it can start before the asked range. |
| `draft` | Rendered at draft quality. |
| `stats` | Units in this render: reused from the cache, rendered now. |

A **unit** is a shot (cut at insert edges) or an insert, encoded as one closed-GOP segment.

| Field | Meaning |
|---|---|
| `unit` | `u<n>`: its position in the timeline (changes when units are added before it). |
| `key` | The unit key: the hash of the fully resolved unit description (stable identity: same key, same pixels). |
| `kind` | `shot`, `insert` or `gap` (background before the first shot). |
| `scene`, `shot` | The scene and the shot (or insert) it shows. |
| `t`, `frames` | Output seconds and frames, `[start, end)`. |
| `video_hash` | BLAKE3 of the encoded segment. Equal hashes in two manifests mean the same bytes. |
| `audio_hash` | BLAKE3 over the per-frame BLAKE3 digests of the mixed PCM (interleaved f32 little-endian stereo) for the unit's frames. Equal hashes mean the same sound. |
| `segment` | The segment's name in the producer's store (`seg/<video hash>.mp4`). |
| `reused` | True when the segment came from the cache. |
| `elements` | What's in the unit (below). |
| `words` | Spine words that start in the unit: `[start, end, text]` in output seconds. |

An **element** is a timeline element live in the unit:

| Field | Meaning |
|---|---|
| `id`, `ptr` | The element id and its JSON pointer in the timeline. |
| `kind` | `shot`, `insert`, `overlay`, `layer` (a video layer inside one of those, with `parent`), `spine` (VO) or `audio`. |
| `z` | Draw order in the unit: higher is on top. Shots and inserts are 0; sound-only elements are -1. |
| `t` | When it's live within the unit, in output seconds. |
| `box` | Change points `[t, x, y, w, h]`: each box holds from `t` until the next entry. `w = h = 0` means not on screen. Graphics boxes are the bounds of their drawn pixels; video boxes are the transformed source rectangle. Absent for sound-only elements. |
| `sources` | `{asset, in, out}`: the source seconds this element plays in the unit. |
| `anchors` | The element's anchors as written in the timeline (`at`, `from`, `to`). |
| `use` | The component the element was expanded from, `<name>@<version>` (`spec/components.md`). Its layers' `ptr`s point into the expansion (`retake-edit expand`). |

## Resolving a note

`retake-edit resolve-note <manifest> <t> [x y w h] [--px]` ranks the elements live at `t` under a stroke box
(normalized 0–1 to the frame, like `images.json` boxes in `spec/notes.md`, or pixels with `--px`):

An element's `score` is the overlap of its box at `t` and the stroke box, over the smaller of the two boxes: a mark
inside an element and a loop drawn around one both score 1.

1. The elements *under* the stroke (score at least 0.5) come first, topmost (`z`) first: a loose loop around a label
   resolves to the label, ahead of the shot under it.
2. Then the rest, by score, then `z`.
3. Ties go to the smaller element (a picture-in-picture before the shot under it).

Without a box, visible elements rank by `z`. Sound-only elements come last. The result also gives the unit and the
spine word at `t` (or the nearest within 0.5 s) for `(audio)` notes:

```json
{ "t": 9.0, "unit": "u5", "key": "b3:1e5a…", "word": [8.8, 8.92, "for"],
  "targets": [
    { "id": "pip/v", "kind": "layer", "ptr": "/scenes/1/overlays/1/layers/1", "score": 1.0, "z": 1,
      "box": [1212, 638, 576, 324], "parent": "pip" },
    { "id": "pip", "kind": "overlay", "ptr": "/scenes/1/overlays/1", "score": 1.0, "z": 1, "box": [1202, 608, 596, 412] },
    { "id": "body/footage", "kind": "shot", "ptr": "/scenes/1/shots/0", "score": 1.0, "z": 0, "box": [0, 0, 1920, 1080] }
  ] }
```

## Comparing versions (informative)

Units with the same `video_hash` in two manifests are the same pixels: notes on them carry over as unchanged, at the
mapped time (`t` of the unit in each manifest gives a piecewise-linear time map). A unit whose hash changed but whose
`shot` is the same maps by shot; a note on it is "answered, check it" when a patch answered it, else "context
changed".
