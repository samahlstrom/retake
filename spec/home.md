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
retake open <folder> [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--allow-host H]... [--verbose]
```

When the argument is a folder instead of a video, the server serves every project in it. `--versions` takes a video,
so it can't be used with a folder (exit 2).

### Which folders are projects

A *project* is a version family with its root, as in spec/notes.md ("Project root"). The candidates are the folder
itself and its immediate subfolders (not hidden ones, not `*.review`). A candidate `C` is a project when it holds a
**versioned video** whose project root is `C`:

- a file with a family video extension (`.mp4 .m4v .mov .webm .mkv .m3u8`) whose stem has a version number
  (`clip_v3`, spec/api.md "Detection"), found directly in `C` or in one of `C`'s own subfolders (not hidden, not
  `*.review`; one level, where version folders are: `C/v3/clip_v3.mp4`);
- and `project.Root(file) == C`. So `deadlock/item-shop/v6/itemshop_v6.mp4` makes `item-shop` a project (its `v6`
  folder is a version folder), `deadlock/macro/macro_v2_roughcut.mp4` makes `macro` one, and a folder of
  unnumbered clips (`broll/`) is none.

The project's **anchor** is the video its family is detected from: of those files, the highest version number, a plain
name (`clip_v3`) before one with more after the number (`clip_v3_draft`), then the newest. Its server follows the
newest version of the family (as `retake serve` does), so a version pushed later shows up without a restart. A root
with two families (`a_v1.mp4` and `b_v1.mp4` side by side) shows the one with the newest version.

The folder is scanned again on every `GET /api/home` (at most once every 2 seconds) and when a request names a
project it doesn't know, so new projects appear on the home without a restart.

**Names.** A project's name is its folder's name made URL-safe, the team server's rule
(`^[a-z0-9][a-z0-9-]{0,62}$`): lower case, every run of other characters becomes `-`, cut to 63 characters, and
`video` if nothing is left. When two folders give the same name, the later one in name order gets `-2`, `-3`, ….
Its title is the folder's name as it is.

### Routes

The same routing as the team server (spec/server.md): `/` is the home, and each project is the existing review page
and API (spec/api.md, spec/notes.md, spec/bridge.md) under `/p/<name>/`, for its newest version.

| Method | Path | Needs | Does |
|---|---|---|---|
| GET/HEAD | `/`, `/index.html` | watch | The home screen (the page's `home.html`). |
| GET | `/api/home` | watch | The projects (below). |
| GET | `/api/ping` | any role | `{"ok": true}` |
| GET/HEAD | `/assets/*` and other page files | watch | The page's static files, as at `/p/<name>/`. |
| GET | `/s/<token>` | nothing | A share link for the whole home (below). |
| GET | `/p/<name>` | as below | 308 to `/p/<name>/` |
| any | `/p/<name>/...` | spec/team.md's table | The project's review page and API, exactly as `retake open <video>` serves them at `/`. |
| GET | `/favicon.ico` | nothing | 204 |

Anything else is 404 `{"error":"not found"}`; an unknown project is 404 `{"error":"no such project"}`.

**Who a request is.** As spec/team.md, with the folder in place of the project: local requests (`127.0.0.1:<port>`
or `localhost:<port>`, no proxy header) without a token are the `owner`, with no accounts. A request through a shared
host (an `--allow-host` name, or one in the folder's hosts table) needs a token of the folder (below), from
`Authorization: Bearer` or the `retake_share` cookie; its role applies on the home and on every project. On
`/p/<name>/...` a Bearer token of that video's own store works too (an agent token from `retake token create
<video> --role agent` on `/p/<name>/mcp`). Existing single-video share links and cookies continue working when the
main address becomes a workspace: their hosts are recognized, and their tokens open a filtered home containing
only that video and its containing project/group. They never grant access to another video's API, media, notes,
events, or worker routes. The token remains in its original store, so expiry and revocation keep working. Hosts,
origins, the 401/403/429 answers, rate limits and `Cache-Control: private` are those of spec/team.md.

### `GET /api/home`

```json
{
  "title": "deadlock",
  "folder": "/abs/path/deadlock",
  "role": "owner",
  "projects": [
    { "name": "item-shop", "title": "item-shop", "url": "p/item-shop/", "version": "v6", "versions": 6,
      "duration": 412.5, "pushed": "2026-10-28T19:02:11Z", "status": "to_review", "takes": 0, "open": 0,
      "stream": null, "thumb": "p/item-shop/api/poster" }
  ]
}
```

- `title` is the folder's name. `folder` (its path) is sent to the owner only. `role` is the request's role.
- `projects` is sorted **to review first, then by newest push**, then by name.
- A project: `url` and `thumb` are relative to the home. `version` is the newest version's label ("" before the
  project's server can read one), `versions` how many there are, `duration` the newest version's (or null).
  `pushed` is when the newest version arrived: its row in the project store's `versions` table (a push), else the
  file's modification time, as RFC 3339 UTC.
- `status` is `to_review`, `reviewed` or `being_edited` (below). `takes` is the number of closed takes on the newest
  version, `open` the number of its notes that are `open` or `question`.
- `stream` is the newest version's streaming state when the streaming side reports one, else null:
  `{"state": "pending" | "rendering" | "complete", "done": n, "total": m}` (below).
- `thumb` is the newest version's poster (below).

The team server (spec/server.md) answers `GET /api/home` too, for a signed-in user: their projects, with a `user`
field `{email, name}` and no `folder`. Its projects page at `/` stays as it is.

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
No `v` means the current version; an unknown label is 404 `{"error":"no such version"}`; a version that has no frame
yet is 404 `{"error":"no poster"}`. It needs watch.

1. The streaming side's poster, when it exists: `<stem>.poster.jpg` beside the version's file
   (`v4/clip_v4.poster.jpg` for `v4/clip_v4.m3u8`).
2. Else a frame at t = 0, made once with ffmpeg (`-ss 0 -frames:v 1`, at most 640 px wide) and cached under the
   project root's `.retake/thumbs/` as `<sha256 of path, size and modification time>.jpg`. A changed file gets a new
   one. A file ffmpeg can't read isn't tried again by the same process.

The answer has `Content-Type: image/jpeg`, `Last-Modified` and `Cache-Control: no-cache`, and answers
`If-Modified-Since` with 304. The home's `thumb` and the review page's `<video poster>` are the same URL, so the image
the home showed is the one the page shows while the video loads: it feels like it's already playing.

## The home screen

The page's second entry, `home.html`, in the review page's look: the dark background, the same fonts and colors,
nothing more than the videos.

- A grid of cards, one per project (one column on a phone). A card is a link to `p/<name>/` with the thumbnail (16:9,
  a fixed box, so nothing moves when it loads), the title, the newest version's label and length, and the status
  chip (plus the streaming chip).
- The header says how many are to review. With no projects: "No videos here yet", and what a video's file name
  looks like.
- It reads `api/home` on load and every 15 s while visible (which also keeps an `--idle-exit` server up).
- A viewer's link sees the same home; their review pages are view only (spec/team.md).
- Test hook: `window.__home = { loaded, projects }`.

## Moving through projects (the review page)

Only when the page is a project of a home: its path ends in `/p/<name>/` and `<home>api/home` answers. Then:

- **"← All videos"**, a small link at the top left of the video, goes to the home.
- **"Next video →"** shows at the top right after Done, when another project is `to_review`: the first one in the
  home's order other than this one. It goes to that project's page.
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
- `share list`, `share revoke` and `token create` take the folder in place of a video and work on the folder's store.

## Tests

- Go: discovery in a folder (version folders, flat families, the folder itself, hidden, `.review` and unnumbered
  files left out, names and collisions), the status rules, thumbnail caching (made once, remade for a changed file,
  the poster beside the file first), `/api/home` (status, thumbnail URL, newest version, order), `api/poster` and
  unwatch, and share tokens scoped to the folder.
- e2e `e2e/tests/12-home.spec.ts`: a folder with three generated projects. The home lists them with thumbnails and
  statuses; a card opens its review page with the poster; a take and Done make it Reviewed; "Next video" and `>` go
  to the next one to review; the home and the controls work on a phone.

## Project workspace and in-app sharing

The home is a project workspace. Its hierarchy is project → freely named groups → videos → versions. Groups describe organization; review/render status is separate. Existing detected version families remain videos at their existing `/p/<name>/` URLs. Media and saved takes are never moved when organizing them.

The main application address opens **Projects**. It never automatically opens a video, including when there is
only one project or video. Clicking a project opens `/?project=<id>`, showing its groups and video cards; clicking
a video opens its existing review URL. Reload, browser Back and direct project links retain this selection. The
player has a **Projects** link and a link to its containing project. The folder-backed application is served on
the main address; the standalone `open <video>` command remains an explicit review-only tool, not the app launcher.
Empty projects stay visible and offer organization controls rather than dropping the user into an editor.

`GET /api/home` retains `projects` (the legacy video cards) and adds `workspace: {revision, projects:[{id,title,groups:[{id,title}]}], placements:[{video,project,group}]}`. With no saved organization there is one default project named after the opened folder and an Ungrouped section. Newly discovered videos join that default project. Owners can create and rename projects/groups and move videos through the UI. Search and status filtering work across groups. The player exposes a visible Projects link back to this workspace.

`POST /api/workspace` saves the organization with its expected revision. It is owner-only and origin checked. Invalid memberships and unknown video IDs return 400; stale revisions return 409 so another person's edits are never silently overwritten. State lives in the home store and is shared across browsers, rather than in localStorage. Reads for a scoped share expose only included videos and organization.

`POST /api/shares` creates a share with `{role:"reviewer"|"viewer", scope:"project:<id>"|"group:<project>/<group>"|"video:<name>", label?:string}`. The owner chooses Share then Copy link. Default access is can-comment, expiry is seven days, and scope is explicit. The server reuses an active public host or starts the configured HTTPS tunnel; it never presents a localhost address as an external share link. The response is `{url,id,expires,role,scope}`. `DELETE /api/shares/<id>` revokes a link. Secrets are returned only on creation and stored only as hashes. Scoped tokens are checked on every media, notes, recording and event request, not just hidden from the home. Moving a video out of a shared project/group removes that link's access to it. Guests cannot create shares or change organization.

A video opened standalone also has `POST /api/shares` for that video family. `retake open <video> --home-url URL` optionally supplies the owner's Projects navigation when an existing workspace is served separately; guest responses omit this owner-only address. The folder-hosted player uses its normal relative Projects link.

The current account-based team server keeps its existing membership model. Organization metadata and token scopes use the shared store's SQLite/Postgres schema; this change does not claim a new team administration interface.

## Dropdown appearance and interaction

Project filters and video placement controls use the same styled select menu as comment filters and agent
settings. Both the closed control and the open menu use Retake's dark surface, rounded border, chevron, selected
checkmark and visible hover/focus states. Menus are portaled above panels, stay within the viewport, and scroll
when long. They expose combobox/listbox semantics, support arrow keys, typeahead, Enter and Escape, and retain
the last saved placement if saving fails. Menu keystrokes never start recording or seek the video. Disabled and
saving controls cannot change values. Existing version-picker behavior is unchanged.
