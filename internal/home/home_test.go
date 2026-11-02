package home

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// touch writes a file (bytes, not video: discovery goes by names) with a modification time.
func touch(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video "+filepath.Base(path)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"item-shop":                        "item-shop",
		"Item Shop (v2)!":                  "item-shop-v2",
		"  __x__  ":                        "x",
		"Café crème":                       "caf-cr-me",
		"":                                 "video",
		"!!!":                              "video",
		"2026-10-25":                       "2026-10-25",
		strings.Repeat("ab", 40) + " tail": strings.Repeat("ab", 31) + "a",
	} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if got := unique("x", map[string]bool{"x": true, "x-2": true}); got != "x-3" {
		t.Errorf("unique = %q", got)
	}
	long := strings.Repeat("a", MaxName)
	if got := unique(long, map[string]bool{long: true}); len(got) != MaxName || !strings.HasSuffix(got, "-2") {
		t.Errorf("unique long = %q", got)
	}
}

func TestDiscover(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Shows")
	now := time.Now()
	old := now.Add(-time.Hour)
	// The folder itself: a flat family.
	touch(t, filepath.Join(dir, "intro_v1.mp4"), old)
	// Version folders: the newest number wins over a newer file of an older one.
	touch(t, filepath.Join(dir, "alpha", "v1", "alpha_v1.mp4"), now)
	touch(t, filepath.Join(dir, "alpha", "v2", "alpha_v2.mp4"), old)
	touch(t, filepath.Join(dir, "alpha", "v2", "alpha_v2.review", "x_v9.mp4"), now) // inside a .review: never
	// A flat family: the plain name before the draft, whatever their times.
	touch(t, filepath.Join(dir, "Bravo Cut", "bravo_v3_draft.mp4"), now)
	touch(t, filepath.Join(dir, "Bravo Cut", "bravo_v3.mp4"), old)
	touch(t, filepath.Join(dir, "Bravo Cut", "bravo_v1.mp4"), old)
	// The same name made URL-safe: the later folder in name order gets -2.
	touch(t, filepath.Join(dir, "bravo-cut", "other_v1.m3u8"), old)
	// Not projects: unnumbered clips, a hidden folder, a .review folder, and a text file with a version.
	touch(t, filepath.Join(dir, "broll", "sunset.mp4"), old)
	touch(t, filepath.Join(dir, ".cache", "x_v1.mp4"), old)
	touch(t, filepath.Join(dir, "notes.review", "y_v1.mp4"), old)
	touch(t, filepath.Join(dir, "deep", "readme_v1.txt"), old)
	// A family two levels down whose root is its own folder: a video in the project deep, named by its path. The
	// videos directly in the folder (each folder holds one) are in the folder's own project, the top.
	touch(t, filepath.Join(dir, "deep", "sub", "w_v1.mp4"), old)

	got := Discover(dir)
	want := []Found{
		{Name: "shows", Title: "Shows", Root: dir, Anchor: filepath.Join(dir, "intro_v1.mp4")},
		{Name: "bravo-cut", Title: "Bravo Cut", Root: filepath.Join(dir, "Bravo Cut"),
			Anchor: filepath.Join(dir, "Bravo Cut", "bravo_v3.mp4"), Path: "Bravo Cut"},
		{Name: "alpha", Title: "alpha", Root: filepath.Join(dir, "alpha"),
			Anchor: filepath.Join(dir, "alpha", "v2", "alpha_v2.mp4"), Path: "alpha"},
		{Name: "bravo-cut-2", Title: "bravo-cut", Root: filepath.Join(dir, "bravo-cut"),
			Anchor: filepath.Join(dir, "bravo-cut", "other_v1.m3u8"), Path: "bravo-cut"},
		{Name: "deep-sub", Title: "sub", Root: filepath.Join(dir, "deep", "sub"),
			Anchor: filepath.Join(dir, "deep", "sub", "w_v1.mp4"), Path: "deep/sub", Project: "deep"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Discover:\n got %+v\nwant %+v", got, want)
	}

	// A version folder opened by itself holds no project of its own: its videos' root is its parent.
	if got := Discover(filepath.Join(dir, "alpha", "v2")); len(got) != 0 {
		t.Errorf("a version folder: %+v", got)
	}
	// Sibling folders that hold one family make their parent the root (spec/notes.md), so a family split across
	// two plainly named folders is one project.
	split := filepath.Join(t.TempDir(), "split")
	touch(t, filepath.Join(split, "cut", "first", "ep_v1.mp4"), old)
	touch(t, filepath.Join(split, "cut", "second", "ep_v2.mp4"), old)
	if got := Discover(split); len(got) != 1 || got[0].Name != "cut" || filepath.Base(got[0].Anchor) != "ep_v2.mp4" {
		t.Errorf("split family: %+v", got)
	}
	if got := Discover(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("a missing folder: %+v", got)
	}
}

func TestStatusOf(t *testing.T) {
	for _, c := range []struct {
		closed, open int
		want         Status
	}{{0, 0, ToReview}, {0, 3, ToReview}, {1, 0, Reviewed}, {2, 1, BeingEdited}} {
		if got := StatusOf(c.closed, c.open); got != c.want {
			t.Errorf("StatusOf(%d, %d) = %s, want %s", c.closed, c.open, got, c.want)
		}
	}
}

// takeIn writes a take folder of a version with session.json; closed sets closed: true.
func takeIn(t *testing.T, video, take string, closed bool) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(video), strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))+".review", take)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"format": 3}`
	if closed {
		body = `{"format": 3, "closed": true}`
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func infoOf(t *testing.T, label, path string, extra map[string]any) []byte {
	t.Helper()
	v := map[string]any{"label": label, "path": path, "name": filepath.Base(path), "duration": 12.5}
	for k, x := range extra {
		v[k] = x
	}
	m := map[string]any{"versions": []any{map[string]any{"label": "v1"}, v}}
	for k, x := range v {
		m[k] = x
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMakeCards(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "v2", "clip_v2.mp4")
	pushed := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	touch(t, video, pushed)

	// No version yet: to review, without a thumbnail.
	if c := Make(Source{Name: "empty"}); c.Status != ToReview || c.Thumb != "" || c.Version != "" || c.Title != "empty" ||
		c.URL != "p/empty/" {
		t.Errorf("empty card %+v", c)
	}

	// A version no one closed a take on (an open take doesn't count): to review, with the file's time as its push.
	takeIn(t, video, "2026-10-02_10-00-00", false)
	c := Make(Source{Name: "clip", Title: "Clip", Info: infoOf(t, "v2", video, nil)})
	want := Card{Name: "clip", Title: "Clip", Video: "clip", URL: "p/clip/", Version: "v2", Versions: 2, Duration: c.Duration,
		Pushed: "2026-10-01T12:00:00Z", Status: ToReview, Thumb: "p/clip/api/poster", Changed: pushed.UnixMilli()}
	if c.Duration == nil || *c.Duration != 12.5 || !reflect.DeepEqual(c, want) {
		t.Errorf("card %+v", c)
	}

	// Done on one take: reviewed. A legacy take with notes.md counts as closed too.
	takeIn(t, video, "2026-10-02_11-00-00", true)
	if c := Make(Source{Name: "clip", Info: infoOf(t, "v2", video, nil)}); c.Status != Reviewed || c.Takes != 1 {
		t.Errorf("reviewed card %+v", c)
	}
	legacy := filepath.Join(root, "v2", "clip_v2.review", "2026-10-02_12-00-00")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "notes.md"), []byte("# notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c := Make(Source{Name: "clip", Info: infoOf(t, "v2", video, nil)}); c.Takes != 2 {
		t.Errorf("legacy take not counted: %+v", c)
	}

	// Open notes on it: being edited. The push record's time wins over the file's.
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	st.Now = func() time.Time { return pushed.Add(time.Hour) }
	if _, err := st.RecordPush(project.Push{Label: "v2", Path: video, Scope: project.Scope{Version: "v2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Sync(project.Run{Version: "v2", Comments: []project.Comment{
		{Take: "2026-10-02_11-00-00", Seg: "S1", N: 0, T0: 1, T1: 2, Text: "tighter", Author: "Ana"},
		{Take: "2026-10-02_11-00-00", Seg: "S1", N: 1, T0: 3, T1: 4, Text: "louder", Author: "Ana"},
		{Take: "2026-10-02_11-00-00", Seg: "S1", N: 2, T0: 5, T1: 6, Text: "scrap that", Author: "Ana", Retracted: true},
	}}); err != nil {
		t.Fatal(err)
	}
	c = Make(Source{Name: "clip", Info: infoOf(t, "v2", video, nil), Store: st})
	if c.Status != BeingEdited || c.Open != 2 || c.Pushed != "2026-10-01T13:00:00Z" {
		t.Errorf("being edited card %+v", c)
	}
	// Notes of another version don't count.
	if c := Make(Source{Name: "clip", Info: infoOf(t, "v3", video, nil), Store: st}); c.Open != 0 {
		t.Errorf("notes of v2 counted on v3: %+v", c)
	}

	// The streaming side's state and poster pass through.
	c = Make(Source{Name: "clip", Info: infoOf(t, "v2", video, map[string]any{
		"stream": map[string]any{"state": "rendering", "done": 3, "total": 12}, "poster": "video/v2/clip_v2.poster.jpg"})})
	if !reflect.DeepEqual(c.Stream, &Stream{State: "rendering", Done: 3, Total: 12}) ||
		c.Thumb != "p/clip/video/v2/clip_v2.poster.jpg" {
		t.Errorf("stream card %+v %+v", c, c.Stream)
	}
	if c := Make(Source{Name: "x", Info: []byte(`{"label": "v1", "versions": [{"label": "v1", "stream":
		{"status": "pending"}, "poster": "/abs.jpg"}]}`)}); c.Stream == nil || c.Stream.State != "pending" ||
		c.Thumb != "/abs.jpg" {
		t.Errorf("stream from the family entry: %+v", c)
	}
	if c := Make(Source{Name: "x", Info: []byte(`not json`)}); c.Version != "" || c.Status != ToReview {
		t.Errorf("garbled info: %+v", c)
	}
}

func TestParseStream(t *testing.T) {
	for raw, want := range map[string]*Stream{
		``:                      nil,
		`null`:                  nil,
		`"rendering"`:           nil,
		`{}`:                    nil,
		`{"state": "complete"}`: {State: "complete"},
		`{"state": "rendering", "ready": 2, "total": 9}`: {State: "rendering", Done: 2, Total: 9},
		`{"status": "pending", "rendered": -1}`:          {State: "pending"},
	} {
		if got := ParseStream(json.RawMessage(raw)); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseStream(%s) = %+v, want %+v", raw, got, want)
		}
	}
}

func TestSort(t *testing.T) {
	cards := []Card{
		{Name: "b", Status: Reviewed, Pushed: "2026-10-05T00:00:00Z"},
		{Name: "d", Status: ToReview, Pushed: "2026-10-01T00:00:00Z"},
		{Name: "a", Status: BeingEdited, Pushed: "2026-10-03T00:00:00Z"},
		{Name: "c", Status: ToReview, Pushed: "2026-10-02T00:00:00Z"},
		{Name: "e", Status: ToReview, Pushed: "2026-10-02T00:00:00Z"},
		{Name: "f", Status: ToReview},
	}
	Sort(cards)
	var got []string
	for _, c := range cards {
		got = append(got, c.Name)
	}
	if want := []string{"c", "e", "d", "f", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order %v, want %v", got, want)
	}
}

func TestJoinURL(t *testing.T) {
	for in, want := range map[string]string{
		"poster.jpg":                "p/x/poster.jpg",
		"./video/v1/a.jpg":          "p/x/video/v1/a.jpg",
		"/p/x/a.jpg":                "/p/x/a.jpg",
		"https://cdn.test/a.jpg":    "https://cdn.test/a.jpg",
		"data:image/jpeg;base64,AA": "data:image/jpeg;base64,AA",
		"a:b/c":                     "a:b/c",
		"1a:b":                      "p/x/1a:b",
	} {
		if got := joinURL("p/x/", in); got != want {
			t.Errorf("joinURL(%q) = %q, want %q", in, got, want)
		}
	}
}
