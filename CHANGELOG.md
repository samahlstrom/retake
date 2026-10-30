# Changelog

All notable changes are listed here. Versions follow semver once the first release ships.

## Unreleased

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
