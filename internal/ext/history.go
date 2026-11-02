package ext

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"time"
)

// The history of the agent's edits to owned files (spec/scripts.md): before each agent attempt the owned files are
// copied, after it the copies of what changed are kept, and Undo puts one file back while nobody changed it since.
// Layout: <video>/.retake/history/<stamp>-<rand>/edit.json and files/<owned path>.
const (
	HistoryDir   = ".retake/history"
	MaxHistory   = 100      // edits kept: the newest
	maxSnapFile  = 1 << 20  // the largest file copied before an attempt
	maxSnapTotal = 16 << 20 // all the copies of one attempt
)

// Edit is one owned file an agent attempt changed: its sha256 before ("" = it didn't exist) and after ("" = it's
// gone).
type Edit struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	At     int64  `json:"at"` // unix ms, when the attempt ended
	Task   string `json:"task,omitempty"`
	Before string `json:"before"`
	After  string `json:"after"`
}

var editID = regexp.MustCompile(`^\d{8}-\d{6}\.\d{6}-[0-9a-f]{8}$`)

// Snap is the owned files as they were before an agent attempt.
type Snap struct {
	folder Folder
	task   string
	id     string
	before map[string]string // path -> sha256
	copied map[string]bool   // the paths copied into the snapshot
}

// Snapshot records the owned files of the video folder root before an agent attempt, and copies those of at most
// 1 MiB (16 MiB in all) into .retake/history/<id>/files/. Finish ends it.
func Snapshot(root string, owns []string, task string) (*Snap, error) {
	f := Folder{Root: root, Owns: owns}
	files, err := f.List("")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	s := &Snap{folder: f, task: task, before: map[string]string{}, copied: map[string]bool{},
		id: time.Now().UTC().Format("20060102-150405.000000") + "-" + hex.EncodeToString(b)}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	var total int64
	for _, fi := range files {
		s.before[fi.Path] = fi.SHA256
		if fi.Size > maxSnapFile || total+fi.Size > maxSnapTotal {
			continue
		}
		sum, n, err := copyIn(r, fi.Path, s.filesDir()+"/"+fi.Path)
		if err != nil {
			continue // it went or changed while copied: not undoable, the rest is
		}
		s.before[fi.Path] = sum
		s.copied[fi.Path] = true
		total += n
	}
	return s, nil
}

// SnapState is a snapshot as a worker's record keeps it, so a later worker can finish it (spec/automation.md, "Agents
// outlive Retake").
type SnapState struct {
	ID     string            `json:"id"`
	Task   string            `json:"task,omitempty"`
	Owns   []string          `json:"owns"`
	Before map[string]string `json:"before"`
	Copied []string          `json:"copied,omitempty"`
}

// State is s as a record keeps it.
func (s *Snap) State() SnapState {
	st := SnapState{ID: s.id, Task: s.task, Owns: s.folder.Owns, Before: s.before}
	for p := range s.copied {
		st.Copied = append(st.Copied, p)
	}
	sort.Strings(st.Copied)
	return st
}

// ResumeSnap is the snapshot st of the video folder root, to finish. nil when st isn't one.
func ResumeSnap(root string, st SnapState) *Snap {
	if !editID.MatchString(st.ID) {
		return nil
	}
	s := &Snap{folder: Folder{Root: root, Owns: st.Owns}, task: st.Task, id: st.ID, before: st.Before, copied: map[string]bool{}}
	if s.before == nil {
		s.before = map[string]string{}
	}
	for _, p := range st.Copied {
		s.copied[p] = true
	}
	return s
}

func (s *Snap) dir() string      { return HistoryDir + "/" + s.id }
func (s *Snap) filesDir() string { return s.dir() + "/files" }

// copyIn copies the file src of r to dst (made with its folders), and returns the sha256 and size it copied.
func copyIn(r *os.Root, src, dst string) (string, int64, error) {
	in, err := r.Open(filepath.FromSlash(src))
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = in.Close() }()
	if err := r.MkdirAll(filepath.FromSlash(path.Dir(dst)), 0o755); err != nil {
		return "", 0, err
	}
	out, err := r.OpenFile(filepath.FromSlash(dst), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// Finish compares the owned files with the snapshot. It keeps the copies of the files that changed or vanished (and
// notes the new ones) in edit.json, and returns those edits; when nothing changed it removes the snapshot. A changed
// file that was too large to copy isn't kept, since it can't be undone. Then it prunes the history to the newest
// MaxHistory edits.
func (s *Snap) Finish() ([]Edit, error) {
	if s == nil {
		return nil, nil
	}
	r, err := os.OpenRoot(s.folder.Root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	files, err := s.folder.List("")
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	after := map[string]string{}
	for _, fi := range files {
		after[fi.Path] = fi.SHA256
	}
	var edits []Edit
	keep := map[string]bool{}
	for _, p := range sortedPaths(s.before, after) {
		before, was := s.before[p]
		cur := after[p]
		switch {
		case was && before == cur:
			continue
		case was && !s.copied[p]:
			continue // too large to copy: can't be undone
		}
		if was {
			keep[p] = true
		}
		edits = append(edits, Edit{ID: s.id, Path: p, At: now, Task: s.task, Before: before, After: cur})
	}
	for p := range s.copied {
		if !keep[p] {
			_ = r.Remove(filepath.FromSlash(s.filesDir() + "/" + p))
		}
	}
	if len(edits) == 0 {
		_ = r.RemoveAll(filepath.FromSlash(s.dir()))
		return nil, nil
	}
	if err := writeEdits(r, s.id, edits); err != nil {
		return nil, err
	}
	pruneHistory(r)
	return edits, nil
}

// sortedPaths is the paths of both maps, sorted.
func sortedPaths(a, b map[string]string) []string {
	var out []string
	for p := range a {
		out = append(out, p)
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func writeEdits(r *os.Root, id string, edits []Edit) error {
	dir := HistoryDir + "/" + id
	if err := r.MkdirAll(filepath.FromSlash(dir), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(edits, "", "  ")
	if err != nil {
		return err
	}
	tmp := dir + "/.edit.json.tmp"
	if err := r.WriteFile(filepath.FromSlash(tmp), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return r.Rename(filepath.FromSlash(tmp), filepath.FromSlash(dir+"/edit.json"))
}

func readEdits(r *os.Root, id string) ([]Edit, error) {
	b, err := r.ReadFile(filepath.FromSlash(HistoryDir + "/" + id + "/edit.json"))
	if err != nil {
		return nil, err
	}
	var edits []Edit
	if err := json.Unmarshal(b, &edits); err != nil {
		return nil, fmt.Errorf("history %s: %w", id, err)
	}
	return edits, nil
}

// historyIDs is the edits' folder names, newest first.
func historyIDs(r *os.Root) []string {
	d, err := r.Open(filepath.FromSlash(HistoryDir))
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil
	}
	var ids []string
	for _, en := range entries {
		if en.IsDir() && editID.MatchString(en.Name()) {
			ids = append(ids, en.Name())
		}
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	return ids
}

// pruneHistory removes all but the newest MaxHistory edits.
func pruneHistory(r *os.Root) {
	ids := historyIDs(r)
	for _, id := range ids[min(len(ids), MaxHistory):] {
		_ = r.RemoveAll(filepath.FromSlash(HistoryDir + "/" + id))
	}
}

// History is the agent's edits of the folder's owned files, newest first; path narrows them to one file ("" = all).
func (f Folder) History(p string) ([]Edit, error) {
	out := []Edit{}
	if p != "" && !f.owned(p) {
		return out, ErrNotOwned
	}
	r, err := os.OpenRoot(f.Root)
	if err != nil {
		return out, ErrNotFound
	}
	defer func() { _ = r.Close() }()
	for _, id := range historyIDs(r) {
		edits, err := readEdits(r, id)
		if err != nil {
			continue
		}
		for _, e := range edits {
			if (p == "" && f.owned(e.Path)) || e.Path == p {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

// Undo puts back the file path as it was before the edit id: its copy, or no file when the edit made it. It does so
// only while the file is still as that edit left it (else a *ChangedError with its sha now), and then drops the edit.
func (f Folder) Undo(id, p string) (FileInfo, error) {
	if !editID.MatchString(id) {
		return FileInfo{}, ErrNotFound
	}
	if !f.owned(p) {
		return FileInfo{}, ErrNotOwned
	}
	r, err := os.OpenRoot(f.Root)
	if err != nil {
		return FileInfo{}, ErrNotFound
	}
	defer func() { _ = r.Close() }()
	edits, err := readEdits(r, id)
	if err != nil {
		return FileInfo{}, ErrNotFound
	}
	i := slices.IndexFunc(edits, func(e Edit) bool { return e.Path == p })
	if i < 0 {
		return FileInfo{}, ErrNotFound
	}
	e := edits[i]
	copyPath := HistoryDir + "/" + id + "/files/" + p
	var info FileInfo
	if e.Before == "" {
		err = f.Remove(p, matchFor(e.After))
		if errors.Is(err, ErrNotFound) {
			err = &ChangedError{} // it was made by the edit and is gone since
		}
		info = FileInfo{Path: p, Deleted: true}
	} else {
		var b []byte
		b, err = r.ReadFile(filepath.FromSlash(copyPath))
		if err != nil {
			return FileInfo{}, fmt.Errorf("the copy from before isn't there: %w", err)
		}
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != e.Before {
			return FileInfo{}, errors.New("the copy from before doesn't match its sha256")
		}
		info, err = f.Write(p, bytes.NewReader(b), matchFor(e.After))
	}
	if err != nil {
		return FileInfo{}, err
	}
	edits = slices.Delete(edits, i, i+1)
	_ = r.Remove(filepath.FromSlash(copyPath))
	if len(edits) == 0 {
		_ = r.RemoveAll(filepath.FromSlash(HistoryDir + "/" + id))
	} else if err := writeEdits(r, id, edits); err != nil {
		return info, err
	}
	return info, nil
}

// matchFor is the If-Match that holds only for a file whose sha is after ("" = no file).
func matchFor(after string) string {
	if after == "" {
		return "*"
	}
	return after
}
