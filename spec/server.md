# Team server (milestone 3b): `retake serve`

This is the contract for a self-hosted server that hosts many projects for a team. Change it here first.

One `retake serve` process hosts many **projects**. Each project is what `retake open` serves for one folder: a version
family, its takes and its project store (spec/api.md, spec/notes.md). People sign in with one-time links (there are
no passwords), guests come in through the share links of spec/team.md, and agents push versions with tokens.

`retake open` and `retake share` don't change.

## Running it

```
retake serve --data DIR [--listen :8080] [--public-url https://review.example.com] [--trust-proxy CIDR]...
             [--database postgres://...] [--s3-endpoint HOST:PORT --s3-bucket B ...] [--ui-dir DIR] [--verbose]
```

Settings come from the flags, else the environment, else `[server]` in config.toml, else the defaults:

| Setting | Flag | Environment | config.toml `[server]` | Default |
|---|---|---|---|---|
| Data folder | `--data` | `RETAKE_DATA` | `data` | required |
| Listen address | `--listen` | `RETAKE_LISTEN` | `listen` | `:8080` |
| Public URL | `--public-url` | `RETAKE_PUBLIC_URL` | `public_url` | none (below) |
| Trusted proxies | `--trust-proxy` (repeatable) | `RETAKE_TRUSTED_PROXIES` (comma-separated) | `trusted_proxies` | none |
| Database | `--database` | `RETAKE_DATABASE_URL` | `database` | SQLite under the data folder |
| S3 endpoint | `--s3-endpoint` | `RETAKE_S3_ENDPOINT` | `s3_endpoint` | none: local files |
| S3 bucket | `--s3-bucket` | `RETAKE_S3_BUCKET` | `s3_bucket` | `retake` |
| S3 keys | none (secrets) | `RETAKE_S3_ACCESS_KEY`, `RETAKE_S3_SECRET_KEY` | `s3_access_key`, `s3_secret_key` | none |
| S3 region | `--s3-region` | `RETAKE_S3_REGION` | `s3_region` | `us-east-1` |
| S3 over plain HTTP | `--s3-insecure` | `RETAKE_S3_INSECURE=1` | `s3_insecure` | false (HTTPS) |
| SMTP | none (secrets) | `RETAKE_SMTP_URL` | `smtp_url` | none: links are printed |
| Mail sender | `--smtp-from` | `RETAKE_SMTP_FROM` | `smtp_from` | `retake@<public host>` |

- `RETAKE_SMTP_URL` is `smtp://user:password@host:587` (STARTTLS, required) or `smtps://user:password@host:465`
  (TLS from the start).
- The admin commands below (`retake user`, `retake project`) read the same settings, so in a container
  `docker compose exec retake retake user add ...` reaches the same stores. They also take `--data`.
- The server listens on plain HTTP. TLS is the reverse proxy's job (deploy/README.md).
- `retake serve` prints the URL, the drivers, and the number of projects and users. With no users yet it says how to
  add the first admin.

### The data folder

```
DIR/
  server.db                    the server store (SQLite driver)
  projects/<name>/             a project's workspace, laid out as retake open expects:
    media/<name>_v1.mp4          the versions (v<N> by file name, spec/api.md)
    media/<name>_v1.review/      their takes, notes.md, notes.json and snaps/
    .retake/project.db           the project store (SQLite driver); jobs.lock
  uploads/<id>.json, <id><ext>   tus uploads in progress (below)
```

A project's workspace is a plain retake project folder: `retake open DIR/projects/<name>/media/<name>_v1.mp4` opens
it, and a folder made by `retake open` can be copied in as a project (`retake project add <name> --from FOLDER`).

## Requests: the public URL, hosts and proxies

- **The public URL** is the origin people use, such as `https://review.example.com`. Sign-in and share links are made
  from it, the CSRF check compares origins with it, and every request's `Host` must be its host (or 403
  `{"error":"bad host"}`, which blocks DNS rebinding). `GET /healthz` and `GET /api/ping` answer any host.
- Without a public URL, any host is accepted, a request's own origin is `<proto>://<host>` (below), and the CLI makes
  links for `http://localhost:<port>`. The server warns about this at startup.
- **Trusted proxies.** Only when the TCP peer's address is in a trusted range (`--trust-proxy 10.0.0.0/8`, or a single
  address) does the server read `X-Forwarded-For`, `X-Forwarded-Proto` and `X-Forwarded-Host`:
  - the client's address is the right-most `X-Forwarded-For` entry that isn't itself a trusted proxy;
  - the request's protocol is `X-Forwarded-Proto` (`http` or `https`), its host `X-Forwarded-Host`, else `Host`.

  From any other peer those headers are ignored: the client is the peer, the protocol is `http`, and the host is
  `Host`. The client's address is what failed attempts are counted against (rate limits below).
- **Cookies are `Secure`.** The one exception is a public URL with `http://` on a host that isn't loopback (a LAN
  test); the server then warns that cookies can be stolen. Browsers keep `Secure` cookies on `http://localhost` and
  `http://127.0.0.1`.
- Every response that depends on who asks gets `Cache-Control: private` (or `no-store`), as in spec/team.md.

## Accounts

There are no passwords. An admin creates users, and each user signs in with a one-time link.

| Field | Holds |
|---|---|
| `email` | the address, lower case, unique |
| `name` | the display name, or "" |
| `admin` | admins create projects and users, and own every project |
| `created`, `disabled` | when; a disabled user can't sign in and their sessions stop working |

### Sign-in links

- `retake user add <email> [--name NAME] [--admin] [--expires 24h] [--json]` creates the user and prints a one-time
  sign-in link, `<public URL>/signin/<code>`. With SMTP set, it mails the link instead, and says so.
- `retake user link <email> [--expires 24h]` makes a new link for an existing user (sessions end, so this is how
  people sign in again).
- A code is `rs_` plus 43 base64url characters (256 random bits). The store keeps its SHA-256 (hex). It works once,
  within its lifetime (default 24 h, at most 7 days).
- `GET /signin/<code>` shows a page with one button, "Sign in as <email>", which posts the code
  (`POST /signin/<code>`). Mail scanners that open links therefore don't use them up. The page has
  `Referrer-Policy: same-origin` (not `no-referrer`, with which browsers send `Origin: null` on the post) and
  `Cache-Control: no-store`.
- `POST /signin/<code>` (origin-checked like every post, below) uses the code, starts a session, sets the cookie and
  answers 303 to `/`.
- A malformed, unknown, used or expired code gives 401 with "This sign-in link has expired or was used. Ask for a new
  one." It counts as a failed attempt (rate limits).
- `GET /signin` is the sign-in page. With SMTP it has an email field: `POST /signin` (form field `email`) mails a link
  when that address has an account, and always answers "If <email> has an account, a sign-in link is on its way."
  Without SMTP it says to ask an admin for a link.

### Sessions

- The cookie is `retake_session=<value>; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=<seconds to its absolute
  expiry>`. The value is `ss_` plus 43 base64url characters. The store keeps its SHA-256.
- A session lasts 30 days at most, and ends after 7 days without use. Last use is written at most once a minute.
- **Rotation.** Signing in always starts a new session. A session's value is replaced every 24 hours of use: the next
  request gets a `Set-Cookie` with a new value, and the old value keeps working for 60 seconds, for requests already
  on their way.
- **Revocation.** `POST /signout` ends the current session, clears the cookie and answers 303 to `/signin`.
  `retake user signout <email>` ends all of a user's sessions; removing or disabling a user ends them too.
  `retake user sessions <email>` lists them (id, created, last use, expiry).
- A request with an expired or revoked session cookie gets the cookie cleared and goes on as anonymous.

## Projects and members

- A project's name matches `^[a-z0-9][a-z0-9-]{0,62}$`. It is unique, and it is the project's address: `/p/<name>/`.
- `retake project add <name> [--title T] [--video FILE | --from FOLDER]` (admins over HTTP: `POST /api/projects`
  `{"name", "title"}`). `--video` copies the file in as `media/<name>_v1<ext>` and records it as pushed v1. `--from`
  copies a retake project folder in as it is (its `.retake/` store too, with the SQLite driver).
- `retake project list [--json]`.
- **Roles.** A member has one role on a project: `viewer`, `reviewer` and `approver` as in spec/team.md, or `owner`:
  an approver's rights, plus managing the project's members, share links and agent tokens. Admins are owners of every
  project.
- `retake project member add <project> <email> --role R`, `retake project member remove <project> <email>`,
  `retake project member list <project>`; over HTTP (owners): `GET /api/projects/<name>/members`,
  `POST /api/projects/<name>/members` `{"email", "role"}`, `DELETE /api/projects/<name>/members/<email>`.
- **Tokens.** Share links and agent tokens are the project store's `tokens` of spec/team.md, made by
  `retake project token create <project> --role viewer|reviewer|approver|agent [--label L] [--expires 30d] [--json]`
  (or `POST /api/projects/<name>/tokens` `{"role", "label", "expires"}` by an owner, which returns the token once).
  A share role prints the link `<public URL>/p/<name>/s/<token>`; an agent token prints the `retake remote add` line
  and the MCP setup for `<public URL>/p/<name>/mcp`. `retake project token list|revoke <project> [<id>]`.

## Who a request is

For a request to `/p/<name>/...`, the first of these that applies:

1. **`Authorization: Bearer <token>`**: a token of the project (spec/team.md). Malformed, unknown, expired or revoked:
   401 `{"error":"bad token"}`. A share cookie or session never counts on `/mcp`, the uploads and the push route.
2. **The session cookie**: the user's role on the project (an admin is `owner`). A signed-in user who isn't a member
   gets 403 `{"error":"not a member"}` (the page route: an HTML page saying so).
3. **The project's share cookie** (`retake_share`, scoped to `Path=/p/<name>/`): the token's role.
4. **None**: the page route answers 303 to `/signin`; anything else 401 `{"error":"sign in required"}`.

The routes of spec/api.md and spec/notes.md work under `/p/<name>/` exactly as they do at `/` of `retake open`, with
the permissions of spec/team.md's route table (watch, record, approve, agent). Server mode adds:

| Route (under `/p/<name>/`) | Needs |
|---|---|
| `GET /s/<token>` | nothing: a share link (below) |
| `OPTIONS, POST /api/uploads`, `HEAD, PATCH, DELETE /api/uploads/<id>` | agent (Bearer) |
| `POST /api/push` | agent (Bearer) |
| `/mcp` | agent (Bearer) |

`/api/info`'s `role` is `viewer`, `reviewer`, `approver` or `owner`.

**The author of a take.** A member's takes get `session.json` `author.name` from their account: the name, else the
email. A share guest's get the token's label, else "guest".

**Share links** work as in spec/team.md, scoped to the project: `GET /p/<name>/s/<token>` sets `retake_share` with
`Path=/p/<name>/` and bounces to `/p/<name>/`. Guests need no account.

### Server routes

| Method | Path | Needs | Does |
|---|---|---|---|
| GET | `/` | a session (else 303 to `/signin`) | The projects page: your projects, with links, and Sign out. |
| GET, POST | `/signin` | nothing | The sign-in page (above). |
| GET, POST | `/signin/<code>` | nothing | A sign-in link (above). |
| POST | `/signout` | a session | Ends it. |
| GET | `/api/me` | a session | `{email, name, admin, projects: [{name, title, role}]}` |
| GET | `/api/projects` | a session | `{projects: [{name, title, role, versions}]}`: the ones you can see |
| POST | `/api/projects` | admin | Creates a project: `{name, title}`. 201 with the project. |
| GET, POST | `/api/projects/<name>/members` | owner | The members; adds or changes one: `{email, role}` |
| DELETE | `/api/projects/<name>/members/<email>` | owner | Removes one |
| GET, POST | `/api/projects/<name>/tokens` | owner | The tokens (never their values); makes one: `{role, label, expires}` returns `{id, token, url}` once |
| DELETE | `/api/projects/<name>/tokens/<id>` | owner | Revokes one |
| GET, POST | `/api/users` | admin | The users; adds one: `{email, name, admin}` returns `{user, link}` |
| POST | `/api/users/<email>/link` | admin | A new sign-in link: `{link}` |
| GET | `/api/ping`, `/healthz` | nothing | `{"ok": true}`; `/healthz` answers 503 `{"ok": false, "error"}` when a store doesn't |
| GET | `/p/<name>` | as `/p/<name>/` | 308 to `/p/<name>/` |

Anything else is 404 `{"error":"not found"}`. An unknown project is 404 `{"error":"no such project"}`. A project with no
version yet answers its page with "No versions yet" and its API with 404 `{"error":"no versions yet"}`, except the
uploads and the push.

## CSRF

- Every `POST`, `PUT`, `PATCH` and `DELETE` that doesn't carry a Bearer token must come from our origin: its `Origin`
  header must equal the public URL's origin (without a public URL: the request's own origin). Without an `Origin`,
  `Sec-Fetch-Site` must be `same-origin` or `none`. Without either, the request is refused. A refused request gets 403
  `{"error":"bad origin"}`.
- This covers the sign-in posts too, so another site can't sign a browser into someone else's account.
- Requests with a Bearer token (agents, `retake push --remote`) carry no ambient credentials and skip the check.
- The second layer is the cookies: `SameSite=Lax` for the session, `SameSite=Strict` for share links.

## Rate limits

As in spec/team.md: a client with 10 failed attempts in the last minute gets 429 `{"error":"too many attempts"}`
with `Retry-After`. A failed attempt is a bad Bearer token, a bad share link or cookie, or a bad sign-in code. `POST
/signin` (mailing a link) is limited to 5 a minute per client, whatever the address.

## Uploads (tus 1.0.0)

The editor works on its own machine, so versions are uploaded. Uploads speak tus 1.0.0
(https://tus.io/protocols/resumable-upload) with the `creation`, `termination` and `expiration` extensions, at
`/p/<name>/api/uploads`, for the project's agent tokens.

- Every request but `OPTIONS` needs `Tus-Resumable: 1.0.0`, else 412 with `Tus-Version: 1.0.0`. Every response
  carries `Tus-Resumable: 1.0.0`.
- `OPTIONS` answers 204 with `Tus-Version: 1.0.0`, `Tus-Extension: creation,termination,expiration` and
  `Tus-Max-Size` (default 64 GiB).
- `POST` creates an upload. `Upload-Length` is required (no deferred length) and at most `Tus-Max-Size` (else 413).
  `Upload-Metadata` must carry `filename` (a video file name: `.mp4 .m4v .mov .webm .mkv`) and `sha256` (the file's
  SHA-256, 64 hex digits), each base64 as tus says; else 400. The answer is 201 with
  `Location: /p/<name>/api/uploads/<id>` and `Upload-Expires`. An id is `up_` plus 22 base64url characters.
- `HEAD /<id>` answers 200 with `Upload-Offset`, `Upload-Length`, `Upload-Metadata`, `Upload-Expires` and
  `Cache-Control: no-store`.
- `PATCH /<id>` needs `Content-Type: application/offset+octet-stream` (else 415) and `Upload-Offset` equal to the
  upload's offset (else 409). The body is appended; whatever arrived before a connection broke is kept, so the client
  asks `HEAD` and goes on from there. A body that goes past `Upload-Length` gets 413, and nothing past the length is
  written. The answer is 204 with the new `Upload-Offset` and `Upload-Expires`.
- When the last byte arrives, the server checks the file's SHA-256. A match completes the upload. A mismatch answers
  460 `{"error":"sha256 mismatch: ..."}` and empties the upload (its offset goes back to 0).
- `DELETE /<id>` answers 204 and removes the upload.
- An unknown id, one of another project, or an expired one is 404. An upload expires 24 hours after its last `PATCH`;
  expired uploads are deleted every hour.
- Uploads live in `DIR/uploads/`: the data in `<id><ext>`, with the file name's extension, and the state in
  `<id>.json` (`{id, project, filename, size, sha256, created, expires}`; the offset is the data file's size).

### Pushing an upload: `POST /p/<name>/api/push`

```json
{"upload": "up_...", "parent": "v3", "label": "", "changes": [{"note": 1, "summary": "..."}],
 "asks": [{"note": 2, "question": "..."}]}
```

- This is `version_push` (spec/notes.md) with the uploaded file, moved into the family. The push rule applies.
- The answer is the push result (200), or `{"error", "code", "notes"}` of internal/agent: 400 `bad_request`, 404
  `not_found`, 409 `push_rule` or `illegal`, 500 `failed`. A complete upload is needed (else 409 `{"code":
  "bad_request"}` saying so).
- A pushed upload is gone. A rejected push keeps it, so the agent fixes its changelog and pushes the same upload again
  without sending the file twice.
- **The first version.** In a project with no versions, the upload becomes `media/<name>_v1<ext>` (or the `label`'s
  number), recorded with no parent; `changes` and `asks` must be empty.
- `by` is the token's label, else "agent".

### Remote MCP

`/p/<name>/mcp` serves the tools of spec/notes.md, as `/mcp` does in spec/team.md (streamable HTTP, stateless,
`notes_wait` at most 50 s). `version_push`'s `file` is `upload:<id>`, an upload of this project; on the server a path
is refused. Paths in results (snapshots, images, versions) are the server's own.

## CLI: remotes

```
retake remote add <name> <project URL> --token <agent token>
retake remote list [--json]
retake remote remove <name>
retake push --remote <name> <file> [--parent vN] [--label vN] [--change ID="summary"]... [--ask ID="question"]... [--json]
```

- A remote is a project URL (`https://review.example.com/p/demo`) and an agent token. Remotes are kept in
  `$XDG_CONFIG_HOME/retake/remotes.toml`, mode 0600 (it holds tokens): `[remote.<name>] url = "...", token = "..."`.
- `push --remote` hashes the file, creates an upload and sends it in 8 MiB `PATCH`es. After a network error it asks
  `HEAD` for the offset and goes on, up to 5 times in a row with backoff. Then it posts `/api/push` and prints
  `pushed v4 from v3` and the URL.
- **Resuming across runs.** The upload's URL is written to `$XDG_STATE_HOME/retake/uploads.json` (default
  `~/.local/state/retake/uploads.json`) as soon as it exists, keyed by the remote, the file's path, size, mtime and
  SHA-256. A second `push --remote` of the same file resumes that upload (or reuses it when it's complete). The entry
  is removed once the push is recorded.

## Storage drivers

There are two parts, each with two drivers, chosen independently.

### Stores: SQLite (default) or Postgres (`--database postgres://...`)

- **The server store** holds `projects`, `users`, `members`, `signin_links`, `sessions` and `settings` (the last
  public URL a server ran with, for the CLI's links).
  - SQLite: `DIR/server.db`.
  - Postgres: the schema `retake`.
- **Project stores** hold each project's tables of spec/notes.md and spec/team.md: `notes`, `threads`, `versions`,
  `jobs`, `events`, `tokens`, `hosts`.
  - SQLite: `DIR/projects/<name>/.retake/project.db`, the very file `retake open` uses.
  - Postgres: one schema per project, `retake_p<id>` (the project's id), with the same tables and columns. The types
    map one to one: `INTEGER PRIMARY KEY AUTOINCREMENT` is `BIGINT GENERATED BY DEFAULT AS IDENTITY`, `INTEGER` is
    `BIGINT`, `REAL` is `DOUBLE PRECISION`, and times stay ISO 8601 text, so every query is the same SQL.
- **Migrations** are forward-only steps, one list per store and dialect. SQLite keeps the step in `PRAGMA
  user_version`; Postgres in a `schema_version` table in each schema. A store newer than the build is refused. The
  SQLite and Postgres lists of a store have the same number of steps (a test checks), so a new step is written for
  both.
- Postgres writes to one project take a transaction-scoped advisory lock (`pg_advisory_xact_lock`), as SQLite's
  `BEGIN IMMEDIATE` serializes them, so event numbers commit in order.

### Media: local files (default) or S3-compatible (`--s3-endpoint`)

- **Local:** the workspace under DIR is the only copy.
- **S3:** every file of a project's workspace except `.retake/` is also stored in the bucket at
  `projects/<name>/<path in the workspace>`: versions, take audio and images, the takes' JSON, transcripts, notes.md,
  notes.json and snapshots. The workspace becomes a cache:
  - The server writes to the workspace first, since the notes engine, ffmpeg and whisper.cpp read local files.
  - A project's files are uploaded when they change: after each take write (`/data`, `/image`, Done), after a push,
    after each job, and every 30 seconds in case something was missed. A file is uploaded again when its size or
    modification time changes.
  - When the server opens a project, it first downloads the files its workspace lacks. So DIR's `projects/` can be
    lost or moved to a new machine without losing anything.
  - The bucket is created if it doesn't exist.
- **Serving.** Versions and take images are **proxied** from S3 with Range support (the same single-range rules as
  spec/api.md), not redirected to presigned URLs:
  - Every byte still goes through the role check, so revoking a token or a membership takes effect at once. A
    presigned URL keeps working until it expires.
  - The browser talks to one origin, so there's no CORS setup, no second public hostname or certificate, and the bucket
    stays on a private network (in the compose file, MinIO isn't published at all).
  - The cost is that video bytes pass through the retake process. For a review team that's small, and a CDN or
    presigned URLs can come later without changing the page.
  - A file that isn't in the bucket yet (uploaded a moment ago) is served from the workspace.

## Jobs

Each project's `transcribe` and `notes` jobs run in the server process, exactly as in `retake open`: one job of each
kind at a time per project, with the `[transcribe]` config (whisper.cpp, CPU or CUDA, spec/notes.md). A job that
fails because no transcriber is set up carries the install hint, and the page shows it.

## The page

The page uses relative URLs (`api/info`, `video/v3`, `./assets/...`), so the same build works at `/` of
`retake open` and at `/p/<name>/` of the server. The server's own URLs (take images in `/api/history` and
`/api/notes`) carry the `/p/<name>` prefix. Nothing else about the page changes.

## Docker

- `Dockerfile`: a multi-stage build: the page (node), a static Go binary with the page embedded (`CGO_ENABLED=0`,
  `-tags webdist`), whisper.cpp's `whisper-cli` built for the CPU, and a runtime image with ffmpeg that runs
  `retake serve --data /data` as a non-root user (uid 10001).
- `deploy/docker-compose.yml`: retake, Postgres and MinIO, with volumes and healthchecks.
- `deploy/README.md`: a self-host guide (Caddy, backups, upgrades, the first admin).

## Deferred

- A web admin page: users, members and tokens are managed with the CLI and the JSON routes.
- Fetching snapshots and take images by a remote agent, and redacting server paths in results and `/api/info`.
- Per-person sidebars.
- Presigned URLs or a CDN for media; several server replicas (uploads and the workspace are local to one server).
- Renaming or deleting projects; an audit log of who used which token.
