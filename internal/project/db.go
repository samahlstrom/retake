package project

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the pure-Go SQLite driver, registered as "sqlite"
)

// Dir is the folder under the project root that holds the store.
const Dir = ".retake"

// DBPath returns the path of a project's store: <root>/.retake/project.db.
func DBPath(root string) string { return filepath.Join(root, Dir, "project.db") }

// ErrNoProject is returned by OpenExisting when the project has no store yet.
var ErrNoProject = errors.New("no project store yet")

// DefaultPoll is how often waiters look for events written by other processes.
const DefaultPoll = 250 * time.Millisecond

// Store is an open project store. It is safe for concurrent use, and several processes (the server, the CLI, the
// MCP server) may have the same store open: SQLite's WAL mode and busy timeout serialize their writes.
type Store struct {
	db   *sql.DB
	root string
	mu   sync.Mutex // serializes this process's write transactions
	bell bell       // rung after every committed write that appended events
	// Now tells the time; tests swap it.
	Now func() time.Time
	// Poll is how often waiters check for events from other processes (DefaultPoll if 0).
	Poll time.Duration
}

// Open opens the store of the project at root, creating <root>/.retake/project.db if needed, and migrates it to the
// current schema.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, Dir), 0o755); err != nil { //nolint:gosec // a user folder others may read
		return nil, fmt.Errorf("creating the project store: %w", err)
	}
	return open(root, migrations)
}

// OpenExisting opens the store of the project at root if it exists, else returns ErrNoProject.
func OpenExisting(root string) (*Store, error) {
	if _, err := os.Stat(DBPath(root)); errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoProject
	}
	return open(root, migrations)
}

func dsn(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x, as SQLite URIs want
	}
	q := url.Values{}
	for _, pragma := range []string{"busy_timeout(10000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)"} {
		q.Add("_pragma", pragma)
	}
	q.Set("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: p}).String() + "?" + q.Encode()
}

func open(root string, ms []string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(DBPath(root)))
	if err != nil {
		return nil, fmt.Errorf("opening the project store: %w", err)
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, root: root, Now: time.Now}
	s.bell.init()
	if err := migrate(db, ms); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("project store %s: %w", DBPath(root), err)
	}
	return s, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Root returns the project root.
func (s *Store) Root() string { return s.root }

// Path returns the database file's path.
func (s *Store) Path() string { return DBPath(s.root) }

func (s *Store) poll() time.Duration {
	if s.Poll > 0 {
		return s.Poll
	}
	return DefaultPoll
}

// timeFormat is how times are stored: UTC, milliseconds, sortable as text.
const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func (s *Store) stamp() string { return s.Now().UTC().Format(timeFormat) }

// ParseTime parses a stored time.
func ParseTime(v string) (time.Time, error) { return time.Parse(timeFormat, v) }

// tx is one write transaction. Events appended through it commit with the change they describe.
type tx struct {
	*sql.Tx
	s      *Store
	events []pendingEvent
}

type pendingEvent struct {
	kind string
	data any
}

// emit appends an event when the transaction commits.
func (t *tx) emit(kind string, data any) { t.events = append(t.events, pendingEvent{kind, data}) }

// write runs fn in a write transaction (BEGIN IMMEDIATE) and appends its events in the same transaction.
func (s *Store) write(fn func(t *tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sqlTx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	t := &tx{Tx: sqlTx, s: s}
	if err := fn(t); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := t.flushEvents(); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return err
	}
	if len(t.events) > 0 {
		s.bell.ring()
	}
	return nil
}

// read runs fn in a read transaction, so it sees one consistent snapshot.
func (s *Store) read(fn func(t *sql.Tx) error) error {
	sqlTx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = sqlTx.Rollback() }()
	return fn(sqlTx)
}

// bell wakes every waiter at once: wait returns a channel that ring closes.
type bell struct {
	mu sync.Mutex
	ch chan struct{}
}

func (b *bell) init() { b.ch = make(chan struct{}) }

func (b *bell) wait() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

func (b *bell) ring() {
	b.mu.Lock()
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}
