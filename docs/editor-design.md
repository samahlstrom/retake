> **Decisions since this draft (owner, 2026-10-29):**
> - **Graphics.** Use the After Effects pattern, not TypeScript web pages in headless Chrome. Graphics are data in the
>   timeline JSON: layers, text, shapes, images, keyframes and easing. A Rust renderer draws them with Skia (the
>   `skia-safe` crate). Small JavaScript expressions run in an embedded QuickJS. An `html` layer type in a browser stays
>   only as an escape hatch.
> - **Previews before final renders.**
>   - The editor is a build system: every step's output is cached by a hash of its inputs.
>   - A version can be a timeline revision previewed from the cache.
>   - The final render happens only on approval.
>   - Live preview of graphics in the browser comes later, through Skia's WebAssembly build (CanvasKit).
> - **Solo first.** No real-time co-editing. The agent is the only writer of the timeline, through a patch log.
> - **The CLI placeholder `vid` below is now part of `retake`** (`retake edit …`). The engine itself is a separate Rust
>   binary that the Go CLI calls.

# Code-only editing: design

Status: draft for Sam, 2026-10-29. Scope: the editing half of the review product. In this design an agent edits in code and pushes versions into the review loop. The product name isn't decided, so the CLI is called `vid` here as a placeholder.

## Recommendation summary

- **Reusable motion graphics are code, not template files.** A component is a TypeScript module with a typed props schema, rendered in a pinned headless Chromium. Lottie and Rive are allowed only as leaf assets a component plays. MOGRT and After Effects templates are out: only Adobe apps can render them.
- **The timeline is data.** One schema-checked JSON file, changed by small patches tagged with the note ids they answer. One change to your proposed split: times are **anchors** (a VO word, another element's edge), not seconds, so a VO trim doesn't rewrite 200 numbers.
- **The renderer is a hybrid.** Footage, compositing, audio and encoding are native (ffmpeg plus a Rust compositor). The browser renders only graphics, once, as alpha frames cached by a hash of (component code, props, size, fps, frame count). Footage never enters the browser.
- **The render unit is the shot.** Each shot is encoded as a closed-GOP segment named by its content hash. That one hash drives re-render-only-what-changed, upload-only-what-changed, note carry-over, and the time map for C. Byte identity comes from reusing cached bytes, not from two renders happening to match.
- **Don't build on Remotion.** It is source-available, not open source. It is free only for individuals, non-profits and companies of up to 3 people, and render-automation products pay per render [1–3]. Every company that self-hosts our product would inherit that. Prototype the browser backend on HyperFrames' engine (Apache-2.0) [9] behind our own component contract, and keep GSAP (non-OSI license) out of the core library [10].
- **One asset catalog** covers footage, audio, images, fonts, components and brand kits: SQLite plus a content-addressed store, searchable over CLI and MCP. It produces contact sheets so the agent can see assets, tracks licenses, and blocks publishing anything not cleared. Deadlock match data is a plug-in, and the core has no Deadlock code.
- **The bridge is a render manifest sidecar.** It maps output time ranges to shot, elements, timeline path, source in/out and on-screen boxes. The review tool falls back to frame matching without it, so versions made by other tools still work.
- **Build order:** timeline schema + resolver, shot renderer + cache, manifest + review bridge, catalog, browser component runtime. The pilot is item-shop 0:00–3:10, with today's Pillow drawing code wrapped as a Python component backend so the look can match closely first.

## What the v7 script tells us

`itemshop_v7_edu.py` is 2,025 lines. Only `plan()` (lines 983–1375, about 200 calls like `pop(...)`, `bfl(...)` and `tcg(...)`) describes *this* edit. The other ~1,600 lines are machinery every video needs again: word anchors, insert ripples (`F(t)`), a frame reader, compositing (cards, glow, masks, a 3D panel warp), text rendering, a shot cache, and a numpy mixer with ducking.

Most review notes became data changes in `plan()`: text, timing, a clip swap. A few became code changes in the machinery: sub-pixel scroll, a slide-from-center transition, the `pic_end` hold. The VO trims needed a 40-line `make_vo7()` plus a function `mv()` that remapped the EDL, the word list and every later time. This design follows that seam: `plan()` becomes data, the machinery becomes a shared renderer plus versioned components, and time references become anchors.

The shot cache already pays off: the v7 preview reused 27 of its 50 shots from v6 and rendered 23 (`v7/work/render_v7.log`). It also shows the risk of hand-written cache keys. `topo_reader(T0)` reads the looping background at `T0 % 40 s`, but `shot_key()` drops `F0`. A shot that moves in time therefore reuses a render with the old background phase. §3 closes that gap.

## 1. Format and language for reusable assets

Abbreviations: "n/p" = no figure published; "n/d" = not documented. Numbers in brackets are sources at the end.

| Option | License | How it renders | Speed | Alpha out | Same input → same bytes | Agent-friendly | Verdict |
|---|---|---|---|---|---|---|---|
| MOGRT | Adobe; Stock terms bar redistributing templates in tools [18] | Premiere only (Win/macOS); no OSS renderer [18] | n/a on Linux | Via Premiere export | n/d | Low: JSON metadata around a binary AE project | No |
| AE template + aerender / nexrender | AE license; nexrender is MIT but its worker needs AE on Win/macOS [19] | After Effects | AE speed | ProRes 4444 | n/d | Low: binary, ExtendScript only | No |
| Lottie / dotLottie | Spec CSL-1.0 under the Linux Foundation [20]; ThorVG MIT, Skottie BSD-3, lottie-web MIT, velato Apache/MIT [22] | Native vector raster (ThorVG headless, no display server) or browser | Fast (native CPU), n/p | RGBA | Keyframe-only = pure function of time; bytes n/d | Edit via slots: medium. Author from scratch: low (GPT-5 zero-shot valid output 12.7% [24]) | Leaf asset only |
| Rive (.riv) | Runtime MIT; editor free but exports carry a splash; CLI license unverified [25] | Native GPU renderer; no headless video tool [25] | n/p | n/d | n/d | Medium: text "RML" exists but is tied to Rive's tools | Leaf asset, later |
| Remotion | Source-available; company license from 4 people; per-render fee for automators [1–3] | Chrome Headless Shell screenshots + ffmpeg [6] | Marketing figures only | VP8/VP9 alpha, ProRes 4444, PNG [4] | Frame-stable only if pure; no byte guarantee [5] | High (React) | No: the license follows every operator |
| Motion Canvas | MIT [7] | Browser canvas; no headless render (open since 2023) [7] | n/p | PNG sequence only [7] | n/d | High | No: stagnant |
| Revideo | MIT [8] | Puppeteer + ffmpeg, parallel workers [8] | n/p | WebM alpha, ProRes 4444 [8] | n/d | High | No: maintenance fragile |
| HyperFrames | Apache-2.0; its examples use GSAP (non-OSI) [9, 10] | Seek t = frame/fps, Chrome BeginFrame capture on Linux, ffmpeg [9] | n/p | ProRes 4444 MOV, WebM [9] | "Same input, identical output" per frame; exact pixels need pinned Docker [9] | High: HTML/CSS, built for agents | Candidate engine for our browser backend |
| MoviePy | MIT [11] | numpy frames → ffmpeg, CPU | "Slower than ffmpeg directly" [11] | VP8 alpha, RGBA | n/d | High, but low activity | No |
| MLT (`melt`) | LGPL-2.1; some modules GPL [12] | Native C; GPU via Movit / libplacebo [12] | n/p | VP8/VP9/qtrle presets [12] | n/d | Medium: XML project format | Spike as a compositor alternative |
| GStreamer GES | LGPL-2.1; official Rust crate [13] | GStreamer pipeline | n/p | Via GStreamer | n/d | Low–medium | No: complexity |
| Blender VSE | GPL-2.0+ [14] | Native, `blender -b`; GPU for some effects only [14] | n/p | PNG, FFV1, VP9, ProRes 4444 [14] | n/d | Medium (bpy) | No: heavy, GPL |
| ffmpeg filtergraph | LGPL; GPL once x264 is in [15] | Native filters | Fastest for simple ops | ProRes 4444, VP9 alpha, PNG, qtrle [15] | Yes with a pinned build, bitexact flags, fixed x264 threads, no VBV [15, 16] | Low as hand-written strings; fine when generated | Yes: decode, encode, simple shots |
| OpenTimelineIO | Apache-2.0 [17] | Not a renderer; effects are a name plus metadata [17] | — | — | — | Medium; Rust bindings stale, no Go [17] | Export format only |
| Today: Python + Pillow + numpy | Ours | Per-frame numpy compositing over ffmpeg pipes | Slow (CPU numpy at 1080p60) | — | n/d | High | Keep as the `python` backend for the pilot |
| **Proposed**: TS components + Rust/ffmpeg compositor | Ours | Graphics rendered once in a browser as alpha; footage native | Graphics cached; footage at native speed | Yes | Yes by reuse; cold cache best-effort | High | **Recommended** |

**Recommendation: plain code. TypeScript components rendering DOM/CSS/SVG/Canvas in a pinned headless Chromium.**

Why:

- **Agents read, diff, test and fix code.** "Slide in from the center" is one readable line of code, or dozens of bezier keyframes in Lottie JSON. Writing Lottie from scratch fails often: GPT-5's zero-shot valid-output rate was 12.7% in the OmniLottie benchmark [24]. A MOGRT is a zip around an After Effects project only Adobe can open [18].
- **HTML/CSS is the layout engine agents know best.** Wrapping, fit-to-width, flexbox, shadows, SVG and web fonts come free. v7 rebuilt each of these in Pillow (`text_rgba`, the two-line wrap search, the shadow blur).
- **It matches your stack.** Components can be Svelte 5 or framework-free TypeScript, and the review UI could later draw live overlay previews with the same code.
- **Template files remain possible as leaves.** A `lottie` component plays `.lottie` assets with ThorVG (MIT, headless CPU) [22]. dotLottie theme slots override color, text and image [21]; the core Lottie spec has no text layer [20]. After Effects work exported through Bodymovin loses expressions and most effects [23].

The runtime contract is language-agnostic. The core sends `{component@version, props, size, fps, frames [a,b)}` to a backend and gets back RGBA frames, a bounding-box track and optional layer instructions. There are three backends:

- **`browser`** (the default) runs in pinned Chromium. Prototype it on HyperFrames' engine, which already does frame-seeked BeginFrame capture with alpha [9]. If that doesn't fit "render one transparent component", our own CDP capture loop is small.
- **`python`** wraps today's Pillow code (`card()`, `tcg`, `itemcards.py`) so the pilot can match v7's look closely before anything is ported.
- **`native`** is Rust, for hot paths: libass captions first, vello later.

Purity is tested. CI renders a component's frames in shuffled order and must get the same hashes as an in-order render, and `Date.now`, `Math.random` and `requestAnimationFrame` fail a lint.

### Component definition

```ts
// brand-kits/samson/components/pop-text/index.ts
import { component, z, ease } from "vid/component";

export default component({
  name: "pop-text",
  version: "1.4.0",                       // semver; the lockfile pins a content hash
  props: z.object({
    text:  z.string().max(48).describe("Upper-cased. \\n splits lines; no stacking (one pop at a time)"),
    color: z.enum(["white", "yellow", "green", "red", "purple"]).default("white"),
    size:  z.number().int().min(40).max(200).default(100),
    x:     z.number().default(960),
    y:     z.number().default(910),
    maxWidth: z.number().default(1700),
    enter: z.enum(["pop", "rise", "cut"]).default("pop"),
  }),
  uses: ({ kit }) => [kit.font("display")],  // declared inputs are hashed; anything else is unreadable
  examples: [{ text: "EARLIER", color: "yellow", size: 120 }],  // rendered into the catalog preview
  mount(root, p, kit) {
    root.innerHTML = `<div class="t" style="left:${p.x}px;top:${p.y}px;max-width:${p.maxWidth}px;
      font:800 ${p.size}px ${kit.font("display").family};color:${kit.color(p.color)}">${kit.escape(p.text.toUpperCase())}</div>`;
  },
  seek(root, p, t) {                      // pure in t: same t, same pixels, in any order
    const k = p.enter === "pop" ? 1.18 - 0.18 * ease.out(Math.min(t / 0.09, 1)) : 1;
    root.querySelector<HTMLElement>(".t")!.style.transform = `translate(-50%,-50%) scale(${k})`;
  },
});
```

**Footage-aware components** (floating card, 3D shop panel, cursor spotlight) declare footage inputs (`panel: footage()`) but never receive footage pixels. They return **layer instructions** instead: per-frame quads, crops, rounded masks and opacity for each input, plus their own RGBA layers above and below. The native compositor moves the footage, so hours-long OBS recordings never enter the browser.

## 2. Timeline vs components

**I agree with the split: the timeline is data and components are code.** Four refinements:

1. **Times are anchors.** Grammar: `word:<spine-id>/<word>[#n][@end][±s]`, `start:<id>`, `end:<id>`, `sync:{src, to}`, and `t:<seconds>` as a discouraged escape hatch. A deterministic resolver turns anchors into frames. Resolved times go into the manifest, never back into the timeline.
2. **A spine carries the master clock.** VO ranges and the inserts that push them form one ordered list, like Final Cut's primary storyline. Shots, overlays and audio attach to spine anchors. Inserting the Yu-Gi-Oh clip ripples everything after it with no arithmetic. That replaces `F(t)` and `resolve_inserts()`.
3. **No macros in the timeline.** v7's `pops()` and `tcg()` helpers become components with array props (`pop-sequence`, `card-deal`). An agent may write a script that *generates* a patch, such as a first cut from a footage query, but only the expanded data is stored.
4. **Code changes are version pins.** Some notes are component behavior ("hard-cut, don't fade"). The agent ships `card-deal@2.1.0` and re-pins only the elements the note is about. An upgrade never re-renders a scene nobody asked about; if it did, the reviewer could no longer trust "unchanged".

Patches address elements by id, not array index (so not RFC 6902), and carry their base revision, the notes they answer and a summary:

```json
{ "base": 41, "answers": ["v3#5"], "summary": "Lane fight -> Oct 23 Paige loss; DEFEAT lands on 'losses'",
  "ops": [ { "op": "set", "id": "why/losses", "path": "picture",
             "value": { "use": "footage", "asset": "proj:rec/2026-10-23_22-05-54",
                        "sync": { "src": 5912.2, "to": "word:why_d/losses" } } } ] }
```

`sync` replaces this line of v7: `clip(S("why_d") - 0.1, "oct23", 5912.2 - (w("why_d", "losses") - (S("why_d") - 0.1)))`.

## 3. Renderer architecture

Pipeline: **resolve → plan units → render layers → composite units → mix audio → package.**

1. **Resolve.** Resolve anchors against catalog transcripts into a flat, absolute-time list of units (shots, split at insert points) and their elements. Failures are typed errors the agent can act on: `anchor word:why_b/Eternus not found in spine why_b (did you mean 'Eternus,'?)`.
2. **Plan** two kinds of key:
   - **Layer key** (expensive, position-independent): hash(component content hash, props, size, fps, frame count, backend image). A pop is reused wherever it lands.
   - **Unit key** (cheap): hash of the resolved unit description, including absolute-time inputs like the background loop phase. That description is the *only* input the compositor gets, so the key can't miss an input. This is the hermetic rule that closes the topo gap.
3. **Render layers.** Backends render missing layers into the store. Identical consecutive frames are stored once, which helps because most pops are static after 0.1 s.
4. **Composite units.** The Rust compositor reads footage through ffmpeg pipes (v7's `Reader` pattern). It applies cut, crop, sub-pixel zoom, color, quads and masks, overlays the cached layers, and pipes frames to the encoder. Plain shots can run as one generated ffmpeg filtergraph instead; the agent never writes filter strings. Every unit starts on an IDR frame with identical encoder settings, so units concatenate with `-c copy`, as v7 already does.
5. **Mix audio** for the whole timeline in one pass: VO, inserts, game ambience, ducked beds, SFX and loudness targets. Mixing takes seconds, beds cross unit boundaries, and per-unit AAC segments would click at the seams because of encoder priming [27]. The manifest records a hash of the mixed PCM per unit, so "audio unchanged here" is exact.
6. **Package.** Locally: one MP4 (concat + mux, faststart). Hosted: CMAF segments named by unit hash plus a playlist per version. A segment's `tfdt` timestamp depends on its position, so segments are stored at zero and the server rewrites `tfdt` on the way out. A rippled shot is still the same stored bytes.

**Why two keys.** Two keys fix the topo problem without losing reuse. The looping background is a native layer drawn at absolute time during compositing, and the cards are cached layers on top. When a trim shifts a card shot by 0.5 s, the card layer is reused, the background phase is correct, and only the cheap composite and encode run again.

**Determinism.** Unchanged scenes produce identical bytes because they are reused: same unit key, same stored file. Re-render determinism matters only with a cold cache (a new machine, CI, a render farm):

- **ffmpeg:** pin the build. `-fflags +bitexact` writes only platform-, build- and time-independent data; `-flags:v +bitexact` and `sws_flags=bitexact` cover codecs and scaling [15].
- **x264:** stable for a fixed build and thread count. A different thread count changes the output, and threaded VBV is non-deterministic by design [16]. Pin `threads`; no VBV on cacheable renders.
- **Chromium is the weak link.** BeginFrame capture and `--deterministic-mode` are both experimental [26]. Playwright warns that rendering varies by host, hardware and headless mode [26], and HyperFrames pins Chrome, fonts and ffmpeg in Docker for exact output [9]. So: pin a container image, bundle fonts, rasterize in software, and put the image digest in layer keys. Cross-machine equality is best-effort, which is fine because the cache guarantees identity.

**GPU.** The 4090 runs NVENC for draft previews, and ASR and embeddings in the catalog. Cacheable renders use x264, whose determinism rules are documented (NVENC's aren't). A wgpu compositor comes later, only if profiling demands it.

**Compositor alternative.** MLT is the strongest off-the-shelf native compositor: LGPL, active, a documented XML format, GPU modules [12]. Spend a week compiling the float-card and 3D-panel shots to MLT XML before writing the Rust compositor. Adopt MLT if it handles quads, rounded masks and sub-pixel zoom cleanly and its output is stable run to run (not documented).

## 4. Asset management for agents

**Storage.** A content-addressed blob store (`blobs/b3/<hash>`, BLAKE3) holds imported and generated files. Large recordings are referenced in place: hashed once, never copied. The catalog is SQLite (WAL) with FTS5 and sqlite-vec, as `classification-and-embeddings.md` recommends; sqlite-vec is still pre-1.0 [28].

**Scopes.** Ids carry their scope, and lookup goes project, then brand, then global:

- `lib:` (global): CC0 SFX, fonts.
- `brand:samson/`: a versioned kit with tokens (colors, fonts, sizes, safe areas), components, music and house lint rules.
- `proj:`: VO, memes, generated art.

Components are catalog entries too, immutable per version and identified by content hash.

### A catalog entry

```json
{
  "id": "proj:rec/2026-10-23_22-05-54",
  "kind": "video",
  "scope": "project",
  "blob": { "hash": "b3:5e0c…", "locator": "file:///…/2026-10-23 22-05-54.mp4", "in_store": false },
  "media": { "duration": 7311.4, "size": [1920, 1080], "fps": 60, "vfr": false,
             "audio": [ { "stream": 1, "role": "mic" }, { "stream": 2, "role": "game" } ] },
  "proxy": { "hash": "b3:91aa…", "size": [960, 540], "gop": 1 },
  "title": "OBS recording, Oct 23 evening (Paige ranked)",
  "tags": ["paige", "ranked", "defeat"],
  "transcript": { "track": "mic", "model": "faster-whisper large-v3", "ref": "tx:7781" },
  "previews": { "contact_sheet": "b3:0d2f….webp", "every_s": 60, "waveform": "b3:a913….png" },
  "spans": 412,
  "clocks": [ { "domain": "deadlock:match/123469477", "plugin": "deadlock",
                "epochs": [ { "asset_t": [1820.0, 5920.0], "domain_t0": -42.3, "rate": 1.0 } ],
                "confidence": "high", "method": "hud_clock_ocr x7" } ],
  "license": { "status": "owned", "publish": true },
  "provenance": { "importer": "obs-folder", "seen": "2026-10-24T03:12Z" }
}
```

**Spans** generalize the footage DB's `segments`: `(asset, t0, t1, type, label, data, source_of_label, confidence)`. A span is both a search result and a timeline source (`{"use":"footage","span":"sp:88412","pre":4,"post":3}`).

**Plug-ins** are executables in any language speaking JSON over stdio. A plug-in can declare:

- span types (with a schema for `data`);
- clock domains (Deadlock: `vod = game + offset`, split into pause epochs);
- indexers for new assets;
- tables and read-only SQL views;
- MCP tools, components (rank ladder, item cards) and lint rules.

Today's footage DB becomes the `deadlock` plug-in nearly unchanged, with `footage find`, `sql` and `cut` as its tools. A cooking channel installs nothing extra. Transcripts, visual embeddings, shot boundaries and manual tags come from the core; a `recipe-steps` plug-in could add spans from transcript phrases.

**Seeing assets.** Every asset gets previews at import, which a multimodal agent requests as images:

| Asset kind | Previews |
|---|---|
| Video | Contact sheet with burned-in timecodes; a per-span 6-frame strip |
| Audio | Waveform + loudness PNG |
| Font | Specimen sheet |
| Image | Thumbnail |
| Component | Storybook sheet: each `examples` entry at 5 times, plus a 2-second low-fps WebP |
| Brand kit | One-page style sheet |

`asset_preview(id, at=[…])` renders frames on demand from the proxy.

**Search** applies structured filters (kind, scope, tags, license, plug-in SQL), then merges FTS over titles, tags, transcripts and captions with vector search over windowed embeddings by reciprocal-rank fusion. Results are asset and span ids, never model-made timestamps.

**Licenses.** Every entry carries a status:

| Status | Meaning |
|---|---|
| `owned` | Your own material |
| `cleared` | Licensed, no attribution needed |
| `attribution` | Licensed; the credit line is stored |
| `review-only` | Fine in drafts, blocked at publish |
| `blocked` | Never use |

Entries can also carry `content_id_risk` (the Gojo clip was blocked on upload). `INDEX.csv` imports directly: myinstants rows become `review-only` with high Content ID risk, and Kenney rows become `cleared` (CC0). `license_report(version)` lists every asset in the output with its time ranges and writes the description credits. `publish` refuses while any `review-only` or `blocked` asset is seen or heard.

**Dedup.** Exact duplicates are caught by content hash. Near duplicates are caught by perceptual hashes (pHash/dHash for images, frame-pHash sequences for video, Chromaprint for audio): "this looks like `lib:memes/thanos-rem` (distance 4)". Derived files (proxies, trims, deep-fried audio) record parent and recipe, so they can be rebuilt instead of kept.

### MCP tools (assets and rendering)

Every tool is also a CLI command with `--json` output.

| Tool | Does |
|---|---|
| `asset_search(q, kind?, scope?, tags?, license?, where?)` | Hybrid search; returns entries + preview URIs |
| `asset_get(id)` / `asset_preview(id, at?, kind?)` | Entry; contact sheet, frames, waveform or specimen as images |
| `span_find(type?, where?, near?)` / `span_add(asset, t0, t1, type, label)` | Moments inside assets; manual tags |
| `asset_import(path|url, scope, license)` | Hash, probe, proxy, transcript, previews, dedup check |
| `plugin_query(plugin, tool, args)` | e.g. `deadlock.sql`, `deadlock.find --type team_wipe` |
| `component_describe(name)` | Props schema, examples, preview sheet, changelog |
| `license_report(version)` | Assets used, by time range; credits text; publish blockers |
| `timeline_get(scene?)` / `timeline_patch(patch)` | Read; apply a validated patch → diff, affected units, render estimate |
| `timeline_log()` / `timeline_revert(patch_id)` | Patch history with note ids |
| `render_plan(range?)` | Units, cache hits and misses, time estimate |
| `render_frames(times[])` | Stills at given times (renders only those frames); for self-checks |
| `render_range(a, b, quality)` / `render_status(job)` | Draft or final render of a range |
| `render_compare(va, vb, times[])` | Side-by-side stills with the time map applied |
| `notes_list(version, status?)` / `notes_resolve(version, t, box?)` | Notes with resolved targets |
| `note_ask(version, t, text, note?)` | Question pinned to a moment |
| `version_push(label, changelog)` | Publish a version to the review tool |

## 5. Connection to the review tool

**Render manifest.** Each version gets a `manifest.json` sidecar with a public, small spec, so other renderers can emit it too. One unit:

```json
{ "unit": "u17", "scene": "why-this-matters", "shot": "why/popular-float",
  "t": [34.083, 38.917], "frames": [2045, 2335],
  "video_hash": "b3:9f2c…", "audio_hash": "b3:41aa…", "segment": "seg/9f2c….m4s",
  "elements": [
    { "id": "why/popular-float", "ptr": "/scenes/1/shots/2", "kind": "picture", "z": 0,
      "use": "float-card@2.1.0", "box": [[34.083, 60, 120, 1100, 960]],
      "sources": [ { "asset": "lib:broll/item-shop", "in": 233.0, "out": 237.834 } ] },
    { "id": "why/ranks", "ptr": "/scenes/1/overlays/4", "kind": "overlay", "z": 3,
      "use": "deadlock/rank-ladder@1.0.0", "t": [34.18, 37.62],
      "box": [[34.18, 1440, 120, 1650, 1000]],
      "anchors": { "from": "word:why_b/Everyone+0.10", "to": "word:why_b/same-0.30" } } ],
  "words": [ [34.08, 34.41, "Everyone"], [34.41, 34.52, "at"] ] }
```

**Resolving a note.** When notes are built, the review tool calls `notes_resolve(v, t, box)`. It ranks the elements live at `t` by stroke-box overlap, then z-order, and attaches the spine word at `t` for `(audio)` notes. A red loop around the right column at 0:36.7 resolves to `why-this-matters / why/ranks`, ahead of the float card under it. Each note in `notes.json` gains `targets: [...]`. Without a manifest, notes keep time and strokes only, as today.

**Patches tagged with notes.** Every patch carries `answers: [note ids]`, and a note can take several patches. A patch with `answers: []` is the agent's own initiative and is listed separately, so you see everything that changed.

**Pushing a version.** `version_push` publishes the media, manifest and changelog:

```json
{ "version": "v8", "from": "v7", "rev": 57,
  "notes": [
    { "note": "v7#5", "status": "done", "patches": ["p52"], "targets": ["why/losses"],
      "at_old": 60.9, "at_new": 61.1, "summary": "Lane fight -> your Oct 23 loss; DEFEAT on 'losses'" },
    { "note": "v7#2", "status": "question", "at_new": 36.7,
      "question": "Start the ladder at Obscurus ('?') or at Initiate?" } ],
  "unrequested": [ { "patch": "p55", "summary": "Bed crossfade moved 0.2 s to follow the trim" } ],
  "units": { "total": 50, "reused": 41, "rendered": 9 } }
```

**Time map and C.** Units with the same `video_hash`, or the same shot id, in both manifests give a piecewise-linear map from v7 time to v8 time. With it, C flips to the *corresponding* moment, not the same clock time. v7's NOTES had to tell you that "v7 runs 0.22 s later up to 1:58"; the map handles that offset automatically.

**Carrying notes over.** For each note on the old version:

- Its unit's `video_hash` (and `audio_hash` for `(audio)` notes) exists in the new version → carry it to the mapped time as *unchanged*.
- The unit changed and the note was answered → *answered, check it*.
- The unit changed for another reason → *context changed*.

This replaces `review inherit`'s 2-second frame sampling. Frame sampling stays as the fallback for versions without a manifest.

## 6. Agent workflow: 14 notes on v7 → v8 pushed, 11 answered, 3 questions

1. `notes_list(v7)` returns 14 notes with transcripts, stroke snapshots, retraction flags and resolved targets. The agent reads the snapshots as images.
2. **Triage** (agent reasoning, no tool): merge continuations (v7's #3 was the start of #4), apply retractions (later overrides earlier), and sort into *data edit*, *component change*, *footage search* and *ambiguous*.
3. For footage notes ("change this clip", "use my own games, not Viscous"):
   - `plugin_query(deadlock, find, {type:"defeat", hero:"paige", has_footage:true})`;
   - `asset_preview(span, kind="strip")` to look at the candidates;
   - `span_find(where="label LIKE '%build page%'")`.
4. For each actionable note: `timeline_patch({answers:[…], ops:[…]})`. The tool validates the schema, resolves anchors and returns the diff plus `affected: 3 units, ~40 s render`. A failed anchor is fixed and retried.
5. For component notes ("slide from the center, don't jump"): edit the component, run its purity test and storybook render (`component_describe` shows the new sheet), bump to `float-card@2.2.0`, and re-pin only the targeted elements in the same patch.
6. **Self-check:**
   - `render_frames([…])` at every touched moment, plus `render_compare(v7, draft, …)`;
   - plug-in lints: VO pause check, footage freeze/black check, house rules (one pop at a time, at most 2 zooms);
   - `license_report(draft)`.
7. Three notes need you (which rank emblem to start on, a text wording, which of two clips): `note_ask(v7, t, text, note)` for each, pinned at the moment.
8. `render_range(0, 190, final)` → 50 units, 41 reused, 9 rendered, audio re-mixed.
9. `version_push("v8", changelog)`. The review tool shows v8 with 11 notes answered, 3 questions, the carried-over notes and the C time map.

Typical call count: 1 notes call, 5–10 searches and previews, about 12 patches, 2 component edits, 15–30 frame checks, 1 render, 1 push.

## 7. Pilot: item-shop v7 preview (0:00–3:10) as data

How the script maps over:

| v7 script | New format |
|---|---|
| `make_vo7()` trims | 3 patches that shorten spine items |
| `INSERTS` | Spine items |
| `plan()` | Scenes |
| `BEDS` + `mix()` | `music-bed` elements + the brand kit's mix settings |
| `card` / `tcg` / `float` / `p3d` / `ranks` | Components, first via the Python backend so the pixels match |

Excerpt (the cold open and the start of "why this matters"):

```json
{
  "schema": "timeline/0.1", "id": "itemshop", "rev": 41,
  "format": { "size": [1920, 1080], "fps": 60, "sample_rate": 48000 },
  "kits": ["brand:samson@3", "plugin:deadlock@0.4"],
  "spine": [
    { "id": "open_a", "asset": "proj:vo/take-2", "in": 3.212, "out": 9.871 },
    { "id": "open_b", "asset": "proj:vo/take-2", "in": 11.040, "out": 17.336 },
    { "id": "yugioh", "asset": "proj:memes/yugioh-trap-card", "in": 8.85, "frames": 151,
      "hold_from": 11.216, "audio": { "lufs": -20, "fade_out": 0.10 } },
    { "id": "open_c", "asset": "proj:vo/take-2", "in": 17.80, "out": 29.95 },
    { "id": "why_a", "asset": "proj:vo/take-2", "in": 31.02, "out": 33.10, "gap_before": 0.70 }
  ],
  "scenes": [
    { "id": "cold-open",
      "shots": [
        { "id": "open/fight", "at": "start:open_a",
          "picture": { "use": "footage", "asset": "proj:rec/2026-10-23_22-05-54", "in": 3278.8 } },
        { "id": "open/panel", "at": "word:open_a/This",
          "picture": { "use": "shop-panel-3d@2.0.0", "props": {
            "bg": { "asset": "lib:broll/item-shop", "in": 4.0 },
            "panel": { "asset": "lib:broll/item-shop", "in": 233.0 } } } },
        { "id": "open/deck", "at": "word:open_b/deck-0.12",
          "picture": { "use": "card-deal@1.3.0", "props": {
            "cards": [ { "item": "Knockdown" }, { "item": "Refresher" }, { "item": "Healing Rite" } ],
            "deal_every": 0.13, "cy": 560 } } }
      ],
      "overlays": [
        { "id": "open/flea", "use": "pop-text@1.4.0", "from": "word:open_b/flea", "to": "word:open_b/No-0.05",
          "props": { "text": "NOT A FLEA MARKET", "size": 96, "y": 940 } },
        { "id": "open/gigachad", "use": "mascot@1.0.0", "from": "word:open_c/dominant", "to": "end:open_c+0.3",
          "props": { "pose": "brand:samson/poses/knight_meme_gigachad", "side": "right", "h": 520 } }
      ] },
    { "id": "why-this-matters",
      "shots": [
        { "id": "why/title", "at": "start:why_a-0.25",
          "picture": { "use": "title-card@1.1.0", "props": { "title": "WHY THIS MATTERS" } } },
        { "id": "why/popular-float", "at": "start:why_b-0.2",
          "picture": { "use": "float-card@2.1.0", "props": { "side": "left",
            "src": { "asset": "lib:broll/item-shop", "in": 233.0, "crop": [568, 26, 1184, 956] } } } }
      ],
      "overlays": [
        { "id": "why/rank-pops", "use": "pop-sequence@1.0.0", "until": "word:why_b/same-0.3",
          "props": { "x": 1545, "y": 215, "size": 66, "color": "yellow",
            "items": [ { "at": "word:why_b/Everyone", "text": "EVERYONE\nAT YOUR RANK" },
                       { "at": "word:why_b/Eternus-0.1", "text": "UP TO\nETERNUS" } ] } },
        { "id": "why/ranks", "use": "deadlock/rank-ladder@1.0.0",
          "from": "word:why_b/Everyone+0.10", "to": "word:why_b/same-0.30",
          "props": { "highlight_at": "word:why_b/Eternus", "x": 1545, "y": 640, "h": 118 } }
      ] }
  ],
  "audio": [
    { "id": "bed/open", "use": "music-bed@1.0.0", "asset": "brand:samson/music/all-good-things-come-in-threes",
      "from": "start:open_a", "to": "start:why_a", "props": { "lufs": -33, "duck_db": -7, "xfade": 3.0 } }
  ]
}
```

**Pilot acceptance:**

1. Render 0:00–3:10 and compare it with `itemshop_v7_0-310_preview.mp4` per frame. Python-backend components should come within a small PSNR tolerance, not exact bytes, because encoder and compositor rounding differ.
2. Replay three real v3 notes as patches.
3. Confirm only the touched units re-render and that the manifest resolves your strokes to the right elements.

## 8. Costs, risks, what to build first

**What's hard:**

- **Frame-accurate decode from long-GOP, sometimes VFR recordings.** Seeking a 2-hour OBS file per shot is slow and easy to get off by a frame. Check for CFR at import, and cut drafts from short-GOP proxies.
- **Browser render speed.** Per-frame capture is the slowest stage, and nobody publishes fps figures, so measure it in the pilot. Mitigations: render layers once, dedup frames, move captions to the native backend. Hosting Chromium also needs sandboxing and memory limits.
- **Cache keys.** Too few inputs give stale renders (the topo gap); too many re-render everything. Hermetic backends (no file or network access beyond declared inputs) make keys correct by construction, at the cost of routing every input through the catalog.
- **Schema churn.** The format will change under real use. Version it from day one (`timeline/0.1`) and ship migrations with the resolver.
- **Taste.** Making components look good is design work. House style has to live in the brand kit and its lints, or every agent run reinvents it.
- **A fast-moving dependency.** HyperFrames ships near-daily releases at v0.8 [9]. Pin it behind the component contract, with our own capture loop as the exit.
- **Toolchain licenses.** Enabling x264 puts the whole ffmpeg build under the GPL [15], so ship ffmpeg as a separately invoked binary (or use the system's), never linked into the core. GSAP's free license isn't OSI and excludes products that let users make animations without code in competition with Webflow [10]. "An agent animates for you" is close enough to that line to keep GSAP out of the core.

**Build first (in order):**

1. Timeline schema, anchor resolver, patch op set and patch log, with the pilot excerpt as a fixture.
2. Shot renderer: ffmpeg I/O plus a Rust compositor, the two-level cache and the manifest.
3. Review bridge: `notes_resolve`, changelog, time map for C, hash-based carry-over.
4. Catalog: import `INDEX.csv`, the B-roll index and footage.db (as the `deadlock` plug-in); FTS search; contact sheets; license status.
5. Component runtime: Python backend (wrap v7 drawing code), then the browser backend and storybook previews.
6. MCP server over the same commands.

**Defer:**

- embeddings search (FTS + plug-in SQL first);
- Lottie/Rive leaf players;
- OTIO export (cuts and markers only, since OTIO doesn't model effects [17]);
- the hosted render farm and CMAF packaging;
- the GPU compositor;
- the native component backend beyond captions;
- the style scorer.

## Open questions for Sam

1. Should TypeScript components be Svelte 5 (same as the review UI) or framework-free DOM? Svelte gives a live overlay preview in the UI later; framework-free is simpler to sandbox.
2. Is a perceptual match (small PSNR/SSIM tolerance) enough for the pilot? Exact frames would mean keeping v7's numpy compositing as well as its Pillow drawing.
3. Should C flip to the mapped moment (new) or the same clock time (today)?
4. Where does the VO edit live: inside the timeline spine (proposed) or as a separate VO project the timeline references?
5. Brand kit as a git repo (diffable, shareable) or a catalog-only package?
6. Should component code changes ever re-render scenes you didn't comment on, or is "pin per element" the rule?
7. Hosted service: is it acceptable that Chromium runs server-side for graphics, or should hosted renders need a local worker?
8. License gate strictness: block publish on `review-only` assets, or warn and require a typed confirmation?

## Sources

Checked 2026-10-29. Local facts (line counts, cache hits, the topo key gap) come from `itemshop_v7_edu.py` and `~/Videos/deadlock/item-shop/v7/work/` (read-only).

1. Remotion license: https://github.com/remotion-dev/remotion/blob/main/LICENSE.md
2. Remotion license FAQ ("source-available… not open source", Automators pricing): https://www.remotion.dev/docs/license/faq
3. Remotion terms (headcount, user-supplied code, distribution): https://www.remotion.dev/docs/license/terms
4. Remotion transparent video: https://www.remotion.dev/docs/transparent-videos
5. Remotion flickering / purity: https://www.remotion.dev/docs/flickering
6. Remotion Chrome Headless Shell: https://www.remotion.dev/docs/chrome-headless-shell
7. Motion Canvas: https://github.com/motion-canvas/motion-canvas/blob/main/LICENSE, headless issue https://github.com/motion-canvas/motion-canvas/issues/415, yuv420p exporter https://github.com/motion-canvas/motion-canvas/blob/main/packages/ffmpeg/server/FFmpegExporterServer.ts, releases https://github.com/motion-canvas/motion-canvas/releases
8. Revideo: https://github.com/midrender/revideo (LICENSE, README, `packages/ffmpeg/src/ffmpeg-exporter-server.ts`)
9. HyperFrames: https://github.com/heygen-com/hyperframes, https://hyperframes.heygen.com/concepts/determinism, https://hyperframes.heygen.com/packages/engine
10. GSAP license: https://gsap.com/community/standard-license/ and https://webflow.com/blog/gsap-becomes-free
11. MoviePy: https://github.com/Zulko/moviepy
12. MLT: https://github.com/mltframework/mlt/blob/master/COPYING, https://www.mltframework.org/docs/mltxml/, https://github.com/mltframework/mlt/tree/master/presets/consumer/avformat/alpha, https://www.mltframework.org/
13. GES: https://gstreamer.freedesktop.org/documentation/gst-editing-services/index.html, https://crates.io/crates/gstreamer-editing-services
14. Blender: https://www.blender.org/about/license/, https://docs.blender.org/manual/en/latest/files/media/video_formats.html, https://developer.blender.org/docs/release_notes/5.2/sequencer/
15. FFmpeg: https://ffmpeg.org/legal.html, https://ffmpeg.org/ffmpeg-formats.html, https://ffmpeg.org/ffmpeg-codecs.html, https://ffmpeg.org/ffmpeg-scaler.html
16. x264 determinism: https://forum.doom9.org/archive/index.php/t-129887.html, https://mailman.videolan.org/pipermail/x264-devel/2015-April/011035.html
17. OpenTimelineIO: https://github.com/AcademySoftwareFoundation/OpenTimelineIO/blob/main/LICENSE.txt, https://opentimelineio.readthedocs.io/en/latest/tutorials/otio-timeline-structure.html, https://github.com/vfx-rs/opentimelineio-bind
18. MOGRT: https://filext.com/file-extension/MOGRT, https://fileinfo.com/extension/mogrt, https://stock.adobe.com/license-terms
19. nexrender: https://github.com/inlife/nexrender
20. Lottie spec: https://www.linuxfoundation.org/press/lottie-animation-community-announces-lottie-v1.0-specification, https://github.com/lottie/lottie-spec/blob/main/License.md, https://github.com/lottie/lottie-spec/tree/main/schema/layers
21. dotLottie 2.0: https://dotlottie.io/spec/2.0/, https://docs.lottiefiles.com/en/format/dotlottie/interactivity/theming/specification
22. Lottie runtimes: https://github.com/thorvg/thorvg, https://github.com/google/skia/blob/main/LICENSE, https://github.com/airbnb/lottie-web, https://github.com/linebender/velato, https://github.com/LottieFiles/dotlottie-rs
23. After Effects → Lottie limits: https://docs.lottiefiles.com/en/integrations/after-effects/09_lottie-compatibility
24. OmniLottie (CVPR 2026): https://arxiv.org/abs/2603.02138
25. Rive: https://github.com/rive-app/rive-runtime, https://rive.app/docs/runtimes/advanced-topic/format, https://rive.app/pricing, https://rive.app/docs/cli/reference/commands
26. Headless Chromium: https://github.com/ChromeDevTools/devtools-protocol/blob/master/pdl/domains/HeadlessExperimental.pdl, https://chromium.googlesource.com/chromium/src/+/refs/heads/main/headless/public/switches.h, https://playwright.dev/docs/test-snapshots
27. AAC priming: https://developer.apple.com/library/mac/technotes/tn2258/_index.html, https://developer.apple.com/documentation/http-live-streaming/preparing-audio-for-http-live-streaming
28. sqlite-vec: https://github.com/asg017/sqlite-vec

Not verified: absolute render speeds for any tool (only Remotion's marketing figures exist), byte-level determinism for MLT, GES, Blender and MoviePy, and the Rive CLI's license.
