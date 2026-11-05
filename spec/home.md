# The home screen: many videos to review (v1)

This is the contract for reviewing many videos in one sitting: `retake open <folder>` serves every project in a
folder, a home screen lists them with a first-frame thumbnail and a status, and the review page moves from one video
to the next. Change it here first.

A reviewer opens the home, picks a video (or the first one to review), talks and draws over it, presses Done, and
goes straight to the next one. Nothing renders or streams until a video is opened, and leaving it says so, so the
machine that hosts the review does work only for the videos someone is watching.

`retake open <video>` doesn't change: one project at `/`, as in spec/api.md.

## Local multi-project mode: `retake open <folder>`

```
retake open <folder> [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--theme DIR] [--allow-host H]... [--verbose]
```

When the argument is a folder instead of a video, the server serves every project in it. `--versions` takes a video,
so it can't be used with a folder (exit 2).

The local owner can change this selection in **Linked folders**, reached from the navigation or Settings.
The owner can select several folders, choose whether to include their subfolders, and remove a link without
deleting files. The original folder remains selected until the owner changes it. See [library.md](library.md)
for the selection API, persistence, offline folders, and share boundaries.

### Folders, projects and videos

The home is the folder as a tree: **folders** that organize, **projects** in them, and each project's **videos**.
`deadlock/item-shop/v7/itemshop_v7.mp4` is the video `item-shop` of the project `deadlock`;
`deadlock/2026-10-25/jump-souls/v4/jump-to-secure-souls_v4.mp4` is the video `jump-souls` of the project
`2026-10-25`, in the folder `deadlock`.

- A **video** is a version family with its project root, as in spec/notes.md ("Project root"): its versions, takes,
  notes, store and agent. It is what the routes below, `/p/<name>/` and the rest of this spec call a *project* (one
  review page, one card), so review URLs, notes and agents stay per video. A folder `C` holds a video when a
  **versioned video** has its root at `C`:
  - a file with a family video extension (`.mp4 .m4v .mov .webm .mkv .m3u8`) whose stem has a version number
    (`clip_v3`, spec/api.md "Detection"), found directly in `C` or in one of `C`'s own subfolders (not hidden, not
    `*.review`; one level, where version folders are: `C/v3/clip_v3.mp4`);
  - and `project.Root(file) == C`. So `deadlock/item-shop/v6/itemshop_v6.mp4` puts a video at `item-shop` (its `v6`
    folder is a version folder), `macro/macro_v2_roughcut.mp4` one at `macro`, and a folder of unnumbered clips
    (`broll/`) holds none. Nor does a folder of recordings named for their date and time (OBS's
    `2026-10-17 21-30-05.mp4`) or for an ID with a v (`twitch_v2903906005_3480-4520.mp4`): a version number has at
    most 4 digits (spec/api.md, "Detection"), so they never look like one video at a made-up version.

  One folder holds one video: its store is the folder's `.retake/`. A folder with two families side by side
  (`a_v1.mp4` and `b_v1.mp4`) shows the one with the newest version, as before (per-video stores will lift this).
- A **video's folder** is a folder that holds just one video: one family (directly, or in its `vN/` folders) and no
  other video in the folders inside it. Its other contents (`assets/`, `work/`, `vo/`) are the video's own. Such a
  folder is the video, not a project: the tree shows it as a video named after the folder (`item-shop`), in the
  **project** that is the folder above it (`deadlock`).
- A **project** is any folder that holds videos directly: video folders, or family files of its own. So it is the
  folder above a video's folder, and also a folder that holds a video itself and more (two families side by side, or
  videos in folders inside it, which are then its own projects' videos). It can be at any depth, with folders above
  it that only organize.
- A **folder** is any other folder on the way to a project. A folder with no video inside it doesn't show.
- A folder with a native timeline (spec/bridge.md: `[edit] timeline` in `retake.toml`, or `edit/timeline.json`) and
  no version yet is a video to come: `retake connect` puts it on the team server (one video, no version, so its
  worker can render the first one), and `retake open` shows it once it has a version.

The opened folder is the top of the tree, and the project of the videos directly in it: of the videos in
`deadlock/` when `deadlock/` is opened, and of its own family (`shows/intro_v1.mp4` when `shows/` is opened). The
folders in it are scanned as usual.

Inside a video's folder the scan looks only 2 folders further (not into version folders) for more videos:
`item-shop/shorts/one-item/v1/one-item_v1.mp4` makes `item-shop` a project holding its own video and the project
`item-shop/shorts`. Deeper videos inside a video's folder (`item-shop/assets/memes/gojo/cache/v2/…`) are its own and
never show.

**The scan** starts at the opened folder and goes down at most 6 folders. It never enters hidden folders (`.retake`
among them), `*.review` folders, `node_modules` or `__pycache__`, and it reads at most 20 000 folders. It lists
folders and stats only the files named like versioned videos: no video is opened or probed to build the tree. A
folder reached through a symbolic link is followed when it lies outside the opened folder (once); a link to a folder
inside it is left out, since the scan finds that folder anyway.

Once the owner chooses linked folders, each link is a strict scan boundary: symbolic links cannot bring in
outside folders. With **Include subfolders** off, discovery checks only that folder's own video family and
version folders. It does not scan child projects. An empty selection scans nothing.

A video's **anchor** is the file its family is detected from: of its versioned files, the highest version number, a
plain name (`clip_v3`) before one with more after the number (`clip_v3_draft`), then the newest. Its server follows
the newest version of the family (as `retake serve` does), so a version pushed later shows up without a restart.

The folder is scanned again on `GET /api/home` when the last scan is older than 2 seconds, or than 10 times as long
as that scan took (a big folder is scanned less often), and when a request names a video it doesn't know, so new
projects and videos appear without a restart. With `--verbose`, each scan logs how many folders it read and how long
it took.

**Names.** A video's name is its folder's path in the opened folder made URL-safe, the team server's rule
(`^[a-z0-9][a-z0-9-]{0,62}$`): lower case, every run of other characters (`/` too) becomes `-`, cut to 63
characters, and `video` if nothing is left. So `item-shop` stays `item-shop` and `2026-10-19/fullgame` is
`2026-10-19-fullgame`; the opened folder's own video is named after the folder. Names are given shallowest folder
first, then in path order, and when two folders give the same name the later one gets `-2`, `-3`, …, so a folder
added deeper never takes a name already in use. The name doesn't depend on which project the video is in, so its
URL stays when folders around it change. Its title is the folder's name as it is.

### Routes

The same routing as the team server (spec/server.md): `/` is the home, and each project is the existing review page
and API (spec/api.md, spec/notes.md, spec/bridge.md) under `/p/<name>/`, for its newest version.

| Method | Path | Needs | Does |
|---|---|---|---|
| GET/HEAD | `/`, `/index.html` | watch | The home screen (the page's `home.html`). |
| GET | `/api/home` | watch | The projects (below). |
| GET | `/api/ping` | any role | `{"ok": true}` |
| POST | `/api/projects` | local owner | Makes a new local project, optionally importing one selected video. |
| GET/HEAD | `/assets/*` and other page files | watch | The page's static files, as at `/p/<name>/`. |
| GET | `/s/<token>` | nothing | A share link: the whole home, or one video's page for a video's link (below). |
| GET | `/api/videos/<name>/trash` | owner | What Move to Trash would move ("A video's ⋯"). |
| POST | `/api/videos/<name>/trash` | owner | Moves the video's folder to the OS trash. |
| POST | `/api/trash/<id>/restore` | owner | Puts it back (Undo). |
| POST | `/api/videos/<name>/reveal` | owner | Shows the video's folder in the computer's file manager. |
| GET | `/p/<name>` | as below | 308 to `/p/<name>/` |
| any | `/p/<name>/...` | spec/team.md's table | The project's review page and API, exactly as `retake open <video>` serves them at `/`. |
| GET | `/favicon.ico` | nothing | 204 |

Anything else is 404 `{"error":"not found"}`; an unknown project is 404 `{"error":"no such project"}`.

### New local project

The local Home always offers **New project** and **Open existing project** to its local owner, before or after videos
exist. Open existing project opens the existing linked-folders flow; it never grants a shared viewer, a tunnel request,
or a project token access to local files.

`POST /api/projects` is local-owner-only: a shared host, bearer token, cookie, or non-owner gets 403. Its request is a
streaming `multipart/form-data` body with `root` (the opaque `id` from `GET /api/library`), `name` (the new folder's
display name, 1–200 characters), and an optional `video` file selected by the person. `root` is required, including
for the implicit initial link (`original`). The server resolves it against the current library transaction and creates
only at that linked root. It never accepts a filesystem path, follows an unlinked folder, or revives a removed link.
An offline or removed id gets 409; an unknown id gets 400. The response contains only the project name and relative
review URL, never the root path or id.

- Without `video`, Retake creates `<root>/<slug>/edit/timeline.json` as an empty native project and returns 201
  `{name, url}`. It needs no agent and does not render until a person or agent adds a valid timeline.
- With `video`, Retake first writes only under a new temporary folder inside the selected root. It probes the streamed
  upload with its bundled ffprobe. A browser-safe H.264 `yuv420p` video with AAC audio is moved to
  `<root>/<slug>/v1/<slug>_v1.mp4`; all other readable videos are converted there with bundled ffmpeg to H.264
  `yuv420p`, AAC when audio exists, fast-start MP4. A video without audio stays playable without an invented audio
  stream. The original selected file is never changed.
- The copy/convert streams from disk: it does not buffer the video in memory. Cancellation, an unreadable video,
  invalid multipart input, a name collision, or ffmpeg failure removes the temporary folder and leaves no project.
  The response is 400 with one plain error (409 for an existing folder); successful response is 201 `{name, url}`.

The page shows upload/copy/convert progress and an inline failure with Retry. It only navigates to the returned project
after success. A blank project has its native timeline and a built-in Scripts-compatible v0 brief, so its first in-app
action is to write the brief or open its chat; no agent or render starts until the person chooses one.

**Who a request is.** As spec/team.md, with the folder in place of the project: local requests (`127.0.0.1:<port>`
or `localhost:<port>`, no proxy header) without a token are the `owner`, with no accounts. A request through a shared
host (an `--allow-host` name, or one in the folder's hosts table) needs a token of the folder (below), from
`Authorization: Bearer` or the `retake_share` cookie; its role applies on the home and on every project. On
`/p/<name>/...` a Bearer token of that video's own store works too (an agent token from `retake token create
<video> --role agent` on `/p/<name>/mcp`). Existing single-video share links and cookies continue working when the
main address becomes the folder's home: their hosts are recognized, and their tokens open a filtered home containing
only that video (scope `video:<name>`, "Share scopes" below). They never grant access to another video's API, media,
notes, events, or worker routes. The token remains in its original store, so expiry and revocation keep working. Hosts,
origins, the 401/403/429 answers, rate limits and `Cache-Control: private` are those of spec/team.md.

### `GET /api/home`

```json
{
  "title": "deadlock",
  "folder": "/abs/path/deadlock",
  "role": "owner",
  "projects": [
    { "name": "item-shop", "title": "item-shop", "video": "itemshop", "path": "item-shop", "project": "item-shop",
      "url": "p/item-shop/", "version": "v6", "versions": 6, "duration": 412.5, "pushed": "2026-10-28T19:02:11Z",
      "status": "to_review", "takes": 0, "open": 0, "questions": 0, "stream": null, "thumb": "p/item-shop/api/poster" }
  ],
  "tree": {
    "title": "deadlock", "path": "", "project": true,
    "children": [
      { "title": "2026-10-25", "path": "2026-10-25", "project": true, "videos": [ { "name": "2026-10-25-jump-souls", "…": "…" } ], "children": [] }
    ],
    "videos": [ { "name": "item-shop", "…": "…" }, { "name": "macro-v1", "…": "…" } ]
  }
}
```

- `title` is the folder's name. `folder` (its path) is sent to the owner only. `role` is the request's role. A local
  home's owner also gets `trash`, what the computer calls its trash for Move to Trash's words: `"Trash"`, or
  `"Recycle Bin"` on Windows, where every "Trash" below reads "Recycle Bin".
- `projects` is every video (the name is older than the tree), sorted **to review first, then by newest push**, then
  by name.
- A video: `url` and `thumb` are relative to the home. `version` is the newest version's label ("" before the
  project's server can read one), `versions` how many there are, `duration` the newest version's (or null).
  `pushed` is when the newest version arrived: its row in the project store's `versions` table (a push), else the
  file's modification time, as RFC 3339 UTC.
- `title` is its folder's name; `video` is its family's name, the newest version's file name without the version
  (`itemshop` for `itemshop_v7.mp4`; "" before there is a version). The page names a video in a video's folder by
  `title` (`item-shop`), and one in a project's own folder by `video`, else `title`.
- `path` is the folder that holds it, relative to the opened folder with `/` between names ("" for the folder
  itself); `project` is the folder of its project (spec above): the folder above `path` for a video's folder
  (`deadlock` for `deadlock/item-shop`; "" when that's the opened folder), else `path` itself. On the team server
  they are what `retake connect` sent ("" for a project made another way).
- `status` is `to_review`, `reviewed` or `being_edited` (below). `takes` is the number of closed takes on the newest
  version, `open` the number of its notes that are `open` or `question`, and `questions` the number that are
  `question` (the agent asked and waits for an answer).
- `stream` is the newest version's streaming state when the streaming side reports one, else null:
  `{"state": "pending" | "rendering" | "complete", "done": n, "total": m}` (below).
- `live` is true while the agent works on the video (one of its tasks is running or stopping), so its newest version
  is the live one (spec/automation.md, "The live version"); absent otherwise. `version` is then often one of its
  revisions (`v6-draft11`): the page shows it as `v6`.
- `thumb` is the newest version's poster (below).

`tree` is the same videos as folders → projects → videos. A node is `{title, path, project, videos, children}`:
`title` the folder's name (the response's `title` for the top), `path` as above, `project` whether it is a project,
`videos` the project's videos (the same objects as in `projects`; the project's own video first, then by path), and
`children` the folders and projects in it, by title (case ignored). The top node is the opened folder, a project
when videos are directly in it. Only folders on the way to a project are in it, and for a share link only those on
the way to a video the link can see. Local and hosted build it the same way from each video's `path` and `project`,
so a computer's folder looks the same on the team server as in `retake open` (except a timeline folder with no
version yet, which only the team server lists, as a video without a version).

`machines` lists the computers that edit these projects (spec/automation.md), for the owner; it is `[]` for anyone
else:

```json
"machines": [ { "id": "w_…", "name": "sam-pc", "online": true, "agents": [ { "id": "claude", "name": "Claude Code" } ] } ]
```

- It comes from the worker heartbeats in the projects' stores (the `workers` of `GET api/automation`), not from a
  list of its own. A computer runs one worker per project, so the workers are grouped by name: one entry per name
  (the computer's hostname unless the worker was given another), with the smallest worker id as its `id`.
- `online` is true while one of its workers polled within the worker lease (30 s). `agents` is every agent its
  workers offer, once each, in the order first seen. Online machines come first, then by name.
- In `retake open <folder>` the in-process worker shows up here like any other, online while it runs. The team
  server (spec/server.md) lists the machines of the signed-in person's projects.

The team server (spec/server.md) answers `GET /api/home` too, for a signed-in user: their projects, with a `user`
field `{email, name}`, no `folder`, and the tree titled "Your projects". Its projects page at `/` stays as it is.

### Status

Computed for the newest version, on every request:

| Status | When |
|---|---|
| `to_review` | No one has closed a take on it yet: no take folder of its own with `session.json` `closed: true` (take format v1.1), or a legacy `notes.md` in the take folder. Takes carried over by `inherit.json` don't count: they belong to an older version. |
| `being_edited` | It has a closed take, and notes in its scope (recorded on it, or carried over inside their ranges) are `open` or `question`: the agent's turn, since the next push must answer them (the push rule). |
| `reviewed` | It has a closed take, and nothing in its scope is open: the notes are still being written up (the jobs), or there are none. |

A new version starts at `to_review` again, so a project goes to review, being edited, to review, ... as the agent
pushes. The home shows them as **To review**, **Reviewed** and **Being edited**, plus the streaming state when there
is one: **Rendering n/total** (`rendering`) or **Not rendered yet** (`pending`).

## Thumbnails and the poster: `GET api/poster[?v=<label>]`

Every project page (and `retake open <video>`'s `/`) answers `GET api/poster`: a JPEG of the version's first frame.
No `v` means the current version; an unknown label is 404 `{"error":"no such version"}`. It needs watch.

1. The streaming side's poster, when it exists: `<stem>.poster.jpg` beside the version's file
   (`v4/clip_v4.poster.jpg` for `v4/clip_v4.m3u8`).
2. Else a frame at t = 0, made once with ffmpeg (`-ss 0 -frames:v 1`, at most 640 px wide) and cached under the
   project root's `.retake/thumbs/` as `<sha256 of path, size and modification time>.jpg`. A changed file gets a new
   one. A file ffmpeg can't read isn't tried again by the same process.
3. A **relayed version** (its video is on the editing computer: a worker's render, the live version's revisions
   `v6-draft11` among them, or a connected computer's version file; spec/automation.md, "The media relay") gets its
   frame the same way, from the start of its media asked through the relay: a playlist's first segment, with the
   init segment before it (`EXT-X-MAP`) for fMP4; a file's first chunk and, when it is longer, its last one, in place
   in a sparse copy (an MP4's index is at one end or the other). It is cached as `<sha256 of what names that media for
   good>.jpg`: a render's id and file, or a version file's worker, label, size and time. A render with no segment yet,
   or a computer that is offline, has none for now and is asked again next time.
4. **Else the newest version before it that has one**: a version with no frame of its own (yet) answers with the
   frame of the newest video version whose number is not higher (a live version's other revisions first, then the
   versions before it), so a card always has a picture. Only when no version has a frame is it 404
   `{"error":"no poster"}` (an extension's version alone, an idea, never has one).

The answer has `Content-Type: image/jpeg`, `Last-Modified` and `Cache-Control: no-cache`, and answers
`If-Modified-Since` with 304. The home's `thumb` and the review page's `<video poster>` are the same URL, so the image
the home showed is the one the page shows while the video loads: it feels like it's already playing.

## The home screen

The page's second entry, `home.html`, in the review page's look: the dark background, the same fonts and colors,
nothing more than the videos.

- **The folder tree** on the left (`tree`): the opened folder on top (selecting it shows every video), the folders
  and projects in it with a chevron that opens and closes them, a project's videos under it, and a status dot on
  each video: **has questions** (`questions > 0`), else **to review**,
  else **being edited**; a reviewed video has none. A closed folder or project shows the dot of the most pressing
  video inside it. Rows have a fixed height and the dot a fixed place, so nothing moves when statuses change. A
  filter box at the top keeps the rows whose name (or a name above or below them) contains the text, opened.
- **Selecting.** Clicking a folder's or project's name selects it (and opens it); its chevron only opens or closes
  it. Clicking a video opens its review page. The selection shows in the main area: its name, a breadcrumb of the
  folders above it, "N videos · M to review", and its videos as cards (a project: its own; a folder or the top: every
  video inside it). Nothing selected is the top: every video. The top row opens and closes like the others. Arrow keys move through the rows (→ opens, ← closes or
  goes up, Enter selects).
- **Remembered.** The selection is in the URL hash, `#/<path>` with each folder name URL-encoded
  (`#/2026-10-25/jump-souls`), so a link opens the same place; Back and Forward don't step through selections (the
  hash is replaced, not pushed). The opened folders, the selection and the column's width are kept in
  `localStorage` (`retake.nav:<home path>`); a link's hash wins over them, and its folders are opened. A selection
  that no longer exists falls back to the top.
- **Desktop:** a column of 200–480 px (264 px at first) that the handle on its edge resizes (drag, or ←/→ when the
  handle has focus; double-click resets it) and the ☰ button at the top hides and shows again. **Phone** (up to
  760 px wide): the column is a drawer that ☰ opens over the page; choosing something, the backdrop or Escape
  closes it.
- A grid of cards, one per video (one column on a phone). A card is a link to `p/<name>/` with the thumbnail (16:9,
  a fixed box, so nothing moves when it loads), the video's name, the newest version's name and length, and the
  status chip (plus the streaming chip; "Has questions" when the agent waits for an answer). In a folder's view a
  card also names its project when that says more than the video's name.
- **A version's name, never "draft".** Cards, the folder tree and the page's title name a version by the one label
  helper the review page uses (`verName`): a live version's revision `v6-draft11` is `v6`. A card adds a small red
  dot after it while `live`; the tree's row, which already has its status dot, doesn't.
- **Every card has a picture.** A video's card shows its `thumb`, which is always a real frame when any version of it
  has one (the poster falls back, below). A card whose image fails to load shows the quiet box. A video whose only
  version is an extension's kind (an idea: a script) shows its document instead of a frame: its first image when it
  has one (the first `![…](path)` with a path inside the kind's folder), else its title (the first `# ` line, else
  the video's name) and its first lines (up to three, as plain text, Markdown marks taken out), small, on the card's
  dark box. The page reads the document through the extension's file routes (`api/ext/<id>/files?glob=<folder>/<marker>`,
  then the file; spec/extensions.md), so it works hosted too, and only a kind whose marker names a `.md` or `.txt`
  file is read this way. The chip stays the kind's word ("Idea").
- **A card's ⋯** (part `video-menu`) at the right of its name: there on hover or focus on a computer, always there but
  quiet on a phone ("A video's ⋯" below). Opening it doesn't open the video.
- With no videos: "No videos here yet", and what a video's file name looks like.
- It reads `api/home` on load and every 15 s while visible (which also keeps an `--idle-exit` server up).
- A viewer's link sees the same home; their review pages are view only (spec/team.md).
- Test hook: `window.__home = { loaded, projects, selected }` (`selected`: the selected path, "" for the top).

## A video's ⋯

Every card has a ⋯ with what you do to a video now and then, in this order; an item shows only where it can work, and
the ⋯ doesn't show at all when nothing can:

1. **Share…**: the sheet of spec/team.md, "Sharing a video from the page". The owner and approvers.
2. **Agent for this video ▸**: the agents the editing computer offers, a check on the one this video uses, and **Use my
   default (Claude Code)** (the default agent's name; checked when the video has no agent of its own). Picking one
   sets the video's agent (spec/automation.md, "An agent for one video"). The owner, while a computer offers an agent.
3. **Download v6 (MP4)**: the newest version as one MP4, the version menu's Download (spec/api.md, "Download") for
   this card's video: it gets the file ready when it has to and downloads it by itself; a toast says when it's getting
   ready, or why it can't. Anyone who can watch (a refused one says so), and not for an idea (no cut yet).
4. **Show in folder**: the video's folder in the file manager of the computer Retake runs on. The owner of a local
   home only (`retake open <folder>`, `retake start`); never on the team server or through a share link.
5. **Move to Trash…**: the owner of a local home only, set apart, in the danger color.

The review page's ⋯ has the same Share…, Agent for this video and Download (spec/notes.md, "The video's ⋯").

### Move to Trash

It moves the video's folder (its versions, `.retake/` with its comments, takes, agent work and links, and everything
else in it) to the operating system's trash. Retake never deletes a video for real.

- **What goes.** The video's folder (`path`) when it is a video's folder (its card's `project` isn't its `path`).
  A video whose files share a folder with other videos (a project's own video), or the opened folder itself, can't
  go: the menu shows Move to Trash… disabled, with the reason as its title ("Its folder holds other videos too").
- **Confirm in the page.** A small dialog names exactly what goes, before anything moves: "Move item-shop to the
  Trash?", the folder (`~/Videos/deadlock/item-shop`), its size and its number of versions ("6 versions · 2.4 GB"),
  then **Cancel** (focused) and **Move to Trash**. No browser dialog.
- **Undo.** After the move the home shows a toast for 8 seconds, "Moved to Trash · Undo"; Undo puts the folder back
  where it was, with the same name and address. After that it is in the OS trash, and restoring it there brings it
  back too (the home finds it on its next scan).
- **While the agent works** on the video (a task running or stopping) it's refused (409, "The agent is working on
  this video. Stop it first.").

`GET /api/videos/<name>/trash` answers `{"name", "title", "path", "size", "files", "versions", "ok", "why"}`: the
folder (`~/…` under the home folder), its size in bytes and its number of files (symbolic links not followed; at most
200 000 files are counted, after which `size` is at least that), the video's versions, and whether it can go (`ok`)
or why not (`why`, one short sentence). `POST` moves it: `{"id", "path"}`, where `id` names the move for Undo; 409
with `{"error": why}` when it can't go. `POST /api/trash/<id>/restore` moves it back: `{"ok": true, "name"}` (its name,
the same as before unless a new video took it meanwhile). An unknown `id`, or one whose folder left the trash, is 404;
a folder at the old place since is 409 and nothing moves. All four are owner only and origin-checked, and only on
`retake open <folder>` (the team server has none: 404).

The project's server and store are closed before the folder moves, so nothing writes into it on its way.

**Where the trash is.**

- **Linux and other freedesktop systems**: the home trash, `$XDG_DATA_HOME/Trash` (`~/.local/share/Trash`), when the
  folder is on the same disk; else the disk's own `<mount point>/.Trash-<uid>`. The folder goes to `files/<name>` and
  an `info/<name>.trashinfo` (`[Trash Info]`, `Path=` the folder's absolute path, URL-escaped, `DeletionDate=` the
  local time, `YYYY-MM-DDThh:mm:ss`) is written first, so the file manager's Restore puts it back. `<name>` is the
  folder's name, else `<name>.2`, `<name>.3`, … when one is there.
- **macOS**: `~/.Trash/<name>` (`<name> 2`, … when taken). Finder's Put Back doesn't know it (only Finder writes its
  records); Undo does.
- **Windows**: the Recycle Bin of the folder's drive, `<drive>:\$Recycle.Bin\<the person's SID>` (made when the drive
  has a Recycle Bin but not their folder yet). The folder goes to `$R<id><ext>` and its record `$I<id><ext>` is
  written first (format 2: its size, the deletion time and its path), as Explorer does, so the Recycle Bin lists it
  under its own name and its Restore puts it back. `<id>` is 6 random capital letters or digits; `<ext>` the name's
  extension, if any. A network share or a drive without a Recycle Bin is 409 like another disk below.
- Nowhere else, and never a copy: a folder the trash can't take by renaming it (another disk without a trash of its
  own) is 409 "Can't move it to the Trash from this disk" and stays.

### Show in folder

`POST /api/videos/<name>/reveal` opens the video's folder in the file manager on the computer Retake runs on:
`xdg-open <folder>` on Linux, `open -R <folder>` on macOS, `explorer /select,<folder>` on Windows. `{"ok": true}`, or
500 `{"error"}` when the opener can't run. With `RETAKE_REVEAL` set, that program is run with the folder instead (tests).

## Settings

The owner's settings live on the home: **Settings** (a gear) at the bottom of the folder column opens them in the main
area, at `#settings` (a folder in the column, or Back, goes to the cards again). Only the owner sees the entry; the
routes refuse anyone else (403). They replace the agent menu the review page had: the review page shows no agent.

- **Agent.** The *default agent*, from the agents the computers offer (none chosen yet: each computer's first), and
  *If its usage runs out*: "Pause and ask me" (the default) or "Switch to <another agent>". Saving them sets every
  project's editing config (`api/automation`'s `config.agent` and `config.fallback`) where that project's computer
  offers the agent; a project no computer polled yet takes the default at its first poll (the worker offers it first).
  A new default agent that was the fallback leaves no fallback. One quiet line under them says how else an agent is chosen: "A video's ⋯
  can give it its own agent, and holding Send picks one for one send."
- **Where agents run**: background, herdr or tmux (spec/automation.md, "Watching the agents"). Locally it's
  `open_agents_in`, used by agents started after retake restarts. On the team server it's each computer's own setting,
  shown read-only.
- **Computers.** Locally: this computer, online while its workers run, and its agents. On the team server: the
  computers (`machines`), and "Connect your computer" (the `POST api/connect` line) when none is online.
- **Extensions**: the installed ones, each with an on/off switch (locally `[extensions] enabled`, as `retake ext
  enable` does, with a project extension's pin). A change applies when retake restarts, and the row says so. On the
  team server they're the server's own (`--extensions`), read-only.
- **Sharing** (locally): how a link made in the page reaches people (spec/team.md, "How a link reaches people"):
  *Through a Cloudflare tunnel* (the default) or *On my network only* (`[share] tunnel = "off"`). When cloudflared
  isn't found, one line says so with the command that installs it (`brew install cloudflared` on macOS; on Linux a
  download of Cloudflare's own binary into `~/.local/bin`) and **Copy**. Not on the team server (its links use its own
  address).
- **Theme**: the theme folder in effect, its name when a theme is there, else that `retake theme init` makes one
  (read-only).
- **Linked folders** ([library.md](library.md)): opens the same local folder manager as the navigation entry.
  It replaces the single-folder editor under both `retake start` and `retake open <folder>`. It is absent on the
  team server. **Test** next to the default agent runs the agent once, as the setup page does.

### `GET api/settings` and `POST api/settings`

At the home's root (`retake open <folder>`; the team server's `/api/settings`), owner only, POSTs origin-checked.

```json
{ "hosted": false, "agents": [{"id": "claude", "name": "Claude Code"}], "agent": "claude", "fallback": "",
  "open_in": "background", "open_in_editable": true,
  "extensions": [{"id": "scripts", "name": "Scripts", "description": "…", "on": true, "loaded": true}],
  "extensions_editable": true, "theme": {"folder": "~/.config/retake/theme", "used": false},
  "config": "~/.config/retake/config.toml", "machines": [ … as in api/home … ] }
```

- `agents` is this computer's agents (locally, the config read again) or every computer's (hosted). `agent` "" means
  none chosen. `loaded` says whether an extension is in effect now; it differs from `on` until a restart. Paths under
  the home folder show as `~/…`. `config` is there locally only.
- `folder` is the videos folder (`~/…`), `folder_editable` whether a `folder` change is taken (`retake start`), and
  `test` whether `POST api/settings/test {"agent"}` runs an agent's test (spec/setup.md, "The agent test").
  `GET api/settings/folders?path=` is the setup page's folder list. All three are local only.
- `sharing` (local only): `{"tunnel": "cloudflare"|"off", "cloudflared": true|false, "install": "<command>"}`.
- `POST` takes any of `{"agent", "fallback", "open_in", "extensions": {"<id>": true|false}, "folder", "share_tunnel": "cloudflare"|"off"}` and answers like `GET`.
  Everything is checked before anything is written: an unknown key, an agent no computer offers, a fallback that is
  the default agent, an `open_in` other than background, herdr or tmux, or an extension that isn't installed is 400
  with `{"error"}` and changes nothing. On the team server `open_in` and `extensions` are 400 (not its settings).
- **Locally** the change goes to `$XDG_CONFIG_HOME/retake/config.toml` (`default_agent`, `fallback_agent`,
  `open_agents_in`, `[extensions] enabled`, `[share] tunnel`; docs/config.md), line by line: the rest of the file, comments included,
  stays as it is, and a default value removes its line. **Hosted** the agent and fallback are kept in the server's
  database (the `settings/agents` setting).

## Moving through projects (the review page)

Only when the page is a project of a home: its path ends in `/p/<name>/` and `<home>api/home` answers. Then:

- **"← Project › video"**, a small link at the top left of the video (`#allVideos`), names where the video is: its
  project's title and the video's name (`deadlock › item-shop`), or only the project's when the two are the same
  name but for case and punctuation (a project's own video, `shorts` and `Shorts`). It goes back to the home at the place the reviewer came from: the
  remembered selection when the video is inside it, else the video's project (`<home>#/<project path>`).
- **"Next video →"** shows at the top right after Done, when another video is `to_review`: the first one in the
  home's order other than this one, from the same project when it has one. It goes to that video's page.
- **`>`** (Shift+. on a US keyboard; no other key of the page) does the same at any time: a take in progress is
  closed and saved first, exactly like Done, and then the next project to review opens. With none left, the toast
  says "No more videos to review".
- On a phone both are 44 px tap targets in the same places.
- While loading, `<video poster>` is `api/poster`.
- Test hook: `window.__review.home = { available, next }` (`next`: the next project's name, or null).

Nothing else on the review page changes.

## Close means close: `POST api/versions/<label>/unwatch`

When the reviewer leaves a project page (← All videos, Next video, Back, closing the tab), the page tells the server,
so the streaming side can stop a render no one is watching:

- On `pagehide` the page sends `navigator.sendBeacon('api/versions/<label>/unwatch?viewer=<id>')` for every version
  it played on this visit (the base and any it flipped to). `<id>` is random per page load.
- Its event stream (`api/events`) closes with the page, too.

`POST api/versions/<label>/unwatch[?viewer=<id>]` needs watch (a viewer's link may leave too), is origin-checked like
every POST, and answers 204 for ordinary videos. A streamed version requires the same nonempty viewer id as its
watch call and returns its stream state (`200`, spec/bridge.md). An unknown label is 404. The optional
`server.Options.Unwatch` hook is notified as well.

### Streaming integration

- A pushed timeline version is a pending HLS version until it is rendered, with a first-frame poster
  `<stem>.poster.jpg` beside its playlist.
- **Watching** starts when a visible page calls `POST api/versions/<label>/watch?viewer=<id>` and renews
  its lease; the render stops when the last viewer leaves (unwatch above, or the watch lease expires). `GET api/info` never starts a render: the home reads
  every project's `api/info` to draw its cards.
- `api/info`'s version objects (the top level and `versions`) carry `stream`: `{"state": "pending" | "rendering" |
  "complete", "done": n, "total": m}` for a streamed version, and may carry `poster`, the poster's URL relative to the
  page. The home passes `stream` through and prefers that `poster` for `thumb`.

## Sharing the home: `retake share <folder>`

```
retake share <folder> [the options of retake share <video>]
retake share list <folder> [--json]
retake share revoke <folder> <id> [--json]
retake token create <folder> --role R [...]
```

One link for the whole home, as `retake share <video>` makes one for a review (spec/team.md):

- **The folder's store** is `<folder>/.retake/home.db`, with the project store's schema; only its `tokens` and
  `hosts` tables are used. Its tokens are the home's: they work on `/`, `/api/home` and every project of that folder,
  and nowhere else (a project's own store, another folder's server and `retake open <video>` don't know them).
- `retake share <folder>` attaches to a running `retake open <folder>` of the same folder (on the configured port or
  the next ones: its `GET /api/home` names the folder), or starts one like `open --no-open --idle-exit 0`. It starts
  the tunnel, adds the host to the folder's hosts table, makes a token and prints `https://<host>/s/<token>` with a QR
  code. The link sets `retake_share` (`Path=/`) and bounces to `/`, the home.
- A video's link (its token in the video's own store: `retake share <video>`, or Share… in the page, spec/team.md
  "Sharing a video from the page") opened on the folder's address sets the same cookie and bounces to that video's
  page, `/p/<name>/`, not to the home.
- `share list`, `share revoke` and `token create` take the folder in place of a video and work on the folder's store.

## Tests

- Go: discovery in a folder (version folders, flat families, the folder itself, hidden, `.review` and unnumbered
  files left out, date-and-time recordings and long ID numbers left out, names and collisions), the scan of a tree (nested folders, `vN/` folders, a project's `shorts/`,
  junk folders, the depth limits, links, a big generated tree in well under a second), the tree built from paths,
  the status rules, `live` while a task runs, thumbnail caching (made once, remade for a changed file, the poster
  beside the file first, a relayed version's frame made once from its fetched start and not remembered while it
  can't be fetched), a relayed version's poster (a file's two ends, a playlist's init and first segment, a live
  revision falling back to the newest version before it until its first segment is out),
  `/api/home` (status, thumbnail URL, newest version, order, the tree, local and hosted alike), `retake connect`
  sending each video's path, `api/poster` and unwatch, share tokens scoped to the folder or to one video, and retired
  `project:`/`group:` links refused with 410 and a page (the migration that drops the organization keeps their rows).
- Go: Move to Trash on a scratch folder with a scratch `XDG_DATA_HOME` (the folder and its info file in the trash,
  Undo putting it back, a taken name, a video that can't go, an agent at work), a video's link bouncing to its page,
  and Show in folder through `RETAKE_REVEAL`.
- Web unit tests: the tree's rows (opened, filtered, dots), the hash, the remembered state, a version's name (never
  "draft") and a script's preview (title, first lines, first image). A video's ⋯: which items
  show for whom, the size and versions line, the agent list and its check.
- e2e `e2e/tests/12-home.spec.ts`: a folder with three generated projects. The home lists them with thumbnails and
  statuses, every card's image a loaded frame (an idea's card its document); a card opens its review page with the poster; a take and Done make it Reviewed; "Next video" and `>` go
  to the next one to review; the home and the controls work on a phone.
- e2e `e2e/tests/24-menus.spec.ts` (Chromium on a computer and an iPhone 15) and `24-menus-webkit.spec.ts` (WebKit
  on an iPhone 15): a card's ⋯ (on hover on a computer, always on a phone), Share… making a link that a fresh browser
  opens to that video only and comments on with the link's name, Move to Trash with its confirm and Undo, and an agent
  for one video.
- e2e `e2e/tests/16-nav.spec.ts`: a nested folder. The tree opens, a project shows its videos, a video opens, its
  back link returns to the same place, a hash link opens it, and on a phone the drawer does the same.

## Share scopes

Folders on disk are the organization (the tree above). The project organization that lived only in Retake's database
(projects and groups made in the page, New project, New group, Rename, Move to, `workspace` in `GET /api/home` and
`POST /api/workspace`) is removed, with its `workspace` table (project store schema step 6, SQLite and Postgres).
`POST /api/workspace` is 404 like any unknown route, and `GET /api/home` no longer has `workspace`. An old
`?project=<id>` link opens the home's top.

A token in the folder's store (`tokens.scope`) opens:

- the whole folder: no scope, the usual share link (`retake share <folder>`);
- one video: `video:<name>`. A video's own share link opened on the folder's address works this way (its token is in
  that video's store, and counts as `video:<name>`). The home lists that video only, and every other project's routes
  answer 403 "not allowed".

`project:<id>` and `group:<project>/<group>`, the scopes of links made for that organization, are **retired**: what
they opened is gone. The server never guesses a replacement. Such a link is refused everywhere with 410: `/s/<token>`
sets no cookie, and the page routes (`/`, `/p/<name>/`) answer a short page, "This link was for a project or group
that Retake no longer has (folders on disk organize videos now). Ask whoever sent it for a new link."; the API answers
`{"error": "retired link"}`. The token rows stay (revocable, listed by `retake share list`); the owner makes a new
folder or video link. Any other unknown scope opens nothing (403 on a project, an empty home).

## Dropdown appearance and interaction

Comment filters and agent settings use one styled select menu. Both the closed control and the open menu use
Retake's dark surface, rounded border, chevron, selected checkmark and visible hover/focus states. Menus are portaled
above panels, stay within the viewport, and scroll when long. They expose combobox/listbox semantics, support arrow
keys, typeahead, Enter and Escape. Menu keystrokes never start recording or seek the video. Disabled and saving
controls cannot change values. Existing version-picker behavior is unchanged.
