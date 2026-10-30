// Package accounts is the server store of `retake serve` (spec/server.md): the projects, the users and their
// project roles, one-time sign-in links, sessions, and a few settings. It runs on SQLite (DIR/server.db) or on
// Postgres (the schema "retake"), through internal/sqldb. Secrets (sign-in codes, session values) are stored only as
// their SHA-256.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/sqldb"
	"github.com/samahlstrom/retake/internal/team"
)

// Schema is the Postgres schema of the server store.
const Schema = "retake"

// Errors.
var (
	ErrNotFound  = errors.New("not found")
	ErrExists    = errors.New("already exists")
	ErrBadLink   = errors.New("this sign-in link has expired or was used")
	ErrNoSession = errors.New("no such session")
)

// Lifetimes (spec/server.md, "Sessions" and "Sign-in links").
const (
	SessionTTL     = 30 * 24 * time.Hour // a session's longest life
	IdleTTL        = 7 * 24 * time.Hour  // a session unused this long ends
	RotateEvery    = 24 * time.Hour      // a session's value is replaced this often
	RotateGrace    = 60 * time.Second    // the old value keeps working this long
	SeenEvery      = time.Minute         // last use is written at most this often
	DefaultLinkTTL = 24 * time.Hour
	MaxLinkTTL     = 7 * 24 * time.Hour
)

// Store is an open server store. It is safe for concurrent use.
type Store struct {
	db *sqldb.DB
	mu sync.Mutex // serializes this process's writes
	// Now tells the time; tests swap it.
	Now func() time.Time
}

// OpenSQLite opens (creating) the SQLite server store at path and migrates it.
func OpenSQLite(path string) (*Store, error) {
	db, err := sqldb.OpenSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("opening the server store: %w", err)
	}
	return start(context.Background(), db, migrations, path)
}

// OpenPostgres opens the server store in the "retake" schema of the Postgres database at dsn and migrates it.
func OpenPostgres(ctx context.Context, dsn string) (*Store, error) {
	db, err := sqldb.OpenPostgres(ctx, dsn, Schema, sqldb.PoolOptions{MaxOpen: 8, MaxIdle: 2})
	if err != nil {
		return nil, fmt.Errorf("opening the server store: %w", err)
	}
	return start(ctx, db, pgMigrations, "postgres schema "+Schema)
}

func start(ctx context.Context, db *sqldb.DB, steps []string, where string) (*Store, error) {
	if err := sqldb.Migrate(ctx, db, steps); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("server store %s: %w", where, err)
	}
	return &Store{db: db, Now: time.Now}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the store's database answers.
func (s *Store) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

// Dialect says which database holds the store.
func (s *Store) Dialect() sqldb.Dialect { return s.db.Dialect() }

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func (s *Store) now() time.Time { return s.Now().UTC() }

func stamp(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(v string) (time.Time, bool) {
	t, err := time.Parse(timeFormat, v)
	return t, err == nil
}

// write runs fn in one write transaction.
func (s *Store) write(fn func(tx *sqldb.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginWrite(context.Background())
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

type scanner interface{ Scan(dest ...any) error }

type querier interface {
	Query(q string, args ...any) (*sql.Rows, error)
	QueryRow(q string, args ...any) *sql.Row
}

// unique reports whether err is a unique-constraint violation, in either dialect.
func unique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "SQLSTATE 23505") ||
		strings.Contains(msg, "duplicate key value")
}

// ---- secrets

var secretRE = regexp.MustCompile(`^[a-z]{2}_[A-Za-z0-9_-]{43}$`)

// NewSecret returns a fresh secret (prefix, "_", 256 random bits in base64url) and its hash, the only part stored.
func NewSecret(prefix string) (secret, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("making a secret: %w", err)
	}
	secret = prefix + "_" + base64.RawURLEncoding.EncodeToString(b[:])
	return secret, Hash(secret), nil
}

// WellFormed reports whether s has the shape of a secret with the prefix.
func WellFormed(prefix, s string) bool {
	return strings.HasPrefix(s, prefix+"_") && secretRE.MatchString(s)
}

// Hash is the stored form of a secret: its SHA-256, hex.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Secret prefixes.
const (
	LinkPrefix    = "rs" // a sign-in code
	SessionPrefix = "ss" // a session cookie's value
)

// ---- projects

// Project is one row of the projects table.
type Project struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Created string `json:"created"`
}

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidName reports whether name can be a project's name (its /p/<name>/ address).
func ValidName(name string) bool { return nameRE.MatchString(name) }

const projectCols = `id, name, title, created`

func scanProject(r scanner) (Project, error) {
	var p Project
	err := r.Scan(&p.ID, &p.Name, &p.Title, &p.Created)
	return p, err
}

// CreateProject adds a project. A taken name gives ErrExists.
func (s *Store) CreateProject(name, title string) (Project, error) {
	if !ValidName(name) {
		return Project{}, fmt.Errorf("project name %q: use lower-case letters, digits and -, up to 63, starting with a "+
			"letter or digit", name)
	}
	p := Project{Name: name, Title: strings.TrimSpace(title), Created: stamp(s.now())}
	err := s.write(func(tx *sqldb.Tx) error {
		return tx.QueryRow(`INSERT INTO projects (name, title, created) VALUES (?, ?, ?) RETURNING id`, p.Name, p.Title,
			p.Created).Scan(&p.ID)
	})
	if unique(err) {
		return Project{}, fmt.Errorf("project %s: %w", name, ErrExists)
	}
	return p, err
}

// Project returns a project by name.
func (s *Store) Project(name string) (Project, error) {
	p, err := scanProject(s.db.QueryRow(`SELECT `+projectCols+` FROM projects WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return p, fmt.Errorf("project %s: %w", name, ErrNotFound)
	}
	return p, err
}

// Projects returns every project, by name.
func (s *Store) Projects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT ` + projectCols + ` FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- users

// User is one row of the users table.
type User struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Admin    bool   `json:"admin"`
	Created  string `json:"created"`
	Disabled string `json:"disabled,omitempty"` // "" = not disabled, else when
}

// Author is the name a user's takes carry: the name, else the email.
func (u User) Author() string {
	if strings.TrimSpace(u.Name) != "" {
		return strings.TrimSpace(u.Name)
	}
	return u.Email
}

// NormalizeEmail checks an address and lower-cases it.
func NormalizeEmail(email string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	a, err := mail.ParseAddress(e)
	if err != nil || a.Address != e || !strings.Contains(e, "@") || len(e) > 254 {
		return "", fmt.Errorf("%q is not an email address", email)
	}
	return e, nil
}

const userCols = `id, email, name, admin, created, disabled`

func scanUser(r scanner) (User, error) {
	var u User
	err := r.Scan(&u.ID, &u.Email, &u.Name, &u.Admin, &u.Created, &u.Disabled)
	return u, err
}

// CreateUser adds a user. A taken email gives ErrExists.
func (s *Store) CreateUser(email, name string, admin bool) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	u := User{Email: e, Name: strings.TrimSpace(name), Admin: admin, Created: stamp(s.now())}
	err = s.write(func(tx *sqldb.Tx) error {
		return tx.QueryRow(`INSERT INTO users (email, name, admin, created) VALUES (?, ?, ?, ?) RETURNING id`, u.Email,
			u.Name, u.Admin, u.Created).Scan(&u.ID)
	})
	if unique(err) {
		return User{}, fmt.Errorf("user %s: %w", e, ErrExists)
	}
	return u, err
}

// User returns a user by email.
func (s *Store) User(email string) (User, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE email = ?`, e))
	if errors.Is(err, sql.ErrNoRows) {
		return u, fmt.Errorf("user %s: %w", e, ErrNotFound)
	}
	return u, err
}

func userByID(q querier, id int64) (User, error) {
	u, err := scanUser(q.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, fmt.Errorf("user %d: %w", id, ErrNotFound)
	}
	return u, err
}

// Users returns every user, by email.
func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountUsers returns how many users there are.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUser changes a user's name and admin flag.
func (s *Store) UpdateUser(email, name string, admin bool) (User, error) {
	u, err := s.User(email)
	if err != nil {
		return u, err
	}
	err = s.write(func(tx *sqldb.Tx) error {
		_, err := tx.Exec(`UPDATE users SET name = ?, admin = ? WHERE id = ?`, strings.TrimSpace(name), admin, u.ID)
		return err
	})
	if err != nil {
		return u, err
	}
	return userByID(s.db, u.ID)
}

// SetDisabled disables a user (they can't sign in, and their sessions end) or enables them again.
func (s *Store) SetDisabled(email string, disabled bool) (User, error) {
	u, err := s.User(email)
	if err != nil {
		return u, err
	}
	when := ""
	if disabled {
		when = stamp(s.now())
	}
	err = s.write(func(tx *sqldb.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET disabled = ? WHERE id = ?`, when, u.ID); err != nil {
			return err
		}
		if disabled {
			_, err := tx.Exec(`UPDATE sessions SET revoked = ? WHERE user_id = ? AND revoked = ''`, when, u.ID)
			return err
		}
		return nil
	})
	if err != nil {
		return u, err
	}
	return userByID(s.db, u.ID)
}

// RemoveUser deletes a user, with their memberships, links and sessions.
func (s *Store) RemoveUser(email string) error {
	u, err := s.User(email)
	if err != nil {
		return err
	}
	return s.write(func(tx *sqldb.Tx) error {
		for _, q := range []string{`DELETE FROM sessions WHERE user_id = ?`, `DELETE FROM signin_links WHERE user_id = ?`,
			`DELETE FROM members WHERE user_id = ?`, `DELETE FROM users WHERE id = ?`} {
			if _, err := tx.Exec(q, u.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- members

// MemberRoles are the roles a member can have on a project.
var MemberRoles = []team.Role{team.Viewer, team.Reviewer, team.Approver, team.Owner}

// ParseRole parses a member role.
func ParseRole(s string) (team.Role, error) {
	for _, r := range MemberRoles {
		if string(r) == strings.ToLower(strings.TrimSpace(s)) {
			return r, nil
		}
	}
	return "", fmt.Errorf("%q is not a member role (viewer, reviewer, approver or owner)", s)
}

// Member is a user's role on a project.
type Member struct {
	User    User      `json:"user"`
	Role    team.Role `json:"role"`
	Created string    `json:"created"`
}

// SetMember gives a user a role on a project, replacing the one they had.
func (s *Store) SetMember(projectID, userID int64, role team.Role) error {
	if _, err := ParseRole(string(role)); err != nil {
		return err
	}
	return s.write(func(tx *sqldb.Tx) error {
		_, err := tx.Exec(`INSERT INTO members (project_id, user_id, role, created) VALUES (?, ?, ?, ?)
			ON CONFLICT (project_id, user_id) DO UPDATE SET role = excluded.role`, projectID, userID, string(role),
			stamp(s.now()))
		return err
	})
}

// RemoveMember takes a user off a project. Removing someone who isn't a member is ErrNotFound.
func (s *Store) RemoveMember(projectID, userID int64) error {
	return s.write(func(tx *sqldb.Tx) error {
		r, err := tx.Exec(`DELETE FROM members WHERE project_id = ? AND user_id = ?`, projectID, userID)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("member: %w", ErrNotFound)
		}
		return nil
	})
}

// Members returns a project's members, by email.
func (s *Store) Members(projectID int64) ([]Member, error) {
	rows, err := s.db.Query(`SELECT u.id, u.email, u.name, u.admin, u.created, u.disabled, m.role, m.created
		FROM members m JOIN users u ON u.id = m.user_id WHERE m.project_id = ? ORDER BY u.email`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Member{}
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.User.ID, &m.User.Email, &m.User.Name, &m.User.Admin, &m.User.Created, &m.User.Disabled,
			&role, &m.Created); err != nil {
			return nil, err
		}
		m.Role = team.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Role returns a user's role on a project: owner for an admin, else their membership's. ok is false when they
// aren't a member.
func (s *Store) Role(projectID int64, u User) (role team.Role, ok bool, err error) {
	if u.Admin {
		return team.Owner, true, nil
	}
	var r string
	err = s.db.QueryRow(`SELECT role FROM members WHERE project_id = ? AND user_id = ?`, projectID, u.ID).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return team.Role(r), true, nil
}

// ProjectRole is a project a user can see, with their role on it.
type ProjectRole struct {
	Project
	Role team.Role `json:"role"`
}

// ProjectsOf returns the projects a user can see, by name: every project for an admin (as owner), else the ones
// they are a member of.
func (s *Store) ProjectsOf(u User) ([]ProjectRole, error) {
	out := []ProjectRole{}
	if u.Admin {
		all, err := s.Projects()
		if err != nil {
			return nil, err
		}
		for _, p := range all {
			out = append(out, ProjectRole{p, team.Owner})
		}
		return out, nil
	}
	rows, err := s.db.Query(`SELECT p.id, p.name, p.title, p.created, m.role FROM members m
		JOIN projects p ON p.id = m.project_id WHERE m.user_id = ? ORDER BY p.name`, u.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pr ProjectRole
		var role string
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.Title, &pr.Created, &role); err != nil {
			return nil, err
		}
		pr.Role = team.Role(role)
		out = append(out, pr)
	}
	return out, rows.Err()
}

// ---- sign-in links

// CreateLink makes a one-time sign-in code for a user, valid for ttl (0 = DefaultLinkTTL, at most MaxLinkTTL).
func (s *Store) CreateLink(userID int64, ttl time.Duration) (code string, expires time.Time, err error) {
	if ttl <= 0 {
		ttl = DefaultLinkTTL
	}
	if ttl > MaxLinkTTL {
		return "", time.Time{}, fmt.Errorf("a sign-in link lasts at most %s", MaxLinkTTL)
	}
	code, hash, err := NewSecret(LinkPrefix)
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.now()
	expires = now.Add(ttl)
	err = s.write(func(tx *sqldb.Tx) error {
		if _, err := userByID(tx, userID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO signin_links (hash, user_id, created, expires) VALUES (?, ?, ?, ?)`, hash, userID,
			stamp(now), stamp(expires))
		return err
	})
	return code, expires, err
}

// link finds a usable code's user: ErrBadLink for a malformed, unknown, used or expired one, or a disabled user.
func (s *Store) link(q querier, code string) (User, error) {
	if !WellFormed(LinkPrefix, code) {
		return User{}, ErrBadLink
	}
	var userID int64
	var expires, used string
	err := q.QueryRow(`SELECT user_id, expires, used FROM signin_links WHERE hash = ?`, Hash(code)).Scan(&userID,
		&expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrBadLink
	}
	if err != nil {
		return User{}, err
	}
	exp, ok := parseTime(expires)
	if used != "" || !ok || !s.now().Before(exp) {
		return User{}, ErrBadLink
	}
	u, err := userByID(q, userID)
	if errors.Is(err, ErrNotFound) || err == nil && u.Disabled != "" {
		return User{}, ErrBadLink
	}
	return u, err
}

// PeekLink returns the user of a usable sign-in code without using it (the page with the Sign in button).
func (s *Store) PeekLink(code string) (User, error) { return s.link(s.db, code) }

// UseLink uses a sign-in code once and returns its user. A second use gives ErrBadLink.
func (s *Store) UseLink(code string) (User, error) {
	var u User
	err := s.write(func(tx *sqldb.Tx) error {
		var err error
		if u, err = s.link(tx, code); err != nil {
			return err
		}
		r, err := tx.Exec(`UPDATE signin_links SET used = ? WHERE hash = ? AND used = ''`, stamp(s.now()), Hash(code))
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && n != 1 {
			return ErrBadLink
		}
		return nil
	})
	return u, err
}

// ---- sessions

// Session is one row of the sessions table (never its value).
type Session struct {
	ID      int64  `json:"id"`
	UserID  int64  `json:"user_id"`
	Created string `json:"created"`
	Rotated string `json:"rotated"`
	Seen    string `json:"seen"`
	Expires string `json:"expires"`
	Revoked string `json:"revoked,omitempty"`
	Agent   string `json:"agent,omitempty"`
	IP      string `json:"ip,omitempty"`
}

const sessionCols = `id, user_id, created, rotated, seen, expires, revoked, agent, ip`

func scanSession(r scanner) (Session, error) {
	var x Session
	err := r.Scan(&x.ID, &x.UserID, &x.Created, &x.Rotated, &x.Seen, &x.Expires, &x.Revoked, &x.Agent, &x.IP)
	return x, err
}

// ExpiresAt returns when the session ends at the latest.
func (x Session) ExpiresAt() time.Time {
	t, _ := parseTime(x.Expires)
	return t
}

// StartSession starts a session for a user and returns the cookie's value (shown once).
func (s *Store) StartSession(userID int64, agent, ip string) (value string, sess Session, err error) {
	value, hash, err := NewSecret(SessionPrefix)
	if err != nil {
		return "", Session{}, err
	}
	now := s.now()
	if len(agent) > 200 {
		agent = agent[:200]
	}
	sess = Session{UserID: userID, Created: stamp(now), Rotated: stamp(now), Seen: stamp(now),
		Expires: stamp(now.Add(SessionTTL)), Agent: agent, IP: ip}
	err = s.write(func(tx *sqldb.Tx) error {
		return tx.QueryRow(`INSERT INTO sessions (hash, user_id, created, rotated, seen, expires, agent, ip)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, hash, userID, sess.Created, sess.Rotated, sess.Seen,
			sess.Expires, agent, ip).Scan(&sess.ID)
	})
	return value, sess, err
}

// CheckSession looks up a session cookie's value. It returns the session and its user, or ErrNoSession for a
// malformed, unknown, expired (absolutely or idle), revoked or rotated-away value, or a disabled user. When the
// session is due for rotation, it gets a new value, returned as rotated (send it as the new cookie); the old value
// keeps working for RotateGrace.
func (s *Store) CheckSession(value string) (sess Session, u User, rotated string, err error) {
	if !WellFormed(SessionPrefix, value) {
		return Session{}, User{}, "", ErrNoSession
	}
	hash := Hash(value)
	now := s.now()
	err = s.write(func(tx *sqldb.Tx) error {
		rotated = ""
		var current bool
		var prevUntil string
		row := tx.QueryRow(`SELECT `+sessionCols+`, hash = ?, prev_until FROM sessions WHERE hash = ? OR prev_hash = ?`,
			hash, hash, hash)
		err := row.Scan(&sess.ID, &sess.UserID, &sess.Created, &sess.Rotated, &sess.Seen, &sess.Expires, &sess.Revoked,
			&sess.Agent, &sess.IP, &current, &prevUntil)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoSession
		}
		if err != nil {
			return err
		}
		if !current {
			if until, ok := parseTime(prevUntil); !ok || !now.Before(until) {
				return ErrNoSession
			}
		}
		seen, _ := parseTime(sess.Seen)
		if sess.Revoked != "" || !now.Before(sess.ExpiresAt()) || now.Sub(seen) >= IdleTTL {
			return ErrNoSession
		}
		if u, err = userByID(tx, sess.UserID); err != nil {
			if errors.Is(err, ErrNotFound) {
				return ErrNoSession
			}
			return err
		}
		if u.Disabled != "" {
			return ErrNoSession
		}
		if r, ok := parseTime(sess.Rotated); current && (!ok || now.Sub(r) >= RotateEvery) {
			value, newHash, err := NewSecret(SessionPrefix)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE sessions SET prev_hash = hash, prev_until = ?, hash = ?, rotated = ?, seen = ?
				WHERE id = ?`, stamp(now.Add(RotateGrace)), newHash, stamp(now), stamp(now), sess.ID); err != nil {
				return err
			}
			sess.Rotated, sess.Seen, rotated = stamp(now), stamp(now), value
			return nil
		}
		if now.Sub(seen) >= SeenEvery {
			if _, err := tx.Exec(`UPDATE sessions SET seen = ? WHERE id = ?`, stamp(now), sess.ID); err != nil {
				return err
			}
			sess.Seen = stamp(now)
		}
		return nil
	})
	if err != nil {
		return Session{}, User{}, "", err
	}
	return sess, u, rotated, nil
}

// EndSession revokes one session (sign out).
func (s *Store) EndSession(id int64) error {
	return s.write(func(tx *sqldb.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET revoked = ? WHERE id = ? AND revoked = ''`, stamp(s.now()), id)
		return err
	})
}

// EndSessions revokes every live session of a user and returns how many.
func (s *Store) EndSessions(userID int64) (int, error) {
	var n int64
	err := s.write(func(tx *sqldb.Tx) error {
		r, err := tx.Exec(`UPDATE sessions SET revoked = ? WHERE user_id = ? AND revoked = ''`, stamp(s.now()), userID)
		if err != nil {
			return err
		}
		n, err = r.RowsAffected()
		return err
	})
	return int(n), err
}

// Sessions returns a user's sessions that haven't ended, newest first.
func (s *Store) Sessions(userID int64) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions WHERE user_id = ? AND revoked = '' AND expires > ?
		ORDER BY id DESC`, userID, stamp(s.now()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Session{}
	now := s.now()
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		if seen, ok := parseTime(x.Seen); ok && now.Sub(seen) >= IdleTTL {
			continue
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Prune deletes sessions and sign-in links that ended more than a day ago, and returns how many rows went.
func (s *Store) Prune() (int, error) {
	cut := stamp(s.now().Add(-24 * time.Hour))
	idle := stamp(s.now().Add(-IdleTTL - 24*time.Hour))
	var n int64
	err := s.write(func(tx *sqldb.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM sessions WHERE expires < ? OR (revoked <> '' AND revoked < ?) OR seen < ?`, []any{cut, cut, idle}},
			{`DELETE FROM signin_links WHERE expires < ? OR (used <> '' AND used < ?)`, []any{cut, cut}},
		} {
			r, err := tx.Exec(q.sql, q.args...)
			if err != nil {
				return err
			}
			k, _ := r.RowsAffected()
			n += k
		}
		return nil
	})
	return int(n), err
}

// ---- settings

// SettingPublicURL is the settings key of the public URL the server last ran with, for the CLI's links.
const SettingPublicURL = "public_url"

// Setting returns a setting's value ("" when unset).
func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting sets a setting.
func (s *Store) SetSetting(key, value string) error {
	return s.write(func(tx *sqldb.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
}
