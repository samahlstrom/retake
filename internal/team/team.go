// Package team is the vocabulary of team mode (spec/team.md): roles and what they may do, share tokens (made here,
// stored as hashes by internal/project), and the limiter for failed token attempts.
package team

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Role is who a request acts as.
type Role string

// The roles. Owner is not a token role: it is a local request without a token.
const (
	Viewer   Role = "viewer"
	Reviewer Role = "reviewer"
	Approver Role = "approver"
	Agent    Role = "agent"
	Owner    Role = "owner"
)

// TokenRoles are the roles a token can have.
var TokenRoles = []Role{Viewer, Reviewer, Approver, Agent}

// ParseRole parses a token role.
func ParseRole(s string) (Role, error) {
	for _, r := range TokenRoles {
		if string(r) == strings.ToLower(strings.TrimSpace(s)) {
			return r, nil
		}
	}
	return "", fmt.Errorf("%q is not a role (viewer, reviewer, approver or agent)", s)
}

// Perm is what a route needs.
type Perm int

// The permissions, from spec/team.md's route table.
const (
	PermAny     Perm = iota // any role: /api/ping
	PermWatch               // the page, the videos, the reads
	PermRecord              // take writes
	PermApprove             // approve a version, reopen a note
	PermAgent               // /mcp
	PermManage              // owner-only worker assignment and execution controls
)

func (p Perm) String() string {
	switch p {
	case PermAny:
		return "any"
	case PermWatch:
		return "watch"
	case PermRecord:
		return "record"
	case PermApprove:
		return "approve"
	case PermAgent:
		return "agent"
	case PermManage:
		return "manage"
	}
	return fmt.Sprintf("perm(%d)", int(p))
}

// Can reports whether role r may do what p needs.
func (r Role) Can(p Perm) bool {
	switch p {
	case PermAny:
		return r != ""
	case PermWatch:
		return r == Viewer || r == Reviewer || r == Approver || r == Owner
	case PermRecord:
		return r == Reviewer || r == Approver || r == Owner
	case PermApprove:
		return r == Approver || r == Owner
	case PermAgent:
		return r == Agent
	case PermManage:
		return r == Owner
	}
	return false
}

// tokenPrefix marks retake tokens, so secret scanners and people can tell what one is.
const tokenPrefix = "rt_"

// tokenRE is the shape of a token: the prefix and 32 bytes in unpadded base64url.
var tokenRE = regexp.MustCompile(`^rt_[A-Za-z0-9_-]{43}$`)

// NewToken returns a fresh token (256 random bits) and its hash, the only part to store.
func NewToken() (token, hash string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("making a token: %w", err)
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
	return token, Hash(token), nil
}

// WellFormed reports whether s has the shape of a token (checked before any lookup).
func WellFormed(s string) bool { return tokenRE.MatchString(s) }

// Hash is the stored form of a token: its SHA-256, hex.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
