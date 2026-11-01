# Hosting Retake

Host Retake when other people should review without being at your computer. The server holds the review: the page,
the comments and the versions. Your computer keeps the footage, the project and your agent CLI, and connects out to
the server, so there is nothing to open on your router. **Your footage never uploads:** the server asks your computer
for the few seconds of video someone is watching.

Working alone? You don't need any of this. `retake open ~/Videos` on your own computer is the whole setup.

## 1. Start the server

On the host, with Docker, in a clone of this repository:

```bash
cd deploy
echo RETAKE_PUBLIC_URL=https://review.example.com > .env    # the address people will use
docker compose up -d
docker compose logs retake                                  # shows the setup link
```

That's one container and one volume. Put HTTPS in front with Caddy: [`deploy/Caddyfile`](../deploy/Caddyfile) is
ready, change the hostname. Any other proxy must pass `/api/events` through unbuffered and allow responses of 30
seconds or more.

## 2. Create your account

Open the setup link from the log, `https://review.example.com/setup?code=su_…`, and enter your name and email. You are
the owner. The link works once, and only while the server has no accounts, so nobody else can claim a fresh server.
There are no passwords: later you sign in with links.

## 3. Connect your computer

The next page shows one line with a token. Run it in the folder that holds your video projects:

```bash
cd ~/Videos
retake connect https://review.example.com --token rk_…
```

Each project shows up on the server with its name and first frame, nothing else. Its agents and renders run here.
Leave it running while people review; new project folders are picked up on their own. Next time, plain
`retake connect` reconnects. **Connect your computer** on the home page makes a new line any time.

That's it. Open a project on the server, record a review, press Send.

## What your computer needs

- `retake` and `retake-edit` (the renderer), and Claude Code or Codex, signed in. Their own logins and settings are
  used; nothing is copied to the server.
- Projects with an edit timeline (`edit/timeline.json`, or `[edit] timeline` in `retake.toml`). A folder of finished
  videos without one is skipped; `retake push --remote` uploads a version of those instead.
- Linux or macOS. Keep `retake connect` running: while your computer is offline, the server can't play its video.

## How a review moves

A comment is **Open** until it goes to the agent: press Send, or **Finish review** with Send on. It is **Working**
while the agent edits, **Question** when the agent needs you (answer it and it goes back to Working), and **Done**
once a new version rendered. The new version streams from your computer while it renders, and the page follows it.
Pause, retry or switch the agent from the Agent panel; switching keeps the files and the history.

## Agents

By default `retake connect` offers the Claude Code and Codex it finds on your PATH. For other models or wrappers, pass
`--profiles profiles.json`: a list of `{"id", "name", "command": [...]}`. The command reads the task on stdin and
writes a JSON report (`state`: `completed` or `question`, and `message`) to `$RETAKE_REPORT`. Commands only ever come
from this file, never from the server. Task logs and renders stay in each project's `.retake/worker/`.

## Good to know

- Invite people per project: an account (`docker compose exec retake retake user add EMAIL`, then
  `retake project member add`) or a share link (`retake project token create PROJECT --role reviewer`).
- One server process holds a project's video relay: don't run replicas behind a load balancer.
- `retake worker --remote NAME --project DIR` still connects a single project with that project's agent token.

Postgres and MinIO, transcription, mail, backups and upgrades: [`deploy/README.md`](../deploy/README.md). The
contracts: [`spec/server.md`](../spec/server.md) and [`spec/automation.md`](../spec/automation.md).
