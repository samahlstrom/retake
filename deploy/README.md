# Self-hosting the team server

`retake serve` is one server for a team: many projects, people who sign in with a link (there are no passwords),
guests with share links, and agents that push new versions from their own machine. This guide runs it with Docker
Compose, next to Postgres for the stores and MinIO for the media. It takes about ten minutes.

The contract (routes, sessions, uploads, drivers) is [`spec/server.md`](../spec/server.md). This page is how to run it.

## What you get

- `retake`: the server, with the review page built in, ffmpeg, and whisper.cpp for transcription (CPU).
- `postgres`: users, sessions, projects, and each project's notes, versions, jobs and tokens.
- `minio`: the versions, the recorded audio, the dropped images, transcripts and notes.

The server listens on plain HTTP on `127.0.0.1:8080`. A reverse proxy in front of it handles HTTPS (Caddy, below).

## What you need

- A machine with Docker and Docker Compose, about 2 GB of RAM, and disk for your videos.
- A hostname that points at it, such as `review.example.com`, for HTTPS.
- A clone of this repository (the image is built from it).

## 1. Set the secrets

Make `deploy/.env` (Compose reads it; it is not committed):

```bash
RETAKE_PUBLIC_URL=https://review.example.com
POSTGRES_PASSWORD=<a long random string>
MINIO_ROOT_USER=retake
MINIO_ROOT_PASSWORD=<another long random string>
```

`RETAKE_PUBLIC_URL` is the address people type. Sign-in and share links are made from it, and requests for any other
host are refused. `openssl rand -base64 30` makes a good password.

## 2. Start it

```bash
docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml ps
```

The first build takes a few minutes (it compiles whisper.cpp). All three services should turn `healthy`. The server
creates its tables and the bucket on its first start.

In the commands below, `dc` stands for `docker compose -f deploy/docker-compose.yml`.

## 3. Add the first admin

```bash
dc exec retake retake user add you@example.com --name "Your Name" --admin
```

This prints a sign-in link, `https://review.example.com/signin/rs_…`. Open it and press **Sign in**. The link works
once, for 24 hours. When your session ends (after 30 days, or a week unused), make a new one:

```bash
dc exec retake retake user link you@example.com
```

Add the rest of the team the same way, without `--admin`. With mail set up (below), the links are mailed instead of
printed, and people can ask for a new one on the sign-in page.

## 4. Make a project and push a version

```bash
dc exec retake retake project add demo --title "Demo"
dc exec retake retake project member add demo alex@example.com --role reviewer
dc exec retake retake project token create demo --role agent --label editor
```

Roles are `viewer` (watch), `reviewer` (and record notes), `approver` (and approve, reopen) and `owner` (and manage
members, share links and tokens). Admins own every project.

The agent token is printed once, with the line to set up the editor's machine. There, with `retake` installed:

```bash
retake remote add review https://review.example.com/p/demo --token rt_…
retake push --remote review demo_v1.mp4
```

The upload resumes if the connection drops, and the server checks the file's SHA-256. The first push becomes v1; later
pushes follow the push rule, as `retake push` does locally ([agents.md](../docs/agents.md)). The project is at
`https://review.example.com/p/demo/`.

An agent such as Claude Code reaches the project's tools at `https://review.example.com/p/demo/mcp` with the same
token. `retake project token create` prints the setup.

Guests without an account get a share link:

```bash
dc exec retake retake project token create demo --role reviewer --label "Sam (client)" --expires 7d
```

## Put HTTPS in front

[Caddy](https://caddyserver.com) gets and renews the certificate on its own. On the same machine, a `Caddyfile`:

```
review.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Caddy keeps the `Host` header and adds `X-Forwarded-For` and `X-Forwarded-Proto`. The server only believes those
headers from addresses you trust. Without that, it still works, but every visitor counts as the proxy for the
rate limits of failed sign-in links and tokens. Seen from inside the container, the proxy is Docker's bridge, so add to
`deploy/.env`:

```bash
RETAKE_TRUSTED_PROXIES=172.16.0.0/12
```

If the proxy runs on another machine, trust its address instead. Never trust a range that the internet can reach the
server from directly.

The published port stays on `127.0.0.1`, so only the proxy reaches the server. To publish it on all interfaces (a
proxy on another machine), set `RETAKE_BIND=0.0.0.0`.

## Transcription

The image has whisper.cpp's `whisper-cli`, built for x86-64 CPUs with AVX2 (2013 and later; build with
`--build-arg WHISPER_CPU=portable` for older ones). A model downloads on first use into the `retake-data` volume. On a
CPU, pick a small model in a `deploy/config.toml`:

```toml
[transcribe]
model = "small"   # tiny, base, small, medium, large-v3-turbo, large-v3
```

and mount it by uncommenting the `config.toml` line under `retake` in the compose file. Until a model works, the jobs
fail with a hint that the page shows, and the takes are kept. The image has no GPU build. For CUDA, run
`retake serve` on the host after `scripts/install-whisper.sh --cuda` ([config.md](../docs/config.md#transcribe)).

## Mail

To mail sign-in links, add to `deploy/.env`:

```bash
RETAKE_SMTP_URL=smtp://user:password@smtp.example.com:587     # STARTTLS; smtps://…:465 for TLS from the start
RETAKE_SMTP_FROM=review@example.com
```

## Backups

Postgres and the bucket hold everything. The `retake-data` volume is a cache of the media (the server downloads what's
missing when it starts), plus uploads in progress and the whisper models.

```bash
# the stores
dc exec -T postgres pg_dump -U retake retake > retake-$(date +%F).sql
# the media: mirror the bucket into ./backup/media
docker run --rm --network retake_default -v "$PWD/backup:/backup" \
  -e MC_HOST_src="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@minio:9000" \
  --entrypoint mc pgsty/minio:RELEASE.2026-08-04T00-00-00Z mirror --overwrite src/retake /backup/media
```

Take the database dump first: a version in the bucket that the database doesn't know yet is harmless, the other way
round is not. To restore, load the dump into an empty `retake` database (`psql -U retake retake < retake.sql`) and
mirror the folder back into the bucket.

## Upgrades

```bash
git pull
dc up -d --build
```

The server migrates its tables when it starts. Migrations only go forward, and a newer schema refuses to run with an
older server, so back up first. The volumes keep everything across rebuilds.

## Check a build

`deploy/smoke.sh` runs the whole stack end to end and removes it after (containers, volumes and the image): it adds
an admin, signs in through the link in a headless Chromium, makes a project and an agent token, pushes a generated
video with `retake push --remote`, records a take, waits for its transcription and notes jobs, and looks for the
take's audio in MinIO. It needs `bin/retake` (`make build`), ffmpeg and the e2e suite's node modules.

## Without Postgres and MinIO

For a small team, one container is enough: SQLite and the media live in one volume.

```bash
docker build -t retake .
docker run -d --name retake --restart unless-stopped -p 127.0.0.1:8080:8080 -v retake-data:/data \
  -e RETAKE_PUBLIC_URL=https://review.example.com retake
docker exec retake retake user add you@example.com --admin
```

Back up the volume (`/data`: `server.db` and `projects/`) while the container is stopped, or copy the SQLite files with
`sqlite3 … .backup`. Each `projects/<name>/` folder is an ordinary retake project that `retake open` can open.
