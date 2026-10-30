# Retake (working name)

Review a video by talking over it. Press **R**, say what you think, draw on the frame with a pen, press **R** again.
Your notes are pinned to the exact moment. An AI agent turns them into the next version, and you flip between old and
new with **C**.

> Status: milestone 1 (the local player) is done. Not released, and the name and license aren't final.

## What works today

- `retake open clip_v6.mp4` opens the review page for that file and finds its other versions (`clip_v5.mp4`,
  `../v7/clip_v7.mp4`, …) automatically.
- **R** records a segment of voice; the pen draws only while recording. Every word and stroke is pinned to video time.
- **[ ]** step between versions, **C** flips to the comparison version at the same moment.
- Takes save automatically next to the video in `<name>.review/<timestamp>/`. Audio is kept in the browser until the
  server confirms it, so closing the tab or a crash loses nothing.
- Versions can be single files or `.m3u8` playlists.

All keys: R record · Space play · ←/→ 5 s · 1–4 pen color · Z undo · C compare · [ ] versions · , . frame step · F
fullscreen.

## Build and run

Needs [mise](https://mise.jdx.dev) (installs the pinned Go and node) and ffmpeg (`ffprobe` reads the videos).

```bash
mise install
mise exec -- make build          # page + Go binary with the page inside -> bin/retake
bin/retake open path/to/clip_v2.mp4
bin/retake sessions path/to/clip_v2.mp4 --json
```

Optional config at `~/.config/retake/config.toml`: `name`, `port`, `idle_exit_minutes`, `open_browser`.

## Sharing

`retake share clip_v6.mp4 --label alex` puts the review behind an HTTPS tunnel (cloudflared, or `--tunnel tailscale`)
and prints a link with a QR code. A link is a viewer, reviewer (the default) or approver; it expires (24 h by default),
and `retake share revoke` ends it early. Agents elsewhere use `/mcp` with `retake token create --role agent`. Local use
needs no token. See [`docs/sharing.md`](docs/sharing.md).

## Develop

```bash
mise exec -- make test   # Go (race detector) + page unit tests
mise exec -- make lint   # golangci-lint, svelte-check, eslint, prettier
mise exec -- make e2e    # Playwright end-to-end suite on generated media
```

Docs: [`spec/api.md`](spec/api.md) (HTTP API), [`spec/take-format.md`](spec/take-format.md) and
[`spec/take.schema.json`](spec/take.schema.json) (what a review saves), [`e2e/README.md`](e2e/README.md) (what each
end-to-end test proves), [`docs/roadmap.md`](docs/roadmap.md) (the plan).
