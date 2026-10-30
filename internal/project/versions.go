package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ChangeEntry is one line of a version's changelog: what it did about one note.
type ChangeEntry struct {
	Note    int64  `json:"note"`
	Summary string `json:"summary"`
}

// VersionRow is one row of the versions table: a version that was pushed.
type VersionRow struct {
	Label     string        `json:"label"`
	Path      string        `json:"path"`
	SHA256    string        `json:"sha256"`
	Parent    string        `json:"parent"`
	By        string        `json:"by"`
	Created   string        `json:"created"`
	Changelog []ChangeEntry `json:"changelog"`
}

const versionCols = `label, path, sha256, parent, by, created, changelog`

func scanVersion(r scanner) (VersionRow, error) {
	var v VersionRow
	var changelog string
	if err := r.Scan(&v.Label, &v.Path, &v.SHA256, &v.Parent, &v.By, &v.Created, &changelog); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(changelog), &v.Changelog); err != nil || v.Changelog == nil {
		v.Changelog = []ChangeEntry{}
	}
	return v, nil
}

// Versions returns the pushed versions, oldest first.
func (s *Store) Versions() ([]VersionRow, error) {
	rows, err := s.db.Query(`SELECT ` + versionCols + ` FROM versions ORDER BY created, label`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []VersionRow
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Version returns one pushed version.
func (s *Store) Version(label string) (VersionRow, error) {
	v, err := scanVersion(s.db.QueryRow(`SELECT `+versionCols+` FROM versions WHERE label = ?`, label))
	if errors.Is(err, sql.ErrNoRows) {
		return v, fmt.Errorf("version %s: %w", label, ErrNotFound)
	}
	return v, err
}

// PushAsk is a question asked in the same push as a version.
type PushAsk struct {
	Note int64
	Text string
}

// Push is a version being pushed: the file is already in place (or about to be) and this records it.
type Push struct {
	Label     string
	Path      string
	SHA256    string
	Parent    string
	By        string
	Changelog []ChangeEntry
	Asks      []PushAsk
	// Scope is the parent's notes: every one of them that is open must be in the changelog or asked about.
	Scope Scope
}

// PushRuleError is a push that leaves notes open on its parent unhandled.
type PushRuleError struct {
	Parent string
	Notes  []int64 // the open notes that are neither in the changelog nor asked about
}

func (e *PushRuleError) Error() string {
	ids := make([]string, len(e.Notes))
	for i, id := range e.Notes {
		ids[i] = fmt.Sprintf("#%d", id)
	}
	subject := "note " + strings.Join(ids, ", ") + " is"
	if len(ids) > 1 {
		subject = "notes " + strings.Join(ids, ", ") + " are"
	}
	return fmt.Sprintf("push rejected: %s open on %s; list each in the changelog (what this version changes for "+
		"it) or ask the reviewer about it in the same push", subject, e.Parent)
}

// PushResult is what a push recorded.
type PushResult struct {
	Version   VersionRow
	Answered  []int64 // notes now answered by this version
	Questions []int64 // the question threads it asked
}

// CheckPush validates a push without recording anything: the label is free, every changelog and ask entry names
// an open note (each once), and the push rule holds.
func (s *Store) CheckPush(p Push) error {
	return s.read(func(t *sql.Tx) error { return checkPush(t, p) })
}

func checkPush(q querier, p Push) error {
	if strings.TrimSpace(p.Label) == "" {
		return errors.New("a pushed version needs a label")
	}
	var n int
	if err := q.QueryRow(`SELECT count(*) FROM versions WHERE label = ?`, p.Label).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("version %s was already pushed", p.Label)
	}
	handled := map[int64]bool{}
	check := func(id int64, what string) error {
		if handled[id] {
			return fmt.Errorf("note #%d is listed twice (changelog and asks name each note once)", id)
		}
		handled[id] = true
		note, err := noteByID(q, id)
		if err != nil {
			return err
		}
		to := StatusAnswered
		if what == "ask" {
			to = StatusQuestion
		}
		by := ByPush
		if what == "ask" {
			by = ByAgent
		}
		return checkTransition(id, note.Status, to, by)
	}
	for _, c := range p.Changelog {
		if strings.TrimSpace(c.Summary) == "" {
			return fmt.Errorf("note #%d: a changelog entry needs a one-line summary of the change", c.Note)
		}
		if err := check(c.Note, "change"); err != nil {
			return err
		}
	}
	for _, a := range p.Asks {
		if strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("note #%d: a question needs its text", a.Note)
		}
		if err := check(a.Note, "ask"); err != nil {
			return err
		}
	}
	open, err := filterNotes(q, NoteFilter{Statuses: []Status{StatusOpen}, Scope: &p.Scope})
	if err != nil {
		return err
	}
	var missing []int64
	for _, n := range open {
		if !handled[n.ID] {
			missing = append(missing, n.ID)
		}
	}
	if len(missing) > 0 {
		return &PushRuleError{Parent: p.Parent, Notes: missing}
	}
	return nil
}

// RecordPush records a pushed version after checking it like CheckPush: the version row, the changelog's notes
// (open to answered), and the asked questions (open to question), with a "version" and a "notes" event. Nothing
// is recorded if the check fails.
func (s *Store) RecordPush(p Push) (PushResult, error) {
	var res PushResult
	err := s.write(func(t *tx) error {
		res = PushResult{}
		if err := checkPush(t, p); err != nil {
			return err
		}
		changelog := p.Changelog
		if changelog == nil {
			changelog = []ChangeEntry{}
		}
		cl, err := json.Marshal(changelog)
		if err != nil {
			return err
		}
		now := s.stamp()
		if _, err := t.Exec(`INSERT INTO versions (label, path, sha256, parent, by, created, changelog)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, p.Label, p.Path, p.SHA256, p.Parent, p.By, now, string(cl)); err != nil {
			return err
		}
		for _, c := range p.Changelog {
			if _, err := t.Exec(`UPDATE notes SET status = ?, answered_in = ?, change = ?, updated = ? WHERE id = ?`,
				string(StatusAnswered), p.Label, strings.TrimSpace(c.Summary), now, c.Note); err != nil {
				return err
			}
			res.Answered = append(res.Answered, c.Note)
		}
		for _, a := range p.Asks {
			th, err := t.ask(Ask{Note: a.Note, Version: p.Label, Text: a.Text})
			if err != nil {
				return err
			}
			res.Questions = append(res.Questions, th.ID)
		}
		t.emit(EventVersion, VersionEvent{Why: "push", Label: p.Label, Parent: p.Parent, Path: p.Path})
		ids := append(slices.Clone(res.Answered), p.askNotes()...)
		if len(ids) > 0 {
			t.emit(EventNotes, NotesEvent{Why: "push", Version: p.Label, Notes: unique(ids), Questions: res.Questions})
		}
		res.Version, err = scanVersion(t.QueryRow(`SELECT `+versionCols+` FROM versions WHERE label = ?`, p.Label))
		return err
	})
	return res, err
}

func (p Push) askNotes() []int64 {
	out := make([]int64, len(p.Asks))
	for i, a := range p.Asks {
		out[i] = a.Note
	}
	return out
}
