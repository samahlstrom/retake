package bridge

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jsonx"
)

// PatchRef is a patch as a rendered push lists it.
type PatchRef struct {
	ID      string   `json:"id"`
	Rev     int64    `json:"rev"`
	Answers []string `json:"answers,omitempty"`
	Notes   []int64  `json:"notes,omitempty"` // the note ids its answers name
	Summary string   `json:"summary"`
}

// Skipped is a patch answer left out of the changelog, and why.
type Skipped struct {
	Patch  string `json:"patch"`
	Answer string `json:"answer"`
	Why    string `json:"why"`
}

// Units counts a render's units.
type Units struct {
	Total    int `json:"total"`
	Reused   int `json:"reused"`
	Rendered int `json:"rendered"`
}

// PushRecord is <stem>.push.json beside a rendered version (spec/bridge.md, "Push with render").
type PushRecord struct {
	Version     string     `json:"version"`
	From        string     `json:"from"`
	Rev         int64      `json:"rev"`
	Timeline    string     `json:"timeline"`
	Draft       bool       `json:"draft"`
	Patches     []PatchRef `json:"patches"`
	Unrequested []PatchRef `json:"unrequested"`
	Skipped     []Skipped  `json:"skipped"`
	Units       Units      `json:"units"`
	// Stream is a streamed push's render (spec/bridge.md, "Streaming"): how to render it, and how far it is.
	Stream *family.StreamRecord `json:"stream,omitempty"`
}

// NoteChange is one note's changelog entry from patches: their summaries and ids.
type NoteChange struct {
	Note    int64
	Summary string
	Patches []string
}

// Changelog picks the patches of a new version (the log's entries with base < rev <= upTo; base < 0: all up to
// upTo) and turns their answers into one changelog entry per note, in the order notes are first answered.
// open(id) reports whether a note may be answered, else why not; those answers are skipped. Patches without answers
// are unrequested.
func Changelog(log []Patch, base, upTo int64, open func(id int64) (bool, string)) ([]NoteChange, PushRecord) {
	rec := PushRecord{Patches: []PatchRef{}, Unrequested: []PatchRef{}, Skipped: []Skipped{}}
	var out []NoteChange
	idx := map[int64]int{}
	for _, p := range log {
		if p.Rev <= base || p.Rev > upTo {
			continue
		}
		ref := PatchRef{ID: p.ID, Rev: p.Rev, Answers: p.Answers, Summary: strings.TrimSpace(p.Summary)}
		if len(p.Answers) == 0 {
			rec.Unrequested = append(rec.Unrequested, ref)
			continue
		}
		for _, a := range p.Answers {
			id, ok := NoteID(a)
			if !ok {
				rec.Skipped = append(rec.Skipped, Skipped{Patch: p.ID, Answer: a, Why: "not a note id (use #5 or v7#5)"})
				continue
			}
			if ok, why := open(id); !ok {
				rec.Skipped = append(rec.Skipped, Skipped{Patch: p.ID, Answer: a, Why: why})
				continue
			}
			ref.Notes = append(ref.Notes, id)
			i, seen := idx[id]
			if !seen {
				idx[id] = len(out)
				out = append(out, NoteChange{Note: id})
				i = len(out) - 1
			}
			c := &out[i]
			if ref.Summary != "" && !strings.Contains(c.Summary, ref.Summary) {
				if c.Summary != "" {
					c.Summary += "; "
				}
				c.Summary += ref.Summary
			}
			c.Patches = append(c.Patches, p.ID)
		}
		rec.Patches = append(rec.Patches, ref)
	}
	for i := range out {
		if out[i].Summary == "" {
			out[i].Summary = "changed by " + strings.Join(out[i].Patches, ", ")
		}
	}
	return out, rec
}

// ReadPushRecord reads a version's push record; nil when it has none.
func ReadPushRecord(video string) (*PushRecord, error) {
	b, err := os.ReadFile(PushRecordPath(video))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r PushRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// WritePushRecord writes a version's push record beside it, whole (a temporary file renamed over it).
func WritePushRecord(video string, rec PushRecord) error {
	b, err := jsonx.Marshal(rec)
	if err != nil {
		return err
	}
	b, err = jsonx.Indent1(b)
	if err != nil {
		return err
	}
	return writeAtomic(PushRecordPath(video), b)
}

// writeAtomic writes path whole: a temporary file beside it, renamed over it.
func writeAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil { //nolint:gosec // a record others may read
		_ = os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}
