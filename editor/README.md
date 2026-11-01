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
retake-edit render     <timeline.json> [--range a-b] [--draft] [-o out.mp4] [--encoder auto|x264|nvenc|…]
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

A component may draw with a program instead of layers (`backend: {command}`, spec/components.md "Backends"): its use
expands into one `raster` layer, and `crates/render/src/backend.rs` runs the program once per graphics run over the
`retake-raster/1` protocol (a JSON request on stdin, framed straight-alpha RGBA on stdout, a cleared environment and
a timeout). `examples/backends/python-text/` is a standard-library Python example; the package hash pins the program.

The renderer only sees the expansion, so the layer and unit keys hash it: a changed component re-renders exactly
the elements whose expansion changed (`crates/render/tests/components.rs`). `lint` (`crates/timeline/src/lint.rs`)
evaluates the kits' rules over the resolved elements; `crates/render/src/lint.rs` measures each overlay's drawn box
with Skia for safe-area rules. `component preview` (`crates/render/src/preview.rs`) renders each example through the
normal pipeline with placeholder footage (an ffmpeg gradient) and composes the sheet with Skia. The starter library
is in `library/`.

Every command takes `--json` and prints `{"ok": true, …}` or `{"ok": false, "errors": [{code, message, path, hint,
suggestions}]}`. Renders go to `.retake/renders/` next to the timeline unless `-o` says otherwise; the manifest is
written next to the video.

## Picture: warps, blend modes, masks, mattes, groups

Every layer goes through one projective transform (`crates/render/src/transform.rs`): the 2D one, or a CSS-style 3D
turn (`rotation_x`, `rotation_y`, `perspective`), or a corner pin (`corners`). Skia draws graphics through it; the
compositor (`composite.rs`, `warp_image`) samples footage through it bilinearly from one decode per layer, with
trilinear mip levels where it shrinks (`resample: unfiltered` skips them, like OpenCV's warps). Layers blend in seven
W3C modes, carry a mask (rect, ellipse or SVG path; feather, invert, an outline on its edge) or a track matte, and
group into opacity groups. In the engine (`engine.rs`) a graphics layer that blends in another mode than `normal`,
has a matte or is one becomes its own run and an item with `Fx`, since the compositor applies modes and mattes
against what's under it; a group of graphics only is drawn by Skia inside its run (children blend within it); a
group with footage is an `Item::Group`, composited into its own picture first. Every item has a `slot` for its
decode state and its manifest boxes. `resample: lanczos` resizes images with a Pillow-exact Lanczos-3
(`resample.rs`) and decodes footage with a Lanczos scaler (zimg's; ffmpeg's for RGB sources), cropped in the pipe when the crop is constant
(`unit.rs`, `decode_plan`); `delogo` runs first in that pipe. `crates/render/tests/picture.rs` checks each against an
independent reference (ffmpeg's `perspective`, `crop,scale` and `delogo`, the W3C formulas) or its other path, and
`tests/pillow.rs` text placement and Lanczos against Pillow (skipped without it; `RETAKE_PILLOW_PYTHON`).

## Audio

The mix (`crates/render/src/audio.rs`) sums the spine, video layers' sound and audio elements in one pass per render
range: each picks its audio stream (`stream`; mono plays at unity in both channels), gets its gain (a constant, or
keys and expressions evaluated per output frame), its fades (with a curve) and its ducking (under the VO's speech,
or under inserts and spine items with attack and release). `crates/render/src/dsp.rs` has the BS.1770-4 meter, the
low-pass and the 4× oversampled true-peak limiter of the master bus. Loudness targets (an element's `loudness`, the
master's) are measured on the whole timeline's mix and cached in the store's `levels` index, so a `--range` render
uses the same gains as the full render and a second render doesn't measure again.

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
  above the render scale), scaled in the ffmpeg pipe. A picture-in-picture at 30% decodes at 30%. Layers that read
  the same frames of a file share that decode ("Rendering fast", below).
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
  count, encoder settings (the encoder and every option it gets, the ffmpeg build), the colour converter, each layer with its source's content hash and source times
  (including absolute-time inputs such as a `clock: timeline` background), and the layer keys on it.
- Segments are stored by content and reused byte for byte. With a cold cache on the same machine, x264 with pinned
  threads and bitexact muxing reproduces the same bytes (the end-to-end test checks it). Hardware encoders make no
  such promise; the cache's reuse is what keeps a unit's bytes.

## Rendering fast

A unit is one shot (or insert): its footage decoded by ffmpeg, composited in Rust, encoded by ffmpeg. Units render
`--jobs` at a time. Within a unit:

- **One decode per source.** Video layers that read the same frames of the same file share one ffmpeg decoder
  (`unit.rs`, `feeds`): the same file and decode (crop, delogo, scaler, colour), the same `in`, clock, loop and hold,
  over the same active frames. Bilinear layers share at the largest size any of them needs; a Lanczos decode is its
  exact displayed size, so only equal ones share. A layer that shows on none of its unit's frames decodes nothing.
  The item-shop pilot's 3D panels are 96 tiles of one clip: 96 decoders became one.
- **Colour conversion with zimg.** When the ffmpeg in use has the `zscale` filter (probed once, cached per machine),
  YUV footage is converted to RGB by zimg in the decode pipe, scaled with spline36 (Lanczos for `resample: lanczos`),
  and comes out as planar RGB that the reader packs into RGBA. It reads the source's matrix and range from its tags;
  untagged sources are read as BT.601 limited range, as ffmpeg's own scaler reads them, so footage looks the same
  either way. RGB, grey and palette sources, and an ffmpeg without zscale, keep ffmpeg's scaler (`swscale`), the old
  path. The choice is in every unit key (`convert`); `RETAKE_CONVERT=swscale` forces the old path. zimg is closer to
  exact and costs a third of the CPU (numbers below).
- **A pipeline per unit.** Each decoder has a reader thread that decodes ahead into a two-frame queue (and starts a
  quarter second before its layer appears); the compositor draws on the unit's thread (rows in parallel); a writer
  thread feeds the encoder. Frame buffers go round (decoders to the compositor and back, the compositor to the
  encoder and back) instead of being allocated per frame. The bytes are the same as composing in lockstep
  (`crates/render/tests/pipeline.rs` checks frames and segments).
- **Hardware encoders.** `--encoder auto|x264|nvenc|videotoolbox|qsv|vaapi` (or `RETAKE_ENCODER`; Retake passes
  `[render] encoder` from its config this way). `auto`, the CLI's default, picks NVENC on NVIDIA, VideoToolbox on
  macOS, else x264, after a real one-frame test encode with the render's own settings, cached for a day per ffmpeg
  build in `<user cache>/retake/ffmpeg-caps.json` (`$XDG_CACHE_HOME/retake`, `~/Library/Caches/retake`,
  `%LOCALAPPDATA%\retake`). Every encoder's settings are in the unit key, so switching re-renders each unit once and
  segments of two encoders never mix. All encoders get the same RGB → YUV conversion (ffmpeg's scaler, BT.709
  limited), the same colour tags, and a keyframe every 2 s (hardware encoders have one forced on every GOP), so stream
  segments cut where they're planned. A process holds at most `RETAKE_HW_SESSIONS` (default 4) hardware sessions;
  a unit whose hardware encoder fails is tried twice more before the render fails. The library's default is x264 (no
  device, the same bytes every run); there is no CUDA, Metal or VAAPI code: only ffmpeg's encoders.

### Encoders

NVENC was tuned on the item-shop pilot's first 40 s (11 units, 2648 frames of 1080p60 game footage, graphics and 3D
panels): each unit's lossless composite encoded on its own, as renders do, and scored against it with libvmaf
(frame-weighted over the units; p5 is the 5th-percentile frame).

| Settings | Size | VMAF | VMAF p5 | PSNR-Y | SSIM | Encode |
|---|---|---|---|---|---|---|
| x264 medium, CRF 18 (the CPU path) | 50.0 MB | 96.75 | 93.89 | 46.41 dB | 0.99909 | 71 fps |
| NVENC p5, CQ 18 (no tuning) | 128.8 MB | 97.98 | 96.24 | 51.75 dB | 0.99966 | 208 fps |
| NVENC p6 hq, spatial AQ, CQ 24 / 25 / 26 / 28 | 67.5 / 60.4 / 54.2 / 43.6 MB | 97.00 / 96.74 / 96.44 / 95.62 | 94.47 / 94.00 / 93.35 / 91.58 | 47.37 / 46.70 / 46.04 / 44.65 | 0.99933 / 0.99920 / 0.99905 / 0.99862 | 190–195 fps |
| **NVENC p7 hq, multipass, spatial + temporal AQ, B-frames as references, CQ 26** (the default) | 56.0 MB | 96.86 | 94.04 | 46.95 dB | 0.99913 | 153 fps |
| the same at CQ 25 | 62.5 MB | 97.09 | 94.61 | 47.58 dB | 0.99927 | 154 fps |

The default matches or beats x264 CRF 18 on every score at 12% more bytes, and one session encodes about twice as
fast as a whole render composites. Drafts use p4, CQ 29, no multipass (not tuned). It needs a Turing or newer GPU
(B-frames as references); on an older one the test encode fails and `auto` picks x264.

VideoToolbox (`h264_videotoolbox -profile:v high -q:v 65 -realtime 0 -allow_sw 0 -g 120 -bf 2`, drafts `-q:v 50`)
is a starting point that **needs a real Mac**: only its command line is tested here. `-q:v` is constant quality,
which only Apple Silicon offers; an Intel Mac fails the test encode and gets x264. Tune `-q:v` the same way: render a
slice losslessly, encode each unit, and compare VMAF and size with x264 CRF 18. QSV (`-preset slower
-global_quality 19`) and VAAPI (`-rc_mode CQP -qp 19`, `RETAKE_VAAPI_DEVICE`) are only used when asked for, and are
untested.
