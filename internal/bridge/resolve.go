package bridge

import (
	"cmp"
	"math"
	"slices"
)

// Target is a timeline element under a note, as `retake-edit resolve-note` ranks it.
type Target struct {
	ID     string      `json:"id"`
	Kind   string      `json:"kind"`
	Ptr    string      `json:"ptr"`
	Score  float64     `json:"score"` // the fraction of the stroke box on the element (0 without a box)
	Z      int64       `json:"z"`
	Box    *[4]float64 `json:"box,omitempty"` // where it was at the note's time, in manifest pixels
	Parent string      `json:"parent,omitempty"`
}

// Resolution is what a moment and a stroke box resolve to.
type Resolution struct {
	T       float64  `json:"t"`
	Unit    string   `json:"unit,omitempty"`
	Key     string   `json:"key,omitempty"`
	Word    *Word    `json:"word,omitempty"`
	Targets []Target `json:"targets"`
}

func area(b [4]float64) float64 { return math.Max(b[2], 0) * math.Max(b[3], 0) }

func overlap(a, b [4]float64) float64 {
	x0, y0 := math.Max(a[0], b[0]), math.Max(a[1], b[1])
	x1, y1 := math.Min(a[0]+a[2], b[0]+b[2]), math.Min(a[1]+a[3], b[1]+b[3])
	return math.Max(x1-x0, 0) * math.Max(y1-y0, 0)
}

func boxArea(t Target) float64 {
	if t.Box == nil {
		return math.Inf(1)
	}
	return area(*t.Box)
}

// rank orders targets the resolve-note way: by score, then z (topmost first), then the smaller element, then id.
func rank(ts []Target) {
	slices.SortStableFunc(ts, func(a, b Target) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), cmp.Compare(b.Z, a.Z), cmp.Compare(boxArea(a), boxArea(b)),
			cmp.Compare(a.ID, b.ID))
	})
}

// Resolve ranks the elements live at output time t under a stroke box ([x, y, w, h] in manifest pixels; nil: no
// stroke), exactly as `retake-edit resolve-note` does (spec/manifest.md): by the fraction of the stroke box on each
// element's box at t, then by z, then the smaller element first. Sound-only elements come last. It also gives the unit
// and the spine word at t (or the nearest within 0.5 s).
func Resolve(m *Manifest, t float64, stroke *[4]float64) Resolution {
	i := m.UnitAt(t)
	if i < 0 {
		return Resolution{T: t, Targets: []Target{}}
	}
	u := m.Units[i]
	targets := []Target{}
	for _, e := range u.Elements {
		if t < e.T[0]-1e-9 || t > e.T[1]+1e-9 {
			continue
		}
		b, on := e.BoxAt(t)
		if !on && e.Kind != "spine" && e.Kind != "audio" {
			continue // not on screen right now
		}
		score := 0.0
		if stroke != nil && on {
			s := *stroke
			switch {
			case area(s) > 0:
				score = overlap(s, b) / area(s)
			case s[0] >= b[0] && s[0] <= b[0]+b[2] && s[1] >= b[1] && s[1] <= b[1]+b[3]:
				score = 1 // a point stroke: inside or not
			}
		}
		tg := Target{ID: e.ID, Kind: e.Kind, Ptr: e.Ptr, Score: math.Round(score*1e4) / 1e4, Z: e.Z, Parent: e.Parent}
		if on {
			bb := b
			tg.Box = &bb
		}
		targets = append(targets, tg)
	}
	rank(targets)
	return Resolution{T: t, Unit: u.Unit, Key: u.Key, Word: wordAt(u.Words, t), Targets: targets}
}

func wordAt(words []Word, t float64) *Word {
	for _, w := range words {
		if t >= w.T0 && t < w.T1 {
			return &w
		}
	}
	var best *Word
	bestD := 0.0
	for _, w := range words {
		d := w.T0 - t
		if t >= w.T0 {
			d = t - w.T1
		}
		if d <= 0.5 && (best == nil || d < bestD) {
			best, bestD = &w, d
		}
	}
	return best
}

// Stroke is one stroke of a note: when it was drawn (video seconds) and its box, normalized 0-1 to the frame.
type Stroke struct {
	T   float64
	Box [4]float64
}

// NoteTargets resolves a note: each stroke at its own time with its box, merged by element (its best score, in the
// resolve-note order); a note without strokes resolves at t0 without a box. The unit and word are those at t0.
func NoteTargets(m *Manifest, t0 float64, strokes []Stroke) Resolution {
	at := Resolve(m, t0, nil)
	if len(strokes) == 0 {
		return at
	}
	best := map[string]Target{}
	var order []string
	for _, s := range strokes {
		px := [4]float64{s.Box[0] * m.Size[0], s.Box[1] * m.Size[1], s.Box[2] * m.Size[0], s.Box[3] * m.Size[1]}
		for _, tg := range Resolve(m, s.T, &px).Targets {
			old, seen := best[tg.ID]
			if !seen {
				order = append(order, tg.ID)
			}
			if !seen || tg.Score > old.Score {
				best[tg.ID] = tg
			}
		}
	}
	out := make([]Target, 0, len(order))
	for _, id := range order {
		out = append(out, best[id])
	}
	rank(out)
	at.Targets = out
	return at
}
