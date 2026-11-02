This video's plan is one Markdown document, `v0/<video>_v0.md` (the one `*_v0.md` file in `v0/`). The person and you
both edit it.

- The person's own words under its `#` title are theirs: don't rewrite or remove them.
- Write each paragraph, list item and `[shown]` direction as one line: no hard line breaks inside them. The page wraps
  text to the screen, and a phone shows broken lines as ragged short lines.
- Shape it in that same file: a `## Outline` section with the beats, then a `## Script` section with the same beats as
  `###` headings, what's said as paragraphs and what's shown in `[brackets]`. Keep the person's phrasing where it
  works. Don't make other files in `v0/`.
- Talk-outs: the person's spoken words are already in the document as plain paragraphs; `v0/voice/<stamp>.md` says
  which audio each came from, if you need it.
- Images: save them in `v0/images/` and put them in the document as `![caption](images/<name>.png)` (relative to
  `v0/`). When the person should choose between looks, make each one and ask with them as options, about the place in
  the document they're for: `retake ask --note 12 --file v0/<video>_v0.md --heading "Cold open" --option "Ink@v0/images/ink.png" --option "Neon@v0/images/neon.png" "Which look?"`.
  They show in the document; the person taps one.
- Links: put an address alone on its own line to embed it (a YouTube, Vimeo, X or TikTok address plays in place, any
  other page shows as a card with its title); a link inside a sentence stays a link.
- Edit in place. Keep headings stable, because they're anchors. Move text rather than delete it, and list anything
  cut under `## Cut` at the end.
- Each comment's change says what you did (the report).
- When unsure, ask about a place in the document and carry on:
  `retake ask --note 12 --file v0/<video>_v0.md --heading "Cold open" --option "Start with the result" --option "Start with the problem" "Which opening?"`

## When a comment wants a version

A comment that wants a version (such as "Build the first cut (v1) from the script.") is done only with a new video
version:

- Build it from the document's `## Script`, section by section in order, with the footage and assets in the video's
  folder (not `v0/voice/`).
- A plain project (no timeline): write the video to `$RETAKE_OUTPUT`. The worker pushes it as `v1/<folder>_v1.mp4`.
- A timeline project (`edit/timeline.json` or `retake.toml`): edit the timeline, and the worker renders the version as
  for any task.
- Then write `v0/cuts/<label>.json` (`v0/cuts/v1.json` for v1): which script sections the version covers, and where.

  ```json
  { "version": "v1",
    "sections": [ { "heading": "Cold open", "from": 0, "to": 12.4 },
                  { "heading": "The trick", "from": 12.4, "to": 61.0 } ] }
  ```

  `heading` is a heading of the script, without its `#`s. Times are seconds in that version. A section that isn't in
  the cut is left out.
