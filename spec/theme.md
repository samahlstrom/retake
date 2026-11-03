# Themes: your look, outside the install (Theme API v1)

This is the contract for changing how the page looks without changing Retake. Change it here first.

The look belongs to whoever runs Retake, not to the binary. A theme is a folder of CSS, fonts and images that lives
outside the install, so installing a new `retake` changes how it works and never how it looks. A team that embeds
Retake (an admin portal, a studio's review tool) keeps its own look across every update.

A theme is CSS, assets and four `theme.toml` fields. It never runs code and never changes behaviour: the pen, R to
record and every control work the same under any theme.

## Where the theme lives

The first of these that is set wins:

1. `--theme DIR` on `retake open`, `retake share` and `retake serve`. `--theme none` turns theming off.
2. `RETAKE_THEME=DIR` (or `none`).
3. The default folder:
   - `retake open` and `retake share`: `$XDG_CONFIG_HOME/retake/theme/` (`~/.config/retake/theme/`).
   - `retake serve`: `<data dir>/theme/` (`/data/theme/` in the Docker image).

A folder that doesn't exist is no theme: the built-in look, and nothing printed. A `~` at the start of DIR is the home
folder. The server reads the folder on every page load, so editing `theme.css` shows on the next reload.

## The folder

```
theme/
  theme.toml      optional: name, logo, favicon, mode
  theme.css       optional: your CSS, loaded after the built-in CSS
  logo.svg        any assets theme.css or theme.toml name (fonts, images), in any subfolder
  fonts/Brand.woff2
```

### theme.toml

```toml
name = "Acme Review"     # shown in the page titles and at the top of the home, instead of "Retake"
logo = "logo.svg"        # shown before the name at the top of the home; a path inside the theme folder
favicon = "favicon.svg"  # the browser tab's icon; a path inside the theme folder
mode = "dark"            # dark (default), light or auto (follow the computer's setting)
```

Every key is optional. An unknown key, a bad value or a file that isn't there is a warning (at startup and in
`retake theme check`) and is ignored; the rest of the theme still applies. A `theme.toml` that doesn't parse is
ignored as a whole, with a warning.

`mode` sets `<html data-theme="dark|light|auto">` and `<meta name="color-scheme">` (so native controls and scrollbars
match). The built-in tokens are the dark look. A theme that supports both writes its light tokens under
`:root[data-theme="light"]`, and for `auto` under `@media (prefers-color-scheme: light) { :root[data-theme="auto"] }`.

## How the page loads it

When a theme folder is found, the server adds these to the page's `<head>` (the review page `index.html` and the
home `home.html`), after the built-in CSS:

```html
<link rel="stylesheet" href="theme/theme.css">                <!-- when theme.css exists -->
<link rel="icon" href="theme/<favicon>">                       <!-- when favicon is set and exists -->
<meta name="retake-brand" content="<name>">                     <!-- when name is set -->
<meta name="retake-logo" content="theme/<logo>">               <!-- when logo is set and exists -->
<meta name="color-scheme" content="dark|light|light dark">
```

and `data-theme="<mode>"` on `<html>`. Without a theme folder the page is served byte for byte as built. The URLs
are relative, like the rest of the page: `/theme/theme.css` at the root, `/p/<project>/theme/theme.css` on a project
page of `retake serve` or `retake open <folder>`. The same files answer at both.

The page reads the two metas: the brand name replaces "Retake" in the titles (`<project> · <name>` on the home;
`Review · <video> · <name>` on the review page) and in the home's top left corner, after the logo. Without a theme
name or logo that corner is the built-in wordmark: "Retake" and a period in `--wordmark-dot` (the record red); the
titles stay plain "Retake".

A custom page (`--ui-dir`) gets the same additions when its HTML has a `</head>`; it may use them or ignore them.

`retake serve`'s own pages (sign in, first-run setup, short messages) are plain HTML without the page's CSS. With a
theme they get its name instead of "Retake" in the title and the heading, its logo above the heading, its favicon and
`/theme/theme.css`; their colours and fonts are the tokens with built-in fallbacks (`var(--bg, …)`), so the tokens a
theme sets apply there too. Their Content-Security-Policy then allows styles, fonts and images from the server itself,
still no script.

### The cascade

Every built-in rule is inside `@layer retake`. The theme's CSS is not in a layer, so any theme rule wins over any
built-in rule, whatever their specificity, without `!important`. A theme that uses layers of its own still wins:
unlayered and later-declared layers both beat `retake`.

### Serving theme files

`GET theme/<path>` serves a file of the theme folder:

- Only these types, by extension; anything else is 404:

  | Extension | Content-Type |
  |---|---|
  | `.css` | `text/css; charset=utf-8` |
  | `.woff2` `.woff` `.ttf` `.otf` | `font/woff2`, `font/woff`, `font/ttf`, `font/otf` |
  | `.png` `.jpg` `.jpeg` `.gif` `.webp` `.avif` `.ico` | `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `image/avif`, `image/x-icon` |
  | `.svg` | `image/svg+xml` |

- Never a script: no `.js`, `.mjs`, `.html` or anything not in the table.
- Every answer has `X-Content-Type-Options: nosniff` and
  `Content-Security-Policy: default-src 'none'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; sandbox`,
  so an SVG opened on its own can't run a script, and `Cache-Control: no-cache` (a changed file shows on reload).
- No hidden files or folders (a segment starting with `.`), no `..`, and nothing outside the folder: a symlink that
  points out of it is 404.
- At most 10 MB a file.
- The same access as the page's own scripts and styles (`assets/…`): at the root of `retake serve` they need no
  sign-in (they are the same for everyone); under a project, and through a share link, whatever the page needs.

### A missing or broken theme never breaks the page

- No folder, an empty folder, or no `theme.css`: the built-in look.
- CSS with errors: the browser skips the rules it can't read, as with any stylesheet. The built-in CSS is separate,
  so the page works.
- A `logo` or `favicon` that's missing or not an allowed type: left out, with a warning.
- A broken `theme.toml`: ignored, with a warning; `theme.css` still loads.
- The server never refuses to start over a theme. It prints one line at startup,
  `retake: theme <folder> (<name>, theme.css)`, and a `retake: theme: …` warning for each problem
  `retake theme check` would report. A folder named with `--theme` or `RETAKE_THEME` that isn't there is a warning
  too; the default folder not being there is silent.

## Tokens

Tokens are CSS custom properties on `:root`. Every colour and font of the built-in page comes from one of them; the
defaults are in `web/src/theme/tokens.css`. Set them in `theme.css`:

```css
:root {
  --accent: #2f6fed;
  --on-accent: #fff;
  --font-ui: 'Brand Sans', system-ui, sans-serif;
}
```

Hovers, hairlines and borders are `--overlay` mixed with transparency (`--line`, `--border` and the rest are defined
from it), so a light theme sets `--overlay: #000` instead of every one of them. Over the video, the pills (the hint,
the toast, the comments toggle, "← Projects") use `--scrim` and `--on-scrim`, which a light theme can leave dark.

### Colours

| Token | Default | What it colours |
|---|---|---|
| `--bg` | `#09090a` | The page and the video's stage |
| `--surface` | `#0e0e10` | The bar under the video |
| `--surface-raised` | `#121215` | The comments column, home cards, the connect card, the version list |
| `--surface-sunken` | `#0c0c0f` | The command line box, behind the agent's images |
| `--surface-control` | `#17171a` | Inputs, buttons and selects; empty thumbnails |
| `--surface-control-hover` | `#242429` | A hovered select; the select's list |
| `--surface-popover` | `#1d1d21` | Menus, popovers and tips |
| `--overlay` | `#fff` | Hovers, hairlines and borders: this colour, mostly transparent |
| `--shadow` | `#000` | Shadows |
| `--line` | `--overlay` at 7% | Hairlines between parts |
| `--line-strong` | `--overlay` at 10% | Popover edges, the command box |
| `--border` | `--overlay` at 14% | Control outlines (version, Record, Send) |
| `--border-hover` | `--overlay` at 32% | A hovered control's outline |
| `--scrim` | `#09090a` | Behind text over the video (used at 72%) |
| `--on-scrim` | `#dcdce1` | Text over the video |
| `--on-scrim-strong` | `#fff` | Bold or hovered text over the video |
| `--text-strong` | `#fff` | Hovered and emphasized text |
| `--text` | `#ececef` | Body text |
| `--text-2` | `#e4e4ea` | Comment text, menus, popovers |
| `--text-3` | `#d8d8df` | Names, empty states |
| `--text-4` | `#c9c9d1` | Times, secondary labels |
| `--text-5` | `#b9b9c4` | The agent's status, draft tags |
| `--text-6` | `#a3a3ad` | Notes in popovers, counts |
| `--muted` | `#8e8e99` | Labels, tabs, quiet words |
| `--dim` | `#85858d` | The bar's quiet text (total time, key hints) |
| `--faint` | `#6f6f7a` | Counts, the ? key, idle icons |
| `--faintest` | `#5c5c66` | Empty-project icon |
| `--wordmark-dot` | `var(--record)` | The period after "Retake" in the built-in wordmark (home, sign-in pages) |
| `--accent` | `#ececef` | Send and the other primary buttons |
| `--accent-hover` | `#fff` | A hovered primary button |
| `--on-accent` | `#111114` | Text on `--accent` |
| `--focus` | `#a693fa` | Focused fields and focus rings |
| `--link` | `#9edcff` | Inline actions (Undo after a delete) |
| `--record` | `#ff453a` | The record dot, the red edge while recording |
| `--record-text` | `#ff7a70` | "REC 0:12" and other words in the record red |
| `--record-wait` | `#d9a441` | The record dot while the mic starts |
| `--agent-edge` | `var(--record)` | The light that travels around the video's edge while the agent works on its comments (a faint still hairline with reduced motion) |
| `--voice` | `#5ac8fa` | Your voice on the timeline and the waveform, progress dots |
| `--voice-text` | `#d6eefa` | "To review" on the home |
| `--flip` | `#ffcf6e` | The version button while comparing (C) |
| `--status-open` | `#8e8e99` | Open |
| `--status-working` | `#efb441` | Working |
| `--status-question` | `var(--agent)` | Question |
| `--status-done` | `#3cd38a` | Done; the agent's dot when ready |
| `--status-off` | `#5c5c66` | The agent's dot when its computer is away |
| `--agent` | `#a693fa` | The agent's visuals on the video (boxes, arrows, lines), its question card |
| `--agent-ink` | `#16121f` | Text on `--agent` (the "Agent" chip on the video, "?" on the timeline) |
| `--agent-text` | `#ddd7ff` | The agent's words in the comments |
| `--agent-text-2` | `#c9bfff` | "Question", check marks |
| `--danger` | `#ff8f86` | Delete, errors |
| `--warn` | `#e2bd73` | A paused comment, offline warnings |
| `--change` | `#9ccfb3` | What a done comment changed |
| `--working-sweep` | `--overlay` at 5% | The light that sweeps across the comment the agent is working on |
| `--scrollbar-thumb` | `--overlay` at 14% | Every scrollbar's thumb: the document, the chat, the comments, the folder tree, the home, Settings |
| `--scrollbar-thumb-hover` | `--overlay` at 28% | The thumb under the pointer, where a page can style that (Safari on a computer, the comments' list); Chrome and Firefox shade `--scrollbar-thumb` themselves |
| `--scrollbar-track` | `transparent` | Behind the thumb |

**Scrollbars** match the page everywhere: thin, no arrow buttons, the thumb a low-contrast tone of `--overlay` on a
transparent track. Chrome and Firefox get `scrollbar-width: thin` and `scrollbar-color: var(--scrollbar-thumb)
var(--scrollbar-track)`; Safari on a computer (no `scrollbar-color`) gets the same tokens through `::-webkit-scrollbar`
(8 px, a round thumb inset 2 px, no buttons). Phones keep their own overlay scrollbars. A light theme that sets
`--overlay: #000` gets a dark thumb with no more work. The comments' list draws its own thumb from the same tokens.

The timeline and the live waveform are drawn on a canvas, from these (read when the page loads):

| Token | Default | What it colours |
|---|---|---|
| `--track` | `--overlay` at 14% | The timeline's track |
| `--track-pending` | `--overlay` at 5% | The part of a streamed version not rendered yet |
| `--track-played` | `--overlay` at 82% | The track up to the playhead |
| `--playhead` | `var(--text-strong)` | The playhead |
| `--playhead-recording` | `#ff6a60` | The playhead while recording |
| `--wave` | `--overlay` at 35% | The waveform's quiet bars |
| `--wave-idle` | `--overlay` at 12% | The flat waveform before you record |

The four pen colours (red, yellow, green, white) are not tokens: they are part of the review (the agent reads them
as words) and stay the same under every theme.

### Fonts, sizes, radii and spacing

| Token | Default | What it sets |
|---|---|---|
| `--font-ui` | `Inter, 'Adwaita Sans', system-ui, 'Noto Sans', sans-serif` | Every text |
| `--font-mono` | `ui-monospace, 'JetBrains Mono', Menlo, monospace` | Times in the comments, the command line |
| `--font-size` | `13px` (`14.5px` from 2200 px wide) | The base text size on a computer (phones use 15 px) |
| `--radius-sm` | `6px` | Small chips, menu items |
| `--radius` | `8px` | Buttons, inputs, the version button (Record and Send are this plus 1 px) |
| `--radius-md` | `10px` | Cards, menus, the version list, phone buttons |
| `--radius-lg` | `12px` | Popovers, the connect card, project covers |
| `--radius-pill` | `999px` | Pills over the video |
| `--bar-height` | `64px` (`76px` wide) | The bar under the video |
| `--bar-padding` | `24px` (`40px` wide) | The bar's left and right padding |
| `--bar-gap` | `18px` (`28px` wide) | Space between the bar's parts |
| `--record-width` | `112px` (`124px` wide) | Record's fixed width: a mic and Record, or REC and the time (it never changes while recording) |
| `--send-min-width` | `84px` | Send's least width (Send, Sending…) |
| `--agent-status-width` | `140px` | Unused since the agent's name left the bar; kept for themes that set it |
| `--sidebar-width` | `360px` (`400px` wide) | The comments column (and a script's chat) until the viewer drags its edge |
| `--home-max-width` | `1480px` (`2000px` wide) | The home's content width |
| `--card-min-width` | `260px` (`340px` wide) | The least width of a video card on the home |
| `--grid-gap` | `28px 20px` | Space between the home's cards (rows, columns) |

"Wide" is a screen 2200 px wide or more. A theme that sets one of these sets it for every width (add your own
`@media` rule for wide screens).

## Parts

Every major element has a `data-part` attribute. Select it in `theme.css`:

```css
[data-part~='bar'] { border-top-color: var(--accent); }
[data-part~='comment'][data-state='question'] { background: #1a1630; }
```

`data-part` holds one or more space-separated names, so use `~=`. An element keeps its parts across releases (see
Versioning). Classes and ids are not part of the API and can change in any release.

| Part | Element | Page |
|---|---|---|
| `stage` | The video and everything over it | review |
| `bar` | The controls under the video (desktop), and the phone's control bars | review |
| `play` | Play/pause | review |
| `time` | The current time and length | review |
| `timeline` | The timeline (track, markers, tip) | review |
| `marker` | A comment's marker on the timeline; `data-state` is its status | review |
| `ask-marker` | An agent's question ("?") on the timeline | review |
| `tip` | The tip over the timeline | review |
| `version-picker` | The version button | review |
| `version-list` | The list of versions it opens | review |
| `pen-button` | The pen: one button, its pen in the colour you draw with (the eraser while that's on); it opens the pens | review |
| `pens` | The pen's colours and the eraser, in what the pen button opens | review |
| `pen` | One pen colour; `data-color` is its colour | review |
| `eraser` | The eraser | review |
| `record` | Record your voice: a microphone with the record dot; recording, a ring, REC and the time (bar, phone pill, phone round button) | review |
| `send` | Send (the bar's one action); held, it lists the agents | review |
| `agent-picker` | The agents a held Send lists, for that one send | review |
| `send-notice` | The one line over Send when it can't reach an agent | review |
| `keys` | The ? button | review |
| `keys-sheet` | The keys it shows | review |
| `hint` | The one-line hint | review |
| `toast` | A short message over the video | review |
| `home-nav` | The back link and "Next video" over the video | review |
| `review-back` | The back link over the video: "Project › video", back to the same place on the home | review |
| `agent-visuals` | The agent's boxes, arrows and lines over the video | review |
| `agent-edge` | The light that travels around the video's edge while the agent works on this video's comments | review |
| `offline` | "The editing computer is offline" over the video | review |
| `undo` | Undo (phone) | review |
| `flip` | Compare two versions (phone) | review |
| `sidebar` | The comments column | review |
| `sidebar-toggle` | The pill over the video that shows and hides it | review |
| `sidebar-grip` | The handle on the column's left edge that resizes it (desktop; spec/notes.md, "The column's width") | review |
| `version-menu` | The version in the comments' header (`v8 ⌄`), a menu when there are more | review |
| `version-menu-list` | The versions it lists, each with where its comments go and what's still to do (`v7 → v8 · 3 to do`) | review |
| `tabs` | To do · Done (and an extension's tabs) | review |
| `comment` | One comment; `data-state` is `open`, `working`, `question` or `done` | review |
| `status` | A status: a dot and a word (in a comment, on a video card) | both |
| `into` | Where a comment's edit goes (`→ v8`; `→ v8 · rendering`, then `in v8` once done), next to its status | review |
| `doing` | The agent's line under the comment it's working on ("slowing the mask fade…"); that comment has `data-doing` | review |
| `reply-box` | The field to answer the agent | review |
| `question-card` | The agent's question under its comment (thumbnail, choices) | review |
| `progress` | The strip at the bottom of the comments (recording, transcribing, undo) | review |
| `sketch` | A comment's drawing, small, on the still of its moment | review |
| `more` | A ⋯ button | both |
| `menu` | A menu a ⋯ opens | both |
| `select` | A dropdown's button (in Settings) | home |
| `connect` | The "Connect your computer" command line and its Copy button | both |
| `home` | The home page: the folder tree and the videos | home |
| `nav` | The folder tree on the left (a drawer on a phone) | home |
| `nav-search` | The tree's filter | home |
| `nav-all` | The tree's top row: every video | home |
| `nav-folder` | A folder in the tree | home |
| `nav-project` | A project in the tree | home |
| `nav-video` | A video in the tree | home |
| `nav-grip` | The handle that resizes the tree | home |
| `nav-toggle` | ☰, which shows and hides the tree | home |
| `nav-ext` | An extension's section under the folder tree (spec/extensions.md) | home |
| `nav-ext-title` | Its header row's title, which opens the extension's page | home |
| `nav-ext-action` | Its + (the extension's action, such as New script) | home |
| `nav-ext-video` | A video in that section (an idea: only an extension's version) | home |
| `ext-slot` | Where an extension's UI mounts; `data-ext` is its id, `data-slot` the slot | home, review |
| `comment-line` | The comment box at the bottom of the comments ("Comment at 0:18 · goes into v8…", or "Comment for the agent…" on an extension's version) | review |
| `chat` | The chat with the agent, in place of the comments on a script (spec/chat.md) | review |
| `chat-thread` | Its messages | review |
| `chat-person` | A message you sent (your words, images, a voice message) | review |
| `chat-agent` | The agent's words: plain text, no label | review |
| `chat-tool` | A turn's one activity line at the top of its reply: what the agent does now (`data-state="live"`: Running a command…), then what it did (`data-state="done"`: Edited the script · ran 3 commands) | review |
| `chat-question` | The agent's question in the chat, with its choices | review |
| `chat-info` | A quiet line from Retake in the chat (Stopped) | review |
| `chat-voice` | A voice message on its way (Transcribing…) | review |
| `chat-status` | The one quiet line under the chat: what a Send started (Building v1…) | review |
| `chat-composer` | Ask the agent…: attach, the words, the mic and the send arrow | review |
| `chat-send` | The chat's send arrow | review |
| `chat-stop` | The chat's stop square while the agent replies | review |
| `chat-pill` | On a phone, the bar's Ask the agent… pill that opens the chat | review |
| `brand` | The name (and logo) at the top left of the home | home |
| `logo` | The theme's logo inside `brand` | home |
| `brand-name` | The name inside `brand` | home |
| `wordmark-dot` | The red period of the built-in wordmark "Retake." (not there with a theme's name or logo) | home |
| `account` | Who is signed in, and Sign out | home |
| `connect-card` | The "Connect your computer" card | home |
| `home-grid` | A grid of videos | home |
| `video-card` | One video; `data-status` is its status | home |
| `nav-settings` | Settings, at the bottom of the folder tree (the owner's) | home |
| `settings` | The settings page | home |
| `settings-section` | A group of settings (Agent, Computers, Extensions, Theme) | home |
| `settings-row` | One setting: its name, a line about it, and its control | home |
| `settings-switch` | An on/off switch (an extension) | home |

`data-state` and `data-status` values are part of the API too: a comment's and a marker's `data-state` is `open`,
`working`, `question` or `done`; a video card's `data-status` is the home's status word (spec/home.md); the chat's
activity line's `data-state` is `live` or `done`.

Renamed before the first release, the old name still on the new element (listed as deprecated in `api.json`, so
`retake theme check` says what to use): `agent-status` is now `send-notice`, `agent-menu` is `agent-picker`,
`version-strip` is `version-menu` and `earlier-versions` is `version-menu-list` (the agent's name left the bar, and the
version strip became the comments' version menu).

`web/src/theme/api.json` is the machine-readable list of parts and of deprecated names
(`"deprecated": {"tokens": {"--old": {"use": "--new", "since": "2.0.0"}}, "parts": {…}}`); `retake theme init` and
`retake theme check` read it, and `web/src/theme/tokens.css`, from the binary.

## Versioning

The tokens, the parts and their `data-state` values, the four `theme.toml` keys and the `theme/` URL are the Theme
API, version 1. The page says which version it is in `<meta name="retake-theme-api" content="1">` (always there,
theme or not).

- **Adding** a token or a part is not a breaking change and can happen in any release.
- **Removing or renaming** one happens only in a major release (`retake` 2.0.0, 3.0.0 …), and the old name keeps
  working for one major release: a renamed token's old name still sets the new one (`--new: var(--old, <default>)`),
  and a renamed part's element carries both names (`data-part="new old"`). Both are listed in `api.json` as
  deprecated, and `retake theme check` says what to change.
- A default value may change in any release (that's a new look); the token keeps doing the same job.
- A changed default never touches your theme: it is a file outside the install, and nothing in Retake writes it.

Release notes say which tokens and parts were added, deprecated or removed. After an update, run
`retake theme check` (docs/releasing.md says when the release notes must say so).

## Commands

```
retake theme init [DIR] [--force]
retake theme check [DIR] [--json]
```

`DIR` defaults to the folder `retake open` would use (`--theme`, `RETAKE_THEME`, then
`$XDG_CONFIG_HOME/retake/theme/`). For a hosted server, give its folder: `retake theme check /data/theme`.

**`retake theme init`** writes a starter theme into DIR (made if it isn't there):

- `theme.css`: every token at its current value (commented out, so the theme changes nothing until you uncomment a
  line), then every part with an example selector, commented out.
- `theme.toml`: the four keys, commented out.

It never overwrites a file without `--force`; with an existing file it exits 1 and says which.

**`retake theme check`** reads `DIR/theme.css` and `DIR/theme.toml` and reports what this version of Retake won't
use:

- a token the theme sets or reads that this version doesn't have (removed, renamed or misspelled). A custom property
  the theme both sets and reads itself (`--brand-blue: …; --accent: var(--brand-blue)`) is the theme's own and isn't
  reported;
- a deprecated token or part, with its new name;
- a part a selector names that this version doesn't have;
- `theme.toml`: a parse error, an unknown key, a bad `mode`, a `logo` or `favicon` that's missing or not an allowed
  type;
- a script or a page in the folder (`.js`, `.mjs`, `.html`, `.wasm` …): it is never served. Other files that aren't
  served (a font's `OFL.txt`, a `README.md`) are fine and not reported.

It prints one line per finding and a summary (`theme ok: 12 tokens, 3 parts` or `2 problems`). Exit 0 when nothing
was found, 1 when something was (so an update script can stop on it), 2 for bad arguments. `--json` prints
`{"dir", "api", "tokens": [...], "parts": [...], "problems": [{"kind", "name", "message"}]}`. A missing folder is a
problem (`no theme folder`).

## A whole page of your own

A team that wants more than a theme can ship its own page against the HTTP API: `--ui-dir DIR` serves `DIR/index.html`
(the review page) and `DIR/home.html` (the home), and their assets, instead of the built-in page. The API is versioned
on its own (spec/api.md, "API versions"). See docs/theming.md.
