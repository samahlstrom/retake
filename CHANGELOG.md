# Changelog

All notable changes are listed here. Versions follow semver once the first release ships.

## Unreleased

### Edit a comment's words while it's still to do
- ⋯ → **Edit**, or a double-click on a comment's words: they become a field in their own place (same font and size,
  nothing moves), with Cancel and Save where the ⋯ was. Enter saves, Shift+Enter is a new line, Esc cancels. On a
  phone, a tap on the words (or ⋯ → Edit) opens compose mode with the words in the comment box, on the keyboard. Works
  for voice comments (their transcribed words), typed ones and drawing-only ones; the drawing, time, audio and author
  stay. No badge afterwards, only an `Edited` tooltip (spec/notes.md, "Editing a comment").
- Open comments only, including ones sent in a batch the agent hasn't reached yet; hidden once the agent marks one
  working or done. The author, approvers and the owner may edit.
- `POST /api/notes/<id>/edit {text}` (origin-checked, idempotent, a `notes` event). A notes rebuild (the take's final
  transcription, an engine change) never overwrites edited words (`note_edits`, store migration 8).
- The agent reads the new words: a queued task at pick-up, and a running batch as one more round (a `Changed comments`
  message in herdr or tmux, a follow-up round in the background; spec/automation.md, "Words changed while they wait").

### Download a version for YouTube
- The version menu ends with **Download v5 (MP4)** for the version on screen, on a computer and a phone (with a single
  version the menu shows just for this). The file is ready to drag into YouTube Studio: MP4 with faststart, H.264 High
  `yuv420p` tagged BT.709, AAC-LC 48 kHz stereo at -14 LUFS (true peak at most -1 dBTP), at the version's size and
  frame rate, named `<video>_v5.mp4` (spec/api.md, "Download").
- A version file that is all that already downloads as it is. Any other is made once with ffmpeg into
  `.retake/downloads/` (H.264 High copied, with its colour tags fixed; audio levelled with two-pass loudnorm) and
  reused. A rendered version downloads the renderer's export (`<stem>.export.mp4`), packaged from the cached units with
  the new `retake-edit render --export-loudness -14` unless the timeline's master sets a level; the export's MP4 now
  carries the full BT.709 colour description.
- While it gets ready the entry shows `Checking…` or a percentage, then the download starts by itself; a failure is
  one plain line under it, and so is a license note ("Has 2 assets marked review only"), which never blocks it.
- `GET/POST /api/download?v=<label>` and `GET /video/<label>/download` (attachment, ranges, Content-Length). Hosted, the
  computer that has the version makes it and the file comes through the relay in 8 MiB chunks, cached on the server.
- Anyone who can watch may download; `[server] downloads = "reviewers"` or `"off"` (or `RETAKE_DOWNLOADS`) narrows it.

### Chat with the agent on a script
- On a script, the column beside the document (the sheet on a phone) is a chat with the agent in place of the
  comments (spec/chat.md): ask it, and it edits the document while you watch, pulls in links and images, and asks with
  choices to tap (pictures side by side, stacked on a phone). Its tool use is one quiet line each; no raw JSON. R or
  the mic sends a voice message (transcribed); pasted, dropped or attached images go in `v0/images/`. Stop stops the
  reply; messages sent meanwhile wait for the next turn. ⋯ → Undo the agent's edit undoes a reply's edit.
- The agent runs on the editing computer (the in-process worker locally, the connected one hosted), one turn per
  message: Claude Code streams its words and resumes its session (`--resume`), Codex resumes its thread, other agents
  get the recent chat. A turn never runs beside an editing task: "The agent is busy with v1's edits".
- **Send on a script makes the video**: with no cut it builds v1 from the script ("Building v1…"); once cuts exist it
  brings the next version in line with the script's changes, by the batch rule. With nothing changed since the last
  Send it is dimmed and says so. ⋯ → Start v1 is gone. On a phone the document doesn't raise the keyboard until ⋯ →
  Type in the script.

### Ignition is now Scripts
- The extension that turns an idea into a script and a first cut is called **Scripts** (id `scripts`,
  `extensions/scripts/`, spec/scripts.md), with a page icon in the nav and "New script" on its +. "Draft" stays the
  agent's unfinished version (`v5-draft2`). The name the page shows is the manifest's `name` and `[nav] title` only.
- The old id keeps working: `[extensions] enabled = ["ignition"]`, `[extensions.ignition]`, `retake ext enable
  ignition`, `--extensions ignition`, a person's `{"ignition": false}`, and a computer still on an older retake that
  reports `ignition` and `ignition.doc` (spec/extensions.md, "Renamed ids"). Nothing on disk moves.
- Settings at the bottom of the folder column has a gear for its icon (it was a sun).

### Embeds in a script
- A web address alone on its own line in a script's document shows what it points to (spec/scripts.md, "Embeds"):
  YouTube (watch, youtu.be, Shorts, a start time), Vimeo, Loom, Twitch clips and videos, X posts, Instagram posts and
  reels, TikTok, Spotify, SoundCloud, Figma and Google Docs, Slides and Sheets play in a sandboxed frame on the site's
  own embed address; a raw `<iframe src="https://…">` line is rebuilt with only its address and size; any other page
  is a quiet card with its title, site and picture. The box is reserved before anything loads, nothing is wider than
  a phone, and on the caret's line the address shows as typed. A link inside a sentence stays a link. The agent is
  told to put a link alone on its line to embed it.
- `GET api/link-preview?url=` (spec/api.md, "Link preview"): the server reads a public page's `og:` tags and title
  for the cards, never a loopback, private, link-local or metadata address (checked at the connection, after DNS and
  on every redirect), in 5 s and 1 MiB at most, cached on disk. `ctx.linkPreview(url)` in the page API.
- A long script opens at its top again on a desktop (the opening focus no longer scrolls to the caret at the end).

### Phones, paused tasks and recordings
- Phone (portrait): the comments always fill the space between the controls and Record and Send, with To do · Done,
  the list (it scrolls inside) and the comment box, so nothing is left empty; the speech bubble goes to To do instead
  of hiding them. The hint line over Record is gone there (the empty list says how to comment). The comments list
  scrolls again wherever it is longer than its space (desktop too). ⋯ and a comment's time are 40 px tall on a phone.
  Portrait is compact so the list gets the height (three comments in view on an iPhone 15, the video's size as it
  was): the 40 px row of controls sits just under the timeline's track, the comments header is one 44 px row, the box
  and the cards are tighter.
- Paused tasks: an older version's one-task-per-note paused tasks become one paused batch per target when the store
  opens, and paused tasks of a target merge like queued ones. Send (or Retry) resumes that batch with the new comments
  added, run by one agent; the paused comments count in Send (spec/automation.md, "Send resumes a paused batch").
- Version numbers have at most 4 digits (`_v9999`): OBS recordings (`2026-10-17 21-30-05.mp4`) and Twitch downloads
  (`twitch_v2903906005_3480-4520.mp4`) are no longer shown as one video at a made-up version.

### Comment at any moment: draw, type or record
- The pen no longer needs recording. Draw on the video or type in the box at the bottom of the comments ("Comment at
  0:18 · goes into v8…"): the video pauses, the lines go with that comment, Enter saves it as a note with its time,
  its lines and a still of the frame (`POST api/notes` with `t`, `strokes` and `snapshot`; spec/notes.md, "Typed
  comments on a video"), Esc cancels it. Recording still adds your voice and pins what you draw to it; R still
  toggles it.
- The bar: play, time, timeline, version, one pen button (its colour; it opens the colours and the eraser, keys 1–4
  and E still work), Record as a microphone with the red dot (REC and the time while recording; nothing moves), Send
  and ?. Send says Send: its count is in its title, and it never names the agent. Hold it (or right-click it) to pick
  another of the computer's agents for that send (`agent` on `/done` and `POST /api/automation/tasks`). The agent
  pill and its menu are gone (Settings has them); where Send can't reach an agent, one quiet line says so.
- The comments header: a speech bubble, the version as a menu ("v7 → v8 · 3 to do", in place of the version strip
  and the "Open on earlier versions" line), search and hide; To do · Done as a toggle. A card says where its edit
  goes (→ v8), and Send's toast where the comments went. The hidden column's toggle is the speech bubble and the
  count, as on a phone.
- Theme parts `pen-button`, `agent-picker`, `send-notice`, `version-menu`, `version-menu-list` and `into`;
  `agent-status`, `agent-menu`, `version-strip` and `earlier-versions` stay on the new elements, deprecated.

### Settings on the home
- **Settings** at the bottom of the home's folder column (the owner's): the default agent and what happens when its
  usage runs out (moved here from the review page's agent menu, and applied to every project), where agents run,
  extensions on and off, the theme folder, and the computers ("Connect your computer" on the team server). Locally it
  writes `config.toml` (new keys `default_agent` and `fallback_agent`), keeping the rest of the file.
  `GET`/`POST api/settings` (spec/home.md, "Settings").

### C is back; the old project grouping is gone
- C flips to the comparison version at the same moment again, and C flips back; after a trim the time map lands it on
  the matching moment. The comments, markers and drawings switch with it. While you compare, following the agent's
  live drafts waits (nothing pulls the video away), and C back lands on the newest draft. A draft compares with the
  version it is an edit of. `[` and `]` stay gone: the version list and the comments' version strip switch versions.
- The project organization that only lived in Retake's database is removed: `POST /api/workspace`, `workspace` in
  `GET /api/home`, its table (store schema step 6 drops it, SQLite and Postgres) and the `project:`/`group:` share
  scopes. A link with such a scope answers 410 with a page that says why. Folders on disk organize videos.

### Extensions, and Scripts (extension API v1)
- An extension adds a tool around review without changing it (spec/extensions.md, docs/extensions.md): a section in
  the home's left column, a page, a tab next to the comments, its own kind of version (`v0`), files it owns in a
  video's folder, and instructions for the agent. Its page code is one ES module that mounts into named slots, styled
  with the Theme API's tokens in `@layer ext.<id>` (the user's theme still wins). A broken one shows one line in its
  own place; review keeps working.
- `retake ext list`, `enable <id>`, `disable <id>` and `check [DIR|ID]`; `--ext DIR` on `retake open`, `share` and
  `connect` turns one on for a run. `retake serve --extensions scripts` (or `RETAKE_EXTENSIONS`, `[server]
  extensions`) allows one for everyone on a server; each person can turn it off for themselves
  (`PUT /api/me/extensions`).
- **Scripts** (spec/scripts.md) comes with Retake, off until `retake ext enable scripts`. An idea is one Markdown
  document, `v0/<video>_v0.md` (named like `v1/<video>_v1.mp4`): New script makes it, you type or talk into it (R
  records; the words land as a plain paragraph and a comment asks the agent to work them in), Send has the agent
  shape it into an outline and a script in the same file and ask about a heading when unsure, and ⋯ → Start v1 has it
  build the first cut. On a phone the document fills the screen, with Record and Send below and the comments in a
  drawer. A `v0/` from before (idea.md, outline.md, script.md) is joined into the one file, the old files left as they
  are.
- Before every agent attempt Retake keeps a copy of the extension's files (`.retake/history/`), so the editor can undo
  one agent edit of a document without git.
- Core: a folder whose `v0/` holds an enabled kind's marker is a video (an idea) in the scan, `api/info`, `api/home`
  (`"status": "no_cut"`, `kind`, `kinds`, `changed`) and `retake connect`; `POST api/notes` adds typed comments
  (`doc/<n>`, with a text `anchor` and `wants`), done by the agent's report without a render; `retake ask --file PATH
  --line N --heading H --quote Q` asks about a place in a document; the next cut of a v0-only video is
  `v1/<folder>_v1.mp4`; a task's prompt gets `## From the <name> extension` before Retake's own rules. Hosted, the
  files stay on the editing computer and go through its worker's relay (`ext.*` requests), and a new idea's folder is
  made there through the new machine channel (`GET/POST /api/machine/requests`).

### Your look, outside the install (Theme API v1)
- A theme is a folder of CSS, fonts and images that lives outside the install: `~/.config/retake/theme/` for
  `retake open` and `retake share`, `<data dir>/theme/` (`/data/theme/` in Docker) for `retake serve`, or `--theme
  DIR` / `RETAKE_THEME` anywhere (`none`: the built-in look). Updating retake never changes it. `theme.toml` sets
  the name shown in the titles and on the home, a logo, a favicon and light or dark (spec/theme.md,
  docs/theming.md).
- Every colour and font of the page is a token (`--bg`, `--accent`, `--record`, `--status-done`, `--agent`,
  `--font-ui` …), including the timeline's canvas; every major element has a `data-part`; the built-in CSS is in
  `@layer retake`, so a theme wins without `!important`. A test fails on a hard-coded colour.
- `retake theme init [DIR]` writes a starter with every token and part; `retake theme check [DIR]` reports what
  this version doesn't use (exit 1). Theme files are served as CSS, fonts and images only, with a CSP that blocks
  scripts; a broken theme is a startup warning, never a broken page.
- Two examples in `examples/themes/`: `light` and `brand` (a made-up company with an OFL font and a logo).
- Every answer carries `X-Retake-Api-Version: 1` (spec/api.md, "API versions"), for teams that ship their own page
  with `--ui-dir`.
- **Theme API:** v1, all tokens and parts new. After updating, run `retake theme check`.
- The wordmark is "Retake." with the period in the record red (`--wordmark-dot`), on the home and the server's
  sign-in and setup pages; titles stay plain. A theme's name or logo replaces it. `retake serve`'s sign-in, setup and
  message pages wear the host's theme too.
- The built-in look is otherwise the same: before/after screenshots of the review page (desktop, phone) are identical
  to the pixel, and the home differs only in the length on each card (`#ececef` → `#dcdce1`, the over-the-video
  text). States the screenshots don't show moved a few levels to the nearest token: menu and popover text
  (`#e8e8ef` → `#e4e4ea`), the timeline tip, the agent select, error lines (→ `#ff8f86`), the sidebar's focus ring
  (`#b7a6ff` → `#a693fa`), and the reply box's idle colours.
### Folders on the left of the home
- `retake open <folder>` scans the whole tree (6 folders down, hidden, `.review`, `node_modules` and `__pycache__`
  folders skipped, at most 20 000 folders, no video opened): folders, the projects in them, and their videos. A
  folder that holds one video (its family, directly or in `vN/` folders) is that video, in the project above it:
  `deadlock/item-shop` is the video `item-shop` of the project `deadlock`. A folder with more (two families, or
  videos in folders inside it) is a project with its own video. Deeper videos are named by their path
  (`2026-10-19/fullgame` is `/p/2026-10-19-fullgame/`); the ones directly in the folder keep their names and URLs.
- `GET /api/home` gains `tree` (folders → projects → videos) and, per video, `video` (its own name), `path`,
  `project` and `questions`. `retake connect` sends each video's `path` and `project`, so the hosted home shows the
  same tree.
- The home has a left column: the tree with chevrons and status dots (has questions, to review, being edited), a
  filter, a resize handle, ☰ to hide it, and a drawer on a phone. A folder or project shows its videos as cards;
  the selection is in the URL hash and remembered with the opened folders. The review page's back link reads
  "Project › video" (`deadlock › item-shop`) and returns to the same place.
- The organizing that lived only in Retake's database (New project, New group, Move to) is gone from the page.
### Moving or copying a project keeps its stills
- The project store keeps paths relative to the project folder (notes' snapshots, pushed versions, transcribe
  jobs' take folders). Opening an older store fixes its absolute paths, also ones from where the folder used to be.
- A missing still never pauses a task: the worker makes its own from the version at the note's moment, with the
  note's pen lines drawn on it, and the note's context says so (`missing`). A missing attached image is said there
  too. Notes now keep each stroke's line (up to 64 points) and time.
- A comment card's drawing sits on the still of its moment (`GET /api/notes/<id>/snapshot`), else on its version's
  poster, instead of an empty dark box.
### Comments stay on their version
- A version shows its own comments only: the list, the To do and Done counts, the timeline markers, the drawings on
  the video and Send N. Older versions' open comments no longer pile onto the newest version at their old times.
  A draft's comments belong to the version it heads for.
- A strip of versions over the comments (`v1 … v7`, the shown one highlighted, each with what's still to do, the
  agent's draft as `v8 · drafting`) switches versions; on a phone it scrolls sideways. The bar's picker stays.
- On the newest version, one quiet line says what's still open earlier (`Open on earlier versions: v6 · 2, v3 · 91`),
  and each part goes there. The agent still gets every open comment of every version as context.
- Comments recorded on a draft stay on its version after the family stops listing that draft.
### Faster renders
- The pilot's first 40 s render at 50 fps instead of 17 (one render, 16 threads, NVENC), and 8 renders at once at 90
  fps in all, where before 4 at once ran the machine out of memory. The whole 190 s pilot renders in 197 s.
- Layers that read the same frames of a file share one decoder. The item-shop pilot's first 40 s went from 109
  ffmpeg decoders (96 of them the tiles of one 3D panel) to 14, decode CPU from 864 to 71 seconds, and peak memory at
  `--jobs 8` from 17 GB to 4.5 GB.
- Footage is converted to RGB by zimg when ffmpeg has `zscale`: within 0.1–0.7 dB of rounding a float reference
  (swscale was 7–14 dB further), at a third of the CPU. Untagged footage looks as before.
- Each unit is a pipeline: decoders read ahead on their own threads, the encoder is fed from another, frame buffers
  are reused. The same bytes as before the change of threading.
- `retake-edit --encoder auto|x264|nvenc|videotoolbox|qsv|vaapi`, `[render] encoder` in the config. `auto` uses NVENC
  on NVIDIA (tuned to match x264 CRF 18 on the pilot), VideoToolbox on macOS (untested), else x264, after a test
  encode cached per machine. Changing it re-renders each shot once.

### Agents ask on the video, never in the terminal
- The built-in agents never stop for a permission prompt: Claude Code runs with `--dangerously-skip-permissions`
  (and without its own question tool), Codex with `--dangerously-bypass-approvals-and-sandbox`, in the background and
  in herdr or tmux. The docs say plainly that the agent has full access to the editing machine.
- `retake ask --note 12 --at 1:23 "question"` from inside a task asks about a moment of the video, with boxes,
  arrows, freehand lines, images and tap-to-answer choices; no video path or token (a per-attempt local endpoint and
  secret from the worker). It works in `retake open` and through `retake connect`.
- The question shows under its comment (a thumbnail of the frame with the agent's drawing, its choices, the reply
  box) and as a "?" on the timeline; the agent's visuals draw on the video in their own dashed violet style. The
  answer (typed, tapped or spoken) goes back to the same task: into the live herdr or tmux session, or the next
  attempt's context. A task can end an attempt with some notes Done and some still Question.

### Hosting in two commands
- `docker compose up -d` in `deploy/` runs one container with one volume (SQLite and local files). Postgres and
  MinIO moved to the compose file's `scale` profile. `deploy/Caddyfile` is a ready HTTPS front.
- First run: with no accounts, `retake serve` prints a one-time setup link. `/setup` creates the owner in the
  browser and is gone afterwards; nobody without the server's log can claim a fresh server.
- Connect your computer: `POST /api/connect` (admins) makes a machine token and its `retake connect` line.
  `retake connect` registers the folder's timeline projects (name, title, poster; never footage) and runs one worker
  per project in one process, picks up new projects, and reconnects with a bare `retake connect`.
- `GET /api/home` gains `machines` (from worker heartbeats, in hosted and `retake open` mode); the signed-in `/` of
  the team server is the home screen, with `GET /signout` for its Sign out link.
- `docs/editing-workers.md` is now `docs/hosting.md`, the short story.

### On-demand streaming and review home
- Rendered pushes register a poster and pending HLS version immediately. Opening the version starts rendering;
  closing the last viewer stops it. Completed units stay cached, and reopening resumes. `--no-stream` keeps the
  completed-MP4 workflow.
- `retake open <folder>` and `retake share <folder>` show projects with thumbnails, review status, All videos and
  Next video. The player shows render progress and keeps C comparison and mobile scrubbing.
- Integrated the home screen and streaming lifecycle with a shared viewer id and compatible unwatch routes.
- Streamed playback uses hls.js where supported to avoid Chromium native-HLS failures on growing playlists.
  Progress includes the last update of a cached burst, and home posters persist when the player loads.

### Scrubbing on iPhone
- Dragging the timeline now scrubs like a video player: playback pauses for the drag, one seek is in flight at a
  time (to the latest finger position, a fast keyframe seek where the browser has one), the release does one exact
  seek, and playback resumes only if it was playing. iOS browsers (Safari's engine) used to keep playing and snap
  back to where they were when a drag fired more seeks than they could load.

### Eraser
- E (or the eraser button next to the pen colors) switches the pen to an eraser. A swipe over any line on screen
  removes the whole line, recording or not, including lines from earlier takes; a fast swipe misses nothing. Z brings
  back the last erased line. A pen color switches back to the pen.
- Take format v1.2: `erase` and `unerase` events (+`id`) for lines of the take in progress; lines of earlier takes go
  in the version's `erased.json` (new route `POST /api/erase`), which history and the notes leave out. Old takes are
  never rewritten.

### Editor slice 4: what the item-shop pilot asked for
- Perspective: `transform.rotation_x` / `rotation_y` with `perspective` (CSS's model, about the anchor), and corner
  pins (`transform.corners`), on any layer, keyframable. Footage is decoded once and sampled through the warp
  (bilinear, trilinear mips, anti-aliased edges).
- Blend modes (`normal`, `add`, `screen`, `multiply`, `overlay`, `lighten`, `darken`), opacity groups (`type:
  group`), masks (rect, ellipse or SVG path, feather, invert, an outline on the mask's edge) and alpha or luma track
  mattes, on graphics and footage alike. SVG path shapes and linear-gradient fills.
- `resample: lanczos` (Pillow's LANCZOS for images, byte for byte; ffmpeg's Lanczos for footage, cropped in the
  decode pipe) and `resample: unfiltered` (OpenCV-style bilinear); `delogo` boxes; blur and shadow on footage.
- Text `hinting`, `subpixel` and `metrics: "pillow"` (anchored like Pillow's `anchor="mm"`, measured in a test).
- `expr_lib`: JavaScript helpers shared by a timeline's or a composition's expressions.
- Audio: stream selection (`stream`), mono played at unity in both channels, fade curves, ducking under inserts or
  spine items with attack and release, gain keyframes and expressions, per-element `loudness` targets, and a master
  bus (`master: {loudness, true_peak, lowpass}`: BS.1770-4, two passes, a 4× oversampled look-ahead limiter).
- External raster backends: a component may declare `backend: {command}`; the renderer runs the program over the
  `retake-raster/1` protocol (a JSON request on stdin, framed RGBA frames back, a timeout, a cleared environment),
  with the package hash in the layer key. Example: `editor/examples/backends/python-text/`.
- Fixed: kits entries that aren't kit packages (`plugin:deadlock`) are recorded and ignored again, as in slice 2.

### Editor slice 3: components, brand kits, house rules and the shared library
- Components (`spec/components.md`): a `component.json` with a props JSON Schema (descriptions and defaults an agent
  reads), layers in the timeline's own layer model as templates (`$props`, `$id`, kit tokens, `$expr` computed once),
  examples and a changelog. A timeline uses one where layers would go, `{"use": "pop-text@1.0.0", "props"}`; it is
  designed on a 1920x1080 canvas and fits any other.
- The lockfile, `<stem>.lock`, pins every package to a content hash. Rendering refuses an unlocked or changed package;
  `retake-edit component lock` pins, `patch apply` pins what a patch starts using, and `retake-edit component upgrade
  <element|all> --to x.y.z` moves elements one patch at a time. Expansions go into the layer and unit keys, so a
  component change re-renders only the elements whose expansion changed.
- Brand kits: tokens (`kit:color.accent`) anywhere in a timeline or a component, and house rules that `retake-edit
  lint` checks with typed violations: no-overlap, min-gap, max-count-per-window, safe-area (on the boxes the graphics
  really draw), min/max-duration, and custom JavaScript rules. It exits 1 on errors.
- `retake-edit component preview`: each example at five moments on a storybook sheet (PNG) and a 2-second WebP. A
  component imported into the catalog (`retake assets import <dir>`) is an entry of the new kind `component` with
  those previews (catalog schema version 2).
- Text `highlight` (words of a text layer in another fill, as a prop) and `"$words:<spine-id>"` props (a spine item's
  words on a composition's clock), for word-timed karaoke captions. Manifest elements say which component drew them.
- `retake lib search|add|list|describe|pack|recipe export|lint`, with `--json`: static registries (an `index.json` on
  any host or in a git checkout), vendoring with hash checks and the license gate, and edit recipes that carry the
  timeline, its patches and its packages, with placeholders instead of media. MCP: `component_list`,
  `component_describe` (with the sheet as an image), `lib_search`, `lib_add` and `lint`.
- `library/`: ten original CC0 components (title-card, pop-text, lower-third, caption, pip-frame, progress-bar,
  zoom-punch, counter, callout-arrow, freeze-frame-highlight) and the `clean` kit with its house rules;
  `scripts/build-registry.sh` builds `library/index.json`.

### Milestone 3b: the team server
- `retake serve --data DIR` hosts many projects (`spec/server.md`). Each is a version family with its store, served
  at `/p/<project>/` by the same page and API as `retake open`; the page now uses relative URLs.
- Accounts without passwords: `retake user add` prints a one-time sign-in link (or mails it, with SMTP). Its page
  signs in only through a button, so mail scanners don't use it up. Sessions are server-side, hashed, HttpOnly,
  Secure and SameSite=Lax, rotated daily with a one-minute grace, and end after 7 idle or 30 days; they can be
  revoked.
- Project members are viewers, reviewers, approvers or owners; admins own every project. A take's author is the
  account's name. Share links and agent tokens of 3a work per project, with cookies scoped to `/p/<project>/`.
- Every write without a Bearer token must come from the server's own origin (CSRF). `X-Forwarded-*` is believed only
  from `--trust-proxy` addresses, and the Host must be the public URL's.
- Uploads speak tus 1.0.0 and check the file's SHA-256. `retake remote add` and `retake push --remote` upload in
  chunks and resume, even across runs; `POST /p/<project>/api/push` and the project's `/mcp` (`upload:<id>`) push
  them, with the same push rule.
- Storage drivers: SQLite and local files, or Postgres (a schema per project, same tables) and S3-compatible storage,
  where the workspace is a cache that is uploaded as it changes and downloaded when a project opens. Media is proxied
  from S3 with Range support. A conformance suite runs the same tests on both, with Postgres and MinIO in docker.
- `Dockerfile` (a static binary with the page, ffmpeg and a CPU whisper.cpp, as a non-root user),
  `deploy/docker-compose.yml` (retake, Postgres, MinIO) and `deploy/README.md`. `deploy/smoke.sh` runs the stack end
  to end in a headless browser and tears it down.

### Release readiness (milestone 4, short of publishing)
- Versions come from `vX.Y.Z` git tags (`scripts/version.sh`), and `retake version` prints them.
- `.goreleaser.yaml` builds `retake` for Linux, macOS and Windows on amd64 and arm64, with the page inside, plus
  `checksums.txt`. CI builds `retake-edit` on native runners for the same platforms (no Windows arm64 yet). On a tag,
  CI makes all the archives and publishes nothing.
- `scripts/install.sh` installs `retake` (and `retake-edit` with `--with-editor`) into `~/.local/bin`, without sudo,
  after checking the SHA-256.
- `THIRD_PARTY_NOTICES.md` lists the licenses of everything the binaries contain. `scripts/notices.mjs` writes it,
  and CI fails when it's out of date.
- A CC0 sample project (`examples/sample/`, `scripts/make-sample.sh`): a short how-to video in two versions,
  rendered from a timeline, with a pre-made review take. `examples/sample/run-quickstart.sh` runs the quickstart
  without a person, and CI runs it.
- Docs: a new README, and `docs/quickstart.md`, `agents.md`, `config.md`, `timeline.md` and `releasing.md`.
- notes.md and notes.json name the takes' author ("ask Ana rather than guess"), or "the reviewer", where legacy
  named its owner.
- The e2e suite's legacy mode and the legacy scripts need an explicit path; there's no default anymore.

### Editor, slice 1
- `retake-edit`, a Rust binary that makes videos from a timeline file (`spec/timeline.md`, `timeline/0.1`):
  - a spine of voice-over pieces and inserts as the master clock;
  - anchors (`word:`, `start:`, `end:`) instead of seconds, so a trim moves everything after it;
  - shots and overlays made of text, shape, image and video layers, with keyframes, easing and sandboxed JavaScript
    expressions;
  - one-pass audio mixing with fades and ducking.
- Patches are the only way to change a timeline. They name their base revision and the notes they answer, apply
  atomically, and are logged with their inverse (`patch log`, `patch revert`).
- The renderer reads and writes video through ffmpeg, composites in Rust and draws graphics with Skia. Units are
  cached by content: a render only draws what changed and copies the rest byte for byte.
- Each render writes a manifest (`spec/manifest.md`): which elements are on screen, where, from which sources.
  `retake-edit resolve-note` ranks the elements under a reviewer's stroke.
- Commands: `validate`, `resolve`, `patch`, `plan`, `render`, `frames`, `manifest`, `resolve-note`, each with
  `--json` and typed errors with hints.
- A demo project made only from generated media (`editor/examples/demo`).

### Milestone 2: notes, agent API, questions
- The notes engine is ported to Go (`internal/notes`) and checked against the legacy engine's output (golden tests).
- Transcription with whisper.cpp (`scripts/install-whisper.sh` builds it, CUDA when available), or any command that
  prints words as JSON. Models are downloaded on first use and checksummed.
- A project store (`.retake/project.db`, SQLite) holds notes, questions and answers, versions and their changelogs,
  jobs and events.
- Done closes a take and queues its transcription and notes in the server. Failed jobs show a one-line fix.
- Note states: open, question, answered, accepted, retracted. A push is refused while a note on the parent is neither
  answered nor asked about.
- Agent commands, all with `--json`: `retake notes list|wait|sync|build`, `ask`, `note`, `push`, `versions`. The same
  tools over MCP with `retake mcp`. `retake watch --exec` runs a command when new notes land.
- The review page has a notes sidebar: questions on the newest version, resolved notes on older ones. N hides it.
  The reviewer answers a question by recording.
- While recording, images can be dropped or pasted onto the video, pinned to that moment and spot.
- `retake transcribe`, for debugging transcription.

### Phone layout
- The review page has a layout of its own on phones (a touch screen up to 900 px wide or 600 px high, or any window
  under 800x420). Portrait: the video full width on top, a thumb-sized timeline under it, then play, time, the
  version list and a flip button (C), the pen colors and Undo (Z), and a big Record button (R) with Done at the
  bottom. Landscape: the video fills the height, with the pen colors at the left edge, Record at the right edge and
  a slim bar with the timeline at the bottom. Safe areas (notch, home bar) are respected. The desktop layout is
  unchanged, pixel for pixel.
- Safari (iOS included) records AAC in mp4: segments are saved as `seg_NN.m4a`.
- Tests: an end-to-end spec for the phone layout (19 tests, Chromium mobile emulation with touch input, portrait
  and landscape, plus Safari's mp4-only recorder). `RETAKE_E2E_PORT` moves the suite's ports.

### Milestone 1: local player at parity
- `retake` CLI (Go): `open`, `sessions [--json]`, `version`; config file; flags before or after the video.
- Local server implementing `spec/api.md`, compatible with the legacy Python player in both directions. It refuses
  foreign Host headers (DNS rebinding) and foreign Origins on writes.
- Review page ported to Svelte 5 + TypeScript + Bits UI, pixel-identical to the legacy page in idle, toast and
  version-list states.
- New: audio chunks are buffered in IndexedDB until the server confirms them and resent after a reload.
- New: `.m3u8` versions (native HLS, hls.js fallback), served under `/video/<label>/<file>`.
- New: `author` in `session.json`.
- Take format v1 documented, with a JSON Schema every saved take is validated against.
- Tests: Go unit tests (race), page unit tests (vitest), a Playwright end-to-end suite on generated media (46 tests),
  and a legacy parity script. GitHub Actions workflow for all of it.
