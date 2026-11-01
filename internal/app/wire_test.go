package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/notes"
)

// copyDir copies a fixture tree into a temp dir (the engine writes notes.md and notes.json next to the take).
func copyDir(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// The store run built from a real engine result keeps the engine's keys, turns answer_to into AnswerTo, converts
// stroke bboxes to [x, y, w, h] and keeps each stroke's line (at most 64 points, inside the frame) and time.
func TestRunFromResult(t *testing.T) {
	dir := copyDir(t, filepath.Join("..", "notes", "testdata", "answer"))
	res, err := notes.Run(context.Background(), filepath.Join(dir, "clip_v7.review", "2026-06-06_18-00-00"), notes.Options{})
	if err != nil {
		t.Fatal(err)
	}
	run := runFromResult("v7", res)
	if run.Version != "v7" || len(run.Comments) != len(res.Comments) || len(run.Comments) == 0 {
		t.Fatalf("run %q with %d comments, engine had %d", run.Version, len(run.Comments), len(res.Comments))
	}
	answers, lines := 0, 0
	for i, c := range run.Comments {
		e := res.Comments[i]
		if c.Key != e.Key || !strings.HasPrefix(c.Key, "2026-06-06_18-00-00/") || c.Take != "2026-06-06_18-00-00" {
			t.Errorf("comment %d: key %q take %q (engine key %q)", i, c.Key, c.Take, e.Key)
		}
		if c.AnswerTo != 0 {
			answers++
		}
		for _, s := range c.Detail.Strokes {
			if s.Box[2] < 0 || s.Box[3] < 0 || s.Box[0]+s.Box[2] > 1.001 || s.Box[1]+s.Box[3] > 1.001 {
				t.Errorf("comment %d: stroke box %v is not [x, y, w, h] inside the frame", i, s.Box)
			}
			if len(s.Points) > 0 {
				lines++
			}
			if len(s.Points) > 64 || s.T < 0 {
				t.Errorf("comment %d: stroke with %d points at %v", i, len(s.Points), s.T)
			}
			for _, p := range s.Points {
				if p[0] < 0 || p[0] > 1 || p[1] < 0 || p[1] > 1 {
					t.Errorf("comment %d: point %v outside the frame", i, p)
				}
			}
		}
	}
	if lines == 0 {
		t.Error("no stroke kept its line")
	}
	if answers == 0 {
		t.Error("the answer fixture produced no comment with AnswerTo")
	}
}
