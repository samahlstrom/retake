package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/testmedia"
)

// copyFixture copies a notes golden-test case (internal/notes/testdata/<name>) into a temp folder.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "notes", "testdata", name)
	root := testmedia.TempDir(t) // as retake resolves it, so the paths it writes compare equal
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "golden" {
			return filepath.SkipDir
		}
		dst := filepath.Join(root, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, bytes.ReplaceAll(b, []byte("@ROOT@"), []byte(root)), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func runNotes(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Main(context.Background(), append([]string{"notes"}, args...), Env{Stdout: &out, Stderr: &errb,
		Getenv: func(string) string { return "" }, Prober: fakeProber{}})
	return code, out.String(), errb.String()
}

func TestNotesBuild(t *testing.T) {
	root := copyFixture(t, "scrub")
	rd := filepath.Join(root, "clip_v6.review")
	code, out, errs := runNotes(t, "build", rd, "--window", "3", "--max-span=6")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := filepath.Join(rd, "notes.md") + "  (v6: 5 moments, 12 comments, 5 segments, 0 flags, 1 take)\n"
	if out != want {
		t.Errorf("stdout %q, want %q", out, want)
	}
	if !strings.Contains(errs, "video not found") {
		t.Errorf("stderr %q", errs)
	}
	for _, f := range []string{"notes.md", "notes.json"} {
		if _, err := os.Stat(filepath.Join(rd, f)); err != nil {
			t.Error(err)
		}
	}

	// --json, flags before the target, and a take folder as the target.
	take := filepath.Join(rd, "2026-02-01_09-00-00")
	code, out, errs = runNotes(t, "build", "--json", take)
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var s struct {
		NotesMD       string            `json:"notes_md"`
		Version       string            `json:"version"`
		Moments       int               `json:"moments"`
		Comments      int               `json:"comments"`
		Takes         int               `json:"takes"`
		Untranscribed []json.RawMessage `json:"untranscribed"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("--json: %v\n%s", err, out)
	}
	if s.NotesMD != filepath.Join(take, "notes.md") || s.Version != "v6" || s.Takes != 1 || s.Comments != 12 ||
		s.Untranscribed == nil {
		t.Errorf("--json: %+v", s)
	}
}

func TestNotesBuildVideoAndUntranscribed(t *testing.T) {
	root := copyFixture(t, "flip")
	video := filepath.Join(root, "clip_v5.mp4")
	if err := os.WriteFile(video, []byte("not really a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "clip_v5.review", "2026-03-03_15-00-00", "transcript.json")); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runNotes(t, "build", video, "--json")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, errs)
	}
	var s struct {
		Untranscribed []struct{ Take, Seg, File string } `json:"untranscribed"`
	}
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Untranscribed) != 4 || s.Untranscribed[0].File != "seg_01.webm" {
		t.Errorf("untranscribed %+v", s.Untranscribed)
	}
	if !strings.Contains(errs, "seg_01.webm has no transcript yet") {
		t.Errorf("stderr %q", errs)
	}
	b, err := os.ReadFile(filepath.Join(root, "clip_v5.review", "notes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Video struct {
			Path     string  `json:"path"`
			Duration float64 `json:"duration"`
		} `json:"video"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Video.Path != video || doc.Video.Duration != 10 {
		t.Errorf("video %+v: the opened file, probed", doc.Video)
	}
}

func TestNotesBuildErrors(t *testing.T) {
	empty := t.TempDir()
	cases := []struct {
		args []string
		code int
		msg  string
	}{
		{nil, ExitOK, ""},
		{[]string{"build", "--help"}, ExitOK, ""},
		{[]string{"frobnicate"}, ExitUsage, "unknown subcommand"},
		{[]string{"build"}, ExitUsage, "missing the video"},
		{[]string{"build", empty, "extra"}, ExitUsage, "unexpected argument"},
		{[]string{"build", empty, "--window"}, ExitUsage, "--window needs a value"},
		{[]string{"build", empty, "--window", "soon"}, ExitUsage, "not a positive number"},
		{[]string{"build", empty, "--max-span", "0"}, ExitUsage, "not a positive number"},
		{[]string{"build", empty, "--print"}, ExitUsage, "unknown option --print"},
		{[]string{"build", filepath.Join(empty, "nope.mp4")}, ExitUsage, "no such file"},
		{[]string{"build", empty}, ExitUsage, "no review takes"},
	}
	for _, c := range cases {
		code, out, errs := runNotes(t, c.args...)
		if code != c.code || !strings.Contains(errs, c.msg) {
			t.Errorf("notes %v: exit %d, stderr %q; want %d, %q", c.args, code, errs, c.code, c.msg)
		}
		if c.code == ExitOK && !strings.Contains(out, "retake notes build") {
			t.Errorf("notes %v: no usage: %q", c.args, out)
		}
	}
}
