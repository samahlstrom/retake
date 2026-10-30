package bridge

import (
	"math"
	"slices"
)

// Segment is one linear piece of a time map: output seconds A[0]..A[1] of version A show at B[0]..B[1] of version B.
type Segment struct {
	A  [2]float64 `json:"a"`
	B  [2]float64 `json:"b"`
	By string     `json:"by"` // video | spine | shot | gap
}

// TimeMap maps one version's times to another's (spec/bridge.md, "The time map").
type TimeMap struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Segments []Segment `json:"segments"`
}

// Map returns the time in B of time t in A. Without segments, or outside them, t stays (clamped to the map).
func (tm TimeMap) Map(t float64) float64 {
	return mapSegments(tm.Segments, t)
}

func mapSegments(segs []Segment, t float64) float64 {
	if len(segs) == 0 {
		return t
	}
	if t <= segs[0].A[0] {
		return segs[0].B[0]
	}
	for i, s := range segs {
		if t < s.A[1] || i == len(segs)-1 {
			if s.A[1] <= s.A[0] {
				return s.B[0]
			}
			f := math.Min((t-s.A[0])/(s.A[1]-s.A[0]), 1)
			return round6(s.B[0] + f*(s.B[1]-s.B[0]))
		}
	}
	return t
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// rule maps the A times of one stretch linearly, and says by what match.
type rule struct {
	by string
	f  func(t float64) float64
}

// spinePiece is a spine element's stretch in one unit: when it plays and which source seconds.
type spinePiece struct {
	id, asset string
	t0, t1    float64
	in, out   float64
}

func spinePieces(m *Manifest) []spinePiece {
	var out []spinePiece
	for _, u := range m.Units {
		for _, e := range u.Elements {
			if e.Kind != "spine" || len(e.Sources) == 0 {
				continue
			}
			s := e.Sources[0]
			out = append(out, spinePiece{id: e.ID, asset: s.Asset, t0: e.T[0], t1: e.T[1], in: s.In, out: s.Out})
		}
	}
	return out
}

// shotOrdinal returns, for each unit, how many units of the same shot come before it.
func shotOrdinal(m *Manifest) []int {
	seen := map[string]int{}
	out := make([]int, len(m.Units))
	for i, u := range m.Units {
		out[i] = seen[u.Shot]
		seen[u.Shot]++
	}
	return out
}

type mapper struct {
	a, b       *Manifest
	bSpine     []spinePiece
	aOrd       []int
	bShotUnits map[string][]int // shot -> unit indexes in B
}

func newMapper(a, b *Manifest) *mapper {
	mp := &mapper{a: a, b: b, bSpine: spinePieces(b), aOrd: shotOrdinal(a), bShotUnits: map[string][]int{}}
	for i, u := range b.Units {
		if u.Shot != "" {
			mp.bShotUnits[u.Shot] = append(mp.bShotUnits[u.Shot], i)
		}
	}
	return mp
}

// sameVideo finds B's unit with A unit ai's video hash: the one showing the same shot first, then the nearest.
func (mp *mapper) sameVideo(ai int) int {
	ua := mp.a.Units[ai]
	if ua.VideoHash == "" {
		return -1
	}
	best, bestD := -1, 0
	for i, ub := range mp.b.Units {
		if ub.VideoHash != ua.VideoHash {
			continue
		}
		d := abs(i - ai)
		if ub.Shot != ua.Shot {
			d += 1 << 20
		}
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// spineAt finds where B plays the spine source second A plays at t inside unit ai: the same spine id first.
func (mp *mapper) spineAt(ai int, t float64) (rule, bool) {
	for _, e := range mp.a.Units[ai].Elements {
		if e.Kind != "spine" || len(e.Sources) == 0 || t < e.T[0]-1e-9 || t >= e.T[1] {
			continue
		}
		src := e.Sources[0]
		s := src.In + (t - e.T[0])
		var hit *spinePiece
		for i := range mp.bSpine {
			p := &mp.bSpine[i]
			if p.asset != src.Asset || s < p.in-1e-9 || s >= p.out {
				continue
			}
			if hit == nil || (p.id == e.ID && hit.id != e.ID) {
				hit = p
			}
		}
		if hit != nil {
			ain, at0, bin, bt0 := src.In, e.T[0], hit.in, hit.t0
			return rule{by: "spine", f: func(x float64) float64 { return bt0 + (ain + (x - at0) - bin) }}, true
		}
	}
	return rule{}, false
}

// shotEnd is the latest time A unit ai's same-shot match bi maps to: when B's unit is the shorter one, just inside its
// end, so the mapped time still shows that shot (on its last frame); the last unit holds its end.
func (mp *mapper) shotEnd(ai, bi int) float64 {
	ua, ub := mp.a.Units[ai], mp.b.Units[bi]
	if bi == len(mp.b.Units)-1 || ub.T[1]-ub.T[0] >= ua.T[1]-ua.T[0]-1e-9 {
		return ub.T[1]
	}
	return math.Max(ub.T[0], ub.T[1]-0.001)
}

// shotMatch is the unit of B that shows A unit ai's shot: the n-th unit of that shot in each, else B's last one.
func (mp *mapper) shotMatch(ai int) (int, bool) {
	ua := mp.a.Units[ai]
	units := mp.bShotUnits[ua.Shot]
	if ua.Shot == "" || len(units) == 0 {
		return 0, false
	}
	return units[min(mp.aOrd[ai], len(units)-1)], true
}

// ruleAt picks how time t (inside A's unit ai) maps.
func (mp *mapper) ruleAt(ai int, t float64) (rule, bool) {
	ua := mp.a.Units[ai]
	if bi := mp.sameVideo(ai); bi >= 0 {
		ub := mp.b.Units[bi]
		return rule{by: "video", f: func(x float64) float64 { return ub.T[0] + (x - ua.T[0]) }}, true
	}
	if r, ok := mp.spineAt(ai, t); ok {
		return r, true
	}
	if bi, ok := mp.shotMatch(ai); ok {
		ub, hi := mp.b.Units[bi], mp.shotEnd(ai, bi)
		return rule{by: "shot", f: func(x float64) float64 {
			return math.Min(math.Max(ub.T[0]+(x-ua.T[0]), ub.T[0]), hi)
		}}, true
	}
	return rule{}, false
}

// breaks returns the A times inside unit ai where the mapping rule can change: the unit's edges, its spine pieces'
// edges, where B's spine pieces of the same assets start and end, and where a shot match hits B's unit end.
func (mp *mapper) breaks(ai int) []float64 {
	ua := mp.a.Units[ai]
	pts := []float64{ua.T[0], ua.T[1]}
	for _, e := range ua.Elements {
		if e.Kind != "spine" || len(e.Sources) == 0 {
			continue
		}
		pts = append(pts, e.T[0], e.T[1])
		src := e.Sources[0]
		for _, p := range mp.bSpine {
			if p.asset == src.Asset {
				pts = append(pts, e.T[0]+(p.in-src.In), e.T[0]+(p.out-src.In))
			}
		}
	}
	if bi, ok := mp.shotMatch(ai); ok {
		pts = append(pts, ua.T[0]+(mp.shotEnd(ai, bi)-mp.b.Units[bi].T[0]))
	}
	var out []float64
	for _, p := range pts {
		if p >= ua.T[0] && p <= ua.T[1] {
			out = append(out, round6(p))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// BuildTimeMap builds the map from version A's times to version B's from their manifests: by same video hash, then
// same spine source time, then same shot, with the rest interpolated (spec/bridge.md).
func BuildTimeMap(from, to string, a, b *Manifest) TimeMap {
	tm := TimeMap{From: from, To: to, Segments: []Segment{}}
	if a == nil || b == nil || len(a.Units) == 0 || len(b.Units) == 0 {
		return tm
	}
	mp := newMapper(a, b)
	var segs []Segment
	for ai := range a.Units {
		pts := mp.breaks(ai)
		for k := 0; k+1 < len(pts); k++ {
			x, y := pts[k], pts[k+1]
			if y-x < 1e-6 {
				continue
			}
			r, ok := mp.ruleAt(ai, (x+y)/2)
			if !ok {
				segs = append(segs, Segment{A: [2]float64{x, y}, By: "gap"})
				continue
			}
			segs = append(segs, Segment{A: [2]float64{x, y}, B: [2]float64{round6(r.f(x)), round6(r.f(y))}, By: r.by})
		}
	}
	fillGaps(segs, b.End())
	tm.Segments = merge(segs)
	return tm
}

// fillGaps interpolates the gap segments between the matched moments around them.
func fillGaps(segs []Segment, bEnd float64) {
	for i := 0; i < len(segs); i++ {
		if segs[i].By != "gap" {
			continue
		}
		j := i
		for j < len(segs) && segs[j].By == "gap" {
			j++
		}
		a0, a1 := segs[i].A[0], segs[j-1].A[1]
		var b0, b1 float64
		switch {
		case i > 0 && j < len(segs):
			b0, b1 = segs[i-1].B[1], segs[j].B[0]
		case i > 0:
			b0 = segs[i-1].B[1]
			b1 = math.Min(b0+(a1-a0), bEnd)
		case j < len(segs):
			b1 = segs[j].B[0]
			b0 = math.Max(b1-(a1-a0), 0)
		default:
			b0, b1 = a0, math.Min(a1, bEnd)
		}
		for k := i; k < j; k++ {
			f0 := (segs[k].A[0] - a0) / math.Max(a1-a0, 1e-9)
			f1 := (segs[k].A[1] - a0) / math.Max(a1-a0, 1e-9)
			segs[k].B = [2]float64{round6(b0 + f0*(b1-b0)), round6(b0 + f1*(b1-b0))}
		}
		i = j - 1
	}
}

// merge joins neighbors that continue each other on the same line.
func merge(segs []Segment) []Segment {
	out := []Segment{}
	for _, s := range segs {
		if n := len(out); n > 0 {
			p := &out[n-1]
			la, lb := p.A[1]-p.A[0], p.B[1]-p.B[0]
			sa, sb := s.A[1]-s.A[0], s.B[1]-s.B[0]
			if p.By == s.By && math.Abs(p.A[1]-s.A[0]) < 1e-6 && math.Abs(p.B[1]-s.B[0]) < 1e-6 &&
				math.Abs(lb*sa-sb*la) < 1e-6 {
				p.A[1], p.B[1] = s.A[1], s.B[1]
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// Carry says what became of a note's moment in the next version: "unchanged" (the unit it's in is in B, the same
// pixels, and for (audio) notes the same sound), "check" (it changed, and the note was answered), or "changed".
func Carry(a, b *Manifest, t0 float64, audio, answered bool) string {
	if i := a.UnitAt(t0); i >= 0 {
		ua := a.Units[i]
		for _, ub := range b.Units {
			if ua.VideoHash != "" && ub.VideoHash == ua.VideoHash && (!audio || ub.AudioHash == ua.AudioHash) {
				return "unchanged"
			}
		}
	}
	if answered {
		return "check"
	}
	return "changed"
}
