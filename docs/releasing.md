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

On Linux and macOS the `retake` archive is the whole install (docs/install-plan.md): next to `retake` it has the tools
that ship with it, which `scripts/bundle-deps.sh` puts in `dist-bundle/<os>_<arch>/` first.

| In the archive | From | Pinned |
|---|---|---|
| `herdr` | herdr's GitHub release v0.8.2 (Apache-2.0) | SHA-256 per platform |
| `ffmpeg`, `ffprobe` | Linux: John Van Sickle's static GPL build 7.0.2. macOS: none yet (the app uses one on `PATH`) | SHA-256 |
| `retake-edit` | This machine's `editor/target/release/retake-edit` for its own platform, else CI's archives in `dist-editor/` | |
| `licenses/` | herdr's LICENSE, ffmpeg's GPLv3 text, its build notes and where its source is | herdr's by SHA-256 |
| `retake-bundle.json` | What's inside, with versions, licenses and sources. It makes `retake` put its folder first on its own `PATH` (spec/setup.md) | |

Downloads are kept in `~/.cache/retake-bundle`, so a second snapshot downloads nothing, and a checksum that doesn't
match stops the build. Nothing downloaded is run while building.

## A local snapshot

```bash
mise exec -- make snapshot      # scripts/bundle-deps.sh, then goreleaser release --snapshot --clean; nothing published
make mac-app                    # dist/Retake.app (universal, unsigned) and dist/Retake_<version>_macos.zip
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

With the bundled tools (this checkout, November 2026): `linux_amd64` 92 MB (ffmpeg and ffprobe are 80 MB each
unpacked), `linux_arm64` 71 MB, `darwin_*` 18 MB (herdr only), and the universal `Retake_<version>_macos.zip` 35 MB.

**The Mac app.** `scripts/macos-app.sh` joins the two darwin archives into one universal `Retake.app` with
`llvm-lipo`: `Contents/MacOS/Retake` is `retake` itself (with no arguments it runs `retake start --app`), next to
`herdr` and the other tools both archives have; `Contents/Resources/` has `menubar.js` (the menu-bar item, run with
`osascript`), `retake-bundle.json`, the licenses and the notices. It isn't signed and has never run on a Mac. Signing,
notarizing and the `.dmg` need a Mac (docs/install-plan.md).

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

`scripts/install.sh` installs the current machine's archive. It never uses sudo. It downloads `checksums.txt` and the
archive, and refuses to install when the archive's SHA-256 doesn't match. An archive with `retake-bundle.json` is
unpacked whole into `~/.local/share/retake/<version>/` (older versions' folders are removed), `retake` and
`retake-edit` are linked into `~/.local/bin`, and on Linux `~/.local/share/applications/retake.desktop` puts
**Retake** in the app menu (`retake start --app`; `--no-menu` leaves it out). A plain archive is copied into
`~/.local/bin`.

```bash
curl -fsSL <release URL>/install.sh | sh                                 # the latest release
curl -fsSL <release URL>/install.sh | sh -s -- --version 0.1.0 --with-editor --dir ~/bin
```

| Setting | Meaning |
|---|---|
| `REPO` (at the top of the script), or `INSTALL_REPO` | The GitHub `owner/repo` the releases come from. Today a placeholder. |
| `INSTALL_BASE_URL` | Another place for one release's files, `https://` or `file://`; `{version}` is replaced. Needs `--version`. |
| `INSTALL_DIR`, or `--dir` | Where the commands go. |
| `XDG_DATA_HOME` | Where the versions' folders and the app-menu entry go (`~/.local/share`). |

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
- [ ] The decisions in docs/install-plan.md: signing (an Apple Developer account), GPL ffmpeg and its source next to
      each release, an ffmpeg for macOS, and herdr's version. Then put the app's `.dmg` and `install.sh` in the
      release.
- [ ] Decide what the git history publishes. Earlier commits contain the owner's private design notes (the original
      `docs/editor-design.md`), names in test data and the legacy goldens.
- [ ] Turn publishing on: `release.disable: false` in `.goreleaser.yaml` (keep `draft: true`), then push a tag and
      run `goreleaser release --clean` with `GITHUB_TOKEN`.
