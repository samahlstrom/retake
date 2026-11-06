# Retake

Review a video by talking and drawing over it. Your AI agent (Claude Code, Codex, or your own) reads the notes, makes
the next version, and asks you when it isn't sure.

```bash
npm i -g retake-cli
retake
```

Needs Node 18+. `retake` opens Retake in your browser and walks you through setup: your videos folder, your agent,
and where it works. Ctrl+C stops it.

- Update: `npm i -g retake-cli@latest`
- Try it without installing: `npx retake-cli`
- Open one folder or video without setup: `retake open ~/Videos`

Everything Retake uses comes with it: ffmpeg, the `retake-edit` renderer and [herdr](https://herdr.dev) (to watch
your agents work). npm downloads only the part for your computer: macOS (Apple silicon, Intel) or Linux (x64, arm64,
glibc). On Windows (x64; ARM runs it emulated) the first `retake` downloads it from Retake's GitHub release instead
(about 110 MB, checked against the SHA-256 in this package) into `%LOCALAPPDATA%\retake\npm`. The one thing you bring
is the agent, installed and signed in with your own account.

Docs, other ways to install (no Node, Homebrew) and the source: <https://github.com/samahlstrom/retake>.
Apache-2.0. The bundled ffmpeg is GPL (its license, and where its source is, are in `licenses/`).
