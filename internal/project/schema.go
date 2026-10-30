package project

import (
	"context"
	"database/sql"
	"fmt"
)

// migrations are the schema, one step per version: the store at schema version n has run migrations[:n]. They
// are forward-only: never edit or reorder a step that shipped; append a new one. PRAGMA user_version holds n.
var migrations = []string{
	// 1: the five tables of spec/notes.md.
	`
CREATE TABLE notes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT, -- the project-wide note number: #1, #2, ...
	key         TEXT NOT NULL UNIQUE,              -- <take>/<seg>/<n>, stable across notes rebuilds
	version     TEXT NOT NULL,                     -- the label of the version the take was recorded on
	take        TEXT NOT NULL,
	seg         TEXT NOT NULL,
	t0          REAL NOT NULL,
	t1          REAL NOT NULL,
	text        TEXT NOT NULL,
	author      TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'open'
	            CHECK (status IN ('open', 'question', 'answered', 'accepted', 'retracted')),
	answered_in TEXT NOT NULL DEFAULT '',          -- the version that answered it
	change      TEXT NOT NULL DEFAULT '',          -- that version's one-line change for it
	detail      TEXT NOT NULL DEFAULT '{}',        -- JSON from the notes engine: {strokes, snapshot}
	created     TEXT NOT NULL,
	updated     TEXT NOT NULL
);
CREATE INDEX notes_version ON notes (version, status);
CREATE INDEX notes_take ON notes (take);

CREATE TABLE threads (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	note      INTEGER REFERENCES notes (id),       -- null: a question about the version as a whole
	version   TEXT NOT NULL,
	t         REAL,
	kind      TEXT NOT NULL CHECK (kind IN ('question', 'answer')),
	by        TEXT NOT NULL,                       -- 'agent' or the author's name
	text      TEXT NOT NULL,
	options   TEXT NOT NULL DEFAULT '[]',          -- JSON list of version labels
	answer_to INTEGER REFERENCES threads (id),     -- an answer: the question it answers
	key       TEXT UNIQUE,                         -- an answer: the <take>/<seg>/<n> it was recorded in
	created   TEXT NOT NULL
);
CREATE INDEX threads_note ON threads (note);
CREATE INDEX threads_answer_to ON threads (answer_to);

CREATE TABLE versions (
	label     TEXT PRIMARY KEY,
	path      TEXT NOT NULL,
	sha256    TEXT NOT NULL,
	parent    TEXT NOT NULL DEFAULT '',
	by        TEXT NOT NULL DEFAULT '',
	created   TEXT NOT NULL,
	changelog TEXT NOT NULL DEFAULT '[]'           -- JSON [{note, summary}]
);

CREATE TABLE jobs (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	kind     TEXT NOT NULL,                        -- transcribe | notes
	args     TEXT NOT NULL DEFAULT '{}',           -- JSON
	state    TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'done', 'failed')),
	attempts INTEGER NOT NULL DEFAULT 0,
	error    TEXT NOT NULL DEFAULT '',
	hint     TEXT NOT NULL DEFAULT '',          -- a failed job's one-line fix (how to set up transcription, say)
	created  TEXT NOT NULL,
	updated  TEXT NOT NULL
);
CREATE INDEX jobs_state ON jobs (kind, state);

CREATE TABLE events (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,     -- monotonic, never reused
	kind    TEXT NOT NULL,                         -- notes | job | version
	data    TEXT NOT NULL DEFAULT '{}',            -- JSON
	created TEXT NOT NULL
);
`,
	// 2: sharing (spec/team.md): tokens, stored as hashes, and the hostnames of running shares.
	`
CREATE TABLE tokens (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,     -- what share list shows and share revoke takes
	hash    TEXT NOT NULL UNIQUE,                  -- SHA-256 of the token, hex; the token itself is never stored
	role    TEXT NOT NULL CHECK (role IN ('viewer', 'reviewer', 'approver', 'agent')),
	label   TEXT NOT NULL DEFAULT '',              -- who it's for
	created TEXT NOT NULL,
	expires TEXT NOT NULL DEFAULT '',              -- '' = never
	revoked TEXT NOT NULL DEFAULT ''               -- '' = not revoked, else when
);

CREATE TABLE hosts (
	host    TEXT PRIMARY KEY,                      -- a share's public hostname, lower case
	tunnel  TEXT NOT NULL DEFAULT '',              -- cloudflare | tailscale | none
	pid     INTEGER NOT NULL DEFAULT 0,            -- the retake share process that added it
	created TEXT NOT NULL
);
`,
}

// SchemaVersion is the schema version this build writes.
var SchemaVersion = len(migrations)

// migrate brings the database up to len(ms), one transaction per step. A database newer than this build is
// refused rather than touched.
func migrate(db *sql.DB, ms []string) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
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
		if v > len(ms) {
			_ = tx.Rollback()
			return fmt.Errorf("the store has schema version %d, newer than this retake knows (%d): update retake", v,
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
