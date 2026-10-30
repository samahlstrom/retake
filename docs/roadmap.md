# Roadmap

Design sketch (private): https://claude.ai/artifact/5zwmQevPavMzr7RcPEfpkK

## Decisions so far
- **Solo first, team later.** No real-time co-editing.
  - Each person's comments are their own append-only takes, merged by timestamp.
  - The agent is the only writer of the timeline.
  - People who want to review together use a video call. No follow-me mode.
- **The review side never renders.** A version is any video file, or later a timeline revision previewed from the
  editor's cache.
- **Comments go in a sidebar,** not on the timeline.
  - The newest version shows only the agent's questions.
  - Flipping back to an older version shows its notes as they were resolved (✓ plus a one-line change, or ? plus the
    question).
  - A push is rejected unless every open note is answered or has a question.
- **Images.** While recording, the reviewer can drag or paste images onto the video. They're pinned to that moment and
  that spot, and stored as review-only assets.
- **Stack.** Go for the server (one binary with the page embedded). Svelte 5 + TypeScript + Bits UI for the page.
  Rust for the future editor engine.
- **The player plays chunked playlists (`.m3u8`) as well as single files,** so the editor's cached draft segments can
  stream later.

## Milestones
1. **Local player at parity.**
   - The Go server and the Svelte page match the legacy player.
   - Same take format, so `review notes` still reads it.
   - Audio chunks are buffered in IndexedDB until the server confirms them.
   - The player can play `.m3u8` versions.
   - Playwright end-to-end test on generated media. CI workflow.
2. **Notes, agent API, questions.**
   - Port `notes.py` to Go, with golden tests against the Python output.
   - whisper.cpp transcription.
   - CLI with `--json`, plus an MCP server.
   - Note states, the sidebar, image drops, and `retake watch`.
3. **Sharing and team mode.** `retake share` over an HTTPS tunnel, accounts and roles, Postgres + S3, remote MCP,
   Docker Compose.
4. **Release.** Installers, docs, a CC0 sample project, then the hosted service.

## Editor track (after milestone 2)
1. Timeline schema and anchor resolver.
2. Shot renderer with a content-addressed cache and render manifest.
3. Review bridge.
4. Asset catalog.
5. Graphics renderer.

Later: a shared library of graphics, styles and edit recipes.

## Open decisions (owner)
- The name. "retake" is a working name.
- License. AGPL-3.0 for the server and Apache-2.0 for the client is the recommendation, not decided.
- Graphics: native Skia from data plus JS expressions, or TS web pages in headless Chrome.
- Pilot bar: is a close per-frame match to the v7 preview enough?
- C flip: the matching moment, or the same clock time?
- License gate: block publishing on review-only assets, or warn?
