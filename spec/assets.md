# Asset catalog, `assets/0.1`

The catalog is what an agent edits *with*: footage, VO, music, SFX, images, fonts and brand kits, each with a
license, previews the agent can look at, and spans (labeled moments inside an asset). It is searchable from the
CLI (`retake assets …`, every command with `--json`) and over MCP. The core knows nothing about any one channel:
domain knowledge (a game's matches, a recipe's steps) comes from plug-ins.

`internal/assets` implements it. The editor reads assets through the resolver (`retake assets resolve`), never the
database.

## Storage and scopes

| Scope | Id prefix | Where | Holds |
|---|---|---|---|
| project | `proj:` | `<root>/.retake/assets.db`, blobs in `<root>/.retake/blobs/` | VO, recordings, memes, generated art for one project |
| brand | `brand:` | the library (below) | installed brand kits: logos, fonts, music, tokens |
| global | `lib:` | `$RETAKE_LIBRARY`, default `$XDG_DATA_HOME/retake/library/` (`~/.local/share/retake/library/`) | CC0 SFX, fonts, anything reused across projects |

- The **project root** is `--project DIR`, else `$RETAKE_PROJECT`, else the nearest folder at or above the current one
  that has a `.retake/` folder, else the current folder. `retake mcp <video>` uses the video's project root
  (`spec/notes.md`).
- Both databases are SQLite (WAL) with the same schema, created on first write. Reads never create them.
- **Ids carry their scope**: `proj:stills/plated-dish`, `lib:sfx/whoosh-1`, `brand:acme/logo`. After the prefix an
  id matches `^[A-Za-z0-9][A-Za-z0-9_.@/-]*$`, so it is also a valid timeline asset id (`spec/timeline.md`). An
  import names an asset after its path: the file name without its extension for one file, the path relative to the
  folder for a folder import, lowercased, with runs of other characters turned into `-`; `-2`, `-3`, … make it
  unique.
- **Lookup order.** A scoped id is looked up in its scope only. An id without a prefix (`logo`) is tried as
  `proj:logo`, then in each installed brand kit (`brand:<kit>/logo`; two kits holding it is an error that names
  both), then `lib:logo`.
- **Span ids** are `sp:<n>` for project spans and `sp:lib/<n>` for library (brand and global) spans.

### Blobs and hashing

- A file's identity is **BLAKE3** of its bytes, written `b3:<64 hex digits>`, the same function and format the
  editor uses for its inputs. Files are hashed by streaming, never read into memory. Hashes are cached by (path, size,
  modification time), so an unchanged file is hashed once.
- The blob store is content-addressed: `blobs/b3/<first 2 hex>/<hex><.ext>`. The extension is kept so ffmpeg and
  viewers can tell the format. A blob reference is `b3:<hex><.ext>` (`b3:0d2f….jpg`).
- **Imported files are copied into the store**, except big ones: files of 512 MiB or more, or any file imported with
  `--in-place`, are **referenced in place** (`blob.in_store: false`, `blob.locator: file:///…`) and never copied or
  modified.
- **Generated files** (previews, proxies, frames) are blobs too. Each records its parent asset and a recipe
  (`contact_sheet every=60`, `frame t=12.5 w=960`), so it can be rebuilt instead of kept.
- Plug-ins may add **metadata-only** assets: a path, no hash yet. They are hashed on first `resolve` (or
  `retake assets rehash`), so indexing a 2-hour recording reads none of it.

## A catalog entry

`retake assets get <id> --json` and `asset_get`:

```json
{
  "id": "proj:rec/kitchen-cam-a",
  "kind": "video",
  "scope": "project",
  "blob": { "hash": "b3:5e0c…", "size": 41203339264, "locator": "file:///…/kitchen-cam-a.mp4",
            "in_store": false, "path": "/…/kitchen-cam-a.mp4", "exists": true },
  "media": { "duration": 7311.4, "size": [1920, 1080], "fps": 60, "vfr": false, "codec": "h264",
             "audio": [ { "stream": 1, "codec": "aac", "channels": 2, "sample_rate": 48000, "role": "mic" } ] },
  "proxy": { "hash": "b3:91aa….mp4", "size": [960, 540], "gop": 15 },
  "title": "Kitchen camera A, Oct 23 evening",
  "tags": ["kitchen", "cam-a"],
  "caption": "",
  "transcript": { "model": "whisper.cpp large-v3 (cuda)", "track": 1, "words": 18211 },
  "previews": { "contact_sheet": "b3:0d2f….jpg", "every_s": 60, "waveform": "b3:a913….png",
                "paths": { "contact_sheet": "/…/.retake/blobs/b3/0d/0d2f….jpg" } },
  "spans": 412,
  "clocks": [ { "domain": "slate:take/7", "plugin": "slate",
                "epochs": [ { "asset_t": [1820.0, 5920.0], "domain_t0": -4.3, "rate": 1.0 } ],
                "confidence": "high", "method": "clapper audio x2" } ],
  "license": { "status": "owned", "publish": true },
  "provenance": { "importer": "cli", "source": "/…/kitchen-cam-a.mp4", "seen": "2026-10-24T03:12:00.000Z" },
  "near": [],
  "meta": {}
}
```

| Field | Meaning |
|---|---|
| `kind` | `video`, `audio`, `image`, `font`, `component` or `other`, from the extension and the probe (an `.mp4` without a video stream is `audio`); a folder with a `component.json` is a `component`. |
| `blob` | `hash` (null until hashed), `size` in bytes, `locator` (`file://` for in-place files, `blob:<ref>` for stored ones), `path` (where to read it now), `exists`. |
| `media` | From ffprobe: `duration` (s), `size` `[w, h]`, `fps`, `vfr` (average and nominal rates differ), `codec`, and `audio` streams (`stream` index, `codec`, `channels`, `sample_rate`, `role` from the stream title). Images: `size`. Fonts: `family`, `style`, `glyphs`. |
| `proxy` | A 540p H.264 copy with a short GOP, for fast frame grabs. Made at import for videos up to 10 minutes that are bigger than 960×540 (`--proxy` / `--no-proxy` override). |
| `title`, `tags`, `caption` | Searchable text. `caption` describes the picture (from a plug-in, a captioner later, or by hand). |
| `transcript` | Present when the asset was transcribed (`--transcribe`): the model, the audio track and the word count. The words are searchable. |
| `previews` | Blob refs by kind (below), and their file `paths`. |
| `spans`, `clocks` | The number of spans on the asset, and its clock mappings (below). |
| `license` | Below. |
| `provenance` | `importer` (`cli`, `mcp`, `plugin:<name>`, `kit:<name>@<version>`), `source` (the path it came from), `seen` (import time), and for derived files `parent` and `recipe`. |
| `near` | Near-duplicate images found at import: `[{id, distance}]`. |
| `meta` | Free data from a plug-in or a kit. The core never reads it. |

## Previews

Every asset gets previews at import, stored as blobs. A multimodal agent asks for them with `asset_preview`.

| Kind | Previews |
|---|---|
| Video | `contact_sheet`: one tile every `every_s` seconds (a step from 0.25 s to 20 min giving about 2√duration tiles, 12 to 120: a 12 s clip gets one a second, two hours one every 2 minutes; `--every` overrides), made with ffmpeg's `tile` filter from frames grabbed by seeking, each tile with its timecode burned in. `strip` per span: 6 frames evenly over the span, with timecodes. |
| Audio | `waveform`: peak and RMS per column, time ticks, and the loudness (integrated LUFS, loudness range, peak) from ffmpeg's `ebur128`. |
| Image | `thumbnail`: at most 512 px on the long side. |
| Font | `specimen`: the family and style, the alphabet, digits and a sample line at five sizes, drawn with the font itself. |
| Component | `storybook`: each example at five moments (PNG), and `animation`: two seconds of the first example (animated WebP), from `retake-edit component preview` (`spec/components.md`). Without `retake-edit`, the package's committed `previews/sheet.png`. |

- Sheets, strips and frames are JPEG; waveforms and specimens are PNG; a thumbnail keeps PNG when the image has
  transparency.
- A span's strip is made when the span is added by hand, and on first request for plug-in spans (a plug-in can add
  thousands).
- `asset_preview(id, at=[…])` renders frames on demand, from the proxy when there is one. Frames are cached as blobs.
- A preview that fails (no ffmpeg, a broken file) is a warning on the import, not an error.

## Licenses

| Status | Meaning | Publish |
|---|---|---|
| `owned` | Your own material | yes |
| `cleared` | Licensed, no attribution needed (CC0, a paid library) | yes |
| `attribution` | Licensed; `credit` holds the credit line | yes, with the credit |
| `review-only` | Fine in drafts, blocked at publish | no |
| `blocked` | Never use | no |

`license` is `{status, publish, credit?, source?, note?, content_id_risk?}`. `content_id_risk` (`low`, `medium`,
`high`) flags material that platform matching may claim even when licensed. **An import without `--license` is
`review-only`** with the note "no license given at import": nothing is publishable until someone says so. Only
people change licenses (`retake assets set`); the agent tools can't.

**The gate.** `license_report` lists publish blockers: an asset that is `review-only` or `blocked`, an `attribution`
asset with no credit line, or an asset the catalog doesn't know (no license on record). `content_id_risk` of `medium`
or `high` is a warning, not a blocker. A publish step refuses while the report has blockers.

## Spans

A span is a labeled moment inside an asset: `(asset, t0, t1, type, label, data, source_of_label, confidence)`.
It is both a search result and a timeline source.

```json
{ "span": "sp:88412", "asset": "proj:rec/kitchen-cam-a", "t0": 1913.0, "t1": 1925.5,
  "type": "step", "label": "Fold the butter into the dough", "data": { "recipe": "croissants", "step": 4 },
  "source_of_label": "transcript", "confidence": "high", "plugin": "recipe-steps",
  "clocks": [ { "domain": "slate:take/7", "t0": 89.0, "t1": 101.5 } ],
  "strip": "/…/.retake/blobs/b3/1c/1c0e….jpg" }
```

- `t0`, `t1`: seconds into the asset, `t0 <= t1`, within its duration when known.
- `type`: a span type. The core has `tag` (a manual label, the default), `shot` and `speech`. Plug-ins declare
  their own, each with a JSON Schema for `data`; `span_add` checks `data` against it. A type declared by two plug-ins
  is refused for the second.
- `source_of_label`: where the label came from: `manual` (a person, by CLI), `agent` (an agent, over MCP), or a
  plug-in's word (`transcript`, `match_data`, …). `confidence` is free text (`high`, `derived`, `keyword`, or a
  number).
- `key`: a plug-in's stable key for the span. Re-indexing updates spans in place by key, so ids survive.

### Clocks

A clock maps an asset's time to a domain's time: a game clock, a match, a take's slate. `epochs` are pieces with
`domain_t = domain_t0 + (asset_t - asset_t[0]) * rate` for `asset_t` in `[asset_t[0], asset_t[1]]` (a paused game
splits into several). A domain is `<plugin>:<thing>` (`slate:take/7`, `game:match/4812`). Span results carry their times
on every clock of the asset, and `span_find --near <domain>@<t>` finds spans near a domain time.

## Import

```
retake assets import <path|dir>... [--scope proj|lib] [--license S] [--credit TEXT] [--license-source URL]
      [--content-id-risk R] [--tags a,b] [--title T] [--name N] [--in-place] [--proxy|--no-proxy]
      [--transcribe] [--track N] [--every S] [--json]
retake assets import [<path>] --via <plugin>[/<indexer>] [--arg k=v]... [--json]
```

For each file: stat, kind, hash (streaming, cached), exact-duplicate check, copy into the store (or reference in
place), probe, previews, near-duplicate check, transcript, search index. A folder is walked recursively, skipping
hidden files and folders and files of unknown kinds.

- **Exact duplicates** (same BLAKE3 as an asset in any visible scope) are not imported again: the item reports
  `status: "duplicate"` and the existing id.
- **Near duplicates** (images): a 64-bit difference hash (dHash) of a 9×8 grayscale reduction. Images within a
  Hamming distance of 10 are reported in `near` ("looks like `lib:memes/thanos-rem` (distance 4)"), never merged.
- **Transcripts** run with `--transcribe` when transcription is configured (`[transcribe]`, `spec/notes.md`), on the
  first audio stream or `--track N`.
- `--via` runs a plug-in's indexer instead (below).

The result lists every item: `{path, id, status, kind, hash, duplicate_of?, near?, warnings?}`, `status` being
`imported`, `duplicate`, `updated` (a plug-in re-index) or `skipped` (with `reason`).

**Components.** A folder holding a `component.json` (`spec/components.md`) imports as one entry of kind `component`,
`<scope>:components/<name>@<version>`: its `blob` is the `component.json` (referenced in place for a project; for
`--scope lib` the package is first copied into `<library>/components/<name>@<version>/`, where `retake lib search`
and the editor look), its `hash` the package hash, its title, tags (plus `component` and the category) and caption
(the description) from the manifest, its license from the SPDX id (`CC0-1.0`, `MIT`, `Apache-2.0`, the BSDs, `ISC`
and `OFL-1.1` are `cleared`, `CC-BY-4.0` is `attribution`, anything else `review-only`), and `meta.component` its
name, version, category, `use_as`, hash and folder. A released version never changes: the same package again is a
duplicate, another one under the same version is skipped.

## Search

```
retake assets search "<query>" [--kind K]... [--scope S]... [--tags a,b] [--license S]... [--type T]...
      [--where SQL] [--limit N] [--json]
```

1. **Structured filters** first: kind, scope (`proj`, `brand`, `lib`), tags (all must be present), license
   status, span type, and `--where`: a SQL condition on spans (columns `id`, `asset`, `t0`, `t1`, `type`, `label`,
   `data` (JSON: use `json_extract`), `source_of_label`, `confidence`, `plugin`), which may use plug-in views. It runs
   on a read-only connection, as one statement.
2. **Full-text search** (SQLite FTS5, `unicode61`, words matched as prefixes, all words required) over asset titles,
   tags, transcripts and captions, and over span labels, types and data. Asset hits rank by BM25 with title > tags >
   caption > transcript; the asset list and the span list are merged by reciprocal-rank fusion. Embedding search is a
   later third list in the same fusion (`assets.VectorIndex`).
3. Results are asset ids, each with its best preview's path, its matching spans (ids, times from the catalog), and
   for transcript matches the words' times. Nothing is estimated: every time in a result is stored data.

With `--where` or `--type`, results are the matching spans grouped by asset. With no query, the filters alone list
assets, newest first.

## Resolver (for the editor)

```
retake assets resolve <id>... [--project DIR]
```

Prints one JSON object per id, one per line: `{"id", "path", "hash", "kind"}`, plus `"stale": true` and
`"cataloged_hash"` when an in-place file changed since it was cataloged. A metadata-only asset is hashed now. An id
that can't be resolved prints `{"id", "code", "error"}` and the command exits 1. Unprefixed ids follow the lookup
order. The editor calls this for `proj:`, `lib:` and `brand:` ids whose timeline entry has no `path`.

## Brand kits

A kit is a folder with a `kit.json`:

```json
{
  "schema": "retake-kit/0.1",
  "name": "acme",
  "version": "3",
  "title": "Acme Kitchen",
  "license": { "status": "owned" },
  "tokens": { "colors": { "accent": "#ffcc00" }, "fonts": { "display": "fonts/Display.ttf" },
              "sizes": { "title": 96 }, "safe_areas": { "shorts": [0, 220, 1080, 1500] } },
  "assets": [ { "key": "logo", "path": "logo.png", "title": "Logo", "tags": ["logo"] },
              { "key": "fonts/display", "path": "fonts/Display.ttf", "license": { "status": "cleared", "credit": "OFL" } } ]
}
```

`retake assets kit install <dir>` copies each listed file into the library's store as `brand:<name>@<version>/<key>`
(with previews) and records the kit and its tokens. `brand:<name>/<key>` means the newest installed version;
`brand:<name>@<version>/<key>` pins one. A timeline lists its kits as `"kits": ["brand:acme@3"]`. Kit assets take
the kit's `license` unless they set their own.

## Plug-ins

A plug-in is an executable, in any language, that speaks JSON over stdin and stdout, plus a `plugin.json` that
declares what it adds. A cooking channel installs none.

### Discovery

Plug-ins are folders holding a `plugin.json`, found in this order (the first of a name wins):

1. `<root>/.retake/plugins/*/`
2. each folder in `$RETAKE_PLUGIN_PATH` (`:`-separated; `;` on Windows): a plug-in folder itself, or a folder of
   them
3. `<library>/plugins/*/` (`retake assets plugin install <dir>` links a folder there)

### `plugin.json`

```json
{
  "schema": "retake-plugin/0.1",
  "name": "game",
  "version": "0.1.0",
  "description": "Matches and labeled moments from a match log",
  "exec": ["python3", "plugin.py"],
  "span_types": [ { "type": "goal", "description": "a goal, from the match log",
                    "data_schema": { "type": "object", "required": ["match_id"] } } ],
  "clock_domains": [ { "domain": "game:match", "description": "game clock seconds of one match" } ],
  "indexers": [ { "name": "matches", "description": "matches and moments", "accepts": [],
                  "args_schema": { "type": "object" } } ],
  "views": [ { "name": "game_moments", "description": "spans with match columns",
               "sql": "SELECT id AS span, asset, t0, t1, type, label, json_extract(data, '$.match_id') AS match_id FROM spans WHERE plugin = 'game'" } ],
  "tools": [ { "name": "find", "description": "Moments by type, player, outcome", "input_schema": { "type": "object" } } ]
}
```

| Field | Meaning |
|---|---|
| `name` | `^[a-z][a-z0-9-]*$`, unique. |
| `exec` | The command, as an argv. A first element with a `/` (`./csv-index`) is relative to the plug-in folder; otherwise it is looked up on `PATH`. The process runs in the plug-in folder. |
| `span_types` | Span types it adds, each with a `data_schema` (JSON Schema). |
| `clock_domains` | Clock domain prefixes it maps asset times to. |
| `indexers` | Ways to bring assets in. `accepts`: file extensions it reads (`[".csv"]`), or empty for one that finds its own data. `args_schema` describes `--arg`s. |
| `views` | Read-only SQL views over the catalog's tables (`assets`, `spans`, `clocks`). The name must start with `<name>_`; the SQL is one `SELECT` or `WITH`. Views are (re)created in a catalog when the plug-in indexes into it, and are usable from `--where` and `retake assets sql`. |
| `tools` | Extra tools, each with an `input_schema`. They run through `plugin_query`, and are also MCP tools named `<plugin>_<tool>` (`game_find`). |

### Protocol, `retake-plugin/0.1`

One process per call. The core writes **one JSON request** to the plug-in's stdin and closes it. The plug-in writes
**JSON lines** to stdout, each an object with a `type`, and exits 0. stderr is a log the core passes on. A non-zero
exit, a line that isn't JSON, or a missing final line fails the call. Calls time out (indexers after 30 minutes,
tools after 2).

Request:

```json
{ "protocol": "retake-plugin/0.1", "method": "index", "indexer": "matches", "tool": "",
  "args": { "limit": 5 }, "path": "/abs/path/given/to/import/or/empty",
  "context": { "project_root": "/…", "scope": "project", "catalog": "/…/.retake/assets.db", "plugin_dir": "/…" } }
```

`method` is `index` or `tool`. `context.catalog` may be opened read-only; plug-ins never write it.

**index** answers with any number of these lines, then `done`:

```json
{"type": "asset", "key": "rec_0412", "name": "game/rec_0412", "kind": "video",
 "path": "/…/rec.mp4", "import": "metadata", "title": "…", "tags": ["obs"], "caption": "",
 "license": {"status": "owned"}, "media": {"duration": 7311.4, "size": [1920, 1080], "fps": 60}, "meta": {}}
{"type": "span", "key": "4812:goal:547.0", "asset": "rec_0412", "t0": 619.5, "t1": 631.5,
 "span_type": "goal", "label": "Blue scores from the corner", "data": {"match_id": 4812},
 "source_of_label": "match_data", "confidence": "high"}
{"type": "clock", "asset": "rec_0412", "domain": "game:match/4812",
 "epochs": [{"asset_t": [0, 2435], "domain_t0": -14.5, "rate": 1}], "confidence": "high", "method": "scoreboard ocr x3"}
{"type": "log", "message": "3 matches, 41 moments"}
{"type": "done", "stats": {"assets": 1, "spans": 41}}
```

- `asset`: `key` is the plug-in's stable key; `name` the id to use (default `<plugin>/<key>`, in the scope of the
  import). `import: "full"` imports the file like `retake assets import` (hash, copy or in place, previews) and then
  applies the record's fields; `"metadata"` (the default) records the path without reading it. `path` is absolute, or
  relative to the request's `path` (its folder when it is a file; the plug-in's folder when there is none).
  Re-sending a key updates that asset.
- `span`: `asset` is an asset key from this plug-in, or a catalog id (`proj:…`). The span's type is `span_type`
  (`type` is the line's). Types the plug-in doesn't declare are refused.
- `clock`: replaces the asset's clock for that domain.
- An asset, span or clock that fails validation is reported in the import result and skipped; the rest commit.

**tool** answers with one `result` line (any JSON) or an `error` line:

```json
{"type": "result", "result": {"rows": [{"span_key": "4812:goal:547.0", "clock": "9:07", "label": "…"}]}}
{"type": "error", "code": "bad_request", "message": "unknown player 'blu' (did you mean Blue?)"}
```

In a tool result, every object with a `span_key` this plug-in indexed gets `span` (the catalog span id) and
`asset` added, so the agent can preview and cut it.

## License report

```
retake assets license-report <timeline.json | manifest.json> [--resolved resolved.json] [--json]
```

Lists every asset a timeline or a render manifest uses, with where it is used, its license, the publish blockers,
and the credits for the video description.

- **Manifest** (`spec/manifest.md`): each element's `sources` give the asset; the element's `t` (or its unit's)
  gives output seconds. Exact.
- **Timeline** (`spec/timeline.md`): every `asset` reference (spine items, layers, audio, and a component use's
  props, `spec/components.md`) and every font text layers use. Output times come from `retake-edit resolve --json`
  (given with `--resolved`, or run when `retake-edit` is on `PATH` or in `$RETAKE_EDIT`); without it each use lists
  its anchors as written.
- A timeline asset id that isn't in the catalog is matched by its file (the timeline's `path`, by catalog path, then
  by hash). The built-in `display` font is cleared (OFL).

```json
{ "source": "/…/timeline.json", "kind": "timeline", "publishable": false,
  "assets": [ { "asset": "proj:music/example-tune", "timeline_id": "proj:music", "title": "Example Tune",
                "kind": "audio", "license": { "status": "attribution", "credit": "…", "publish": true },
                "ranges": [[0.0, 61.2]], "uses": [ { "element": "bed", "ptr": "/audio/0" } ] } ],
  "blockers": [ { "asset": "proj:memes/gojo", "reason": "review-only: fine in drafts, blocked at publish",
                  "ranges": [[34.1, 35.6]] } ],
  "warnings": [ "proj:memes/gojo: content_id_risk high" ],
  "credits": "Music: \"Example Tune\" by A. Composer, CC BY 4.0" }
```

## CLI

Every command takes `--project DIR` and `--json`. Exit codes: 0 ok, 1 the operation failed (with `--json`, a
`{"code", "error"}` object on stdout), 2 bad arguments.

| Command | Does |
|---|---|
| `retake assets import …` | Above. |
| `retake assets search "<q>" …` | Above. |
| `retake assets get <id>` | The entry. |
| `retake assets preview <id> [--kind K] [--at T]... [--span sp:N]` | Preview files (made if missing): `contact_sheet`, `strip`, `waveform`, `thumbnail`, `specimen`, or frames at times; a component's `storybook` and `animation`. |
| `retake assets span find [--type T]... [--where SQL] [--near ID@T \| DOMAIN@T] [--window S] [--asset ID] [--limit N]` | Spans. |
| `retake assets span add <id> <t0> <t1> [--type T] [--label L] [--data JSON] [--confidence C]` | Adds a span (`source_of_label: manual`). |
| `retake assets set <id> [--title T] [--tags a,b] [--add-tag T]... [--rm-tag T]... [--caption C] [--license S] [--credit C] [--license-source U] [--content-id-risk R] [--license-note N]` | Edits an entry: the only way to change a license. |
| `retake assets resolve <id>...` | Above. |
| `retake assets rehash <id>...` | Hashes metadata-only assets now. |
| `retake assets license-report <file>` | Above. |
| `retake assets sql "<SELECT>"` | Read-only SQL over the project catalog, plug-in views included. |
| `retake assets plugins` | Discovered plug-ins and what they add. |
| `retake assets plugin <name> <tool> [--args JSON]` | Runs a plug-in tool (`plugin_query`). |
| `retake assets plugin install <dir>` | Links a plug-in folder into the library. |
| `retake assets kit install <dir>` | Installs a brand kit. |
| `retake assets mcp` | Serves the asset tools alone over MCP on stdio (`retake mcp <video>` serves them too). |

Times on the command line are seconds (`75.5`), `m:ss` or `h:mm:ss`.

## MCP tools

Served by `retake mcp <video>` (with the review tools of `spec/notes.md`) and `retake assets mcp`.

| Tool | Does |
|---|---|
| `asset_search(q, kind?, scope?, tags?, license?, type?, where?, limit?)` | Search; asset and span ids with preview paths. |
| `asset_get(id)` | The entry. |
| `asset_preview(id, kind?, at?, span?)` | Preview files, returned as images too (up to 4 per call). |
| `span_find(type?, where?, near?, window?, asset?, limit?)` | Spans. |
| `span_add(asset, t0, t1, type?, label?, data?, confidence?)` | Adds a span (`source_of_label: agent`). |
| `asset_import(paths, scope?, tags?, title?, caption?, via?, args?, transcribe?, in_place?)` | Imports; always `review-only` unless a plug-in record says otherwise. |
| `plugin_query(plugin, tool, args?)` | Runs a plug-in tool. |
| `license_report(path)` | The license report of a timeline or manifest. |
| `<plugin>_<tool>` | Each plug-in tool, with its own input schema. |

## Database (informative)

Tables: `assets` (one row per entry; JSON columns for media, previews, tags, license extras, provenance, meta),
`spans`, `clocks`, `span_types`, `transcripts` (words as JSON, and their text), `derived` (asset, recipe → blob),
`hash_cache` (path, size, mtime → hash), `kits`, and the FTS5 tables `assets_fts` and `spans_fts`. `PRAGMA
user_version` holds the schema version (2: the `component` kind); migrations are forward-only, as in the project
store, and run with foreign keys off, each followed by a foreign key check.

## Later

Embedding search (sqlite-vec, windowed embeddings) as a third fused list; near-duplicate video (frame-pHash
sequences) and audio (Chromaprint); brand kit style sheets as entries; remote catalogs for team mode.
