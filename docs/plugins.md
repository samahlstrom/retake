# Assets and plug-ins

The asset catalog is what an agent edits *with*: recordings, VO, music, sound effects, images, fonts and brand kits.
Every asset has a license, previews the agent can look at, and spans (labeled moments inside it). The CLI is
`retake assets …` (every command takes `--json`), and the same operations are MCP tools. The contract is
[spec/assets.md](../spec/assets.md); this page is the practical guide.

The core knows nothing about any one kind of channel. Domain knowledge (a game's matches, a recipe's steps) comes
from plug-ins: small programs in any language.

## Where things live

| Scope | Ids | Where |
|---|---|---|
| Project | `proj:vo/intro` | `<project>/.retake/assets.db` and `<project>/.retake/blobs/` |
| Brand kits | `brand:acme/logo` | the library |
| Library | `lib:sfx/whoosh` | `$RETAKE_LIBRARY`, default `~/.local/share/retake/library/` |

- The project is `--project DIR`, else `$RETAKE_PROJECT`, else the nearest folder with a `.retake/` folder, else the
  current one. It's the same root as a review project and a timeline's folder.
- An id without a prefix (`logo`) is looked up in the project, then the brand kits, then the library.
- Files are identified by BLAKE3 (`b3:…`), the hash the editor uses. Imported files are copied into the store;
  files of 512 MiB or more (or `--in-place`) stay where they are and are never copied or changed.
- Reads never create a catalog. The first import does.

## Importing

```sh
retake assets import media/                      # a folder: ids keep its layout (proj:media/intro)
retake assets import vo.wav --license owned --transcribe
retake assets import ~/Music/bed.mp3 --scope lib --license attribution \
    --credit 'Music: "Example Tune" by A. Composer, CC BY 4.0'
retake assets import ~/Videos/kitchen-cam-a.mp4 --in-place --title "Kitchen cam A"
```

Each file is hashed, checked against the catalog (an exact duplicate isn't imported again; a near-duplicate image is
reported as "looks like `lib:x` (distance 4)"), probed, and given previews. `--transcribe` uses the transcription
set up in the config ([config.md](config.md)); `--track N` picks the audio track. Videos up to 10 minutes get a 540p
proxy for fast frame grabs (`--proxy`, `--no-proxy`).

**An import without `--license` is `review-only`.** Nothing is publishable until a person says so:

```sh
retake assets set proj:media/intro --license owned
retake assets set lib:bed --license attribution --credit "…"
retake assets set proj:meme/x --license review-only --content-id-risk high
```

| License | Means | Published? |
|---|---|---|
| `owned` | your own material | yes |
| `cleared` | licensed, no credit needed (CC0, a paid library) | yes |
| `attribution` | licensed, with a credit line | yes, with the credit |
| `review-only` | fine in drafts | no |
| `blocked` | never use | no |

Agents can import (always `review-only`) but can't change a license.

## Looking at assets

```sh
retake assets get proj:media/intro                     # the entry, as JSON
retake assets preview proj:media/intro                 # the contact sheet: a tile every few seconds, timecodes on
retake assets preview proj:media/intro --at 12.5 --at 1:03
retake assets preview proj:media/intro --span sp:12    # a span's 6-frame strip
```

Video gets a contact sheet, audio a waveform with its loudness (LUFS, range, peak), images a thumbnail, fonts a
specimen sheet drawn with the font itself. Previews are files in the blob store; their paths are in every result, and
the MCP `asset_preview` tool returns them as images too.

## Searching and spans

```sh
retake assets search "butter"                          # titles, tags, captions, transcripts, span labels
retake assets search "butter" --kind video --tags kitchen --license owned
retake assets search --type step --where "json_extract(data, '$.recipe') = 'croissants'"
retake assets span add proj:media/intro 12.0 18.5 --label "folds the dough"
retake assets span find --asset proj:media/intro
retake assets span find --near slate:take/7@1:30 --window 20
```

Every word must match, as a prefix. Results are asset ids with their preview paths, matching spans (`sp:<n>` with
`t0`/`t1` in seconds into the asset) and, for transcript matches, where the words were said. Every time in a result
is stored data, never an estimate. A span is a timeline source: cut `[t0, t1]` of its asset.

`--where` is one SQL condition on spans (`id`, `asset`, `t0`, `t1`, `type`, `label`, `data`, `source_of_label`,
`confidence`, `plugin`). It runs read-only. `retake assets sql "SELECT …"` runs any read-only query over the
catalog, plug-in views included.

## Licenses before publishing

```sh
retake assets license-report timeline.json             # or a render's manifest: v8.manifest.json
```

It lists every asset the video uses with the output time ranges where it's seen or heard, the credits for the
description, and the **publish blockers**: `review-only` or `blocked` assets, `attribution` without a credit line, and
assets the catalog doesn't know. Content-ID risk is a warning. For a timeline, output times come from
`retake-edit resolve` when it's installed (or `--resolved file`); a manifest has them already.

## Brand kits

A kit is a folder with a `kit.json` listing its files and tokens (colors, fonts, sizes, safe areas):

```json
{ "schema": "retake-kit/0.1", "name": "acme", "version": "3", "license": { "status": "owned" },
  "tokens": { "colors": { "accent": "#ffcc00" } },
  "assets": [ { "key": "logo", "path": "logo.png" }, { "key": "fonts/display", "path": "Display.ttf",
                "license": { "status": "cleared", "credit": "SIL OFL" } } ] }
```

`retake assets kit install acme-kit/` copies it into the library as `brand:acme@3/…`. `brand:acme/logo` is the newest
installed version; `brand:acme@3/logo` pins one.

## For the editor and agents

- `retake assets resolve <id>...` prints one `{"id", "path", "hash", "kind"}` per line (with `"stale": true` when an
  in-place file changed). The editor uses it for asset ids that have no `path` in the timeline.
- `retake mcp <video>` serves the asset tools next to the review tools: `asset_search`, `asset_get`,
  `asset_preview`, `span_find`, `span_add`, `asset_import`, `plugin_query`, `license_report`, and one
  `<plugin>_<tool>` per plug-in tool. `retake assets mcp` serves them alone.

## Plug-ins

A plug-in is a folder with a `plugin.json` and a program. It can add assets, span types (with a schema for their
data), clocks (a mapping from an asset's time to a domain's time: a game clock, a slate), read-only SQL views, and
tools. Retake finds plug-ins in `<project>/.retake/plugins/`, in the folders of `$RETAKE_PLUGIN_PATH`, and in
`<library>/plugins/` (`retake assets plugin install <dir>` links one there).

```sh
retake assets plugins                                     # what's installed, and what each adds
retake assets import index.csv --via csv-index            # run a plug-in's indexer
retake assets plugin csv-index columns --args '{"path": "index.csv"}'   # run one of its tools
```

### The example: csv-index

[plugins/csv-index](../plugins/csv-index/README.md) imports any CSV with path, title, tags and license columns (a
hand-kept asset index), one asset per row, with the license text mapped to a status ("CC0" is `cleared`, "CC BY" is
`attribution`, "unknown, copyrighted" is `review-only` with a high content-ID risk). Build it with
`mise exec -- go build -o csv-index .` in its folder.

### Writing one

`plugin.json`:

```json
{
  "schema": "retake-plugin/0.1",
  "name": "recipe-steps",
  "version": "0.1.0",
  "exec": ["python3", "plugin.py"],
  "span_types": [ { "type": "step", "description": "one recipe step",
                    "data_schema": { "type": "object", "required": ["recipe", "step"] } } ],
  "indexers": [ { "name": "steps", "description": "steps from the VO transcripts" } ],
  "views": [ { "name": "recipe_steps_all",
               "sql": "SELECT id AS span, asset, t0, t1, json_extract(data, '$.step') AS step FROM spans WHERE plugin = 'recipe-steps'" } ],
  "tools": [ { "name": "list", "description": "the steps of a recipe",
               "input_schema": { "type": "object", "properties": { "recipe": { "type": "string" } } } } ]
}
```

The protocol is one process per call. Retake writes one JSON request to the program's stdin and closes it:

```json
{ "protocol": "retake-plugin/0.1", "method": "index", "indexer": "steps", "args": {}, "path": "",
  "context": { "project_root": "/…", "scope": "project", "catalog": "/…/.retake/assets.db", "plugin_dir": "/…" } }
```

The program answers with JSON lines on stdout and exits 0. An indexer sends `asset`, `span` and `clock` lines and
ends with `done`:

```json
{"type": "asset", "key": "take7", "kind": "video", "path": "media/take7.mp4", "import": "metadata", "title": "Take 7"}
{"type": "span", "key": "take7:step4", "asset": "take7", "t0": 89.0, "t1": 101.5, "span_type": "step", "label": "Fold the butter in", "data": {"recipe": "croissants", "step": 4}}
{"type": "done"}
```

A tool sends one `{"type": "result", "result": …}` (or `{"type": "error", "code": "bad_request", "message": …}`).
`log` lines and stderr are passed on to the user. The rules worth knowing:

- Keys are yours and stable: indexing again updates the same assets and spans, so their ids don't change.
- `"import": "metadata"` records a path without reading the file (it's hashed when first resolved);
  `"import": "full"` imports it like `retake assets import`.
- A span's type is `span_type`, and must be one the plug-in declares. Bad records are skipped and reported; the rest
  are kept.
- In a tool's result, any object with a `span_key` you indexed gets the catalog's `span` and `asset` ids added.
- The catalog is yours to read (`context.catalog`, read-only), never to write. A plug-in that reads someone's
  database should open it read-only too (SQLite: `file:…?mode=ro&immutable=1`).

The full protocol, with every field, is in [spec/assets.md](../spec/assets.md#plug-ins).
