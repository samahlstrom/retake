# Example themes

Two themes for Retake's page (spec/theme.md, docs/theming.md). Use one as it is, or copy it and change it:

```bash
retake open ~/Videos --theme examples/themes/light
cp -r examples/themes/brand ~/.config/retake/theme      # your default look from now on
retake theme check ~/.config/retake/theme
```

| Theme | What it shows |
|---|---|
| `light/` | A light page from tokens only (`--overlay: #000` flips every hairline and hover), `mode = "light"`, and two parts: a dark stage for the video and an outline on the white pen. |
| `brand/` | A made-up company, "Northwind Review": its own font loaded with `@font-face`, a teal accent, a logo and a favicon from `theme.toml`, and the bar and the name styled through their parts. |

The CSS, the TOML files and the SVG logo are CC0: use them for anything. `brand/fonts/` is Noto Serif, under the SIL
Open Font License 1.1 (`brand/fonts/OFL.txt`).
