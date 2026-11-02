package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// TypedNote is a typed comment to add (spec/notes.md, "Typed comments on a video", and spec/extensions.md,
// "Comments and questions on an extension's version"). On an extension's version it has an anchor and maybe wants;
// on a video (Video) its time, its pen lines and maybe a still of its moment.
type TypedNote struct {
	Version string
	Text    string
	Author  string
	Anchor  *Anchor
	Wants   string

	Video   bool
	At      float64  // seconds on Version
	Strokes []Stroke // normalized 0-1 to the frame; points already at most MaxStrokePoints
	// Snapshot is the still of its moment with its lines drawn (JPEG or PNG bytes, checked by the caller), kept as
	// .retake/typed/doc-<n><SnapshotExt> in the project. Empty: none (the worker makes one when it needs it).
	Snapshot    []byte
	SnapshotExt string
}

// Limits of a typed comment on a video.
const (
	MaxTypedStrokes  = 50
	MaxTypedSnapshot = 8 << 20
)

// PenColors are the pen's colours, the words the agent reads.
var PenColors = []string{"red", "yellow", "green", "white"}

// check reports what's wrong with a typed comment.
func (in TypedNote) check() error {
	n := utf8.RuneCountInString(in.Text)
	switch {
	case n > MaxDocNote || n == 0 && (!in.Video || len(in.Strokes) == 0):
		if in.Video {
			return fmt.Errorf("a comment is up to %d characters, and needs words or a drawing", MaxDocNote)
		}
		return fmt.Errorf("a comment is 1 to %d characters", MaxDocNote)
	case in.Wants != "" && in.Wants != WantsVersion:
		return fmt.Errorf("wants is %q or nothing", WantsVersion)
	}
	if in.Anchor != nil {
		if in.Video {
			return errors.New("an anchor is for an extension's version; a comment on a video has its time")
		}
		if err := in.Anchor.Check(); err != nil {
			return err
		}
	}
	if !in.Video {
		return nil
	}
	if math.IsNaN(in.At) || math.IsInf(in.At, 0) || in.At < 0 {
		return errors.New("t is the comment's time in seconds, 0 or more")
	}
	if len(in.Strokes) > MaxTypedStrokes {
		return fmt.Errorf("a comment has at most %d lines", MaxTypedStrokes)
	}
	for i, st := range in.Strokes {
		if !slices.Contains(PenColors, st.Color) {
			return fmt.Errorf("line %d: color %q isn't red, yellow, green or white", i+1, st.Color)
		}
		if len(st.Points) == 0 || len(st.Points) > MaxStrokePoints {
			return fmt.Errorf("line %d: 1 to %d points", i+1, MaxStrokePoints)
		}
		for _, p := range st.Points {
			if !unit(p[0]) || !unit(p[1]) {
				return fmt.Errorf("line %d: points are 0 to 1 across the frame", i+1)
			}
		}
		if math.IsNaN(st.T) || math.IsInf(st.T, 0) || st.T < 0 {
			return fmt.Errorf("line %d: t is a time in seconds", i+1)
		}
	}
	if len(in.Snapshot) > MaxTypedSnapshot {
		return fmt.Errorf("the still is over %d MiB", MaxTypedSnapshot>>20)
	}
	if len(in.Snapshot) > 0 && in.SnapshotExt != ".jpg" && in.SnapshotExt != ".png" {
		return errors.New("the still is a JPEG or a PNG")
	}
	return nil
}

func unit(f float64) bool { return !math.IsNaN(f) && f >= 0 && f <= 1 }

// AddTyped adds a typed comment: an open note with the next doc/<n> key and no take. On an extension's version it has
// no times; on a video t0 = t1 = At, its lines and its still are in its detail, and it wants a version (only a new
// video version finishes it, as for any note of a take). A notes rebuild never retracts it.
func (s *Store) AddTyped(in TypedNote) (Note, error) {
	in.Text = strings.TrimSpace(in.Text)
	if in.Video {
		in.Wants = WantsVersion
	}
	if err := in.check(); err != nil {
		return Note{}, err
	}
	d := Detail{Anchor: in.Anchor, Wants: in.Wants}
	at := 0.0
	if in.Video {
		at = math.Round(in.At*1e3) / 1e3
		d.Strokes = in.Strokes
	}
	var out Note
	err := s.write(func(t *tx) error {
		var n int64
		if err := t.QueryRow(`SELECT COUNT(*) FROM notes WHERE key LIKE 'doc/%'`).Scan(&n); err != nil {
			return err
		}
		now := s.stamp()
		var id int64
		for k := n + 1; ; k++ {
			key := fmt.Sprintf("%s%d", DocKey, k)
			var used int
			if err := t.QueryRow(`SELECT COUNT(*) FROM notes WHERE key = ?`, key).Scan(&used); err != nil {
				return err
			}
			if used > 0 {
				continue
			}
			d.Snapshot = s.keepTypedStill(key, in.Snapshot, in.SnapshotExt)
			detail, err := json.Marshal(d)
			if err != nil {
				return err
			}
			if err := t.QueryRow(`INSERT INTO notes (key, version, take, seg, t0, t1, text, author, status, detail, created,
				updated) VALUES (?, ?, '', '', ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, key, in.Version, at, at, in.Text,
				in.Author, string(StatusOpen), string(detail), now, now).Scan(&id); err != nil {
				return err
			}
			break
		}
		t.emit(EventNotes, NotesEvent{Why: "add", Version: in.Version, Notes: []int64{id}, Opened: []int64{id}})
		var err error
		out, err = noteByID(t, id)
		return err
	})
	return out, err
}

// keepTypedStill writes a typed comment's still into the project (.retake/typed/doc-<n>.jpg) and returns its path
// relative to the root, or "" when there is none or it can't be written (the worker then makes one).
func (s *Store) keepTypedStill(key string, b []byte, ext string) string {
	if len(b) == 0 || s.root == "" {
		return ""
	}
	rel := filepath.Join(".retake", "typed", strings.ReplaceAll(key, "/", "-")+ext)
	abs := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		return ""
	}
	if err := os.WriteFile(abs, b, 0o644); err != nil { //nolint:gosec // a still the page shows, inside the project
		return ""
	}
	return filepath.ToSlash(rel)
}
