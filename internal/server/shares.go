package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// Sharing a video from the page (spec/team.md, "Sharing a video from the page"): the owner makes, lists and revokes the
// video's share links. A link's token is kept in the video's own store, so it opens this video only.

// Reach is how a share link reaches people (spec/team.md, "How a link reaches people").
type Reach struct {
	Kind string `json:"kind"` // hosted, tunnel or lan
	Host string `json:"host"` // the host it uses; "" while a tunnel would be started
	Note string `json:"note"` // one plain line on reaching it from outside, or ""
	// Base is what a link is made of: <Base>s/<token>. Ready fills it in.
	Base string `json:"-"`
}

// Sharing gives a Server's share links their address: the team server's own, a tunnel's, or the local network's.
type Sharing interface {
	// Reach says what a new link would use, without starting anything.
	Reach(r *http.Request) Reach
	// Ready returns the address to use, starting what it needs (a tunnel) within ctx.
	Ready(ctx context.Context, r *http.Request) (Reach, error)
}

// ShareWait is how long making a link waits for its address (a tunnel's hostname).
const ShareWait = 60 * time.Second

// ShareLink is one of the video's links, as the share sheet lists it.
type ShareLink struct {
	ID      int64  `json:"id"`
	Role    string `json:"role"`
	Label   string `json:"label"`
	Created string `json:"created"`
	Expires string `json:"expires"` // "" = never
}

// shareExpiry is the sheet's choices of how long a link works (0 = never).
var shareExpiry = map[string]time.Duration{"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "never": 0}

// maxShareLabel is the longest name a link may carry.
const maxShareLabel = 80

// sharesRoute is GET and POST api/shares, and POST api/shares/<id>/revoke. Only the owner reaches it (permFor).
func (s *Server) sharesRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method == http.MethodPost && !s.originOK(r) {
		s.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	switch {
	case len(parts) == 2 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		s.listShares(w, r)
	case len(parts) == 2 && r.Method == http.MethodPost:
		s.makeShare(w, r)
	case len(parts) == 4 && parts[3] == "revoke" && r.Method == http.MethodPost:
		s.revokeShare(w, r, parts[2])
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}

// shareable reports whether a token is one of the video's share links: a person's (not an agent's), for this video.
func shareable(t project.Token) bool {
	return t.Role != string(team.Agent) && t.Scope == ""
}

func (s *Server) listShares(w http.ResponseWriter, r *http.Request) {
	links := []ShareLink{}
	st, err := s.store(false)
	if err != nil && !errors.Is(err, project.ErrNoProject) {
		s.storeFailed(w, r, err)
		return
	}
	if st != nil {
		rows, err := st.Tokens()
		if err != nil {
			s.storeFailed(w, r, err)
			return
		}
		now := s.opts.Clock.Now()
		for _, t := range slices.Backward(rows) {
			if shareable(t) && t.Valid(now) {
				links = append(links, ShareLink{ID: t.ID, Role: t.Role, Label: t.Label, Created: t.Created,
					Expires: t.Expires})
			}
		}
	}
	reach := Reach{}
	if s.opts.Sharing != nil {
		reach = s.opts.Sharing.Reach(r)
	}
	s.json(w, r, http.StatusOK, map[string]any{"links": links, "reach": reach})
}

// cleanLabel checks a link's name: trimmed, at most maxShareLabel characters, no control characters.
func cleanLabel(label string) (string, bool) {
	label = strings.TrimSpace(label)
	if len([]rune(label)) > maxShareLabel {
		return "", false
	}
	return label, !strings.ContainsFunc(label, unicode.IsControl)
}

func (s *Server) makeShare(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Label   string `json:"label"`
		Role    string `json:"role"`
		Expires string `json:"expires"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b) != nil {
		s.fail(w, r, http.StatusBadRequest, "invalid request")
		return
	}
	if b.Role == "" {
		b.Role = string(team.Reviewer)
	}
	if b.Expires == "" {
		b.Expires = "7d"
	}
	ttl, okTTL := shareExpiry[b.Expires]
	label, okLabel := cleanLabel(b.Label)
	switch {
	case b.Role != string(team.Reviewer) && b.Role != string(team.Viewer):
		s.fail(w, r, http.StatusBadRequest, "role must be reviewer or viewer")
		return
	case !okTTL:
		s.fail(w, r, http.StatusBadRequest, "expires must be 7d, 30d or never")
		return
	case !okLabel:
		s.fail(w, r, http.StatusBadRequest, "the name is too long or has control characters")
		return
	case s.opts.Sharing == nil:
		s.fail(w, r, http.StatusServiceUnavailable, "this server can't make links")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), ShareWait)
	defer cancel()
	reach, err := s.opts.Sharing.Ready(ctx, r)
	if err != nil {
		s.fail(w, r, http.StatusServiceUnavailable, err.Error())
		return
	}
	st, err := s.store(true)
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	tok, hash, err := team.NewToken()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "making the link failed")
		return
	}
	row, err := st.CreateToken(project.NewToken{Hash: hash, Role: b.Role, Label: label, TTL: ttl})
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	link := ShareLink{ID: row.ID, Role: row.Role, Label: row.Label, Created: row.Created, Expires: row.Expires}
	s.json(w, r, http.StatusCreated, map[string]any{"url": reach.Base + "s/" + tok, "link": link, "reach": reach})
}

func (s *Server) revokeShare(w http.ResponseWriter, r *http.Request, idText string) {
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		s.fail(w, r, http.StatusNotFound, "no such link")
		return
	}
	st, err := s.store(false)
	if errors.Is(err, project.ErrNoProject) {
		s.fail(w, r, http.StatusNotFound, "no such link")
		return
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	rows, err := st.Tokens()
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	i := slices.IndexFunc(rows, func(t project.Token) bool { return t.ID == id })
	if i < 0 || !shareable(rows[i]) {
		s.fail(w, r, http.StatusNotFound, "no such link")
		return
	}
	if _, err := st.RevokeToken(id); err != nil {
		s.storeFailed(w, r, err)
		return
	}
	s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
}
