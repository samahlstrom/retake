# The timeline: a guide

`retake-edit` makes videos from a timeline: one JSON file that describes an edit as data. An agent changes it only
through patches, so every change is small, logged and tied to the notes it answers. This page explains the format
with the sample project's timeline ([examples/sample/timeline.json](../examples/sample/timeline.json)). The exact
rules are in [spec/timeline.md](../spec/timeline.md), and the JSON Schema is
[spec/timeline.schema.json](../spec/timeline.schema.json).

## The idea in one paragraph

A voice-over carries the video. The timeline lists its pieces in order (the *spine*), and everything else hangs off
moments in it: "show the title when she says *few*", "cut to the footage at the start of step two". Those moments are
*anchors*, not seconds. When a piece of the voice-over is trimmed, everything after it moves with it, and no number
has to be rewritten.

## The parts

```json
{
  "schema": "timeline/0.1",
  "id": "how-to",
  "rev": 0,
  "format": { "size": [1280, 720], "fps": 30, "sample_rate": 48000, "background": "#000000" },
  "assets": { … },
  "spine": [ … ],
  "scenes": [ … ],
  "audio": [],
  "end": "end:e+0.60"
}
```

| Part | What it is |
|---|---|
| `rev` | The revision. Each applied patch adds one. |
| `format` | Output size, frame rate, audio rate and the color where nothing is drawn. |
| `assets` | The media files, by id. |
| `spine` | The master clock: voice-over pieces and inserts, played end to end. |
| `scenes` | Shots (what fills the screen) and overlays (what's drawn on top), grouped by scene. |
| `audio` | Music beds and other sound. |
| `end` | Where the video ends, as an anchor. |

Unknown fields are errors, so a typo can't be ignored silently. Put your own data under any object's `meta` field.

## Assets

```json
"assets": {
  "proj:vo":    { "path": "voice/vo.ogg", "words": "voice/vo.words.json" },
  "proj:rinse": { "path": "media/rinse.mp4" }
}
```

A path is relative to the timeline file, absolute, `~/…`, or uses `$VAR`. The engine only reads assets; it never
changes them. `words` is the asset's transcript: the word times that `word:` anchors resolve against.
`retake transcribe` makes one.

## The spine

```json
"spine": [
  { "id": "a", "asset": "proj:vo", "in": 0.4,  "out": 4.8 },
  { "id": "b", "asset": "proj:vo", "in": 5.2,  "out": 9.62 },
  { "id": "c", "asset": "proj:vo", "in": 10.0, "out": 14.25 }
]
```

Each item plays a range of an asset. Item `b` starts where `a` ends. To trim a pause, change an `in` or `out`, and
everything later moves.

An item with `"kind": "insert"` takes over the screen with its own picture and sound, like a meme clip in the middle
of a sentence. Shots pause while an insert plays and continue after it.

## Anchors

Every time in a timeline is an anchor:

| Anchor | Means |
|---|---|
| `start:b`, `end:b` | The edges of an element: a spine item, a shot, an overlay, a scene. |
| `word:c/grams` | When "grams" starts in spine item `c`. `@end` for when it ends, `#2` for the second "grams", `explain*` for a prefix. |
| `t:12.5` | 12.5 seconds into the output. Avoid it: it doesn't move with trims. |

Any anchor takes an offset with a decimal point: `start:b+0.30`, `word:a/few-0.10`.

A wrong anchor is an error with a hint the agent can act on:

```
error[anchor_word_not_found] at /scenes/1/overlays/3/from: anchor word:c/gram: word 'gram' not found in spine item c (did you mean 'grams'?)
  suggestions: grams
```

## Shots and overlays

```json
{ "id": "steps",
  "shots": [
    { "id": "steps/rinse", "at": "start:b",
      "picture": { "layers": [ { "id": "steps/rinse/v", "type": "video", "asset": "proj:rinse", "fit": "cover" } ] } }
  ],
  "overlays": [
    { "id": "label/1", "from": "start:b+0.30", "to": "end:b", "z": 1,
      "layers": [ … ] }
  ] }
```

- A **shot** starts at `at` and lasts until the next shot starts.
- An **overlay** is drawn over the shots from `from` to `to`. Higher `z` is on top.
- Both hold a list of **layers**, drawn bottom to top. Inside them, time starts at 0 when the shot or overlay starts.

Ids are unique across the whole timeline, and they may contain `/`, so `label/1/text` reads like a path.

## Layers

There are four layer types:

| Type | Draws | Main fields |
|---|---|---|
| `text` | Text, shaped with HarfBuzz | `text`, `size`, `fill`, `stroke`, `align`, `max_width`, `font` |
| `shape` | A rectangle or an ellipse | `shape`, `size`, `radius`, `fill`, `stroke` |
| `image` | A picture | `asset`, `size` |
| `video` | Footage | `asset`, `in`, `fit`, `loop`, `hold_from`, `audio` |

Every layer has a `transform` (`anchor`, `position`, `scale`, `rotation`, `opacity`), and can have `crop`, `blur` and
`shadow`. Positions are pixels of the output size, from the top left. The built-in `display` font is Baloo 2 at
weight 800.

From the sample, a step label: a dark bar with text on it, both anchored at their left edge.

```json
{ "id": "label/2/bar", "type": "shape", "shape": "rect", "size": [520, 64], "radius": 10, "fill": "#000000b0",
  "transform": { "anchor": [0, 0.5], "position": [40, 650] } },
{ "id": "label/2/text", "type": "text", "text": "STEP 2   ADD THE COFFEE", "size": 30, "align": "left",
  "transform": { "anchor": [0, 0.5], "position": [64, 650] } }
```

## Animation

Any number in a transform (and most others) can be a constant, keyframes, or an expression.

```json
"scale":   { "keys": [ { "t": 0, "v": 0.7, "ease": "out-back" }, { "t": 0.35, "v": 1.0 } ] },
"opacity": { "keys": [ { "t": "word:a/few-0.10", "v": 0 }, { "t": "word:a/few+0.30", "v": 1 } ] },
"rotation": { "expr": "3 * Math.sin(t * 6.283)" }
```

- A key's `t` is seconds from the start of the shot or overlay, or an anchor.
- `ease` shapes the motion from that key to the next: `linear`, `out-cubic`, `out-back`, `in-out-sine`, …, or a
  cubic Bézier `[x1, y1, x2, y2]` as in CSS.
- An expression is JavaScript run in a sandbox. It sees `t`, `value` and the composition's `props`, and nothing else:
  the same `t` always gives the same result.

## Components and kits

Instead of writing layers, an overlay or a picture can use a component from the library, by name and exact version:

```json
{ "id": "pop/faster", "use": "pop-text@1.0.0", "from": "word:b/faster", "to": "word:b/faster+1.20",
  "props": { "text": "Faster", "color": "kit:color.accent" } }
```

- `retake lib describe pop-text` shows its props (with descriptions and defaults) and its storybook sheet;
  `retake lib add pop-text` copies it into `components/` next to the timeline and pins it in `timeline.lock`.
- `"kits": ["clean@1.0.0"]` brings a kit's tokens (`kit:color.accent`, `kit:size.title`) and its house rules:
  `retake-edit lint edit/timeline.json` lists what breaks them, with times and a hint.
- Change a component's look by patching its `props`. A new version of a component is used element by element:
  `retake-edit component upgrade edit/timeline.json pop/faster --to 1.1.0`. Only those elements render again.
- `retake-edit expand` prints the timeline with every component expanded into layers.

[spec/components.md](../spec/components.md) has the details, and how to write your own.

## Audio

```json
"audio": [
  { "id": "bed", "asset": "proj:music", "from": "start:a", "loop": true, "gain_db": -14,
    "fade_in": 1.0, "fade_out": 1.5, "duck": { "db": -8 } }
]
```

`duck` lowers the bed while the voice-over speaks. The whole timeline is mixed in one pass.

## Patches

An agent changes the timeline only through patches. A patch names the revision it was written for, the notes it
answers, and a one-line summary:

```json
{
  "base": 1,
  "answers": ["v2#1"],
  "summary": "Bigger step labels: 44 px text on a taller bar",
  "ops": [
    { "op": "set", "id": "label/2/text", "path": "size", "value": 44 },
    { "op": "set", "id": "label/2/bar", "path": "size", "value": [700, 84] }
  ]
}
```

- Ops address elements by id: `set`, `insert`, `remove` and `move`.
- A patch applies whole or not at all. The result must validate, anchors included.
- A patch for an older revision is refused. Read the timeline again and rebase.
- Applied patches are logged in `<timeline>.patches.jsonl`, each with its inverse, so `patch revert` can undo one.
- `"answers": []` marks a change the agent made on its own. It's listed separately.

## Commands

```bash
retake-edit validate timeline.json                  # schema, ids, references, anchors
retake-edit resolve  timeline.json                  # every anchor as seconds and frames; the units
retake-edit patch apply timeline.json patch.json    # --dry-run to check first
retake-edit patch log timeline.json
retake-edit patch revert timeline.json p2
retake-edit plan   timeline.json                    # what a render would reuse and redraw
retake-edit render timeline.json -o out.mp4         # --range a-b for part of it, --draft for speed
retake-edit frames timeline.json 3.5 12.0           # PNG stills, for checking a change
```

Every command takes `--json` and reports errors as `{code, message, path, hint}`.

## Rendering and the cache

The renderer cuts the output into units: each shot (split where inserts interrupt it) and each insert. Each unit is
encoded on its own and cached under a hash of everything that goes into it. A render only draws units whose inputs
changed, and copies the rest byte for byte. In the quickstart, the label patch redrew 3 of 5 units.

Each render also writes a manifest next to the video (`<name>.manifest.json`, [spec/manifest.md](../spec/manifest.md)).
It says which timeline elements are on screen where, so a reviewer's stroke can be traced back to the element it
circles.

## Editing with the timeline

When the versions you review are rendered from a timeline, Retake connects the two ([spec/bridge.md](../spec/bridge.md)).
A push renders the timeline itself, notes point at timeline elements, C lands on the matching moment, and notes carry
over to the next version.

**A timeline project.** Retake looks for the timeline from the project root (the folder that holds the version
folders): the path in `retake.toml` under `[edit] timeline`, else `edit/timeline.json`. The sample's `how-to/retake.toml`
points at `../edit/timeline.json`.

### Push a revision as a version

`--render` defaults to on-demand streaming: push a poster and pending version, then render when a reviewer opens it. Unchanged units reuse the cache, and closing the last viewer stops work. The example below uses `--no-stream` to wait for a complete MP4.

```bash
retake-edit patch apply edit/timeline.json edit/patches/02-v3.json   # its "answers": ["v2#1"]
retake push how-to/v2/how-to_v2.mp4 --render --no-stream --parent v2
```

```
pushed v3 from v2: ~/retake-sample/how-to/v3/how-to_v3.mp4
  rendered ../edit/timeline.json rev 2: 5 units, 2 reused, 3 rendered
  #1 Bigger step labels: 44 px text on a taller bar (p2)
  answered #1
```

`--render --no-stream` renders the current revision (only the units that changed draw again) and moves it into the family as the
next version, with its manifest beside it (`how-to_v3.manifest.json`) and a record of the push (`how-to_v3.push.json`).
The changelog comes from the patches made since the parent was rendered: each patch's `answers` and `summary` become
the line for those notes, so nothing is typed twice. `--change` still replaces a line, and `--ask` asks about a note
instead. A patch with `"answers": []` is listed as *unrequested*, so you see everything that changed. The push rule
holds as for any push, and it's checked before anything renders. `--draft` renders at half size with a fast encoder.
An agent does the same with `version_push` and `render: true`.

Retake runs `retake-edit` from `[edit] retake_edit` in its config, else `$RETAKE_EDIT`, `$PATH`, or
`editor/target/release` in the repo it was built from.

### Notes point at elements

When `retake notes sync` (or the job after Done) runs for a version that has a manifest, each note gets *targets*:
the timeline elements under its stroke at its moment, best first. A loop around a label resolves to the label, ahead of
the shot under it. `retake notes list` shows the best one (`[1 stroke, on label/2]`), and `--json` and the agent's
`notes_list` give them all, with their ids, so the agent patches the right element.

### C lands on the matching moment

From the manifests of two versions, Retake builds a map from one's times to the other's: units with the same pixels,
the same voice-over seconds, or the same shot. When a patch trims 0.3 s of voice-over, everything after it plays 0.3 s
earlier, and C, the version list, a question's version chips and "see it in v3" all show the other version at the
matching moment, not the same clock time. `GET /api/timemap?from=v2&to=v3` serves the map. Without manifests the time stays
as it is.

### Notes carry over

The newest version's sidebar shows its parent's notes at their new times, with what became of them: *unchanged* (the
same pixels, and for `(audio)` notes the same sound), *answered, check it* (that part changed, and the note was
answered), or *context changed*. Questions about the parent's notes move to their new times too.

### Faster drafts

`retake-edit proxy edit/timeline.json` makes a small copy of each video asset with a keyframe every half second, and
draft renders read those instead. That helps most with long screen or camera recordings, which often have a keyframe
only every few seconds. Static graphics, such as a card with a soft shadow, are drawn once per render, not once per
frame.

The editor's demo runs the whole loop with no person and no voice: `editor/examples/demo/loop.sh` renders v1, writes a
review take, syncs its notes, patches, pushes v2 with `--render`, and prints the changelog, the carry-over and the time
map.
