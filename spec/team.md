# Team mode, slice 1 (milestone 3a): sharing a review, and remote agents

This is the contract for sharing a review over an HTTPS tunnel with tokens and roles, and for agents that reach the
project over HTTP (remote MCP). Change it here first.

One `retake` server still serves one version family on `127.0.0.1` (spec/api.md). Sharing puts that same server
behind a tunnel, and everyone who comes through the tunnel must carry a token. **Local use doesn't change:** a request
to `127.0.0.1:<port>` or `localhost:<port>` without a token has full access, exactly as in milestone 1.

## Roles

| Role | Can | Through |
|---|---|---|
| `viewer` | watch: the page, the versions, history, the sidebar, events, take images | a share link |
| `reviewer` | watch, and record: takes (data, audio, images, Done), and Send to the agent. Answering a question is recording. | a share link |
| `approver` | everything a reviewer can, plus approve a version and reopen a note | a share link |
| `agent` | agent operations only: the `/mcp` route (notes_list, notes_wait, note_ask, note_set, version_push, versions_list). Not the page. | `Authorization: Bearer` |
| `owner` | everything the page does (an approver's rights). Not a token role: it is a local request without a token. | localhost |

Permissions by route:

| Route | Needs |
|---|---|
| `GET/HEAD /`, `/index.html`, page files, `/favicon.ico`, `/video...`, `/api/info`, `/api/history`, `/api/notes`, `/api/events`, `/api/takes/...`, `/api/poster`, `/api/link-preview`, unknown GET paths (still 404), `POST /api/versions/<label>/watch` and `/unwatch` (spec/bridge.md, "Streaming") | watch |
| `GET/POST /api/download`, `GET/HEAD /video/<label>/download` (spec/api.md, "Download") | watch, or record with `[server] downloads = "reviewers"`; nobody with `"off"` |
| `GET /api/ping` | any role |
| `POST /api/session/<take>/data`, `/audio`, `/image`, `/done`, unknown POST paths (still 404) | record |
| `POST /api/notes/<id>/reopen`, `POST /api/versions/<label>/approve` | approve |
| `/mcp` (any method) | agent, by Bearer token only, local requests too |
| `/api/worker/*` | agent, by Bearer token only, local requests too (spec/automation.md) |
| `GET /api/automation` | watch |
| `POST /api/automation/tasks`, `POST /api/automation/tasks/<id>/answer` and `/retry` | record |
| `POST /api/automation`, other `POST /api/automation/tasks/<id>/<action>` | owner only |
| `GET /s/<token>` | nothing: it is how a token arrives |

A role without the permission gets 403 `{"error":"not allowed"}`.

## Tokens

- A token is 256 random bits, written `rt_` plus 43 base64url characters. It is printed once, when it is made.
- The project store keeps only its SHA-256 (hex), in the `tokens` table (forward-only migration 2):

  | Column | Holds |
  |---|---|
  | `id` | the token's number, which `share list` shows and `share revoke` takes |
  | `hash` | SHA-256 of the token, hex, unique |
  | `role` | `viewer`, `reviewer`, `approver` or `agent` |
  | `label` | who it's for ("alex"), or "" |
  | `created` | when it was made |
  | `expires` | when it stops working; "" = never |
  | `revoked` | when it was revoked; "" = it wasn't |

- A token is valid while it isn't revoked and, if it has an expiry, before it.
- A reviewer's or approver's takes get the token's label as `session.json` `author.name` (spec/api.md), so each
  person's notes carry their name. Without a label, the server's own name is used, as before.
- The same migration adds the `hosts` table: `host` (unique), `tunnel` (`cloudflare`, `tailscale` or `none`), `pid`
  (the `retake share` process that added it), `created`. A share adds its public hostname while it runs and removes
  it when it stops. Rows of dead processes are pruned by the next `retake share`.

## Hosts

Every request is one of:

- **Local:** the `Host` header is `127.0.0.1:<port>` or `localhost:<port>`, and the request carries no proxy header.
- **Shared:** the `Host` is an `--allow-host` name, or one in the `hosts` table. Also a local `Host` that carries a
  proxy header: `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `Forwarded`, `X-Real-IP`,
  `CF-Connecting-IP`, `CF-Ray` or `Tailscale-User-Login`. A proxy can rewrite `Host` to the backend address
  (`tailscale serve` has done so), and such a request must not pass as local.
- **Anything else:** 403 `{"error":"bad host"}`, as before (DNS rebinding).

Origins accepted on writes: `http://127.0.0.1:<port>`, `http://localhost:<port>`, and `https://<h>` for every shared
host `h`, from flags or the table. The other POST rules of spec/api.md stay.

## Authentication

- A token comes from `Authorization: Bearer <token>` (any request), from the `retake_share` cookie (shared requests
  only), or from a `/s/<token>` link.
- **Local:** no Bearer token means `owner`. A Bearer token must be valid, and then its role applies. Cookies are ignored
  on local requests: cookies aren't scoped by port, so a share cookie for `127.0.0.1` would reach every local server.
- **Shared:** no token gives 401 `{"error":"token required"}`. An invalid one (malformed, unknown, expired or revoked)
  gives 401 `{"error":"bad token"}` and clears the cookie. Both send `WWW-Authenticate: Bearer`. The page route (`/`,
  `/index.html`) answers with a short HTML page instead: "This review needs a share link" or "This link has expired".
- **`/mcp`:** always needs a valid Bearer `agent` token. A cookie never counts there.
- Refused requests (400-level answers of the checks above: bad host, 401, 403, 429) don't count as contact for the
  idle timer.
- A shared response's `Cache-Control` gets `private`, so a CDN in front of the tunnel never caches a response meant
  for one token.

## Share links

`GET /s/<token>`:

- Valid: `Set-Cookie: retake_share=<token>; Path=/; Max-Age=<seconds until it expires, at most 400 days>; HttpOnly;
  Secure; SameSite=Strict`, and a 200 HTML page that replaces the location with `/` (`Referrer-Policy: no-referrer`,
  `Cache-Control: no-store`). Not a redirect: a link tapped on another site starts a cross-site navigation, and a
  `SameSite=Strict` cookie isn't sent along a cross-site redirect chain. The bounce page's own navigation is
  same-site, so the page then loads with the cookie, and the token leaves the address bar.
- Invalid: 401 HTML ("This link has expired or was revoked"), counted as a failed attempt.
- The verbose request log writes a share link as `/s/<token>`, never the token itself.
- The cookie's path is `/` because the server serves one review at its root. On the team server, which serves several
  projects, each share cookie is scoped to its project's path, `/p/<name>/` (spec/server.md).

## Rate limiting

- A failed attempt is a presented token that is malformed, unknown, expired or revoked (on any route). Requests
  without a token aren't attempts.
- Failures are counted per client: `CF-Connecting-IP`, else the last `X-Forwarded-For` entry, else the remote
  address. A client with 10 failures in the last minute gets 429 `{"error":"too many attempts"}` with `Retry-After`
  for any request that presents a token, before the token is looked up, until its oldest failure is a minute old.
- At most 4096 clients are tracked; the stalest is dropped first.
- Behind a reverse proxy that sends no forwarding header (`--tunnel none`), every client counts as the proxy's
  address. Tokens can't be guessed (256 bits); the limit only keeps a flood of bad ones off the store.

## `/api/info`

Adds `role`: `owner`, `viewer`, `reviewer` or `approver`.

## The page

- **viewer:** no Record, no Done, no pen colors, no undo, no Answer. The hint says "View only". R, a drag on the
  video, or an image drop shows the toast "View only". Nothing else changes.
- **reviewer:** the normal page, without Reopen.
- **approver, owner:** the normal page with Reopen (and Approve, when the page has one).
- A page from an older server (no `role`) behaves as `owner`.

## Remote MCP

- `/mcp` on the same server and port as the page, so a share's tunnel reaches it too. It speaks the streamable HTTP
  transport of the official Go SDK (`github.com/modelcontextprotocol/go-sdk`), stateless.
- The tools are those of `retake mcp` (spec/notes.md), with limits for an agent on another machine:
  - `version_push`'s `file` must be inside the project root (a relative path is relative to the root), so a token
    can't pull in, or move away, files from elsewhere on the server's disk.
  - `notes_wait`'s `timeout` is at most 50 s (default 50 s): Cloudflare ends a response that sends nothing for 100 s.
    The agent calls it again with the same cursor.
- Paths in results (snapshots, images, versions) are paths on the server's machine. Uploads and serving those files
  over HTTP come in milestone 3b.

## CLI

```
retake share <video> [--role reviewer] [--expires 24h] [--label L] [--tunnel cloudflare|tailscale|none]
                     [--host H] [--funnel] [--tunnel-bin PATH] [--port N] [--no-qr] [--json]
retake share list <video> [--json]
retake share revoke <video> <id> [--json]
retake token create <video> --role R [--label L] [--expires 30d] [--host H] [--json]
retake token list <video> [--json]          (the same as share list)
retake token revoke <video> <id> [--json]   (the same as share revoke)
```

**`retake share`**:

1. Finds the server for `<video>`'s family: a running `retake` server on the configured port or the next ones (as
   `open` tries them; with `--port N`, only N) whose `/api/info` has a `role` field (a server that knows tokens) and
   lists `<video>` among its versions. It attaches to that one. Otherwise it starts one, like `open --no-open
   --idle-exit 0`, which stops when the share stops.
2. Starts the tunnel and reads its public hostname:
   - `cloudflare` (default): `cloudflared tunnel --no-autoupdate --url http://127.0.0.1:<port>`, and the first
     `https://<name>.trycloudflare.com` it prints.
   - `tailscale`: `tailscale serve http://127.0.0.1:<port>` (the tailnet only), or `tailscale funnel` with
     `--funnel` (the internet), in the foreground, and the first `https://<name>.ts.net` it prints.
   - `none`: no tunnel, for a reverse proxy that is already there. `--host` names its hostname.
   - The binary is `--tunnel-bin`, else the config's `[share] cloudflared` or `tailscale`, else the one on `$PATH`.
3. Adds the hostname to the `hosts` table, so the server accepts it.
4. Makes a token (`--role` viewer, reviewer or approver; default reviewer; `--expires` default 24h) and prints
   `https://<host>/s/<token>` with a QR code (`--no-qr` leaves it out), the token's id and how to revoke it.
5. Keeps the attached server from idling out (it pings `/api/ping` every 30 s), and runs until Ctrl+C, the tunnel
   exiting, or the server going away. Then it stops the tunnel (and the server it started) and removes the host.
   The token stays valid until it expires or is revoked.

`--json` prints `{id, role, label, expires, host, url, port, attached}` as one line once the link works, then runs
the same way. **`retake token create`** makes a token without a tunnel and prints it once. For `agent` tokens it
also prints the setup for Claude Code and Codex:

```
claude mcp add --transport http retake https://<host>/mcp --header "Authorization: Bearer <token>"
export RETAKE_TOKEN=<token>
codex mcp add retake --url https://<host>/mcp --bearer-token-env-var RETAKE_TOKEN
```

`<host>` is `--host`, else the newest live share host of the project, else `http://127.0.0.1:<port>`. Durations are
Go durations (`90m`, `24h`) or days (`7d`); `0` or `never` means no expiry.

**A whole folder.** `retake share <folder>` (and `share list`, `share revoke`, `token create` with a folder) shares the
home of `retake open <folder>`: one link for every project in it, with tokens kept in the folder's own store
(spec/home.md, "Sharing the home").

Config additions:

```toml
[share]
cloudflared = ""   # the cloudflared binary; "" = $PATH
tailscale = ""     # the tailscale binary; "" = $PATH
```

## Deferred to milestone 3b

Milestone 3b ([server.md](server.md)) does accounts, Postgres and S3 with Docker Compose, uploads, and several
projects per server with per-project share paths and cookies. What it leaves is listed there, under "Deferred".
The list as it stood:

- Accounts: people who sign in, instead of a token per link. Per-person sidebars.
- Postgres and S3 instead of SQLite and the local disk, and Docker Compose.
- Uploads: a remote agent pushing a file from its own machine, and serving snapshots and take images to it.
- `/api/info` and the take files still carry absolute paths of the owner's machine; a shared request should get them
  redacted.
- Several projects on one server, with per-project share paths and cookies.
- A named, fixed Cloudflare tunnel (`cloudflared tunnel run`), and an audit log of who used which token.

### Share controls in the review UI

Owners can create viewer/reviewer links from the player, without using the CLI. Links last seven days. A folder link opens the whole folder or one video (`video:<name>`, spec/home.md "Share scopes"); the token's optional `scope` is enforced for every request. Links made for the removed project organization (`project:`/`group:` scopes) are refused with 410 and a page that says why. Older unscoped tokens retain their existing access. Tokens remain hashed and revocable. UI-created tunnels live with the serving process and stop when it stops. A link is reported ready only after the public host is available. No email or invitation is sent automatically.

Comment deletion and restoration (`POST /api/notes/<id>/delete|restore`) require approve permission, like reopening
feedback. Viewers, reviewer-only links and editing agents cannot delete other people's review history.
