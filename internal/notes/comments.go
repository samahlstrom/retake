package notes

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
)

type itemKind int

const (
	kindSpeech itemKind = iota
	kindStroke
	kindImage
)

// item is one thing said, drawn or dropped in a take, before it becomes part of a comment.
type item struct {
	kind   itemKind
	s, e   float64
	seg    string
	videoT float64 // strokes and images: the frame it was drawn on
	// speech
	text   string
	conf   float64
	va, vb float64
	path   *Path
	stroke *Stroke
	image  *Image
}

type groupKey struct {
	stretch int
	seg     string
}

// videoDims returns the frame size used for stroke geometry (16x9 when unknown) and the clamp duration (0 = none).
func videoDims(video *jsonx.Object) (w, h, duration float64) {
	w, h = 16, 9
	if v, ok := video.Get("width"); ok && truthy(v) {
		w = numOr(v, 16)
	}
	if v, ok := video.Get("height"); ok && truthy(v) {
		h = numOr(v, 9)
	}
	if v, ok := video.Get("duration"); ok && truthy(v) {
		duration = numOr(v, 0)
	}
	return w, h, duration
}

// takeComments turns one take's speech, strokes and images into comments pinned to video time, each tagged with
// its segment, the version on screen and the pass.
func takeComments(t *take, video *jsonx.Object) []*Comment {
	W, H, duration := videoDims(video)
	tl := newTimeline(t.events, duration, t.version)
	groups := map[groupKey][]*item{}
	var keys []groupKey
	add := func(k groupKey, it *item) {
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], it)
	}
	for _, u := range buildUnits(t.words, tl) {
		st0 := tl.stretch(u.stretch)
		va, vb := st0.v0, st0.v0
		if st0.playing {
			va = tl.videoT(max(u.s, st0.s0))
			if u.path != nil {
				vb = float64(u.path.EndFirst)
			} else {
				vb = tl.videoT(u.e)
			}
		}
		add(groupKey{u.stretch, u.seg}, &item{kind: kindSpeech, text: u.text, conf: u.conf, s: u.s, e: u.e, seg: u.seg,
			va: va, vb: vb, path: u.path})
	}
	undone := map[string]bool{}
	for _, e := range t.events {
		if str(e["event"]) == "undo" {
			undone[pyKey(e["id"])] = true
		}
	}
	diag := minStroke * hypot(W, H)
	for _, st := range t.strokes {
		s, ok := num(st["session_t"])
		if !ok || undone[pyKey(st["id"])] {
			continue
		}
		if id, ok := num(st["id"]); ok && t.erased[int64(id)] {
			continue // erased later, on this version (erased.json)
		}
		pts := parsePoints(st["points"])
		if len(pts) > 0 {
			minX, minY, maxX, maxY := pts[0][0], pts[0][1], pts[0][0], pts[0][1]
			for _, p := range pts[1:] {
				minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
				maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
			}
			if hypot((maxX-minX)*W, (maxY-minY)*H) < diag {
				t.dropped++
				continue
			}
		}
		desc, g := Describe(pts, W, H)
		color := str(st["color"])
		if _, ok := colors[color]; !ok || jsonx.Kind(st["color"]) != "string" {
			color = "red"
		}
		seg := t.segOf(st["seg"], s)
		e := s
		if truthy(st["session_t_end"]) {
			e = numOr(st["session_t_end"], s)
		}
		points := st["points"]
		if !truthy(points) {
			points = json.RawMessage("[]")
		}
		vt, ok := num(st["video_t"])
		if !ok {
			vt = tl.videoT(s) // legacy would stop here; a stroke without a frame is placed by the clock
		}
		stroke := &Stroke{ID: st["id"], Color: color, Seg: seg, VideoT: st["video_t"], Version: st["version"],
			Flip: truthy(st["flip"]), Desc: color + " " + desc, Shape: g.Shape, Points: points, pts: pts, videoT: vt}
		for i := range 4 {
			stroke.BBox[i] = jsonFloat(g.BBox[i])
		}
		stroke.From = [2]jsonFloat{jsonFloat(g.From[0]), jsonFloat(g.From[1])}
		stroke.To = [2]jsonFloat{jsonFloat(g.To[0]), jsonFloat(g.To[1])}
		add(groupKey{tl.stretchOf(s), seg}, &item{kind: kindStroke, s: s, e: e, seg: seg, videoT: vt, stroke: stroke})
	}
	for _, im := range t.images {
		s, ok := num(im["session_t"])
		if !ok {
			continue
		}
		seg := t.segOf(im["seg"], s)
		vt, ok := num(im["video_t"])
		if !ok {
			vt = tl.videoT(s)
		}
		file := str(im["file"])
		img := &Image{ID: im["id"], Seg: seg, File: file, Mime: str(im["mime"]), Version: im["version"],
			Flip: truthy(im["flip"]), VideoT: im["video_t"], videoT: vt}
		if file != "" {
			img.Path = filepathJoin(t.dir, file)
		}
		for i, b := range parseFloats(im["box"], 4) {
			img.Box[i] = jsonFloat(b)
		}
		add(groupKey{tl.stretchOf(s), seg}, &item{kind: kindImage, s: s, e: s, seg: seg, videoT: vt, image: img})
	}

	comment := func(mode string, anchor, va, vb float64, items []*item, stt stretch) *Comment {
		s0, e1 := items[0].s, items[0].e
		for _, x := range items {
			s0, e1 = math.Min(s0, x.s), math.Max(e1, x.e)
		}
		shown, flip := tl.viewAt(s0)
		if shown == nil || *shown == "" {
			shown = t.version
		}
		c := &Comment{Take: t.id, Seg: items[0].seg, Version: shown, Flip: flip, Pass: stt.pass, Mode: mode,
			Anchor: jsonFloat(r3(anchor)), VideoT: jsonFloat(r3(va)), VideoTEnd: jsonFloat(r3(vb)),
			SessionT: jsonFloat(r3(s0)), SessionTEnd: jsonFloat(r3(e1)), Speech: []Speech{}, Strokes: []*Stroke{},
			takeFolder: t.folder, Author: t.author}
		var texts []string
		for _, x := range items {
			switch x.kind {
			case kindSpeech:
				texts = append(texts, x.text)
				c.Unclear = c.Unclear || x.conf < unclear
				if c.Moved == nil && x.path != nil {
					c.Moved = x.path
				}
				c.Audio = c.Audio || audioPat.MatchString(x.text)
				c.Speech = append(c.Speech, Speech{Text: x.text, Conf: jsonFloat(x.conf), SessionT: jsonFloat(r3(x.s)),
					SessionTEnd: jsonFloat(r3(x.e)), VideoT: jsonFloat(r3(x.va)), VideoTEnd: jsonFloat(r3(x.vb)),
					Path: x.path})
			case kindStroke:
				x.stroke.SessionT, x.stroke.SessionTEnd = jsonFloat(r3(x.s)), jsonFloat(r3(x.e))
				c.Strokes = append(c.Strokes, x.stroke)
			case kindImage:
				x.image.SessionT = jsonFloat(r3(x.s))
				c.Images = append(c.Images, x.image)
			}
		}
		c.Text = strings.Join(texts, " ")
		return c
	}

	sort.SliceStable(keys, func(i, j int) bool { return minS(groups[keys[i]]) < minS(groups[keys[j]]) })
	var out []*Comment
	for _, k := range keys {
		its := append([]*item(nil), groups[k]...)
		sort.SliceStable(its, func(i, j int) bool { return its[i].s < its[j].s })
		stt := tl.stretch(k.stretch)
		if !stt.playing {
			out = append(out, comment("paused", stt.v0, stt.v0, stt.v0, its, stt))
			continue
		}
		var gs [][]*item
		for _, x := range its {
			if x.kind == kindSpeech {
				gs = append(gs, []*item{x})
			}
		}
		for _, x := range its {
			if x.kind == kindSpeech {
				continue
			}
			best, bestGap := -1, attachTo
			for gi, g := range gs {
				sp := g[0]
				d := max(0, sp.s-x.e, x.s-sp.e)
				if sp.kind == kindSpeech && d <= bestGap {
					best, bestGap = gi, d
				}
			}
			if best >= 0 {
				gs[best] = append(gs[best], x)
			} else {
				gs = append(gs, []*item{x})
			}
		}
		for _, g := range gs {
			head := g[0]
			if head.kind == kindSpeech {
				out = append(out, comment("playing", head.va, head.va, head.vb, g, stt))
			} else {
				out = append(out, comment("playing", head.videoT, head.videoT, head.videoT, g, stt))
			}
		}
	}
	for _, c := range out {
		markX(c.Strokes, W, H)
		c.origSeg = t.segID(c.Seg)
	}
	return out
}

func minS(items []*item) float64 {
	m := math.Inf(1)
	for _, x := range items {
		m = math.Min(m, x.s)
	}
	return m
}

// cluster chains comments into moments while their anchors are <= window s apart and the moment spans <= maxSpan s,
// in video order.
func cluster(comments []*Comment, window, maxSpan float64) []*Moment {
	sorted := append([]*Comment(nil), comments...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Anchor != sorted[j].Anchor {
			return sorted[i].Anchor < sorted[j].Anchor
		}
		return sorted[i].Order < sorted[j].Order
	})
	type acc struct {
		first, last float64
		cs          []*Comment
	}
	var ms []*acc
	for _, c := range sorted {
		a := float64(c.Anchor)
		if n := len(ms); n > 0 && a-ms[n-1].last <= window && a-ms[n-1].first <= maxSpan {
			ms[n-1].cs = append(ms[n-1].cs, c)
			ms[n-1].last = a
		} else {
			ms = append(ms, &acc{first: a, last: a, cs: []*Comment{c}})
		}
	}
	out := make([]*Moment, 0, len(ms))
	for i, m := range ms {
		cs := m.cs
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].Order < cs[j].Order })
		id := fmt.Sprintf("M%d", i+1)
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, c := range cs {
			c.Moment = id
			lo, hi = math.Min(lo, float64(c.VideoT)), math.Max(hi, float64(c.VideoTEnd))
		}
		out = append(out, &Moment{ID: id, Start: jsonFloat(r3(lo)), End: jsonFloat(r3(hi)), Comments: cs})
	}
	return out
}

// numberMarks numbers strokes and images in reading order (moment by moment), as the snapshots' badges show them.
func numberMarks(moments []*Moment) {
	n, k := 0, 0
	for _, m := range moments {
		for _, c := range m.Comments {
			for _, s := range c.Strokes {
				n++
				s.N = n
			}
			for _, im := range c.Images {
				k++
				im.N = k
			}
		}
	}
}

// build runs the engine over takes (sorted by id): comments in spoken order, moments, numbered marks and flags.
func build(takes []*take, video *jsonx.Object, window, maxSpan float64) ([]*Moment, []*Comment, []FlagEntry) {
	relabelSegments(takes)
	type keyed struct {
		ti int
		c  *Comment
	}
	var all []keyed
	for ti, t := range takes {
		for _, c := range takeComments(t, video) {
			all = append(all, keyed{ti, c})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].ti != all[j].ti {
			return all[i].ti < all[j].ti
		}
		return all[i].c.SessionT < all[j].c.SessionT
	})
	comments := make([]*Comment, len(all))
	perSeg := map[[2]string]int{}
	for i, k := range all {
		c := k.c
		c.Order = i + 1
		seg := [2]string{c.takeFolder, c.origSeg}
		perSeg[seg]++
		c.Key = fmt.Sprintf("%s/%s/%d", c.takeFolder, c.origSeg, perSeg[seg])
		comments[i] = c
	}
	answers := map[string]json.RawMessage{}
	for _, t := range takes {
		for _, sg := range t.segments {
			if a, ok := sg.obj.Get("answer_to"); ok && !isNull(a) {
				answers[sg.label] = a
			}
		}
	}
	for _, c := range comments {
		c.AnswerTo = answers[c.Seg]
	}
	return rebuild(comments, window, maxSpan)
}

// rebuild renumbers comments 1..n in their spoken order, then clusters, numbers marks and flags them again.
func rebuild(comments []*Comment, window, maxSpan float64) ([]*Moment, []*Comment, []FlagEntry) {
	comments = append([]*Comment(nil), comments...)
	sort.SliceStable(comments, func(i, j int) bool { return comments[i].Order < comments[j].Order })
	for i, c := range comments {
		c.Order = i + 1
	}
	moments := cluster(comments, window, maxSpan)
	numberMarks(moments)
	return moments, comments, attachFlags(comments)
}
