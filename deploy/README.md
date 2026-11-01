# Self-hosting reference

The short version is [docs/hosting.md](../docs/hosting.md): `docker compose up -d`, create the owner account in the
browser, then run the **Connect your computer** line on the computer that holds your projects. This page has the
details. The contract (routes, sessions, tokens, drivers) is [`spec/server.md`](../spec/server.md).

## What runs

One container, `retake`: the server with the review page built in, ffmpeg, and whisper.cpp for transcription (CPU).
One volume, `retake-data` at `/data`: the SQLite stores, each project's comments, takes and posters, uploads in
progress and the whisper models. Your footage isn't there: it stays on your computer, and the server relays the video
someone is watching from it (`retake connect`).

The server listens on plain HTTP on `127.0.0.1:8080`. A reverse proxy in front handles HTTPS.

You need a machine with Docker Compose, about 2 GB of RAM, a hostname that points at it, and a clone of this
repository (the image is built from it; the first build takes a few minutes, it compiles whisper.cpp).

## Settings

`deploy/.env` (Compose reads it; it is not committed):

```bash
RETAKE_PUBLIC_URL=https://review.example.com   # the address people use; links are made from it
RETAKE_TRUSTED_PROXIES=172.16.0.0/12           # the proxy, as the container sees it (below)
# RETAKE_BIND=0.0.0.0                          # publish the port on every interface (a proxy on another machine)
# RETAKE_PORT=8080
```

Requests for any host other than the public URL's are refused.

## The first account

While the server has no accounts it prints a setup link when it starts:

```bash
docker compose logs retake      # https://review.example.com/setup?code=su_…
```

The link creates the owner (an admin) and works once. A restart prints a new one. Once anyone has an account, `/setup`
is gone. From a shell on the host this works too: `docker compose exec retake retake user add you@example.com --admin`.

## HTTPS

[`Caddyfile`](Caddyfile) is ready: change the hostname and run Caddy on the same machine. Caddy gets the certificate
on its own. Whatever proxy you use must pass the event stream (`/api/events`) through unbuffered, allow responses of
30 seconds or more (relayed video, agents waiting for notes) and any request size; the Caddyfile lists the nginx
equivalents.

The server only believes `X-Forwarded-*` headers from addresses in `RETAKE_TRUSTED_PROXIES`. Without them it still
works, but every visitor counts as the proxy for the rate limits of failed links and tokens. Seen from the container,
a proxy on the host is Docker's bridge, `172.16.0.0/12`. Never trust a range the internet reaches the server from.

## People

The owner connects their computer from the home page. Others, in `dc` = `docker compose exec retake`:

```bash
dc retake user add alex@example.com --name Alex                  # prints a one-time sign-in link
dc retake project member add intro alex@example.com --role reviewer
dc retake project token create intro --role reviewer --expires 7d   # a share link, no account needed
```

Roles: `viewer` (watch), `reviewer` (and record), `approver` (and approve, reopen), `owner` (and manage). Admins own
every project. Sessions last 30 days (a week unused); `retake user link EMAIL` makes a new sign-in link.

## Transcription

The image has whisper.cpp's `whisper-cli` for x86-64 CPUs with AVX2 (build with `--build-arg WHISPER_CPU=portable` for
older ones). A model downloads on first use into the volume. Pick a small one in `deploy/config.toml` and uncomment
its line in the compose file:

```toml
[transcribe]
model = "small"   # tiny, base, small, medium, large-v3-turbo, large-v3
```

There is no GPU build in the image; for CUDA run `retake serve` on the host ([config.md](../docs/config.md#transcribe)).

## Mail

To mail sign-in links instead of printing them, add to `deploy/.env`:

```bash
RETAKE_SMTP_URL=smtp://user:password@smtp.example.com:587     # STARTTLS; smtps://…:465 for TLS from the start
RETAKE_SMTP_FROM=review@example.com
```

## Postgres and MinIO

For a bigger team, the `scale` profile keeps the stores in Postgres and the media in MinIO. In `deploy/.env`:

```bash
COMPOSE_PROFILES=scale
POSTGRES_PASSWORD=<a long random string>
MINIO_ROOT_PASSWORD=<another one>
RETAKE_DATABASE_URL=postgres://retake:${POSTGRES_PASSWORD}@postgres:5432/retake?sslmode=disable
RETAKE_S3_ENDPOINT=minio:9000
```

`docker compose up -d` then starts all three. The volume becomes a cache of the media. `openssl rand -base64 30`
makes a good password.

## Backups

One container: stop it and copy the volume, or copy the SQLite files with `sqlite3 … .backup`. Each
`/data/projects/<name>/` is an ordinary retake project folder.

With the scale profile, dump the database first, then mirror the bucket:

```bash
docker compose exec -T postgres pg_dump -U retake retake > retake-$(date +%F).sql
docker run --rm --network retake_default -v "$PWD/backup:/backup" \
  -e MC_HOST_src="http://retake:$MINIO_ROOT_PASSWORD@minio:9000" \
  --entrypoint mc pgsty/minio:RELEASE.2026-08-04T00-00-00Z mirror --overwrite src/retake /backup/media
```

## Your look

The page's look is a theme folder in the volume, `/data/theme/` (docs/theming.md), so an upgrade never changes it:

```bash
docker compose cp ./my-theme retake:/data/theme   # theme.css, theme.toml, fonts, a logo
```

The server reads it on every page load. `RETAKE_THEME` in the compose file points somewhere else, or `none` for the
built-in look.

## Upgrades

```bash
git pull
docker compose up -d --build
docker compose exec retake retake theme check /data/theme   # with a theme: anything this version doesn't use
```

The server migrates its tables when it starts. Migrations only go forward, so back up first. The server also prints
a `retake: theme:` warning at startup for anything in your theme the new version doesn't use.

## Check a build

`deploy/smoke.sh` runs the scale stack end to end and removes it after: it adds an admin, signs in through the link in
a headless Chromium, makes a project and an agent token, pushes a generated video with `retake push --remote`,
records a take, waits for its transcription and notes, and looks for the take's audio in MinIO. It needs `bin/retake`
(`make build`), ffmpeg and the e2e suite's node modules.
