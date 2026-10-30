# Assets and plug-ins

<!-- STUB (asset catalog worker): `retake assets …`, the catalog, plug-ins. Replace this page. -->
> **Stub.** The asset catalog (`retake assets …`) is being built. Its worker replaces this page with the real
> commands and formats.

What the catalog is for, from the design ([editor-design.md](editor-design.md), section 4):

- **One catalog** for footage, audio, images, fonts and brand kits: SQLite plus a content-addressed store. Long
  recordings are referenced in place and hashed once, never copied.
- **Scopes.** Asset ids carry their scope (`proj:`, `brand:`, `lib:`), and lookup goes project, then brand, then
  global. The timeline already uses these ids (`"proj:vo"`).
- **Seeing assets.** Each asset gets previews an agent can look at: a contact sheet for video, a waveform for audio,
  a specimen for a font.
- **Search** by kind, tags, license and text (titles, transcripts), returning asset ids, never made-up timestamps.
- **Licenses.** Each asset has a status: `owned`, `cleared`, `attribution`, `review-only` or `blocked`. A render can
  list every asset it uses, with credits, and publishing can refuse `review-only` ones.
- **Plug-ins** are programs in any language that speak JSON over stdio. A plug-in can add span types (moments inside
  assets), indexers, read-only SQL views, tools and lint rules. The core has no domain-specific code.

## Commands

<!-- STUB (asset catalog worker): the `retake assets` commands. -->
> **Stub.** `retake assets …` goes here.

## Writing a plug-in

<!-- STUB (asset catalog worker): the plug-in protocol. -->
> **Stub.** The plug-in protocol goes here.
