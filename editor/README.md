# Editor engine (Rust)

Code-only video editing. An agent edits a timeline (`spec/timeline.md`) through patches; `retake-edit` validates it,
resolves its anchors, and renders it, re-rendering only the units whose resolved description changed. Each render
publishes a manifest (`spec/manifest.md`) that the review side uses to resolve notes to elements.

## Layout

- `crates/timeline` (`retake-timeline`): the format, validation, the anchor resolver, units, patches, keyframes and
  easing, and the manifest with note resolution. No pixels.
- `crates/render` (`retake-render`): ffmpeg pipes (decode and encode, never linked), the Rust compositor, Skia
  graphics (`skia-safe`, prebuilt binaries), QuickJS expressions (`rquickjs`), the content-addressed cache, the audio
  mix, and the pipeline.
- `crates/retake-edit`: the CLI.
- `fonts/`: vendored OFL fonts. `display` is Baloo 2 at weight 800.
- `examples/demo`: a demo built only from generated media.

## Commands

Rust is pinned in the repo's `mise.toml`. Build at `nice -n 19`; `.cargo/config.toml` caps jobs at 6.

```sh
cd editor
cargo build --release
cargo test --workspace                       # needs ffmpeg and ffprobe on PATH
cargo clippy --workspace --all-targets -- -D warnings && cargo fmt --check
```

```sh
retake-edit validate   <timeline.json>                     # schema, ids, references, anchors
retake-edit resolve    <timeline.json>                     # anchors → seconds and frames, units
retake-edit patch apply <timeline.json> <patch.json|-> [--dry-run]
retake-edit patch log  <timeline.json>
retake-edit patch revert <timeline.json> <p3>
retake-edit plan       <timeline.json> [--range a-b] [--draft]   # hits, misses, estimate
retake-edit render     <timeline.json> [--range a-b] [--draft] [-o out.mp4]
retake-edit frames     <timeline.json> 3.5 12.0 [-o dir]   # PNG stills
retake-edit manifest   <timeline.json>                     # renders what's missing, prints the manifest
retake-edit resolve-note <manifest.json> 9.0 0.7 0.65 0.1 0.1     # t and a normalized stroke box
retake-edit proxy      <timeline.json> [proj:footage ...]  # short-GOP draft-size proxies (drafts use them)
retake-edit expand     <timeline.json>                     # kit tokens replaced, component uses expanded
retake-edit lint       <timeline.json> [--rules f.json]    # house rules of the timeline's kits; exit 1 on errors
retake-edit component lock    <timeline.json> [--update name@ver | --update-all]
retake-edit component upgrade <timeline.json> <element|all> --to 1.1.0 [--component name]
retake-edit component preview <component dir> [-o dir]     # previews/sheet.png and preview.webp
retake-edit component hash    <package dir>                # the content hash lockfiles and registries pin
```

## Components, kits and lint

A component (`spec/components.md`) is a `component.json`: a props JSON Schema, layers as templates, examples. A
timeline uses one where layers would go, `{"use": "pop-text@1.0.0", "props": {…}}`, and lists brand kits in
`"kits"` for their tokens (`kit:color.accent`) and house rules. `crates/timeline/src/component.rs` finds packages
(`components/` next to the timeline, `$RETAKE_COMPONENT_PATH`, the user library), checks them against the lockfile
`<stem>.lock`, and expands every use into plain layers before anything resolves: substitution, `$expr` templates
(QuickJS, registered by `retake_render::init`), and the fit from the design canvas to the timeline's. A `Project`
keeps both documents: `src` (what patches change and what is saved) and `doc` (the expansion, what renders).

The renderer only sees the expansion, so the layer and unit keys hash it: a changed component re-renders exactly
the elements whose expansion changed (`crates/render/tests/components.rs`). `lint` (`crates/timeline/src/lint.rs`)
evaluates the kits' rules over the resolved elements; `crates/render/src/lint.rs` measures each overlay's drawn box
with Skia for safe-area rules. `component preview` (`crates/render/src/preview.rs`) renders each example through the
normal pipeline with placeholder footage (an ffmpeg gradient) and composes the sheet with Skia. The starter library
is in `library/`.

Every command takes `--json` and prints `{"ok": true, …}` or `{"ok": false, "errors": [{code, message, path, hint,
suggestions}]}`. Renders go to `.retake/renders/` next to the timeline unless `-o` says otherwise; the manifest is
written next to the video.

## The demo

```sh
cd editor/examples/demo
./make-media.sh media ../../target/release/retake-edit   # generated footage, loop, tones, logo, VO
retake-edit render timeline.json
retake-edit patch apply timeline.json patches/01-note-pop.json   # answers note v1#3
retake-edit render timeline.json                                 # 5 of 6 units come from the cache
```

Patching changes `timeline.json` (its `rev` and the patch log), so work on a copy if you want to keep the example
clean.

`loop.sh` runs the whole review loop on a copy, headless (`spec/bridge.md`): it renders v1 into a version family,
starts the review server on a test port, writes a review take with three stroked notes (synthetic words, no audio),
syncs the notes (each gets the timeline elements under its stroke), applies a patch answering one of them and a VO
trim, pushes v2 with `retake push --render`, and prints the changelog, the notes carried over to v2 and the time map.

```sh
RETAKE_EDIT=target/release/retake-edit examples/demo/loop.sh   # needs bin/retake (make build), curl and jq
```

## A project outside the repo

A timeline can live anywhere and point at media in place: asset paths are relative to the timeline file, absolute,
`~/…`, or use `${VAR}`. The engine only reads assets; the cache and renders go to `.retake/` next to the timeline
(`RETAKE_DIR` or `--cache` move them). Asset content is hashed once (cached by path, size and mtime), so a long
recording isn't re-read on every plan.

## Caching, in short

- **Layer key**: a run of consecutive graphics layers, resolved to composition time, plus canvas, fps, the frame range
  in composition time, and font, image and expression inputs. Position-independent: a pop is reused wherever it lands.
  Stored as cropped, LZ4-compressed alpha frames; identical frames are stored once.
- **Static and animated runs.** Consecutive layers split into static stretches (constant props, no text expression, no
  edge inside the range) and animated ones. A static run is drawn once, stored as one image, and keyed without its
  range, so a card with a big shadow under a ticking clock is drawn once and survives ripples that change the shot's
  length. An animated run skips drawing a frame whose layers evaluate to the same state as the frame before (a pop
  that has settled). Layers that show on no frame of the range are left out.
- **Video is decoded at its displayed size**: the source times the largest scale it's shown at in the unit (never
  above the render scale), scaled in the ffmpeg pipe. A picture-in-picture at 30% decodes at 30%.
- **Proxies.** `retake-edit proxy` makes a short-GOP (a keyframe every 0.5 s), 540-line H.264 copy of each video
  asset into the cache, keyed by the source's content hash. Draft renders decode the proxy when there is one (it's
  part of their unit keys); full renders always read the source. Worth it for long recordings with sparse keyframes,
  where every seek otherwise decodes seconds of frames.

Demo, 1920x1080, 483 frames, cold cache, `--jobs 3` (one machine, so only the ratios mean much): a full render went
from 8.6 s (graphics 2.3 s) to 6.9 s (graphics 0.7 s; 120 of 610 graphics frames drawn); a draft from 3.2 s to 2.6 s,
and 2.1 s with proxies (2.9 s to 2.1 s when the footage has one keyframe in 30 s). Re-rendering after the pop patch
went from 2.8 s to 2.3 s; after the VO-trim patch it draws no graphics at all (it drew two runs before), and the time
left is encoding the three units that changed.
- **Unit key**: the hash of the fully resolved unit description, the only input the compositor gets: canvas, frame
  count, encoder settings (including the ffmpeg build), each layer with its source's content hash and source times
  (including absolute-time inputs such as a `clock: timeline` background), and the layer keys on it.
- Segments are stored by content and reused byte for byte. With a cold cache on the same machine, x264 with pinned
  threads and bitexact muxing reproduces the same bytes (the end-to-end test checks it).
