package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver, registered as "sqlite"
)

// DBName is the catalog file's name, in <root>/.retake/ for a project and in the library folder.
const DBName = "assets.db"

// db is one open catalog database: a project's or the library's. Writes go through rw (one transaction at a time
// per process; SQLite's busy timeout serializes processes); SQL written by people and agents runs on ro, whose
// connections are query_only.
type db struct {
	rw    *sql.DB
	ro    *sql.DB
	dir   string // the folder holding assets.db and blobs/
	scope string // ScopeProject, or ScopeLib for the library (which also holds brand kits)
	mu    sync.Mutex
	now   func() time.Time
}

func dsn(path string, readOnly bool) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x, as SQLite URIs want
	}
	q := url.Values{}
	pragmas := []string{"busy_timeout(10000)", "foreign_keys(1)"}
	if readOnly {
		pragmas = append(pragmas, "query_only(1)")
	} else {
		pragmas = append(pragmas, "journal_mode(WAL)", "synchronous(NORMAL)")
		q.Set("_txlock", "immediate")
	}
	for _, pragma := range pragmas {
		q.Add("_pragma", pragma)
	}
	return (&url.URL{Scheme: "file", Path: p}).String() + "?" + q.Encode()
}

// openDB opens (creating if create is set) the catalog in dir. It returns nil, nil when the catalog doesn't exist
// and create isn't set: reads never create a catalog.
func openDB(dir, scope string, create bool, now func() time.Time) (*db, error) {
	path := filepath.Join(dir, DBName)
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if !create {
			return nil, nil
		}
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a user folder others may read
			return nil, fmt.Errorf("creating the asset catalog: %w", err)
		}
	}
	rw, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("opening the asset catalog: %w", err)
	}
	rw.SetMaxOpenConns(4)
	if err := migrate(rw, migrations); err != nil {
		_ = rw.Close()
		return nil, fmt.Errorf("asset catalog %s: %w", path, err)
	}
	ro, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		_ = rw.Close()
		return nil, fmt.Errorf("opening the asset catalog: %w", err)
	}
	ro.SetMaxOpenConns(2)
	return &db{rw: rw, ro: ro, dir: dir, scope: scope, now: now}, nil
}

func (d *db) close() error {
	return errors.Join(d.ro.Close(), d.rw.Close())
}

func (d *db) blobs() blobStore { return blobStore{dir: filepath.Join(d.dir, "blobs")} }

// timeFormat is how times are stored: UTC, milliseconds, sortable as text (as in the project store).
const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func (d *db) stamp() string { return d.now().UTC().Format(timeFormat) }

// write runs fn in one write transaction.
func (d *db) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	tx, err := d.rw.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// migrate brings the database up to len(ms), one transaction per step, as the project store does. A database newer
// than this build is refused rather than touched.
func migrate(sdb *sql.DB, ms []string) error {
	ctx := context.Background()
	conn, err := sdb.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var v int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			_ = tx.Rollback()
			return err
		}
		if v > len(ms) {
			_ = tx.Rollback()
			return fmt.Errorf("the catalog has schema version %d, newer than this retake knows (%d): update retake", v,
				len(ms))
		}
		if v == len(ms) {
			return tx.Rollback()
		}
		if _, err := tx.ExecContext(ctx, ms[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrating to schema version %d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}
