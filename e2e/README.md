# End-to-end tests

Playwright drives headless Chromium through the review page with real keys and mouse moves, while a fake mic plays
a generated WAV. Every input is made with ffmpeg at the start of each run. There is no real footage and no real
voice. The suite runs serially, with one browser, because the scenario is timed against the fake mic.

## Run

```bash
make e2e                                   # builds bin/retake, then runs the suite against it
cd e2e && npm ci && npm test               # the suite alone (needs bin/retake from `make build`)
RETAKE_SERVER=legacy RETAKE_LEGACY_REVIEW=/path/to/review npm test   # against the legacy Python player (new-only tests skip)
npm run typecheck                          # tsc --strict over the suite
npm run fixtures -- <dir>                  # only generate the media, into <dir>/media
```

Run it through mise (`mise exec -- npm test`) to use the pinned node. Needs `ffmpeg`/`ffprobe` on PATH, and Chromium.

| Env var | Default | Meaning |
|---|---|---|
| `RETAKE_SERVER` | `new` | `new`: `bin/retake open <video> --no-open --port <p> --idle-exit 0`. `legacy`: the Python player's `review <video> --no-open --port <p> --idle-exit 0`. |
| `RETAKE_BIN` | `<repo>/bin/retake` | The new server's binary. |
| `RETAKE_LEGACY_REVIEW` | none; required with `RETAKE_SERVER=legacy` | The legacy player's `review` launcher. It is only run, never modified. |
| `CHROMIUM_PATH` | `/usr/lib/chromium/chromium` if it exists, else Playwright's Chromium | The browser. CI points it at Playwright's Chromium. |
| `RETAKE_E2E_WORK` | `<repo>/.retake-work/e2e` | Scratch root: media, version families, takes, server logs. It is wiped at the start of a run and deleted after a passing run. A failing run keeps it. The suite only deletes a folder that has its `.retake-e2e` marker. |
| `RETAKE_E2E_KEEP` | unset | Set it to keep the scratch data after a passing run too. |
| `RETAKE_E2E_PORT_BASE` | `8831` | The first of the ten ports below; move it to run two checkouts side by side. |
| `RETAKE_EDIT` | `<repo>/editor/target/release/retake-edit`, else `$PATH` | The editor, for `08-timemap` (it skips without one). |
| `RETAKE_E2E_SHOTS` | unset | A folder: `05-sidebar` writes its screenshots there (1920×1080 and phone size). |
| `RETAKE_E2E_THEME_SHOTS` | unset | A folder: `17-theme` writes the home and the review page there, desktop and phone, for the built-in look and both example themes. |
| `RETAKE_E2E_WEBKIT` | unset | `docker`: run the phone specs (`*-webkit.spec.ts`) in WebKit too, its browser in Playwright's Docker image (below). A `ws://…` URL: a WebKit browser server you started. Unset: no `webkit` project, and those specs skip. |
| `RETAKE_E2E_NO_GPU` | unset | Set: Chromium runs with `--disable-gpu` (software rendering only, for a machine whose GPU is busy). Renders take `RETAKE_ENCODER=x264` for the same. |
| `RETAKE_E2E_WEBKIT_PORT` | `8950` | The WebKit browser server's port with `RETAKE_E2E_WEBKIT=docker`. |

Ports: the servers use 8831–8840 (scenario 8831, crash safety 8832 behind a proxy on 8834, HLS 8833, sidebar 8835,
phone layout 8836, sharing 8837 behind a proxy on 8838, time map 8839, the team server 8840; the relay's server
reuses 8837). The browser talks to
Playwright over a pipe, so it opens no devtools port. Each server is started
for its spec file, waited on
through `/api/ping`, and always stopped. Its log is in `<work>/logs/`.

## WebKit (iPhone Safari's engine)

Phones run Safari, so the phone layout is checked in WebKit as well. Playwright's WebKit build doesn't run on every
Linux host, so its browser runs in Playwright's Docker image (the one that matches `@playwright/test`), as a browser
server on the host's network; the tests, the servers, ffmpeg and the fixtures stay on the host and connect to it:

```bash
RETAKE_E2E_WEBKIT=docker npx playwright test --project webkit       # starts the container, runs, stops it
```

The global setup runs `docker run -d --rm --init --network host --ipc=host -v <repo>/e2e/node_modules:/pw/node_modules:ro
mcr.microsoft.com/playwright:v1.64.1-noble node /pw/node_modules/playwright-core/cli.js run-server --port 8950 --host
127.0.0.1` and the teardown stops that container by its id. To keep one running yourself, start it the same way and
pass `RETAKE_E2E_WEBKIT=ws://127.0.0.1:8950/`. The image's tag must match the suite's Playwright version. The `webkit`
project runs only `*-webkit.spec.ts`, on Playwright's `iPhone 13` and `iPhone 15` devices (their viewports already
leave out Safari's toolbars); the default run (`chromium`) is unchanged and skips them.

## Fixtures (`lib/fixtures.ts`)

- **A version family.** `v5/clip_v5.mp4` and `v6/clip_v6.mp4` (40 s), `v7/clip_v7_0-010_preview.mp4` (a 10 s
  preview), and a `v5/clip_v5_draft.mp4` decoy that is newer than the plain v5. All are 1280x720, 30 fps, H.264
  yuv420p + AAC, with faststart and a keyframe every second. They have drawtext labels when fontconfig finds a font.
- **HLS v8.** `clip_v8/clip_v8.m3u8`, made with `-hls_segment_type fmp4`, sits beside `init.mp4` and
  `seg_NNN.m4s`. Its folder is a sibling of `v6/`, so family detection finds it as v8.
- **Fake mic.** A 40 s WAV (48 kHz mono s16) of tone bursts, timed from the first R on the legacy selftest schedule:
  bursts at 1.0, 3.3, 9.5, 13.0, 19.0 and 22.5 s. Each burst has its own frequency, so the audio check can tell
  exactly which speech landed in which segment file. The 9.5 s burst falls in the unrecorded loop and must never be
  captured. Chromium restarts the WAV every time a page opens the mic.

## What each test proves

**`01-schema-and-fixtures`** (no browser)
- `spec/take.schema.json` accepts a complete take, each file on its own, the `/data` body, and unknown extra fields.
- The schema rejects 23 kinds of broken take.
- The fixtures have the planned lengths, codecs and HLS layout.
- The tone analyzer finds every fake-mic burst at its time. This is the self-check behind the audio assertions
  below.

**`02-scenario`** ports the legacy `tests/e2e_headless.mjs` and `check_e2e.py`, without the transcription and notes
checks. It runs against both servers.
- **Family.** v5, v6 and the v7 preview are found (legacy; this server opens the newest version, so its family is v5
  and v6). The newer draft decoy is skipped.
- **A.** Without recording, play works and a drag makes no take and no stroke of a take. Legacy: the "Press R" hint
  shows. This one: the drag starts a typed comment there, which pauses the video, and Esc drops it.
- **S1–S4.**
  - R-gated recording and pen.
  - An unrecorded loop whose speech and drag are dropped.
  - C flips to the partner at the same frame (within 40 ms) and back: v7 on the legacy player, v5 here.
  - S3 is recorded while flipped.
  - Done.
  - The serialized take has S1–S4 with the right version and flip, and strokes 1 (red, S1) and 2 (green, S4).
  - It matches `$defs/dataBody`.
- **`[ ]`.** Legacy: steps keep the time (within 0.8 s) and the play state, `]` and C past the preview's end are
  refused ("only covers"), and `]` inside the preview switches. Here `[` and `]` are not keys and change nothing.
- **Reload.** The two strokes come back as history, and no take is open.
- **On disk.**
  - There is exactly one take, on v6, and none for v5 or v7.
  - The files equal what the page serialized.
  - There is one audio file per segment and no others.
  - Each file's decoded length is within 0.6 s of `t_end − t_start`.
  - Each burst said during a segment is in that file, at its moment (within 0.5 s).
  - No file holds anything said outside it, including the unrecorded loop's burst.
  - Strokes have their color, version, flip, paused state and position, and lie inside their segment on both clocks.
  - Event kinds and counts match (4 × rec_start/rec_stop, 2 × draw_start/draw_end, 4 version flips), and the
    rec events line up with the segments.
  - There are voice marks.
- **Schema.** Every JSON file of every take validates against `spec/take.schema.json` (ajv, draft 2020-12).
- **Author** (new only). `session.json` has `author.name` from the config file. The suite points
  `XDG_CONFIG_HOME` at the scratch dir.
- **Errors.** No uncaught page errors.

**`03-crash-safety`** (new only)
- While recording, every audio upload fails. The test keeps talking for 11 s, then reloads the page with uploads
  still failing, and checks that nothing reached the disk.
- Then uploads work again. The reloaded page must resend the take from IndexedDB on its own. The segment file must
  hold the audio up to the reload, with each burst at its moment.
- The failures come from a proxy in front of the server (`lib/proxy.ts`), not from `page.route`. A keepalive upload
  sent while the page unloads bypasses browser-side routing: against the legacy page it put 55 KB on disk. The
  proxy cuts those uploads off too.

**`04-hls`** (new only)
- `/video/v8/<file>` serves the playlist, init and segments (with ranges), and nothing outside the folder.
- v8 is detected from its own folder.
- The page opens v8. C flips m3u8 → mp4 at the same time, C flips back, and the playlist plays (currentTime
  advances, readyState ≥ 2). C after playing flips at the same time again.
- `[` and `]` are not keys: playback and the playhead go on.

**`05-sidebar`** (new only; milestone 2, `spec/notes.md`)
- The family is v5 and v6 (the newest, opened). The sidebar's content needs an agent, so `GET /api/notes`,
  `POST /api/notes/<id>/reopen` and the SSE stream `GET /api/events` are mocked with `page.route`. `POST /image` and
  `GET /api/takes/...` go to the server when it has them (probed first, without side effects), else they are mocked
  and the test checks the requests instead of the disk. `POST /done` always goes to the server.
- **Newest version:** only the open questions, at their times, with version chips; answered and retracted items are
  hidden. The column sits right of the video, and the bar stays full width.
- Clicking an item seeks to it. A chip flips to that version at the same time, and the sidebar then shows that
  version's notes as resolved (✓ with the change and "answered in v6", ? with the question). Reopen posts. "see it in
  v6" goes back to the note's moment on v6.
- N hides the sidebar (the page is then exactly as before), and that is remembered across a reload.
- Not recording, an image drop or paste is refused with "Press R to record before adding images".
- Answer seeks to the question and records with `answer_to` on the segment.
- While recording, a drop lands at its point and time, its corner resizes it, and a paste lands where the pointer
  was. Z undoes the last stroke or image, whichever came last.
- Done saves `answer_to`, `closed` and the image (`images.json` and `img_01.png` when the server stores images), the
  take validates against the schema, and the page posts `/done`.
- Job events show progress, then a failure with the server's hint. A `notes` event brings in the new note.
- An earlier take's image shows near its time, dimmed and not resizable. On a phone (portrait), the list always fills
  the space between the controls and Record, and the bubble shows To do instead of hiding it.

**`06-mobile`** (new only): the phone layout, in Chromium's mobile emulation (`isMobile`, `hasTouch`, a coarse
pointer), driven by taps and real touch drags (CDP touch events). There is no WebKit project yet: Playwright's
WebKit build needs Ubuntu's libicu74, libxml2 and libflite1, and doesn't launch on hosts without them (Arch, say).
- At 390x844 portrait and 844x390 landscape:
  - The page is in the phone layout and fits: no horizontal overflow, nothing scrolls.
  - Record, Send, undo, play, the version chip, the pen and (in its popover) the pen colors and the eraser are
    visible, inside the viewport and at least 40 px. The timeline is at least 44 px tall and wide. The video is full
    width (portrait) or nearly the full height (landscape).
  - Landscape: the touch hint shows until the first comment. Portrait: no hint over Record; the comments under the
    controls say how to comment ("No comments yet"). A drag without recording starts a typed comment (the video pauses,
    no take starts), and Cancel (portrait) or Esc drops it. Drags neither scroll nor zoom the page.
  - Tapping Record opens a segment without moving the video. The pen draws into the take in the color picked from
    its popover, and Undo removes the last stroke.
  - A thumb drag on the timeline scrubs.
  - The flip button switches to v7 at the same time (within 40 ms) and back. Past the v7 preview's end it shows as
    disabled, and a tap gets C's refusal toast.
  - Done saves the take: every file validates against `spec/take.schema.json`, and the audio file is named for its
    type.
  - The version list opens by tap, its items are 44 px tall and on screen, and a tap switches the version.
- iOS Safari's recorder (simulated: only `audio/mp4` is supported, and the recorder reports its type only once
  started): the segment is mp4 audio saved as `seg_01.m4a`, it decodes, and the take validates.

**`07-share`** (new only; milestone 3a, `spec/team.md`): the server allows a made-up share host
(`--allow-host share.test`), and the proxy (`lib/proxy.ts`) stands in for the tunnel: it sends every request on with
`Host: share.test` and the page's origin as `https://share.test`. Tokens come from `retake token create`. Nothing
reaches a real tunnel.
- Without a token the share host answers 401: the page says it needs a share link, and the API, the video and a POST
  are refused. A made-up link is refused too. Straight to 127.0.0.1 the page is the owner's, as before.
- A viewer's link (`/s/<token>`) sets an HttpOnly, Secure, SameSite=Strict cookie for `/` and leaves the address bar
  without the token. The page has no Record, Done or pen colors, and the hint says "View only". R only shows "View
  only", a drag draws nothing, the video plays, and the server refuses a viewer's POST (403). No take is written.
- A reviewer's link records a take (R, a stroke, R, Done). It is saved with the link's label as `author.name`, and
  validates.

**`08-timemap`** (new only; the review bridge, `spec/bridge.md`): two versions rendered with `retake-edit` from the
editor's demo timeline (`editor/examples/demo`, at 320x180 and quarter scale), v2 after a patch that trims 0.3 s of
VO air at 3.6 s. Both have render manifests. It skips without a built `retake-edit`.
- `/api/timemap` maps v1 onto v2 (9.0 s → 8.7 s, 1.0 s stays) and back, and an unknown version is a 404.
- C on v2 at 8.7 s shows v1 at 9.0 s (the matching moment, not the same clock time), and C again goes back to 8.7 s.
  Before the trim, C keeps the time.
- `[` and `]` are not keys. Picking v1 in the version list lands on the matching moment too (12.7 s on v2 is 13.0 s
  on v1), and picking v2 comes back.

**`09-server`** (new only; milestone 3b, `spec/server.md`): `retake serve` with the default drivers (SQLite and local
files) in a scratch data folder. The admin, the project (with v6 as its first version) and the members come from the
CLI.
- Opening a sign-in link shows a "Sign in as" button and sets nothing: a mail scanner can't use the link. The button
  signs in with an HttpOnly, Secure, SameSite=Lax session cookie for `/`, and the projects page lists the project. The
  link then works no more.
- A reviewer opens the project from the projects page (`/p/demo/`, the same page under a base path), records a take
  (R, a stroke, R, Done), and Done queues the transcribe and notes jobs. The take is in the project's workspace, with
  the account's name as `author.name`, and validates.
- A viewer watches without Record, and the server refuses their POST (403). Signing out ends the session: the API
  answers 401.

**`14-relay`** (new only; `spec/automation.md`, "Version files" and "The media relay"): `retake serve`, and
`retake connect` (with a fake agent profile) on a folder with no timeline: `Promo/promo_v1.mp4` (4 s) and
`promo_v2.mp4` (16 s at a constant 14 Mbit/s, about 28 MB: four relay chunks). Nothing is uploaded.
- The project and both versions reach the server, marked as relayed. The page plays v2 from the computer, then seeks
  to 13 s (a part it hasn't loaded) and plays on. Every video answer is a 206 byte range of at most 8 MiB, and one
  starts past the first chunk. The server's relay cache holds the chunks, and its project has no copy of the file.
- `retake connect` stops. Once the server calls the computer offline, choosing v1 (never played, so not cached)
  shows its poster with "The editing computer is offline", not the "won't play" message. When `retake connect` runs
  again the caption goes within a few seconds and v1 plays. `RETAKE_E2E_SHOTS` gets a screenshot of the offline page.

**`17-theme`** (new only; `spec/theme.md`): `retake open <folder>` on two generated projects, with a scratch
`XDG_CONFIG_HOME`, so the user's theme folder is `<it>/retake/theme`.
- With no theme folder, the page is served as built (no theme link, no `data-theme`, `theme/theme.css` is 404), its
  tokens are the built-in values, and every answer says `X-Retake-Api-Version: 1`.
- `examples/themes/brand` in the default folder applies with no flag: `--accent`, the bar's part rule, the name in
  the titles and the home, the logo, the favicon and the OFL font. Its files come with their own Content-Type,
  nosniff and a sandboxing CSP.
- `--theme examples/themes/light` wins over the default folder: light surfaces, a dark stage (a part rule), and the
  timeline canvas drawn in the theme's track colour.
- A broken theme (bad TOML, broken CSS, a script) loads the page with no errors; the script is never served; the
  server log and `retake theme check` (exit 1) say what's wrong.
- `retake theme check` passes both examples and flags a token this version doesn't have (`--hair`).
- An update: a freshly built binary at another path serves the same theme from the same folder, and the theme's files
  are byte for byte what they were.

**`19-phone-webkit`** (new only; WebKit only, `RETAKE_E2E_WEBKIT`): `retake open` with Scripts on, a v0 document
written as the owner writes one (a long paragraph, an unbroken 60-letter word, a long URL, headings, a list, a task)
and a plain video, on the iPhone 13 and iPhone 15:
- Nothing is wider than the screen: no sideways scroll, and no element past either edge (the document, the chat
  sheet, the video page, the home).
- The bar (the chat's pill and Send on a script, Record and Send on a video) is wholly on screen, inside `innerHeight`
  and the visual viewport.
- The top bar fits: back on the left, the document's ⋯ at the right edge and the chat's toggle just left of it, none
  overlapping, each at least 40 px.
- The chat (spec/chat.md): the pill opens the sheet over the bar with its composer focused; a message gets the fake
  agent's reply and question, its two picture choices stacked; the sheet closes again.
- Screenshots: `.retake-work/scripts-shots/webkit-<doc|doc-list|chat-closed|chat-open|chat-question|video|home>-<device>.png`.

**`21-comment`** (new only; `spec/notes.md`, "Typed comments on a video", and `spec/automation.md`, "HTTP and UI"):
`retake open <folder>` with a scratch config of two fake agents (their program is `true`), desktop Chromium.
- Idle: play, time, timeline, version, the pen, Record (a mic with the red dot, "Record voice (R)"), Send and ?; no
  agent's name anywhere in the bar; the comments header is a speech bubble, the version and To do · Done.
- The pen: one button opens the four colours and the eraser; its icon takes the colour, and the eraser glyph with E.
- A typed comment with a drawing and no recording: the video pauses, the line goes with the comment, Z undoes it,
  the playhead moves freely, Enter saves a note (`doc/1`) with its time, its line and a JPEG still, and no take is
  made; its card says Open and "→ v7". Esc drops a comment with its lines; a drawing alone saves with Enter.
- Recording: REC and the time, the pen draws into the take, and nothing in the bar moves (the boxes are equal).
- The comments' version menu says "v6 → v7" and the to-do count, and switches versions.
- Send says Send (the count in its title); held with two agents it lists them, nothing is sent until one is picked,
  and the tasks run the picked agent ("Sent to v7."). With one agent (a second server) holding Send is a plain Send.
- 30 comments (a third server): the list scrolls inside the comments column, down to the last one.
- Screenshots of each state and of the home's Settings go to `.retake-work/review2-shots/` (`RETAKE_E2E_SHOTS`).

**`21-review-webkit`** (new only; WebKit only, `RETAKE_E2E_WEBKIT`): the same review page on the iPhone 13 and
iPhone 15: the comments fill the space between the controls and Record and Send, with no comments and with 8 (the
list scrolls inside it, it has room for three one-line comments and shows three, Send doesn't move, the bubble goes
to To do), screenshots `comments-0`, `comments-8` and
`comments-8-scrolled`; every control at least 40 px and inside the screen and the visual viewport, nothing sideways;
the pen's popover; a typed comment with a drawing, saved with its Save; the version menu; recording (a Web Audio stand-in for
the microphone, and a stand-in recorder where WebKit has none), with Send not moving; and the home's Settings.

**`22-embeds`** and **`22-embeds-webkit`** (new only; `spec/scripts.md`, "Embeds"): `retake open` with Scripts on and
a document holding a YouTube address, an unknown page, a picture, a link in a sentence, a raw `<iframe>` and a Twitch
clip. Chromium on a desktop and an iPhone 15; WebKit (`RETAKE_E2E_WEBKIT`) on the iPhone 13 and iPhone 15. No site is
loaded: the test answers every request that leaves the test server (a stub page or a generated picture) and the link
preview itself (`lib/embeds.ts`).
- The YouTube frame's `src` is `youtube-nocookie.com/embed/<id>?start=42`, its attributes exactly `sandbox`, `allow`,
  `referrerpolicy`, `loading`, `title` and `src`, its box 16:9; the raw frame keeps only its address, size (4:3) and
  title; the Twitch clip names this page's host as `parent`; the link in a sentence stays a link.
- The card shows the address's host and path, then the preview's title, site and picture, without its box or what
  follows moving; a click or a tap opens the address in a new tab.
- On the desktop the address line is folded away off the caret; ArrowDown onto it shows it as typed, with the same
  frame element under it.
- Nothing is wider than the screen. Screenshots: `.retake-work/embeds-shots/` (`chromium-desktop`,
  `chromium-iphone15`, `webkit-<device>` and `webkit-<device>-embeds`).

**`23-keyboard`** and **`23-keyboard-webkit`** (new only; `spec/notes.md`, "Typing on a phone", and `spec/chat.md`):
`retake open` on a folder with a video with eight comments and a script. Chromium's mobile emulation on the iPhone
15's viewport; WebKit (`RETAKE_E2E_WEBKIT`) on the iPhone 13 and iPhone 15. No browser here raises a real on-screen
keyboard, so `lib/keyboard.ts` simulates one the way the page sees it: the visual viewport's `height` and `offsetTop`
stubbed and its `resize` or `scroll` sent.
- A video: the comment box takes the focus (compose mode) and, once the keyboard is up, its bottom is the keyboard's
  top; every other element still on screen keeps its exact box (the video too, paused); the controls, the comments
  header, the list, Record and Send, back and the bubble are hidden. A 100 px pan of the visible area is followed (the
  whole page moves with it, the box still on the keyboard). A tap on the video, the keyboard's Done (a blur), and
  Return (which saves the comment) each end it, and every box is back where it was, the list's scroll too.
- A script: the bar's pill opens the chat sheet with its composer focused; with the keyboard up the sheet's bottom is
  the keyboard's top and its top 64 px down, the composer at its bottom, and nothing behind it moved (the bar hidden
  under it); Done and the keyboard going down bring the sheet back to the bottom. ⋯ → Type in the script, then a tap
  low in the text: with the keyboard up the page ends at the keyboard, Record and Send are hidden, and the caret's line
  is above the keyboard; Done brings the bar back.
- Screenshots: `.retake-work/keyboard-shots/` (`chromium-<what>` and `webkit-<what>-<device>`, `<what>` one of
  `video`, `video-keyboard`, `video-after`, `script`, `script-chat-keyboard`, `script-doc-keyboard`).

**`23-progress`** and **`23-progress-webkit`** (new only; spec/automation.md, "Marking progress" and "The live
version"): a plain video with three typed comments, sent with Send to a fake agent that works them one at a time
(`retake working`, a different take of the video as its edit, `retake done`) and waits for the test's gate between
steps (`lib/progress.ts`). Chromium on a desktop and an iPhone 15; WebKit (`RETAKE_E2E_WEBKIT`) on the iPhone 15.
- v2 is live at once (`v2-draft1`, the page follows it, the picker says `v2`, never "draft"), and the red edge light is
  on while the agent works.
- The working comment alone has the sweep (`data-doing`) and its line ("trimming the intro…"); there is no "Agent
  working on" pill.
- Each gate moves the counts by exactly one (To do 3 → 2 → 1 → 0, Done 0 → 1 → 2 → 3), the finished card leaves To do,
  the sweep moves to the next one, and the page follows each new revision of v2 without reloading.
- Done shows each change with "in v2", which plays v2 at that comment's moment. The report makes v2 final, the edge
  light goes, and the notes are answered in v2 with the agent's words.
- Screenshots: `.retake-work/progress-shots/` (`working-<device>`, `mid-batch-<device>`).
