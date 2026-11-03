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
| Linux (x86_64, arm64) | Nothing; paste one line | `curl -fsSL <release>/install.sh \| sh`, then open **Retake** from the app menu (or run `retake start`). | Built, tested against a local snapshot (install, links, menu entry, checksum refusal). |
| Windows (x64; ARM runs it emulated) | Nothing; paste one line in PowerShell | `irm <release>/install.ps1 \| iex` (or `npm i -g retake-cli`), then open **Retake** from the Start menu (or run `retake`). | Built; CI builds the package and smoke-tests it on `windows-latest` (below). Not yet tried on a Windows desktop by a person. |

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

**Windows** is a folder and a Start menu entry, not an installer. `scripts/install.ps1` (`irm …/install.ps1 | iex`)
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
- `scripts/build-windows.ps1` makes the zip on a Windows runner: the page, `retake.exe` and `retakew.exe` (Go, as
  goreleaser builds them), `retake-edit.exe` (cargo, MSVC), and the pinned downloads (ffmpeg, herdr), into
  `dist/windows/`, plus `dist/retake_<version>_windows_amd64.zip` and its line in `dist/checksums-windows.txt`.
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

Where static builds come from today: Windows, Gyan Doshi's "essentials" build (GPL, with libx264 and zimg;
9.0.2, from its versioned GitHub mirror GyanD/codexffmpeg, pinned by SHA-256 in `scripts/build-windows.ps1`; CI
checks `ffmpeg -buildconf` for both); Linux, John Van Sickle's static builds (GPL; his site lists x264 and zimg among
the libraries; amd64/arm64; 7.0.2, pinned by SHA-256 in `scripts/bundle-deps.sh`; check `ffmpeg -buildconf` on a
real machine before a release) or BtbN/FFmpeg-Builds (GPL and LGPL variants, but 130–150 MB and
rolling); macOS, Martin Riedl's build server (GPL, arm64 and x86_64, signed) or evermeet.cx (Intel only). For the
first real release, **build ffmpeg ourselves in CI** from a pinned source tarball with only what Retake uses (x264,
zimg, opus, the decoders and muxers): smaller (about 20 MB a binary), reproducible, and the source offer is just our
own tarball. The snapshot here bundles ffmpeg for Linux and Windows; the Mac app snapshot has none (Retake then says to
install it).

### The npm package for Windows: `retake-cli-win32-x64`

The NPM worker's packaging makes it from `scripts/build-windows.ps1`'s `dist/windows/` (or the zip, which holds the
same files), the same way as the other platform packages: the programs and `retake-bundle.json` side by side in `bin/`,
so Retake finds its own tools first, and the license files at the package's top.

```
retake-cli-win32-x64/            os: ["win32"], cpu: ["x64"]
  bin/retake.exe                 the server, the page, the worker
  bin/retakew.exe                the console-less launcher (Start menu, login item)
  bin/retake-edit.exe            the renderer
  bin/ffmpeg.exe, bin/ffprobe.exe
  bin/herdr.exe
  bin/conpty/conpty.dll, bin/conpty/herdr-conpty.json,
  bin/conpty/x64/OpenConsole.exe, bin/conpty/arm64/OpenConsole.exe   herdr's ConPTY, beside herdr.exe as it expects
  bin/retake-bundle.json         {"bundle": 1, "version", "tools": {ffmpeg, herdr}}
  licenses/                      ffmpeg-GPLv3.txt, ffmpeg-SOURCE.txt, ffmpeg-build-readme.txt, herdr-LICENSE.txt,
                                 herdr-conpty-LICENSE.txt, herdr-conpty-NOTICE.md
  THIRD_PARTY_NOTICES.md, LICENSE, README.md
```

herdr's version is the one every platform's package has (`scripts/bundle-deps.sh` and `scripts/build-windows.ps1`
pin the same one). The arm64 `OpenConsole.exe` stays although the package is x64: `herdr-conpty.json` lists it.

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
7. **One way to build the Windows zip.** Two paths exist and their zips have the same name: `scripts/build-windows.ps1`
   on `windows-latest` (builds everything natively, including `retakew.exe`), or goreleaser's cross-built `retake.exe`
   plus `scripts/bundle-deps.sh windows_amd64` and the `retake-edit` archive from CI's editor matrix. The release
   workflow should use one; with goreleaser, its build list needs `retakew` (`-H=windowsgui`) too, and its own Windows
   archive must go (`ignore: [{goos: windows}]`) when `build-windows.ps1`'s zip is used.
8. **The app's identifier**, `io.github.samahlstrom.retake` for now (the bundle id and the login item). It's
   hard to change after release, like the name.

## What's built now (this branch)

- `retake start` and the setup page (spec/setup.md), with Go and web unit tests, and end-to-end specs
  (`e2e/tests/40-setup*.spec.ts`, Chromium and WebKit). Settings gained the videos folder and an agent Test.
- Retake's own tools first: `retake-bundle.json` next to the binary puts its folder first on `PATH`; herdr is looked
  for next to the binary before `PATH`.
- `scripts/bundle-deps.sh` (pinned herdr for 4 platforms, ffmpeg for Linux), goreleaser archives with them,
  `scripts/install.sh` installing a bundle, `scripts/macos-app.sh` making the universal, unsigned `Retake.app` with
  its menu bar (`scripts/macos/menubar.js`). Nothing downloaded was run here.
- Windows: `scripts/build-windows.ps1` (the zip, with herdr's and ffmpeg's Windows builds pinned), `scripts/install.ps1`
  (installs it for the user: folder, PATH, Start menu; `-Uninstall`), `cmd/retakew` (the console-less launcher), and
  CI's `windows` job with `scripts/windows-smoke.ps1`. Nothing downloaded was run on Linux; CI runs it on Windows.
- Not built: the `.dmg`, signing, an ffmpeg for macOS, starting herdr's server when none runs (to check with herdr:
  whether `herdr workspace create` starts one), the update check, an app icon.
