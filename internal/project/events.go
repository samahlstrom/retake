package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Event kinds. They are also the SSE event names of GET /api/events.
const (
	EventNotes   = "notes"   // notes or questions changed: the sidebar should refresh
	EventJob     = "job"     // a job changed state: {id, kind, state, ...}
	EventVersion = "version" // the family changed: a version was pushed
)

// Event is one row of the event log.
type Event struct {
	Seq     int64           `json:"seq"`
	Kind    string          `json:"kind"`
	Data    json.RawMessage `json:"data"`
	Created string          `json:"created"`
}

// NotesEvent is the data of a "notes" event.
type NotesEvent struct {
	Why       string  `json:"why"`                 // sync | ask | set | push | reopen | approve
	Version   string  `json:"version,omitempty"`   // the version it concerns
	Notes     []int64 `json:"notes,omitempty"`     // every note whose row changed
	Opened    []int64 `json:"opened,omitempty"`    // notes that became open: new, reinstated, reopened, answered
	Answers   []int64 `json:"answers,omitempty"`   // new answer threads
	Questions []int64 `json:"questions,omitempty"` // new question threads
}

// JobEvent is the data of a "job" event.
type JobEvent struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	State    string `json:"state"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error,omitempty"`
	Hint     string `json:"hint,omitempty"` // a failed job's one-line fix, such as how to set up transcription
	Take     string `json:"take,omitempty"`
	Version  string `json:"version,omitempty"`
}

// VersionEvent is the data of a "version" event.
type VersionEvent struct {
	Why    string `json:"why"` // push
	Label  string `json:"label"`
	Parent string `json:"parent,omitempty"`
	Path   string `json:"path,omitempty"`
}

func (t *tx) flushEvents() error {
	now := t.s.stamp()
	for _, e := range t.events {
		b, err := json.Marshal(e.data)
		if err != nil {
			return err
		}
		if _, err := t.Exec(`INSERT INTO events (kind, data, created) VALUES (?, ?, ?)`, e.kind, string(b), now); err != nil {
			return err
		}
	}
	return nil
}

// Events returns up to limit events after since, oldest first (limit <= 0: all).
func (s *Store) Events(since int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := s.db.Query(`SELECT seq, kind, data, created FROM events WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var data string
		if err := rows.Scan(&e.Seq, &e.Kind, &data, &e.Created); err != nil {
			return nil, err
		}
		e.Data = json.RawMessage(data)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastSeq returns the newest event's seq, or 0.
func (s *Store) LastSeq() (int64, error) {
	var seq sql.NullInt64
	if err := s.db.QueryRow(`SELECT max(seq) FROM events`).Scan(&seq); err != nil {
		return 0, err
	}
	return seq.Int64, nil
}

// Changed returns a channel that is closed after this Store's next write that appended events. Writes by other
// processes (or other Stores on the same file) don't close it: poll for those.
func (s *Store) Changed() <-chan struct{} { return s.bell.wait() }

// PollInterval is how often waiters should look for events from other processes.
func (s *Store) PollInterval() time.Duration { return s.poll() }

// WaitEvents returns the events after since, waiting until there is at least one or ctx is done (then it returns
// ctx's error).
func (s *Store) WaitEvents(ctx context.Context, since int64, limit int) ([]Event, error) {
	tick := time.NewTicker(s.poll())
	defer tick.Stop()
	for {
		changed := s.Changed()
		evs, err := s.Events(since, limit)
		if err != nil || len(evs) > 0 {
			return evs, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		case <-tick.C:
		}
	}
}
