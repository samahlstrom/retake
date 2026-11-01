package accounts

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/samahlstrom/retake/internal/sqldb"
)

// The first-run owner and machine tokens (spec/server.md, "First run: the owner" and "Machines").

// Secret prefixes of the first run and of machines.
const (
	SetupPrefix   = "su" // the first-run setup code
	MachinePrefix = "rk" // a machine token: retake connect
)

// ErrSetUp is CreateOwner's answer once the server has a user.
var ErrSetUp = errors.New("this server is already set up")

// ErrBadMachine is a machine token that is malformed, unknown, revoked, or whose admin is gone.
var ErrBadMachine = errors.New("bad machine token")

// CreateOwner adds the server's first user, an admin, in the same transaction that checks there is no user yet.
// A second call gives ErrSetUp.
func (s *Store) CreateOwner(email, name string) (User, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}
	u := User{Email: e, Name: strings.TrimSpace(name), Admin: true, Created: stamp(s.now())}
	err = s.write(func(tx *sqldb.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM users`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrSetUp
		}
		return tx.QueryRow(`INSERT INTO users (email, name, admin, created) VALUES (?, ?, ?, ?) RETURNING id`, u.Email,
			u.Name, u.Admin, u.Created).Scan(&u.ID)
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// MachineToken is one row of the machine_tokens table (never its value).
type MachineToken struct {
	ID        int64  `json:"id"`
	CreatedBy int64  `json:"created_by"`
	Created   string `json:"created"`
	Used      string `json:"used"`
	Revoked   string `json:"revoked"`
}

const machineCols = `id, created_by, created, used, revoked`

func scanMachine(r scanner) (MachineToken, error) {
	var m MachineToken
	err := r.Scan(&m.ID, &m.CreatedBy, &m.Created, &m.Used, &m.Revoked)
	return m, err
}

// CreateMachineToken makes a machine token for an admin and returns its value, the only time it is shown.
func (s *Store) CreateMachineToken(userID int64) (string, MachineToken, error) {
	tok, hash, err := NewSecret(MachinePrefix)
	if err != nil {
		return "", MachineToken{}, err
	}
	m := MachineToken{CreatedBy: userID, Created: stamp(s.now())}
	err = s.write(func(tx *sqldb.Tx) error {
		u, err := userByID(tx, userID)
		if err != nil {
			return err
		}
		if !u.Admin || u.Disabled != "" {
			return fmt.Errorf("user %s isn't an enabled admin", u.Email)
		}
		return tx.QueryRow(`INSERT INTO machine_tokens (hash, created_by, created) VALUES (?, ?, ?) RETURNING id`, hash,
			userID, m.Created).Scan(&m.ID)
	})
	if err != nil {
		return "", MachineToken{}, err
	}
	return tok, m, nil
}

// CheckMachineToken returns a usable machine token's row: not revoked, and made by an admin who still exists, is
// enabled and is still an admin. Anything else gives ErrBadMachine. Its last use is written at most once a minute.
func (s *Store) CheckMachineToken(token string) (MachineToken, error) {
	if !WellFormed(MachinePrefix, token) {
		return MachineToken{}, ErrBadMachine
	}
	m, err := scanMachine(s.db.QueryRow(`SELECT `+machineCols+` FROM machine_tokens WHERE hash = ?`, Hash(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return MachineToken{}, ErrBadMachine
	}
	if err != nil {
		return MachineToken{}, err
	}
	if m.Revoked != "" {
		return MachineToken{}, ErrBadMachine
	}
	u, err := userByID(s.db, m.CreatedBy)
	if errors.Is(err, ErrNotFound) || err == nil && (!u.Admin || u.Disabled != "") {
		return MachineToken{}, ErrBadMachine
	}
	if err != nil {
		return MachineToken{}, err
	}
	now := s.now()
	if last, ok := parseTime(m.Used); !ok || now.Sub(last) >= SeenEvery {
		m.Used = stamp(now)
		if err := s.write(func(tx *sqldb.Tx) error {
			_, err := tx.Exec(`UPDATE machine_tokens SET used = ? WHERE id = ?`, m.Used, m.ID)
			return err
		}); err != nil {
			return MachineToken{}, err
		}
	}
	return m, nil
}

// MachineTokens lists the machine tokens, oldest first.
func (s *Store) MachineTokens() ([]MachineToken, error) {
	rows, err := s.db.Query(`SELECT ` + machineCols + ` FROM machine_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MachineToken{}
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RevokeMachineToken ends a machine token now. An unknown id gives ErrNotFound; revoking twice keeps the first time.
func (s *Store) RevokeMachineToken(id int64) (MachineToken, error) {
	var m MachineToken
	err := s.write(func(tx *sqldb.Tx) error {
		var err error
		m, err = scanMachine(tx.QueryRow(`SELECT `+machineCols+` FROM machine_tokens WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("machine token %d: %w", id, ErrNotFound)
		}
		if err != nil || m.Revoked != "" {
			return err
		}
		m.Revoked = stamp(s.now())
		_, err = tx.Exec(`UPDATE machine_tokens SET revoked = ? WHERE id = ?`, m.Revoked, id)
		return err
	})
	return m, err
}
