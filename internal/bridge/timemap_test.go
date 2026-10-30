package bridge

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-3 }

// checkCover asserts that the segments cover A from 0 to its end, in order, without holes.
func checkCover(t *testing.T, tm TimeMap, end float64) {
	t.Helper()
	if len(tm.Segments) == 0 {
		t.Fatal("no segments")
	}
	if tm.Segments[0].A[0] != 0 || !near(tm.Segments[len(tm.Segments)-1].A[1], end) {
		t.Fatalf("segments cover %v-%v, want 0-%v", tm.Segments[0].A[0], tm.Segments[len(tm.Segments)-1].A[1], end)
	}
	for i := 1; i < len(tm.Segments); i++ {
		if !near(tm.Segments[i].A[0], tm.Segments[i-1].A[1]) {
			t.Fatalf("hole between %+v and %+v", tm.Segments[i-1], tm.Segments[i])
		}
	}
}

// The demo's v1 -> v2: 0.3 s of VO air after 'generated.' (3.6-3.9 s on v1) is trimmed, so everything after it
// plays 0.3 s earlier; the picture-in-picture changed but its voice didn't.
func TestTimeMapOverAVOTrim(t *testing.T) {
	a, b := fixture(t, "demo_v1.manifest.json"), fixture(t, "demo_v2.manifest.json")
	tm := BuildTimeMap("v1", "v2", a, b)
	checkCover(t, tm, a.End())
	cases := []struct {
		t, want float64
		why     string
	}{
		{0.5, 0.5, "the title is the same unit (same video hash)"},
		{2.5, 2.5, "the footage before the trim plays the same VO second"},
		{4.5, 4.2, "the insert is the same bytes, 0.3 s earlier"},
		{5.5, 5.2, "the footage after the insert: same shot, from its start"},
		{7.67, 7.37, "the pop (re-rendered unit) by its VO"},
		{9, 8.7, "the picture-in-picture (changed) by its VO"},
		{13, 12.7, "the music bed (re-encoded: timeline clock) by its VO"},
		{16.1, 15.8, "the end"},
	}
	for _, c := range cases {
		if got := tm.Map(c.t); !near(got, c.want) {
			t.Errorf("%v -> %v, want %v (%s)", c.t, got, c.want, c.why)
		}
	}
	// The trimmed air (3.6-3.9 on v1) isn't in v2: it lands at the end of the shortened footage shot.
	for _, x := range []float64{3.65, 3.8, 3.89} {
		if got := tm.Map(x); got < 3.5 || got >= 3.6 {
			t.Errorf("trimmed %v -> %v, want just before 3.6", x, got)
		}
	}
	by := map[string]bool{}
	for _, s := range tm.Segments {
		by[s.By] = true
		if s.A[1] < s.A[0] || s.B[1] < s.B[0] {
			t.Errorf("backwards segment %+v", s)
		}
	}
	for _, k := range []string{"video", "spine", "shot"} {
		if !by[k] {
			t.Errorf("no %q segment in %+v", k, tm.Segments)
		}
	}
	// And back: v2's moments land on v1's.
	back := BuildTimeMap("v2", "v1", b, a)
	checkCover(t, back, b.End())
	for _, c := range cases {
		if got := back.Map(c.want); !near(got, c.t) {
			t.Errorf("back %v -> %v, want %v", c.want, got, c.t)
		}
	}
}

func TestTimeMapIdentityAndNone(t *testing.T) {
	a := fixture(t, "demo_v1.manifest.json")
	tm := BuildTimeMap("v1", "v1", a, a)
	if len(tm.Segments) != 1 || tm.Segments[0].A != [2]float64{0, 16.1} || tm.Segments[0].B != tm.Segments[0].A {
		t.Fatalf("a version onto itself is one segment: %+v", tm.Segments)
	}
	if none := BuildTimeMap("v1", "v9", a, nil); len(none.Segments) != 0 || none.Map(5) != 5 {
		t.Fatalf("no manifest: %+v", none)
	}
	if got := (TimeMap{}).Map(3.3); got != 3.3 {
		t.Fatal(got)
	}
}

func unit(name, shot, hash string, t0, t1 float64, els ...Element) Unit {
	return Unit{Unit: name, Kind: "shot", Shot: shot, T: [2]float64{t0, t1}, VideoHash: hash, AudioHash: "a" + hash,
		Elements: els}
}

func spine(id, asset string, t0, t1, in float64) Element {
	return Element{ID: id, Kind: "spine", Z: -1, T: [2]float64{t0, t1},
		Sources: []Source{{Asset: asset, In: in, Out: in + t1 - t0}}}
}

func TestTimeMapReorderedAndUnmatched(t *testing.T) {
	// v1: A B C; v2: C B, A dropped, and a new unit N first. Same bytes map to wherever they went; A has no match
	// and falls between its neighbors' mapped moments.
	a := &Manifest{FPS: 10, Units: []Unit{unit("u1", "a", "ha", 0, 2), unit("u2", "b", "hb", 2, 5),
		unit("u3", "c", "hc", 5, 6)}}
	b := &Manifest{FPS: 10, Units: []Unit{unit("u1", "n", "hn", 0, 1), unit("u2", "c", "hc", 1, 2),
		unit("u3", "b", "hb", 2, 5)}}
	tm := BuildTimeMap("v1", "v2", a, b)
	checkCover(t, tm, 6)
	if got := tm.Map(3); !near(got, 3) {
		t.Errorf("B: %v", got)
	}
	if got := tm.Map(5.5); !near(got, 1.5) {
		t.Errorf("C moved first: %v", got)
	}
	if got := tm.Map(1); got < 0 || got > 2 {
		t.Errorf("A (gone) should fall before B's start: %v", got)
	}
	if tm.Segments[0].By != "gap" {
		t.Errorf("A is a gap: %+v", tm.Segments[0])
	}
}

func TestTimeMapBySpineAcrossUnits(t *testing.T) {
	// A VO item cut differently: v2 cut 1 s out of the middle of the VO, inside one changed shot.
	a := &Manifest{FPS: 10, Units: []Unit{unit("u1", "s", "h1", 0, 6, spine("vo", "proj:vo", 0, 6, 10))}}
	b := &Manifest{FPS: 10, Units: []Unit{unit("u1", "s", "h2", 0, 5,
		spine("vo", "proj:vo", 0, 2, 10), spine("vo2", "proj:vo", 2, 5, 13))}}
	tm := BuildTimeMap("v1", "v2", a, b)
	checkCover(t, tm, 6)
	for _, c := range [][2]float64{{1, 1}, {1.9, 1.9}, {3.5, 2.5}, {5.5, 4.5}} {
		if got := tm.Map(c[0]); !near(got, c[1]) {
			t.Errorf("%v -> %v, want %v", c[0], got, c[1])
		}
	}
	// The cut second (2-3 on v1) isn't voiced in v2: same shot from its start, so it stays at 2-3.
	if got := tm.Map(2.5); !near(got, 2.5) {
		t.Errorf("cut stretch -> %v", got)
	}
}

func TestCarry(t *testing.T) {
	a, b := fixture(t, "demo_v1.manifest.json"), fixture(t, "demo_v2.manifest.json")
	cases := []struct {
		t               float64
		audio, answered bool
		want            string
	}{
		{1, false, false, "unchanged"},   // the title: the same bytes in v2
		{1, true, true, "unchanged"},     // and the same sound
		{4.5, false, false, "unchanged"}, // the insert's picture is the same...
		{4.5, true, true, "check"},       // ...but its sound changed (the music bed's phase moved with the trim)
		{9, false, true, "check"},        // the picture-in-picture changed, and the note was answered
		{13, false, false, "changed"},    // the music card re-rendered for another reason
		{99, false, false, "changed"},    // outside every unit
	}
	for _, c := range cases {
		if got := Carry(a, b, c.t, c.audio, c.answered); got != c.want {
			t.Errorf("t %v audio %v answered %v: %s, want %s", c.t, c.audio, c.answered, got, c.want)
		}
	}
}

func TestMapSegmentsEdges(t *testing.T) {
	segs := []Segment{{A: [2]float64{1, 2}, B: [2]float64{10, 12}, By: "video"}, {A: [2]float64{2, 4}, B: [2]float64{12, 12}, By: "shot"}}
	for _, c := range [][2]float64{{0, 10}, {1.5, 11}, {2, 12}, {3, 12}, {9, 12}} {
		if got := mapSegments(segs, c[0]); !near(got, c[1]) {
			t.Errorf("%v -> %v, want %v", c[0], got, c[1])
		}
	}
	m := merge([]Segment{{A: [2]float64{0, 1}, B: [2]float64{5, 6}, By: "video"}, {A: [2]float64{1, 2}, B: [2]float64{6, 7}, By: "video"},
		{A: [2]float64{2, 3}, B: [2]float64{7, 8}, By: "spine"}})
	if len(m) != 2 || m[0].A != [2]float64{0, 2} || m[0].B != [2]float64{5, 7} {
		t.Fatalf("merge = %+v", m)
	}
}
