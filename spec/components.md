# Components, brand kits and the shared library, `retake-component/0.1`

Motion graphics and edit styles as code anyone can read, diff, share and render: no MOGRT, no `.aep`, no app that
owns the format. A **component** is a reusable composition in plain JSON: a props schema, layers in the timeline's
own layer model (`spec/timeline.md`) with `$props` substitution, and JavaScript expressions for logic. A **brand
kit** holds tokens (colors, fonts, sizes, safe areas, caption style, pacing), the components a channel uses, and its
**house rules**, which `retake-edit lint` checks. A **registry** is a static `index.json` anyone can host; `retake lib
add` copies a package into the project (the shadcn model: the project owns its copy). Nothing is published anywhere.

The editor (`editor/`, `retake-edit`) expands, locks, previews and lints. The Go side (`internal/lib`, `retake lib
…`, MCP) searches, adds, packs and exports. Change this file first, then the code.

## A component

A component is a folder with a `component.json`:

```json
{
  "schema": "retake-component/0.1",
  "name": "pop-text",
  "version": "1.0.0",
  "title": "Pop text",
  "description": "One or two words that pop in over the picture, settle, and fade out. One at a time.",
  "license": "CC0-1.0",
  "authors": ["Retake contributors"],
  "tags": ["text", "emphasis"],
  "category": "pop",
  "use_as": ["overlay"],
  "canvas": [1920, 1080],
  "duration": 1.6,
  "props": {
    "type": "object",
    "additionalProperties": false,
    "required": ["text"],
    "properties": {
      "text":  { "type": "string", "maxLength": 40, "description": "The words. \\n breaks the line." },
      "color": { "type": "string", "default": "kit:color.accent", "description": "Fill color, #rrggbb or a kit token." },
      "y":     { "type": "number", "default": 860, "description": "Vertical center, in design pixels." }
    }
  },
  "tokens": { "color": { "accent": "#ffd23f" } },
  "layers": [
    { "id": "text", "type": "text", "text": "$props.text", "fill": "$props.color", "size": 120,
      "transform": { "position": [960, "$props.y"],
                     "scale": { "keys": [ { "t": 0, "v": 1.3, "ease": "out-back" }, { "t": 0.14, "v": 1 } ] },
                     "opacity": { "keys": [ { "t": "end:$id-0.12", "v": 1 }, { "t": "end:$id", "v": 0 } ] } } }
  ],
  "examples": [ { "name": "one word", "props": { "text": "FASTER" } } ],
  "changelog": [ { "version": "1.0.0", "changes": "First version." } ]
}
```

| Field | Meaning |
|---|---|
| `schema` | `retake-component/0.1`. |
| `name` | `^[a-z][a-z0-9-]*$`. Unique in a registry, across components and kits. |
| `version` | Semver, `MAJOR.MINOR.PATCH` (a `-pre` suffix is allowed). A released version never changes: a fix is a new version. |
| `title`, `description` | For people and agents. Say what it is for and how it behaves. |
| `license` | An SPDX id. Packing requires one of `CC0-1.0`, `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `OFL-1.1`, `CC-BY-4.0`. |
| `authors`, `tags` | Free text; tags are searched. |
| `category` | One word house rules match on: `pop`, `title`, `lower-third`, `caption`, `pip`, `progress`, `zoom`, `counter`, `callout`, `freeze`, or your own. |
| `use_as` | Where it may be used: `overlay`, `picture` (a shot's or an insert's picture), or both. |
| `canvas` | The design canvas `[w, h]` every pixel value in the component is in. Default `[1920, 1080]`. |
| `duration` | A suggested length in seconds; previews use it. |
| `props` | A JSON Schema (draft 2020-12) of the props, `type: object`. Give every property a `description` and, when it's optional, a `default`: agents read them. Props are checked against it, after defaults are filled in. |
| `tokens` | Default kit tokens (below), used when the timeline's kits don't define a token. |
| `inputs` | Files in the package: `fonts` (`{name: {path, weight?}}`) and `images` (`{name: {path}}`). |
| `background` | Pictures only: the composition's background color (a template). |
| `layers` | The layers, as templates (below). Ids are local: `text` becomes `<element id>/text`. |
| `backend` | Instead of `layers`: a program that draws the component's frames, `{command, timeout?}` ("Backends", below). A component has `layers` or a `backend`, not both. |
| `examples` | Prop sets: `[{name, props, duration?, description?}]`. Previews render each; they are checked against `props`. |
| `changelog` | `[{version, date?, changes}]`, newest first. |
| `meta` | Free data, never read. |

A component may hold other files: a `README.md`, `previews/` (made by `retake-edit component preview`), and the
files `inputs` names. A font file needs its license file in the same folder (`OFL.txt`, `LICENSE…`).

### Props that are assets

A prop whose schema has `"x-retake-input": "video" | "image" | "audio"` takes an asset id of the timeline
(`proj:clip`). Expansion checks that the timeline's `assets` defines it. Previews give footage and image inputs a
generated placeholder (an ffmpeg gradient clip, a checkerboard), so a component can be seen before it's used.

### Templates

`layers` and `background` are templates. Before the layers are used:

| In a template | Becomes |
|---|---|
| `"$props.<name>[.<path>]"` (the whole string) | The prop's value, of any JSON type. A missing value removes the key it is under. |
| `"$id"` | The element's id: the overlay, the shot, or the spine item of an insert. |
| `"$font.<name>"`, `"$image.<name>"` | The font name, or the asset id, of a file in `inputs`. |
| `"kit:<path>"` (the whole string) | A kit token (below). |
| `{"$expr": "<JavaScript>"}` | The expression's value, computed once at expansion. It sees `props` (merged, tokens resolved), `kit` (the tokens), `canvas` (`{w, h}`, the design canvas) and `id`. Same sandbox as `expr` (`spec/timeline.md`, "Expressions"). |
| `$id` or `$props.<path>` inside a longer string | Its text: `"end:$id-0.12"` → `"end:pop/a-0.12"`. `$$` is a literal `$`. |

Strings under an `expr` key (runtime expressions) and under `meta` are left alone: runtime expressions read the same
merged props as `props`, at every frame. Times in templates are composition seconds or anchors, as in any layer; the
element's own edges (`end:$id-0.25`) time an exit.

### Expansion

A timeline uses a component where it would write layers:

```json
{ "id": "pop/faster", "use": "pop-text@1.0.0", "from": "word:b/faster", "to": "word:b/faster+1.20", "z": 2,
  "props": { "text": "FASTER" } }
```

```json
{ "id": "open/title", "at": "start:a", "picture": { "use": "title-card@1.0.0", "props": { "title": "POUR-OVER" } } }
```

- `use` is `<name>@<version>`, an exact version. The overlay, or the composition (a shot's or an insert's
  `picture`), takes `props` and no `layers`.
- **Props** are the schema's defaults overlaid with the given props (one level deep), then kit tokens are resolved,
  then they are checked against the schema. A failure is a `component_props` error at the prop's pointer, with the
  property's description in the hint.
- **Layers**: the templates are substituted, ids prefixed with `<element id>/`, and the result must be valid layers.
  The composition's `props` become the merged props, so runtime expressions see them.
- **Fitting the canvas.** When the timeline's `format.size` differs from the component's `canvas`, every pixel value
  is scaled by `k = min(w/W, h/H)` and positions are mapped by `x' = x·k + (w − W·k)/2` (and the same for y), so the
  design is centered, whole, like `contain`. Scaled: positions (constants, keys, and the result of a position
  `expr`), text `size`, `max_width` and stroke width, shape `size`, `radius` and stroke width, image `size` (or the
  scale of an image without one), shadows and `blur`. Video layers only move: their `fit` already follows the
  timeline's canvas. Props stay in design pixels.
- **Words.** A composition prop whose value is `"$words:<spine-id>"` becomes that spine item's words on the
  composition's clock, `[{"w", "s", "e"}]` (seconds, negative before the composition starts), when the timeline is
  resolved. A caption component reads its words this way, so captions ripple with the VO.
- **Errors** are typed and point at the use: `component_not_found` (with close names), `component_unlocked`,
  `component_changed`, `component_props`, `component_target` (used where `use_as` doesn't allow),
  `component_input_unknown`, `component_template` (a template that doesn't make valid layers), `kit_token_unknown`.

`retake-edit expand <timeline>` prints the expanded timeline. The renderer only ever sees the expansion, and the
expansion is what the layer and unit keys hash (`spec/timeline.md`, "Units and caching"): a component change
re-renders exactly the elements whose expansion changed, and a pinned version that didn't change re-renders nothing.
A render manifest's elements say which component drew them (`use`, `spec/manifest.md`). Layer ids from a component
(`pop/faster/text`) appear in the expansion only; patch the element's `props` instead.

### Finding a component

`name@version` is looked for in these folders, in order; in each, `<name>@<version>/` first, then `<name>/` if its
`component.json` has that version:

1. `components/` next to the timeline: the project's vendored packages (`retake lib add` puts them there);
2. each folder in `$RETAKE_COMPONENT_PATH` (`:`-separated);
3. `<library>/components/`, the user's library (`$RETAKE_LIBRARY`, else `$XDG_DATA_HOME/retake/library`, else
   `~/.local/share/retake/library`).

Kits are found the same way (a folder with a `kit.json`).

### Backends

A component can draw with a program instead of layers: any language, any drawing code a creator already has (a
Pillow script, a Cairo or canvas renderer, a game engine's exporter). The program ships in the package, so the
package hash pins it, and it runs only through a locked use.

```json
{ "schema": "retake-component/0.1", "name": "python-text", "version": "1.0.0", "license": "CC0-1.0",
  "use_as": ["overlay"], "props": { … },
  "backend": { "command": ["python3", "draw.py"], "timeout": 30 } }
```

| `backend` field | Meaning |
|---|---|
| `command` | The argv to run. A first element with a `/` (`./draw`, `bin/draw`) is a path in the package; a bare name (`python3`) is looked up on `PATH`. Other arguments are passed as they are; the program runs in the package folder. |
| `timeout` | Seconds one request may take, default 60, at most 3600. |

**Expansion.** A use of a backend component expands into one layer that draws the whole composition, with the
merged props on the composition as usual (`$words:` props too):

```json
{ "id": "pop/faster/raster", "type": "raster", "component": "python-text@1.0.0", "canvas": [1920, 1080],
  "backend": { "command": ["python3", "draw.py"], "dir": "/abs/components/python-text@1.0.0",
               "hash": "b3:5c1e…", "timeout": 30 } }
```

Its content box is the timeline's frame and its transform the identity: the program places its own pixels. The use's
`from`/`to` time it like any overlay. A `raster` layer only comes from a backend: a timeline
(or a component template) that writes one itself is refused (`raster_source`), so opening someone's timeline never runs
a program it names. The layer key hashes the layer (the package hash, so the program's bytes, and the command), the
composition's props, the output size and the frame range, so editing the program re-renders exactly its uses; `dir` is
left out, so moving the project doesn't.

**The protocol, `retake-raster/1`.** The renderer runs the command once per graphics run (a raster layer is always a run
of its own), in the package folder, with a cleared environment: only `PATH`, `LANG=C.UTF-8`,
`PYTHONDONTWRITEBYTECODE=1` (a `__pycache__` would change the package hash) and `TMPDIR`, a fresh scratch folder
removed afterwards. It writes one JSON line on stdin and closes it:

```json
{ "protocol": "retake-raster/1", "component": "python-text@1.0.0", "props": { "text": "FASTER" },
  "size": [1920, 1080], "scale": 1.0, "comp": [1920, 1080], "canvas": [1920, 1080], "fps": 30, "frames": [0, 36] }
```

- `size` is the output frame in pixels (a draft is half the timeline's `comp`, `scale` 0.5); `canvas` is the
  component's design canvas, for programs that lay out in design pixels and scale by `min(size / canvas)`.
- `frames` is `[a, b)`, composition frames; frame `k` is at composition time `t = k / fps` (negative `k` is before the
  composition starts). Everything a frame may depend on is in the request: the same request must give the same frames.
- For each frame, in order, the program writes to stdout a JSON header line, then the pixels:
  - `{"box": [x, y, w, h]}` and exactly `w × h × 4` bytes of RGBA with straight (not premultiplied) alpha, rows top to
    bottom, placed at (x, y) in output pixels (it may extend past the frame; that part is clipped);
  - `{"same": true}`: the previous frame again (nothing follows; not on a request's first frame);
  - `{"empty": true}`: nothing drawn.
- It exits 0 after the last frame. Errors are typed, with the program's stderr (its last lines) in the message:
  `backend_failed` (it couldn't start, or exited non-zero), `backend_timeout` (the request took longer than `timeout`;
  the program is killed), `backend_output` (a malformed header, too few bytes, too few frames, or bytes after the
  last frame).

A Python backend reads the request with `json.loads(sys.stdin.readline())` and writes `sys.stdout.buffer`: see
`editor/examples/backends/python-text/`, which draws with the standard library only.

**Trust.** A backend runs code with the user's rights (the environment is cleared, but nothing is sandboxed). It runs
only for a use pinned in the lockfile, so adding a backend component to a project (`retake lib add`, `component lock`)
is the consent; a changed program is `component_changed` until it's re-pinned. The interpreter (`python3`) isn't part
of the key: pin it outside, or ship a self-contained program.

## The lockfile

`<timeline stem>.lock` (`timeline.lock` for `timeline.json`), next to the timeline, pins every package the timeline
uses to its content hash:

```json
{
  "schema": "retake-lock/0.1",
  "packages": {
    "pop-text@1.0.0": { "type": "component", "hash": "b3:5c1e…", "path": "components/pop-text@1.0.0",
                        "source": "registry:../library/index.json", "license": "CC0-1.0" },
    "clean@1.0.0":    { "type": "kit", "hash": "b3:90ad…", "path": "components/clean@1.0.0",
                        "source": "registry:../library/index.json", "license": "CC0-1.0" }
  }
}
```

- **The package hash** is `b3:` and the BLAKE3 of, for every file in the package folder sorted by its relative path
  (`/`-separated, byte order), skipping `previews/` and hidden files and folders: the path, a NUL byte, the file's
  BLAKE3 in hex, a newline. `retake-edit component hash <dir>` prints it; Go computes the same.
- **Every use and every kit must be locked.** Validating, resolving and rendering refuse a package that isn't in the
  lock (`component_unlocked`) or whose files changed since (`component_changed`, with both hashes). A released
  version is immutable: copy it to a new version, then upgrade.
- `retake-edit component lock <timeline>` pins every package the timeline uses that isn't pinned yet;
  `--update <name@version>` (repeatable) or `--update-all` re-pins changed ones.
- `patch apply` pins the packages a patch starts using. `retake lib add` writes the entry with its `source`.
- **Upgrading is explicit, per element.** `retake-edit component upgrade <timeline> <element>|all --to <version>
  [--component <name>] [--answers <note>]…` writes one patch that sets `use` on the element (or on every element
  using `<name>`), pins the new version, and reports the units that will re-render. Nothing else changes.

## Brand kits

A kit is a folder with a `kit.json` (the catalog's kit format, `spec/assets.md`, plus `components` and `rules`):

```json
{
  "schema": "retake-kit/0.1",
  "name": "clean",
  "version": "1.0.0",
  "title": "Clean",
  "license": { "status": "cleared", "spdx": "CC0-1.0" },
  "tokens": {
    "color": { "accent": "#ffd23f", "text": "#f5f5f2", "bg": "#111317" },
    "font": { "display": "display" },
    "size": { "title": 120, "caption": 56 },
    "safe_area": { "action": [0.05, 0.05, 0.9, 0.9], "title": [0.1, 0.1, 0.8, 0.8] },
    "caption": { "words_per_line": 4, "highlight": "#ffd23f" },
    "pacing": { "pop_min_gap": 0.8, "zooms_per_10s": 2 }
  },
  "components": ["pop-text@1.0.0", "lower-third@1.0.0"],
  "rules": [ { "id": "one-pop-at-a-time", "rule": "no-overlap", "kind": "pop", "message": "one pop at a time" } ],
  "assets": []
}
```

- `license` is the catalog's license object (it applies to the kit's `assets`), with the package's SPDX id in
  `spdx` (a plain SPDX string is read too). `version` is semver for a kit in a registry.
- A timeline uses a kit by listing it: `"kits": ["clean@1.0.0"]`. A `brand:<name>@<version>` entry is a kit
  installed in the catalog (`spec/assets.md`); the editor reads its tokens and rules when it finds the kit's folder
  as above, and otherwise only records it. Any other entry isn't a kit package: a plug-in or another scheme
  (`plugin:deadlock`), or a name without a version. It is recorded (it stays in the document, and `retake-edit
  expand --json` lists it under `kits_recorded`) and ignored by expansion, as timeline/0.1 always did. Only a
  `<name>@<version>` entry that isn't installed is an error (`kit_not_found`).
- **Tokens.** Any string that is exactly `kit:<group>.<name>[.<path>]`, anywhere in the timeline (but `meta` and
  `expr` strings), in props, in component templates and defaults, and in rules, is replaced by the token's value of
  any JSON type: `"fill": "kit:color.accent"`, `"size": "kit:size.title"`. Lookup goes through the timeline's kits
  in order, then (inside a component) the component's own `tokens`. A group may also be spelled in the plural
  (`colors`), as older kits do. An unknown token is a `kit_token_unknown` error with close matches.
- Token groups by convention: `color`, `font` (font names of the timeline), `size` (pixels of the design canvas),
  `safe_area` (`[x, y, w, h]` as fractions of the frame), `caption`, `pacing` (seconds and counts), `motion`.
- `components` lists the components that come with the kit: `retake lib add <kit>` adds them too.

## House rules and `retake-edit lint`

`retake-edit lint <timeline> [--kit <name@version>] [--rules <file>] [--json]` checks a timeline against its kits'
`rules` (or the given ones) and prints typed, actionable violations:

```json
{ "clean": false, "errors": 1, "warnings": 0, "rules": 6, "elements": 14,
  "violations": [
    { "rule": "no-overlap", "id": "one-pop-at-a-time", "severity": "error", "code": "lint_no_overlap",
      "message": "pop overlaps pop at 0:12.3–0:12.5 (pop/a, pop/b); one pop at a time",
      "elements": ["pop/a", "pop/b"], "ptrs": ["/scenes/1/overlays/0", "/scenes/1/overlays/2"], "t": [12.3, 12.5],
      "hint": "end pop/a before 0:12.3 or start pop/b after 0:12.5" } ] }
```

It exits 1 when there are errors (warnings alone exit 0).

**Elements** are the shots, overlays and inserts, with their resolved output times `t`, their **kinds** (the element
kind `shot`, `overlay` or `insert`; the component's `category` and `name`; and `meta.kind`, so hand-built layers can
join a rule) and, for overlays, the box their graphics draw (union over a few sample frames, in timeline pixels).

Every rule has `id`, `rule`, `severity` (`error`, the default, or `warning`), an optional `message` (appended to the
violation's text), and `kind` (an element kind, or `*`). Numbers may be kit tokens.

| `rule` | Fields | A violation when |
|---|---|---|
| `no-overlap` | `kind`, `with?` (default: `kind`), `tolerance?` (s) | Two elements of the kinds overlap in time by more than `tolerance`. |
| `min-gap` | `kind`, `seconds` | One element of the kind starts less than `seconds` after the previous one ends. |
| `max-count-per-window` | `kind`, `max`, `window` (s) | More than `max` elements of the kind start within `window` seconds. |
| `safe-area` | `kind`, `area` (`[x, y, w, h]` fractions of the frame, or a token) | An element's drawn box leaves the area. |
| `min-duration`, `max-duration` | `kind`, `seconds` | An element of the kind is shorter (longer) than `seconds`. |
| `custom` | `expr` | The expression returns violations. |

**The escape hatch.** A `custom` rule's `expr` is a JavaScript expression run in the expression sandbox with
`elements` (`[{id, ptr, element, kinds, use, t: [from, to], box, props}]`), `timeline` (`{id, duration, fps, size}`),
`kit` (the tokens) and `rule`. It returns a list of `{message, elements?, t?, hint?}`:

```json
{ "id": "readable-pops", "rule": "custom", "severity": "warning",
  "expr": "elements.filter(e => e.kinds.includes('pop') && e.t[1] - e.t[0] < 0.6).map(e => ({elements: [e.id], t: e.t, message: `${e.id} shows for ${(e.t[1] - e.t[0]).toFixed(2)} s; pops need 0.6 s to read`}))" }
```

## Previews

`retake-edit component preview <dir> [-o <dir>] [--no-webp] [--json]` renders each example at 5 moments (4, 12, 35,
70 and 95% of its duration: the entrance, settled, and the exit) into a storybook sheet, `previews/sheet.png` (one
row per example, labeled, each tile with its time), plus a 2-second, 10 fps animated WebP of the first example,
`previews/preview.webp` (made by ffmpeg's libwebp). Overlays are drawn over a soft backdrop. Tiles are 320 pixels
wide. Same component, same bytes. An agent looks at the sheet before it uses a component.

Imported into the catalog (`retake assets import <component dir>`, `spec/assets.md`), a component is an entry of kind
`component` whose previews are the `storybook` sheet and the `animation`.

## Registries

A registry is a static `index.json`, served from any static host or a git checkout, with the packages next to it:

```json
{
  "schema": "retake-registry/0.1",
  "name": "retake-starter",
  "packages": [
    { "name": "pop-text", "type": "component", "version": "1.0.0", "title": "Pop text", "description": "…",
      "category": "pop", "tags": ["text"], "license": "CC0-1.0", "hash": "b3:5c1e…",
      "path": "components/pop-text",
      "files": [ { "path": "component.json", "hash": "b3:…", "size": 4210 } ],
      "previews": { "sheet": "components/pop-text/previews/sheet.png" },
      "requires": [] }
  ]
}
```

`type` is `component`, `kit` or `recipe`. `path` and preview paths are relative to the index. A package's files are
`<index folder>/<path>/<file path>`; each is checked against its hash, and the whole against the package hash.
`requires` lists other packages (`name@version`) to add with it (a kit's components).

| Command | Does |
|---|---|
| `retake lib search <q> [--registry URL\|PATH] [--type T] [--json]` | Searches the registry and the user library (`<library>/components/`) by name, title, description, category and tags. Every word must match. Newest version per package, with `installed` when the project has it. |
| `retake lib add <name>[@version] [--registry URL\|PATH] [--timeline T] [--force] [--json]` | Copies the package (and what it `requires`) into `components/<name>@<version>/` next to the project's timeline, checks every hash, and records it in the lockfile with its source. The project owns the copy. Refuses a package without an allowed license. |
| `retake lib list [--timeline T] [--json]` | The components the project can use (vendored, `$RETAKE_COMPONENT_PATH`, the user library), and which the timeline uses. |
| `retake lib describe <name>[@version] [--json]` | Props schema, defaults, examples, preview paths, changelog, and a ready-to-patch use. |
| `retake lib pack <dir> [--index FILE] [--json]` | Validates a package and prints its registry entry; with `--index`, writes it into that index (paths relative to it). |
| `retake lib recipe export <timeline> -o <dir> [--json]` | Below. |
| `retake lib lint [<timeline>] [--json]` | `retake-edit lint` on the project's timeline. |

The registry is `--registry`, else `$RETAKE_REGISTRY`. A URL is `http(s)://…/index.json`; anything else is a path.
The timeline is `--timeline`, else the project's (`spec/bridge.md`), else `timeline.json` in the current folder.
`scripts/build-registry.sh` builds `library/index.json` from `library/` with `retake lib pack --index`.

**The license gate.** `pack` refuses a package without an allowed `license`, a font without a license file beside
it, and, for asset ids that props or examples name, an asset the project's catalog has as `review-only` or
`blocked` (`spec/assets.md`, "Licenses").

## Edit recipes

`retake lib recipe export <timeline> -o <dir>` packages an edit's *recipe*, without its media: the timeline, its patch
log, its lockfile and the components and kits it uses, plus `recipe.json`:

```json
{ "schema": "retake-recipe/0.1", "name": "how-to", "timeline": "timeline.json", "rev": 3,
  "placeholders": { "proj:footage": { "kind": "video", "duration": 8.0, "size": [1280, 720], "tags": ["footage"],
                                      "path": "media/footage.mp4" } },
  "packages": ["title-card@1.0.0", "clean@1.0.0"], "warnings": [] }
```

- Every asset becomes a placeholder: its path is `media/<name><ext>`, and `recipe.json` gives its kind, duration,
  size and tags (from the catalog, else a probe). A `words` transcript becomes `media/<name>.words.json`, to fill
  in. Media files and transcripts are never copied; paths in the patch log are rewritten the same way.
- **The license gate:** the export refuses a package without an allowed license and an asset the catalog has as
  `blocked`; a `review-only` asset is a warning. Nothing it writes holds blocked content.

## Agent tools

MCP tools (served by `retake mcp <video>` with the review and asset tools), each also a `retake lib` command with
`--json`:

| Tool | Does |
|---|---|
| `component_list(q?)` | Components the project can use, with title, category, `use_as`, version, whether the timeline uses and locks it, and the sheet's path. |
| `component_describe(name)` | Props schema, defaults, examples, preview paths (sheet, WebP), changelog, and a use to patch in. |
| `lib_search(q, registry?, type?)` | Search a registry and the user library. |
| `lib_add(name, registry?)` | Add a package to the project (local agents only). |
| `lint(timeline?)` | House-rule violations of the project's timeline. |
