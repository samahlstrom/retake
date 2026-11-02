This video has a plan in `v0/<video>_v0.md` (Ignition): one Markdown document whose `## Script` has a heading per
beat.

- Follow its script unless the notes say otherwise.
- With each new version, also write `v0/cuts/<label>.json` (`v0/cuts/v3.json` for v3): which script sections the
  version covers, and where.

  ```json
  { "version": "v3",
    "sections": [ { "heading": "Cold open", "from": 0, "to": 12.4 },
                  { "heading": "The trick", "from": 12.4, "to": 61.0 } ] }
  ```

  `heading` is a heading of the script, without its `#`s. Times are seconds in that version. A section that isn't in
  the cut is left out.
