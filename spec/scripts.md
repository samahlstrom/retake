# Scripts: from an idea to the first cut (extension, `retake-ext/1`)

This is the contract for Scripts, the first extension (spec/extensions.md). Change it here first.

Scripts is where a video starts. You talk to the agent in the chat beside one Markdown document (spec/chat.md): it
shapes your idea into an outline and a script in that document while you watch, pulls in links and images, and asks
with choices when it should. When you like the script, Send builds the first cut from it. The
document is **v0** of the video: one plain file in the video's folder, `v0/<video>_v0.md`, next to `v1/<video>_v1.mp4`
and the cuts that follow.

It is bundled (`extensions/scripts/`) but off until turned on: `retake ext enable scripts`, or on a hosted server
`--extensions scripts`. It adds no core feature of its own: everything it uses is the extension API.

The name the page shows ("Scripts") is the manifest's `name` and `[nav] title`, and nowhere else: the page code says
`ctx.ext.name`. A script's document is still "your idea" in the copy; the feature is Scripts.

**It was called Ignition.** The old id keeps working wherever an id is read (spec/extensions.md, "Renamed ids"):
`[extensions] enabled = ["ignition"]`, `[extensions.ignition]`, `retake ext enable ignition`, `--extensions ignition`,
a person's `{"ignition": false}`, a worker that reports `ignition`, the kind `ignition.doc` and the routes under
`ext/ignition/` and `api/ext/ignition/`. Nothing on disk was keyed by the id (`v0/` and `.retake/history/` aren't), so
there is nothing to move.

## The manifest

```toml
api = 1
id = "scripts"
name = "Scripts"
version = "1.0.0"
description = "Talk out an idea, shape it into a script, and start the video."

[ui]
module = "ui/scripts.js"
style = "ui/scripts.css"

[nav]
title = "Scripts"
icon = "ui/script.svg"
list = "kind-only"
action = { title = "New script", route = "new" }

[[kinds]]
id = "doc"
folder = "v0"
marker = ["*_v0.md", "idea.md"]   # the document; idea.md: a v0 from before it was one file (migrated, below)
label = "Script"
status = "Idea"
record = true
chat = true                       # the agent chat in place of the comments (spec/chat.md)

[[slots]]
slot = "page"

[[slots]]
slot = "version"
kind = "doc"

[[slots]]
slot = "sidebar-tab"
title = "Script"
when = "has-kind"

[files]
owns = ["v0/**"]
read = "watch"
write = "record"
create = "owner"

[agent]
instructions = "agent/task.md"
context = "agent/context.md"
```

No server and no tools: the agent edits one Markdown file with what it already has.

## v0: one document

```
deadlock/                         a project
  jump-souls/                     the video's folder
    v0/
      jump-souls_v0.md            the document: everything about the video before its first cut
      voice/                      talk-outs, out of sight: 20261101-190211.webm, .words.json, .md (the sidecar)
      cuts/v1.json                which sections of the document a version covers, and where (below)
    footage/ …                    the video's own files, as today
    v1/jump-souls_v1.mp4          the first cut
```

- **The document is the truth.** `v0/<video>_v0.md`, named like the cuts (`<video>` is the video folder's name for a
  new idea; a v0 made next to existing cuts uses their name, `clip` for `clip_v1.mp4`). Plain Markdown, UTF-8, no
  front matter, no markers, no hidden state. It starts with `# <title>`. Edit it in any editor; Retake shows outside
  edits within 2 seconds. It diffs and merges in git like any text. A `v0/` holding one `*_v0.md` is Scripts'; with
  more than one, `<folder>_v0.md` wins, else the first by name.
- **Nothing else shows.** The audio of a talk-out is kept in `v0/voice/` with its words and a sidecar
  (`<stamp>.md`: the audio's name and the text it gave), so the document stays plain. `v0/cuts/` is for the agent and
  the Script tab.
- **Before it was one file.** A `v0/` with `idea.md` and no `*_v0.md` is migrated the first time Scripts opens it:
  it writes `v0/<folder>_v0.md` with `idea.md`, then `outline.md`, then `script.md` (each file's text as it is, the
  outline and the script under `## Outline` and `## Script` when they don't start with a `#` heading of their own),
  and leaves the old files where they are. From then on it reads only the one file.
- A folder with only `v0/` is a video with one version: an **idea**. Its home card shows the chip "Idea" on an empty
  thumbnail box, and it has no status dot.

## New script

The + on the Scripts section (or "New script" on its page) opens `#ext/scripts/new`: a title, and the project to put
it in (a select of the tree's projects; the one selected in the tree, else the top). **Create** calls
`ctx.createVideo`, which makes `<project>/<slug>/v0/<slug>_v0.md` holding `# <title>` and a blank line, and opens the
new video's page on v0 with the caret under the title. The slug is the title in lower case with every run of other
characters turned into `-`, at most 63 characters, `-2`, `-3`, … when the folder exists. **Create and talk** does the
same and starts recording.

Creating needs the owner (`create = "owner"`). Hosted, it goes to the computer that holds the project (the machine
channel), and is disabled with "The editing computer is offline" when it isn't connected.

## The editor (the version slot)

Where the video would be, on v0: **the document, and nothing else.** One editor, the full width of the area, in a
readable column (about 70 characters) at a comfortable reading size (17 px, 18 px on a phone), in the Theme API's
tokens. No tabs and no toolbar.

- **Live preview** (as Obsidian does it): the editor is CodeMirror 6 editing the raw text, so the file is exactly what
  was typed, never rewritten. The Markdown is shown rendered: headings at their sizes, bold, italic, inline code,
  bullet and numbered lists with a hanging indent, task boxes (`- [ ]`) you tap to tick (that changes only the `[ ]`
  to `[x]`), links that open on a tap, `>` quotes and `---` rules. The markup characters (`#`, `**`, list markers,
  `>`, a link's brackets and address) are hidden except on the lines the caret or the selection touches, where the
  text is shown as typed. A bare `[picture or sound]` (no address) stays as typed, a little quieter, so script cues
  keep their brackets. Line breaks are kept as they are (LF or CRLF).
- **Images.** `![caption](images/x.png)` (a path relative to `v0/`) shows the image inline: the column's full width
  on a phone, rounded, with the caption under it; on the caret's line the Markdown shows instead. Pasting or dropping
  an image saves it as `v0/images/<name>` (a new name when one is taken, 25 MiB at most) and puts its Markdown at the
  caret. Hosted, images go through the same owned-file routes as the document.
- **Image decisions.** A question the agent asks about a place in the document with image options (`retake ask --file
  v0/<video>_v0.md --heading "Cold open" --option "Ink@images/ink.png" --option "Neon@images/neon.png" "Which look?"`)
  also shows inline, right after its heading or line: the question and its images side by side (stacked on a phone).
  Tapping one answers it; once answered it goes away. (A question asked from a chat turn shows in the chat instead,
  spec/chat.md.)
- **Embeds.** A web address alone on its own line shows what it points to: a player or a post for the sites below,
  a quiet card for any other page ("Embeds", below).

- **Saving.** 600 ms after the last keystroke and when the text loses focus, with `If-Match` on the version read. The
  only word it shows is a quiet "Not saved · Retry" when a save fails, or "Changed outside Retake · Reload · Keep
  mine" when the file changed under unsaved typing (Keep mine writes over it). Outside edits with nothing unsaved
  go in as the smallest edit, keeping the caret on its word. Undo (Ctrl+Z) only undoes your typing.
- **⋯**, one quiet button at the top right of the document: **Undo the agent's edit** (with when, while there is one
  to undo), and on a phone **Type in the script** (below). Nothing else is over the text. (Start v1 is gone: Send does
  it.)
- **The chat** is core's (spec/chat.md): the column beside the document (the sheet on a phone) is the chat with the
  agent, in place of the comments. A script has no To do or Done. The agent is the main editor now: you ask, it edits,
  the document reloads in place while you watch (the caret and the scroll stay where they are).
- **Questions.** An older question the agent asked about a heading from a comment's task (`retake ask --file`) still
  shows in the document under its place, with its choices; a tap answers it. Questions from the chat show in the chat.
- **Keys.** Typing goes to the text. Esc leaves the text, and then R records a voice message for the chat. The page's
  other review keys do nothing here.
- **A phone** shows the document from the top of the screen to the bottom bar, which holds the chat's pill ("Ask the
  agent…", with its mic) and Send. Tapping the pill opens the chat sheet with the keyboard up; the sheet covers the
  bar and stays above the keyboard, and its header has the version. Reading never raises the keyboard: the document
  is read-only to the keyboard until ⋯ → **Type in the script** (⋯ → **Done typing** goes back). The top row is back on
  the left, and on the right the chat's bubble and ⋯. Text is at least 17 px and wraps inside the screen; nothing
  scrolls sideways. The bottom bar stays above Safari's toolbar and the home indicator (`viewport-fit=cover`, the
  safe-area insets, `dvh`). Phone checks run in Chromium and in WebKit (iPhone device descriptors).

`ctx.onAnchor(fn)` (an addition to the page API): the version slot tells core where a typed comment points (the
heading above the caret). A script has no comment line now, but the API stays for other kinds.

## Embeds

The agent pulls in links as it shapes the script (references, clips, a tweet, a mood board), and they should read as
what they are, not as addresses. So in a script's document, and only there:

- **A web address alone on its own line** (`https://…`, or `<https://…>`, nothing else on the line, outside code)
  shows as an embed under that line, in the document's column, in the live preview and in a viewer's read-only one.
  Off the caret's line only the embed shows; on it the address shows as typed, with the embed still under it (the
  same element, so a playing video doesn't restart). ArrowDown and ArrowUp step onto an embed's line, since off the
  caret it has no height of its own. A link inside a sentence stays an ordinary link.
- **Known sites** become a frame (`<iframe>`) on the site's own embed address. They are one table in
  `extensions/scripts/src/lib/embed.ts`; adding a site is one entry:

  | Site | Addresses | Frame |
  |---|---|---|
  | YouTube | `youtube.com/watch?v=ID`, `youtu.be/ID`, `/shorts/ID`, `/live/ID`, `/embed/ID`, with `t=` or `start=` (`90`, `1m30s`) | `https://www.youtube-nocookie.com/embed/ID?start=S`; 16:9, Shorts 9:16 |
  | Vimeo | `vimeo.com/ID`, `vimeo.com/ID/HASH`, `player.vimeo.com/video/ID` | `https://player.vimeo.com/video/ID?h=HASH`; 16:9 |
  | Loom | `loom.com/share/ID` | `https://www.loom.com/embed/ID`; 16:9 |
  | Twitch | clips (`clips.twitch.tv/SLUG`, `twitch.tv/<channel>/clip/SLUG`), VODs (`twitch.tv/videos/ID`, with `t=`) | `https://clips.twitch.tv/embed?clip=SLUG&parent=<host>`, `https://player.twitch.tv/?video=ID&parent=<host>&autoplay=false&time=…`; `<host>` is the page's own (`location.hostname`); 16:9 |
  | X / Twitter | `x.com/<user>/status/ID`, `twitter.com/…` | `https://platform.twitter.com/embed/Tweet.html?id=ID`; 550 px wide at most, 560 px tall |
  | Instagram | `instagram.com/p/CODE`, `/reel/CODE`, `/tv/CODE` | `https://www.instagram.com/p/CODE/embed` (`reel/`, `tv/`); 400 px wide at most, 600 px tall |
  | TikTok | `tiktok.com/@<user>/video/ID` | `https://www.tiktok.com/embed/v2/ID`; 340 px wide at most, 700 px tall |
  | Spotify | `open.spotify.com/[intl-xx/]<track\|album\|playlist\|episode\|show\|artist>/ID` | `https://open.spotify.com/embed/<type>/ID`; 152 px tall for a track or an episode, else 352 px |
  | SoundCloud | `soundcloud.com/<user>/<track>`, `/<user>/sets/<set>` | `https://w.soundcloud.com/player/?url=<address>&visual=false`; 166 px tall, a set 450 px |
  | Figma | `figma.com/<file\|design\|proto\|board>/KEY/…` | `https://www.figma.com/embed?embed_host=retake&url=<address>`; 16:9 |
  | Google Docs, Slides, Sheets | `docs.google.com/<document\|presentation\|spreadsheets>/d/ID/…` | `https://docs.google.com/<…>/d/ID/preview`; Slides 16:9, the others 560 px tall |

  An address of these sites that isn't in the table (a channel, a profile) is a card.
- **A raw frame**: a line that is only `<iframe src="https://…" …></iframe>` (the way sites hand out embed code) shows
  as that frame, rebuilt rather than passed through. Only its `src` (https, and never this page's own host), `width`,
  `height` and `title` are read; every other attribute is dropped. Anything that doesn't read as one frame stays as
  text, a little quieter, as HTML does now.
- **Every frame** the page makes, known or raw, has exactly `sandbox="allow-scripts allow-same-origin allow-popups
  allow-presentation"`, `allow="fullscreen; picture-in-picture; encrypted-media"`,
  `referrerpolicy="strict-origin-when-cross-origin"`, `loading="lazy"`, a `title`, and its `src`. Retake's own pages
  are never framed.
- **Its box is reserved** before anything loads, so nothing moves: the column's full width (less for the narrow posts
  above), at the site's ratio or height. A raw frame takes its ratio from `width` and `height` when both are numbers
  (clamped between 1:3 and 4:1), its height alone when only that is a number (`width="100%" height="352"`), else 16:9.
- **Any other address** is a quiet link card: the page's title, its site and its picture, in a box of fixed height
  (the picture at the right, cropped). Until the preview comes, and when there is none, it shows the address's host
  and path in the same box. Tapping it opens the address in a new tab (`noopener`). The preview comes from
  `GET api/link-preview` (spec/api.md), read by the server that serves the page, so it works the same hosted.
- **On a phone** frames and cards are never wider than the screen and nothing scrolls sideways.
- **Content-Security-Policy.** The review page sends none today. A page that adds one must allow these frames
  (`frame-src https:`) and the cards' pictures (`img-src https:`).
- The Script tab next to a video stays text: an address line there reads as its address.

## Talking it out

R (or the chat's mic) records a **voice message** for the chat (spec/chat.md, "Images and voice"): REC, the timer and
the red edge work as on a video, there is no pen. When it stops, the audio is saved as
`v0/voice/chat/<yyyymmdd-hhmmss>.webm` (never picked up as a talk-out), transcribed with the editing computer's `[transcribe]`, and its words are sent to
the agent as your message. It makes no comment and adds nothing to the document by itself: the agent works it in.
Without transcription set up, the audio is kept and the job's hint shows in the chat.

A kind without `chat` keeps the talk-out of before (the words go into the document at the caret with a sidecar in
`v0/voice/`, and an Open comment asks the agent to work them in); Scripts has `chat = true`.

## Shaping it with the agent

You ask in the chat; each message is a turn of the agent (spec/chat.md, "Turns"). Its prompt has the chat's rules, and
before them Scripts' `agent/context.md` and `agent/task.md`:

- `v0/<video>_v0.md` is this video's plan, one document. The person's own words under its title are theirs: don't
  rewrite or remove them.
- Images go in `v0/images/` and into the document as `![caption](images/<name>.png)`; for a decision between looks,
  ask with image options (`--option "Ink@v0/images/ink.png"`), and the person picks one in the document.
- Shape it in that file: a `## Outline` section with the beats, then a `## Script` section with the same beats as `###`
  headings, what's said as paragraphs and what's shown in `[brackets]`. Keep the person's phrasing where it works.
  Don't make other files.
- Edit in place. Keep headings stable, because they're anchors. Move text rather than delete it, and list anything cut
  under `## Cut` at the end.
- When unsure, ask about a place in the document and carry on:
  `retake ask --note 12 --file v0/jump-souls_v0.md --heading "Cold open" --option "Start on the jump" "Which opening?"`.

The agent answers in a few words in the chat; the document reloads through the watch, so you see it change. When it
asks with choices (`retake ask --option …`, no `--note`), they show in the chat to tap.

**Undo the agent's edit.** Before every agent attempt and every chat turn on a video that has `v0/`, Retake keeps a
copy of v0's files (core's history, spec/extensions.md "Agent edit history"). ⋯ → "Undo the agent's edit" puts the
document back as it was before the newest agent edit, and the next undo goes one further back. When the document
changed since, it refuses with "Changed since the agent's edit". Git stays optional.

## Send: "I like it, make the video"

Send on the script makes the video (spec/chat.md, "Send on a chat version"), with no confirmation; the chat's status
line says what started ("Building v1…").

- **No cut yet:** a comment on v0 with `"wants": "version"`, "Build the first cut (v1) from the doc." (the kind's label),
  sent at once. It makes v1.
- **Cuts exist:** a comment on the newest cut at 0:00, "Bring the video in line with the doc's changes
  (v0/<video>_v0.md).", which goes into the next version by the batch rule: comments on vN become edits in vN+1, one
  agent per edit batch.
- **Nothing changed** since the last Send (the document's sha256 is the one sent, and its comment is working or
  done): Send is dimmed and says "Nothing changed since the last Send". Never a silent no-op.

The task's prompt for the first cut adds:

- Build v1 from the document's `## Script`, section by section in order, with the footage and assets in the video's
  folder (not `v0/voice/`).
- A plain project (no timeline): write the video to `$RETAKE_OUTPUT`. The worker pushes it as `v1/<folder>_v1.mp4`.
- A timeline project (`edit/timeline.json` or `retake.toml`): edit the timeline, and the worker renders v1 as for any
  task.
- Then write `v0/cuts/v1.json`.

The comment is Done with v1, the page follows it to v1, and the video's review starts there. v0 stays one click away.

## Later versions link back to the script

`agent/context.md` goes into every task of a video that has `v0/`: follow the document's script unless the notes say
otherwise, and with each new version write `v0/cuts/<label>.json`:

```json
{ "version": "v3",
  "sections": [ { "heading": "Cold open", "from": 0, "to": 12.4 },
                { "heading": "The trick", "from": 12.4, "to": 61.0 } ] }
```

`heading` is a heading of the document's script, without its `#`s. Times are seconds in that version. A section that
isn't in the cut is left out. **On a video** (the Script sidebar tab, on videos that have v0): the script read-only,
with the section under the playhead highlighted; clicking a section seeks to its start.

## In the nav and the version strip

- **The Scripts section**, under the folder tree: its header row opens the scripts page (`#ext/scripts/`) and its +
  makes a new one. Its rows are the ideas (videos with only v0), most recently edited first.
- **The scripts page** (headed with the manifest's `name`): New script, then each idea's title (the document's `# `
  line, else the folder's name), its project, when it was last edited and its first two lines. Clicking one opens it.
- **The version strip and picker** list `v0 · Script` first. A video with a cut opens on its newest cut, as today.
  Following latest and comparing (C) never land on v0.

## Local and hosted

- **Local** (`retake open`): the files are read and written on disk, the server's `[transcribe]` transcribes, and the
  in-process worker runs the agent.
- **Hosted** (`retake serve` + `retake connect`): the files stay on the editing computer. The server reads and writes
  them through the video's worker, the same relay that plays its video. A talk-out's audio passes through the server
  to the computer and isn't kept on the server. The computer's `[transcribe]` transcribes it, and its worker runs the
  agent with its own copy of Scripts' instructions. While the computer is offline, v0 shows "The editing computer is
  offline", and nothing is cached on the host in this version.
- Roles: a viewer reads v0, a reviewer edits it, talks and sends, and the owner makes new scripts.

## What stays out

- **Your own skills, prompts and workflows.** They belong to your agent's setup. Your Claude Code or Codex loads them as
  it always does, and Scripts never names, installs or calls them. Its instructions are neutral and short.
- Generated voices, text-to-speech or voice-over. The voice is the person's own.
- Editing the video by its transcript, a project board, titles, thumbnails, publishing. Those are other extensions, or
  none.
- Rich text, a documents database, and syncing with Google Docs or Notion.

## Tests

- Go: v0 with `*_v0.md` (and the old `idea.md`) found by the scan (an idea folder, a video with v0 and cuts, a `v0/`
  without either, a `v0/clip_v0.mp4`); a marker given as a list of globs; `api/info` and `api/home` (`no_cut`,
  `kind`); New script's folder and slug collisions, locally and through the machine channel; the first cut in a
  v0-only plain project pushing `v1/<folder>_v1.mp4`; the chat and Send (spec/chat.md, "Tests").
- Web unit: the document's name, the migration's text, the heading above the caret, the talk-out paragraph and its
  sidecar, the playhead section.
- Web unit: embeds: each site's addresses to its frame (and what stays a card), the raw frame's sanitizer, the
  address-alone-on-a-line rule; Go: the link preview route against a local test server (title, `og:` tags, the size
  and time limits, the cache) and the address guard. Nothing in a test reaches the internet.
- e2e `e2e/tests/22-embeds.spec.ts` and `22-embeds-webkit.spec.ts` (desktop, and an iPhone in WebKit): a YouTube
  address, a raw frame and an unknown page in a document: the frames' `src` and attributes, the card with a stubbed
  preview, no sideways scroll, nothing moving while the card loads. Every request off the test server is answered
  by the test, so no site is loaded.
- e2e `e2e/tests/18-scripts.spec.ts`, with a fake transcriber (a command engine that prints fixed words) and a fake
  agent, on a phone and on a desktop:
  - New script opens the one document, typing saves, and an outside edit shows.
  - A voice message goes to the chat; the agent writes the outline and script into the document while the page shows
    it and asks "Which opening?" with two pictures; a tap answers it; ⋯ undoes the agent's edit; a link typed in the
    chat goes in alone on its line.
  - Send builds v1 (an MP4 and `cuts/v1.json`); v1 opens; back on the script, Send says nothing changed.
  - On a phone: the pill and Send in the bar, the chat sheet with its composer focused, reading without the keyboard,
    ⋯ → Type in the script.
  - A multi-file v0 from before is migrated into one file and the old files stay.
  - The same flow hosted (`retake serve` and `retake connect` on scratch folders).
