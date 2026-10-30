# Sharing a review

`retake share` lets someone else watch a version, or record notes on it, from their own browser or phone. It puts
the review behind an HTTPS tunnel and gives you a link that only works for the person you send it to, for as long as
you choose. Agents on another machine connect the same way, with a token of their own.

The contract (routes, cookies, tokens) is [`spec/team.md`](../spec/team.md). This page is how to use it.

## Share a link

```bash
retake share clip_v6.mp4 --label alex
```

This prints a link like `https://some-words.trycloudflare.com/s/rt_…` and a QR code for a phone. Send the link; it
works until it expires (24 hours by default) or until you stop sharing. Ctrl+C stops sharing.

What happens:

- If `retake open` is already serving that video (on the configured port or the next ones, or on `--port N`), `share`
  uses that server and keeps it from idling out. Otherwise it starts one and stops it with the share.
- It starts a [Cloudflare quick tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/)
  with `cloudflared` (no account needed), and lets the tunnel's hostname in.
- The person who opens the link gets a cookie for that review, and the link's token leaves their address bar.
- The takes they record are saved next to the video like yours, with `--label` as the author, so their notes carry
  their name.

Options:

| Option | Default | Meaning |
|---|---|---|
| `--role viewer\|reviewer\|approver` | `reviewer` | What the link can do (below). |
| `--expires 24h` | `24h` | How long the link works: `90m`, `24h`, `7d`, or `never`. |
| `--label NAME` | none | Who the link is for. It becomes the author of their takes. |
| `--tunnel cloudflare\|tailscale\|none` | `cloudflare` | How the review is reached (below). |
| `--port N` | the config's port | Use the server on port N (or start one there). |
| `--no-qr`, `--json` | | Leave out the QR code; print one JSON line instead. |

## Roles

| Role | Can |
|---|---|
| viewer | Watch the versions, flip between them, read the notes. No Record and no pen; the page says "View only". |
| reviewer | Everything above, and record: talk and draw over the video, and answer the agent's questions. |
| approver | Everything a reviewer can, and approve a version or reopen a note. |

On your own machine (`http://127.0.0.1:…`) nothing changes: no token, everything allowed.

## See and end links

```bash
retake share list clip_v6.mp4          # every token, its role, label and state, and the shares running now
retake share revoke clip_v6.mp4 3      # token #3 stops working at once
```

Stopping a share closes the tunnel, so its link stops working too. The token itself stays valid until it expires or
is revoked, and works again through any later share of the same project, so revoke the links you no longer want.

## Other ways in

**Tailscale.** `retake share clip_v6.mp4 --tunnel tailscale` runs `tailscale serve`, so only devices on your tailnet
can open the link. Add `--funnel` to use `tailscale funnel` and reach anyone on the internet.

**Your own reverse proxy.** If a proxy (Caddy, nginx) already serves `review.example.com` and forwards it to the
retake server, run `retake share clip_v6.mp4 --tunnel none --host review.example.com`. Have the proxy send
`X-Forwarded-For`, so failed attempts are counted per visitor.

**The tunnel binary.** `--tunnel-bin PATH`, or in `~/.config/retake/config.toml`:

```toml
[share]
cloudflared = "/opt/cloudflared/cloudflared"
tailscale = "/usr/local/bin/tailscale"
```

## Connect an agent from another machine

The server speaks MCP over HTTP at `/mcp`, with the same tools as `retake mcp` (notes, questions, pushing versions).
It needs an agent token, also on your own machine:

```bash
retake token create clip_v6.mp4 --role agent --label ci --host some-words.trycloudflare.com
```

That prints the token once, and the setup for Claude Code and Codex:

```bash
claude mcp add --transport http retake https://some-words.trycloudflare.com/mcp --header "Authorization: Bearer rt_…"

export RETAKE_TOKEN=rt_…
codex mcp add retake --url https://some-words.trycloudflare.com/mcp --bearer-token-env-var RETAKE_TOKEN
```

Without `--host`, the address is that of a share that is running now, or else the local server. Agent tokens last 30
days unless you pass `--expires`.

Over HTTP an agent can only push a version file that is inside the project folder (a relative path is relative to
it), and `notes_wait` returns after 50 s at most; the agent calls it again with the same cursor.

## How it stays safe

- A link carries a random 256-bit token. The project keeps only its hash (`.retake/project.db`), so the database
  can't be used to make links.
- Every request through a tunnel or proxy needs a valid token, and each page and API route checks the token's role.
  A viewer can't record even by calling the API directly.
- The cookie is `HttpOnly`, `Secure` and `SameSite=Strict`, and writes must come from the review's own address.
- Repeated bad tokens from one address are refused for a minute.
- Responses through a tunnel are marked `private`, so Cloudflare never caches them.

Not yet (milestone 3b): accounts instead of links, uploads from a remote agent, and hiding the paths of your machine
(`/api/info` still shows the folders the videos are in).
