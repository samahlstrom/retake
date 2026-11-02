# Quickstart: the whole loop on the sample project

This walks through one round of review on a generated sample: you open v2, leave a note, an agent answers it with v3,
and you flip between the two. It takes about five minutes. The output shown below is from a real run.

## What you need

- `retake` and `retake-edit`. From a clone of this repository:

  ```bash
  mise install
  mise exec -- make build                              # bin/retake
  (cd editor && mise exec -- cargo build --release)    # editor/target/release/retake-edit
  export PATH="$PWD/bin:$PWD/editor/target/release:$PATH"
  ```

- ffmpeg and ffprobe on your `PATH`.
- A browser with a microphone, or the pre-made take (step 3).
- Optional: whisper.cpp, to turn your own speech into text (`scripts/install-whisper.sh`, then see
  [config.md](config.md#transcribe)). The pre-made take comes with its transcript, so you can skip this.
- Optional: an agent such as Claude Code or Codex ([agents.md](agents.md)). Step 5 also shows the agent's part done
  by hand.

## 1. Make the sample

```bash
scripts/make-sample.sh ~/retake-sample
```

This renders a 22-second how-to video ("How to brew pour-over coffee") twice from one timeline: v1, then v2 after a
patch. The footage is ffmpeg's test patterns, the titles are drawn by `retake-edit`, and the voice is synthetic
([examples/sample/README.md](../examples/sample/README.md)).

```
~/retake-sample/
  how-to/v1/how-to_v1.mp4     version 1
  how-to/v2/how-to_v2.mp4     version 2
  edit/                       the editor project: timeline.json, patches/, voice/, media/
```

Add `--with-take` to also get a review take on v2, so you can skip recording in step 3.

## 2. Open v2

```bash
cd ~/retake-sample
retake open how-to/v2/how-to_v2.mp4
```

```
retake: how-to_v2.mp4  (1280x720, 30.002 fps, 0:22.2)
  versions v1 [v2]
  page   http://127.0.0.1:8777/
  saves  ~/retake-sample/how-to/v2/how-to_v2.review/<take>/   (other versions save next to their own file)
  Ctrl+C stops the server
```

The page opens in your browser. `retake` found v1 by its name. Leave the server running.

## 3. Record a note

1. Press Space to play. Pause at step two, around 0:11.
2. Press **R** to start recording. The browser asks for the microphone the first time.
3. Say "The labels at the bottom are too small. Make them bigger, please."
4. Draw a loop around the "STEP 2" label. While you record, the line goes with what you say.
5. Press **R** to stop, then click **Send**. Without an agent it just saves the take, and one line over Send says
   what's missing.

No microphone, or nothing to say out loud? Draw on the video, or click the box at the bottom of the comments
("Comment at 0:11 · goes into v3…"), type, and press Enter: that's a comment too, with your drawing and that moment.

The take is saved in `how-to/v2/how-to_v2.review/<timestamp>/`.

**Without a microphone:** run `scripts/make-sample.sh ~/retake-sample --with-take` in step 1 instead. It puts a take
with exactly this note on v2, recorded with a synthetic voice.

## 4. Turn the take into notes

If transcription is set up, Send already queued this in the server, and the note shows up on its own. Otherwise, or
to see it happen:

```bash
retake notes sync how-to/v2/how-to_v2.mp4
```

```
retake: transcribing 2026-01-15_10-00-00
notes for v2: 1 new, 0 changed, 0 retracted, 0 reinstated, 0 answers
```

```bash
retake notes list how-to/v2/how-to_v2.mp4
```

```
#1   v2   0:11.0-0:11.0  open      Sample Reviewer: The labels at the bottom are too small, make them bigger, please.  [1 stroke, on label/2]
cursor 1
```

Note #1 is open. `notes sync` transcribes the takes that need it, runs the notes engine, and stores the result in
`how-to/.retake/project.db`. The engine also writes `how-to/v2/how-to_v2.review/notes.md`, a readable version with a
snapshot of the frame and your stroke. `on label/2` is the timeline element your stroke circled, found in v2's render
manifest ([timeline.md](timeline.md#editing-with-the-timeline)).

## 5. Let an agent answer it

**With an agent.** Connect it as in [agents.md](agents.md), then ask it something like:

> Answer the open review notes on ~/retake-sample/how-to/v2/how-to_v2.mp4. The video is rendered from
> ~/retake-sample/edit/timeline.json with retake-edit; change it only with patches, each with the note ids it answers.
> Then push v3 with version_push and render: true. Ask me about any note you're unsure of.

**By hand.** This is what the agent does. It changes the timeline with a patch that answers the note
([examples/sample/patches/02-v3.json](../examples/sample/patches/02-v3.json) makes the labels 44 px instead of 30):

```bash
retake-edit patch apply edit/timeline.json edit/patches/02-v3.json
```

```
applied p2 (rev 1 → 2), answers v2#1; re-renders 3 of 5 units
```

Then it renders the new revision and pushes it as v3. `how-to/` is a timeline project (`how-to/retake.toml` points at
the timeline), so `--render --no-stream` does both. Only the units the patch touched are drawn again; the rest come from the cache.

For live review, omit `--no-stream`: the push registers a playlist and poster immediately, then renders while someone watches. This walkthrough uses a completed MP4 so its scripted checks can inspect the manifest right away:

```bash
retake push how-to/v2/how-to_v2.mp4 --render --no-stream --parent v2
```

```
pushed v3 from v2: ~/retake-sample/how-to/v3/how-to_v3.mp4
  rendered ../edit/timeline.json rev 2: 5 units, 2 reused, 3 rendered
  #1 Bigger step labels: 44 px text on a taller bar (p2)
  answered #1
```

The push puts the render in the family as `how-to/v3/how-to_v3.mp4`, with its manifest beside it, and marks note #1
answered. The changelog line is the patch's summary: the patch said it answers `v2#1`. A push that left an open note
out would be refused ([agents.md](agents.md#the-push-rule)). Without `--render`, `retake push <video> <file>` pushes
any file you rendered yourself.

```bash
retake versions how-to/v2/how-to_v2.mp4
```

```
v1   ~/retake-sample/how-to/v1/how-to_v1.mp4  0:22.2  0 open
v2   ~/retake-sample/how-to/v2/how-to_v2.mp4  0:22.2  0 open
v3   ~/retake-sample/how-to/v3/how-to_v3.mp4  0:22.2  0 open  (from v2 by agent, 1 change)
       #1 Bigger step labels: 44 px text on a taller bar (p2)
       rendered from ../edit/timeline.json rev 2: 5 units, 2 reused
```

## 6. Flip with C

Stop the server (Ctrl+C) and open the new version:

```bash
retake open how-to/v3/how-to_v3.mp4
```

1. Play to step two and pause. The label is bigger.
2. Press **C**. The page shows v2 at the same moment, with the small label. The comments switch with it: v2's Done
   tab has #1, with the change and "See v3".
3. Press **C** again to come back to v3.

On v3, the newest version, the sidebar shows only the agent's open questions. There are none here.

## Run it all as a script

[`examples/sample/run-quickstart.sh`](../examples/sample/run-quickstart.sh) runs every step above without a person.
It uses the pre-made take for step 3 and the fixed patch for step 5, and presses C in headless Chromium. It's a smoke
test for this page:

```
$ retake open how-to/v3/how-to_v3.mp4 --no-open --port 8932 --idle-exit 0

$ curl -s http://127.0.0.1:8932/api/notes\?v=v2
{"version":"v2","newest":false,"items":[{"id":1,"kind":"note","status":"answered","t0":11,"t1":11,"text":"The labels at the bottom are too small, make them bigger, please.","by":"Sample Reviewer","change":"Bigger step labels: 44 px text on a taller bar","answered_in":"v3"}],"jobs":[]}

(headless Chromium: seek to 11.0 s, press C, press C again)
before C: {"base":"v3","shown":"v3","flipped":false,"switching":false,"t":11}
after C:  {"base":"v3","shown":"v2","flipped":true,"switching":false,"t":11}
C again: {"base":"v3","shown":"v3","flipped":false,"switching":false,"t":11}

quickstart: PASS
```

## Next

- Leave a note the agent can't decide on, and watch it come back as a question in the sidebar. Click **Answer** and
  record your answer.
- Let the agent pick up new notes by itself with `retake watch` ([agents.md](agents.md#retake-watch)).
- Edit `edit/timeline.json` through your own patches ([timeline.md](timeline.md)).
