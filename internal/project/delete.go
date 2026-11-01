package project

import (
	"database/sql"
	"errors"
)

// DeleteNote retracts a comment until explicitly restored, retaining its original content and prior status.
func (s *Store) DeleteNote(id int64) error {
	err := s.write(func(t *tx) error {
		n, err := noteByID(t, id)
		if err != nil {
			return err
		}
		result, err := t.Exec(`INSERT INTO note_deletions (note, previous_status) VALUES (?, ?)
			ON CONFLICT (note) DO NOTHING`, id, string(n.Status))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			return err
		}
		if err := t.setStatus(id, StatusRetracted); err != nil {
			return err
		}
		t.emit(EventNotes, NotesEvent{Why: "delete", Notes: []int64{id}, Version: n.Version})
		return nil
	})
	if err != nil {
		return err
	}
	return s.ReconcileWork()
}

// RestoreNote undoes a user deletion without restarting withdrawn editing work.
func (s *Store) RestoreNote(id int64) error {
	return s.write(func(t *tx) error {
		n, err := noteByID(t, id)
		if err != nil {
			return err
		}
		var status string
		err = t.QueryRow(`SELECT previous_status FROM note_deletions WHERE note = ?`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := t.Exec(`DELETE FROM note_deletions WHERE note = ?`, id); err != nil {
			return err
		}
		if err := t.setStatus(id, Status(status)); err != nil {
			return err
		}
		t.emit(EventNotes, NotesEvent{Why: "restore", Notes: []int64{id}, Version: n.Version})
		return nil
	})
}
