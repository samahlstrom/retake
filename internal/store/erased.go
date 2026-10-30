package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Erased lines (spec/take-format.md, "Erased lines"): the eraser can remove a line from an earlier take. Takes are
// never rewritten, so the erase is kept in <version>.review/erased.json, and history and the notes leave the line out.

// ErasedLine is one line removed by the eraser: a stroke of a take shown on the version.
type ErasedLine struct {
	Take string `json:"take"` // the take's folder name
	ID   int64  `json:"id"`   // the stroke's id in that take
	At   string `json:"at,omitempty"`
	By   string `json:"by,omitempty"`
}

type erasedFile struct {
	Erased []ErasedLine `json:"erased"`
}

// ErasedPath is the erased-lines file of a version's review folder.
func ErasedPath(reviewDir string) string { return filepath.Join(reviewDir, "erased.json") }

func readErasedFile(reviewDir string) erasedFile {
	var f erasedFile
	b, err := os.ReadFile(ErasedPath(reviewDir))
	if err == nil {
		_ = json.Unmarshal(b, &f) // a broken file counts as no erased lines
	}
	return f
}

// ReadErased returns the lines erased on a version: take folder → stroke id → true. None when the file is missing.
func ReadErased(reviewDir string) map[string]map[int64]bool {
	out := map[string]map[int64]bool{}
	for _, l := range readErasedFile(reviewDir).Erased {
		if out[l.Take] == nil {
			out[l.Take] = map[int64]bool{}
		}
		out[l.Take][l.ID] = true
	}
	return out
}

// SetErased adds (on) or removes (off) one erased line in reviewDir's erased.json, written atomically. The caller
// serializes writes.
func SetErased(reviewDir, take string, id int64, on bool, by string, now time.Time) error {
	f := readErasedFile(reviewDir)
	kept := f.Erased[:0]
	for _, l := range f.Erased {
		if l.Take != take || l.ID != id {
			kept = append(kept, l)
		}
	}
	f.Erased = kept
	if on {
		f.Erased = append(f.Erased, ErasedLine{Take: take, ID: id, At: now.Format(time.RFC3339), By: by})
	}
	if f.Erased == nil {
		f.Erased = []ErasedLine{}
	}
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(reviewDir, 0o755); err != nil { //nolint:gosec // the review folder, like a take's
		return err
	}
	return WriteFileAtomic(ErasedPath(reviewDir), append(b, '\n'))
}
