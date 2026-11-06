# Install plan: one click, then it's running

Goal: someone who has never opened a terminal downloads Retake, opens it, answers three questions in the browser and
is looking at their videos. Everything Retake needs ships with it, except the AI agent, which the person installs and
signs in to with its maker's own installer.

Status of each part is at the end of every section: **built** (in this repo, tested on Linux), **built, untested**
(made here, never run on that OS), or **not built**.

## What "one click" means

| OS | What you download | What you do | Status |
|---|---|---|---|
| macOS (Apple silicon and Intel) | `Retake.dmg` with one universal `Retake.app` | Drag it to Applications, open it. A menu-bar icon appears and the browser opens the setup page. | App bundle: built, untested. `.dmg`: not built (needs a Mac or a macOS CI runner for `hdiutil`); a `.zip` of the app is built instead. |
| Any, with Node 18+ (the README's first route) | Nothing; paste one line | `npm i -g retake-cli`, then `retake`: setup opens in the browser. | Built; tested end to end on Linux (`scripts/npm-smoke.mjs`), and on every platform by the release workflow's dry run (docs/releasing.md). |
| Linux (x86_64, arm64), macOS without Node | Nothing; paste one line | `curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh \| sh`, then `retake` (or **Retake** in the Linux app menu). Or `brew install samahlstrom/retake/retake`. | Built, tested against a local snapshot (install, links, menu entry, checksum refusal). The Homebrew formula: tested by `brew install` on both Mac runners in the release workflow. |
| Windows (x64; ARM runs it emulated) | Nothing; paste one line in PowerShell | `irm <release>/install.ps1 \| iex` (or `npm i -g retake-cli`, whose first run downloads the release's zip), then open **Retake** from the Start menu (or run `retake`). | Built; CI builds the zip and smoke-tests the npm route, download included, on `windows-latest` (below). Not yet tried on a Windows desktop by a person. |

**macOS is a menu-bar app, not a dock app.** Retake is a server plus a browser page; it has no window of its own, so a
dock icon would open nothing. The menu-bar item has **Open Retake**, **Start at login** and **Quit Retake**. The
person's work happens in the browser, as it does today.

How the Mac app is put together (scripts/macos-app.sh):

- `Contents/MacOS/Retake` is the `retake` binary itself (a universal Go binary, made with `llvm-lipo`). Started with
  no arguments from an app bundle, it runs `retake start --app`: it reads the person's login-shell `PATH` (apps
  started from Finder don't get it, so it would miss `claude` in `~/.local/bin` or `/opt/homebrew/bin`), serves the
  setup page or the videos folder, opens the browser, and never idle-exits.
- `Contents/MacOS/` also holds `retake-edit`, `herdr`, `ffmpeg`, `ffprobe` and `whisper-cli`. Retake puts that folder first on its
  own `PATH`, so it, its renders and its agents use these copies. The person's shell is never changed.
- `Contents/Resources/menubar.js` is the menu-bar item, a short JavaScript-for-Automation script that `osascript`
  runs (ships with every Mac, nothing to compile). The core (server, browser, setup) is Go and tested on Linux; only
  this menu is Mac-only. If it fails, Retake still runs and opens the browser.
- Known gap: opening the app a second time while it runs shows nothing (macOS hands the "reopen" to a process with no
  window). The menu-bar icon is the way back. A small native Swift launcher, built on a Mac, fixes this; do that
  when a Mac is in the loop.

Linux: the install script unpacks the release into `~/.local/share/retake/<version>/`, links `retake` and
`retake-edit` into `~/.local/bin`, and adds a **Retake** entry to the app menu (`~/.local/share/applications`). No
sudo. An AppImage would add a double-clickable file but no tray or autostart for free; it's not worth it before
there are Linux users who aren't developers.

**Windows** is a folder and a Start menu entry, not an installer. `install.ps1` at the repo's top (`irm …/install.ps1 |
iex`; the release also carries it)
downloads `retake_<version>_windows_amd64.zip`, checks its SHA-256 against the release's `checksums.txt`, unpacks it
into `%LOCALAPPDATA%\Programs\retake` (an older install there is replaced file by file; a running Retake keeps going),
adds that folder to the user's PATH and makes a **Retake** Start menu shortcut. No administrator rights, no MSI or
MSIX: there's nothing to register. `-Uninstall` undoes it, leaving settings and videos. docs/windows.md is the
user's page.

- The shortcut and the login item (`retake autostart on`: the user's Run key) run `retakew.exe start --app`.
  `retakew.exe` (`cmd/retakew`, built with `-H=windowsgui`) is a tiny launcher with no console window that starts
  `retake.exe` beside it hidden, so a person never sees a console they could close by mistake. `retake.exe` stays a
  console program for the terminal. No tray icon: Retake lives in the browser, and opening it again from the Start
  menu finds the one that runs.
- The release's Windows zip is made like the others: goreleaser cross-builds `retake.exe` and `retakew.exe` on Linux,
  and `scripts/bundle-deps.sh` adds herdr, ffmpeg, `retake-edit.exe` and `whisper-cli.exe`. The release workflow
  builds `retake-edit.exe` with `scripts/build-windows.ps1 -OutDir <dir>` and builds Whisper from its pinned source on
  `windows-latest`, after it initializes MSVC; neither can be cross-compiled.
- `scripts/build-windows.ps1` without `-OutDir` builds that same package natively in one go, for CI and by hand: the
  page, `retake.exe`, `retakew.exe`, `retake-edit.exe`, `whisper-cli.exe` and the pinned downloads (ffmpeg, herdr, the same pins as
  `bundle-deps.sh`) in `dist/windows/`, plus `dist/retake_<version>_windows_amd64.zip` and its line in
  `dist/checksums-windows.txt`. That zip is for testing; the release uses goreleaser's.
- CI's `windows` job (`.github/workflows/ci.yml`) runs the Go tests and the page's unit tests on `windows-latest`,
  builds `retake-edit`, runs `build-windows.ps1`, and smoke-tests the result (`scripts/windows-smoke.ps1`):
  `retake start` on a scratch folder of generated clips, the page and a poster, then a typed comment sent to a fake
  agent (a PowerShell script) that makes the next version in the background.
- Signing: unsigned for now. Files the install line downloads aren't marked as coming from the internet, so
  SmartScreen doesn't stop them; a zip downloaded in a browser is, and its first start shows "Windows protected your
  PC" (More info → Run anyway). Signing is a decision below.

## What ships inside

| Part | Why | Source | License |
|---|---|---|---|
| `retake` | The server, the page, the worker. | This repo (goreleaser). | Apache-2.0. |
| `retakew` (Windows) | Starts `retake` with no console window: the Start menu entry and the login item run it. | This repo (`cmd/retakew`, `-H=windowsgui`). | Same as retake. |
| `retake-edit` | Renders timeline projects. | This repo, built per platform on native runners (it links prebuilt Skia, so it can't be cross-compiled). | Same as retake. |
| `herdr` | Where agents open by default: a workspace per video you can watch and type into. | Pinned upstream release binaries with their SHA-256 (`scripts/bundle-deps.sh`; on Windows `scripts/build-windows.ps1`): v0.8.2 on every platform, the version Retake's herdr support was built and tested against. Windows: `herdr-windows-x86_64.zip`, which is `herdr.exe` plus the `conpty\` folder (Microsoft's ConPTY: `conpty.dll`, `OpenConsole.exe`, `herdr-conpty.json`) that herdr loads from beside itself, so it ships whole, in that layout. | Apache-2.0: ship its LICENSE (there's no NOTICE file upstream). The ConPTY files are MIT: their LICENSE and NOTICE go in `licenses/`. |
| `ffmpeg`, `ffprobe` | Probing, thumbnails, stills, renders. | Linux: our own static build from pinned sources (below). macOS, Windows: static builds, pinned with SHA-256. | GPL-3.0-or-later as built (with x264). |
| `whisper-cli` | Optional local transcription runtime; models still download on first use. | whisper.cpp 1.9.5 built natively with portable CPU flags and no bundled model. | MIT. |
| `retake-bundle.json`, `licenses/`, `THIRD_PARTY_NOTICES.md` | The bundle's manifest (what's inside, versions, sources) and every license text. | Written by `scripts/bundle-deps.sh`. | |

**Bundle ffmpeg; don't fetch it on first run.** Fetching saves 40–70 MB of download but adds a network step, a
failure mode and a progress bar to the first run, and on a Mac a binary fetched later is outside the signed,
notarized app. A one-click app should work offline the moment it's opened.

**GPL or LGPL ffmpeg.** `retake-edit` encodes with `libx264` for previews and proxies (and as the CPU encoder), and
`libx264` is GPL. So:

- **GPL build (chosen).** Same results as on our machines. Retake only runs ffmpeg as a separate program, so the
  GPL doesn't reach Retake's own code ("mere aggregation"). What it costs us: ship ffmpeg's license text (done) and
  its exact source: `ffmpeg-source-<version>.tar.xz` next to each release's archives (below). One more file per
  release.
- **LGPL build.** No source-offer duty beyond LGPL's, but no `libx264`: `retake-edit` would have to switch to
  VideoToolbox on Mac (fine) and OpenH264 on Linux (lower quality at the same size), and previews and proxies change.
  Not worth it.

Where the static builds come from (all GPL 9.0.2, all pinned by SHA-256, all with `--enable-libx264
--enable-libzimg` and the `zscale` filter):

| Platform | Build | Checked |
|---|---|---|
| Linux x64, arm64 | **Our own**, built by the release workflow from pinned sources (`scripts/ffmpeg-build/build.sh`, below) | on the runner that built it, then by the npm smoke test on Ubuntu 22.04 x64 and arm64 |
| macOS arm64, x64 | Martin Riedl's 9.0.2 (signed by him; `scripts/bundle-deps.sh`) | configure line and filter list read from the binaries here (they can't run on Linux); run on both Mac runners by the release workflow |
| Windows x64 | Gyan Doshi's 9.0.2 "essentials", from his GitHub releases (`scripts/bundle-deps.sh`) | configure line read here; run on the Windows runner by the release workflow |

`scripts/npm-smoke.mjs` runs each platform's ffmpeg on that platform: build config, filters, and a real encode.

#### Linux: our own ffmpeg

Linux had no build we could ship: John Van Sickle's site timed out from GitHub's runners, BtbN's GPL build is 173 MB a
binary (it links dozens of libraries, which pushed the npm packages past npm's limit), and Martin Riedl's Linux builds
are configured with `--enable-nonfree`, which can't be redistributed. So the release builds its own, small one.

- **What's in it.** FFmpeg 9.0.2 with `--enable-gpl --enable-version3 --enable-libx264 --enable-libzimg`, and every
  one of FFmpeg's own decoders, encoders, demuxers, muxers, parsers, filters, protocols and bitstream filters, plus
  libavdevice's `lavfi` input. Outside libraries: x264 (stable branch, pinned commit), zimg 3.0.6, and zlib 1.3.2
  (PNG and compressed MP4 headers need it). `--disable-autodetect` turns off everything else (no TLS, no hardware
  encoders, no X11, no sound devices), so the build is the same whatever the image has. No ffplay.
- **Static, against musl.** It's built in an Alpine container (`alpine:3.24.3`, pinned by digest) and linked
  `-static`, so it needs no shared library at all, not even a libc: it runs on any x64 or arm64 Linux. A 2 MB thread
  stack is linked in (musl's default, 128 KB, is too small for some codecs).
- **About 23 MB a binary** (BtbN's was 173 MB): FFmpeg is compiled at `-O2` rather than its default `-O3` (8 MB less a
  binary; its hot loops are assembly either way, and x264 and zimg keep their own flags), unused functions are
  dropped (`--gc-sections`), C gets no unwind tables, and it's a plain static executable (`-no-pie`, no relocations).
- **Everything Retake runs is in it**, checked against the code: `zscale`, `scale`, `format`, `colorspace`, `crop`,
  `pad`, `overlay`, `fps`, `tile`, `select`, `setsar`, `ebur128`, `loudnorm`, `aresample`, `aformat`, `pan`,
  `volume`, `asplit`, the `lavfi` sources (`testsrc2`, `color`, `sine`, `anullsrc`), libx264, AAC, PNG, MJPEG, PCM,
  `h264_metadata` and `aac_adtstoasc`, the concat demuxer, MP4/MOV, Matroska/WebM, image2, HLS, f32le and ADTS, and
  the decoders for what people drop in (H.264, HEVC, VP8, VP9, ProRes, Opus, Vorbis, MP3, FLAC, ...). Nothing Retake
  runs reads https. Not in it: AV1 *decoding* (FFmpeg has no software AV1 decoder of its own; it needs dav1d) and
  NVENC (needs a dynamically linked build): `retake-edit`'s `auto` encoder falls back to x264 without it.
- **Pinned.** Every source by SHA-256, the image by digest, all in the one script. The build is cached by the
  script's own SHA-256 (in CI with actions/cache, locally in `~/.cache/retake-bundle/ffmpeg`), so a release whose
  script didn't change takes seconds; a change rebuilds both arches, about 10 minutes on GitHub's runners.
- **Where it runs.** `.github/workflows/ffmpeg.yml`, on `ubuntu-24.04` and `ubuntu-24.04-arm`: `release.yml` calls it
  and bundles its artifacts, and it runs by itself when the script changes. After building, it runs the binaries
  there (`build.sh --check`): no interpreter or shared library, `-buildconf` has x264 and zimg, an encode through
  retake-edit's `zscale` chain with libx264 and AAC read back by ffprobe, PNG and JPEG stills, `h264_metadata`,
  concat into Matroska, `ebur128` and `loudnorm`, f32le, HLS; and the sizes. Locally, `make snapshot` runs the same
  script in Docker (the other arch under emulation, slowly), or takes prebuilt binaries from `RETAKE_FFMPEG_DIR`.
- **The source the GPL asks for** ships with each release as one asset, `ffmpeg-source-9.0.2.tar.xz`: the FFmpeg,
  x264, zimg, zlib and musl tarballs as released, the build script, and a README saying how to rebuild. It's listed
  in `checksums.txt`. Each archive's `licenses/` has `ffmpeg-SOURCE.txt` (where that file is),
  `ffmpeg-build.txt` (the sources' SHA-256s, the configure line, the compiler and image) and the license texts of
  FFmpeg, x264, zimg, zlib and musl. GCC's runtime (libgcc, libstdc++, for zimg) is linked under the GCC Runtime
  Library Exception.

### npm: ffmpeg comes in the platform package (decided)

The npm route could have fetched ffmpeg on first run (pinned SHA-256, a progress bar on the setup page) to keep the
packages small. It bundles it instead, in each `retake-cli-<os>-<cpu>` package (macOS and Linux; Windows has no
package and fetches the whole release archive, below), for these reasons:

- **The bytes are the same.** First-run fetching saves nothing for the person: they download ffmpeg either way, just
  later. It only moves 60–75 MB from `npm i` to the setup page.
- **It works the moment it's installed, offline after that**, like the app and the archives, which bundle it for the
  same reasons (above). npm and npx cache the package, so a second `npx retake-cli` downloads nothing.
- **No new code that can fail at the worst moment.** First-run fetching needs a downloader, xz or zip unpacking in
  Go, a progress UI, retries, and a third-party host that's up (John Van Sickle's site is one person's server). One
  set of archives also stays the single source for npm, Homebrew, the install scripts and the app.
- **It fits.** npmjs.org has taken a 118 MB package (it documents no limit; a 199 MB one was refused).
  `scripts/npm-pack.sh` stops above 117 MB, so a growing build fails at packaging, not at `npm publish`. The Linux
  packages are 52–54 MB since they carry our own ffmpeg (above); macOS is 89–100 MB. The fix when one nears the
  limit: our own smaller ffmpeg for that platform too, not fetching.

### npm on Windows: the release's zip, downloaded on first run

There's no `retake-cli-win32-x64`: npm's spam filter refused that name's first publish, and every new name needs the
owner to publish it once by hand before the workflow may. So `retake-cli`'s package.json carries, in `archives`, the
URL of the release's `retake_<version>_windows_amd64.zip` (goreleaser's, above), its SHA-256 from the release's
`checksums.txt` and its size, written by `scripts/npm-pack.sh`. Windows on ARM gets the same zip and runs it emulated,
as `install.ps1` does.

The first `retake` downloads it from GitHub ("Getting Retake 0.2.3 for Windows (113 MB)…" on stderr), refuses it
unless the SHA-256 matches, unpacks it with Node's zlib and renames the folder into place in one step, so two
launches at once can't leave half a folder:

```
%LOCALAPPDATA%\retake\npm\<version>\      the zip as it is: the same files install.ps1 installs
  retake.exe                     the server, the page, the worker
  retakew.exe                    the console-less launcher (Start menu, login item)
  retake-edit.exe                the renderer
  ffmpeg.exe, ffprobe.exe, whisper-cli.exe
  herdr.exe
  conpty\conpty.dll, conpty\herdr-conpty.json,
  conpty\x64\OpenConsole.exe, conpty\arm64\OpenConsole.exe   herdr's ConPTY, beside herdr.exe as it expects
  retake-bundle.json             {"bundle": 1, "version", "tools": {ffmpeg, herdr}}
  licenses\                      ffmpeg-GPLv3.txt, ffmpeg-SOURCE.txt, ffmpeg-build-readme.txt, herdr-LICENSE.txt,
                                 herdr's ConPTY license and notice, whisper-cpp-LICENSE.txt
  THIRD_PARTY_NOTICES.md, LICENSE, README.md, CHANGELOG.md
```

Every later run starts `retake.exe` from there, and Retake puts that folder first on its own `PATH` and its agents'
(`retake-bundle.json`, spec/setup.md). The three newest versions are kept. herdr's version is the one every
platform's archive has (`scripts/bundle-deps.sh` and `scripts/build-windows.ps1` pin the same one). The arm64
`OpenConsole.exe` stays although the build is x64: `herdr-conpty.json` lists it.

## What can't be bundled: the agents

Claude Code, Codex and the rest are the person's own programs with their own accounts and billing. Retake never ships
or installs them. The setup page finds them on `PATH` (with the login shell's `PATH` and the usual folders:
`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`), and when there's none, it shows the makers' own installers,
each with a copy button, then **Check again**:

- Claude Code: `curl -fsSL https://claude.ai/install.sh | bash`, then `claude` once to sign in.
- Codex: `curl -fsSL https://chatgpt.com/codex/install.sh | sh` (or `brew install --cask codex`), then `codex` and
  "Sign in with ChatGPT".
- On Windows: Claude Code with `irm https://claude.ai/install.ps1 | iex` (its shell tool needs Git for Windows:
  `winget install Git.Git`), Codex with `npm install -g @openai/codex`. Retake finds `claude.exe`, `codex.exe` and npm's
  `.cmd` launchers on PATH.

This is the one place a normie needs Terminal: paste one line, then sign in in the browser window the agent opens.
There's no way around it short of the agent makers shipping a GUI installer for their CLI. **Test** on the setup page
runs the agent once with a one-line prompt (Claude Code on its cheapest model, no tools), so a missing sign-in shows
up before the first real edit.

## Code signing and notarization

An Apple Developer account is $99 a year. With it, CI signs every binary in the app with a Developer ID certificate
(Retake, retake-edit, herdr, ffmpeg, ffprobe; re-signing herdr and ffmpeg is allowed by their licenses), notarizes the
`.dmg` with `notarytool` and staples it. Opening it is then a normal double-click.

Without it, the app is unsigned, and on the first open macOS says it "can't be opened because Apple cannot check it
for malicious software". The person has to:

1. Open it once (it's blocked), then go to **System Settings → Privacy & Security**, scroll down, click **Open
   Anyway**, and confirm with their password; or
2. run `xattr -dr com.apple.quarantine /Applications/Retake.app` in Terminal.

That's a few minutes of confusion for every editor, and some IT-managed Macs don't allow it at all. For HealthTree's
editors, the $99 is worth it. Linux needs no signing (the install script checks SHA-256 against the release's
`checksums.txt`).

## Updates

- **Today, by route:** npm, `npm i -g retake-cli@latest` (`npx retake-cli` checks for a newer version by itself);
  Homebrew, `brew upgrade retake`; the install scripts, run the same line again.
- **First (not built yet):** the app checks once a day for a newer release (a small JSON file next to the releases: version, date,
  notes, download URL) and shows "Retake 0.4 is out" with a link in Settings. The person downloads and replaces the
  app. Nothing changes on its own. On Linux the same note says to run the install line again.
- **Later, signed:** Sparkle (the standard Mac self-updater, with its own signature on each update) inside a native
  launcher. Not before there's an Apple Developer account, since an unsigned app that updates itself gets
  quarantined again.

Retake already survives restarts without losing agent work (docs/agents.md), so an update is: quit, replace, open.

## Decisions for Sam

1. **Apple Developer account ($99/yr)** for signing and notarizing. Without it, every editor goes through the "Open
   Anyway" steps once, and managed Macs may refuse the app.
2. **GPL ffmpeg** (built that way; on Linux it's our own build and its source ships next to each release) or LGPL
   (needs `retake-edit` to stop using `libx264`). Linux's build leaves out AV1 decoding (dav1d, about 2 MB more) and
   NVENC: say if footage in AV1 should work out of the box.
3. **Where agents open by default.** The brief says herdr (bundled). For editors who never open a terminal, "in the
   background" may suit better: herdr is only useful if they open it (`herdr` in Terminal). The setup page asks
   either way; this is only the pre-selected choice.
4. **herdr version, one on every platform.** v0.8.2 is pinned everywhere because Retake was built against it (it has
   a Windows build); upstream is at v0.9.5. 0.9.x has Windows fixes Retake would benefit from (Codex and Claude Code
   prompt submission on Windows, npm agent shims), which is a reason to move up. Moving needs one pass over Retake's
   herdr commands and new pins in `scripts/bundle-deps.sh` and `scripts/build-windows.ps1` together.
5. **Where releases live.** A public GitHub repo (the install script and update check read from it) or a private
   bucket. Either works; the repo also needs a name and a license first (docs/releasing.md).
6. **Windows signing.** Unsigned works for the install line (no SmartScreen stop) but not for a zip downloaded in a
   browser. Azure Trusted Signing (about $10 a month, needs a verifiable organization or individual identity) or an
   OV certificate signs `retake.exe`, `retakew.exe` and `retake-edit.exe` in CI; SmartScreen reputation then builds up
   per certificate.
7. **The app's identifier**, `io.github.samahlstrom.retake` for now (the bundle id and the login item). It's
   hard to change after release, like the name.

## What's built now (this branch)

- `retake start` and the setup page (spec/setup.md), with Go and web unit tests, and end-to-end specs
  (`e2e/tests/40-setup*.spec.ts`, Chromium and WebKit). Settings gained the videos folder and an agent Test.
- Retake's own tools first: `retake-bundle.json` next to the binary puts its folder first on `PATH`; herdr is looked
  for next to the binary before `PATH`.
- `scripts/bundle-deps.sh` (pinned herdr for 4 platforms; Linux's ffmpeg from `scripts/ffmpeg-build/build.sh`),
  goreleaser archives with them, `install.sh` installing a bundle, `scripts/macos-app.sh` making the universal,
  unsigned `Retake.app` with its menu bar (`scripts/macos/menubar.js`). Nothing downloaded was run here.
- npm: `npm i -g retake-cli`, then `retake` (npm/, `scripts/npm-pack.sh`, `scripts/npm-smoke.mjs`); plain `retake` in
  a terminal is `retake start` (spec/setup.md); on Windows its first run downloads the release's zip. A Homebrew
  formula (`scripts/brew-formula.sh`), ffmpeg for macOS and Windows and herdr for Windows in the archives, and
  `.github/workflows/release.yml`, which on a tag push builds every platform on its own runner, tests each install,
  then publishes the GitHub release, npm and Homebrew in the same run (docs/releasing.md).
- Windows: `install.ps1` (installs a release for the user: folder, PATH, Start menu; `-Uninstall`), `cmd/retakew`
  (the console-less launcher, in the goreleaser build), `scripts/build-windows.ps1` (`-OutDir` for the release's
  `retake-edit.exe`, else the whole package), and CI's `windows` job with `scripts/windows-smoke.ps1`. Nothing
  downloaded was run on Linux; CI runs it on Windows.
- Not built: the `.dmg`, signing, starting herdr's server when none runs (to check with herdr: whether `herdr
  workspace create` starts one), the update check, an app icon.
