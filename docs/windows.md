# Retake on Windows

Retake runs on Windows 10 and 11 (x64; Windows on ARM runs the x64 build emulated). It works as everywhere else: a
server on your computer and the page in your browser. This page is what's different.

## Install

In PowerShell (no administrator rights needed):

```powershell
irm https://github.com/samahlstrom/retake/releases/latest/download/install.ps1 | iex
```

It checks the download against the release's SHA-256 list, installs into `%LOCALAPPDATA%\Programs\retake`, adds that
folder to your PATH and puts **Retake** in the Start menu. Then open Retake from the Start menu, or run `retake` in a
new terminal.

- Security software on managed PCs often blocks that kind of line (a script run straight from the internet). Save the
  script first, then run it:

  ```powershell
  curl.exe -fsSLo install.ps1 https://github.com/samahlstrom/retake/releases/latest/download/install.ps1
  powershell -ExecutionPolicy Bypass -File install.ps1
  ```

  `-Version 0.4.0` installs a given version; `-Uninstall` takes Retake away again (your settings and videos stay).
- Or with npm, as on any system: `npm i -g retake-cli`.
- Retake isn't code-signed yet. The install line above doesn't trip SmartScreen, but a zip you download in a browser
  and unpack yourself is marked as coming from the internet: the first start then says "Windows protected your PC".
  Click **More info**, then **Run anyway**. Once is enough.

Everything Retake uses comes with it: herdr (where you can watch your agents), ffmpeg and ffprobe, and `retake-edit`,
the renderer. You bring the agent:

- **Claude Code**: `irm https://claude.ai/install.ps1 | iex`, then `claude` once to sign in. Its shell tool needs Git
  for Windows (`winget install Git.Git`).
- **Codex**: `npm install -g @openai/codex`, then `codex` and "Sign in with ChatGPT".

Retake finds them on your PATH, npm's `.cmd` launchers included. The setup page shows the same lines.

## Where things are

| What | Where |
|---|---|
| Retake itself | `%LOCALAPPDATA%\Programs\retake` (install line), or npm's global folder |
| Settings (`config.toml`), themes, extensions | `%APPDATA%\retake` |
| Data, caches, the running Retake's state | `%LOCALAPPDATA%\retake` |
| Your videos, by default | `%USERPROFILE%\Videos` |
| An agent's task files (prompt, context, report, log) | `.retake\worker\` in the video's folder, as everywhere |

`XDG_CONFIG_HOME` and the other `XDG_*` variables still win when you set them (docs/config.md).

## Where agents work

Settings → **Where agents run** (and the setup page) offers two choices on Windows (`open_agents_in` in config.toml):

- **In herdr** (the default): each video gets a workspace you can watch and type into. Run `herdr` in a terminal to see
  them. herdr's Windows build runs its panes through ConPTY; its sessions keep running when you close the terminal
  window. See herdr's own [Windows page](https://herdr.dev/docs/windows-beta/) for what differs there. (It also notes
  that endpoint security can block its `irm | iex` installer; Retake brings herdr along, so that doesn't apply.)
- **In the background**: the agent runs with no window; you see what it did on the page.

tmux is offered only where it's installed, so not on Windows. Agents outlive Retake here too: quitting Retake
(Ctrl+C, closing its terminal) leaves them working, and Retake picks them up when it starts again.

## Other differences

- **Move to Trash** moves the video's folder to the Recycle Bin, where Explorer can restore it, and **Undo** puts it
  back. A folder on a drive without a Recycle Bin (a network drive, most USB sticks) stays where it is.
- **Show in folder** opens Explorer with the video selected.
- **Start at login** (`retake autostart on`) adds Retake to your account's Run key; it starts `retakew.exe start --app`,
  which runs Retake without a console window and opens the browser.
- There's no tray or menu-bar icon: Retake lives in your browser. Open it again from the Start menu; it finds the one
  already running.
