package notes

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// A port of the legacy tests/test_mapping.py: deterministic checks of the engine (no audio): R-segments, clock
// mapping, moments, spoken order, version/flip tags, shapes and retraction/reinstatement flags.

// words spreads text over [s, e] on the take clock, like the legacy test helper.
func words(text string, s, e float64, seg string) []word {
	ws := strings.Fields(text)
	step := (e - s) / float64(len(ws))
	out := make([]word, len(ws))
	for i, w := range ws {
		out[i] = word{w: " " + w, s: r3(s + float64(i)*step), e: r3(s + float64(i+1)*step - 0.02), p: 0.95,
			seg: pyKey(json.RawMessage(`"` + seg + `"`))}
	}
	return out
}

type obj = map[string]any

func raws(t *testing.T, list []obj) []map[string]json.RawMessage {
	t.Helper()
	out := make([]map[string]json.RawMessage, len(list))
	for i, x := range list {
		b, err := json.Marshal(x)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func jsonObject(t *testing.T, x obj) *jsonx.Object {
	t.Helper()
	b, err := json.Marshal(x)
	if err != nil {
		t.Fatal(err)
	}
	return objectOf(b)
}

func mkTake(t *testing.T, id string, events, strokes, segs []obj, ws []word) *take {
	t.Helper()
	v := "v6"
	tk := &take{id: id, folder: id, version: &v, meta: jsonx.NewObject(), models: map[string]bool{"test": true},
		events: raws(t, events), strokes: raws(t, strokes), words: ws}
	for _, s := range segs {
		o := jsonObject(t, s)
		tk.segments = append(tk.segments, &segment{obj: o, key: pyKey(field(o, "id")), id: str(field(o, "id"))})
	}
	return tk
}

var testVideo = obj{"duration": 60.0, "width": 1920, "height": 1080, "fps": 30, "name": "clip_v6.mp4",
	"path": "/nonexistent.mp4"}

func ev(s, v float64, name string, kw obj) obj {
	e := obj{"session_t": s, "video_t": v, "event": name}
	for k, x := range kw {
		e[k] = x
	}
	return e
}

// mainTake is test_mapping.py's take: a playing comment, a paused one with a stroke, a loop while not recording,
// a retraction, a comparison against v5 (flipped) and a reinstatement with a stroke on the same paused frame.
func mainTake(t *testing.T) *take {
	E := []obj{
		ev(0, 8, "start", obj{"playing": false, "rate": 1, "version": "v6", "base": "v6", "flip": false}),
		ev(0.5, 8, "rec_start", obj{"seg": "S1", "playing": false, "version": "v6", "flip": false}),
		ev(1, 8, "play", nil),
		ev(3, 10, "pause", nil),
		ev(6, 10, "rec_stop", obj{"seg": "S1"}),
		ev(7, 6, "seek", obj{"from": 10.0}),
		ev(7.2, 6, "play", nil),
		ev(10, 8.8, "pause", nil),
		ev(11, 8.8, "rec_start", obj{"seg": "S2", "playing": false, "version": "v6", "flip": false}),
		ev(14.5, 8.8, "rec_stop", obj{"seg": "S2"}),
		ev(15, 8.8, "play", nil),
		ev(17, 10.8, "pause", nil),
		ev(17.5, 10.8, "version", obj{"shown": "v5", "base": "v6", "flip": true}),
		ev(18, 10.8, "rec_start", obj{"seg": "S3", "playing": false, "version": "v5", "flip": true}),
		ev(20.5, 10.8, "rec_stop", obj{"seg": "S3"}),
		ev(21, 10.8, "version", obj{"shown": "v6", "base": "v6", "flip": false}),
		ev(22, 10.8, "rec_start", obj{"seg": "S4", "playing": false, "version": "v6", "flip": false}),
		ev(25, 10.8, "rec_stop", obj{"seg": "S4"}),
	}
	var W []word
	W = append(W, words("Look at the stats.", 1.5, 2.8, "S1")...)
	W = append(W, words("This zoom is too slow.", 3.6, 5.0, "S1")...)
	W = append(W, words("Throw out everything I said about this clip.", 11.5, 14.0, "S2")...)
	W = append(W, words("This was better in v5.", 18.5, 20.0, "S3")...)
	W = append(W, words("Actually, add that back.", 22.5, 24.0, "S4")...)
	S := []obj{
		{"id": 1, "seg": "S1", "color": "red", "session_t": 3.3, "session_t_end": 3.5, "video_t": 10.0,
			"video_t_end": 10.0, "paused": true, "version": "v6", "flip": false,
			"points": [][2]float64{{0.4, 0.4}, {0.5, 0.5}, {0.6, 0.6}}},
		{"id": 2, "seg": "S4", "color": "green", "session_t": 24.2, "session_t_end": 24.4, "video_t": 10.8,
			"video_t_end": 10.8, "paused": true, "version": "v6", "flip": false,
			"points": [][2]float64{{0.1, 0.2}, {0.3, 0.2}}},
	}
	var segs []obj
	for i, ab := range [][2]float64{{0.5, 6}, {11, 14.5}, {18, 20.5}, {22, 25}} {
		id := []string{"S1", "S2", "S3", "S4"}[i]
		segs = append(segs, obj{"id": id, "file": "seg_0" + id[1:] + ".webm", "t_start": ab[0], "t_end": ab[1]})
	}
	tk := mkTake(t, "T1", E, S, segs, W)
	tk.recorded = 13.5
	return tk
}

func TestMapping(t *testing.T) {
	tk := mainTake(t)
	video := jsonObject(t, testVideo)
	moments, comments, flags := build([]*take{tk}, video, 2, 8)

	type row struct {
		order      int
		seg, mode  string
		videoT     float64
		version    string
		flip       bool
		strokes    int
		moment     string
		retracted  bool
		commentKey string
	}
	var got []row
	for _, c := range comments {
		got = append(got, row{c.Order, c.Seg, c.Mode, float64(c.VideoT), pyOpt(c.Version), c.Flip, len(c.Strokes),
			c.Moment, c.Retracted, c.Key})
	}
	want := []row{
		{1, "S1", "playing", 8.5, "v6", false, 0, "M1", false, "T1/S1/1"},
		{2, "S1", "paused", 10.0, "v6", false, 1, "M1", false, "T1/S1/2"},
		{3, "S2", "paused", 8.8, "v6", false, 0, "M1", false, "T1/S2/1"},
		{4, "S3", "paused", 10.8, "v5", true, 0, "M1", false, "T1/S3/1"}, // S3 and S4 share a frame, not a comment
		{5, "S4", "paused", 10.8, "v6", false, 1, "M1", false, "T1/S4/1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comments:\n got %+v\nwant %+v", got, want)
	}
	if len(moments) != 1 || len(moments[0].Comments) != 5 {
		t.Fatalf("moments: %+v", moments)
	}
	if len(flags) != 2 || flags[0].Order != 3 || flags[0].Kind != FlagRetract || !reflect.DeepEqual(flags[0].RefersTo, []int{1, 2}) ||
		flags[1].Order != 5 || flags[1].Kind != FlagReinstate || !reflect.DeepEqual(flags[1].RefersTo, []int{3}) {
		t.Fatalf("flags: %+v", flags)
	}
	if flags[0].Scope != ScopeClipWide || !reflect.DeepEqual(*flags[1].Restores, []int{1, 2}) {
		t.Fatalf("flag scope/restores: %+v %+v", flags[0], *flags[1].Restores)
	}

	md := renderMD(moments, comments, flags, video, []*take{tk}, "v6", []string{"v5", "v6"})
	for _, s := range []string{
		"## Rules for applying these notes", "ask Sam rather than guess",
		"## Flags: retractions and reinstatements (2)",
		`- #3 S2 [v6] · paused 00:08.8 · "Throw out everything I said about this clip." · FLAG retraction ` +
			`(everything earlier on this clip): would void #1, #2`,
		`- #5 S4 [v6] · paused 00:10.8 · "Actually, add that back." · stroke 2: green line`,
		"FLAG reinstatement: undoes #3, restoring #1, #2",
		`- #4 S3 [v5 flip] · paused 00:10.8 · "This was better in v5." · M1`,
		"family v5 v6",
	} {
		if !strings.Contains(md, s) {
			t.Errorf("notes.md lacks %q\n%s", s, md)
		}
	}
	chrono := strings.SplitN(md, "## In the order you said it", 2)[1]
	var heads []string
	for _, l := range strings.Split(chrono, "\n") {
		if strings.HasPrefix(l, "### ") {
			heads = append(heads, strings.Split(l, " · ")[0])
		}
	}
	if !reflect.DeepEqual(heads, []string{"### S1", "### S2", "### S3", "### S4"}) {
		t.Errorf("chronological heads: %v", heads)
	}
	i1, i2, i3, i5 := strings.Index(chrono, "#1 S1"), strings.Index(chrono, "#2 S1"), strings.Index(chrono, "#3 S2"),
		strings.Index(chrono, "#5 S4")
	if i1 >= i2 || i2 >= i3 || i3 >= i5 {
		t.Errorf("chronological order: %d %d %d %d", i1, i2, i3, i5)
	}
}

func TestFlagTable(t *testing.T) {
	cases := []struct{ text, kind, scope string }{
		{"Throw out everything I said about this clip.", FlagRetract, ScopeClipWide},
		{"throw all of that out", FlagRetract, ScopeWider},
		{"Scrap that.", FlagRetract, ScopeJustBefore},
		{"Never mind.", FlagRetract, ScopeJustBefore},
		{"ignore that zoom comment", FlagRetract, ScopeClip},
		{"Rescind all my previous comments on this clip.", FlagRetract, ScopeClipWide},
		{"Forget what I said so far.", FlagRetract, ScopeWider},
		{"Actually, add that back.", FlagReinstate, ""},
		{"actually keep it", FlagReinstate, ""},
		{"add that in again", FlagReinstate, ""},
		{"Never mind, keep the zoom.", FlagReinstate, ""},
		{"Throw the enemy out of the lane here.", "", ""},
		{"This zoom is too slow.", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		f := DetectFlag(c.text)
		kind := ""
		if f != nil {
			kind = f.Kind
		}
		if kind != c.kind {
			t.Errorf("%q: kind %q, want %q", c.text, kind, c.kind)
			continue
		}
		if c.scope != "" && f.Scope != c.scope {
			t.Errorf("%q: scope %q, want %q", c.text, f.Scope, c.scope)
		}
	}
	if f := DetectFlag("ok, scratch that idea"); f == nil || f.Phrase != "scratch that" {
		t.Errorf("phrase: %+v", f)
	}
}

// A sentence cut by Space: 'zoom' (said just after pausing, then 2 s of silence) stays with its sentence.
func TestSentenceCutByPause(t *testing.T) {
	E := []obj{ev(0, 0, "start", obj{"playing": true, "rate": 1}), ev(3, 3, "pause", nil)}
	var W []word
	W = append(W, words("look at this", 2.0, 2.9, "S1")...)
	W = append(W, words("zoom", 2.95, 3.15, "S1")...)
	W = append(W, words("It's way too slow.", 5.2, 6.4, "S1")...)
	tk := mkTake(t, "T2", E, nil, []obj{{"id": "S1", "t_start": 0, "t_end": 7}}, W)
	_, cs, _ := build([]*take{tk}, jsonObject(t, testVideo), 2, 8)
	var got [][2]string
	for _, c := range cs {
		got = append(got, [2]string{c.Mode, c.Text})
	}
	want := [][2]string{{"playing", "look at this zoom"}, {"paused", "It's way too slow."}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A sentence spoken across a scrub stays whole, under the moment where it started, with the path.
func TestSentenceAcrossScrub(t *testing.T) {
	E := []obj{
		ev(0, 37.9, "start", obj{"playing": true, "rate": 1}),
		ev(1, 38.9, "seek", obj{"from": 38.9}), ev(1, 38.9, "pause", nil),
		ev(2, 43.3, "seek", obj{"from": 38.9}),
	}
	W := words("I do not like how these icons need to appear here.", 0.2, 3.0, "S1")
	tk := mkTake(t, "T3", E, nil, []obj{{"id": "S1", "t_start": 0, "t_end": 4}}, W)
	_, cs, _ := build([]*take{tk}, jsonObject(t, testVideo), 2, 8)
	if len(cs) != 1 || !strings.HasPrefix(cs[0].Text, "I do not like") || !strings.HasSuffix(cs[0].Text, "appear here.") {
		t.Fatalf("comments: %+v", cs)
	}
	c := cs[0]
	if c.Mode != "playing" || float64(c.VideoT) != 38.1 || c.Moved == nil || !c.Moved.Scrub {
		t.Fatalf("comment: %+v moved %+v", c, c.Moved)
	}
	if got := movedText(c); got != "said while scrubbing 00:38.1→00:38.8→00:43.3" {
		t.Errorf("movedText = %q", got)
	}
}

func arrowPoints() [][2]float64 {
	var pts [][2]float64
	for i := range 11 {
		pts = append(pts, [2]float64{0.2 + 0.03*float64(i), 0.5})
	}
	return append(pts, [2]float64{0.47, 0.47}, [2]float64{0.5, 0.5}, [2]float64{0.47, 0.53})
}

func circlePoints() [][2]float64 {
	var pts [][2]float64
	for a := range 40 {
		pts = append(pts, [2]float64{0.7 + 0.05*math.Cos(float64(a)/6), 0.3 + 0.08*math.Sin(float64(a)/6)})
	}
	return pts
}

// Shapes: X from two crossing strokes, a one-stroke arrow, a circle; tiny ticks dropped; (audio) and the VO.
func TestShapesInComments(t *testing.T) {
	S := []obj{
		{"id": 1, "seg": "S1", "color": "red", "session_t": 0.5, "video_t": 5, "points": [][2]float64{{0.4, 0.4}, {0.5, 0.5}, {0.6, 0.6}}},
		{"id": 2, "seg": "S1", "color": "red", "session_t": 0.8, "video_t": 5, "points": [][2]float64{{0.6, 0.4}, {0.5, 0.5}, {0.4, 0.6}}},
		{"id": 3, "seg": "S1", "color": "yellow", "session_t": 1.1, "video_t": 5, "points": arrowPoints()},
		{"id": 4, "seg": "S1", "color": "green", "session_t": 1.4, "video_t": 5, "points": circlePoints()},
		{"id": 5, "seg": "S1", "color": "white", "session_t": 1.6, "video_t": 5, "points": [][2]float64{{0.5, 0.5}, {0.505, 0.502}}},
	}
	E := []obj{ev(0, 5, "start", obj{"playing": false, "rate": 1})}
	W := words("The music is way too loud here.", 2.0, 3.5, "S1")
	tk := mkTake(t, "T4", E, S, []obj{{"id": "S1", "t_start": 0, "t_end": 4}}, W)
	_, cs, _ := build([]*take{tk}, jsonObject(t, testVideo), 2, 8)
	st := map[string]*Stroke{}
	for _, c := range cs {
		for _, s := range c.Strokes {
			st[string(s.ID)] = s
		}
	}
	if len(st) != 4 || st["5"] != nil || tk.dropped != 1 {
		t.Fatalf("strokes %v, dropped %d", st, tk.dropped)
	}
	if !strings.HasPrefix(st["1"].Desc, "red X at (0.50,0.50)") || st["2"].Shape != "x" || st["1"].At == nil {
		t.Errorf("X: %q %q", st["1"].Desc, st["2"].Shape)
	}
	if !strings.HasPrefix(st["3"].Desc, "yellow arrow (0.20,0.50)→(0.50,0.50) pointing right") {
		t.Errorf("arrow: %q", st["3"].Desc)
	}
	if !strings.HasPrefix(st["4"].Desc, "green circle around") {
		t.Errorf("circle: %q", st["4"].Desc)
	}
	if !cs[0].Audio {
		t.Errorf("not (audio): %+v", cs[0])
	}
	cs[0].VO = &VO{Text: "souls are the currency", Start: 4.1, End: 6.0}
	cs[0].Moment, cs[0].Order = "M1", 1
	line := commentLine(cs[0], true)
	for _, s := range []string{`"The music is way too loud here." (audio)`, `VO there 00:04.1–00:06.0: "souls are the currency"`} {
		if !strings.Contains(line, s) {
			t.Errorf("line lacks %q: %s", s, line)
		}
	}
}

func TestDescribe(t *testing.T) {
	const w, h = 1920, 1080
	cases := []struct {
		name   string
		pts    [][2]float64
		shape  string
		prefix string
	}{
		{"line right", [][2]float64{{0.1, 0.5}, {0.4, 0.5}}, "line", "line (0.10,0.50)→(0.40,0.50) heading right"},
		{"line up-left", [][2]float64{{0.5, 0.5}, {0.3, 0.15}}, "line", "line (0.50,0.50)→(0.30,0.15) heading up-left"},
		{"line down", [][2]float64{{0.5, 0.2}, {0.5, 0.6}}, "line", "line (0.50,0.20)→(0.50,0.60) heading down"},
		{"line left", [][2]float64{{0.7, 0.6}, {0.5, 0.6}}, "line", "line (0.70,0.60)→(0.50,0.60) heading left"},
		{"line up", [][2]float64{{0.5, 0.8}, {0.5, 0.2}}, "line", "line (0.50,0.80)→(0.50,0.20) heading up"},
		{"line down-left", [][2]float64{{0.5, 0.2}, {0.3, 0.55}}, "line", "line (0.50,0.20)→(0.30,0.55) heading down-left"},
		{"line up-right", [][2]float64{{0.2, 0.8}, {0.4, 0.45}}, "line", "line (0.20,0.80)→(0.40,0.45) heading up-right"},
		{"line down-right", [][2]float64{{0.2, 0.2}, {0.4, 0.55}}, "line", "line (0.20,0.20)→(0.40,0.55) heading down-right"},
		{"arrow", arrowPoints(), "arrow", "arrow (0.20,0.50)→(0.50,0.50) pointing right"},
		{"circle", circlePoints(), "circle", "circle around (0.65,0.22)–(0.75,0.38)"},
		{"curve", [][2]float64{{0.1, 0.9}, {0.3, 0.85}, {0.2, 0.7}, {0.35, 0.6}, {0.15, 0.55}}, "curve",
			"curve (0.10,0.90)→(0.15,0.55) within (0.10,0.55)–(0.35,0.90)"},
		{"no points", nil, "line", "line (0.00,0.00)→(0.00,0.00) heading right"},
	}
	for _, c := range cases {
		desc, g := Describe(c.pts, w, h)
		if g.Shape != c.shape || !strings.HasPrefix(desc, c.prefix) {
			t.Errorf("%s: %q (%s), want %q (%s)", c.name, desc, g.Shape, c.prefix, c.shape)
		}
	}
	_, g := Describe(arrowPoints(), w, h)
	if g.To != [2]float64{0.5, 0.5} || g.From != [2]float64{0.2, 0.5} {
		t.Errorf("arrow geometry: %+v", g)
	}
	// Two nearly parallel lines don't make an X; two crossing ones do.
	mk := func(a, b [2]float64) *Stroke {
		return &Stroke{Color: "red", Shape: "line", From: [2]jsonFloat{jsonFloat(a[0]), jsonFloat(a[1])},
			To: [2]jsonFloat{jsonFloat(b[0]), jsonFloat(b[1])}}
	}
	par := []*Stroke{mk([2]float64{0.1, 0.1}, [2]float64{0.5, 0.5}), mk([2]float64{0.1, 0.12}, [2]float64{0.5, 0.48})}
	markX(par, w, h)
	if par[0].Shape != "line" || par[1].Shape != "line" {
		t.Errorf("parallel lines became an X")
	}
	x := []*Stroke{mk([2]float64{0.4, 0.4}, [2]float64{0.6, 0.6}), mk([2]float64{0.6, 0.4}, [2]float64{0.4, 0.6}),
		mk([2]float64{0.4, 0.6}, [2]float64{0.6, 0.4})}
	markX(x, w, h)
	if x[0].Shape != "x" || x[1].Shape != "x" || x[2].Shape != "line" || *x[0].At != [2]jsonFloat{0.5, 0.5} {
		t.Errorf("X marking: %q %q %q", x[0].Shape, x[1].Shape, x[2].Shape)
	}
}

func TestCluster(t *testing.T) {
	mk := func(anchors ...float64) []*Comment {
		var cs []*Comment
		for i, a := range anchors {
			cs = append(cs, &Comment{Order: i + 1, Anchor: jsonFloat(a), VideoT: jsonFloat(a), VideoTEnd: jsonFloat(a + 0.5)})
		}
		return cs
	}
	ids := func(ms []*Moment) [][]int {
		var out [][]int
		for _, m := range ms {
			var os []int
			for _, c := range m.Comments {
				os = append(os, c.Order)
			}
			out = append(out, os)
		}
		return out
	}
	cases := []struct {
		name            string
		anchors         []float64
		window, maxSpan float64
		want            [][]int
	}{
		{"chain within the window", []float64{0, 1.5, 3, 4.5, 6, 7.5, 9}, 2, 8, [][]int{{1, 2, 3, 4, 5, 6}, {7}}},
		{"gap over the window", []float64{0, 2.5}, 2, 8, [][]int{{1}, {2}}},
		{"window is inclusive", []float64{0, 2}, 2, 8, [][]int{{1, 2}}},
		{"video order, spoken order inside", []float64{10, 0, 10.5, 1}, 2, 8, [][]int{{2, 4}, {1, 3}}},
		{"max span", []float64{0, 1, 2, 3}, 2, 2.5, [][]int{{1, 2, 3}, {4}}},
		{"wider window", []float64{0, 2.5, 5}, 3, 6, [][]int{{1, 2, 3}}},
	}
	for _, c := range cases {
		ms := cluster(mk(c.anchors...), c.window, c.maxSpan)
		if got := ids(ms); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		for i, m := range ms {
			if m.ID != "M"+string(rune('1'+i)) {
				t.Errorf("%s: moment %d id %s", c.name, i, m.ID)
			}
			for _, cm := range m.Comments {
				if cm.Moment != m.ID {
					t.Errorf("%s: comment %d in %s says %s", c.name, cm.Order, m.ID, cm.Moment)
				}
			}
		}
	}
	ms := cluster(mk(4, 3), 2, 8)
	if float64(ms[0].Start) != 3 || float64(ms[0].End) != 4.5 {
		t.Errorf("moment range %v-%v, want 3-4.5", ms[0].Start, ms[0].End)
	}
}

func TestFormatting(t *testing.T) {
	for in, want := range map[float64]string{0: "00:00.0", 41.27: "00:41.2", 59.99: "00:59.9", 60: "01:00.0",
		3725.4: "1:02:05.4", -3: "00:00.0", 0.3: "00:00.3", 2.9999999: "00:03.0"} {
		if got := fmtT(in); got != want {
			t.Errorf("fmtT(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{0: "0:00", 59.9: "0:59", 61: "1:01", 3600: "60:00"} {
		if got := fmtS(in); got != want {
			t.Errorf("fmtS(%v) = %q, want %q", in, got, want)
		}
	}
	if got := fmtRaw(nil); got != "--:--.-" {
		t.Errorf("fmtRaw(nil) = %q", got)
	}
	for raw, want := range map[string]string{"null": "None", "30": "30", "30.0": "30.0", "29.97": "29.97",
		"true": "True", `"x"`: "x", "1e-5": "1e-05"} {
		if got := pyStr(json.RawMessage(raw)); got != want {
			t.Errorf("pyStr(%s) = %q, want %q", raw, got, want)
		}
	}
	// CPython 3.12 sum() is compensated: 0.1 * 10 sums to exactly 1.0.
	tenth := []float64{0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1, 0.1}
	if got := pySum(tenth); got != 1.0 {
		t.Errorf("pySum = %v", got)
	}
	if got := hypot(3, 4); got != 5 {
		t.Errorf("hypot(3, 4) = %v", got)
	}
	if pyMod(-1, 360) != 359 || pyFloorDiv(-1, 45) != -1 || pyFloorDiv(359, 45) != 7 {
		t.Errorf("pyMod/pyFloorDiv")
	}
}
