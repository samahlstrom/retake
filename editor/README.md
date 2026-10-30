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
```

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

## A project outside the repo

A timeline can live anywhere and point at media in place: asset paths are relative to the timeline file, absolute,
`~/…`, or use `${VAR}`. The engine only reads assets; the cache and renders go to `.retake/` next to the timeline
(`RETAKE_DIR` or `--cache` move them). Asset content is hashed once (cached by path, size and mtime), so a long
recording isn't re-read on every plan.

## Caching, in short

- **Layer key**: a run of consecutive graphics layers, resolved to composition time, plus canvas, fps, the frame range
  in composition time, and font, image and expression inputs. Position-independent: a pop is reused wherever it lands.
  Stored as cropped, LZ4-compressed alpha frames; identical frames are stored once.
- **Unit key**: the hash of the fully resolved unit description, the only input the compositor gets: canvas, frame
  count, encoder settings (including the ffmpeg build), each layer with its source's content hash and source times
  (including absolute-time inputs such as a `clock: timeline` background), and the layer keys on it.
- Segments are stored by content and reused byte for byte. With a cold cache on the same machine, x264 with pinned
  threads and bitexact muxing reproduces the same bytes (the end-to-end test checks it).
