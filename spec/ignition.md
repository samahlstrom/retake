# Ignition: from an idea to the first cut (extension, `retake-ext/1`)

This is the contract for Ignition, the first extension (spec/extensions.md). Change it here first.

Ignition is where a video starts. You talk out the idea, the agent shapes your talk-outs into an outline and a script,
and when the script is right the agent builds the first cut from it. The documents are **v0** of the video: plain
files in the video's folder, tied to every version made from them.

It is bundled (`extensions/ignition/`) but off until turned on: `retake ext enable ignition`, or on a hosted server
`--extensions ignition`. It adds no core feature of its own: everything it uses is the extension API.

## The manifest

```toml
api = 1
id = "ignition"
name = "Ignition"
version = "1.0.0"
description = "Talk out an idea, shape it into a script, and start the video."

[ui]
module = "ui/ignition.js"
style = "ui/ignition.css"

[nav]
title = "Ignition"
icon = "ui/spark.svg"
list = "kind-only"
action = { title = "New idea", route = "new" }

[[kinds]]
id = "doc"
folder = "v0"
marker = "idea.md"
label = "Script"
status = "Idea"
record = true

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

No server and no tools: the agent edits Markdown files with what it already has.

## v0 in a video's folder

```
deadlock/                         a project
  jump-souls/                     the video's folder
    v0/
      idea.md                     the pitch, in your words. Its presence makes v0 Ignition's.
      outline.md                  the beats: one ## heading each, a line or three under it
      script.md                   the script: one ## section per beat; paragraphs are what's said and shown
      refs/                       anything you collect: links.md, images, clips, PDFs
      voice/
        20261101-190211.webm      a talk-out (Opus, as the page records takes)
        20261101-190211.words.json   its words with times (core's transcription)
        20261101-190211.md        its transcript as plain text
      cuts/
        v1.json                   which script sections a version covers, and where (below)
    footage/ …                    the video's own files, as today
    v1/jump-souls_v1.mp4          the first cut
```

- **The files are the truth.** Plain Markdown (headings are `#` lines), UTF-8, no front matter and no hidden state. Edit
  them in any editor; Retake shows outside edits within 2 seconds (the watch lease). They diff and merge in git like
  any text. What Ignition keeps for itself (the doc that was open, the caret) is in the browser's `localStorage`.
- `idea.md` starts with `# <title>`. Only `idea.md` is required; the others appear when first written.
- A folder with only `v0/` is a video with one version: an **idea**. Its home card shows the chip "Idea" on an empty
  thumbnail box, and it has no status dot.

## New idea

The + on the Ignition section (or "New idea" on its page) opens `#ext/ignition/new`: a title, and the project to put
it in (a select of the tree's projects; the one selected in the tree, else the top). **Create** calls
`ctx.createVideo`, which makes `<project>/<slug>/v0/idea.md` holding `# <title>` and a blank line, and opens the new
video's page on v0 with the caret under the title. The slug is the title in lower case with every run of other
characters turned into `-`, at most 63 characters, `-2`, `-3`, … when the folder exists. **Create and talk** does the
same and starts recording.

Creating needs the owner (`create = "owner"`). Hosted, it goes to the computer that holds the project (the machine
channel), and is disabled with "The editing computer is offline" when it isn't connected.

## The editor (the version slot)

Where the video would be, on v0:

- **Header.** The documents as tabs: Idea · Outline · Script · Refs · Voice. Outline and Script are always there and
  their file is made on first edit. Right of the tabs: **Start v1** (while the video has no cut), and a quiet
  saved-state word in a fixed place, so nothing moves.
- **Text.** Plain text, Markdown as typed. Heading lines are larger and bold, and nothing else is styled. No toolbar,
  no preview, no rich text. One readable column (72 characters wide), in the Theme API's tokens (`--surface`, `--text`,
  `--font-ui`; `--agent` for the agent's marks).
- **Saving.** 600 ms after the last keystroke and when the text loses focus, with `If-Match` on the version the editor
  read.
- **Outside edits.** A changed file with no unsaved typing reloads in place, keeping the caret's line. With unsaved
  typing, one line over the text: "Changed outside Retake · Reload · Keep mine" (Keep mine writes over it).
- **Refs.** The files in `refs/` (name, size): drop files to add them (25 MiB each), click to open (images inline,
  others download). `refs/links.md` is a document like the others.
- **Voice.** The talk-outs, newest first: when, how long, play, the transcript, and **Insert at caret**.
- **The comments column** is core's, with v0's comments (To do · Done) and Send, as on a video.
- **The ask box**, under the text: "Ask the agent…". It names what it's about: "About: Cold open" (the heading above
  the caret; × makes it about the whole document). Enter adds an Open comment anchored there (`ctx.comments.add`).
  Ctrl+Enter (⌘+Enter) adds it and sends it.
- **Keys.** Typing goes to the text. Esc leaves the text, and then R talks, as on a video. The Record button always
  works. The page's other review keys do nothing here, because there's no video.

## Talking it out

R (or Record) records a **talk-out**. REC, the timer, the red edge and the level work as on a video. There is no pen.

When it stops:

1. Ignition saves the audio as `v0/voice/<yyyymmdd-hhmmss>.webm` and asks for its transcript (`ctx.transcribe`).
2. When the words come back it writes `voice/<stamp>.md` and puts the talk-out in the open document at the caret (the
   end of `idea.md` when Refs or Voice is open):

   ```
   <!-- talk-out voice/20261101-190211.webm -->
   So the idea is the jump to secure souls only works if you…
   ```

   The comment line is invisible in rendered Markdown and tells the agent and you where it came from.
3. It adds an Open comment anchored to that paragraph: "Work this talk-out into the outline and script." So talk-outs
   pile up in To do until you press Send, the way review takes do.

Without transcription set up, the audio is kept, the block says `(not transcribed: <hint>)`, and the job's hint shows
as a toast. Leaving the page before the words come back loses nothing: the transcript lands in `voice/` and the
comment points there.

With a question selected (below), a talk-out answers it instead: its transcript is sent as the answer, and the audio
is kept in `voice/`.

## Shaping it with the agent

Send sends v0's open comments as one task, as on a video. The task targets Ignition, so its prompt has
`agent/task.md`:

- `v0/` is this video's plan. `idea.md` is the person's own words: don't rewrite or remove them.
- Make `outline.md` the beats, with one `##` heading each, and `script.md` the same beats as `##` sections, with what's
  said as paragraphs and what's shown in `[brackets]`. Keep the person's phrasing where it works.
- Edit the files in place. Keep headings stable, because they're anchors and links. Move text rather than delete it,
  and list anything cut under `## Cut` at the end of `outline.md`.
- Each comment's change says what you did (the report).
- When unsure, ask about a place in a document and carry on:
  `retake ask --note 12 --file v0/script.md --heading "Cold open" --option "Start on the jump" --option "Start on the shop" "Which opening?"`.

**Questions in the editor.** The anchored heading or paragraph gets a mark in the left margin, in the Question colour.
It is found by heading, then quote, then line, so edits since don't lose it. Clicking the mark selects the question
and scrolls the comments column to it. You answer in the column's reply box (typed, or a tapped choice) or with a
talk-out (above). The comment goes from Question back to Working, and the same task carries on.

**Done.** The agent's report finishes the comments with their changes. No render is needed for a note on v0. The
edited documents reload through the watch, so you see them change.

## Start v1

**Start v1** adds a comment on v0 with `"wants": "version"`: "Build the first cut (v1) from script.md." It's editable
before you send it, and Ctrl+Enter sends it. The task's prompt adds:

- Build v1 from `v0/script.md`, section by section in order, with the footage and assets in the video's folder (not
  `v0/voice/`).
- A plain project (no timeline): write the video to `$RETAKE_OUTPUT`. The worker pushes it as `v1/<folder>_v1.mp4`.
  The folder already uses version folders, since `v0/` is one.
- A timeline project (`edit/timeline.json` or `retake.toml`): edit the timeline, and the worker renders v1 as for any
  task.
- Then write `v0/cuts/v1.json`.

The comment is Done with v1, the page follows it to v1, and the video's review starts there. v0 stays one click away.

## Later versions link back to the script

`agent/context.md` goes into every task of a video that has `v0/`: follow `script.md` unless the notes say otherwise,
and with each new version write `v0/cuts/<label>.json`:

```json
{ "version": "v3",
  "sections": [ { "heading": "Cold open", "from": 0, "to": 12.4 },
                { "heading": "The trick", "from": 12.4, "to": 61.0 } ] }
```

`heading` is a `##` heading of `script.md`, without the `## `. Times are seconds in that version. A section that isn't
in the cut is left out.

- **In the editor**, each `##` heading of the script has a quiet chip after it, "v3 0:12", for the newest version
  that covers it. Clicking it opens that version at that time.
- **On a video** (the Script sidebar tab, on videos that have v0): `script.md` read-only, with the section under the
  playhead highlighted. Clicking a section seeks to its start, and sections the shown version doesn't cover are dimmed.
  A heading renamed since the cut has no link until the next version's file.

## In the nav and the version strip

- **The Ignition section**, under the folder tree: its header row opens the ideas page (`#ext/ignition/`) and its +
  makes a new idea. Its rows are the ideas (videos with only v0), most recently edited first.
- **The ideas page**: New idea, then each idea's title (the `# ` line of `idea.md`, else the folder's name), its
  project, when it was last edited and its first two lines. Clicking one opens it.
- **The tree** shows ideas in their projects like other videos, with Ignition's icon where the status dot is.
- **The version strip and picker** list `v0 · Script` first. A video with a cut opens on its newest cut, as today.
  Following latest and comparing (C) never land on v0.

## Local and hosted

- **Local** (`retake open`): the files are read and written on disk, the server's `[transcribe]` transcribes, and the
  in-process worker runs the agent.
- **Hosted** (`retake serve` + `retake connect`): the files stay on the editing computer. The server reads and writes
  them through the video's worker, the same relay that plays its video. A talk-out's audio passes through the server
  to the computer and isn't kept on the server. The computer's `[transcribe]` transcribes it, and its worker runs the
  agent with its own copy of Ignition's instructions. While the computer is offline, v0 shows "The editing computer is
  offline", and nothing is cached on the host in this version.
- Roles: a viewer reads v0, a reviewer edits it, talks and sends, and the owner makes new ideas.

## What stays out

- **Sam's own skills, prompts and workflows.** They belong to his agent setup. His Claude Code loads them as it always
  does, and Ignition never names, installs or calls them. Its instructions are neutral and short.
- A chat with the agent. The agent works through comments, Send and questions, the same loop as on a video.
- Generated voices, text-to-speech or voice-over. The voice is the person's own.
- Editing the video by its transcript, a project board, titles, thumbnails, publishing. Those are other extensions, or
  none.
- Rich text, a documents database, and syncing with Google Docs or Notion.

## Tests

- Go: v0 with `idea.md` found by the scan (an idea folder, a video with v0 and cuts, a `v0/` without `idea.md`, a
  `v0/clip_v0.mp4`); `api/info` and `api/home` (`no_cut`, `kind`); New idea's folder and slug collisions, locally and
  through the machine channel; Start v1 in a v0-only plain project pushing `v1/<folder>_v1.mp4`.
- Web unit: the anchor finder (heading, then quote, then line), the talk-out block, the cuts chips, the playhead
  section.
- e2e `e2e/tests/18-ignition.spec.ts`, with a fake transcriber (a command engine that prints fixed words) and a fake
  agent:
  - New idea opens v0, typing saves, and an outside edit shows.
  - A talk-out lands in the document and adds a To do.
  - Send runs an agent that edits `outline.md` and asks about "Cold open"; a tap answers it; Done.
  - Start v1 runs an agent that writes a generated MP4 and `cuts/v1.json`; v1 opens; the Script tab follows the
    playhead.
  - The same flow hosted (`retake serve` and `retake connect` on scratch folders).
