package hub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// The server's JSON routes (spec/server.md, "Server routes"): who am I, projects, members, tokens and users. They
// need a session; admins and project owners manage things.

func (h *Hub) apiRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, parts []string) {
	if len(parts) >= 1 && parts[0] == "machine" { // a machine token, never a session
		h.machineRoute(w, r, ri, parts[1:])
		return
	}
	s, err := h.session(w, r)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if s == nil {
		h.fail(w, r, http.StatusUnauthorized, "sign in required")
		return
	}
	get, post, del := r.Method == http.MethodGet || r.Method == http.MethodHead, r.Method == http.MethodPost,
		r.Method == http.MethodDelete
	switch {
	case len(parts) >= 1 && parts[0] == "ext":
		h.homeExt(w, r, s, append([]string{"api"}, parts...))
	case len(parts) == 2 && parts[0] == "me" && parts[1] == "extensions" && (get || r.Method == http.MethodPut):
		h.meExtensions(w, r, s)
	case len(parts) == 1 && parts[0] == "me" && get:
		ps, err := h.st.Accounts.ProjectsOf(s.user)
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		h.json(w, r, http.StatusOK, map[string]any{"email": s.user.Email, "name": s.user.Name, "admin": s.user.Admin,
			"projects": ps})
	case len(parts) == 1 && parts[0] == "projects" && get:
		h.listProjects(w, r, s)
	case len(parts) == 1 && parts[0] == "home" && get:
		h.apiHome(w, r, s)
	case len(parts) == 1 && parts[0] == "settings":
		h.settingsRoute(w, r, s)
	case len(parts) == 1 && parts[0] == "projects" && post:
		h.createProject(w, r, s)
	case len(parts) >= 3 && parts[0] == "projects" && parts[2] == "members":
		p, ok := h.ownedProject(w, r, s, parts[1])
		if !ok {
			return
		}
		switch {
		case len(parts) == 3 && get:
			ms, err := h.st.Accounts.Members(p.ID)
			if err != nil {
				h.storeFailed(w, r, err)
				return
			}
			h.json(w, r, http.StatusOK, map[string]any{"members": ms})
		case len(parts) == 3 && post:
			h.setMember(w, r, p)
		case len(parts) == 4 && del:
			h.removeMember(w, r, p, parts[3])
		default:
			h.notFound(w, r)
		}
	case len(parts) >= 3 && parts[0] == "projects" && parts[2] == "tokens":
		row, ok := h.ownedProject(w, r, s, parts[1])
		if !ok {
			return
		}
		p, err := h.project(row.Name)
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		switch {
		case len(parts) == 3 && get:
			ts, err := p.store.Tokens()
			if err != nil {
				h.storeFailed(w, r, err)
				return
			}
			h.json(w, r, http.StatusOK, map[string]any{"tokens": ts})
		case len(parts) == 3 && post:
			h.createToken(w, r, ri, p)
		case len(parts) == 4 && del:
			id, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil {
				h.notFound(w, r)
				return
			}
			t, err := p.store.RevokeToken(id)
			if errors.Is(err, project.ErrNotFound) {
				h.fail(w, r, http.StatusNotFound, "no such token")
				return
			}
			if err != nil {
				h.storeFailed(w, r, err)
				return
			}
			h.json(w, r, http.StatusOK, t)
		default:
			h.notFound(w, r)
		}
	case len(parts) >= 1 && parts[0] == "connect":
		h.connectRoute(w, r, ri, s, parts[1:])
	case len(parts) >= 1 && parts[0] == "users":
		if !s.user.Admin {
			h.fail(w, r, http.StatusForbidden, "admins only")
			return
		}
		h.usersRoute(w, r, ri, parts[1:])
	default:
		h.notFound(w, r)
	}
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(v)
}

func (h *Hub) listProjects(w http.ResponseWriter, r *http.Request, s *session) {
	ps, err := h.st.Accounts.ProjectsOf(s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	type item struct {
		accounts.ProjectRole
		Versions int `json:"versions"`
	}
	out := make([]item, 0, len(ps))
	for _, pr := range ps {
		n := 0
		if p, err := h.project(pr.Name); err == nil {
			if vs, err := p.store.Versions(); err == nil {
				n = len(vs)
			}
		}
		out = append(out, item{pr, n})
	}
	h.json(w, r, http.StatusOK, map[string]any{"projects": out})
}

func (h *Hub) createProject(w http.ResponseWriter, r *http.Request, s *session) {
	if !s.user.Admin {
		h.fail(w, r, http.StatusForbidden, "admins only")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := readJSON(r, &req); err != nil {
		h.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	p, err := h.st.CreateProject(r.Context(), req.Name, req.Title)
	switch {
	case errors.Is(err, accounts.ErrExists):
		h.fail(w, r, http.StatusConflict, "a project with that name exists")
	case err != nil && !accounts.ValidName(req.Name):
		h.fail(w, r, http.StatusBadRequest, err.Error())
	case err != nil:
		h.storeFailed(w, r, err)
	default:
		h.json(w, r, http.StatusCreated, p)
	}
}

// ownedProject finds a project the session's user owns (admins own every project). It answers 404 or 403 itself.
func (h *Hub) ownedProject(w http.ResponseWriter, r *http.Request, s *session, name string) (accounts.Project, bool) {
	p, err := h.st.Accounts.Project(name)
	if errors.Is(err, accounts.ErrNotFound) {
		h.fail(w, r, http.StatusNotFound, "no such project")
		return p, false
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return p, false
	}
	role, ok, err := h.st.Accounts.Role(p.ID, s.user)
	if err != nil {
		h.storeFailed(w, r, err)
		return p, false
	}
	if !ok || role != team.Owner {
		h.fail(w, r, http.StatusForbidden, "owners only")
		return p, false
	}
	return p, true
}

func (h *Hub) setMember(w http.ResponseWriter, r *http.Request, p accounts.Project) {
	var req struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := readJSON(r, &req); err != nil {
		h.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	role, err := accounts.ParseRole(req.Role)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	u, err := h.st.Accounts.User(req.Email)
	if errors.Is(err, accounts.ErrNotFound) {
		h.fail(w, r, http.StatusNotFound, "no such user: an admin adds people first")
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if err := h.st.Accounts.SetMember(p.ID, u.ID, role); err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.json(w, r, http.StatusOK, accounts.Member{User: u, Role: role})
}

func (h *Hub) removeMember(w http.ResponseWriter, r *http.Request, p accounts.Project, rawEmail string) {
	email, err := url.PathUnescape(rawEmail)
	if err != nil {
		h.notFound(w, r)
		return
	}
	u, err := h.st.Accounts.User(email)
	if err == nil {
		err = h.st.Accounts.RemoveMember(p.ID, u.ID)
	}
	if errors.Is(err, accounts.ErrNotFound) {
		h.fail(w, r, http.StatusNotFound, "not a member")
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.json(w, r, http.StatusOK, map[string]bool{"ok": true})
}

// TokenResult is a token made for a project: shown once.
type TokenResult struct {
	project.Token
	Value string `json:"token"`
	URL   string `json:"url,omitempty"` // a share link, or the project's URL for an agent
}

// MakeToken stores a new token for a project's store and returns it with its link (made from base).
func MakeToken(st *project.Store, base, name string, role team.Role, label string, ttl time.Duration) (TokenResult, error) {
	tok, hash, err := team.NewToken()
	if err != nil {
		return TokenResult{}, err
	}
	row, err := st.CreateToken(project.NewToken{Hash: hash, Role: string(role), Label: label, TTL: ttl})
	if err != nil {
		return TokenResult{}, err
	}
	res := TokenResult{Token: row, Value: tok, URL: ShareURL(base, name, tok)}
	if role == team.Agent {
		res.URL = strings.TrimSuffix(base, "/") + "/p/" + name
	}
	return res, nil
}

func (h *Hub) createToken(w http.ResponseWriter, r *http.Request, ri reqInfo, p *proj) {
	var req struct {
		Role    string `json:"role"`
		Label   string `json:"label"`
		Expires string `json:"expires"` // a Go duration or Nd; "" = 30 days for agents, 24 h for links; "never"
	}
	if err := readJSON(r, &req); err != nil {
		h.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	role, err := team.ParseRole(req.Role)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	ttl, err := TokenTTL(req.Expires, role)
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, err.Error())
		return
	}
	res, err := MakeToken(p.store, h.baseURL(ri), p.row.Name, role, strings.TrimSpace(req.Label), ttl)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	h.json(w, r, http.StatusCreated, res)
}

// TokenTTL parses a token's lifetime: a Go duration (90m, 24h), days (30d), or 0/never. "" is the default: 30 days
// for an agent, 24 hours for a share link.
func TokenTTL(s string, role team.Role) (time.Duration, error) {
	s = strings.TrimSpace(s)
	switch s {
	case "":
		if role == team.Agent {
			return 30 * 24 * time.Hour, nil
		}
		return 24 * time.Hour, nil
	case "0", "never":
		return 0, nil
	}
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, errors.New("expires: give a duration like 90m, 24h or 30d, or never")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil || dur < 0 {
		return 0, errors.New("expires: give a duration like 90m, 24h or 30d, or never")
	}
	return dur, nil
}

func (h *Hub) usersRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, parts []string) {
	switch {
	case len(parts) == 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		us, err := h.st.Accounts.Users()
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		h.json(w, r, http.StatusOK, map[string]any{"users": us})
	case len(parts) == 0 && r.Method == http.MethodPost:
		var req struct {
			Email string `json:"email"`
			Name  string `json:"name"`
			Admin bool   `json:"admin"`
		}
		if err := readJSON(r, &req); err != nil {
			h.fail(w, r, http.StatusBadRequest, "bad json")
			return
		}
		u, err := h.st.Accounts.CreateUser(req.Email, req.Name, req.Admin)
		if errors.Is(err, accounts.ErrExists) {
			h.fail(w, r, http.StatusConflict, "a user with that email exists")
			return
		}
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, err.Error())
			return
		}
		h.userLink(w, r, ri, u, http.StatusCreated)
	case len(parts) == 2 && parts[1] == "link" && r.Method == http.MethodPost:
		email, _ := url.PathUnescape(parts[0])
		u, err := h.st.Accounts.User(email)
		if errors.Is(err, accounts.ErrNotFound) {
			h.fail(w, r, http.StatusNotFound, "no such user")
			return
		}
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		h.userLink(w, r, ri, u, http.StatusOK)
	default:
		h.notFound(w, r)
	}
}

// userLink makes a sign-in link for u and answers it (also mailing it when mail is set up).
func (h *Hub) userLink(w http.ResponseWriter, r *http.Request, ri reqInfo, u accounts.User, code int) {
	link, exp, err := h.st.Accounts.CreateLink(u.ID, 0)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	res := map[string]any{"user": u, "link": LinkURL(h.baseURL(ri), link), "expires": exp.UTC().Format(time.RFC3339)}
	if h.opts.Mailer != nil {
		if err := MailLink(r.Context(), h.opts.Mailer, u, LinkURL(h.baseURL(ri), link), exp); err != nil {
			h.logf("mailing a sign-in link to %s: %v", u.Email, err)
		} else {
			res["mailed"] = true
		}
	}
	h.json(w, r, code, res)
}
