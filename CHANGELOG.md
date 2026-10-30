# Changelog

All notable changes are listed here. Versions follow semver once the first release ships.

## Unreleased

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
