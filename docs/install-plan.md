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
| Windows | Not yet | | Not built (see below). |

**macOS is a menu-bar app, not a dock app.** Retake is a server plus a browser page; it has no window of its own, so a
dock icon would open nothing. The menu-bar item has **Open Retake**, **Start at login** and **Quit Retake**. The
person's work happens in the browser, as it does today.

How the Mac app is put together (scripts/macos-app.sh):

- `Contents/MacOS/Retake` is the `retake` binary itself (a universal Go binary, made with `llvm-lipo`). Started with
  no arguments from an app bundle, it runs `retake start --app`: it reads the person's login-shell `PATH` (apps
  started from Finder don't get it, so it would miss `claude` in `~/.local/bin` or `/opt/homebrew/bin`), serves the
  setup page or the videos folder, opens the browser, and never idle-exits.
- `Contents/MacOS/` also holds `retake-edit`, `herdr`, `ffmpeg` and `ffprobe`. Retake puts that folder first on its
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

**Windows** would need: the Go binary (already cross-compiles), `retake-edit` built on a Windows runner (it is in CI
already), herdr's Windows build (exists upstream), an ffmpeg Windows build, a tray app (Go has pure-Win32 tray
libraries), an installer (Inno Setup or MSIX), and a code-signing certificate (an EV certificate, about $300/yr, or
SmartScreen warns for months). The agents (`claude`, `codex`) run on Windows, but `retake`'s worker, herdr and tmux
paths assume a POSIX system and are untested there. Estimate: a week once there is a Windows machine to test on.

## What ships inside

| Part | Why | Source | License |
|---|---|---|---|
| `retake` | The server, the page, the worker. | This repo (goreleaser). | Not decided yet. |
| `retake-edit` | Renders timeline projects. | This repo, built per platform on native runners (it links prebuilt Skia, so it can't be cross-compiled). | Same as retake. |
| `herdr` | Where agents open by default: a workspace per video you can watch and type into. | Pinned upstream release binaries with their SHA-256 (`scripts/bundle-deps.sh`): v0.8.2, the version Retake's herdr support was built and tested against. | Apache-2.0: ship its LICENSE (there's no NOTICE file upstream). |
| `ffmpeg`, `ffprobe` | Probing, thumbnails, stills, renders. | Static builds, pinned with SHA-256 (below). | GPL (recommended) or LGPL. |
| `retake-bundle.json`, `licenses/`, `THIRD_PARTY_NOTICES.md` | The bundle's manifest (what's inside, versions, sources) and every license text. | Written by `scripts/bundle-deps.sh`. | |

**Bundle ffmpeg; don't fetch it on first run.** Fetching saves 40–70 MB of download but adds a network step, a
failure mode and a progress bar to the first run, and on a Mac a binary fetched later is outside the signed,
notarized app. A one-click app should work offline the moment it's opened.

**GPL or LGPL ffmpeg.** `retake-edit` encodes with `libx264` for previews and proxies (and as the CPU encoder), and
`libx264` is GPL. So:

- **GPL build (recommended).** Same results as on our machines. Retake only runs ffmpeg as a separate program, so the
  GPL doesn't reach Retake's own code ("mere aggregation"). What it costs us: ship ffmpeg's license text (done) and
  offer its exact source: put the ffmpeg source tarball (and its build script) next to each release. That's one more
  file per release.
- **LGPL build.** No source-offer duty beyond LGPL's, but no `libx264`: `retake-edit` would have to switch to
  VideoToolbox on Mac (fine) and OpenH264 on Linux (lower quality at the same size), and previews and proxies change.
  Not worth it.

Where the static builds come from (all GPL, all pinned by SHA-256 in `scripts/bundle-deps.sh`, all with
`--enable-libx264 --enable-libzimg` and the `zscale` filter):

| Platform | Build | Checked |
|---|---|---|
| Linux x64, arm64 | John Van Sickle's 7.0.2 | x64 run here: build config, `-filters`, an encode through retake-edit's `zscale` chain with libx264 |
| macOS arm64, x64 | Martin Riedl's 9.0.2 (signed by him) | configure line and filter list read from the binaries here (they can't run on Linux); run on both Mac runners by the release workflow |
| Windows x64 | Gyan Doshi's 9.0.2 "essentials", from his GitHub releases | configure line read here; run on the Windows runner by the release workflow |

`scripts/npm-smoke.mjs` runs each platform's ffmpeg on that platform: build config, filters, and a real encode. Later,
**build ffmpeg ourselves in CI** from a pinned source tarball with only what Retake uses (x264, zimg, opus, the
decoders and muxers): smaller (about 20 MB a binary, which would halve every download), reproducible, and the source
offer is just our own tarball.

### npm: ffmpeg comes in the platform package (decided)

The npm route could have fetched ffmpeg on first run (pinned SHA-256, a progress bar on the setup page) to keep the
packages small. It bundles it instead, in each `retake-cli-<os>-<cpu>` package, for these reasons:

- **The bytes are the same.** First-run fetching saves nothing for the person: they download ffmpeg either way, just
  later. It only moves 60–75 MB from `npm i` to the setup page.
- **It works the moment it's installed, offline after that**, like the app and the archives, which bundle it for the
  same reasons (above). npm and npx cache the package, so a second `npx retake-cli` downloads nothing.
- **No new code that can fail at the worst moment.** First-run fetching needs a downloader, xz or zip unpacking in
  Go, a progress UI, retries, and a third-party host that's up (John Van Sickle's site is one person's server). One
  set of archives also stays the single source for npm, Homebrew, the install scripts and the app.
- **It fits, just.** The packages are 71–111 MB (Windows is the biggest); npmjs.org has taken a 118 MB package (it
  documents no limit; a 199 MB one was refused). `scripts/npm-pack.sh` stops above 117 MB, so a growing build fails
  at packaging, not at `npm publish`. The fix when one nears it: our own smaller ffmpeg (above), not fetching.

## What can't be bundled: the agents

Claude Code, Codex and the rest are the person's own programs with their own accounts and billing. Retake never ships
or installs them. The setup page finds them on `PATH` (with the login shell's `PATH` and the usual folders:
`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`), and when there's none, it shows the makers' own installers,
each with a copy button, then **Check again**:

- Claude Code: `curl -fsSL https://claude.ai/install.sh | bash`, then `claude` once to sign in.
- Codex: `curl -fsSL https://chatgpt.com/codex/install.sh | sh` (or `brew install --cask codex`), then `codex` and
  "Sign in with ChatGPT".

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
2. **GPL ffmpeg** (recommended; we put ffmpeg's source next to each release) or LGPL (needs `retake-edit` to stop
   using `libx264`).
3. **Where agents open by default.** The brief says herdr (bundled). For editors who never open a terminal, "in the
   background" may suit better: herdr is only useful if they open it (`herdr` in Terminal). The setup page asks
   either way; this is only the pre-selected choice.
4. **herdr version.** v0.8.2 is pinned because Retake was built against it; upstream is at v0.9.5. Moving up needs
   one pass over Retake's herdr commands.
5. **Where releases live.** A public GitHub repo (the install script and update check read from it) or a private
   bucket. Either works; the repo also needs a name and a license first (docs/releasing.md).
6. **Windows:** not now, or plan the week above.
7. **The app's identifier**, `io.github.samahlstrom.retake` for now (the bundle id and the login item). It's
   hard to change after release, like the name.

## What's built now (this branch)

- `retake start` and the setup page (spec/setup.md), with Go and web unit tests, and end-to-end specs
  (`e2e/tests/40-setup*.spec.ts`, Chromium and WebKit). Settings gained the videos folder and an agent Test.
- Retake's own tools first: `retake-bundle.json` next to the binary puts its folder first on `PATH`; herdr is looked
  for next to the binary before `PATH`.
- `scripts/bundle-deps.sh` (pinned herdr for 4 platforms, ffmpeg for Linux), goreleaser archives with them,
  `install.sh` installing a bundle, `scripts/macos-app.sh` making the universal, unsigned `Retake.app` with
  its menu bar (`scripts/macos/menubar.js`). Nothing downloaded was run here.
- npm: `npm i -g retake-cli`, then `retake` (npm/, `scripts/npm-pack.sh`, `scripts/npm-smoke.mjs`); plain `retake` in
  a terminal is `retake start` (spec/setup.md). A Homebrew formula (`scripts/brew-formula.sh`), ffmpeg for macOS and
  Windows and herdr for Windows in the archives, and `.github/workflows/release.yml`, which builds every platform on
  its own runner, tests each install, drafts the GitHub release and, once it's published, publishes npm and Homebrew
  (docs/releasing.md).
- Not built: the `.dmg`, signing, starting herdr's server when none runs (to check with herdr: whether `herdr
  workspace create` starts one), the update check, an app icon.
