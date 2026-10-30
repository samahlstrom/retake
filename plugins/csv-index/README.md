# csv-index

A catalog plug-in (`spec/assets.md`, "Plug-ins") that imports any CSV with path, title, tags and license columns,
such as an asset index a creator keeps by hand: one asset per row, with the row's license text mapped to a catalog
status. Go, standard library only.

## Build and use

```sh
cd plugins/csv-index
mise exec -- go build -o csv-index .
retake assets plugin install .                       # or put this folder on RETAKE_PLUGIN_PATH
retake assets plugin csv-index columns --args '{"path": "assets/INDEX.csv"}'   # check the mapping first
retake assets import assets/INDEX.csv --via csv-index --arg limit=20
retake assets import assets/INDEX.csv --via csv-index --arg import=metadata --arg root=/media/library
```

## Columns

Header names are matched case-insensitively; the first match wins.

| Field | Columns |
|---|---|
| path (required) | `path`, `file`, `filename`, `file_name` |
| title | `title`, `name`; else from the file name: the part before the first `__`, hyphens to spaces |
| tags | `tags`, `tags_when_to_use`, `keywords`, split on `/ ; , \|`, lowercased; plus the `type` value |
| license | `license`, `licence`, `license_status` |
| credit | `credit`, `attribution` |
| source | `source_url`, `source`, `url` |
| type | `type`: a tag, and a kind hint (`sfx`, `music`, `audio` → audio; `*video*` → video; `*image*`, `*icon*`, `*card*`, `*map*`, `*art*` → image; `font` → font) |

The whole row is kept in the asset's `meta.row`. The asset key is the path as written, so a re-import updates the
same entries. Paths go out absolute: a relative one is relative to `root` (default: the CSV's folder).

## License mapping

The license text is kept as `license.note`. In order:

1. An exact status (`owned`, `cleared`, `attribution`, `review-only`, `blocked`) passes through.
2. "not CC", "no license", "not licensed", "unknown", "copyrighted" → `license_default` (`review-only`).
3. "CC0", "public domain" → `cleared`.
4. "CC-BY", "CC BY", "attribution", "credit required" → `attribution`; the credit is the credit column, else the
   title and the license text.
5. "OFL", "open font license", "font licence" → `cleared`.
6. "owned", "own work", " own ", "original" → `owned`.
7. "blocked", "do not use" → `blocked`.
8. "Epidemic", "Artlist", "licensed", "royalty-free" → `cleared`.
9. Anything else, or empty → `license_default`.

A row that isn't publishable (`review-only`, `blocked`) and mentions copyright, a meme or an unknown source gets
`content_id_risk: high`.

## Args

`root`, `import` (`full`, the default: hash, store and preview each file; `metadata`: paths only), `limit`,
`license_default`, and `path_column`, `title_column`, `tags_column`, `license_column` to name columns that aren't
detected.
