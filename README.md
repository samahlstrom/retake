# Retake (working name)

Review a video by talking over it. Press **R**, say what you think, draw on the frame with a pen, press **R** again.
Your notes are pinned to the exact moment. An AI agent turns them into the next version, and you flip between old and
new with **C**.

> Status: early development, milestone 1 (the local player). Not released, and the license isn't decided yet.

## Quickstart (development)

```bash
mise install            # Go, node, golangci-lint at the pinned versions
make build              # builds the page and the Go binary into bin/retake
bin/retake open path/to/clip_v2.mp4
```

Docs: [`spec/api.md`](spec/api.md) (HTTP API), [`spec/take-format.md`](spec/take-format.md) (what a review saves),
[`docs/roadmap.md`](docs/roadmap.md) (the plan).
