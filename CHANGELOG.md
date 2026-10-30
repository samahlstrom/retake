# Changelog

All notable changes are listed here. Versions follow semver once the first release ships.

## Unreleased

### Editor slice 4: what the item-shop pilot asked for
- Perspective: `transform.rotation_x` / `rotation_y` with `perspective` (CSS's model, about the anchor), and corner
  pins (`transform.corners`), on any layer, keyframable. Footage is decoded once and sampled through the warp
  (bilinear, trilinear mips, anti-aliased edges).
- Blend modes (`normal`, `add`, `screen`, `multiply`, `overlay`, `lighten`, `darken`), opacity groups (`type:
  group`), masks (rect, ellipse or SVG path, feather, invert, an outline on the mask's edge) and alpha or luma track
  mattes, on graphics and footage alike. SVG path shapes and linear-gradient fills.
- `resample: lanczos` (Pillow's LANCZOS for images, byte for byte; ffmpeg's Lanczos for footage, cropped in the
  decode pipe) and `resample: unfiltered` (OpenCV-style bilinear); `delogo` boxes; blur and shadow on footage.
- Text `hinting`, `subpixel` and `metrics: "pillow"` (anchored like Pillow's `anchor="mm"`, measured in a test).
- `expr_lib`: JavaScript helpers shared by a timeline's or a composition's expressions.
- Audio: stream selection (`stream`), mono played at unity in both channels, fade curves, ducking under inserts or
  spine items with attack and release, gain keyframes and expressions, per-element `loudness` targets, and a master
  bus (`master: {loudness, true_peak, lowpass}`: BS.1770-4, two passes, a 4× oversampled look-ahead limiter).
- External raster backends: a component may declare `backend: {command}`; the renderer runs the program over the
  `retake-raster/1` protocol (a JSON request on stdin, framed RGBA frames back, a timeout, a cleared environment),
  with the package hash in the layer key. Example: `editor/examples/backends/python-text/`.
- Fixed: kits entries that aren't kit packages (`plugin:deadlock`) are recorded and ignored again, as in slice 2.

### Editor slice 3: components, brand kits, house rules and the shared library
- Components (`spec/components.md`): a `component.json` with a props JSON Schema (descriptions and defaults an agent
  reads), layers in the timeline's own layer model as templates (`$props`, `$id`, kit tokens, `$expr` computed once),
  examples and a changelog. A timeline uses one where layers would go, `{"use": "pop-text@1.0.0", "props"}`; it is
  designed on a 1920x1080 canvas and fits any other.
- The lockfile, `<stem>.lock`, pins every package to a content hash. Rendering refuses an unlocked or changed package;
  `retake-edit component lock` pins, `patch apply` pins what a patch starts using, and `retake-edit component upgrade
  <element|all> --to x.y.z` moves elements one patch at a time. Expansions go into the layer and unit keys, so a
  component change re-renders only the elements whose expansion changed.
- Brand kits: tokens (`kit:color.accent`) anywhere in a timeline or a component, and house rules that `retake-edit
  lint` checks with typed violations: no-overlap, min-gap, max-count-per-window, safe-area (on the boxes the graphics
  really draw), min/max-duration, and custom JavaScript rules. It exits 1 on errors.
- `retake-edit component preview`: each example at five moments on a storybook sheet (PNG) and a 2-second WebP. A
  component imported into the catalog (`retake assets import <dir>`) is an entry of the new kind `component` with
  those previews (catalog schema version 2).
- Text `highlight` (words of a text layer in another fill, as a prop) and `"$words:<spine-id>"` props (a spine item's
  words on a composition's clock), for word-timed karaoke captions. Manifest elements say which component drew them.
- `retake lib search|add|list|describe|pack|recipe export|lint`, with `--json`: static registries (an `index.json` on
  any host or in a git checkout), vendoring with hash checks and the license gate, and edit recipes that carry the
  timeline, its patches and its packages, with placeholders instead of media. MCP: `component_list`,
  `component_describe` (with the sheet as an image), `lib_search`, `lib_add` and `lint`.
- `library/`: ten original CC0 components (title-card, pop-text, lower-third, caption, pip-frame, progress-bar,
  zoom-punch, counter, callout-arrow, freeze-frame-highlight) and the `clean` kit with its house rules;
  `scripts/build-registry.sh` builds `library/index.json`.

### Milestone 3b: the team server
- `retake serve --data DIR` hosts many projects (`spec/server.md`). Each is a version family with its store, served
  at `/p/<project>/` by the same page and API as `retake open`; the page now uses relative URLs.
- Accounts without passwords: `retake user add` prints a one-time sign-in link (or mails it, with SMTP). Its page
  signs in only through a button, so mail scanners don't use it up. Sessions are server-side, hashed, HttpOnly,
  Secure and SameSite=Lax, rotated daily with a one-minute grace, and end after 7 idle or 30 days; they can be
  revoked.
- Project members are viewers, reviewers, approvers or owners; admins own every project. A take's author is the
  account's name. Share links and agent tokens of 3a work per project, with cookies scoped to `/p/<project>/`.
- Every write without a Bearer token must come from the server's own origin (CSRF). `X-Forwarded-*` is believed only
  from `--trust-proxy` addresses, and the Host must be the public URL's.
- Uploads speak tus 1.0.0 and check the file's SHA-256. `retake remote add` and `retake push --remote` upload in
  chunks and resume, even across runs; `POST /p/<project>/api/push` and the project's `/mcp` (`upload:<id>`) push
  them, with the same push rule.
- Storage drivers: SQLite and local files, or Postgres (a schema per project, same tables) and S3-compatible storage,
  where the workspace is a cache that is uploaded as it changes and downloaded when a project opens. Media is proxied
  from S3 with Range support. A conformance suite runs the same tests on both, with Postgres and MinIO in docker.
- `Dockerfile` (a static binary with the page, ffmpeg and a CPU whisper.cpp, as a non-root user),
  `deploy/docker-compose.yml` (retake, Postgres, MinIO) and `deploy/README.md`. `deploy/smoke.sh` runs the stack end
  to end in a headless browser and tears it down.

### Release readiness (milestone 4, short of publishing)
- Versions come from `vX.Y.Z` git tags (`scripts/version.sh`), and `retake version` prints them.
- `.goreleaser.yaml` builds `retake` for Linux, macOS and Windows on amd64 and arm64, with the page inside, plus
  `checksums.txt`. CI builds `retake-edit` on native runners for the same platforms (no Windows arm64 yet). On a tag,
  CI makes all the archives and publishes nothing.
- `scripts/install.sh` installs `retake` (and `retake-edit` with `--with-editor`) into `~/.local/bin`, without sudo,
  after checking the SHA-256.
- `THIRD_PARTY_NOTICES.md` lists the licenses of everything the binaries contain. `scripts/notices.mjs` writes it,
  and CI fails when it's out of date.
- A CC0 sample project (`examples/sample/`, `scripts/make-sample.sh`): a short how-to video in two versions,
  rendered from a timeline, with a pre-made review take. `examples/sample/run-quickstart.sh` runs the quickstart
  without a person, and CI runs it.
- Docs: a new README, and `docs/quickstart.md`, `agents.md`, `config.md`, `timeline.md` and `releasing.md`.
- notes.md and notes.json name the takes' author ("ask Ana rather than guess"), or "the reviewer", where legacy
  named its owner.
- The e2e suite's legacy mode and the legacy scripts need an explicit path; there's no default anymore.

### Editor, slice 1
- `retake-edit`, a Rust binary that makes videos from a timeline file (`spec/timeline.md`, `timeline/0.1`):
  - a spine of voice-over pieces and inserts as the master clock;
  - anchors (`word:`, `start:`, `end:`) instead of seconds, so a trim moves everything after it;
  - shots and overlays made of text, shape, image and video layers, with keyframes, easing and sandboxed JavaScript
    expressions;
  - one-pass audio mixing with fades and ducking.
- Patches are the only way to change a timeline. They name their base revision and the notes they answer, apply
  atomically, and are logged with their inverse (`patch log`, `patch revert`).
- The renderer reads and writes video through ffmpeg, composites in Rust and draws graphics with Skia. Units are
  cached by content: a render only draws what changed and copies the rest byte for byte.
- Each render writes a manifest (`spec/manifest.md`): which elements are on screen, where, from which sources.
  `retake-edit resolve-note` ranks the elements under a reviewer's stroke.
- Commands: `validate`, `resolve`, `patch`, `plan`, `render`, `frames`, `manifest`, `resolve-note`, each with
  `--json` and typed errors with hints.
- A demo project made only from generated media (`editor/examples/demo`).

### Milestone 2: notes, agent API, questions
- The notes engine is ported to Go (`internal/notes`) and checked against the legacy engine's output (golden tests).
- Transcription with whisper.cpp (`scripts/install-whisper.sh` builds it, CUDA when available), or any command that
  prints words as JSON. Models are downloaded on first use and checksummed.
- A project store (`.retake/project.db`, SQLite) holds notes, questions and answers, versions and their changelogs,
  jobs and events.
- Done closes a take and queues its transcription and notes in the server. Failed jobs show a one-line fix.
- Note states: open, question, answered, accepted, retracted. A push is refused while a note on the parent is neither
  answered nor asked about.
- Agent commands, all with `--json`: `retake notes list|wait|sync|build`, `ask`, `note`, `push`, `versions`. The same
  tools over MCP with `retake mcp`. `retake watch --exec` runs a command when new notes land.
- The review page has a notes sidebar: questions on the newest version, resolved notes on older ones. N hides it.
  The reviewer answers a question by recording.
- While recording, images can be dropped or pasted onto the video, pinned to that moment and spot.
- `retake transcribe`, for debugging transcription.

### Phone layout
- The review page has a layout of its own on phones (a touch screen up to 900 px wide or 600 px high, or any window
  under 800x420). Portrait: the video full width on top, a thumb-sized timeline under it, then play, time, the
  version list and a flip button (C), the pen colors and Undo (Z), and a big Record button (R) with Done at the
  bottom. Landscape: the video fills the height, with the pen colors at the left edge, Record at the right edge and
  a slim bar with the timeline at the bottom. Safe areas (notch, home bar) are respected. The desktop layout is
  unchanged, pixel for pixel.
- Safari (iOS included) records AAC in mp4: segments are saved as `seg_NN.m4a`.
- Tests: an end-to-end spec for the phone layout (19 tests, Chromium mobile emulation with touch input, portrait
  and landscape, plus Safari's mp4-only recorder). `RETAKE_E2E_PORT` moves the suite's ports.

### Milestone 1: local player at parity
- `retake` CLI (Go): `open`, `sessions [--json]`, `version`; config file; flags before or after the video.
- Local server implementing `spec/api.md`, compatible with the legacy Python player in both directions. It refuses
  foreign Host headers (DNS rebinding) and foreign Origins on writes.
- Review page ported to Svelte 5 + TypeScript + Bits UI, pixel-identical to the legacy page in idle, toast and
  version-list states.
- New: audio chunks are buffered in IndexedDB until the server confirms them and resent after a reload.
- New: `.m3u8` versions (native HLS, hls.js fallback), served under `/video/<label>/<file>`.
- New: `author` in `session.json`.
- Take format v1 documented, with a JSON Schema every saved take is validated against.
- Tests: Go unit tests (race), page unit tests (vitest), a Playwright end-to-end suite on generated media (46 tests),
  and a legacy parity script. GitHub Actions workflow for all of it.
