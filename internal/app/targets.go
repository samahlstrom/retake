package app

import (
	"encoding/json"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/notes"
	"github.com/samahlstrom/retake/internal/project"
)

// manifests returns a lookup of the family's render manifests by version label, read once each (nil: none). A
// comment resolves against the manifest of the version its take was recorded on, which is v for its own takes.
func manifests(v family.Version, fam []family.Version) func(label string) *bridge.Manifest {
	paths := map[string]string{v.Label: v.Path}
	for _, x := range fam {
		paths[x.Label] = x.Path
	}
	cache := map[string]*bridge.Manifest{}
	return func(label string) *bridge.Manifest {
		if m, ok := cache[label]; ok {
			return m
		}
		var m *bridge.Manifest
		if p, ok := paths[label]; ok {
			m, _ = bridge.ForVideo(p) // no manifest (or a broken one): the note keeps time and strokes only
		}
		cache[label] = m
		return m
	}
}

// attachTargets resolves every comment of a notes run to timeline elements (spec/bridge.md, "Notes resolve to
// elements"): each stroke at its own time with its box, else the comment's start without a box. res.Comments and
// run.Comments are in the same order (runFromResult makes one from the other).
func attachTargets(run *project.Run, res *notes.Result, manifest func(label string) *bridge.Manifest) {
	for i := range run.Comments {
		if i >= len(res.Comments) {
			return
		}
		c, src := &run.Comments[i], res.Comments[i]
		c.Detail.Audio = src.Audio
		label := c.Version
		if label == "" {
			label = run.Version
		}
		m := manifest(label)
		if m == nil {
			continue
		}
		var strokes []bridge.Stroke
		for _, st := range src.Strokes {
			t := c.T0
			var vt float64
			if json.Unmarshal(st.VideoT, &vt) == nil {
				t = vt
			}
			x0, y0, x1, y1 := float64(st.BBox[0]), float64(st.BBox[1]), float64(st.BBox[2]), float64(st.BBox[3])
			strokes = append(strokes, bridge.Stroke{T: t, Box: [4]float64{x0, y0, x1 - x0, y1 - y0}})
		}
		r := bridge.NoteTargets(m, c.T0, strokes)
		c.Detail.Unit = r.Unit
		for _, tg := range r.Targets {
			c.Detail.Targets = append(c.Detail.Targets, project.Target(tg))
		}
		if r.Word != nil && src.Audio {
			c.Detail.Word = &project.Word{T0: r.Word.T0, T1: r.Word.T1, Text: r.Word.Text}
		}
	}
}
