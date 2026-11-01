package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/samahlstrom/retake/internal/sqldb"
)

// Paths in the store are relative to the project root, with forward slashes (spec/notes.md, "Paths"), so a project
// folder can be moved, renamed or copied and its notes still find their snapshots. A path outside the root is kept
// as it was given.

// Rel is how the store keeps the path p: relative to the project root when p is inside it, else p unchanged ("" stays
// "").
func (s *Store) Rel(p string) string { return relTo(s.root, p) }

// Abs is the file a stored path names now: a relative one is under the project root, an absolute one (outside the
// root, or written before paths were relative) is itself.
func (s *Store) Abs(p string) string { return absTo(s.root, p) }

// SnapshotFile is the file of a note's snapshot when it is a regular file inside the project, given as its path
// relative to the project root (for os.Root) and its info.
func (s *Store) SnapshotFile(n Note) (string, os.FileInfo, bool) {
	p := n.Detail.Snapshot
	if p == "" || s.root == "" {
		return "", nil, false
	}
	rel := filepath.FromSlash(p)
	if filepath.IsAbs(p) {
		in, ok := inside(s.root, p)
		if !ok {
			return "", nil, false
		}
		rel = filepath.FromSlash(in)
	}
	if !filepath.IsLocal(rel) {
		return "", nil, false
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return "", nil, false
	}
	defer func() { _ = root.Close() }()
	st, err := root.Stat(rel)
	if err != nil || !st.Mode().IsRegular() {
		return "", nil, false
	}
	return rel, st, true
}

func relTo(root, p string) string {
	if p == "" || root == "" || !filepath.IsAbs(p) {
		return p
	}
	if rel, ok := inside(root, p); ok {
		return rel
	}
	return p
}

func absTo(root, p string) string {
	if p == "" || filepath.IsAbs(p) || root == "" {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(p))
}

// inside returns p relative to root, slash-separated, when p is inside root, also through symlinks on either side.
func inside(root, p string) (string, bool) {
	if rel, err := filepath.Rel(root, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel), true
	}
	r, err1 := filepath.EvalSymlinks(root)
	q, err2 := filepath.EvalSymlinks(p)
	if err1 != nil || err2 != nil {
		return "", false
	}
	if rel, err := filepath.Rel(r, q); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel), true
	}
	return "", false
}

// relocate is the stored form of an absolute path written before paths were relative. Inside the root it becomes
// relative. Outside it (the project was moved, renamed or copied), the longest tail of it of at least two parts that
// names a file (dir: a folder) of this project is used: /old/place/demo/v6/demo_v6.review/snaps/02.png becomes
// v6/demo_v6.review/snaps/02.png when that file is here. Otherwise it stays as it was.
func relocate(root, p string, dir bool) string {
	if p == "" || !isAbsAny(p) {
		return p
	}
	if filepath.IsAbs(p) {
		if rel, ok := inside(root, p); ok {
			return rel
		}
	}
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	for i := 0; i+2 <= len(parts); i++ {
		rel := filepath.FromSlash(strings.Join(parts[i:], "/"))
		if !filepath.IsLocal(rel) {
			continue
		}
		if st, err := os.Stat(filepath.Join(root, rel)); err == nil && st.IsDir() == dir {
			return filepath.ToSlash(rel)
		}
	}
	return p
}

// isAbsAny reports whether p is absolute here or on another system (/x, C:\x, C:/x, \\host\x), so a path written on
// another machine is relocated too.
func isAbsAny(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// relocatePaths makes the absolute paths an older store holds relative to the project root (relocate): the notes'
// snapshots, the pushed versions' files and the jobs' take folders. It writes only when something changes, so opening
// a store that's already relative writes nothing.
func (s *Store) relocatePaths() error {
	if s.root == "" {
		return nil
	}
	type change struct {
		query string
		args  []any
	}
	var changes []change
	err := s.read(func(t *sqldb.Tx) error {
		rows, err := t.Query(`SELECT id, detail FROM notes WHERE detail LIKE '%"snapshot"%'`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id int64
			var raw string
			if err := rows.Scan(&id, &raw); err != nil {
				return err
			}
			var d Detail
			if json.Unmarshal([]byte(raw), &d) != nil {
				continue
			}
			if p := relocate(s.root, d.Snapshot, false); p != d.Snapshot {
				d.Snapshot = p
				b, err := json.Marshal(d)
				if err != nil {
					return err
				}
				changes = append(changes, change{`UPDATE notes SET detail = ? WHERE id = ?`, []any{string(b), id}})
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		vrows, err := t.Query(`SELECT label, path FROM versions`)
		if err != nil {
			return err
		}
		defer func() { _ = vrows.Close() }()
		for vrows.Next() {
			var label, path string
			if err := vrows.Scan(&label, &path); err != nil {
				return err
			}
			if p := relocate(s.root, path, false); p != path {
				changes = append(changes, change{`UPDATE versions SET path = ? WHERE label = ?`, []any{p, label}})
			}
		}
		if err := vrows.Err(); err != nil {
			return err
		}
		jrows, err := t.Query(`SELECT id, args FROM jobs WHERE args LIKE '%"dir"%'`)
		if err != nil {
			return err
		}
		defer func() { _ = jrows.Close() }()
		for jrows.Next() {
			var id int64
			var raw string
			if err := jrows.Scan(&id, &raw); err != nil {
				return err
			}
			var a JobArgs
			if json.Unmarshal([]byte(raw), &a) != nil {
				continue
			}
			if p := relocate(s.root, a.Dir, true); p != a.Dir {
				a.Dir = p
				b, err := json.Marshal(a)
				if err != nil {
					return err
				}
				changes = append(changes, change{`UPDATE jobs SET args = ? WHERE id = ?`, []any{string(b), id}})
			}
		}
		return jrows.Err()
	})
	if err != nil || len(changes) == 0 {
		return err
	}
	return s.write(func(t *tx) error {
		for _, c := range changes {
			if _, err := t.Exec(c.query, c.args...); err != nil {
				return err
			}
		}
		return nil
	})
}
