package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Stroke is one pen stroke of a note, as the notes engine describes it.
type Stroke struct {
	Shape string     `json:"shape"` // as the engine names it: circle, arrow, line, ...
	Color string     `json:"color"`
	Box   [4]float64 `json:"box"` // [x, y, w, h], normalized 0-1 to the frame
	// Points is the line as drawn, normalized 0-1 to the frame (at most MaxStrokePoints, evenly picked), and T the
	// video time of the frame it was drawn on. Notes stored before they were kept have neither.
	Points [][2]float64 `json:"points,omitempty"`
	T      float64      `json:"t,omitempty"`
}

// MaxStrokePoints is the most points a stored stroke keeps.
const MaxStrokePoints = 64

// FewerPoints picks at most MaxStrokePoints of pts, evenly, the first and the last included, each rounded to 4
// decimals.
func FewerPoints(pts [][2]float64) [][2]float64 {
	if len(pts) == 0 {
		return nil
	}
	n := min(len(pts), MaxStrokePoints)
	out := make([][2]float64, n)
	for i := range out {
		j := 0
		if n > 1 {
			j = i * (len(pts) - 1) / (n - 1)
		}
		out[i] = [2]float64{round4(pts[j][0]), round4(pts[j][1])}
	}
	return out
}

func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// Detail is what the notes engine knows about a note beyond its text, kept for the agent.
type Detail struct {
	Strokes  []Stroke `json:"strokes,omitempty"`  // the strokes drawn while it was said
	Snapshot string   `json:"snapshot,omitempty"` // a still of the frame with the strokes, relative to the root
	Audio    bool     `json:"audio,omitempty"`    // an (audio) note: about sound, not the picture
	// Targets are the timeline elements under the note, best first, from its version's render manifest
	// (spec/bridge.md); Unit is the manifest unit at t0 and Word the spine word there. Empty without a manifest.
	Targets []Target `json:"targets,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Word    *Word    `json:"word,omitempty"`
}

// Target is a timeline element under a note (spec/bridge.md, "Notes resolve to elements").
type Target struct {
	ID     string      `json:"id"`
	Kind   string      `json:"kind"`
	Ptr    string      `json:"ptr"`
	Score  float64     `json:"score"`
	Z      int64       `json:"z"`
	Box    *[4]float64 `json:"box,omitempty"`
	Parent string      `json:"parent,omitempty"`
}

// Word is a spine word of the edit: [t0, t1) in output seconds.
type Word struct {
	T0   float64 `json:"t0"`
	T1   float64 `json:"t1"`
	Text string  `json:"text"`
}

// Comment is one comment of a notes engine run: the part of the engine's result that a sync needs.
type Comment struct {
	// Key is the engine's stable key, <take folder>/<segment id>/<n> with n from 1. A comment carried over to
	// other versions keeps it. "" = built from Take, Seg and N.
	Key     string
	Take    string  // the take id (YYYY-MM-DD_HH-MM-SS)
	Seg     string  // the segment's own id within the take (segments.json "id", e.g. "S2"); images.json uses it too
	N       int     // the comment's index within that segment (when Key is empty)
	Version string  // the label of the version the take was recorded on; "" = the run's version
	T0, T1  float64 // the video time it covers, in seconds
	Text    string
	Author  string
	// Retracted is set when the engine flags a retraction ("scrap that") that covers this comment.
	Retracted bool
	// AnswerTo is the question thread this comment answers (segments.json "answer_to"); 0 = an ordinary note.
	// An answer is stored as an answer thread, not as a note.
	AnswerTo int64
	Detail   Detail
}

// NoteKey returns the comment's stable note key: Key, else <take>/<seg>/<n>.
func (c Comment) NoteKey() string {
	if c.Key != "" {
		return c.Key
	}
	return fmt.Sprintf("%s/%s/%d", c.Take, c.Seg, c.N)
}

// Run is the result of one notes engine run for a version: every comment of its takes, its inherited ones
// included.
type Run struct {
	Version  string
	Comments []Comment
}

// Note is one row of the notes table.
type Note struct {
	ID         int64   `json:"id"`
	Key        string  `json:"key"`
	Version    string  `json:"version"`
	Take       string  `json:"take"`
	Seg        string  `json:"seg"`
	T0         float64 `json:"t0"`
	T1         float64 `json:"t1"`
	Text       string  `json:"text"`
	Author     string  `json:"author"`
	Status     Status  `json:"status"`
	AnsweredIn string  `json:"answered_in,omitempty"`
	Change     string  `json:"change,omitempty"`
	Detail     Detail  `json:"-"`
	Created    string  `json:"created"`
	Updated    string  `json:"updated"`
}

// ErrNotFound is returned for an unknown note, thread or version.
var ErrNotFound = errors.New("not found")

const noteCols = `id, key, version, take, seg, t0, t1, text, author, status, answered_in, change, detail, created, updated`

type scanner interface{ Scan(dest ...any) error }

func scanNote(r scanner) (Note, error) {
	var n Note
	var detail string
	err := r.Scan(&n.ID, &n.Key, &n.Version, &n.Take, &n.Seg, &n.T0, &n.T1, &n.Text, &n.Author, &n.Status,
		&n.AnsweredIn, &n.Change, &detail, &n.Created, &n.Updated)
	if err != nil {
		return n, err
	}
	_ = json.Unmarshal([]byte(detail), &n.Detail) // a malformed detail just has no strokes
	return n, nil
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func queryNotes(q querier, where string, args ...any) ([]Note, error) {
	rows, err := q.Query(`SELECT `+noteCols+` FROM notes `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func noteByID(q querier, id int64) (Note, error) {
	n, err := scanNote(q.QueryRow(`SELECT `+noteCols+` FROM notes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, fmt.Errorf("note #%d: %w", id, ErrNotFound)
	}
	return n, err
}

// Note returns one note.
func (s *Store) Note(id int64) (Note, error) { return noteByID(s.db, id) }

// Carried is a take that inherit.json carries over onto a version, with the video time ranges it keeps.
type Carried struct {
	Take   string
	Ranges [][2]float64
}

// Scope is the set of notes that are on a version: the ones recorded on it, plus the ones of takes it inherits
// (inside their ranges).
type Scope struct {
	Version string
	Carried []Carried
}

// Has reports whether a note is in the scope.
func (sc Scope) Has(n Note) bool {
	if n.Version == sc.Version {
		return true
	}
	for _, c := range sc.Carried {
		if c.Take == n.Take && inRanges(n.T0, c.Ranges) {
			return true
		}
	}
	return false
}

func inRanges(t float64, ranges [][2]float64) bool {
	for _, r := range ranges {
		if r[0] <= t && t <= r[1] {
			return true
		}
	}
	return false
}

// NoteFilter selects notes. Zero fields don't filter.
type NoteFilter struct {
	Statuses []Status
	Scope    *Scope // only the notes on this version
	IDs      []int64
	Takes    []string
}

// Notes returns the notes that match f, by id.
func (s *Store) Notes(f NoteFilter) ([]Note, error) { return filterNotes(s.db, f) }

func filterNotes(q querier, f NoteFilter) ([]Note, error) {
	var conds []string
	var args []any
	if len(f.Statuses) > 0 {
		conds = append(conds, "status IN ("+placeholders(len(f.Statuses))+")")
		for _, st := range f.Statuses {
			args = append(args, string(st))
		}
	}
	if len(f.IDs) > 0 {
		conds = append(conds, "id IN ("+placeholders(len(f.IDs))+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	if len(f.Takes) > 0 {
		conds = append(conds, "take IN ("+placeholders(len(f.Takes))+")")
		for _, take := range f.Takes {
			args = append(args, take)
		}
	}
	if f.Scope != nil {
		c := "version = ?"
		args = append(args, f.Scope.Version)
		if len(f.Scope.Carried) > 0 {
			c = "(" + c + " OR take IN (" + placeholders(len(f.Scope.Carried)) + "))"
			for _, x := range f.Scope.Carried {
				args = append(args, x.Take)
			}
		}
		conds = append(conds, c)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	notes, err := queryNotes(q, where+" ORDER BY id", args...)
	if err != nil || f.Scope == nil {
		return notes, err
	}
	out := notes[:0]
	for _, n := range notes {
		if f.Scope.Has(n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// SyncResult says what a sync changed.
type SyncResult struct {
	Added      []int64 // new notes
	Updated    []int64 // notes whose text, times or detail changed
	Retracted  []int64 // notes the engine retracted or no longer produces
	Reinstated []int64 // retracted notes that are back
	Answers    []int64 // new answer threads
	Reopened   []int64 // notes whose question was answered (question -> open)
}

func (r SyncResult) empty() bool {
	return len(r.Added)+len(r.Updated)+len(r.Retracted)+len(r.Reinstated)+len(r.Answers)+len(r.Reopened) == 0
}

// Sync stores a notes engine run. Notes keep their ids and states across runs (they are matched by key). New keys
// become open notes. A note of this version that is open and whose key the engine no longer produces, or that
// the engine flags as retracted, becomes retracted; it is never deleted, and it is reinstated (open again) when
// it comes back. Answered and accepted notes keep their state: they are history. A comment that answers a question
// becomes an answer thread, and its note goes from question back to open. One "notes" event describes the
// changes; a sync that changes nothing appends none.
func (s *Store) Sync(run Run) (SyncResult, error) {
	var res SyncResult
	err := s.write(func(t *tx) error {
		res = SyncResult{}
		ev := NotesEvent{Why: "sync", Version: run.Version}
		seen := map[string]bool{}
		for _, c := range run.Comments {
			if c.Version == "" {
				c.Version = run.Version
			}
			key := c.NoteKey()
			if seen[key] {
				continue // the engine gave a key twice: the first wins
			}
			seen[key] = true
			if c.AnswerTo > 0 {
				ok, err := t.syncAnswer(c, key, &res)
				if err != nil {
					return err
				}
				if ok {
					continue
				}
			}
			if err := t.syncNote(c, key, &res); err != nil {
				return err
			}
		}
		open, err := queryNotes(t, `WHERE version = ? AND status = ?`, run.Version, string(StatusOpen))
		if err != nil {
			return err
		}
		for _, n := range open {
			if seen[n.Key] || slices.Contains(res.Reopened, n.ID) {
				continue // still produced, or its question was just answered
			}
			if err := t.setStatus(n.ID, StatusRetracted); err != nil {
				return err
			}
			res.Retracted = append(res.Retracted, n.ID)
		}
		if res.empty() {
			return nil
		}
		ev.Notes = unique(res.Added, res.Updated, res.Retracted, res.Reinstated, res.Reopened)
		ev.Opened = unique(openOnly(res.Added, t), res.Reinstated, res.Reopened)
		ev.Answers = res.Answers
		t.emit(EventNotes, ev)
		if len(res.Answers) > 0 {
			return t.resumeAfterSync() // a task waiting on a recorded answer goes again
		}
		return nil
	})
	return res, err
}

// openOnly keeps the added notes that are open (an added note may be born retracted).
func openOnly(ids []int64, t *tx) []int64 {
	var out []int64
	for _, id := range ids {
		var st string
		if err := t.QueryRow(`SELECT status FROM notes WHERE id = ?`, id).Scan(&st); err == nil && Status(st) == StatusOpen {
			out = append(out, id)
		}
	}
	return out
}

func unique(lists ...[]int64) []int64 {
	var out []int64
	for _, l := range lists {
		for _, id := range l {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	slices.Sort(out)
	return out
}

func (t *tx) syncNote(c Comment, key string, res *SyncResult) error {
	c.Detail.Snapshot = t.s.Rel(c.Detail.Snapshot) // kept relative to the project root (spec/notes.md, "Paths")
	detail, err := json.Marshal(c.Detail)
	if err != nil {
		return err
	}
	now := t.s.stamp()
	old, err := scanNote(t.QueryRow(`SELECT `+noteCols+` FROM notes WHERE key = ?`, key))
	if errors.Is(err, sql.ErrNoRows) {
		status := StatusOpen
		if c.Retracted {
			status = StatusRetracted
		}
		var id int64
		if err := t.QueryRow(`INSERT INTO notes (key, version, take, seg, t0, t1, text, author, status, detail, created,
			updated) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			key, c.Version, c.Take, c.Seg, c.T0, c.T1, c.Text, c.Author, string(status), string(detail), now,
			now).Scan(&id); err != nil {
			return err
		}
		res.Added = append(res.Added, id)
		return nil
	}
	if err != nil {
		return err
	}
	var deleted int
	if err := t.QueryRow(`SELECT COUNT(*) FROM note_deletions WHERE note = ?`, old.ID).Scan(&deleted); err != nil {
		return err
	}
	if deleted > 0 {
		return nil // an explicit user deletion must survive final transcription and future rebuilds
	}
	oldDetail, _ := json.Marshal(old.Detail)
	if old.Text != c.Text || old.T0 != c.T0 || old.T1 != c.T1 || old.Author != c.Author ||
		string(oldDetail) != string(detail) {
		if _, err := t.Exec(`UPDATE notes SET text = ?, t0 = ?, t1 = ?, author = ?, detail = ?, updated = ? WHERE id = ?`,
			c.Text, c.T0, c.T1, c.Author, string(detail), now, old.ID); err != nil {
			return err
		}
		res.Updated = append(res.Updated, old.ID)
	}
	switch {
	case c.Retracted && old.Status == StatusOpen:
		if err := t.setStatus(old.ID, StatusRetracted); err != nil {
			return err
		}
		res.Retracted = append(res.Retracted, old.ID)
	case !c.Retracted && old.Status == StatusRetracted:
		if err := t.setStatus(old.ID, StatusOpen); err != nil {
			return err
		}
		res.Reinstated = append(res.Reinstated, old.ID)
	}
	return nil
}

// syncAnswer stores a comment that answers a question. ok is false if there is no such question, and the
// comment is then stored as an ordinary note.
func (t *tx) syncAnswer(c Comment, key string, res *SyncResult) (ok bool, err error) {
	q, err := threadByID(t, c.AnswerTo)
	if errors.Is(err, ErrNotFound) || (err == nil && q.Kind != KindQuestion) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var id int64
	var text string
	err = t.QueryRow(`SELECT id, text FROM threads WHERE key = ?`, key).Scan(&id, &text)
	switch {
	case err == nil:
		if text != c.Text {
			_, err = t.Exec(`UPDATE threads SET text = ? WHERE id = ?`, c.Text, id)
		}
		return true, err
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	by := c.Author
	if by == "" {
		by = "reviewer"
	}
	if err := t.QueryRow(`INSERT INTO threads (note, version, t, kind, by, text, options, answer_to, key, created)
		VALUES (?, ?, ?, ?, ?, ?, '[]', ?, ?, ?) RETURNING id`, q.Note, c.Version, c.T0, KindAnswer, by, c.Text, q.ID,
		key, t.s.stamp()).Scan(&id); err != nil {
		return false, err
	}
	res.Answers = append(res.Answers, id)
	if q.Note != nil {
		n, err := noteByID(t, *q.Note)
		if err != nil {
			return false, err
		}
		if n.Status == StatusQuestion {
			if err := t.setStatus(n.ID, StatusOpen); err != nil {
				return false, err
			}
			res.Reopened = append(res.Reopened, n.ID)
		}
	}
	return true, nil
}

func (t *tx) setStatus(id int64, st Status) error {
	_, err := t.Exec(`UPDATE notes SET status = ?, updated = ? WHERE id = ?`, string(st), t.s.stamp(), id)
	return err
}

// NoteUpdate is the agent's change to one note (note_set).
type NoteUpdate struct {
	Status Status
	In     string // the version that answered it (status answered); the caller checks that it exists
	Change string // that version's one-line change for it (status answered)
}

// SetNote applies the agent's change to one note. The agent may answer an open note (with the version and the
// change), or correct the version and change of an answered one; every other change is someone else's, and is
// refused with a *TransitionError that says whose.
func (s *Store) SetNote(id int64, u NoteUpdate) (Note, error) {
	var out Note
	err := s.write(func(t *tx) error {
		n, err := noteByID(t, id)
		if err != nil {
			return err
		}
		if u.Status == StatusAnswered && (strings.TrimSpace(u.In) == "" || strings.TrimSpace(u.Change) == "") {
			return fmt.Errorf("note #%d: answering a note needs the version that answers it and a one-line change", id)
		}
		if n.Status != StatusAnswered || u.Status != StatusAnswered {
			if err := checkTransition(id, n.Status, u.Status, ByAgent); err != nil {
				return err
			}
		}
		if _, err := t.Exec(`UPDATE notes SET status = ?, answered_in = ?, change = ?, updated = ? WHERE id = ?`,
			string(u.Status), strings.TrimSpace(u.In), strings.TrimSpace(u.Change), s.stamp(), id); err != nil {
			return err
		}
		t.emit(EventNotes, NotesEvent{Why: "set", Version: n.Version, Notes: []int64{id}})
		out, err = noteByID(t, id)
		return err
	})
	return out, err
}

// Reopen is the reviewer reopening an answered note: it is open again, so the next push must handle it.
func (s *Store) Reopen(id int64) (Note, error) {
	var out Note
	err := s.write(func(t *tx) error {
		n, err := noteByID(t, id)
		if err != nil {
			return err
		}
		if err := checkTransition(id, n.Status, StatusOpen, ByReviewer); err != nil {
			return err
		}
		if n.Status != StatusAnswered {
			return &TransitionError{Note: id, From: n.Status, To: StatusOpen, By: ByReviewer}
		}
		if err := t.setStatus(id, StatusOpen); err != nil {
			return err
		}
		t.emit(EventNotes, NotesEvent{Why: "reopen", Version: n.Version, Notes: []int64{id}, Opened: []int64{id}})
		out, err = noteByID(t, id)
		return err
	})
	return out, err
}

// Approve is the reviewer approving a version: every note it answered becomes accepted. It returns those notes.
func (s *Store) Approve(label string) ([]int64, error) {
	var ids []int64
	err := s.write(func(t *tx) error {
		ids = nil
		notes, err := queryNotes(t, `WHERE status = ? AND answered_in = ? ORDER BY id`, string(StatusAnswered), label)
		if err != nil {
			return err
		}
		for _, n := range notes {
			if err := t.setStatus(n.ID, StatusAccepted); err != nil {
				return err
			}
			ids = append(ids, n.ID)
		}
		if len(ids) > 0 {
			t.emit(EventNotes, NotesEvent{Why: "approve", Version: label, Notes: ids})
		}
		return nil
	})
	return ids, err
}
