package worker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// A still or an image the server can't give, with no still to make in its place, is said in the note's context and
// leaves no stale file behind; nothing about it stops the task.
func TestNoteFilesSayWhatIsMissing(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	defer ts.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note-1.png"), []byte("an earlier attempt's"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &runner{opts: Options{URL: ts.URL, HTTP: ts.Client(), NoStill: true, Log: io.Discard, Root: t.TempDir()}}
	a := project.WorkAssignment{Task: &project.EditingTask{Notes: []int64{1, 2}}, Notes: []project.WorkNote{
		{Note: project.Note{ID: 1, Version: "v1", T0: 2}, Detail: project.Detail{Snapshot: "note-1.png",
			Strokes: []project.Stroke{{Shape: "circle", Color: "red"}}},
			Images: []project.TakeImage{{File: "img_01.png"}, {File: "../secret.png"}}},
		{Note: project.Note{ID: 2, Version: "v1", T0: 4}},
		{Note: project.Note{ID: 3, Version: "v1"}, Detail: project.Detail{Snapshot: "note-3.png"}}, // not assigned
	}}
	r.noteFiles(context.Background(), &a, dir)
	n := a.Notes[0]
	if n.Detail.Snapshot != "" || len(n.Missing) != 3 {
		t.Fatalf("note 1: %q %q", n.Detail.Snapshot, n.Missing)
	}
	for i, want := range []string{"No still of this note: its saved still couldn't be fetched (host returned 404), " +
		"and none could be made here (stills are turned off on this computer). Look at v1 at 2.00 s yourself.",
		"The attached image img_01.png couldn't be fetched", "invalid file name"} {
		if !strings.Contains(n.Missing[i], want) {
			t.Errorf("missing[%d] = %q, want %q", i, n.Missing[i], want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "note-1.png")); !os.IsNotExist(err) {
		t.Error("an earlier attempt's still is left as this note's")
	}
	if len(a.Notes[1].Missing) != 0 || len(a.Notes[2].Missing) != 0 || a.Notes[2].Detail.Snapshot != "note-3.png" {
		t.Errorf("a note without a drawing, or not assigned, changed: %+v", a.Notes[1:])
	}
}

func TestStillMomentAndLines(t *testing.T) {
	n := project.Note{T0: 4}
	old := []project.Stroke{{Shape: "circle", Color: "red", Box: [4]float64{0.2, 0.2, 0.4, 0.2}}, {Shape: "arrow", Box: [4]float64{0.1, 0.1, 0.1, 0.1}}}
	if at := stillTime(n, old); at != 4 {
		t.Errorf("strokes without their line: %v, want the note's start", at)
	}
	drawn := []project.Stroke{{Points: [][2]float64{{0, 0}}, T: 7}, {Points: [][2]float64{{0, 0}}, T: 5}, {Points: [][2]float64{{0, 0}}, T: 6}}
	if at := stillTime(n, drawn); at != 6 {
		t.Errorf("the middle of the lines' times: %v", at)
	}
	lines := linesOf(old)
	if len(lines) != 2 || lines[0].N != 1 || lines[1].N != 2 || lines[0].Color != "red" {
		t.Fatalf("lines %+v", lines)
	}
	for _, p := range lines[0].Points { // an ellipse inside the circle's box
		if p[0] < 0.2-1e-9 || p[0] > 0.6+1e-9 || p[1] < 0.2-1e-9 || p[1] > 0.4+1e-9 {
			t.Fatalf("point %v outside the box", p)
		}
	}
	if got := lines[1].Points; len(got) != 5 || got[0] != got[4] || got[2] != [2]float64{0.2, 0.2} {
		t.Errorf("a rectangle around the box: %v", got)
	}
	if got := linesOf(drawn)[0].Points; len(got) != 1 {
		t.Errorf("a stored line is drawn as it is: %v", got)
	}
}
