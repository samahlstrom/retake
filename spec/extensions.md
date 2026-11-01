# Extensions: tools on top of Retake (`retake-ext/1`)

This is the contract for adding tools to Retake without changing it. Change it here first.

Retake's core is review, versions and the agent loop. An **extension** adds a tool around them: a section in the
home's left column, a new kind of version, UI in named places of the page, files it owns in a video's folder, and
instructions and tools for the agent. It may run a small server of its own. Extensions live outside the install, so a
new `retake` keeps them, and they build against a versioned API with the same promise as themes (spec/theme.md).

Ignition (spec/ignition.md) is the first one. Transcript editing, voice-over and a project board are the kind of
tool that comes next.

## An extension

A folder:

```
ignition/
  extension.toml        the manifest (below)
  ui/                   what the page loads: one ES module, optional CSS, icons, fonts, wasm
  agent/                Markdown for the agent's prompt
  bin/                  optional: its tools and server program
```

### extension.toml

```toml
api = 1                          # the extension API major version it is written for
id = "ignition"                  # ^[a-z][a-z0-9-]{0,31}$, unique
name = "Ignition"
version = "1.0.0"                # the extension's own version (semver)
description = "Talk out an idea, shape it into a script, and start the video."
min_retake = "0.9.0"             # optional

[ui]
module = "ui/ignition.js"        # an ES module that exports one function per slot it fills
style = "ui/ignition.css"        # optional

[nav]                            # optional: a section in the home's left column
title = "Ignition"
icon = "ui/spark.svg"            # optional
list = "kind-only"               # optional: list the videos whose only versions are this extension's kind
action = { title = "New idea", route = "new" }   # optional: a + button on the section's row

[[kinds]]                        # optional: a kind of version
id = "doc"                       # its full id is <ext>.<kind>: ignition.doc
folder = "v0"                    # the version folder it claims; API 1 allows only v0
marker = "idea.md"               # the folder is this kind only when this file is in it
label = "Script"                 # how the version strip and picker name it: "v0 · Script"
status = "Idea"                  # the home's chip for a video that has only this version
record = true                    # Record and R on this version record audio for the extension

[[slots]]                        # where its UI mounts (below)
slot = "version"
kind = "doc"

[[slots]]
slot = "sidebar-tab"
title = "Script"
when = "has-kind"                # always (default) or has-kind: only on videos that have a version of its kind

[files]                          # optional: the files it owns in a video's folder
owns = ["v0/**"]
read = "watch"                   # the least role to read them
write = "record"                 # ... to write them
create = "owner"                 # ... to make a new video folder with them

[agent]                          # optional
instructions = "agent/task.md"   # for a task that targets this extension (≤ 16 KiB)
context = "agent/context.md"     # for every task of a video that has its files (≤ 4 KiB)

[[agent.tools]]                  # optional
name = "beats"
description = "Words and estimated seconds per ## section of a script"
exec = ["./bin/beats"]
input_schema = { type = "object", properties = { file = { type = "string" } } }

[server]                         # optional
command = ["./bin/server"]
runs_on = "machine"              # machine (the editing computer, default) or host
needs = "watch"                  # the least role for any request to it
```

Every table is optional except the top keys. An unknown key is a warning and is ignored (a newer manifest still
loads). A bad value, a missing file it names, or a manifest that doesn't parse disables that extension, with a
warning; the server never refuses to start over one. Roles are spec/team.md's: `watch` (any role), `record`
(reviewer and up), `approve`, `owner`.

## Where extensions are found

The first folder that has an id wins; `retake ext list` shows the others as shadowed.

1. `--ext DIR` (repeatable) on `retake open`, `share`, `serve` and `connect`, then `RETAKE_EXT_PATH` (`:`-separated).
   For developing one. Each DIR is an extension folder or a folder of them.
2. The project's: `<opened folder>/.retake/extensions/<id>/`.
3. The user's: `$XDG_CONFIG_HOME/retake/extensions/<id>/` (`~/.config/retake/extensions/`, next to the theme).
   `retake serve` uses `<data dir>/extensions/<id>/` (`/data/extensions/` in the Docker image).
4. Bundled: `extensions/<id>/` in this repo, built into the binary. Ignition is one.

Being found never turns one on.

## Turning them on

- **Local** (`retake open`, `share`, `connect`): the user config lists them,

  ```toml
  [extensions]
  enabled = ["ignition"]

  [extensions.ignition]     # optional settings: given to its server and tools, never to the page
  ```

  `retake ext enable <id>` and `disable <id>` edit that one key. `--ext DIR` turns on what it names for that run.
- **A project's extension** is code that came with a folder, so enabling it also pins its content hash in the
  config (`[extensions.pins] <id> = "b3:…"`). When its files change it is off, with the warning
  `<id> changed since you enabled it: run retake ext enable <id> again`. A host never loads a project's extension.
- **Hosted** (`retake serve`): only what the admin allows, `--extensions ignition,…`, `RETAKE_EXTENSIONS` or
  `[server] extensions = [...]`, from the server's own install (bundled or `<data dir>/extensions/`). The page is
  never given code a connected computer sent. An allowed extension is on for every user, and each user can turn it off
  for themselves in the account menu (`PUT /api/me/extensions` `{"ignition": false}`, kept in their account).
- **The editing computer** in hosted mode uses its own enabled list for the parts that run there: its kind detection,
  agent instructions and tools, and a `runs_on = "machine"` server. Its worker reports them in each poll
  (`extensions: [{id, version, api}]`). An extension that the host allows but the computer doesn't have still shows
  its UI, and what needs the computer says so in one line: "The editing computer doesn't have Ignition. Run
  `retake ext enable ignition` there." A host and a computer must agree on the extension's major version.

## Contribution points

### Nav section

`[nav]` adds one section to the home's left column, under the folder tree: a header row with the icon and title that
opens the extension's `page` slot, and the optional `action` as a + button on that row. With `list = "kind-only"`, the
section's rows are the videos whose only versions are of the extension's kind (Ignition's ideas), newest change
first; a row opens that video's page. Rows have the tree's height, and the icon sits where the tree's status dot is,
so nothing moves. The section shows on a phone in the same drawer.

The section's URL is the home's hash `#ext/<id>/<route>` (the folder hash starts with `#/`, so the two never meet).
`action.route` opens `#ext/<id>/<route>`.

### Version kinds

A kind is a version that isn't a video: a folder named `folder` inside a video's folder that holds `marker`.

- **Found by the scan** (spec/home.md): a video's folder is also any folder whose `v0/` holds an enabled kind's
  marker, with or without a video version. A folder with only `v0/` is a video with one version (spec/ignition.md's
  ideas). A `v0/` that holds a versioned video file (`clip_v0.mp4`) is a video version, as today; the kind never claims
  it. Two enabled kinds claiming `v0` is a load error for the second (by id).
- **In the API**: `api/info` lists it like any version, `{"label": "v0", "kind": "ignition.doc", "path": "v0",
  "name": "v0", "duration": null}`; `GET /api/home` gives a video whose only versions are of a kind
  `"status": "no_cut"` and `"kind": "ignition.doc"`, and the page shows the kind's `status` on its chip. A connected
  computer sends it in `POST /api/worker/files` as `{"label": "v0", "kind": "ignition.doc", "name": "v0"}`.
- **Numbering** is unchanged: `v0` comes before every cut, the next cut of a video that has only `v0` is `v1`, and
  following latest, comparing (C) and the push rule never pick `v0`.
- **The page**: the version strip and the version picker list it first, named `v0 · <label>`. Choosing it shows the
  extension's `version` slot where the video is. The bar keeps the version picker, Record (when `record = true`),
  Send and the agent's status; the timeline and pen colours aren't there, because there is no video to draw on.
- **Recording** on it (`record = true`): R and Record work as on a video (R when no text field has focus), REC and the
  level show the same way, the pen is off. Core records the audio and hands it to the slot (`ctx.onRecorded`); it
  makes no take. Without `record`, Record is hidden on that version.
- **Comments on it** are typed, through the extension (`ctx.comments.add`), not recorded takes ("Comments and
  questions on an extension's version" below).

### UI slots

| Slot | Where | Mounted when |
|---|---|---|
| `page` | The home's main area, in place of the video cards | `#ext/<id>/…` is opened (its nav row or action) |
| `version` | The review page, in place of the video, for a version of its `kind` | That version is shown |
| `sidebar-tab` | A tab of the comments column, after To do · Done | The tab is chosen |
| `review-panel` | A wide panel in place of the comments column (up to half the page), opened from the same tab row | It is chosen |

- `sidebar-tab` and `review-panel` show on every video version (or only with `when = "has-kind"`). They never cover
  the video or the bar. Pressing R on a video closes a review panel and shows To do, so new comments show.
- On a phone, `page` and `version` fill the page, `sidebar-tab` is a tab of the comments sheet, and `review-panel` is
  not shown in API 1.
- `slot` names are part of the API; an unknown one is a warning.

### Loading and mounting

- `GET api/ext` (at the home and under a video, needs watch) lists the enabled extensions for this person: `{"api": 1,
  "extensions": [{id, name, version, base, module, style, nav, kinds, slots, files, server, machine}]}` (`base` is
  `ext/<id>/`; `machine` is false when the video's editing computer lacks it).
- The page imports `ext/<id>/<module>?v=<version>` the first time one of its slots is shown, and never before. The
  module exports one function per slot it fills: `page`, `version`, `sidebarTab`, `reviewPanel`. Each is
  `(el: HTMLElement, ctx: Ctx) => void | (() => void)`, and the function it returns unmounts it.
- `el` is an empty `<div data-ext="<id>" data-slot="<slot>" data-part="ext-slot">` that core owns and empties after
  unmounting. No shadow DOM: tokens and the user's theme reach it.
- **CSS.** The page links `ext/styles.css` after the built-in CSS and before the theme. It is
  `@layer retake, ext;` and one `@import url("<id>/<style>?v=<version>") layer(ext.<id>);` per extension. So an
  extension's rules beat the built-in ones and the user's theme beats both. Its selectors start with
  `[data-ext='<id>']`, and its colours and fonts are Theme API tokens (`var(--surface-raised)`, `var(--font-ui)`);
  `retake ext check` reports both. Its own parts are `data-part="<id>-<name>"`, so a theme can style them.
- **A failure stays in its slot.** A module that doesn't load or a mount that throws shows one quiet line in the slot
  (`Ignition didn't load: <message>`) and is logged once to the console; everything else works.
- **Its files.** `GET ext/<id>/<path>` serves files under its `ui/` folder only: `.js .mjs` (`text/javascript`),
  `.css`, `.json`, `.wasm`, `.svg`, the fonts and images of the theme's table, never `.html`. Up to 10 MB a file,
  `X-Content-Type-Options: nosniff`, `Cache-Control: no-cache`, no hidden segments, no `..`, no symlinks out of the
  folder. They need what the page's own files need. The same files answer at `/ext/…` and `/p/<name>/ext/…`.

### The page API

`ctx`, given to every mount. Its TypeScript types ship as `retake-ext.d.ts` (`retake ext init` writes it).

```ts
interface Ctx {
  api: 1;
  slot: 'page' | 'version' | 'sidebar-tab' | 'review-panel';
  ext: { id: string; name: string; version: string; base: string };
  role: 'viewer' | 'reviewer' | 'approver' | 'owner';
  route: string;                                       // page: the hash after #ext/<id>/; '' elsewhere
  onRoute(cb: (route: string) => void): () => void;
  video?: { name: string; title: string; path: string; project: string };   // under a video
  version?: { label: string; kind?: string };                               // under a video
  home(): Promise<Home>;                               // GET api/home: the videos and the folder tree, read-only
  files(video?: string): Files;                        // its owned files in a video's folder (default: this one)
  comments(video?: string): Comments;                  // comments on this video's versions
  createVideo(v: { project: string; name: string; files: Record<string, string> }): Promise<{ name: string; url: string }>;
  transcribe(path: string, video?: string): Promise<{ text: string; words: Word[] }>;
  onRecorded(cb: (a: { blob: Blob; type: string; duration: number; started: string }) => void): () => void;
  player?: Player;                                     // sidebar-tab and review-panel on a video version
  on(event: 'notes' | 'job' | 'version' | 'automation' | 'files', cb: (data: unknown) => void): () => void;
  navigate(to: { video?: string; version?: string; t?: number; route?: string }): void;
  toast(text: string): void;
  fetch(path: string, init?: RequestInit): Promise<Response>;   // its server: ext/<id>/api/<path>
}
interface Files {
  list(glob?: string): Promise<FileInfo[]>;            // FileInfo: {path, size, modified, sha256}
  read(path: string): Promise<{ sha256: string; text(): Promise<string>; blob(): Promise<Blob> }>;
  write(path: string, data: string | Blob, o?: { ifMatch?: string }): Promise<FileInfo>;  // rejects {changed, sha256}
  remove(path: string, o?: { ifMatch?: string }): Promise<void>;
  url(path: string): string;                           // for <img>, <audio>
  watch(cb: (changed: FileInfo[]) => void): () => void;
}
interface Comments {
  list(version?: string): Promise<Item[]>;             // api/notes items (spec/notes.md)
  add(c: { text: string; anchor?: Anchor; wants?: 'version' }): Promise<Item>;   // on this extension's version
  send(ids?: number[]): Promise<void>;                 // POST api/automation/tasks
  answer(question: number, text: string): Promise<void>;
}
interface Anchor { file: string; line: number; heading?: string; quote?: string }
interface Player { time(): number; duration(): number; seek(t: number): void; onTime(cb: (t: number) => void): () => void }
```

- Everything goes through the HTTP routes below, as the person viewing, so their role applies.
- There is nothing for the pen, strokes, takes, the recording state or the keys of a video. `player.seek` is ignored
  while recording.

### Files it owns

`owns` lists globs relative to a video's folder (`v0/**`, `vo/*.wav`). They may not reach `.retake/`, a version
folder other than its kind's own (`v1/`, `v2/`, …), or a family video file outside it; `retake ext check` refuses
those. The files are the truth: plain files on the editing computer, editable in any editor and kept in git.

All under a video (`/p/<name>/…`), relative to its folder:

| Method | Path | Needs | Does |
|---|---|---|---|
| GET | `api/ext/<id>/files[?glob=]` | `read` | `{"files": [{path, size, modified, sha256}]}`, its owned files |
| GET | `api/ext/<id>/files/<path>` | `read` | The bytes, `ETag: "<sha256>"`, ranges, content type by extension |
| PUT | `api/ext/<id>/files/<path>` | `write` | Writes it (folders made, temp file and rename). `If-Match: "<sha256>"` (or `*` for new) answers 409 `{"error": "changed", "sha256"}` when it changed. 25 MiB at most. 200 with its FileInfo |
| DELETE | `api/ext/<id>/files/<path>` | `write` | Removes it (`If-Match` as above). 204 |
| POST | `api/ext/<id>/watch?viewer=<id>` | `read` | A lease of 30 s (the page renews it every 10 s): while one is held its files are compared every 2 s and changes come as the `ext.files` event `{ext, files: [FileInfo + deleted?]}` |
| POST | `api/ext/<id>/transcribe` `{"path"}` | `write` | 202 `{"job"}`: transcribes an owned audio file with the editing computer's `[transcribe]`, writes `<path without extension>.words.json` (`{model, words: [{w, s, e, p}]}`) and sends a `job` event `{id, kind: "transcribe-file", state}` |
| POST | `/api/ext/<id>/videos` (the home) `{project, name, files}` | `create` | Makes `<project>/<name>/` with the given owned files (UTF-8, 1 MiB in all) and answers 201 `{name, url}`; 409 when the folder exists |

- Paths use `/`, are owned, have no `..` or hidden segments, and resolve inside the video's folder (`os.Root`); a
  symlink out of it is 404. Writes, deletes and the videos route are origin-checked like every POST.
- **Hosted.** The files are on the editing computer. The server asks the video's worker with the media relay
  (spec/automation.md), which gains ops: `ext.list`, `ext.read` (8 MiB chunks, as media), `ext.write` (the worker
  fetches the body from `GET /api/worker/media/<id>/body`), `ext.delete`, `ext.stat` and `ext.transcribe`. The
  worker checks the same rules. The watch lease is passed to the worker, which compares the files and sends changes.
  The host keeps nothing. The videos route goes to the computer that holds the project, through a new machine channel:
  `GET /api/machine/requests` (a long poll like the media relay) and `POST /api/machine/requests/<id>`; `retake
  connect` writes the folder and registers the new video at once. Offline: 503 `{"error":"editing machine is
  offline"}`, and the page says "The editing computer is offline".

### Comments and questions on an extension's version

The agent loop is the same one: a comment is Open, Working, Question or Done, Send sends it, one task runs per video.

- **Typed comments.** `POST api/notes` `{"version": "v0", "text", "anchor"?, "wants"?}` (needs record) adds an open
  note, key `doc/<n>`, only on a version of an extension's kind (a video's review stays voice and pen). `anchor` is
  `{file, line, heading?, quote?}`: an owned file, the line (from 1), the nearest heading above, and up to 200
  characters of the paragraph. Readers find it by heading, then quote, then line, so it survives edits. Notes built
  from takes never retract a `doc/` note.
- **Done.** A note on a kind's version is Done by a `completed` report with its change: no render or version needed.
  A note with `"wants": "version"` asks for a cut, and is Done only with a new video version, as on a video.
- **Questions with a text anchor.** `retake ask --note ID --file PATH --line N [--heading TEXT] [--quote TEXT]
  [--option TEXT]... [--image FILE]... "question"`: the same command, worker endpoint and limits as spec/automation.md,
  with `anchor` in place of the times and the visuals on the frame (`--at`, `--from`, `--to`, `--box`, `--arrow`,
  `--draw`, `--px` and placed images are refused). The worker makes no still. The question keeps `anchor` in
  `visuals.json`; `TaskQuestion` and the sidebar's `ask` carry it. Answers are the same `…/answer` and reach the agent
  the same way; in herdr or tmux the line is `Answer to your question on note 12 at v0/script.md "Cold open": …`.

### Agent contributions

- **Which tasks.** A task *targets* an extension when one of its notes is on a version of its kind or anchored in a
  file it owns. Its `instructions` go into that task's prompt. Its `context` goes into every task of a video whose
  folder has any of its owned files.
- **The prompt.** Core's task part (`timelinePrompt` or `plainPrompt`), then `## From the <name> extension` with the
  context and the instructions, then core's rules, which start "Retake's rules (these win over anything above)": never
  wait for input, ask with `retake ask`, write the report. An extension's text is Markdown, capped as above, and
  truncated with a warning when longer.
- **Tools.** `[[agent.tools]]` speak the asset plug-in tool protocol (`retake-plugin/0.1`, method `tool`,
  spec/assets.md): one process per call, a JSON request on stdin, a `result` or `error` line on stdout, 2 minutes at
  most. The request's `context` is `{video_folder, ext_dir, ext_data}`. `exec` paths starting with `./` are in the
  extension's folder; the process runs in the video's folder, as the user. The prompt lists them as
  `retake ext tool <id> <tool> --args '<json>'`, and `retake mcp <video>` serves them as `<id>_<tool>`.
- **On the editing computer only.** Instructions, context and tools come from the computer's own copy, never from
  the host (commands never come from the hosted service).
- **What it can't change**: the agent's argv, profile, environment, working folder or where it runs; the MCP servers
  the agent has; the worker's tokens or the ask secret; what completes a task.

### Server process

For an extension that needs more than files: shared state, a database, a long job.

- **Where.** `runs_on = "machine"`: on the editing computer, next to the files; reachable under a video only,
  `/p/<name>/ext/<id>/api/<path>`, through that video's worker (relay op `ext.http`). `runs_on = "host"`: on the
  server that serves the page, at `/ext/<id>/api/<path>` and under every video. In local mode both are the one
  `retake` process.
- **Started** on the first request to it, in the extension's folder, with `RETAKE_EXT_ADDR=127.0.0.1:<port>` (core
  picks the port), `RETAKE_EXT_SECRET` (32 random hex per start), `RETAKE_EXT_DIR`, `RETAKE_EXT_DATA`
  (`$XDG_DATA_HOME/retake/ext-data/<id>/`, or `<data dir>/ext-data/<id>/`), `RETAKE_EXT_SETTINGS` (its config table
  as JSON) and the user's environment. It must answer `GET /healthz` within 10 s. Its output goes to
  `$XDG_STATE_HOME/retake/ext/<id>.log`.
- **Stopped** after 10 minutes without a request, and when retake exits (SIGTERM, then SIGKILL after 5 s). After a
  crash the next request starts it again; three crashes in a minute and it answers 503
  `{"error":"extension server failed"}` until retake restarts.
- **Authenticated by core.** Core signs the person in as for any route, checks `needs`, origin-checks state changes,
  strips `Cookie` and `Authorization`, and adds `X-Retake-Secret`, `X-Retake-Role`, `X-Retake-User` (the display
  name), `X-Retake-Video` and, on the machine, `X-Retake-Folder` (the video's folder). The server refuses any request
  without the secret. It sees no cookie or token.
- **Limits.** 8 MiB each way and 30 s to the first byte. Locally responses stream; through the hosted relay they come
  in 8 MiB chunks, and long-lived streams (SSE, WebSocket) aren't supported in API 1: use `ctx.on` or poll.

## Local and hosted

| Part | Local (`retake open`) | Hosted (`retake serve` + `retake connect`) |
|---|---|---|
| UI, nav, slots | the one process | the host's install, as the admin allows |
| Kind detection | the scan | the computer's scan, sent with its files |
| Owned files, watch, transcription | read and written on disk | on the computer, through the worker's relay |
| New video folder | written on disk | the machine channel to the computer that holds the project |
| Agent instructions and tools | the in-process worker | the computer's worker, its own copy |
| Server, `runs_on = "machine"` | the one process | on the computer, through the relay |
| Server, `runs_on = "host"` | the one process | on the host |

Anything that works locally works hosted, as the rest of Retake does.

## Trust: extensions are trusted code

An extension is a program you choose to run, like an agent or a component backend:

- Its server and tools run as you, with your files and network, unsandboxed.
- Its page code runs inside the Retake page with the viewer's session. It can call every API the viewer can, read
  everything the page shows, and change the page.
- On a hosted server, its page code runs in the browser of everyone who opens that server. That is why only the
  admin's own install is served, never code a connected computer or a project folder sent.

Install one only as you'd install any program.

**What isolation exists.** The files API only reaches owned paths, never footage, other versions or `.retake/`. Every
route keeps its role check, so an extension can't do what the viewer can't. Its server never sees cookies or tokens.
Machine tokens, the ask secret and the worker's credentials never reach page code. Its CSS is layered under the
theme and checked for scope. A failure stays in its slot. Its slots exist only where listed, never over the video.

**What doesn't.** There is no sandbox for page code: in API 1, nothing stops an extension's JavaScript from doing
what any script in the page could.

**In-page modules, not sandboxed frames.** A sandboxed iframe would wall page code off, but:

- the frame protects only the page, not the computer: the server and tools still run as you, so it buys little;
- the tool would then feel like an embed. Tokens and the user's theme would have to be copied in, layout and focus
  would cross a frame edge, R and the page's keys wouldn't reach it, and every call would be a `postMessage` RPC to
  version and keep in step;
- Sam's rule is no iframes in the product.

So slots are plain elements and the module runs in the page. A frame stays possible later, as an opt-in per extension
(`[ui] isolate = true`), without changing the `ctx` API.

## What an extension can never break

1. **The review.** Pen only, R to record, and the pen does nothing unless recording. Core's key handler stays the
   first listener (`onkeydowncapture` on the window, set before any module loads). It handles R, Space, Z, the pen
   keys, `>` and the rest wherever focus isn't in a text field, so a slot can't take them. No slot lies on the video,
   and `ctx` has nothing for the pen, takes or recording.
2. **The agent's permissions model.** Roles stay as they are: record to add comments, send and answer; owner to choose,
   switch and pause the agent. Tasks keep one queue per video, the lease and the completion rules. Agents never
   stop to ask. Core's rules close every prompt. Nothing changes an agent's argv or profile.
3. **The footage and the versions.** Nothing an extension writes is a version file or `.retake/`, and nothing it does
   deletes or renames a version.
4. **Starting and loading.** A broken extension is a warning line at startup and a quiet line in its slot. The server
   still starts, the page still loads, the video still plays. Slots load only when shown, so first play isn't slower.
5. **The look.** Built-in CSS stays in `@layer retake`, extensions in `@layer ext.<id>`, and the user's theme wins
   over both.

## Versioning

The extension API is: the manifest's keys and values, the slot names, `ctx`, the `api/ext…` and `ext/…` routes, the
`ext.files` event, the typed-comment and text-anchor fields, the prompt section, the tool protocol, and the server's
environment and headers. This is version 1. Every page carries `<meta name="retake-ext-api" content="1">`, and
`GET api/ext` says `"api": 1`.

The promise is the Theme API's:

- **Adding** a key, slot, `ctx` member, event or route can happen in any release.
- **Removing or renaming** one happens only in a major release (`retake` 2.0.0), and the old name keeps working for
  one major release. A renamed manifest key is read under both names. A removed `ctx` member stays as a shim that
  warns in the console. Both are listed in `web/src/ext/api.json` under `deprecated`, and `retake ext check` says what
  to change.
- Retake N loads extensions with `api = N` and `api = N - 1`. Others aren't loaded, with a warning:
  `hello 1.0.0 needs extension API 3; this retake has 2`. `min_retake` is refused with a warning the same way.
- The extension uses Theme API tokens and parts, under that API's promise.
- An update never touches an installed extension: it's outside the install. A bundled extension updates with
  Retake. To keep an older one, install a copy in the user's folder, which wins.

## Commands

```
retake ext list [--json]                      found extensions: id, version, where, on/off, shadowed, problems
retake ext enable <id> | disable <id>         edit [extensions] enabled (and pin a project's extension)
retake ext install <dir> [--force] [--data-dir DIR]   copy into the user's folder (or a server's data dir)
retake ext remove <id> [--data-dir DIR]
retake ext check [DIR|ID]... [--json]        none: every enabled extension
retake ext init <id> [DIR]                    a starter: extension.toml, ui/<id>.js, ui/<id>.css, retake-ext.d.ts
retake ext tool <id> <tool> [--args JSON]     run one of its agent tools (agents use this)
```

**`retake ext check`** reports, one line per finding, then `ext ok: ignition 1.0.0 (api 1)` or `N problems`:

- the manifest: parse errors, unknown or deprecated keys, bad values, an `api` this retake doesn't load, a bad `id`;
- files it names that are missing: module, style, icons, agent Markdown, tool and server `exec`;
- the module's exports against its slots (a static scan for `export function|const <slot>`);
- `owns` globs that reach `.retake/`, another version's folder or a family video file;
- CSS selectors not under `[data-ext='<id>']`, and tokens the Theme API doesn't have (the scanner of
  `retake theme check`);
- agent Markdown over its cap.

Exit 0 when nothing was found, 1 when something was, 2 for bad arguments. `--json` prints a list of `{"dir", "id",
"version", "api", "problems": [{"kind", "name", "message"}]}`.

## Tests

- Go: discovery order and shadowing; enabling, pins and a changed pin; hosted allow lists and per-user off; manifest
  validation and `ext check` (every finding, exit codes); the files routes (owned globs, traversal, hidden, symlinks,
  version folders, `.retake/`, If-Match, 25 MiB) locally and through the relay with a test worker; the machine
  channel; the watch lease and `ext.files`; transcription of a file with a fake command engine; typed comments,
  `doc/` keys surviving a notes sync, completion without a render, `wants: version`; `retake ask` with a text anchor
  (CLI, worker, server, herdr line); prompt assembly order and caps; tools over the CLI and MCP; the server process
  (lazy start, health, headers, secret, idle stop, crash limit, stripped cookies); kinds in the scan, `api/info`,
  `api/home` and `POST /api/worker/files`.
- Web unit: the loader, a module that fails to load or throws in mount, unmount on slot change, `ctx` routes, R and
  the review keys still reaching core with an extension's sidebar tab focused.
- e2e `e2e/tests/17-ext.spec.ts`: the hello extension of docs/extensions.md (nav section, page, sidebar tab, theme
  token, a broken copy that shows its error line while review keeps working). Ignition has its own
  (spec/ignition.md).
