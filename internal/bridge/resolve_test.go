package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// The fixtures are real manifests of the demo (editor/examples/demo, rendered as drafts): v1 at rev 0, and v2
// after a patch that makes the picture-in-picture bigger and one that trims 0.3 s of VO air after 2 s.
func fixture(t *testing.T, name string) *Manifest {
	t.Helper()
	m, err := Load(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ids(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}

func box(x, y, w, h float64) *[4]float64 { return &[4]float64{x, y, w, h} }

// small is the manifest of the editor's own resolve-note unit tests.
func small() *Manifest {
	el := func(id, kind string, z int64, b *[4]float64) Element {
		e := Element{ID: id, Ptr: "/x/" + id, Kind: kind, Z: z, T: [2]float64{10, 20}}
		if b != nil {
			e.Box = [][5]float64{{10, b[0], b[1], b[2], b[3]}}
		}
		return e
	}
	return &Manifest{Schema: "manifest/0.1", Size: [2]float64{1920, 1080}, FPS: 30, Duration: 20,
		Units: []Unit{{Unit: "u1", Key: "k1", Kind: "shot", Shot: "shot", T: [2]float64{10, 20},
			Elements: []Element{
				el("shot", "shot", 0, box(0, 0, 1920, 1080)),
				el("pip", "overlay", 2, box(1300, 100, 500, 300)),
				el("pop", "overlay", 1, box(600, 800, 700, 150)),
				el("vo", "spine", -1, nil),
			},
			Words: []Word{{14, 14.4, "Hello"}, {14.6, 15, "world"}}}}}
}

func TestResolveRanksLikeResolveNote(t *testing.T) {
	m := small()
	r := Resolve(m, 15, box(1400, 150, 200, 100))
	if got := ids(r.Targets); !slices.Equal(got[:2], []string{"pip", "shot"}) || r.Targets[0].Score != 1 {
		t.Fatalf("stroke on the pip: %v", got)
	}
	if r.Word == nil || r.Word.Text != "world" {
		t.Fatalf("nearest word within 0.5 s: %+v", r.Word)
	}
	if r := Resolve(m, 12, box(100, 100, 50, 50)); r.Targets[0].ID != "shot" || r.Targets[0].Score != 1 || r.Word != nil {
		t.Fatalf("empty area: %+v", r)
	}
	if got := ids(Resolve(m, 14.2, nil).Targets); !slices.Equal(got, []string{"pip", "pop", "shot", "vo"}) {
		t.Fatalf("no stroke ranks by z, sound last: %v", got)
	}
	if r := Resolve(m, 3, nil); r.Unit != "" || len(r.Targets) != 0 {
		t.Fatalf("outside every unit: %+v", r)
	}
	if r := Resolve(m, 15, box(1500, 200, 0, 0)); r.Targets[0].ID != "pip" || r.Targets[0].Score != 1 {
		t.Fatalf("a point stroke inside the pip: %+v", r.Targets)
	}
	e := Element{Box: [][5]float64{{10, 0, 0, 10, 10}, {12, 0, 0, 0, 0}, {13, 5, 5, 10, 10}}}
	for _, c := range []struct {
		t  float64
		ok bool
	}{{11, true}, {12.5, false}, {13, true}, {9, false}} {
		if _, ok := e.BoxAt(c.t); ok != c.ok {
			t.Errorf("BoxAt(%v) = %v", c.t, ok)
		}
	}
}

func TestNoteTargetsOnTheDemo(t *testing.T) {
	m := fixture(t, "demo_v1.manifest.json")
	// A circle around the picture-in-picture at 9.0 s (the editor's end-to-end test uses the same box).
	r := NoteTargets(m, 9, []Stroke{{T: 9, Box: [4]float64{0.7, 0.65, 0.1, 0.1}}})
	if got := ids(r.Targets); len(got) < 3 || !slices.Equal(got[:3], []string{"pip/v", "pip", "body/footage"}) {
		t.Fatalf("targets = %v", got)
	}
	if r.Unit != "u5" || r.Word == nil || r.Word.Text != "corner" {
		t.Fatalf("unit %s word %+v", r.Unit, r.Word)
	}
	// Two strokes: one on the pip, one on the pop. Each element keeps its best score.
	r = NoteTargets(m, 7.8, []Stroke{{T: 7.8, Box: [4]float64{0.4, 0.75, 0.2, 0.1}}, {T: 9, Box: [4]float64{0.7, 0.65, 0.1, 0.1}}})
	got := ids(r.Targets)
	if !slices.Contains(got, "pop/faster") || !slices.Contains(got, "pip/v") {
		t.Fatalf("merged = %v", got)
	}
	seen := map[string]bool{}
	for _, tg := range r.Targets {
		if seen[tg.ID] {
			t.Fatalf("%s listed twice: %v", tg.ID, got)
		}
		seen[tg.ID] = true
	}
	// No strokes: at t0, by z.
	if r := NoteTargets(m, 1, nil); r.Targets[0].ID != "open/title" {
		t.Fatalf("no strokes = %v", ids(r.Targets))
	}
}

// editorForTests finds a built retake-edit ($RETAKE_EDIT, $PATH, or editor/target/release in this repo).
func editorForTests(t *testing.T) (Editor, string) {
	t.Helper()
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	e := Editor{Dir: repo, Exe: filepath.Join(repo, "bin", "retake")}
	bin, err := e.Find()
	if err != nil {
		t.Skipf("no retake-edit to compare with: %v", err)
	}
	return e, bin
}

// The Go resolution is the editor's: the same targets, scores and word for the same manifest, time and box.
func TestResolveMatchesRetakeEdit(t *testing.T) {
	e, bin := editorForTests(t)
	path := filepath.Join("testdata", "demo_v1.manifest.json")
	m := fixture(t, "demo_v1.manifest.json")
	cases := [][5]float64{
		{9, 0.7, 0.65, 0.1, 0.1}, {7.8, 0.4, 0.75, 0.2, 0.1}, {1, 0.45, 0.45, 0.1, 0.1}, {13, 0.1, 0.8, 0.3, 0.1},
		{4.5, 0.5, 0.5, 0.01, 0.01}, {16.1, 0, 0, 1, 1}, {10.5, 0.9, 0.9, 0.05, 0.05},
	}
	for _, c := range cases {
		args := []string{"resolve-note", path}
		for _, x := range c {
			args = append(args, strconv.FormatFloat(x, 'f', -1, 64))
		}
		args = append(args, "--json")
		var want Resolution
		if err := e.run(context.Background(), bin, args, &want); err != nil {
			t.Fatal(err)
		}
		px := [4]float64{c[1] * m.Size[0], c[2] * m.Size[1], c[3] * m.Size[0], c[4] * m.Size[1]}
		got := Resolve(m, c[0], &px)
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		if string(g) != string(w) {
			t.Errorf("t %v box %v:\n go   %s\n edit %s", c[0], c[1:], g, w)
		}
	}
}

func TestLoad(t *testing.T) {
	if _, err := ForVideo(filepath.Join("testdata", "nothing.mp4")); !errors.Is(err, ErrNoManifest) {
		t.Fatalf("missing = %v", err)
	}
	m := fixture(t, "demo_v2.manifest.json")
	if m.Timeline.Rev != 2 || m.Video != "demo_v2.mp4" || len(m.Units) != 6 || m.Units[4].Words[0].Text == "" {
		t.Fatalf("v2 = %+v %s", m.Timeline, m.Video)
	}
	if got := ManifestPath("/a/clip_v2.mp4"); got != "/a/clip_v2.manifest.json" {
		t.Fatal(got)
	}
	if got := PushRecordPath("/a/clip_v2.mp4"); got != "/a/clip_v2.push.json" {
		t.Fatal(got)
	}
	if m.UnitAt(m.End()) != len(m.Units)-1 || m.UnitAt(-1) != -1 {
		t.Fatal("the last unit holds its end")
	}
	w, _ := json.Marshal(Word{1, 1.5, "hi"})
	var back Word
	if string(w) != `[1,1.5,"hi"]` || json.Unmarshal(w, &back) != nil || back.Text != "hi" ||
		json.Unmarshal([]byte(`[1, 2]`), &back) == nil {
		t.Fatalf("word %s %+v", w, back)
	}
}
