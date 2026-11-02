`v0/` is this video's plan: plain Markdown files the person and you both edit.

- `v0/idea.md` is the person's own words: don't rewrite or remove them.
- Make `v0/outline.md` the beats, with one `##` heading each, and `v0/script.md` the same beats as `##` sections,
  with what's said as paragraphs and what's shown in `[brackets]`. Keep the person's phrasing where it works.
- Talk-outs are in `v0/voice/`: `<stamp>.md` is a transcript, and a `<!-- talk-out voice/<stamp>.webm -->` line marks
  where one was put into a document.
- Edit the files in place. Keep headings stable, because they're anchors and links. Move text rather than delete it,
  and list anything cut under `## Cut` at the end of `outline.md`.
- Each comment's change says what you did (the report).
- When unsure, ask about a place in a document and carry on:
  `retake ask --note 12 --file v0/script.md --heading "Cold open" --option "Start with the result" --option "Start with the problem" "Which opening?"`

## When a comment wants a version

A comment that wants a version (such as "Build the first cut (v1) from script.md.") is done only with a new video
version:

- Build it from `v0/script.md`, section by section in order, with the footage and assets in the video's folder (not
  `v0/voice/`).
- A plain project (no timeline): write the video to `$RETAKE_OUTPUT`. The worker pushes it as `v1/<folder>_v1.mp4`.
  The folder already uses version folders, since `v0/` is one.
- A timeline project (`edit/timeline.json` or `retake.toml`): edit the timeline, and the worker renders the version as
  for any task.
- Then write `v0/cuts/<label>.json` (`v0/cuts/v1.json` for v1): which script sections the version covers, and where.

  ```json
  { "version": "v1",
    "sections": [ { "heading": "Cold open", "from": 0, "to": 12.4 },
                  { "heading": "The trick", "from": 12.4, "to": 61.0 } ] }
  ```

  `heading` is a `##` heading of `script.md`, without the `## `. Times are seconds in that version. A section that
  isn't in the cut is left out.
