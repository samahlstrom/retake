package notes

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// fmtT formats a video time truncated to tenths, as the legacy notes do: 41.27 -> 00:41.2.
func fmtT(t float64) string {
	tenths := int64(float64(math.Max(0, t)*10) + 1e-6)
	m, s10 := tenths/600, tenths%600
	h, m := m/60, m%60
	if h == 0 {
		return fmt.Sprintf("%02d:%02d.%d", m, s10/10, s10%10)
	}
	return fmt.Sprintf("%d:%02d:%02d.%d", h, m, s10/10, s10%10)
}

// fmtRaw is fmtT of a JSON value, "--:--.-" for none.
func fmtRaw(raw json.RawMessage) string {
	f, ok := num(raw)
	if !ok {
		return "--:--.-"
	}
	return fmtT(f)
}

// fmtS formats take time as m:ss.
func fmtS(t float64) string {
	t = math.Max(0, t)
	return fmt.Sprintf("%d:%02d", int64(pyFloorDiv(t, 60)), int64(pyMod(t, 60)))
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func pyOpt(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

func viewTag(c *Comment) string {
	if c.Flip {
		return fmt.Sprintf("[%s flip]", pyOpt(c.Version))
	}
	return fmt.Sprintf("[%s]", pyOpt(c.Version))
}

func when(c *Comment) string {
	if c.Mode == "paused" {
		return "paused " + fmtT(float64(c.VideoT))
	}
	if len(c.Speech) > 0 {
		return fmt.Sprintf("playing %s–%s", fmtT(float64(c.VideoT)), fmtT(float64(c.VideoTEnd)))
	}
	return "playing " + fmtT(float64(c.VideoT))
}

func said(c *Comment) string {
	if c.Text == "" {
		return "(no words)"
	}
	s := `"` + c.Text + `"`
	if c.Unclear {
		s += " (unclear)"
	}
	return s
}

func orders(os []int) string {
	parts := make([]string, len(os))
	for i, o := range os {
		parts[i] = fmt.Sprintf("#%d", o)
	}
	return strings.Join(parts, ", ")
}

func flagText(f *Flag) string {
	refs := orders(f.RefersTo)
	if refs == "" {
		refs = "nothing found"
	}
	if f.Kind == FlagRetract {
		return fmt.Sprintf("FLAG retraction (%s): would void %s", f.Scope, refs)
	}
	restores := ""
	if f.Restores != nil {
		restores = orders(*f.Restores)
	}
	if restores == "" {
		restores = "?"
	}
	return fmt.Sprintf("FLAG reinstatement: undoes %s, restoring %s", refs, restores)
}

// movedText says where the playhead went while he talked, or "".
func movedText(c *Comment) string {
	p := c.Moved
	if p == nil {
		return ""
	}
	var stops []string
	for _, x := range p.Points {
		if s := fmtT(float64(x)); len(stops) == 0 || s != stops[len(stops)-1] {
			stops = append(stops, s)
		}
	}
	if len(stops) < 2 {
		return ""
	}
	if p.Scrub {
		return "said while scrubbing " + strings.Join(stops, "→")
	}
	return "video moved while talking " + strings.Join(stops, "→")
}

func commentLine(c *Comment, full bool) string {
	head := said(c)
	if c.Audio {
		head += " (audio)"
	}
	parts := []string{fmt.Sprintf("#%d %s %s", c.Order, c.Seg, viewTag(c)), when(c), head}
	if len(c.AnswerTo) > 0 {
		parts = append(parts, "answer to question "+pyStr(c.AnswerTo))
	}
	if m := movedText(c); m != "" {
		parts = append(parts, m)
	}
	if full && c.VO != nil {
		parts = append(parts, fmt.Sprintf(`VO there %s–%s: "%s"`, fmtT(float64(c.VO.Start)), fmtT(float64(c.VO.End)),
			c.VO.Text))
	}
	at := func(raw json.RawMessage) string {
		if c.Mode == "playing" && len(c.Speech) > 0 {
			return " @" + fmtRaw(raw)
		}
		return ""
	}
	for _, s := range c.Strokes {
		if full {
			parts = append(parts, fmt.Sprintf("stroke %d%s: %s", s.N, at(s.VideoT), s.Desc))
		} else {
			parts = append(parts, fmt.Sprintf("stroke %d", s.N))
		}
	}
	for _, im := range c.Images {
		if full {
			x, y, w, h := float64(im.Box[0]), float64(im.Box[1]), float64(im.Box[2]), float64(im.Box[3])
			parts = append(parts, fmt.Sprintf("image %d%s: %s at %s–%s", im.N, at(im.VideoT), im.File, pt(x, y),
				pt(x+w, y+h)))
		} else {
			parts = append(parts, fmt.Sprintf("image %d", im.N))
		}
	}
	if c.Flag != nil {
		parts = append(parts, flagText(c.Flag))
	}
	if !full {
		parts = append(parts, c.Moment)
	}
	return "- " + strings.Join(parts, " · ")
}

func getOr(o *jsonx.Object, k, def string) string {
	v, ok := o.Get(k)
	if !ok {
		return def
	}
	return pyStr(v)
}

func renderMD(moments []*Moment, comments []*Comment, flags []FlagEntry, video *jsonx.Object, takes []*take,
	version string, fam []string) string {
	nSegs, nStrokes, nImages, answers := 0, 0, 0, false
	recs := make([]float64, 0, len(takes))
	modelSet := map[string]bool{}
	for _, t := range takes {
		nSegs += len(t.segments)
		recs = append(recs, t.recorded)
		for m := range t.models {
			modelSet[m] = true
		}
	}
	models := make([]string, 0, len(modelSet))
	for m := range modelSet {
		models = append(models, m)
	}
	sort.Strings(models)
	if len(models) == 0 {
		models = []string{"no audio"}
	}
	for _, c := range comments {
		nStrokes += len(c.Strokes)
		nImages += len(c.Images)
		answers = answers || len(c.AnswerTo) > 0
	}
	famText := ""
	if len(fam) > 0 {
		famText = " · family " + strings.Join(fam, " ")
	}
	counts := fmt.Sprintf("%s · %s · %s recorded · %s · %s · %s", plural(len(takes), "take"), plural(nSegs, "segment"),
		fmtS(pySum(recs)), strings.Join(models, ", "), plural(len(moments), "moment"), plural(nStrokes, "stroke"))
	if nImages > 0 {
		counts += " · " + plural(nImages, "image")
	}
	counts += " · " + plural(len(flags), "flag")
	who := reviewer(takes)
	lines := []string{
		fmt.Sprintf("# Review notes: %s (%s)", getOr(video, "name", "?"), version), "",
		fmt.Sprintf("video %s (%sx%s, %s fps, %s)%s  ", getOr(video, "path", "?"), getOr(video, "width", "None"),
			getOr(video, "height", "None"), getOr(video, "fps", "None"), fmtRaw(field(video, "duration")), famText),
		counts, "",
		"## Rules for applying these notes",
		"- Later statements override earlier ones: a higher #number was said later.",
		"- A retraction voids the comments it refers to: the same clip/moment, unless " + who + " names a wider scope.",
		"- A reinstatement restores what that retraction voided.",
		"- When the scope is unclear, ask " + who + " rather than guess. Flags are heuristics, and nothing was deleted.",
		fmt.Sprintf("- `[v7 flip]` = said while %s had flipped (C) to another version to compare: it is about that "+
			"version vs %s. `[v3]` on a %s note = recorded on v3 in a range whose frames are identical.", who, version,
			version),
		"- `(audio)` = the line is about sound (VO pauses, music, volume): check the audio, not just the picture.",
	}
	if nImages > 0 {
		lines = append(lines, "- `image N` = a picture "+who+" dropped onto the frame at that spot (framed in the "+
			"snapshot): use it there.")
	}
	if answers {
		lines = append(lines, "- `answer to question N` = "+who+"'s answer to your question N: apply it to what you "+
			"asked.")
	}
	lines = append(lines, "",
		"How to read: #n = spoken order. S2 = recording segment (R on to R off; only these were recorded). "+
			"[mm:ss.s] = video time. `paused` = said while stopped on that frame. `playing a–b` = said while the "+
			"video ran (allow ~1 s reaction lag). Strokes use normalized coords, (0,0) top-left to (1,1) "+
			"bottom-right, and match the numbered badges in each moment's snapshot. notes.json has everything plus "+
			"narration positions.", "")
	dropped := 0
	for _, t := range takes {
		for _, w := range t.warnings {
			lines = append(lines, "> warning: "+w)
		}
		if truthy(t.inheritNote) {
			lines = append(lines, fmt.Sprintf("> carried over: take %s %s; its lines are tagged [%s]", t.id,
				pyStr(t.inheritNote), pyOpt(t.version)))
		}
		dropped += t.dropped
	}
	if dropped > 0 {
		lines = append(lines, fmt.Sprintf("> %s (< 1%% of the frame diagonal) left out as noise",
			plural(dropped, "tiny stroke")))
	}
	if len(flags) > 0 {
		lines = append(lines, fmt.Sprintf("## Flags: retractions and reinstatements (%d)", len(flags)))
		for _, f := range flags {
			ff := &Flag{Kind: f.Kind, Scope: f.Scope, RefersTo: f.RefersTo, Restores: f.Restores}
			lines = append(lines, fmt.Sprintf(`- #%d %s · %s · "%s" · %s`, f.Order, f.Seg, f.Moment, f.Text, flagText(ff)))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "## By moment (video order)")
	for _, m := range moments {
		rng := fmtT(float64(m.Start))
		if float64(m.End)-float64(m.Start) >= 0.05 {
			rng = fmt.Sprintf("%s–%s", fmtT(float64(m.Start)), fmtT(float64(m.End)))
		}
		snap := "no snapshot"
		if m.Snapshot != nil && *m.Snapshot != "" {
			snap = fmt.Sprintf("%s (frame %s)", *m.Snapshot, fmtT(float64(*m.SnapshotT)))
		}
		lines = append(lines, fmt.Sprintf("### %s · %s · %s", m.ID, rng, snap))
		for _, c := range m.Comments {
			lines = append(lines, commentLine(c, true))
		}
		lines = append(lines, "")
	}
	lines = append(lines, "## In the order you said it")
	bySeg := map[string][]*Comment{}
	for _, c := range comments {
		bySeg[c.Seg] = append(bySeg[c.Seg], c)
	}
	known := map[string]bool{}
	for _, t := range takes {
		segs := append([]*segment(nil), t.segments...)
		sort.SliceStable(segs, func(i, j int) bool {
			return numOr(segs[i].get("t_start"), 0) < numOr(segs[j].get("t_start"), 0)
		})
		for _, sg := range segs {
			known[sg.label] = true
			cs := bySeg[sg.label]
			vr := ""
			if !isNull(sg.get("video_min")) {
				vmax, ok := sg.obj.Get("video_max")
				if !ok {
					vmax = sg.get("video_end")
				}
				vr = fmt.Sprintf("video %s–%s", fmtRaw(sg.get("video_min")), fmtRaw(vmax))
			} else {
				set := map[string]bool{}
				var vs []string
				for _, k := range []string{"video_start", "video_end"} {
					if v := sg.get(k); !isNull(v) && !set[fmtRaw(v)] {
						set[fmtRaw(v)] = true
						vs = append(vs, fmtRaw(v))
					}
				}
				sort.Strings(vs)
				if len(vs) > 0 {
					vr = "video " + strings.Join(vs, "–")
				}
			}
			head := []string{"### " + sg.label, fmt.Sprintf("take %s %s–%s", t.id, fmtS(numOr(sg.get("t_start"), 0)),
				fmtS(numOr(sg.get("t_end"), 0))), vr, plural(len(cs), "comment")}
			var keep []string
			for _, x := range head {
				if x != "" {
					keep = append(keep, x)
				}
			}
			lines = append(lines, strings.Join(keep, " · "))
			for _, c := range cs {
				lines = append(lines, commentLine(c, false))
			}
			lines = append(lines, "")
		}
	}
	var loose []*Comment
	for _, c := range comments {
		if !known[c.Seg] {
			loose = append(loose, c)
		}
	}
	if len(loose) > 0 {
		lines = append(lines, "### Outside any recording segment (older takes) · "+plural(len(loose), "comment"))
		for _, c := range loose {
			lines = append(lines, commentLine(c, false))
		}
		lines = append(lines, "")
	}
	if len(comments) == 0 {
		lines = append(lines, "(nothing recorded: no speech or strokes)")
	}
	return strings.TrimRightFunc(strings.Join(lines, "\n"), unicode.IsSpace) + "\n"
}
