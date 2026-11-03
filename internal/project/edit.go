package project

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Editing a comment (spec/notes.md, "Editing a comment"): a person changes a comment's words while it is still to do
// and the agent hasn't started on it. The note's text holds the new words; note_edits keeps that a person changed
// them, so a notes rebuild never puts the engine's words back.

// ErrNotEditable is a comment whose words can't change now; the error's text says why.
var ErrNotEditable = errors.New("this comment can't be edited now")

// ErrBadEdit is new words that aren't allowed: too long, or none on a comment without a drawing.
var ErrBadEdit = errors.New("those words can't be saved")

// NoteEdit is a person's new words for a comment. Pictured says the comment has an image (the caller knows its take);
// a comment with a drawing or an image may have no words.
type NoteEdit struct {
	Text     string
	By       string
	Pictured bool
}

// EditBlock says why note n's words can't change now in automation state a, or nil: it isn't open, a person deleted
// it, or the agent of a running task is on it (working) or marked it done.
func EditBlock(n Note, deleted bool, a AutomationState) error {
	switch {
	case deleted:
		return fmt.Errorf("%w: it was deleted", ErrNotEditable)
	case n.Status != StatusOpen:
		return fmt.Errorf("%w: it is %s, not to do any more", ErrNotEditable, n.Status)
	}
	for _, j := range a.Tasks {
		if j.State != "running" && j.State != "stopping" || !slices.Contains(j.Notes, n.ID) {
			continue
		}
		if j.Working != nil && j.Working.Note == n.ID || j.mark(n.ID) != nil || slices.Contains(j.Done, n.ID) {
			return fmt.Errorf("%w: the agent is working on it", ErrNotEditable)
		}
	}
	return nil
}

// noteDeleted reports whether a person deleted note id.
func noteDeleted(q querier, id int64) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM note_deletions WHERE note = ?`, id).Scan(&n)
	return n > 0, err
}

// EditNote gives comment id new words (trimmed). The same words it has change nothing (changed is false). It
// returns the note as it is now. ErrNotFound for an unknown note, ErrNotEditable when it can't change now, and
// ErrBadEdit for words that aren't allowed. An edit emits a notes event; a queued or running task that holds the note
// gets "Edited #12" in its history.
func (s *Store) EditNote(id int64, e NoteEdit) (n Note, changed bool, err error) {
	text := strings.TrimSpace(e.Text)
	if utf8.RuneCountInString(text) > MaxDocNote {
		return n, false, fmt.Errorf("%w: a comment is at most %d characters", ErrBadEdit, MaxDocNote)
	}
	err = s.write(func(t *tx) error {
		changed = false
		if n, err = noteByID(t, id); err != nil {
			return err
		}
		deleted, err := noteDeleted(t, id)
		if err != nil {
			return err
		}
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		if err = EditBlock(n, deleted, a); err != nil {
			return err
		}
		if text == n.Text {
			return nil // idempotent: the words it has
		}
		if text == "" && len(n.Detail.Strokes) == 0 && !e.Pictured {
			return fmt.Errorf("%w: a comment without a drawing needs words", ErrBadEdit)
		}
		now := s.stamp()
		if _, err = t.Exec(`INSERT INTO note_edits (note, original, by, updated) VALUES (?, ?, ?, ?)
			ON CONFLICT (note) DO UPDATE SET by = excluded.by, updated = excluded.updated`, id, n.Text, e.By, now); err != nil {
			return err
		}
		if _, err = t.Exec(`UPDATE notes SET text = ?, updated = ? WHERE id = ?`, text, now, id); err != nil {
			return err
		}
		n.Text, n.Updated, n.Edited, changed = text, now, true, true
		t.emit(EventNotes, NotesEvent{Why: "edit", Notes: []int64{id}, Version: n.Version})
		touched := false
		for i := range a.Tasks {
			j := &a.Tasks[i]
			if (j.State == "queued" || j.State == "running" || j.State == "stopping" || j.State == "paused") &&
				slices.Contains(j.Pending(), id) {
				j.AddEvent(s.Now().Unix(), fmt.Sprintf("Edited #%d", id))
				touched = true
			}
		}
		if touched {
			if err = saveAutomation(t, a); err != nil {
				return err
			}
			t.emit("automation", map[string]bool{"changed": true})
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) {
		err = fmt.Errorf("note #%d: %w", id, ErrNotFound)
	}
	return n, changed, err
}
