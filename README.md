<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/media/brand/wordmark-dark.svg">
    <img src="docs/media/brand/wordmark-light.svg" alt="Retake." width="280">
  </picture>
</p>

<p align="center"><b>Talk to your video.</b><br>Watch it, talk and draw over it, press Send. Your own AI agent makes the next version.</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
  <img src="https://img.shields.io/badge/macOS%20%C2%B7%20Linux%20%C2%B7%20Windows-lightgrey" alt="macOS, Linux and Windows">
</p>

![You watch v4, press R, talk and draw three notes, and press Send. One agent on your computer edits the project files, only the parts those edits changed render again, and each note turns done as it lands in v5.](docs/media/how-it-works.svg)

## Install

**macOS and Linux**

```sh
curl -fsSL https://raw.githubusercontent.com/samahlstrom/retake/main/install.sh | sh
```

**Windows**

```powershell
irm https://raw.githubusercontent.com/samahlstrom/retake/main/install.ps1 | iex
```

**npm** (any OS, Node 18+)

```sh
npm i -g retake-cli
```

**Homebrew**

```sh
brew install samahlstrom/retake/retake
```

> [!NOTE]
> The first release is on its way. Until it's out, [build from source](CONTRIBUTING.md).

## Get started

```sh
retake
```

Retake opens in your browser and asks two things: where your videos are, and which AI agent to use. It finds Claude
Code or Codex if you have them, or shows you how to sign in.

Then open a video, press **R**, and talk and draw over it. Press **Send**, and the agent works through your notes one
by one, each landing in the next version as it's done. Press **C** to flip between the old and new version.

## Bring your own agent

![Claude Code, Codex or any command-line agent plugs into the same socket. You pick it in Settings, per video, or for one Send.](docs/media/any-agent.svg)

Retake works with the agent you already pay for: Claude Code, Codex, or any agent that runs in a terminal. It uses
your subscription or API key. Watch it work in [herdr](https://herdr.dev) (included), tmux, or in the background.

## Works with any tool

![The agent reaches ElevenLabs over an API, image generation over MCP, and Blender and ffmpeg on the command line. Each result is a file, and Retake watches the files.](docs/media/any-tool.svg)

Whatever your agent can use, Retake can use: MCP servers, command-line tools, APIs like ElevenLabs. Retake only
watches the files they change and renders what changed.

## Your footage stays put

![The browser talks to Retake, and the footage renders on the machine it lives on, your computer or a box you host.](docs/media/two-halves.svg)

Run it on your computer, or host it for a team and review from anywhere, phones included. Footage stays on the
machine it lives on and renders there; the browser gets the stream. Share one video with a link from its ⋯ menu.

## Public feedback

Set `RETAKE_FEEDBACK_TOKEN` on the Retake server to send the in-app feedback form to GitHub. Use a fine-grained token
limited to `samahlstrom/retake` with only **Issues: Read and write**; the token never reaches the browser. Without it,
the form opens a prefilled GitHub issue instead. See [Config](docs/config.md#public-feedback).

## Docs

- [Quickstart](docs/quickstart.md): the whole loop on a sample project
- [Guide](docs/guide.md): keys, the phone, agents, the timeline editor, sharing
- [Agents](docs/agents.md) · [Hosting](docs/hosting.md) · [Config](docs/config.md) · [Windows](docs/windows.md)
- [Contributing](CONTRIBUTING.md) · [Changelog](CHANGELOG.md)

## License

[Apache-2.0](LICENSE). Bundled third-party software is listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
