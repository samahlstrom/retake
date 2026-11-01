package project

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Thread kinds.
const (
	KindQuestion = "question" // the agent asks
	KindAnswer   = "answer"   // the reviewer answers, by recording a segment with answer_to
)

// Thread is one row of the threads table: a question of the agent's, or a reviewer's answer to one.
type Thread struct {
	ID       int64    `json:"id"`
	Note     *int64   `json:"note"` // nil: about the version as a whole
	Version  string   `json:"version"`
	T        *float64 `json:"t"`
	Kind     string   `json:"kind"`
	By       string   `json:"by"`
	Text     string   `json:"text"`
	Options  []string `json:"options"`
	AnswerTo *int64   `json:"answer_to,omitempty"`
	Created  string   `json:"created"`
}

const threadCols = `id, note, version, t, kind, by, text, options, answer_to, created`

func scanThread(r scanner) (Thread, error) {
	var th Thread
	var note, answerTo sql.NullInt64
	var t sql.NullFloat64
	var options string
	if err := r.Scan(&th.ID, &note, &th.Version, &t, &th.Kind, &th.By, &th.Text, &options, &answerTo,
		&th.Created); err != nil {
		return th, err
	}
	if note.Valid {
		th.Note = &note.Int64
	}
	if answerTo.Valid {
		th.AnswerTo = &answerTo.Int64
	}
	if t.Valid {
		th.T = &t.Float64
	}
	if err := json.Unmarshal([]byte(options), &th.Options); err != nil || th.Options == nil {
		th.Options = []string{}
	}
	return th, nil
}

func queryThreads(q querier, where string, args ...any) ([]Thread, error) {
	rows, err := q.Query(`SELECT `+threadCols+` FROM threads `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Thread
	for rows.Next() {
		th, err := scanThread(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, th)
	}
	return out, rows.Err()
}

func threadByID(q querier, id int64) (Thread, error) {
	th, err := scanThread(q.QueryRow(`SELECT `+threadCols+` FROM threads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return th, fmt.Errorf("thread %d: %w", id, ErrNotFound)
	}
	return th, err
}

// Thread returns one thread.
func (s *Store) Thread(id int64) (Thread, error) { return threadByID(s.db, id) }

// ThreadsOf returns the threads about the given notes, oldest first.
func (s *Store) ThreadsOf(notes []int64) ([]Thread, error) {
	if len(notes) == 0 {
		return nil, nil
	}
	args := make([]any, len(notes))
	for i, id := range notes {
		args[i] = id
	}
	return queryThreads(s.db, `WHERE note IN (`+placeholders(len(notes))+`) ORDER BY id`, args...)
}

// Threads returns threads by id, oldest first.
func (s *Store) Threads(ids []int64) ([]Thread, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return queryThreads(s.db, `WHERE id IN (`+placeholders(len(ids))+`) ORDER BY id`, args...)
}

// Unanswered returns the questions that have no answer yet, oldest first.
func (s *Store) Unanswered() ([]Thread, error) {
	return queryThreads(s.db, `WHERE kind = 'question'
		AND NOT EXISTS (SELECT 1 FROM note_deletions d WHERE d.note = threads.note)
		AND NOT EXISTS (SELECT 1 FROM threads a WHERE a.answer_to = threads.id) ORDER BY id`)
}

// QuestionsOn returns the questions asked about a version as a whole (no note), oldest first.
func (s *Store) QuestionsOn(version string) ([]Thread, error) {
	return queryThreads(s.db, `WHERE kind = 'question' AND note IS NULL AND version = ? ORDER BY id`, version)
}

// Ask is a question of the agent's.
type Ask struct {
	Note    int64    // the note it's about; 0 = the version as a whole
	Version string   // the version it's asked on; "" = the note's
	T       *float64 // the video time it points at; nil = the note's start
	Text    string
	Options []string // version labels the reviewer can pick from; the caller checks them
	By      string   // "" = "agent"
}

// Ask stores a question. A question about a note moves it from open to question.
func (s *Store) Ask(a Ask) (Thread, error) {
	var out Thread
	err := s.write(func(t *tx) error {
		th, err := t.ask(a)
		if err != nil {
			return err
		}
		ev := NotesEvent{Why: "ask", Version: th.Version, Questions: []int64{th.ID}}
		if th.Note != nil {
			ev.Notes = []int64{*th.Note}
		}
		t.emit(EventNotes, ev)
		out = th
		return nil
	})
	return out, err
}

func (t *tx) ask(a Ask) (Thread, error) {
	text := strings.TrimSpace(a.Text)
	if text == "" {
		return Thread{}, errors.New("a question needs its text")
	}
	by := a.By
	if by == "" {
		by = string(ByAgent)
	}
	options := a.Options
	if options == nil {
		options = []string{}
	}
	opts, err := json.Marshal(options)
	if err != nil {
		return Thread{}, err
	}
	var note any
	version, tt := a.Version, a.T
	if a.Note != 0 {
		n, err := noteByID(t, a.Note)
		if err != nil {
			return Thread{}, err
		}
		if err := checkTransition(n.ID, n.Status, StatusQuestion, ByAgent); err != nil {
			return Thread{}, err
		}
		if err := t.setStatus(n.ID, StatusQuestion); err != nil {
			return Thread{}, err
		}
		note = n.ID
		if version == "" {
			version = n.Version
		}
		if tt == nil {
			t0 := n.T0
			tt = &t0
		}
	}
	if version == "" {
		return Thread{}, errors.New("a question about no note needs the version it's about")
	}
	var tv any
	if tt != nil {
		tv = *tt
	}
	var id int64
	if err := t.QueryRow(`INSERT INTO threads (note, version, t, kind, by, text, options, created)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, note, version, tv, KindQuestion, by, text, string(opts),
		t.s.stamp()).Scan(&id); err != nil {
		return Thread{}, err
	}
	return threadByID(t, id)
}
