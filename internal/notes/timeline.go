package notes

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/samahlstrom/retake/internal/jsonx"
)

const (
	rewind = 1.0 // s: a backward seek longer than this starts a new pass
)

// boundary events start a new stretch: a span of take time where the playhead is either still or moving steadily.
var boundary = map[string]bool{"start": true, "play": true, "pause": true, "ended": true, "seek": true}

// event is one events.json entry with its two clocks parsed; f keeps every field.
type event struct {
	sessionT, videoT float64
	name             string
	f                map[string]json.RawMessage
}

type anchor struct {
	s, v    float64
	playing bool
	rate    float64
}

type view struct {
	s     float64
	label string
	flip  bool
}

// timeline maps session_t to video_t from the event log (paused = constant, playing = slope rate), and knows the
// passes (a rewind starts a new one) and which version was on screen.
type timeline struct {
	duration   float64 // 0 = unknown (no clamping), as Python's falsy None or 0
	anchors    []anchor
	s          []float64
	bounds     []float64
	passStarts []float64
	views      []view
	vs         []float64
	version    *string
}

func newTimeline(all []map[string]json.RawMessage, duration float64, version *string) *timeline {
	var evs []event
	for _, e := range all {
		s, okS := num(e["session_t"])
		v, okV := num(e["video_t"])
		if !okS || !okV {
			continue
		}
		evs = append(evs, event{sessionT: s, videoT: v, name: str(e["event"]), f: e})
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].sessionT < evs[j].sessionT })
	tl := &timeline{duration: duration, version: version}
	playing, rate := false, 1.0
	var bounds []float64
	for _, e := range evs {
		switch e.name {
		case "start", "rec_start":
			if p, ok := e.f["playing"]; ok {
				playing = truthy(p)
			}
			if truthy(e.f["rate"]) {
				rate = numOr(e.f["rate"], rate)
			}
		case "play":
			playing = true
		case "pause", "ended":
			playing = false
		case "rate":
			if truthy(e.f["rate"]) {
				rate = numOr(e.f["rate"], rate)
			}
		case "seek":
			if from, ok := num(e.f["from"]); ok && e.videoT < from-rewind {
				tl.passStarts = append(tl.passStarts, e.sessionT)
			}
		}
		if e.name == "start" || e.name == "rec_start" || e.name == "version" {
			if truthy(e.f["version"]) || truthy(e.f["shown"]) {
				label := e.f["shown"]
				if !truthy(label) {
					label = e.f["version"]
				}
				tl.views = append(tl.views, view{s: e.sessionT, label: str(label), flip: truthy(e.f["flip"])})
			}
		}
		tl.anchors = append(tl.anchors, anchor{s: e.sessionT, v: e.videoT, playing: playing, rate: rate})
		if boundary[e.name] {
			bounds = append(bounds, e.sessionT)
		}
	}
	for _, a := range tl.anchors {
		tl.s = append(tl.s, a.s)
	}
	tl.bounds = uniqueSorted(bounds)
	if len(tl.bounds) == 0 {
		first := 0.0
		if len(tl.s) > 0 {
			first = tl.s[0]
		}
		tl.bounds = []float64{first}
	}

	for _, v := range tl.views {
		tl.vs = append(tl.vs, v.s)
	}
	return tl
}

func uniqueSorted(xs []float64) []float64 {
	out := append([]float64(nil), xs...)
	sort.Float64s(out)
	n := 0
	for i, x := range out {
		if i == 0 || x != out[n-1] {
			out[n] = x
			n++
		}
	}
	return out[:n]
}

// bisectRight is Python's bisect.bisect_right.
func bisectRight(a []float64, x float64) int {
	return sort.Search(len(a), func(i int) bool { return a[i] > x })
}

// state returns the playhead, whether it's playing and the rate at session time s.
func (tl *timeline) state(s float64) (float64, bool, float64) {
	if len(tl.anchors) == 0 {
		return 0, false, 1
	}
	k := bisectRight(tl.s, s) - 1
	if k < 0 {
		return tl.anchors[0].v, false, 1
	}
	a := tl.anchors[k]
	v := a.v
	if a.playing {
		v = a.v + (s-a.s)*a.rate
	}
	if tl.duration != 0 {
		v = math.Min(v, tl.duration)
	}
	return math.Max(0, v), a.playing, a.rate
}

func (tl *timeline) videoT(s float64) float64 {
	v, _, _ := tl.state(s)
	return v
}

func (tl *timeline) passOf(s float64) int { return 1 + bisectRight(tl.passStarts, s) }

// viewAt returns the version on screen at s and whether it was the flipped partner.
func (tl *timeline) viewAt(s float64) (*string, bool) {
	k := bisectRight(tl.vs, s+1e-6) - 1
	if k >= 0 {
		l := tl.views[k].label
		return &l, tl.views[k].flip
	}
	return tl.version, false
}

func (tl *timeline) stretchOf(s float64) int { return max(0, bisectRight(tl.bounds, s)-1) }

// Path is where the playhead went while a sentence was spoken, when it crossed a play/pause/seek.
type Path struct {
	Points   []jsonx.Float `json:"points"`
	Scrub    bool          `json:"scrub"`     // it jumped (a seek), rather than just playing on
	EndFirst jsonx.Float   `json:"end_first"` // the playhead just before the first boundary
}

// path returns where the playhead went between s0 and s1 if it crossed a boundary, else nil.
func (tl *timeline) path(s0, s1 float64) *Path {
	var inner []float64
	for _, b := range tl.bounds {
		if s0 < b && b < s1 {
			inner = append(inner, b)
		}
	}
	if len(inner) == 0 {
		return nil
	}
	pts, jumped := []float64{tl.videoT(s0)}, false
	for _, b := range inner {
		before, after := tl.videoT(b-1e-3), tl.videoT(b)
		jumped = jumped || math.Abs(after-before) > 0.5
		pts = append(pts, before, after)
	}
	pts = append(pts, tl.videoT(s1))
	out := []float64{pts[0]}
	for _, p := range pts[1:] {
		if math.Abs(p-out[len(out)-1]) > 0.05 {
			out = append(out, p)
		}
	}
	if len(out) < 2 {
		return nil
	}
	p := &Path{Scrub: jumped, EndFirst: jsonx.Float(r3(tl.videoT(inner[0] - 1e-3)))}
	for _, x := range out {
		p.Points = append(p.Points, jsonx.Float(r3(x)))
	}
	return p
}

type stretch struct {
	i       int
	s0      float64
	playing bool
	rate    float64
	v0      float64
	pass    int
}

func (tl *timeline) stretch(i int) stretch {
	s0 := tl.bounds[i]
	v0, playing, rate := tl.state(s0)
	return stretch{i: i, s0: s0, playing: playing, rate: rate, v0: v0, pass: tl.passOf(s0)}
}
