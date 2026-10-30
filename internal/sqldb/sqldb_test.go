package sqldb

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRebind(t *testing.T) {
	cases := []struct{ in, sqlite, pg string }{
		{`SELECT 1`, `SELECT 1`, `SELECT 1`},
		{`SELECT * FROM t WHERE a = ? AND b IN (?, ?)`, `SELECT * FROM t WHERE a = ? AND b IN (?, ?)`,
			`SELECT * FROM t WHERE a = $1 AND b IN ($2, $3)`},
		{`INSERT INTO t (a) VALUES ('what?') RETURNING id`, `INSERT INTO t (a) VALUES ('what?') RETURNING id`,
			`INSERT INTO t (a) VALUES ('what?') RETURNING id`},
		{`SELECT "odd?col" FROM t WHERE a = ?`, `SELECT "odd?col" FROM t WHERE a = ?`, `SELECT "odd?col" FROM t WHERE a = $1`},
		{`UPDATE t SET a = 'it''s?' WHERE b = ?`, `UPDATE t SET a = 'it''s?' WHERE b = ?`,
			`UPDATE t SET a = 'it''s?' WHERE b = $1`},
	}
	for _, c := range cases {
		if got := SQLite.Rebind(c.in); got != c.sqlite {
			t.Errorf("sqlite %q: %q", c.in, got)
		}
		if got := Postgres.Rebind(c.in); got != c.pg {
			t.Errorf("postgres %q: %q, want %q", c.in, got, c.pg)
		}
	}
}

func TestValidSchema(t *testing.T) {
	for _, ok := range []string{"retake", "retake_p1", "_x", "a9"} {
		if !ValidSchema(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "Retake", "1a", "a-b", "a b", "a;drop", strings.Repeat("a", 64)} {
		if ValidSchema(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestSQLiteMigrate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "x.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{`CREATE TABLE a (id INTEGER PRIMARY KEY AUTOINCREMENT, v TEXT)`, `ALTER TABLE a ADD COLUMN w TEXT`}
	if err := Migrate(ctx, db, steps); err != nil {
		t.Fatal(err)
	}
	if v, err := Version(ctx, db); err != nil || v != 2 {
		t.Fatalf("version %d %v", v, err)
	}
	tx, err := db.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := tx.QueryRow(`INSERT INTO a (v, w) VALUES (?, ?) RETURNING id`, "x", "y").Scan(&id); err != nil || id != 1 {
		t.Fatalf("insert: %d %v", id, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, steps[:1]); !errors.Is(err, ErrTooNew) {
		t.Fatalf("older build: %v", err)
	}
	if IsPostgresURL(path) || !IsPostgresURL("postgres://x") || !IsPostgresURL("postgresql://x") {
		t.Fatal("IsPostgresURL")
	}
	_ = db.Close()
}
