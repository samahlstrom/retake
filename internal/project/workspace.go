package project

import (
	"database/sql"
	"errors"
)

// ErrWorkspaceConflict means another writer has advanced the project view revision.
var ErrWorkspaceConflict = errors.New("the project view changed in another window; refresh and try again")

// Workspace reads the shared project organization and its revision.
func (s *Store) Workspace() (data string, revision int64, err error) {
	err = s.db.QueryRow(`SELECT data, revision FROM workspace WHERE id = 1`).Scan(&data, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

// SaveWorkspace replaces the project view only if the caller's revision is current.
func (s *Store) SaveWorkspace(data string, revision int64) (int64, error) {
	err := s.write(func(t *tx) error {
		if revision == 0 {
			res, err := t.Exec(`INSERT INTO workspace (id, revision, data) VALUES (1, 1, ?) ON CONFLICT (id) DO NOTHING`, data)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if n != 1 {
				return ErrWorkspaceConflict
			}
			return nil
		}
		res, err := t.Exec(`UPDATE workspace SET revision = revision + 1, data = ? WHERE id = 1 AND revision = ?`, data, revision)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrWorkspaceConflict
		}
		return nil
	})
	if err != nil {
		return revision, err
	}
	return revision + 1, nil
}
