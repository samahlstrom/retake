# Extensions: where they hook into today's code (build notes)

For the build of spec/extensions.md and spec/ignition.md. Line numbers are at `1c5bc34` (branch `simple`). The theme
branch moves the CSS and the page's `<head>`. Delete this file once the build is merged.

## New

- `internal/ext/`: manifest parsing and validation, discovery order, enabled list and pins, `check` (reusing the token
  scanner of `internal/theme/check.go`), the ui file server, `ext/styles.css`, the server-process supervisor and
  proxy, and tool calls.
- `internal/cli/ext.go`: `retake ext list|enable|disable|install|remove|check|init|tool`, plus `--ext DIR` on open,
  share, serve and connect.
- `web/src/ext/`: the loader (`api/ext`, `import()`), `ExtSlot.svelte` (mount, unmount, error line, `data-ext`,
  `data-slot`), `ctx`, `api.json` and `retake-ext.d.ts`.
- `extensions/ignition/`: manifest, Svelte UI built by `make build` into `ui/`, `agent/*.md`, and
  `extensions/embed.go` to bundle it.

## Core changes, by contribution point

| Point | Hook | Change |
|---|---|---|
| Config | `internal/config/config.go` `Config` (l.18) | `[extensions] enabled`, `[extensions.<id>]`, `[extensions.pins]`; `[server] extensions` |
| CLI | `internal/cli/cli.go` `Main` switch (l.186–238), `usage` (l.71) | `case "ext"` |
| Routes, project | `internal/server/server.go` `ServeHTTP` method switch (l.450), `get()` (l.496, before the `static` fallback), `post()` (l.646) | `ext/…`, `api/ext…`, `ext/<id>/api/…`; add PUT and DELETE (origin-checked like POST); `POST api/notes` |
| Routes, roles | `internal/server/auth.go` `permFor` (l.98) | needs for the new routes, from the manifest's `files` and `server.needs` |
| Routes, hosted | `internal/hub/hub.go` `ServeHTTP` (l.295–347) | root `/ext/…`, `/api/ext…`, `PUT /api/me/extensions` |
| Routes, local home | `internal/hub/folder.go` `ServeHTTP` (l.388–450, before `static` l.443) | the same at the home |
| Page head | theme branch `internal/theme/page.go` | link `ext/styles.css` between built-in CSS and theme; `<meta name="retake-ext-api">` |
| Kinds: scan | `internal/home/discover.go` `anchor` (l.188), `walk` (l.261), `inner` (l.280) | a folder whose `v0/` holds an enabled marker is a video, even with no video file |
| Kinds: family | `internal/family/family.go` `Version` (l.79), `Detect` (l.142), `detect` (l.209) | `Kind`; add `v0` (no probe, no duration) |
| Kinds: home | `internal/home/home.go` `StatusOf` (l.32), `Card` (l.50) | `no_cut` and `kind` |
| Kinds: hosted page | `internal/hub/projects.go` l.221–232 | a v0-only project must not answer `noVersions` |
| Kinds: api/info | `internal/server/server.go` `info` (l.577) | v0 with `kind` |
| Kinds: hosted files | `internal/worker/plain.go` `onDisk` (l.25), `fileVersions` (l.51); `project.FileVersion` | send `{label: "v0", kind, name: "v0"}` |
| Kinds: connect | `internal/cli/connect.go` `connectProjects` (l.252) | register v0-only folders |
| Files, hosted | `internal/relay/relay.go` `Request` (l.51), `ValidFile` (l.32); `internal/worker/media.go` `readMedia` (l.36), `mediaLoop` (l.170); `internal/server/automation.go` `workerRoute` (l.141) | `Op` field and the `ext.*` ops, owned-path rules, `GET /api/worker/media/<id>/body` for writes |
| Files, watch | `internal/worker/plain.go` `filesLoop` (l.88) pattern; `internal/project/events.go` (l.15–17) | the lease, 2 s compare, `ext.files` event |
| Machine channel | `internal/hub/connect.go` `machineRoute` (l.105); `internal/cli/connect.go` `connector.run` (l.284), `register` (l.365) | `GET/POST /api/machine/requests`; create, then register at once |
| Typed comments | `internal/project/notes.go` `Sync` (l.302), `syncNote` (l.383, the insert at l.397) | `doc/<n>` notes with `anchor`, `wants` in `detail`; `Sync` never retracts `doc/` keys |
| Completion | `internal/project/automation.go` `ReportWork` (l.784); `internal/project/ask.go` `held` (l.564) | doc notes are Done without a render or version; `wants: version` needs one |
| Worker, doc tasks | `internal/worker/agent.go` `execute` (l.70; the render wait ~l.175) | no render wait or push for doc-only tasks |
| Worker, Start v1 | `internal/worker/agent.go` l.88–92 ("no version that plays"); `internal/agent/push.go` `NextVersionFile` (l.166) | a v0-only video's next file is `v1/<folder>_v1.mp4` |
| Prompt | `internal/worker/agent.go` l.88–121 (`timelinePrompt`, `plainPrompt` plain.go l.144), `askRules` (ask.go l.292) | ext context and instructions between the task part and core's rules |
| Worker gating | the poll body (`internal/worker/worker.go` `controlLoop` l.256) | `extensions: [{id, version, api}]`; a blocked line when one is missing |
| Ask anchor | `internal/cli/ask.go` `taskAsk` (l.26), `request` (l.110); `internal/project/ask.go` `WorkAsk` (l.94), `TaskQuestion` (l.118), `AskInTask` (l.415); `internal/worker/ask.go` `serveAsk` (l.131), `frameOf` (l.363), `answerLine` (l.267); `internal/server/ask.go` `attachAsks` (l.30); `internal/project/view.go` `ItemAsk` (l.40) | `anchor`; skip the still and the time map |
| Transcribe a file | `internal/transcribe` `Transcriber`; the jobs | job kind `transcribe-file`, run where the file is |
| Tools | `internal/assets/plugin.go` `run` (l.274); `internal/mcp/mcp.go` `NewServerWith` (l.150) | reuse the tool protocol; `<id>_<tool>` MCP tools |
| Accounts | `internal/accounts` | a per-user list of extensions turned off |

## Page

| Point | Hook | Change |
|---|---|---|
| Version slot | `web/src/App.svelte` (l.23–29) | `<ExtSlot slot="version">` in place of `Stage` when the shown version has a `kind` |
| Version switch | `web/src/lib/review.svelte.ts` `showVersion` (l.1785), `start` (l.2395) | no `#source.load` for a kind; following latest and flip skip it |
| Version type | `web/src/lib/types.ts` `Version` (l.9) | `kind?` |
| Strip and picker | `web/src/lib/notes/view.ts` `versionStrip` (l.244), `VersionEntry` (l.229); `VersionPicker.svelte` | `v0 · <label>` first |
| Bar | `web/src/components/Bar.svelte` (l.23–29), `PhoneControls.svelte` | on a kind: no timeline or pens; Record only with `record` |
| Recording | `review.svelte.ts` `startRec` (l.817), `toggleRec` (l.933) | on a kind: audio only, no take, Blob to the slot |
| Keys | `review.svelte.ts` `onKeydown` (l.2246), `App.svelte` l.20 | stays first; a test that it still is with a slot mounted |
| Sidebar tabs | `web/src/components/sidebar/Sidebar.svelte` tablist (l.133–146) | ext tabs and the review panel; R returns to To do |
| Events | `web/src/lib/notes/notes.svelte.ts` `connect` (l.160) | pass `ext.files` and the rest to `ctx.on` |
| Nav section | `web/src/home/FolderNav.svelte` (after `.rows`, l.239); `web/src/lib/tree.ts` `Row` (l.85), `parseHash` (l.63) | section rows; `#ext/<id>/…` |
| Page slot | `web/src/home/Home.svelte`, `home.svelte.ts` `refresh` (l.181) | `<ExtSlot slot="page">` in the main area for `#ext/…` |
| Deep link | the review page's start | `?v=<label>&t=<seconds>` for `ctx.navigate` (if not there yet) |

## Order

1. `internal/ext` + `retake ext` + `api/ext` + ui files + `ExtSlot` + nav section and page slot (the hello e2e).
2. Owned files, locally then through the relay, and the watch.
3. Version kinds end to end (scan, family, api, home, page, recording), typed comments, completion.
4. Prompt sections, text-anchor questions, tools.
5. Ignition.
6. Machine channel (New idea hosted), then the server process (Ignition doesn't need it).
