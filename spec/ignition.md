# Ignition: from an idea to the first cut (extension, `retake-ext/1`)

This is the contract for Ignition, the first extension (spec/extensions.md). Change it here first.

Ignition is where a video starts. You type or talk your idea into one Markdown document, the agent shapes it into an
outline and a script in that same document, and when the script is right the agent builds the first cut from it. The
document is **v0** of the video: one plain file in the video's folder, `v0/<video>_v0.md`, next to `v1/<video>_v1.mp4`
and the cuts that follow.

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
marker = ["*_v0.md", "idea.md"]   # the document; idea.md: a v0 from before it was one file (migrated, below)
label = "Doc"
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
  edits within 2 seconds. It diffs and merges in git like any text. A `v0/` holding one `*_v0.md` is Ignition's; with
  more than one, `<folder>_v0.md` wins, else the first by name.
- **Nothing else shows.** The audio of a talk-out is kept in `v0/voice/` with its words and a sidecar
  (`<stamp>.md`: the audio's name and the text it gave), so the document stays plain. `v0/cuts/` is for the agent and
  the Script tab.
- **Before it was one file.** A `v0/` with `idea.md` and no `*_v0.md` is migrated the first time Ignition opens it:
  it writes `v0/<folder>_v0.md` with `idea.md`, then `outline.md`, then `script.md` (each file's text as it is, the
  outline and the script under `## Outline` and `## Script` when they don't start with a `#` heading of their own),
  and leaves the old files where they are. From then on it reads only the one file.
- A folder with only `v0/` is a video with one version: an **idea**. Its home card shows the chip "Idea" on an empty
  thumbnail box, and it has no status dot.

## New idea

The + on the Ignition section (or "New idea" on its page) opens `#ext/ignition/new`: a title, and the project to put
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
  text is shown as typed.
- **Images.** `![caption](images/x.png)` (a path relative to `v0/`) shows the image inline: the column's full width
  on a phone, rounded, with the caption under it; on the caret's line the Markdown shows instead. Pasting or dropping
  an image saves it as `v0/images/<name>` (a new name when one is taken, 25 MiB at most) and puts its Markdown at the
  caret. Hosted, images go through the same owned-file routes as the document.
- **Image decisions.** A question the agent asks about a place in the document with image options (`retake ask --file
  v0/<video>_v0.md --heading "Cold open" --option "Ink@images/ink.png" --option "Neon@images/neon.png" "Which look?"`)
  also shows inline, right after its heading or line: the question and its images side by side (stacked on a phone).
  Tapping one answers it, as tapping the choice in the comments column does; once answered it goes away.

- **Saving.** 600 ms after the last keystroke and when the text loses focus, with `If-Match` on the version read. The
  only word it shows is a quiet "Not saved · Retry" when a save fails, or "Changed outside Retake · Reload · Keep
  mine" when the file changed under unsaved typing (Keep mine writes over it). Outside edits with nothing unsaved
  reload in place, keeping the caret's line.
- **⋯**, one quiet button at the top right of the document: **Start v1** (while the video has no cut), and **Undo
  the agent's edit** (with when, while there is one to undo). Nothing else is over the text.
- **The comments** are core's: the comments column (a drawer on a phone), To do · Done, Send, as on a video. On a kind
  version it also has a line to type a comment: "Comment for the agent…"; Enter adds it to To do, anchored to the
  heading above the caret (`ctx.onAnchor`, below), Ctrl+Enter (⌘+Enter) adds and sends.
- **Questions.** A question the agent asked about a heading or line shows in the comments column, as on a video, with
  the place it's about ("Cold open"). Clicking its place scrolls the document there and puts the caret on it (core
  sends the slot `ctx.on('reveal')` with the anchor). The document itself gets no marks.
- **Keys.** Typing goes to the text. Esc leaves the text, and then R talks, as on a video. The Record button always
  works. The page's other review keys do nothing here.
- **A phone** shows the document from the top of the screen to the bottom bar, which holds just Record and Send (the
  version picker and the agent are in the comments drawer's header). The top row is back on the left, and on the right
  ⋯ with the comments' count beside it as a small icon and number (no number at 0). Text is at least 17 px and wraps
  inside the screen; nothing scrolls sideways. The bottom bar stays above Safari's toolbar and the home indicator
  (`viewport-fit=cover`, the safe-area insets, `dvh`). Phone checks run in Chromium and in WebKit (iPhone device
  descriptors).

`ctx.onAnchor(fn)` (an addition to the page API): the version slot tells core where a typed comment points (the
heading above the caret), so the comments column's line can anchor it.

## Talking it out

R (or Record) records a **talk-out**. REC, the timer, the red edge and the level work as on a video. There is no pen.

When it stops, Ignition saves the audio as `v0/voice/<yyyymmdd-hhmmss>.webm` and asks for its transcript
(`ctx.transcribe`). The words go into the document at the caret (at the end when the text had no caret yet) as a plain
paragraph: no marker, no comment line. The sidecar `voice/<stamp>.md` keeps the audio's name and the words, for the
agent and for you. It adds an Open comment anchored to that paragraph: "Work this talk-out into the document." So
talk-outs pile up in To do until you press Send, the way review takes do.

Without transcription set up, the audio is kept, nothing goes into the document, and the job's hint shows as a toast.
Leaving the page before the words come back loses nothing: the sidecar is written when they do, and the next time the
document opens, Ignition puts in the ones it hasn't yet. A question selected in the comments column takes the talk-out
as its answer instead.

## Shaping it with the agent

Send sends v0's open comments, as on a video. The task targets Ignition, so its prompt has `agent/task.md`:

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

**Done.** The agent's report finishes the comments with their changes. No render is needed for a note on v0. The
document reloads through the watch, so you see it change.

**Undo the agent's edit.** Before every agent attempt on a video that has `v0/`, Retake keeps a copy of v0's files
(core's history, spec/extensions.md "Agent edit history"). ⋯ → "Undo the agent's edit" puts the document back as it
was before the newest agent edit, and the next undo goes one further back. When the document changed since, it refuses
with "Changed since the agent's edit". Git stays optional.

## Start v1

⋯ → **Start v1** adds a comment on v0 with `"wants": "version"`, "Build the first cut (v1) from the script.", and
sends it. The task's prompt adds:

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

- **The Ignition section**, under the folder tree: its header row opens the ideas page (`#ext/ignition/`) and its +
  makes a new idea. Its rows are the ideas (videos with only v0), most recently edited first.
- **The ideas page**: New idea, then each idea's title (the document's `# ` line, else the folder's name), its
  project, when it was last edited and its first two lines. Clicking one opens it.
- **The version strip and picker** list `v0 · Doc` first. A video with a cut opens on its newest cut, as today.
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

- **Your own skills, prompts and workflows.** They belong to your agent's setup. Your Claude Code or Codex loads them as
  it always does, and Ignition never names, installs or calls them. Its instructions are neutral and short.
- A chat with the agent. The agent works through comments, Send and questions, the same loop as on a video.
- Generated voices, text-to-speech or voice-over. The voice is the person's own.
- Editing the video by its transcript, a project board, titles, thumbnails, publishing. Those are other extensions, or
  none.
- Rich text, a documents database, and syncing with Google Docs or Notion.

## Tests

- Go: v0 with `*_v0.md` (and the old `idea.md`) found by the scan (an idea folder, a video with v0 and cuts, a `v0/`
  without either, a `v0/clip_v0.mp4`); a marker given as a list of globs; `api/info` and `api/home` (`no_cut`,
  `kind`); New idea's folder and slug collisions, locally and through the machine channel; Start v1 in a v0-only plain
  project pushing `v1/<folder>_v1.mp4`.
- Web unit: the document's name, the migration's text, the heading above the caret, the talk-out paragraph and its
  sidecar, the playhead section.
- e2e `e2e/tests/18-ignition.spec.ts`, with a fake transcriber (a command engine that prints fixed words) and a fake
  agent, on a phone and on a desktop:
  - New idea opens the one document, typing saves, and an outside edit shows.
  - A talk-out lands in the document as a plain paragraph and adds a To do.
  - Send runs an agent that writes the outline and script into the document and asks about "Cold open"; a tap answers
    it; Done; ⋯ undoes the agent's edit.
  - Start v1 from ⋯ runs an agent that writes a generated MP4 and `cuts/v1.json`; v1 opens.
  - A multi-file v0 from before is migrated into one file and the old files stay.
  - The same flow hosted (`retake serve` and `retake connect` on scratch folders).
