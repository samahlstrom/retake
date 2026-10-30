// Package sqldb is the thin layer that lets one set of SQL statements run on SQLite and on Postgres (spec/server.md,
// "Storage drivers"): placeholders are written as ?, and a Postgres connection rewrites them to $1, $2, ...; write
// transactions serialize the same way on both (SQLite's BEGIN IMMEDIATE, a Postgres advisory lock); and migrations
// are forward-only steps whose number is kept in PRAGMA user_version or a schema_version table.
package sqldb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite" // the pure-Go SQLite driver, registered as "sqlite"
)

// Dialect is the SQL flavor of a database.
type Dialect int

// The dialects.
const (
	SQLite Dialect = iota
	Postgres
)

func (d Dialect) String() string {
	if d == Postgres {
		return "postgres"
	}
	return "sqlite"
}

// Rebind rewrites the ? placeholders of q for the dialect. Question marks inside quoted strings or identifiers are
// left alone.
func (d Dialect) Rebind(q string) string {
	if d != Postgres || !strings.Contains(q, "?") {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	var quote byte
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// DB is a database handle that speaks one dialect: Query, QueryRow and Exec take ? placeholders.
type DB struct {
	raw     *sql.DB
	dialect Dialect
	schema  string // Postgres: the schema every connection's search_path points at
	lockKey int64  // Postgres: the advisory lock that write transactions take
}

// Raw returns the underlying *sql.DB.
func (db *DB) Raw() *sql.DB { return db.raw }

// Dialect returns the database's dialect.
func (db *DB) Dialect() Dialect { return db.dialect }

// Schema returns the Postgres schema ("" for SQLite).
func (db *DB) Schema() string { return db.schema }

// Close closes the database.
func (db *DB) Close() error { return db.raw.Close() }

// Query runs a query that returns rows.
func (db *DB) Query(q string, args ...any) (*sql.Rows, error) {
	return db.raw.Query(db.dialect.Rebind(q), args...)
}

// QueryRow runs a query that returns at most one row.
func (db *DB) QueryRow(q string, args ...any) *sql.Row {
	return db.raw.QueryRow(db.dialect.Rebind(q), args...)
}

// Exec runs a statement.
func (db *DB) Exec(q string, args ...any) (sql.Result, error) {
	return db.raw.Exec(db.dialect.Rebind(q), args...)
}

// Ping checks that the database answers.
func (db *DB) Ping(ctx context.Context) error { return db.raw.PingContext(ctx) }

// Tx is a transaction that speaks the database's dialect.
type Tx struct {
	raw     *sql.Tx
	dialect Dialect
}

// Query runs a query that returns rows.
func (t *Tx) Query(q string, args ...any) (*sql.Rows, error) {
	return t.raw.Query(t.dialect.Rebind(q), args...)
}

// QueryRow runs a query that returns at most one row.
func (t *Tx) QueryRow(q string, args ...any) *sql.Row {
	return t.raw.QueryRow(t.dialect.Rebind(q), args...)
}

// Exec runs a statement.
func (t *Tx) Exec(q string, args ...any) (sql.Result, error) {
	return t.raw.Exec(t.dialect.Rebind(q), args...)
}

// Commit commits the transaction.
func (t *Tx) Commit() error { return t.raw.Commit() }

// Rollback aborts the transaction.
func (t *Tx) Rollback() error { return t.raw.Rollback() }

// Dialect returns the transaction's dialect.
func (t *Tx) Dialect() Dialect { return t.dialect }

// BeginRead starts a read-only transaction: one consistent snapshot.
func (db *DB) BeginRead(ctx context.Context) (*Tx, error) {
	opts := &sql.TxOptions{ReadOnly: true}
	if db.dialect == Postgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	tx, err := db.raw.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{raw: tx, dialect: db.dialect}, nil
}

// BeginWrite starts a write transaction. Write transactions on one database run one at a time, across processes:
// SQLite's connections begin IMMEDIATE (OpenSQLite sets it), and on Postgres each takes the database's advisory
// lock until it ends.
func (db *DB) BeginWrite(ctx context.Context) (*Tx, error) {
	tx, err := db.raw.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	t := &Tx{raw: tx, dialect: db.dialect}
	if db.dialect == Postgres {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, db.lockKey); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return t, nil
}

// SQLiteDSN returns the connection string of a SQLite file with the pragmas every retake store uses: a busy
// timeout, WAL, foreign keys, and IMMEDIATE write transactions.
func SQLiteDSN(path string) string {
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

// OpenSQLite opens (creating) the SQLite database at path.
func OpenSQLite(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // a data folder others may read
		return nil, err
	}
	raw, err := sql.Open("sqlite", SQLiteDSN(path))
	if err != nil {
		return nil, err
	}
	raw.SetMaxOpenConns(4)
	return &DB{raw: raw, dialect: SQLite}, nil
}

// WrapSQLite wraps an open SQLite *sql.DB (opened with SQLiteDSN).
func WrapSQLite(raw *sql.DB) *DB { return &DB{raw: raw, dialect: SQLite} }

var schemaRE = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidSchema reports whether name can be a schema name here: lower-case letters, digits and _, so it never needs
// quoting.
func ValidSchema(name string) bool { return schemaRE.MatchString(name) }

// PoolOptions size a Postgres pool.
type PoolOptions struct {
	MaxOpen     int           // 0 = 4
	MaxIdle     int           // 0 = 1
	MaxIdleTime time.Duration // 0 = 1 minute
}

// OpenPostgres opens a pool on the Postgres database at dsn whose connections all work in schema (search_path), and
// creates the schema if it doesn't exist.
func OpenPostgres(ctx context.Context, dsn, schema string, po PoolOptions) (*DB, error) {
	if !ValidSchema(schema) {
		return nil, fmt.Errorf("bad schema name %q", schema)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("the database URL: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["search_path"] = schema
	cfg.RuntimeParams["application_name"] = "retake"
	raw := stdlib.OpenDB(*cfg)
	raw.SetMaxOpenConns(max(1, or(po.MaxOpen, 4)))
	raw.SetMaxIdleConns(or(po.MaxIdle, 1))
	idle := po.MaxIdleTime
	if idle <= 0 {
		idle = time.Minute
	}
	raw.SetConnMaxIdleTime(idle)
	db := &DB{raw: raw, dialect: Postgres, schema: schema, lockKey: LockKey(schema)}
	if _, err := raw.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+schema); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("creating schema %s: %w", schema, err)
	}
	return db, nil
}

func or(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// LockKey is the advisory lock key of a schema: a hash of its name.
func LockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("retake:" + name))
	return int64(h.Sum64()) //nolint:gosec // a lock key, wrapping is fine
}

// ErrTooNew is returned when a store's schema is newer than this build knows.
var ErrTooNew = errors.New("the store is newer than this retake knows")

// Migrate brings the database up to len(steps), one transaction per step. SQLite keeps the step number in PRAGMA
// user_version, Postgres in the schema's schema_version table. A database newer than steps is refused (ErrTooNew)
// rather than touched. Processes that migrate at once take turns.
func Migrate(ctx context.Context, db *DB, steps []string) error {
	if db.dialect == Postgres {
		return migratePostgres(ctx, db, steps)
	}
	conn, err := db.raw.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	for {
		tx, err := conn.BeginTx(ctx, nil) // BEGIN IMMEDIATE: two processes migrating take turns
		if err != nil {
			return err
		}
		var v int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
			_ = tx.Rollback()
			return err
		}
		if v > len(steps) {
			_ = tx.Rollback()
			return tooNew(v, len(steps))
		}
		if v == len(steps) {
			return tx.Rollback()
		}
		if _, err := tx.ExecContext(ctx, steps[v]); err != nil {
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

func tooNew(have, know int) error {
	return fmt.Errorf("%w: it has schema version %d, this build knows %d; update retake", ErrTooNew, have, know)
}

func migratePostgres(ctx context.Context, db *DB, steps []string) error {
	for {
		tx, err := db.raw.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		// The same lock as write transactions: a migration never runs beside a write, or beside another migration.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, db.lockKey); err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version BIGINT NOT NULL)`); err != nil {
			_ = tx.Rollback()
			return err
		}
		var v int
		err = tx.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (0)`); err == nil {
				v = 0
			}
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if v > len(steps) {
			_ = tx.Rollback()
			return tooNew(v, len(steps))
		}
		if v == len(steps) {
			return tx.Commit()
		}
		if _, err := tx.ExecContext(ctx, steps[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrating schema %s to version %d: %w", db.schema, v+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = $1`, v+1); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
}

// Version returns the database's schema version.
func Version(ctx context.Context, db *DB) (int, error) {
	var v int
	if db.dialect == Postgres {
		err := db.raw.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return v, err
	}
	err := db.raw.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// DropSchema removes a Postgres schema and everything in it (tests, and removing a project).
func DropSchema(ctx context.Context, dsn, schema string) error {
	if !ValidSchema(schema) {
		return fmt.Errorf("bad schema name %q", schema)
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return err
	}
	raw := stdlib.OpenDB(*cfg)
	defer func() { _ = raw.Close() }()
	_, err = raw.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	return err
}

// IsPostgresURL reports whether a database setting names Postgres.
func IsPostgresURL(s string) bool {
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://")
}
