# Contributing

Read [CLAUDE.md](CLAUDE.md) first (it's also linked as `AGENTS.md`). Its rules apply to people and AI agents alike.

## Set up

You need [mise](https://mise.jdx.dev), ffmpeg and ffprobe. mise installs the pinned Go, node, Rust and the lint and
release tools from `mise.toml`.

```bash
mise install
mise exec -- make build                                   # bin/retake, with the page built in
(cd editor && mise exec -- cargo build --release)         # editor/target/release/retake-edit
```

Run everything through mise (`mise exec -- …`), or activate mise in your shell, so the pinned versions are used.

Optional:

- `scripts/install-whisper.sh` builds whisper.cpp for transcription ([docs/config.md](docs/config.md#transcribe)).
- Chromium for the end-to-end tests: `/usr/lib/chromium/chromium`, or set `CHROMIUM_PATH`.

## Layout

| Folder | What's there |
|---|---|
| `cmd/retake/`, `internal/` | The Go server and CLI. |
| `web/` | The review page (Svelte 5, Vite, TypeScript, Bits UI). Its build is embedded into `retake`. |
| `editor/` | `retake-edit`, the Rust timeline renderer ([editor/README.md](editor/README.md)). |
| `spec/` | The contracts: HTTP API, take format, notes and agent API, timeline, manifest, and their JSON Schemas. |
| `e2e/` | Playwright end-to-end tests on generated media ([e2e/README.md](e2e/README.md)). |
| `examples/sample/` | The CC0 sample project behind the quickstart. |
| `scripts/` | Install, release, notices, sample and legacy-parity scripts. |
| `docs/` | User docs, the roadmap and design notes. |

## The spec comes first

`spec/` is the contract between the parts: the server and the page, the server and agents, the editor and the review
side. To change behavior a spec describes, change the spec in the same change, before the code. If the code and the
spec disagree, the code is wrong.

## Tests

CI runs all of these on every push. Run the ones for what you touched before you push.

| Command | Checks |
|---|---|
| `mise exec -- make test` | Go tests with the race detector, and the page's unit tests (vitest). |
| `mise exec -- make lint` | golangci-lint, svelte-check, eslint and prettier. |
| `mise exec -- make e2e` | The Playwright suite against `bin/retake` ([e2e/README.md](e2e/README.md)). |
| `cd editor && mise exec -- cargo test --workspace` | The editor (needs ffmpeg). Also `cargo clippy --workspace --all-targets -- -D warnings` and `cargo fmt --check`. |
| `mise exec -- node scripts/notices.mjs --check` | `THIRD_PARTY_NOTICES.md` is current. After changing dependencies, run it without `--check` and commit the result. |
| `mise exec -- examples/sample/run-quickstart.sh` | [docs/quickstart.md](docs/quickstart.md) still works, end to end. |
| `mise exec -- actionlint` | The CI workflow. |

Every part has tests. A change without a test for its behavior isn't done.

## Rules that are easy to miss

- **Nothing personal or licensed ships.** No home-folder paths, emails, tokens, real footage or anyone's voice. Test
  and sample media is generated (ffmpeg, eSpeak NG) or CC0. Fonts need an open license.
- **The review UX stays minimal:** R to record, the pen only while recording, no extra chrome.
- **Machine hygiene:** run browsers headless and close them; keep scratch data under `.retake-work/`; run heavy jobs
  at `nice -n 19` with at most 6 processes; stop every server you start.
- **No publishing and no LICENSE file** until the owner decides the name and the license
  ([docs/releasing.md](docs/releasing.md)).

## Commits

Small, logical commits. The first line says what changed in plain words (`notes: …`, `editor: …`, `docs: …`).
