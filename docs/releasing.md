# Releasing

Nothing is published yet. This page says how a release is built, tested and published, the one-time setup it needs,
and how to try all of it without publishing anything.

## Four ways to install, one set of archives

Every release is one set of archives, `retake_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), each with
everything Retake runs (below). The four install routes all hand out those same archives:

| Route | Command | What it is |
|---|---|---|
| npm (first in the README) | `npm i -g retake-cli`, then `retake` | One small package with the command, and one package per platform holding that platform's archive (below). |
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
| `ffmpeg`, `ffprobe` | Static GPL builds with libx264 and zimg (`zscale`), which `retake-edit` renders with: Linux, John Van Sickle's 7.0.2; macOS, Martin Riedl's 9.0.2 (signed); Windows, Gyan Doshi's 9.0.2 "essentials" | SHA-256 |
| `licenses/` | herdr's LICENSE (and ConPTY's), ffmpeg's GPLv3 text, its build notes and where its source is | herdr's and macOS ffmpeg's by SHA-256 |
| `retake-bundle.json` | What's inside, with versions, licenses and sources. It makes `retake` put its folder first on its own `PATH` (spec/setup.md) | |
| `README.md`, `CHANGELOG.md`, `THIRD_PARTY_NOTICES.md`, `LICENSE` | this repo | |

`scripts/bundle-deps.sh` puts the tools in `dist-bundle/<os>_<arch>/` for goreleaser. Downloads are kept in
`~/.cache/retake-bundle`, so a second run downloads nothing, and a checksum that doesn't match stops it. Nothing
downloaded runs while building; the release workflow runs each platform's ffmpeg on that platform (the smoke test
below). `windows_arm64` gets a plain archive (`retake.exe` only). One `checksums.txt` (SHA-256) covers every archive,
the `retake-edit` ones included.

## The npm packages

`scripts/npm-pack.sh` makes them from the archives in `dist/` into `dist/npm/`:

| Package | Holds | Size (November 2026) |
|---|---|---|
| `retake-cli` | `bin/retake.js`, the launcher (npm/retake-cli), and every platform package below as an optional dependency at exactly its version. No install scripts. | 7 KB |
| `retake-cli-linux-x64` | `bin/`: that platform's archive (the programs and `retake-bundle.json`), with `licenses/` and the notices beside it. `os`, `cpu` (and `libc: glibc` on Linux) make npm install only the one that fits. | 92 MB (240 MB unpacked) |
| `retake-cli-linux-arm64` | | 71 MB |
| `retake-cli-darwin-arm64` | | 77 MB without `retake-edit`, about 90 MB with it (CI builds it) |
| `retake-cli-darwin-x64` | | 87 MB without, about 100 MB with it |
| `retake-cli-win32-x64` | | 98 MB without (herdr and ConPTY included), about 111 MB with it |

- **The name** is set in one place, `name` in `npm/retake-cli/package.json`. The platform packages are that name plus
  `-<os>-<cpu>`; the launcher, the pack script and `scripts/npm-claim.sh` all read it from there. Renaming means
  changing that line, the README's commands, and the trusted publishers (below).
- **The launcher** finds `<name>-<os>-<cpu>` with `require.resolve`, runs its `bin/retake` with the same arguments and
  terminal, passes SIGINT, SIGTERM and SIGHUP to it and its exit code (or signal) back. With no package for the
  platform, or one npm left out (`--omit=optional`, `optional=false`), or musl Linux, it says so and what to do.
  `node --test npm/launcher.test.js` (in `make test`) tests it.
- **Agents never run node.** Retake gives them the platform package's Go binary by its full path (spec/setup.md,
  "How agents are told to run retake"), so `retake ask` costs no node start-up and works in a herdr or tmux pane
  where `nvm` or `mise` isn't active.
- **Size.** npmjs.org documents no limit, but has refused a 199 MB tarball and taken a 118 MB one. `npm-pack.sh`
  stops above 117 MB (`NPM_PACK_MAX_BYTES`), so a release can't fail at `npm publish` instead. The biggest, Windows,
  is about 111 MB: close. ffmpeg and ffprobe are two thirds of every package; building our own ffmpeg with only what
  Retake uses (docs/install-plan.md) would roughly halve them, and is the next step if a package nears the limit.
- **npx** keeps its install: a second `npx retake-cli` downloads nothing, and npm 11 checks for a newer version each
  time (one small request) and installs only what changed. `npx retake-cli@latest` asks for the newest explicitly.

## The release workflow

`.github/workflows/release.yml`. Normal pushes never run it.

| When | What it does |
|---|---|
| A `vX.Y.Z` tag is pushed | Every CI test (it calls `ci.yml`); `retake-edit` on each platform's runner; the archives, `checksums.txt`, the Mac app, the npm packages and the Homebrew formula (`package`); the npm install tested on each platform (`smoke`); then a **draft** GitHub release with the archives, `checksums.txt`, `install.sh` and `install.ps1`. Nothing public yet. |
| You publish that draft on GitHub | `npm`: downloads the release's archives, checks them against its `checksums.txt`, makes the npm packages from them and publishes each with trusted publishing and provenance (a version with a `-` goes to the `next` tag, not `latest`). `homebrew`: writes the formula from the release's `checksums.txt` and pushes it to the tap (not for pre-releases). Both skip what's already there, so re-running is safe. |
| Run workflow by hand, `dry_run` on (the default) | Everything a tag push does except the draft, from any branch, with snapshot versions. All of it is uploaded as the run's artifacts: `release` (archives, checksums, Mac app, `retake.rb`), `npm` (the tarballs), `editor-*`. Publishes nothing. |

**The smoke test** (`scripts/npm-smoke.mjs`, the `smoke` jobs: Linux x64 on Node 18 and current, Linux arm64, both
Macs, Windows) serves the npm tarballs from a registry of its own on 127.0.0.1, in a scratch HOME with empty caches,
and checks: `npm i -g` downloads the command and only that platform's package; `retake --version`; the bundled
ffmpeg has libx264 and zimg (its build config, its filters, and a real encode through `zscale`); `retake --no-open`
serves the setup page, finds the bundled ffmpeg and herdr, and stops on SIGINT with its Go process; an agent's prompt
names the Go binary and `retake working` and `retake done` work with nothing on `PATH`; `npx` downloads once, then
nothing, and `@latest` picks up a newer release. On the Macs it also installs the formula from a local tap and runs
`brew test`. Windows is marked experimental (it doesn't block a release) until it passes once.

**Windows builds** call `scripts/build-windows.ps1 -OutDir <dir>` (the Windows port's), which must put
`retake-edit.exe` (and anything else Windows-only) in `<dir>`. Until that script is in the checkout, the job builds
`retake-edit.exe` with cargo and warns.

### A dry run

Push the branch, then start it from the Actions tab (Release, Run workflow, pick the branch, keep `dry_run`
ticked), or with the GitHub CLI:

```bash
git push origin simple-npm
gh workflow run release.yml --ref simple-npm -f dry_run=true
gh run watch "$(gh run list --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId')"
gh run download <run id> -n npm -D dist/npm            # the npm tarballs it made, to look at or test locally
```

### A real release

```bash
git tag v0.1.0 && git push origin v0.1.0              # tests, builds, a draft release; nothing public
# Look at the draft on GitHub (Releases), edit its notes, then press "Publish release":
# that publishes the npm packages and the Homebrew formula.
```

## One-time setup

1. **npm: claim the names and trust the workflow.** Trusted publishing (no npm token anywhere) can only be turned on
   for a package that exists, so each name is published once by hand as a `0.0.0` placeholder. With an npmjs.com
   account with 2FA, and npm 11.10 or newer:

   ```bash
   npm login
   scripts/npm-claim.sh             # says what it will do
   scripts/npm-claim.sh --publish   # publishes the six placeholders, then runs npm trust for each
   ```

   That's these six, each trusting GitHub Actions, repository `samahlstrom/retake`, workflow `release.yml`, no
   environment: `retake-cli`, `retake-cli-linux-x64`, `retake-cli-linux-arm64`, `retake-cli-darwin-arm64`,
   `retake-cli-darwin-x64`, `retake-cli-win32-x64`. By hand instead: publish any `0.0.0` of each name, then on
   npmjs.com, each package's Settings, Trusted Publisher, GitHub Actions, with those three values. Afterwards, under
   Settings, Publishing access, "Require two-factor authentication and disallow tokens" can be turned on: the
   workflow doesn't need a token.
2. **Homebrew: the tap and its token.** Create the public repository `samahlstrom/homebrew-retake` on GitHub (empty,
   or with a README). Make a fine-grained personal access token with access to only that repository, permission
   Contents: Read and write. Add it to `samahlstrom/retake` as an Actions secret named `HOMEBREW_TAP_TOKEN`
   (Settings, Secrets and variables, Actions). The `homebrew` job pushes `Formula/retake.rb` with it.
3. **GitHub Actions** must be allowed to create releases: Settings, Actions, General, Workflow permissions can stay
   read-only, since the `draft` job asks for `contents: write` itself.

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
The draft release's notes start with the `## X.Y.Z` section of `CHANGELOG.md`, so name the section for the version
before tagging.

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

It reads the latest release from GitHub's `/releases/latest`, which skips drafts, so a draft never reaches it. To try
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

- [ ] The one-time setup above (npm names and trusted publishers, the Homebrew tap and its token).
- [ ] A dry run of the release workflow passes (`gh workflow run release.yml --ref <branch> -f dry_run=true`),
      Windows included; then drop `experimental` from the Windows smoke entry.
- [ ] Pick the name for good. Then change it in: `NAME` in the `Makefile`, `project_name` in `.goreleaser.yaml`,
      `NAME` in `install.sh`, `NAME` in `scripts/notices.mjs`, `name` in `npm/retake-cli/package.json`, the CLI's
      usage text, and the docs. The config folder (`~/.config/retake`), the project store (`.retake/`) and the
      `RETAKE_*` variables carry the name too; decide whether they change.
- [ ] The decisions in docs/install-plan.md: signing (an Apple Developer account), GPL ffmpeg and its source next to
      each release, and herdr's version.
- [ ] Decide what the git history publishes. Earlier commits contain the owner's private design notes (the original
      `docs/editor-design.md`), names in test data and the legacy goldens.
