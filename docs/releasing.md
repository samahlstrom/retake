# Releasing

Nothing is published yet. This page says how versions, archives, the installer and the license notices work, and
what has to happen before the first public release.

## Versions

A version is a git tag `vX.Y.Z`. `scripts/version.sh` turns the checkout into a semver string, and `make build`
links it into the binary:

| Checkout | Version |
|---|---|
| On the tag `v0.1.0` | `0.1.0` |
| 3 commits after it | `0.1.1-dev.3+g1a2b3c4` |
| Before the first tag | `0.0.0-dev+g1a2b3c4` |
| Uncommitted changes | `.dirty` is added to the build part: `+g1a2b3c4.dirty` |

`make version` prints it, and `retake version` prints the one in the binary. A binary from
`go install <module>/cmd/retake@v0.1.0` prints `0.1.0` too.

## What's built where

| Binary | Built by | Platforms |
|---|---|---|
| `retake` | goreleaser (`.goreleaser.yaml`), on one Linux machine. The page is built first and embedded (`-tags webdist`). CGO is off. | linux, darwin, windows × amd64, arm64 |
| `retake-edit` | `cargo build --release` on a native CI runner per platform (the `editor-release` job in `.github/workflows/ci.yml`). | linux × amd64, arm64; darwin × amd64, arm64; windows × amd64 |

`retake-edit` isn't cross-compiled because it links prebuilt Skia, a C++ library. goreleaser's Rust builder uses
cargo-zigbuild, which can't link it for macOS or for Windows' MSVC.

Archives are named `<binary>_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows). Each holds the binary, `README.md`,
`CHANGELOG.md`, `THIRD_PARTY_NOTICES.md`, and the LICENSE once there is one. One `checksums.txt` (SHA-256) covers all
of them.

## A local snapshot

```bash
mise exec -- make snapshot      # goreleaser release --snapshot --clean; archives in dist/, nothing published
```

A snapshot of this checkout made these:

| Archive | Size |
|---|---|
| `retake_0.0.1-snapshot_darwin_amd64.tar.gz` | 6.9 MB |
| `retake_0.0.1-snapshot_darwin_arm64.tar.gz` | 6.5 MB |
| `retake_0.0.1-snapshot_linux_amd64.tar.gz` | 6.9 MB |
| `retake_0.0.1-snapshot_linux_arm64.tar.gz` | 6.4 MB |
| `retake_0.0.1-snapshot_windows_amd64.zip` | 6.9 MB |
| `retake_0.0.1-snapshot_windows_arm64.zip` | 6.4 MB |

The binaries inside are 16–17 MB. Put `retake-edit` archives in `dist-editor/` before running goreleaser, and
`checksums.txt` covers them too.

## On a tag in CI

When a `vX.Y.Z` tag is pushed, CI runs every test job, then:

1. `editor-release` builds and archives `retake-edit` on each platform.
2. `release-snapshot` runs `goreleaser release --snapshot` with those archives in `dist-editor/`.
3. All archives and `checksums.txt` are uploaded as the run's `release-snapshot` artifact, for 14 days.

Nothing is published: `release.disable` is `true` in `.goreleaser.yaml`.

## Release notes and your users' looks

Users' themes live outside the install (spec/theme.md), so a release never changes them, but it can stop using them.
Every release's `CHANGELOG.md` entry says, under **Theme API**:

- tokens and parts added (any release), and defaults that changed (that's a new built-in look);
- tokens and parts deprecated or removed (major releases only; a deprecated name keeps working for one major), with
  `web/src/theme/api.json`'s `deprecated` updated to match;
- the line: run `retake theme check` after updating (hosted: `retake theme check /data/theme`).

The HTTP API's breaking changes are listed the same way, and only in a major release (spec/api.md, "API versions").

## The installer

`scripts/install.sh` installs the binaries for the current machine into `~/.local/bin`. It never uses sudo. It
downloads `checksums.txt` and the archive, and refuses to install when the archive's SHA-256 doesn't match.

```bash
curl -fsSL <release URL>/install.sh | sh                                 # the latest release
curl -fsSL <release URL>/install.sh | sh -s -- --version 0.1.0 --with-editor --dir ~/bin
```

| Setting | Meaning |
|---|---|
| `REPO` (at the top of the script), or `INSTALL_REPO` | The GitHub `owner/repo` the releases come from. Today a placeholder. |
| `INSTALL_BASE_URL` | Another place for one release's files, `https://` or `file://`; `{version}` is replaced. Needs `--version`. |
| `INSTALL_DIR`, or `--dir` | Where the binaries go. |

To try it against a local snapshot:

```bash
INSTALL_BASE_URL="file://$PWD/dist" sh scripts/install.sh --version 0.0.1-snapshot --dir /tmp/retake-test
```

## Third-party notices

`THIRD_PARTY_NOTICES.md` lists every third-party component in the binaries with its license text:

- the Go modules in `retake` for all six platforms, and the Go standard library;
- the npm packages the review page bundles (found from a sourcemap build, so dev tools aren't listed as shipped);
- the crates in `retake-edit`'s dependency graph;
- the C and C++ libraries inside prebuilt Skia and QuickJS, which cargo can't see: they're kept by hand in
  `scripts/notices-extra.json`;
- the fonts (Baloo 2 in `retake-edit`, Go Bold in `retake`);
- the development and test packages, without texts.

`mise exec -- node scripts/notices.mjs` regenerates it, and CI fails when it's out of date. The script stops when
`skia-bindings` or `rquickjs-sys` changes version, so the hand-kept list is reviewed on upgrades.

## Before the first public release

- [ ] Pick the name. Then change it in: `NAME` in the `Makefile`, `project_name` in `.goreleaser.yaml`, `NAME` in
      `scripts/install.sh`, `NAME` in `scripts/notices.mjs`, the CLI's usage text, and the docs. The config folder
      (`~/.config/retake`), the project store (`.retake/`) and the `RETAKE_*` variables carry the name too; decide
      whether they change.
- [ ] Pick the license. Add LICENSE (the archives pick it up), and SPDX headers if wanted.
- [ ] Set the module path: `go mod edit -module <new path>`, rewrite the imports, and update `local-prefixes` in
      `.golangci.yml`. The Makefile and goreleaser read the module path from `go.mod`.
- [ ] Set `REPO` in `scripts/install.sh`, and publish the script with each release.
- [ ] Run CI on GitHub once. The workflow has only been checked with actionlint, and the release jobs have never run.
- [ ] Decide what the git history publishes. Earlier commits contain the owner's private design notes (the original
      `docs/editor-design.md`), names in test data and the legacy goldens.
- [ ] Turn publishing on: `release.disable: false` in `.goreleaser.yaml` (keep `draft: true`), then push a tag and
      run `goreleaser release --clean` with `GITHUB_TOKEN`.
