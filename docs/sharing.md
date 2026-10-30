# Sharing and team mode

<!-- STUB (team mode worker): share links, tokens, accounts and roles, remote MCP. Replace this page. -->
> **Stub.** Team mode (milestone 3) is being built. Its worker replaces this page with the real commands.

The plan, from [roadmap.md](roadmap.md):

- `retake share` puts a review behind an HTTPS link, so someone else can watch and record from their own machine or
  phone.
- Accounts and roles decide who can review, who can push and who can approve.
- The agent connects to a shared project over remote MCP instead of a local `retake mcp`.
- Each person's comments stay their own append-only takes, merged by time. There is no real-time co-editing.

## Until then

A single server can already be reached through an HTTPS tunnel. Allow the tunnel's hostname:

```bash
retake open clip_v2.mp4 --no-open --allow-host my-tunnel.example.com
```

Anyone with that URL can watch every version and record takes, so share it only with people you trust. A phone needs
HTTPS for the microphone, so this is also how you review on a phone today.

## Share links

<!-- STUB (team mode worker): `retake share`. -->
> **Stub.** `retake share` goes here.

## Accounts, roles and tokens

<!-- STUB (team mode worker). -->
> **Stub.**

## Remote MCP

<!-- STUB (team mode worker). -->
> **Stub.**
