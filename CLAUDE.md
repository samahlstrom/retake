# Retake (working name): repo rules

Retake is an open-source video review tool. You watch a version of a video, press R, and talk and draw over it. An AI
agent turns the notes into the next version and asks when it's unsure. See `docs/roadmap.md` for the plan, and
`spec/` for the contracts.

## Layout
- `cmd/retake/`: the CLI entry point (Go).
- `internal/`: the server, version-family detection, probing, take storage and config (Go).
- `web/`: the review page (Svelte 5, Vite, TypeScript, Bits UI). Its build is embedded into the Go binary.
- `e2e/`: Playwright end-to-end tests and fixture generators.
- `editor/`: `retake-edit`, the Rust timeline renderer (`editor/README.md`).
- `spec/`: the HTTP API, the take format and JSON Schemas. Change the spec first, then the code.
- `examples/sample/`: the CC0 sample project behind `docs/quickstart.md` (`scripts/make-sample.sh`).
- `docs/`: roadmap and user docs. `docs/private/` is gitignored: the owner's notes, never committed.

## Commands
Toolchain versions are pinned in `mise.toml`, so run Go and node through mise: `mise exec -- go test ./...`.
- `make build`: web build, then the Go binary with the page embedded (`-tags webdist`), into `bin/retake`.
  `make server` builds without the page (serves a "page not built" stub; use `--ui-dir`).
- `make test`: Go tests (race) and web unit tests. `make lint`: golangci-lint, svelte-check, eslint, prettier.
- `make e2e`: Playwright suite (see `e2e/README.md`; `RETAKE_SERVER=legacy` runs it against the legacy player).
- `make parity`: the legacy player's own e2e driver against this server (local only; needs `LEGACY_REVIEW_DIR`).
- `make snapshot`: release archives in `dist/` (goreleaser; publishes nothing). See `docs/releasing.md`.
- `node scripts/notices.mjs`: regenerate `THIRD_PARTY_NOTICES.md` after any dependency change (CI checks it).
- `examples/sample/run-quickstart.sh`: `docs/quickstart.md` end to end, without a person.

## Non-negotiables
- **The review UX is sacred.** Pen only, minimal, R to record, and the pen does nothing unless recording. Milestone 1
  matches the legacy player exactly.
- **Nothing personal or licensed ships.** No /home/<user> paths, emails, tokens, footage or anyone's voice. No game
  art, commercial music, or fonts without open licenses. Test media is generated (ffmpeg) or CC0.
- **Never modify or write into existing videos/renders outside this repo.** Tests make their own fixtures in a scratch
  dir.
- **No LICENSE file and no publishing** (GitHub, npm, any registry) until the owner decides. The name "retake" isn't
  final either.
- **Every part has tests.** CI runs tests, type checks and lint on every change.

## Machine hygiene (tests, agents)
- Headless Chromium: launch `/usr/lib/chromium/chromium` (not `/usr/bin/chromium`, which adds desktop flags), or set
  `CHROMIUM_PATH`. Close browsers in `finally`, and never leave one running.
- Run node after `ulimit -c 0`. Heavy jobs run at `nice -n 19` with at most 6 processes.
- `/tmp` is a small RAM disk. Put scratch media under `.retake-work/` in the repo (gitignored) or `~/.cache/retake-work/`.
  Delete scratch data and stop scratch servers when done.
- Never open windows, images or the browser on the desktop. Always pass `--no-open`.
