package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/jsonx"
)

func fptr(f float64) *jsonx.Float { v := jsonx.Float(f); return &v }
func iptr(i int) *int             { return &i }

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidNames(t *testing.T) {
	for id, ok := range map[string]bool{
		"2026-10-29_20-48-01": true, "2026-10-29_20-48-1": false, "2026-10-29 20-48-01": false,
		"../2026-10-29_20-48-01": false, "2026-10-29_20-48-01\n": false, "": false,
	} {
		if ValidTakeID(id) != ok {
			t.Errorf("ValidTakeID(%q) != %v", id, ok)
		}
	}
	for name, ok := range map[string]bool{
		"seg_01.webm": true, "seg_123.ogg": true, "seg_07.m4a": true, "seg_1.webm": false, "seg_1234.webm": false,
		"seg_01.wav": false, "../seg_01.webm": false, "seg_01.webm.tmp": false, "SEG_01.webm": false,
	} {
		if ValidSegName(name) != ok {
			t.Errorf("ValidSegName(%q) != %v", name, ok)
		}
	}
}

func TestReviewDir(t *testing.T) {
	v := filepath.Join("a", "v6", "clip_v6.mp4")
	if got, want := ReviewDir(v), filepath.Join("a", "v6", "clip_v6.review"); got != want {
		t.Errorf("ReviewDir = %q, want %q", got, want)
	}
	if got, want := TakeDir(v, "2026-01-02_03-04-05"), filepath.Join("a", "v6", "clip_v6.review", "2026-01-02_03-04-05"); got != want {
		t.Errorf("TakeDir = %q, want %q", got, want)
	}
}

func fields() ServerFields {
	return ServerFields{
		Version: "v6",
		Versions: []VersionRef{
			{Label: "v5", Path: "/x/v5/clip_v5.mp4", Duration: fptr(40)},
			{Label: "v6", Path: "/x/v6/clip_v6.mp4", Duration: fptr(40)},
			{Label: "v7", Path: "/x/v7/c&d_v7.mp4", Duration: nil},
		},
		Video:  VideoRef{Path: "/x/v6/clip_v6.mp4", Name: "clip_v6.mp4", Duration: fptr(40), FPS: 30, Width: iptr(1920), Height: iptr(1080)},
		Author: &Author{Name: "Tester"},
	}
}

func TestSaveDataWritesEveryFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "clip_v6.review", "2026-10-29_20-48-01")
	body := `{"session":{"format":3,"version":"stale","session":"2026-10-29_20-48-01","created":1730000000000,` +
		`"video":{"duration":40},"display":{"show_within":1.5}},"segments":[{"id":"S1","t_start":0.5,"t_end":3,"note":"é"}],` +
		`"events":[{"session_t":0,"video_t":1.25,"event":"start"}],"strokes":[{"id":1,"points":[[0.1,0.2]]}],` +
		`"voice":{"bin":0.25,"bins":[[4,0.5]]}}`
	if err := SaveData(dir, []byte(body), fields()); err != nil {
		t.Fatal(err)
	}
	// session.json keeps the client's key order; version and video are replaced in place, versions and author
	// are appended. Indentation is Python's json.dumps(indent=1).
	wantSession := `{
 "format": 3,
 "version": "v6",
 "session": "2026-10-29_20-48-01",
 "created": 1730000000000,
 "video": {
  "path": "/x/v6/clip_v6.mp4",
  "name": "clip_v6.mp4",
  "duration": 40.0,
  "fps": 30.0,
  "width": 1920,
  "height": 1080
 },
 "display": {
  "show_within": 1.5
 },
 "versions": [
  {
   "label": "v5",
   "path": "/x/v5/clip_v5.mp4",
   "duration": 40.0
  },
  {
   "label": "v6",
   "path": "/x/v6/clip_v6.mp4",
   "duration": 40.0
  },
  {
   "label": "v7",
   "path": "/x/v7/c&d_v7.mp4",
   "duration": null
  }
 ],
 "author": {
  "name": "Tester"
 }
}`
	if got := readFile(t, filepath.Join(dir, "session.json")); got != wantSession {
		t.Errorf("session.json:\n%s\nwant:\n%s", got, wantSession)
	}
	for name, want := range map[string]string{
		"segments.json": "[\n {\n  \"id\": \"S1\",\n  \"t_start\": 0.5,\n  \"t_end\": 3,\n  \"note\": \"é\"\n }\n]",
		"events.json":   "[\n {\n  \"session_t\": 0,\n  \"video_t\": 1.25,\n  \"event\": \"start\"\n }\n]",
		"strokes.json":  "[\n {\n  \"id\": 1,\n  \"points\": [\n   [\n    0.1,\n    0.2\n   ]\n  ]\n }\n]",
		"voice.json":    "{\n \"bin\": 0.25,\n \"bins\": [\n  [\n   4,\n   0.5\n  ]\n ]\n}",
	} {
		if got := readFile(t, filepath.Join(dir, name)); got != want {
			t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
		}
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(tmps) != 0 {
		t.Errorf("temp files left: %v", tmps)
	}
}

func TestSaveDataDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
	}{
		{"empty body", ``},
		{"empty object", `{}`},
		{"falsy values", `{"session":null,"segments":0,"events":"","strokes":{},"voice":[]}`},
		{"utf-8 bom", "\xef\xbb\xbf{}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sf := fields()
			sf.Author = nil
			if err := SaveData(dir, []byte(tc.body), sf); err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"segments.json": "[]", "events.json": "[]", "strokes.json": "[]", "voice.json": "{}"} {
				if got := readFile(t, filepath.Join(dir, name)); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			var meta map[string]any
			if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, "session.json"))), &meta); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(meta))
			for k := range meta {
				keys = append(keys, k)
			}
			if len(keys) != 3 || meta["version"] != "v6" || meta["video"] == nil || meta["versions"] == nil {
				t.Errorf("session.json = %v", meta)
			}
		})
	}
}

func TestSaveDataBadJSONWritesNothing(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `"x"`, `{"a":1} trailing`, `{"session":[1]}`, "{\"a\":\"\xff\"}", `   `} {
		dir := filepath.Join(t.TempDir(), "take")
		err := SaveData(dir, []byte(body), fields())
		if !errors.Is(err, ErrBadJSON) {
			t.Errorf("SaveData(%q) = %v, want ErrBadJSON", body, err)
		}
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("SaveData(%q) created the take folder", body)
		}
	}
}

func TestWriteFileAtomicReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "segments.json")
	writeFile(t, p, "old")
	// A hard link keeps pointing at the old file: an in-place rewrite would change it, a rename doesn't.
	link := filepath.Join(dir, "old-link")
	if err := os.Link(p, link); err != nil {
		t.Skipf("no hard links here: %v", err)
	}
	if err := WriteFileAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "new" {
		t.Errorf("file = %q", got)
	}
	if got := readFile(t, link); got != "old" {
		t.Errorf("the old file was rewritten in place (link = %q)", got)
	}
	if _, err := os.Stat(p + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Error("temp file left behind")
	}
}

func TestWriteAudio(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seg_01.webm")
	steps := []struct {
		offset   int64
		body     string
		wantSize int64
		wantErr  int64 // -1: no error; else the 409 size
		wantFile string
	}{
		{0, "abc", 3, -1, "abc"},
		{3, "def", 6, -1, "abcdef"},
		{6, "", 6, -1, "abcdef"},
		{7, "x", 0, 6, "abcdef"},  // past the end: 409 with the size to resume from
		{-1, "x", 0, 6, "abcdef"}, // negative: same
		{4, "XY", 6, -1, "abcdXY"},
		{2, "Z", 3, -1, "abZ"}, // a resent chunk truncates what came after it
		{0, "", 0, -1, ""},
	}
	for i, s := range steps {
		size, err := WriteAudio(p, s.offset, []byte(s.body))
		var oe *OffsetError
		switch {
		case s.wantErr >= 0:
			if !errors.As(err, &oe) || oe.Size != s.wantErr {
				t.Fatalf("step %d: err = %v, want offset error with size %d", i, err, s.wantErr)
			}
		case err != nil:
			t.Fatalf("step %d: %v", i, err)
		case size != s.wantSize:
			t.Fatalf("step %d: size = %d, want %d", i, size, s.wantSize)
		}
		if got := readFile(t, p); got != s.wantFile {
			t.Fatalf("step %d: file = %q, want %q", i, got, s.wantFile)
		}
	}
	// A missing file has size 0: offset 0 creates it, anything else is past the end.
	q := filepath.Join(t.TempDir(), "seg_02.webm")
	if _, err := WriteAudio(q, 5, []byte("x")); err == nil {
		t.Error("offset 5 on a missing file succeeded")
	}
	if _, err := os.Stat(q); !errors.Is(err, os.ErrNotExist) {
		t.Error("a 409 created the file")
	}
}

func TestSessions(t *testing.T) {
	root := t.TempDir()
	video := filepath.Join(root, "clip_v6.mp4")
	rd := ReviewDir(video)
	// From segments.json, with notes.
	writeFile(t, filepath.Join(rd, "2026-01-01_10-00-00", "segments.json"), `[{"t_start":1.5,"t_end":4},{"t_start":10,"t_end":12.25}]`)
	writeFile(t, filepath.Join(rd, "2026-01-01_10-00-00", "strokes.json"), `[{},{},{}]`)
	writeFile(t, filepath.Join(rd, "2026-01-01_10-00-00", "session.json"), `{"saved":"2026-01-01T10:05:00Z"}`)
	writeFile(t, filepath.Join(rd, "2026-01-01_10-00-00", "notes.md"), "# notes")
	// No segments.json: falls back to session.segments.
	writeFile(t, filepath.Join(rd, "2026-01-02_10-00-00", "session.json"), `{"segments":[{"t_start":0,"t_end":2}],"saved":1}`)
	// No segments at all: session.rec.duration.
	writeFile(t, filepath.Join(rd, "2026-01-03_10-00-00", "session.json"), `{"rec":{"duration":7.5}}`)
	writeFile(t, filepath.Join(rd, "2026-01-03_10-00-00", "segments.json"), `[]`)
	// Empty take folder, a broken file, and folders that aren't takes.
	writeFile(t, filepath.Join(rd, "2026-01-04_10-00-00", "strokes.json"), `[{`)
	writeFile(t, filepath.Join(rd, "not-a-take", "segments.json"), `[]`)
	writeFile(t, filepath.Join(rd, "2026-01-05_10-00-00"), "a file, not a folder")
	writeFile(t, filepath.Join(rd, "inherit.json"), `{}`)

	got, err := json.Marshal(Sessions(video))
	if err != nil {
		t.Fatal(err)
	}
	want := `[` +
		`{"id":"2026-01-01_10-00-00","narration":4.75,"segments":2,"strokes":3,"notes":true,"saved":"2026-01-01T10:05:00Z"},` +
		`{"id":"2026-01-02_10-00-00","narration":2,"segments":1,"strokes":0,"notes":false,"saved":1},` +
		`{"id":"2026-01-03_10-00-00","narration":7.5,"segments":0,"strokes":0,"notes":false,"saved":null},` +
		`{"id":"2026-01-04_10-00-00","narration":null,"segments":0,"strokes":0,"notes":false,"saved":null}]`
	if string(got) != want {
		t.Errorf("sessions:\n%s\nwant:\n%s", got, want)
	}
	if s := Sessions(filepath.Join(root, "none.mp4")); s == nil || len(s) != 0 {
		t.Errorf("no review folder: %v", s)
	}
}

func TestSumTalkKeepsPythonNumberKind(t *testing.T) {
	for _, tc := range []struct{ segs, want string }{
		{`[{"t_start":1,"t_end":4}]`, "3"},
		{`[{"t_start":1,"t_end":4.0}]`, "3.0"},
		{`[{"t_start":0.5,"t_end":3}]`, "2.5"},
		{`[{"t_end":2},{"t_start":null,"t_end":1}]`, "3"},
		{`[{"t_start":1}]`, "-1"},
	} {
		var segs []json.RawMessage
		if err := json.Unmarshal([]byte(tc.segs), &segs); err != nil {
			t.Fatal(err)
		}
		if got := string(sumTalk(segs)); got != tc.want {
			t.Errorf("sumTalk(%s) = %s, want %s", tc.segs, got, tc.want)
		}
	}
}

func TestHistory(t *testing.T) {
	root := t.TempDir()
	v6 := filepath.Join(root, "v6", "clip_v6.mp4")
	v3 := filepath.Join(root, "v3", "clip_v3.mp4")
	own := filepath.Join(ReviewDir(v6), "2026-02-01_10-00-00")
	writeFile(t, filepath.Join(own, "strokes.json"),
		`[{"video_t":5,"color":"green","points":[[0.1,0.1]],"extra":1},{"video_t":50,"points":null},"junk"]`)
	writeFile(t, filepath.Join(own, "voice.json"), `{"bin":0.25,"bins":[[0,1],[400,1]],"other":true}`)

	carried := filepath.Join(ReviewDir(v3), "2026-01-01_10-00-00")
	writeFile(t, filepath.Join(carried, "strokes.json"),
		`[{"video_t":1,"color":"red","points":[[0.5,0.5]]},{"video_t":30,"color":"white","points":[]},{"video_t":null}]`)
	writeFile(t, filepath.Join(carried, "voice.json"), `{"bin":0.5,"bins":[[1,0.2],[20,0.4],[100,0.9],[3]]}`)
	everything := filepath.Join(ReviewDir(v3), "2026-01-02_10-00-00")
	writeFile(t, filepath.Join(everything, "strokes.json"), `[{"video_t":999,"color":"yellow","points":[[0,0]]}]`)
	relative := filepath.Join(ReviewDir(v6), "carried", "2026-01-03_10-00-00")
	writeFile(t, filepath.Join(relative, "strokes.json"), `[]`)

	inherit := map[string]any{"takes": []any{
		map[string]any{"dir": carried, "version": "v3", "ranges": [][]float64{{0, 2}, {9, 11}}, "note": "x"},
		map[string]any{"dir": filepath.Join(root, "gone", "2026-01-01_00-00-00"), "version": "v2"},
		map[string]any{"dir": everything, "version": "v3"},
		map[string]any{"dir": filepath.Join("carried", "2026-01-03_10-00-00"), "version": "v4", "ranges": nil},
		map[string]any{"version": "no dir"},
	}}
	b, err := json.Marshal(inherit)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ReviewDir(v6), "inherit.json"), string(b))

	got, err := json.Marshal(History(v6))
	if err != nil {
		t.Fatal(err)
	}
	want := `[` +
		`{"id":"2026-02-01_10-00-00","inherited_from":null,"voice":{"bin":0.25,"bins":[[0,1],[400,1]],"other":true},` +
		`"strokes":[{"video_t":5,"color":"green","points":[[0.1,0.1]]},{"video_t":50,"color":"red","points":[]}]},` +
		`{"id":"2026-01-01_10-00-00","inherited_from":"v3","voice":{"bin":0.5,"bins":[[1,0.2],[20,0.4]]},` +
		`"strokes":[{"video_t":1,"color":"red","points":[[0.5,0.5]]}]},` +
		`{"id":"2026-01-02_10-00-00","inherited_from":"v3","voice":{"bin":0.25,"bins":[]},` +
		`"strokes":[{"video_t":999,"color":"yellow","points":[[0,0]]}]},` +
		`{"id":"2026-01-03_10-00-00","inherited_from":"v4","voice":{"bin":0.25,"bins":[]},"strokes":[]}]`
	if string(got) != want {
		t.Errorf("history:\n%s\nwant:\n%s", got, want)
	}

	takes := InheritedTakes(v6)
	if len(takes) != 3 || !reflect.DeepEqual(takes[1].Ranges, defaultRanges) {
		t.Errorf("inherited takes = %+v", takes)
	}
	if h := History(filepath.Join(root, "none.mp4")); h == nil || len(h) != 0 {
		t.Errorf("no review folder: %v", h)
	}
	if !strings.HasSuffix(takes[2].Dir, filepath.Join("clip_v6.review", "carried", "2026-01-03_10-00-00")) {
		t.Errorf("relative dir resolved to %s", takes[2].Dir)
	}
}
