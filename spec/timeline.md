# Timeline format, `timeline/0.1`

A timeline is one JSON file that describes an edit as data. An agent changes it only through patches (below). The
engine (`editor/`, the `retake-edit` binary) validates it, resolves its times, and renders it. It re-renders only the
units whose resolved description changed. `spec/timeline.schema.json` is the JSON Schema; the semantic rules here
(unique ids, resolvable anchors, shot order) are checked by `retake-edit validate` on top of it.

Times are seconds (floats). Sizes and positions are pixels of `format.size`, with the origin at the top left and y
down. Colors are `#rgb`, `#rrggbb` or `#rrggbbaa`.

## Top level

```json
{
  "schema": "timeline/0.1",
  "id": "demo",
  "rev": 3,
  "format": { "size": [1920, 1080], "fps": 30, "sample_rate": 48000, "background": "#000000" },
  "kits": [],
  "assets": { "proj:vo": { "path": "media/vo.wav", "words": "media/vo.words.json" } },
  "fonts": { "display": { "path": "fonts/Baloo2-Variable.ttf", "weight": 800 } },
  "spine": [ … ],
  "scenes": [ … ],
  "audio": [ … ],
  "end": "end:outro+0.50"
}
```

| Field | Meaning |
|---|---|
| `schema` | Always `timeline/0.1`. A new version ships with a migration. |
| `id` | The project id. |
| `rev` | The revision. Each applied patch adds 1. A patch must name the revision it was written against. |
| `format.size` | Output `[width, height]`, even numbers. |
| `format.fps` | Integer frames per second. Every unit boundary falls on a frame. |
| `format.sample_rate` | Audio sample rate. Default 48000. It must be a whole number of samples per frame (48000 works for 24, 25, 30, 50, 60 and 120 fps). |
| `format.background` | What shows where no layer draws. Default `#000000`. |
| `kits` | Brand kits the timeline uses, e.g. `"clean@1.0.0"`: their tokens (`kit:color.accent`) and house rules (`spec/components.md`). A `"brand:acme@3"` kit is loaded when its folder is found, else only recorded. Other entries (plug-ins such as `"plugin:deadlock"`, names without a version) are recorded and ignored. |
| `assets` | The media the timeline uses, by asset id (below). |
| `fonts` | Font names for text layers. `display` is built in (the vendored Baloo 2, weight 800) unless redefined. |
| `spine` | The master clock (below). At least one item. |
| `scenes` | Shots and overlays (below). |
| `audio` | Music beds and other audio (below). |
| `end` | An anchor for the end of the output. Default: the end of the last spine item. |
| `expr_lib` | JavaScript helpers every expression can use ("Expressions", below). |

Readers must reject unknown fields: a typo in a property name would otherwise be silently ignored. Extra data goes
under any object's `meta` field, which the engine never reads.

## Ids

Every element has an `id`: spine items, scenes, shots, overlays, layers and audio elements. Ids are unique across the
whole timeline, so a patch or an anchor can name any of them directly. An id matches
`^[A-Za-z0-9][A-Za-z0-9_./-]*$`: letters, digits, `_`, `.`, `/` and `-`, so `why/steps` is fine.

## Assets

```json
"assets": {
  "proj:footage": { "path": "media/footage.mp4" },
  "proj:vo":      { "path": "media/vo.wav", "words": "media/vo.words.json" },
  "lib:logo":     { "path": "${RETAKE_LIB}/logo.png" }
}
```

- An asset id is any string without whitespace. Scopes (`proj:`, `brand:`, `lib:`) are a convention until the catalog
  exists.
- `path` is relative to the timeline file's folder, absolute, `~/…`, or uses `$VAR` / `${VAR}` from the environment.
  So a project can live anywhere and point at local media in place. The engine only ever reads assets.
- `words` (spine assets) is the asset's transcript, in the `transcript.json` word format of `spec/notes.md`:
  `{"files": {"<name>": {"words": [{"w", "s", "e", "p"}]}}}` (the entry whose name matches the asset's file name, or
  the only one), or just `{"words": [...]}`, or a bare array. `w` may carry a leading space; `s` and `e` are seconds
  into the asset.
- Content identity: the engine hashes each asset (BLAKE3), so a file changed in place is a different input. Hashes are
  cached by path, size and modification time.

## Spine

The spine carries the master clock, like a primary storyline. Its items play end to end, in order. Shots, overlays
and audio attach to spine anchors, so trimming a VO range or inserting a clip ripples everything after it with no
arithmetic.

```json
"spine": [
  { "id": "open_a", "asset": "proj:vo", "in": 0.20, "out": 4.10 },
  { "id": "meme",   "kind": "insert", "asset": "proj:meme", "in": 8.85, "frames": 45, "hold_from": 10.2,
    "audio": { "gain_db": -3, "fade_out": 0.10 } },
  { "id": "open_b", "asset": "proj:vo", "in": 4.90, "out": 9.00, "gap_before": 0.30 }
]
```

| Field | Meaning |
|---|---|
| `kind` | `vo` (default): audio only; the picture comes from the shots. `insert`: the asset's own picture and sound take over the screen for the item's length. |
| `asset`, `in` | The source and where in it the item starts. |
| `out` / `dur` / `frames` | Exactly one: the source end, the length in seconds, or the length in frames. |
| `gap_before` | Silence before the item, in seconds. |
| `hold_from` | Inserts: the picture freezes on the frame at this source time while the sound runs on. |
| `audio` | `{gain_db, fade_in, fade_out, mute}` for the item's sound. |
| `picture` | Inserts: a composition (below) to show instead of the asset full frame. |

Item *i* starts where item *i − 1* ends, plus its `gap_before`. **Shot clocks pause during inserts:** a shot cut by an
insert continues after it from the frame where it stopped, and overlays are hidden under inserts.

## Anchors

Every time in a timeline is an anchor: a string the resolver turns into an output time, then a frame.

| Anchor | Resolves to |
|---|---|
| `word:<spine-id>/<word>[#n][@end]` | The start (or with `@end`, the end) of the n-th word (1-based, default 1) in that spine item that matches `<word>`. Matching ignores case and punctuation; a trailing `*` matches a prefix (`word:a/explain*`). A word belongs to the item when its midpoint lies in `[in, out)`. |
| `start:<id>`, `end:<id>` | An element's edge: a spine item, a shot (it ends where the next shot starts), an overlay or audio element (`from` / `to`), or a scene (its first shot's start, its last shot's end). |
| `t:<seconds>` | An absolute output time. Discouraged: it doesn't ripple. |

Any anchor can end with an offset in seconds, `+0.30` or `-0.12`. The offset must have a decimal point, so `shot-2`
stays an id. Examples: `word:why_b/same-0.30`, `start:why_a-0.25`, `end:open_c+0.30`, `word:open_b/deck#2@end`.

Resolution is deterministic and errors are typed, with a JSON pointer and a hint the agent can act on:
`anchor word:why_b/Everist not found in spine item why_b (did you mean 'Everest,'?)`. A cycle (`start:a` → `end:b` →
`start:a`) is an error. Resolved times go into the render manifest, never back into the timeline.

## Scenes and shots

```json
"scenes": [
  { "id": "cold-open",
    "shots": [
      { "id": "open/fight", "at": "start:open_a",
        "picture": { "layers": [ { "id": "open/fight/v", "type": "video", "asset": "proj:footage", "in": 2.0 } ] } },
      { "id": "open/title", "at": "word:open_a/item",
        "picture": { "background": "#14141a", "layers": [ … ] } }
    ],
    "overlays": [
      { "id": "open/tip", "from": "word:open_b/glue", "to": "word:open_b/No-0.05", "z": 1,
        "props": { "label": "NO GLUE NEEDED" }, "layers": [ … ] }
    ] }
]
```

- A **shot** starts at `at` and runs until the next shot starts (in document order across scenes), or to the end.
  Shots must resolve in non-decreasing order. Before the first shot the picture is `format.background`.
- A shot's `picture` and an **overlay** are both *compositions*: `{background?, props?, expr_lib?, layers}`, or a
  component use `{use, props}` (below). Layers draw bottom to top. An overlay draws over the shots from `from` to `to`; overlays
  stack by `z` (default 0), then document order.
- Composition time `t` starts at 0 when the shot or overlay starts, and pauses during inserts.

## Layers (the After Effects pattern)

Graphics are data. A layer has a `type`, its content fields, a transform and optional effects. Every animatable
property ("prop" below) is a constant, keyframes, or an expression.

Common fields:

| Field | Meaning |
|---|---|
| `id`, `type` | `type` is `text`, `shape`, `image`, `video` or `group`. |
| `from`, `to` | When the layer shows, in composition seconds or as anchors. Default: the whole composition. |
| `transform.anchor` | The point of the layer's content box that `position` places, as fractions of the box. Default `[0.5, 0.5]`, the center. |
| `transform.position` | Prop `[x, y]`. Default: the center of the frame. |
| `transform.scale` | Prop, a number or `[sx, sy]`. Default 1. |
| `transform.rotation` | Prop, degrees clockwise. Default 0. |
| `transform.rotation_x`, `transform.rotation_y` | Props, degrees: the layer turned in 3D about its anchor and seen in perspective (below). Default 0. |
| `transform.perspective` | Prop, the viewer's distance from the frame in pixels for `rotation_x` / `rotation_y`. Default: the frame's diagonal. |
| `transform.corners` | Prop of 8 numbers, `[x0, y0, x1, y1, x2, y2, x3, y3]`: a corner pin, where the content box's top-left, top-right, bottom-right and bottom-left corners land, in composition pixels. It replaces `anchor`, `position`, `scale`, `rotation` and the 3D props (setting both is an error). |
| `transform.opacity` | Prop, 0–1. Default 1. |
| `crop` | Prop `[x, y, w, h]` in the content's own pixels (images and video: source pixels, before `fit`). |
| `blur` | Prop, a Gaussian blur sigma in pixels. Any layer, video and groups included. |
| `shadow` | `{color, dx, dy, blur}`: a drop shadow under the layer. Any layer. |
| `blend` | How the layer combines with what's under it: `normal` (default), `add`, `screen`, `multiply`, `overlay`, `lighten` or `darken` (below). |
| `mask` | A shape that cuts the layer out, with feather and an optional outline (below). |
| `matte` | `{layer, mode}`: a track matte, another layer of the same composition whose alpha or luma cuts this one out (below). |

Content by type:

- **text**: `text` (a string, or `{"expr": …}` returning one), `font` (default `display`), `size` (px), `weight`,
  `fill` (default `#ffffff`), `stroke` (`{color, width}`, drawn outside the fill), `align` (`left`, `center`
  (default), `right`), `line_height` (multiple of the size, default 1.12), `max_width` (wrap at spaces), `uppercase`,
  `highlight` (`{words, fill}`: words `[a, b)` of the text, counted from 0 across whitespace-separated words, drawn in
  `fill`; `words` is a prop, so keyframes or an expression can move it, e.g. a karaoke caption's current word; `[-1,
  -1]` highlights nothing). `\n` breaks lines. Text is shaped with HarfBuzz, so kerning applies. Rendering options:
  `hinting`, `subpixel` and `metrics` (below).
- **shape**: `shape` (`rect`, `ellipse` or `path`), `size` `[w, h]`, `radius` (rect corners), `path` (SVG path data
  in the layer's own pixels, for `path`; `size` then defaults to the path's right and bottom extent), `fill`,
  `stroke`.
- **image**: `asset`, optional `size` `[w, h]` (one of them may be `null` to keep the aspect), `resample` (below).
- **video**: `asset`; `in` (the source time at composition time 0, default 0) or `sync: {src, to}` (source time `src`
  plays at anchor `to`); `fit` (`contain` (default), `cover`, `stretch` or `none`: how the cropped source fits the
  frame before the transform); `loop`; `hold_from` (freeze at that source time); `clock` (`local` (default) or
  `timeline`: the source runs on the output clock, so a looping background keeps its phase across cuts); `audio`
  (`{gain_db, fade_in, fade_out}`: mix the source's sound; muted when absent); `resample` (below); `delogo`
  (`[x, y, w, h]` in source pixels, or a list of them: ffmpeg's `delogo` interpolates over the box, e.g. a recorded
  FPS meter, before anything else sees the frame).
- **group**: `layers`, drawn together as one picture, then transformed, masked, faded and blended as one layer (an
  opacity group, like an After Effects precomp). Its content box is the whole frame, so its default transform
  changes nothing, and its children are placed in composition pixels on the same clock. `transform.opacity` applies
  to the flattened group (children don't show through each other), and the children's `blend` modes combine them
  with each other inside the group only. Ids inside are unique like any other.

### Perspective and corner pins

`rotation_x` and `rotation_y` turn a layer in 3D about its anchor point, and `perspective` projects it back onto the
frame, as CSS's `perspective(d) rotateX(x) rotateY(y)`: a content point is placed relative to the anchor, scaled,
rotated in the plane (`rotation`), turned about the vertical axis (`rotation_y`; positive turns the right edge away
from the viewer), then about the horizontal axis (`rotation_x`; positive tips the top edge away), projected from a
viewer `perspective` pixels in front of the frame with the vanishing point at `position`, and moved to `position`.
`corners` instead pins the content box's four corners anywhere (a homography). Both work on every layer type, with
keyframes and expressions, and both are one projective transform: graphics are drawn through it by Skia, and footage
is sampled through it from one decode per source (bilinear, with box-filtered mip levels where it shrinks, and edges
anti-aliased against transparency). A layer with perspective is decoded at its 2D size (the size `scale` gives,
before the projection).

### Blend modes

`blend` combines the layer's pixels (after its mask, matte, effects and opacity) with everything drawn under it in
its composition: `normal` ("over"), `add` (sum, clipped at white), `screen`, `multiply`, `overlay`, `lighten` and
`darken`, the separable modes of the W3C compositing spec (as Skia's `Plus`, `Screen`, `Multiply`, `Overlay`,
`Lighten` and `Darken`), on premultiplied 8-bit sRGB values. A glow drawn with `add` brightens what's under it the
way a light does; `multiply` darkens. Inside a group, a child's mode combines it with the group's earlier children
only.

### Masks and mattes

```json
"mask":  { "shape": "rect", "radius": 22, "feather": 0.6, "stroke": { "color": "#ece7dc", "width": 2 } },
"matte": { "layer": "card/fade", "mode": "luma" }
```

A **mask** is in the pixels of the layer's content box (the box `anchor` refers to: a text's laid-out box, a shape's
`size`, an image's displayed size, a video's cropped source after `fit`, a group's frame), with the origin at its
top-left, so it moves, scales, turns and warps with the layer.

| Field | Meaning |
|---|---|
| `shape` | `rect` (default), `ellipse` or `path`. |
| `rect` | Prop `[x, y, w, h]`: the rect or the ellipse's bounds. Default: the whole content box. |
| `radius` | Prop: rounded corners of a `rect`. Default 0. |
| `path` | SVG path data, for `path`. |
| `feather` | Prop: a Gaussian blur sigma of the mask's edge, in content pixels. Default 0 (a sharp, anti-aliased edge). |
| `invert` | Keep what's outside the shape instead. |
| `stroke` | `{color, width, align}`: an outline drawn along the mask's edge, over the masked content (so a rounded card's outline follows its corners). `align` is `inside` (default: the outline stays within the shape), `center` or `outside`. |

The order is After Effects': content, then the mask (and its outline), then `blur` and `shadow`, then opacity, then
the track matte, then the blend mode. So a masked card casts a rounded shadow; to cut a shape out of a *blurred*
layer (a glow outside a card only), use a track matte, which applies after the blur.

A **track matte** names another layer of the same `layers` list. That layer is drawn only as the matte, never by
itself (its own times, transform and effects apply), and the layer is multiplied by its `alpha` (default),
`alpha-inverted` (1 − alpha), `luma` (the Rec. 709 luma of its premultiplied color, so transparent counts as black)
or `luma-inverted`. A matte layer can't have a matte of its own.

### Resampling

`resample` on image and video layers: `bilinear` (default) or `lanczos`. With `lanczos`, the source is resampled once
with a Lanczos-3 filter to its 2D-scaled size (the size `scale` gives, before rotation and perspective), then placed:
images like Pillow's `Image.LANCZOS` resize; footage in the decode pipe with ffmpeg's Lanczos scaler, cropped first
when the crop doesn't change over the unit (so a cropped card is decoded at exactly its size and not resampled
twice). Placed on whole pixels and not rotated, the resized pixels are copied as they are.

### Text rendering

| Field | Meaning |
|---|---|
| `hinting` | `none`, `slight`, `normal` or `full`: FreeType's hinting of the outlines. Default: Skia's paragraph default (`slight`, with subpixel positioning). |
| `subpixel` | `true` (default): glyphs sit at fractions of a pixel. `false` puts each glyph on whole pixels, as FreeType-based tools such as Pillow do. |
| `metrics` | `skia` (default): each line box is `line_height` × `size` with the font's ascent and descent centred in it (half leading). `pillow`: lines are placed as Pillow's `ImageDraw.text(…, anchor="mm")` places them, centred between the font's ascender and descender rounded to whole pixels as FreeType rounds them, with whole-pixel glyph positions, normal hinting and Pillow's stroke. Use it to match drawings made with Pillow. |

### Props, keyframes and easing

```json
"scale":    { "keys": [ { "t": 0, "v": 1.18, "ease": "out-back" }, { "t": 0.12, "v": 1.0 } ] },
"position": { "keys": [ { "t": "word:open_b/deck", "v": [960, 1200], "ease": [0.2, 0.8, 0.2, 1] },
                        { "t": "word:open_b/deck+0.40", "v": [960, 540] } ] },
"opacity":  { "expr": "t < 0.2 ? t / 0.2 : 1" },
"rotation": { "value": 0, "expr": "value + 3 * Math.sin(t * 6.283)" }
```

- A prop is a constant (a number or an array), `{"keys": [...]}`, `{"value": v, "expr": "…"}` or `{"expr": "…"}`.
  Keys may also carry `expr`, which then sees the keyframed value as `value`.
- A key is `{t, v, ease?}`. `t` is composition seconds or an anchor (resolved relative to the composition's start).
  Keys must be in time order. Before the first key the prop holds the first value; after the last, the last value.
- `ease` on a key shapes the segment *from* that key to the next (as in CSS and Lottie). Named curves: `linear`
  (default), `hold`, `in-quad`, `out-quad`, `in-out-quad`, `in-cubic`, `out-cubic`, `in-out-cubic`, `in-out-sine`,
  `smoothstep`, `in-back`, `out-back`, `in-out-back`, `out-expo`. Or a cubic Bézier `[x1, y1, x2, y2]` with x1, x2 in
  0–1, like CSS `cubic-bezier()`.
- Arrays interpolate per component and must have the same length in every key.

### Expressions

`expr` is a JavaScript expression run in an embedded, sandboxed QuickJS. It sees only `t` (composition seconds),
`value` (the keyframed or constant value, if any) and `props` (the composition's `props`, read-only). It has no I/O,
no `Date`, and `Math.random` throws, so the same `t` gives the same result in any order. Each call has a time and
memory limit. The result must have the prop's type (a number, an array of numbers, or a string for `text`).

**Shared code.** `expr_lib` holds JavaScript statements (helper functions and constants) that every expression can
use: at the top level for the whole timeline, and on a composition (a shot's `picture`, an overlay, an insert's
`picture`) for its layers, after the timeline's. An expression then runs as `(() => { <timeline expr_lib>
<composition expr_lib> return (<expr>); })()`, so it stays one expression and the helpers aren't repeated in every
prop. A library is part of the expressions it serves: changing it re-renders exactly the graphics that use it.

```json
"expr_lib": "const ease = x => (x = Math.min(1, Math.max(0, x)), x * x * (3 - 2 * x));",
"layers": [ { "id": "pop", "type": "text", "text": "HI", "transform": { "opacity": { "expr": "ease(t / 0.3)" } } } ]
```

## Components and kits

A composition can be a component instead of layers: an overlay `{"id", "use": "pop-text@1.0.0", "from", "to",
"props"}`, or a picture `{"use": "title-card@1.0.0", "props"}`. The engine expands it into layers before anything
else, pinned by the lockfile `<stem>.lock`; kit tokens (`"fill": "kit:color.accent"`) are replaced by the kits'
values. A composition prop `"$words:<spine-id>"` becomes that spine item's words on the composition's clock. See
`spec/components.md`.

## Audio

```json
"audio": [
  { "id": "bed", "asset": "proj:music", "from": "start:open_a", "to": "end:outro", "in": 0, "loop": true,
    "gain_db": -18, "fade_in": 1.0, "fade_out": 2.0, "duck": { "db": -7 } }
]
```

`from` / `to` are anchors (`to` defaults to the end). `duck` lowers the element by `db` while the spine's VO is
speaking (fast attack, 0.35 s release). The whole timeline is mixed in one pass: spine VO and inserts, video layers
with `audio`, and audio elements, each with gain and fades.

## Patches

The agent changes the timeline only through patches. A patch addresses elements by id (not by array index, so it
isn't RFC 6902), names the revision it was written against, the notes it answers, and a one-line summary:

```json
{ "base": 3, "answers": ["v2#5"], "summary": "Pop says FASTER on 'faster', not 'quick'",
  "ops": [
    { "op": "set", "id": "open/tip/text", "path": "text", "value": "FASTER" },
    { "op": "set", "id": "open/tip", "path": "from", "value": "word:open_b/faster" }
  ] }
```

| Op | Fields | Does |
|---|---|---|
| `set` | `id?`, `path`, `value` | Sets the value at `path` inside the element (creating an object key if missing). An empty path replaces the whole element; it must keep its id. |
| `insert` | `id?`, `path`, `value`, `before?` / `after?` / `index?` | Inserts `value` into the list at `path` inside element `id`. Position: before or after a sibling id, at an index, or at the end. |
| `remove` | `id?`, `path?` | Removes the element from its list, or, with `path`, the key or list item at that path inside it. |
| `move` | `id`, `to: {id?, path, before?/after?/index?}` | Moves the element into a list, addressed as for `insert`. |

- **The root.** Without `id`, an op addresses the timeline itself: `{"op": "insert", "path": "spine", …}`,
  `{"op": "set", "path": "assets/proj:music~1v2", …}`, `{"op": "set", "path": "end", …}`. `rev` and `schema` can't be
  set.
- **Paths** are `/`-separated, inside the element: `transform/scale/keys/1/v`. In a list, a segment selects the item
  with that `id`, else a numeric index: `layers/title/text`. Escape `/` in a segment as `~1` and `~` as `~0`, as in a
  JSON pointer (asset ids often contain `/`).
- **Base.** A patch whose `base` isn't the current `rev` is rejected (`patch_base_mismatch`): re-read and rebase.
- **Atomic.** All ops apply, then the result must pass `validate` (schema, ids, anchors). Otherwise nothing changes.
- **Log.** An applied patch increments `rev` and is appended to the patch log, `<timeline stem>.patches.jsonl` next to
  the timeline file: one JSON object per line, the patch plus `id` (`p<rev>`), `rev` (the revision it produced),
  `applied` (UTC time) and `inverse` (the ops that undo it).
- **Revert** applies a patch's inverse as a new patch (`reverts: "p4"`, `answers: []`). It is refused if a later
  patch changed the same values (`patch_conflict`); revert that one first.
- `answers: []` marks the agent's own initiative, listed separately in a version's changelog.

## Units and caching (informative)

The renderer splits the output into **units**: each shot, cut at insert edges, and each insert. A unit is encoded as
one closed-GOP H.264 segment, and segments concatenate with `-c copy`. Two keys decide what re-renders:

- **Layer key** (position-independent): a hash of a run of graphics layers as resolved (keyframe times in
  composition time), the canvas, fps, the frame range in composition time, and the font, image and expression
  inputs. A pop is reused wherever it lands. Consecutive layers split into static runs (nothing changes over the
  range) and animated runs; a static run is one image, and its key leaves out the range, so it's reused by a unit of
  any length.
- **Unit key**: a hash of the fully resolved unit description, the only input the compositor gets: canvas, frame
  count, encoder settings, every layer with its source hashes and source times (including absolute-time inputs such
  as a `clock: timeline` background's phase), and the layer keys of the graphics on it.
- A graphics layer that blends with what's under it in another way than `normal`, has a track matte, or is one, is
  a run of its own (the compositor applies the mode and the matte, so they are in the unit key, not the layer key).
  A group of graphics only is drawn inside its run; a group with footage in it is composited by the compositor, its
  children rendered into the group's own picture first.

So a ripple that moves a unit without changing what it shows reuses its segment byte for byte, and a ripple that
changes a unit's length re-encodes it but reuses its cached graphics. Both keys hash a component's expansion, never
its name: a changed component re-renders only the elements whose expansion changed. See `spec/manifest.md` for what a render
publishes.
