# Your look: themes

Retake's look is yours. A theme is a folder of CSS, fonts and images that lives outside the install, so updating
`retake` (a new binary, a new Docker image, a reinstall) changes how it works and never how it looks. Your team's
colours, font and logo stay put.

A theme can't change what Retake does: it's CSS and four settings, never a script. The contract is
[spec/theme.md](../spec/theme.md); this page is the how-to.

## Start one

```bash
retake theme init                 # writes ~/.config/retake/theme/theme.css and theme.toml
$EDITOR ~/.config/retake/theme/theme.css
retake open ~/Videos              # picks the theme up; reload the page after each edit
```

`theme.css` lists every token (colour, font, size) at its current value, commented out, and every part you can
select. Uncomment what you want to change:

```css
:root {
  --accent: #2f6fed;       /* Send and the other primary buttons */
  --on-accent: #fff;
  --focus: #2f6fed;
}
```

`theme.toml` names the theme and adds a logo:

```toml
name = "Acme Review"     # in the page titles and the home's top left, instead of "Retake"
logo = "logo.svg"        # before the name on the home
favicon = "favicon.svg"  # the browser tab
mode = "dark"            # dark, light or auto
```

## Where it lives

| How you run Retake | Theme folder |
|---|---|
| `retake open`, `retake share` | `~/.config/retake/theme/` (`$XDG_CONFIG_HOME/retake/theme/`) |
| `retake serve` | `<data dir>/theme/` |
| Docker (`deploy/`) | `/data/theme/` in the `retake-data` volume |
| Any of them | `--theme DIR`, or `RETAKE_THEME=DIR` |

`--theme none` (or `RETAKE_THEME=none`) shows the built-in look, which is handy to rule your theme out when something
looks off. On a server, copy the folder into the volume:

```bash
docker compose cp ./my-theme retake:/data/theme
```

The server reads the folder on every page load, so there's nothing to restart after an edit.

## Colours

Every colour of the page is a token. The ones you'll want first:

| Token | What it colours |
|---|---|
| `--bg`, `--surface`, `--surface-raised` | The page, the bar under the video, the comments column and cards |
| `--text`, `--muted` | Body text, quiet labels |
| `--accent`, `--on-accent` | Send and primary buttons, and the text on them |
| `--focus` | Focused fields and focus rings |
| `--record` | The record dot and the red edge while recording |
| `--status-open`, `--status-working`, `--status-question`, `--status-done` | A comment's four states |
| `--agent` | The agent's boxes and arrows on the video, and its questions |

Hovers, hairlines and borders are `--overlay` (white by default) at a low opacity, so one line flips all of them for a
light theme: `--overlay: #000`. Text over the video (the hint, the toast, the comments pill) uses `--scrim` and
`--on-scrim`, which you can leave dark in a light theme. The full list, with every default, is in
[spec/theme.md](../spec/theme.md#tokens).

The pen colours (red, yellow, green, white) aren't tokens: the agent reads them as part of your review, so they're
the same everywhere.

## Fonts and images

Put the files in the theme folder and point at them from `theme.css`; URLs are relative to `theme.css`:

```css
@font-face {
  font-family: 'Brand Sans';
  src: url('fonts/BrandSans.woff2') format('woff2');
  font-weight: 100 900;
}
:root {
  --font-ui: 'Brand Sans', system-ui, sans-serif;
}
```

The server serves only CSS, fonts (`.woff2 .woff .ttf .otf`) and images (`.svg .png .jpg .gif .webp .avif .ico`),
up to 10 MB each. It never serves a `.js` or `.html` file from a theme, and every theme file gets a Content-Security
Policy that blocks scripts. Use fonts whose license lets you ship them (OFL, for example).

## Parts

Every major element has a `data-part`. Use them for anything a token doesn't cover:

```css
[data-part~='bar'] { border-top: 2px solid var(--accent); }
[data-part~='comment'][data-state='question'] { background: #f4f0ff; }
[data-part~='brand-name'] { display: none; }     /* a wordmark logo says the name already */
```

`retake theme init` lists them all, and so does [spec/theme.md](../spec/theme.md#parts). Classes and ids aren't
part of the deal and can change in any release; parts and tokens only change in a major release, and the old names
keep working for one more.

Your CSS wins over the built-in CSS without `!important`: every built-in rule is in a CSS layer (`@layer retake`),
and yours isn't.

## Two examples

`examples/themes/` has two themes to start from:

- `light`: the same page on a light background (`mode = "light"`).
- `brand`: a made-up company, "Northwind Review", with its own font (Noto Serif, OFL), a teal accent, a logo and a
  favicon.

```bash
retake open ~/Videos --theme examples/themes/brand
```

## After an update

```bash
retake theme check
```

It reads your theme and says what this version doesn't use: a token or part that was renamed or removed, a typo, a
`theme.toml` problem, a script that will never be served. Exit code 1 when it found something, so an update script
can stop on it. The server prints the same warnings when it starts, so a look never quietly stops applying.

## Going further: a page of your own

A theme changes the look of Retake's page. If you need a different page altogether, say, Retake inside your admin
portal with your own components, build it against the HTTP API and serve it with `--ui-dir`:

```bash
retake open ~/Videos --ui-dir ./my-review-page    # or retake serve --ui-dir ...
```

The folder needs an `index.html` (the review page) and, for a folder of videos or a server, a `home.html` (the
home); everything else in it is served next to them. Use relative URLs (`api/info`, `video/v3`), and the same page
works at `/` and under a project's `/p/<name>/`. The routes are in [spec/api.md](../spec/api.md),
[spec/home.md](../spec/home.md) and [spec/server.md](../spec/server.md); the built-in page in `web/` is a working
example.

The API has a major version. Every answer carries `X-Retake-Api-Version: 1`; within a major version routes and fields
are only added, so check the header once and you're safe until a 2. A theme folder still applies to your page (it
gets the same `<link>` and metas), and you can use or ignore it.
