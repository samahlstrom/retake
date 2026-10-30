# The starter library

Motion graphics and edit styles as code: ten components and one brand kit, original work, in plain JSON that any
agent or person can read, change and share (`spec/components.md`). Every component has examples and a committed
storybook sheet (`previews/sheet.png`: each example at five moments).

| Component | Use as | What it does |
|---|---|---|
| [`title-card`](components/title-card) | picture | A full-frame title: big title, an accent rule that draws out, a subtitle, a soft glow. |
| [`pop-text`](components/pop-text) | overlay | A word or two that pops (or rises) in over the picture, then fades on the element's end. |
| [`lower-third`](components/lower-third) | overlay | A name and a role on a plate that wipes in from an accent bar. |
| [`caption`](components/caption) | overlay | Word-timed karaoke captions from a words JSON (`"$words:<spine-id>"`), the spoken word lit. |
| [`pip-frame`](components/pip-frame) | overlay | A second video in a framed, shadowed box that pops in. |
| [`progress-bar`](components/progress-bar) | overlay | A bar that fills over the element's length, with a dot and a label. |
| [`zoom-punch`](components/zoom-punch) | picture | Footage that punches in on a point at a moment (a keyframed scale), with an optional flash and release. |
| [`counter`](components/counter) | overlay | An expression-driven number that counts up with an ease-out and a bump when it lands. |
| [`callout-arrow`](components/callout-arrow) | overlay | A label on a pill with an arrow that draws out to a point, and a ring that pings around it. |
| [`freeze-frame-highlight`](components/freeze-frame-highlight) | picture | Footage that freezes with a flash, dims all but a circle around the point, rings and names it. |

[`kits/clean`](kits/clean/kit.json) is a neutral dark kit: tokens (colors, the font, sizes, safe areas, caption
style, pacing) for all ten components, and house rules that `retake-edit lint` checks: one pop at a time, room
between pops, at most two punch-ins in ten seconds, graphics inside the action-safe area, names never over captions,
time for callouts to read, and a custom rule (a JavaScript expression) for pops too short to read.

## Using it

```sh
scripts/build-registry.sh                                  # library/index.json, a static registry
retake lib search caption --registry library/index.json
retake lib add pop-text --registry library/index.json      # into components/pop-text@1.0.0 next to your timeline
retake lib add clean --registry library/index.json         # the kit and its ten components
retake-edit lint edit/timeline.json
```

A timeline then says `"kits": ["clean@1.0.0"]` and uses a component where it would write layers:

```json
{ "id": "pop/faster", "use": "pop-text@1.0.0", "from": "word:b/faster", "to": "word:b/faster+1.20",
  "props": { "text": "Faster" } }
```

Every component is designed on a 1920×1080 canvas and fits any other canvas (centered, whole). Colors default to the
kit's tokens (`kit:color.accent`), with the component's own defaults when the timeline has no kit.

## Changing one

A released version never changes: copy `pop-text` to a new version, bump `version` and `changelog`, change it, check
it with `retake-edit component preview <dir>` (and look at the sheet), then move elements to it one at a time with
`retake-edit component upgrade <timeline> <element> --to <version>`. Only those elements re-render.

## License

Everything in this folder is original work by the Retake contributors, dedicated to the public domain under
[CC0 1.0](https://creativecommons.org/publicdomain/zero/1.0/): use, change and share it without asking. The
components draw text with Baloo 2, the font built into `retake-edit`, which keeps its own SIL Open Font License
(`editor/fonts/baloo2/OFL.txt`). The preview sheets are drawn from generated placeholders (an ffmpeg gradient and
shapes), not footage.
