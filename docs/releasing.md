# Releasing

This page says how a release is built, tested and published, the setup it relies on, and how to try all of it
without publishing anything.

## Cutting a release

```bash
git tag vX.Y.Z && git push origin vX.Y.Z
```

That's all. The release workflow tests everything, builds every platform, tests each install, then publishes the
GitHub release, the npm packages and the Homebrew formula, in that one run (below). Name the `## X.Y.Z` section of
`CHANGELOG.md` before tagging: it becomes the release notes.

## Four ways to install, one set of archives

Every release is one set of archives, `retake_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), each with
everything Retake runs (below). The four install routes all hand out those same archives:

| Route | Command | What it is |
|---|---|---|
| npm (first in the README) | `npm i -g retake-cli`, then `retake` | One small package with the command, and one package per platform (macOS, Linux) holding that platform's archive. On Windows the command downloads the release's archive on first run (below). |
| No Node, macOS and Linux | `curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh \| sh` | `install.sh` (repo root): downloads the archive from the GitHub release, checks its SHA-256. |
| No Node, Windows | `irm https://raw.githubusercontent.com/samahlstrom/retake/main/install.ps1 \| iex` | `install.ps1` (repo root, the Windows port's). |
| Homebrew | `brew install samahlstrom/retake/retake` | A formula in the tap `samahlstrom/homebrew-retake`, made from the release's checksums (`scripts/brew-formula.sh`). |

The Mac app (`Retake_<version>_macos.zip`, unsigned) is on the release too (docs/install-plan.md).

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
`go install <module>/cmd/retake@v0.1.0` prints `0.1.0` too. npm drops a version's `+build` part.

## What's in an archive

| File | From | Pinned |
|---|---|---|
| `retake` | goreleaser (`.goreleaser.yaml`), the page built first and embedded (`-tags webdist`), CGO off, cross-compiled on one Linux runner | the commit |
| `retake-edit` | `cargo build --release` on each platform's own runner (it links prebuilt Skia, which can't be cross-linked): Ubuntu 22.04 x64 and arm64 (so glibc 2.35 is enough), macOS 15 arm64 and Intel, Windows | the commit |
| `herdr` | herdr's GitHub release v0.8.2 (Apache-2.0). Windows: `herdr.exe` and its `conpty/` folder (Microsoft's ConPTY, MIT), side by side | SHA-256 per platform |
| `ffmpeg`, `ffprobe` | Static GPL builds with libx264 and zimg (`zscale`), which `retake-edit` renders with: Linux, our own 9.0.2, built by the release workflow from source (`scripts/ffmpeg-build/build.sh`, below); macOS, Martin Riedl's 9.0.2 (signed); Windows, Gyan Doshi's 9.0.2 "essentials" | Linux: every source and the build image; the others: SHA-256 |
| `whisper-cli` | whisper.cpp 1.9.5, built on each supported native release runner with `GGML_NATIVE=OFF`; it is portable CPU transcription support | source tarball SHA-256 in `scripts/install-whisper.sh`; MIT license in each archive |
| `licenses/` | herdr's LICENSE (and ConPTY's), ffmpeg's GPLv3 text, its build notes and where its source is (Linux: also x264's, zimg's, zlib's and musl's texts, and `ffmpeg-build.txt`) | herdr's and macOS ffmpeg's by SHA-256 |
| `retake-bundle.json` | What's inside, with versions, licenses and sources. It makes `retake` put its folder first on its own `PATH` (spec/setup.md) | |
| `README.md`, `CHANGELOG.md`, `THIRD_PARTY_NOTICES.md`, `LICENSE` | this repo | |

`scripts/bundle-deps.sh` puts the tools in `dist-bundle/<os>_<arch>/` for goreleaser. Downloads are kept in
`~/.cache/retake-bundle`, so a second run downloads nothing, and a checksum that doesn't match stops it. Nothing
downloaded runs while building; the release workflow runs each platform's ffmpeg on that platform (the smoke test
below). `windows_arm64` gets a plain archive (`retake.exe` only). One `checksums.txt` (SHA-256) covers every archive,
the `retake-edit` ones and `ffmpeg-source-<v>.tar.xz` included.

### Linux's ffmpeg, and its source

Linux's `ffmpeg` and `ffprobe` are built by `scripts/ffmpeg-build/build.sh` (docs/install-plan.md, "Linux: our own
ffmpeg"): static against musl in a pinned Alpine container, from FFmpeg 9.0.2, x264, zimg, zlib and musl, each pinned
by SHA-256, about 23 MB a binary. In the release workflow, the `ffmpeg` job (`.github/workflows/ffmpeg.yml`) builds
them on an x64 and an arm64 runner, runs them there (`build.sh --check`) and uploads `ffmpeg-linux-amd64` and
`ffmpeg-linux-arm64`; `package` hands those to `bundle-deps.sh` (`RETAKE_FFMPEG_DIR`). The build is cached by the
script's SHA-256, so it only rebuilds when the script changes.

Every release carries **`ffmpeg-source-9.0.2.tar.xz`** next to its archives: the source tarballs as released, the
build script and a README on rebuilding them. That's the GPL's "corresponding source"; each archive's
`licenses/ffmpeg-SOURCE.txt` links it. It's in the `release` artifact of a dry run, and the release uploads it.

To bump a version, change its pin (file name, SHA-256, URL) in `build.sh`, and for FFmpeg `FFMPEG_VERSION`, which
`bundle-deps.sh` reads. A local `make snapshot` builds Linux's ffmpeg in Docker the first time (`~/.cache/retake-bundle/
ffmpeg`; the other arch runs under emulation, which is slow), or takes a build from `RETAKE_FFMPEG_DIR`: download a
release run's `ffmpeg-linux-amd64` and `ffmpeg-linux-arm64` artifacts into one folder (`gh run download <id> -p
'ffmpeg-linux-*' -D dist-ffmpeg`) and set `RETAKE_FFMPEG_DIR=dist-ffmpeg`.

## The npm packages

`scripts/npm-pack.sh` makes them from the archives in `dist/` into `dist/npm/`:

| Package | Holds | Size (November 2026) |
|---|---|---|
| `retake-cli` | `bin/retake.js`, the launcher (npm/retake-cli), and every platform package below as an optional dependency at exactly its version. For Windows, `archives` in its package.json: the release's `retake_<version>_windows_amd64.zip`, its URL, SHA-256 (from the release's `checksums.txt`) and size. No install scripts. | 12 KB |
| `retake-cli-linux-x64` | `bin/`: that platform's archive (the programs and `retake-bundle.json`), with `licenses/` and the notices beside it. `os`, `cpu` (and `libc: glibc` on Linux) make npm install only the one that fits. | 54 MB (131 MB unpacked; ffmpeg and ffprobe 23 MB each) |
| `retake-cli-linux-arm64` | | 52 MB (119 MB unpacked) |
| `retake-cli-darwin-arm64` | | 89 MB |
| `retake-cli-darwin-x64` | | 100 MB |

**These five names are all there will be.** Every new npm name has to be published once by hand, with the owner's
2FA, before trusted publishing works for it, and npm's spam filter refused the first publish of
`retake-cli-win32-x64` outright. So Windows has no package:

- **Windows downloads its archive on first run.** With no platform package for the OS and CPU but an entry in
  `archives` (Windows x64, and Windows on ARM, which runs the x64 build emulated), the launcher downloads
  `https://github.com/samahlstrom/retake/releases/download/v<version>/retake_<version>_windows_amd64.zip`, saying one
  line on stderr ("Getting Retake 0.2.3 for Windows (113 MB)…"). It refuses the file unless its SHA-256 is the one
  in package.json (never one it downloads), unpacks it with Node alone (zlib; why not Windows' `tar.exe` is in the
  launcher) into a temporary folder in `%LOCALAPPDATA%\retake\npm` (`$XDG_CACHE_HOME/retake/npm` on other systems)
  and renames that to `<version>`, so two launches at once both end up with one whole folder. Every later run starts
  `retake.exe` from there; it keeps the three newest versions. No network, a missing release or a wrong SHA-256 each
  get a plain error. `RETAKE_NPM_BASE_URL` points it at another copy of the release's files (the smoke test's
  stand-in for GitHub); the SHA-256 is checked all the same.
- **The name** is set in one place, `name` in `npm/retake-cli/package.json`. The platform packages are that name plus
  `-<os>-<cpu>`; the launcher, the pack script and `scripts/npm-claim.sh` all read it from there. Renaming means
  changing that line, the README's commands, and the trusted publishers (below).
- **The launcher** finds `<name>-<os>-<cpu>` with `require.resolve`, runs its `bin/retake` with the same arguments and
  terminal, passes SIGINT, SIGTERM and SIGHUP to it and its exit code (or signal) back. With no build for the
  platform, or a package npm left out (`--omit=optional`, `optional=false`), or musl Linux, it says so and what to
  do. `node --test npm/launcher.test.js` (in `make test`) tests it, Windows' download from a local server included.
- **Agents never run node.** Retake gives them the Go binary itself (spec/setup.md, "How agents are told to run
  retake"): the platform package's by its full path, and on Windows the downloaded folder first on their `PATH`. So
  `retake ask` costs no node start-up and works in a herdr or tmux pane where `nvm` or `mise` isn't active.
- **Size.** npmjs.org documents no limit, but has refused a 199 MB tarball and taken a 118 MB one. `npm-pack.sh`
  stops above 117 MB (`NPM_PACK_MAX_BYTES`), so a release can't fail at `npm publish` instead. Linux's packages are
  about half the others because their ffmpeg is our own lean build (above).
- **npx** keeps its install: a second `npx retake-cli` downloads nothing, and npm 11 checks for a newer version each
  time (one small request) and installs only what changed. `npx retake-cli@latest` asks for the newest explicitly.

## The release workflow

`.github/workflows/release.yml`. Normal pushes never run it.

| When | What it does |
|---|---|
| A `vX.Y.Z` tag is pushed | Every CI test (it calls `ci.yml`); `retake-edit` on each platform's runner; Linux's ffmpeg (`ffmpeg`, it calls `ffmpeg.yml`); the archives, `checksums.txt`, the Mac app, the npm packages and the Homebrew formula (`package`); the npm install tested on each platform (`smoke`). Once all of that passed, with nobody pressing anything: `release` publishes the GitHub release with the archives, `ffmpeg-source-<v>.tar.xz`, `checksums.txt`, `install.sh` and `install.ps1` (a draft until every file is up, so it's never public half-uploaded). Then, side by side: `npm` checks that Windows' archive on the release has the SHA-256 the command carries and publishes the tarballs the install tests passed with (trusted publishing, provenance; a version with a `-` goes to the `next` tag, not `latest`); `homebrew` writes the formula from the release's `checksums.txt` and pushes it to the tap (not for pre-releases). |
| Run workflow by hand, `dry_run` on (the default), or a push to a `dry-run/*` branch | Everything a tag push does, with snapshot versions, up to the point of publishing: `release` lists what it would upload, `npm` what it would publish, `homebrew` checks its key can push to the tap. All of it is uploaded as the run's artifacts: `release` (archives, `ffmpeg-source-<v>.tar.xz`, checksums, Mac app, `retake.rb`), `npm` (the tarballs), `windows-archive`, `editor-*`, `ffmpeg-linux-*`. Publishes nothing. |

**When a publish job fails**, the others still go out: npm and Homebrew don't wait on each other, and both only wait
for the GitHub release. If npm refuses a package (it says "need auth" when a package doesn't trust the workflow), the
`npm` job fails naming it, in its log and the run's summary, and publishes the platform packages it can but not
`retake-cli`, which would list a missing one. Fix the cause, then **Re-run failed jobs** on the run's page: every
publish step skips what's out already.

**The smoke test** (`scripts/npm-smoke.mjs`, the `smoke` jobs: Linux x64 on Node 18 and current, Linux arm64, both
Macs, Windows) serves the npm tarballs from a registry of its own on 127.0.0.1, in a scratch HOME with empty caches,
and on Windows the release's zip from a stand-in for GitHub Releases (also on 127.0.0.1, redirecting like
github.com does). It checks: `npm i -g` downloads the command and only that platform's package (Windows: the command
alone); `retake --version` (Windows: the first run downloads the archive and checks it, the second downloads
nothing); the bundled
ffmpeg has libx264 and zimg (its build config, its filters, and a real encode through `zscale`); `retake --no-open`
serves the setup page, finds the bundled ffmpeg and herdr, and stops on SIGINT with its Go process; an agent's prompt
names the Go binary and `retake working` and `retake done` work with nothing on `PATH`; `npx` downloads once, then
nothing, and `@latest` picks up a newer release. On the Macs it also installs the formula from a local tap and runs
`brew test`. Every one of them must pass before anything is published.

**Windows builds** call `scripts/build-windows.ps1 -OutDir <dir>` (the Windows port's), which must put
`retake-edit.exe` (and anything else Windows-only) in `<dir>`. Until that script is in the checkout, the job builds
`retake-edit.exe` with cargo and warns.

### A dry run

GitHub offers **Run workflow** only for workflows on the default branch, so until `release.yml` is on `main`, push the
branch under a `dry-run/` name, which runs it as a dry run:

```bash
git push origin HEAD:dry-run/release                   # from the branch to test; publishes nothing
gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
gh run download <run id> -n npm -D dist/npm            # the npm tarballs it made, to look at or test locally
git push origin --delete dry-run/release               # afterwards
```

Once it's on `main`: Actions, Release, Run workflow, pick any branch, keep `dry_run` ticked; or
`gh workflow run release.yml --ref <branch> -f dry_run=true`.

### A real release

`git tag vX.Y.Z && git push origin vX.Y.Z` (Cutting a release, at the top). Nothing else: no draft to press, no
second workflow.

## One-time setup (done)

All of this exists (November 2026). Nothing needs doing again unless the name changes.

- **npm.** Five packages, each trusting GitHub Actions, repository `samahlstrom/retake`, workflow `release.yml`, no
  environment (trusted publishing: no npm token anywhere): `retake-cli`, `retake-cli-linux-x64`,
  `retake-cli-linux-arm64`, `retake-cli-darwin-arm64`, `retake-cli-darwin-x64`. Never add a name (above).
  `scripts/npm-claim.sh` made them: trusted publishing can only be turned on for a package that exists, so it
  publishes a `0.0.0` placeholder of each name by hand (`npm login`, 2FA, npm 11.10+), then runs `npm trust` for
  each. Only a rename would need it again. `retake-cli-win32-x64` was never created (npm called it spam); nothing
  uses it.
- **Homebrew.** The public tap `samahlstrom/homebrew-retake`, with a deploy key that can write to it and nothing else
  ("retake release workflow"), whose private half is this repository's `HOMEBREW_TAP_DEPLOY_KEY` secret. The
  `homebrew` job checks the tap out with it and pushes `Formula/retake.rb`. To replace the key (it needs `gh`
  signed in as the owner, and deletes the key file after):

  ```sh
  k=$(mktemp -u) && ssh-keygen -q -t ed25519 -N "" -C "retake release" -f "$k" && gh repo deploy-key add "$k.pub" -R samahlstrom/homebrew-retake --allow-write --title "retake release workflow" && gh secret set HOMEBREW_TAP_DEPLOY_KEY -R samahlstrom/retake < "$k"; rm -f "$k" "$k.pub"
  ```
- **GitHub Actions** creates the release itself: the repository's default workflow permissions stay read-only, and
  the `release` job asks for `contents: write`.

## A local snapshot

```bash
mise exec -- make snapshot      # scripts/bundle-deps.sh, then goreleaser release --snapshot; archives in dist/
mise exec -- make npm-pack      # dist/npm/*.tgz (--partial: this machine builds retake-edit for itself only)
mise exec -- make npm-smoke     # the smoke test above, on this machine, against those tarballs
make mac-app                    # dist/Retake.app (universal, unsigned) and dist/Retake_<version>_macos.zip
scripts/brew-formula.sh 0.0.1-snapshot dist/checksums.txt "file://$PWD/dist"   # a formula for these archives
```

`RETAKE_SMOKE_PORTS=9180,9181,9182` picks the smoke test's ports (default: free ones), `RETAKE_SMOKE_KEEP=1` keeps its
scratch folder (`.retake-work/npm-smoke`).

## Release notes and your users' looks

Users' themes live outside the install (spec/theme.md), so a release never changes them, but it can stop using them.
Every release's `CHANGELOG.md` entry says, under **Theme API**:

- tokens and parts added (any release), and defaults that changed (that's a new built-in look);
- tokens and parts deprecated or removed (major releases only; a deprecated name keeps working for one major), with
  `web/src/theme/api.json`'s `deprecated` updated to match;
- the line: run `retake theme check` after updating (hosted: `retake theme check /data/theme`).

The HTTP API's breaking changes are listed the same way, and only in a major release (spec/api.md, "API versions").
The release's notes start with the `## X.Y.Z` section of `CHANGELOG.md`, so name the section for the version before
tagging.

## The install script

`install.sh` installs the current machine's archive. It never uses sudo. It downloads `checksums.txt` and the
archive, and refuses to install when the archive's SHA-256 doesn't match. An archive with `retake-bundle.json` is
unpacked whole into `~/.local/share/retake/<version>/` (older versions' folders are removed), `retake` and
`retake-edit` are linked into `~/.local/bin`, and on Linux `~/.local/share/applications/retake.desktop` puts
**Retake** in the app menu (`retake start --app`; `--no-menu` leaves it out). A plain archive is copied into
`~/.local/bin`. Running it again updates.

```bash
curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh | sh                 # the latest release
curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh | sh -s -- --version 0.1.0 --dir ~/bin
```

| Setting | Meaning |
|---|---|
| `REPO` (at the top of the script), or `INSTALL_REPO` | The GitHub `owner/repo` the releases come from: `samahlstrom/retake`. |
| `INSTALL_BASE_URL` | Another place for one release's files, `https://` or `file://`; `{version}` is replaced. Needs `--version`. |
| `INSTALL_DIR`, or `--dir` | Where the commands go. |
| `XDG_DATA_HOME` | Where the versions' folders and the app-menu entry go (`~/.local/share`). |

It reads the latest release from GitHub's `/releases/latest`, which skips drafts, so a release still uploading its
files never reaches it. To try
it against a local snapshot:

```bash
INSTALL_BASE_URL="file://$PWD/dist" sh install.sh --version 0.0.1-snapshot --dir /tmp/retake-test
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
`skia-bindings` or `rquickjs-sys` changes version, so the hand-kept list is reviewed on upgrades. The bundled
programs (herdr, ConPTY, ffmpeg) carry their own texts in each archive's `licenses/`.

## Before the first public release

- [x] The one-time setup above (npm names and trusted publishers, the Homebrew tap and its deploy key).
- [x] A dry run of the release workflow passes, Windows included, and Windows' install test blocks a release.
- [ ] Pick the name for good. Then change it in: `NAME` in the `Makefile`, `project_name` in `.goreleaser.yaml`,
      `NAME` in `install.sh`, `NAME` in `scripts/notices.mjs`, `name` in `npm/retake-cli/package.json`, the CLI's
      usage text, and the docs. The config folder (`~/.config/retake`), the project store (`.retake/`) and the
      `RETAKE_*` variables carry the name too; decide whether they change.
- [ ] The decisions in docs/install-plan.md: signing (an Apple Developer account), GPL ffmpeg (Linux's source ships
      next to each release; AV1 decoding?), and herdr's version.
- [ ] Decide what the git history publishes. Earlier commits contain the owner's private design notes (the original
      `docs/editor-design.md`), names in test data and the legacy goldens.
