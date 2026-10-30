package project

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The tokens and hosts tables of spec/team.md. The store keeps only a token's hash; making and checking tokens is
// internal/team's job.

// Token is one row of the tokens table.
type Token struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Label   string `json:"label"`
	Created string `json:"created"`
	Expires string `json:"expires"` // "" = never
	Revoked string `json:"revoked"` // "" = not revoked
}

// Token states.
const (
	TokenActive  = "active"
	TokenExpired = "expired"
	TokenRevoked = "revoked"
)

// State says whether the token works at now: active, expired or revoked.
func (t Token) State(now time.Time) string {
	if t.Revoked != "" {
		return TokenRevoked
	}
	if exp, ok := t.ExpiresAt(); ok && !now.Before(exp) {
		return TokenExpired
	}
	return TokenActive
}

// Valid reports whether the token works at now.
func (t Token) Valid(now time.Time) bool { return t.State(now) == TokenActive }

// ExpiresAt returns when the token expires; ok is false when it never does. A garbled expiry reads as the zero
// time, so the token counts as expired.
func (t Token) ExpiresAt() (time.Time, bool) {
	if t.Expires == "" {
		return time.Time{}, false
	}
	exp, err := ParseTime(t.Expires)
	if err != nil {
		return time.Time{}, true // a zero time: already past
	}
	return exp, true
}

const tokenCols = `id, role, label, created, expires, revoked` //nolint:gosec // column names, not a credential

func scanToken(r scanner) (Token, error) {
	var t Token
	err := r.Scan(&t.ID, &t.Role, &t.Label, &t.Created, &t.Expires, &t.Revoked)
	return t, err
}

// NewToken is a token to store: its hash (never the token), role, label and lifetime.
type NewToken struct {
	Hash  string
	Role  string
	Label string
	TTL   time.Duration // 0 = never expires
}

// CreateToken stores a token and returns its row.
func (s *Store) CreateToken(n NewToken) (Token, error) {
	if n.Hash == "" {
		return Token{}, errors.New("a token needs its hash")
	}
	now := s.Now().UTC()
	t := Token{Role: n.Role, Label: n.Label, Created: now.Format(timeFormat)}
	if n.TTL > 0 {
		t.Expires = now.Add(n.TTL).Format(timeFormat)
	}
	err := s.write(func(tx *tx) error {
		res, err := tx.Exec(`INSERT INTO tokens (hash, role, label, created, expires) VALUES (?, ?, ?, ?, ?)`,
			n.Hash, t.Role, t.Label, t.Created, t.Expires)
		if err != nil {
			return err
		}
		t.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Token{}, fmt.Errorf("storing the token: %w", err)
	}
	return t, nil
}

// TokenByHash returns the token with that hash, valid or not. An unknown hash gives ErrNotFound.
func (s *Store) TokenByHash(hash string) (Token, error) {
	t, err := scanToken(s.db.QueryRow(`SELECT `+tokenCols+` FROM tokens WHERE hash = ?`, hash))
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("token: %w", ErrNotFound)
	}
	return t, err
}

// Tokens returns every token, oldest first.
func (s *Store) Tokens() ([]Token, error) {
	rows, err := s.db.Query(`SELECT ` + tokenCols + ` FROM tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken revokes a token from now on. Revoking it again keeps the first time.
func (s *Store) RevokeToken(id int64) (Token, error) {
	var t Token
	err := s.write(func(tx *tx) error {
		if _, err := tx.Exec(`UPDATE tokens SET revoked = ? WHERE id = ? AND revoked = ''`, s.stamp(), id); err != nil {
			return err
		}
		var err error
		t, err = scanToken(tx.QueryRow(`SELECT `+tokenCols+` FROM tokens WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("token %d: %w", id, ErrNotFound)
		}
		return err
	})
	return t, err
}

// Host is one row of the hosts table: the public hostname of a running share.
type Host struct {
	Host    string `json:"host"`
	Tunnel  string `json:"tunnel"`
	PID     int    `json:"pid"`
	Created string `json:"created"`
}

// AddHost records a share's hostname (lower-cased), replacing an earlier row for it.
func (s *Store) AddHost(host, tunnel string, pid int) error {
	host = strings.ToLower(host)
	return s.write(func(tx *tx) error {
		_, err := tx.Exec(`INSERT INTO hosts (host, tunnel, pid, created) VALUES (?, ?, ?, ?)
			ON CONFLICT (host) DO UPDATE SET tunnel = excluded.tunnel, pid = excluded.pid, created = excluded.created`,
			host, tunnel, pid, s.stamp())
		return err
	})
}

// RemoveHost forgets a hostname, but only the row of that process (a newer share of the same host keeps its own).
// pid 0 removes it whoever added it.
func (s *Store) RemoveHost(host string, pid int) error {
	host = strings.ToLower(host)
	return s.write(func(tx *tx) error {
		_, err := tx.Exec(`DELETE FROM hosts WHERE host = ? AND (? = 0 OR pid = ?)`, host, pid, pid)
		return err
	})
}

// HostAllowed reports whether a hostname belongs to a share.
func (s *Store) HostAllowed(host string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM hosts WHERE host = ?`, strings.ToLower(host)).Scan(&n)
	return n > 0, err
}

// Hosts returns the shares' hostnames, newest first.
func (s *Store) Hosts() ([]Host, error) {
	rows, err := s.db.Query(`SELECT host, tunnel, pid, created FROM hosts ORDER BY created DESC, host`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Host{}
	for rows.Next() {
		var h Host
		if err := rows.Scan(&h.Host, &h.Tunnel, &h.PID, &h.Created); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
