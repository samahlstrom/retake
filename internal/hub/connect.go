package hub

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/home"
)

// Machines: "Connect your computer" (spec/server.md, "Machines"). An admin makes a machine token; `retake connect`
// uses it to put the computer's projects on the server and to run their workers as each project's agent.

// ConnectCommand is the line the owner runs on their computer.
func ConnectCommand(base, token string) string {
	return fmt.Sprintf("retake connect %s --token %s", strings.TrimSuffix(base, "/"), token)
}

// connectCommand makes a machine token for an admin and returns its command.
func (h *Hub) connectCommand(u accounts.User, base string) (string, error) {
	tok, _, err := h.st.Accounts.CreateMachineToken(u.ID)
	if err != nil {
		return "", err
	}
	return ConnectCommand(base, tok), nil
}

// connectRoute is /api/connect[/<id>], for admins.
func (h *Hub) connectRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, s *session, rest []string) {
	if !s.user.Admin {
		h.fail(w, r, http.StatusForbidden, "admins only")
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		tok, row, err := h.st.Accounts.CreateMachineToken(s.user.ID)
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		base := strings.TrimSuffix(h.baseURL(ri), "/")
		h.json(w, r, http.StatusCreated, map[string]any{"command": ConnectCommand(base, tok), "url": base,
			"token": tok, "id": row.ID})
	case len(rest) == 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		ts, err := h.st.Accounts.MachineTokens()
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		h.json(w, r, http.StatusOK, map[string]any{"tokens": ts})
	case len(rest) == 1 && r.Method == http.MethodDelete:
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			h.notFound(w, r)
			return
		}
		m, err := h.st.Accounts.RevokeMachineToken(id)
		if errors.Is(err, accounts.ErrNotFound) {
			h.fail(w, r, http.StatusNotFound, "no such token")
			return
		}
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		h.json(w, r, http.StatusOK, m)
	default:
		h.notFound(w, r)
	}
}

// machine checks a request's machine token under the rate limit, answering a refused one itself.
func (h *Hub) machine(w http.ResponseWriter, r *http.Request, ri reqInfo, tok string, page bool) bool {
	_, ok := h.machineToken(w, r, ri, tok, page)
	return ok
}

// machineToken is machine with the token's row.
func (h *Hub) machineToken(w http.ResponseWriter, r *http.Request, ri reqInfo, tok string, page bool) (accounts.MachineToken, bool) {
	if h.limited(w, r, ri, h.tokenFails, page) {
		return accounts.MachineToken{}, false
	}
	m, err := h.st.Accounts.CheckMachineToken(tok)
	if errors.Is(err, accounts.ErrBadMachine) {
		h.tokenFails.Fail(ri.client, h.opts.Now())
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		if page {
			h.page(w, r, http.StatusUnauthorized, "Retake", "<p>This is a machine's token, not a link to a review.</p>")
		} else {
			h.fail(w, r, http.StatusUnauthorized, "bad token")
		}
		return accounts.MachineToken{}, false
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return accounts.MachineToken{}, false
	}
	return m, true
}

// machineRoute is /api/machine/...: the routes retake connect uses, with a machine token.
func (h *Hub) machineRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, rest []string) {
	tok := bearer(r)
	if tok == "" {
		w.Header().Set("WWW-Authenticate", "Bearer")
		h.fail(w, r, http.StatusUnauthorized, "machine token required")
		return
	}
	m, ok := h.machineToken(w, r, ri, tok, false)
	if !ok {
		return
	}
	switch {
	case len(rest) == 0 && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		h.json(w, r, http.StatusOK, map[string]any{"ok": true, "url": strings.TrimSuffix(h.baseURL(ri), "/")})
	case len(rest) >= 1 && rest[0] == "requests":
		h.machineRequests(w, r, m, rest)
	case len(rest) == 1 && rest[0] == "projects" && r.Method == http.MethodPost:
		h.registerProject(w, r, ri, m)
	case len(rest) == 3 && rest[0] == "projects" && rest[2] == "poster" && r.Method == http.MethodPut:
		h.putPoster(w, r, rest[1])
	default:
		h.notFound(w, r)
	}
}

// maxTitle is the longest project title a machine may send.
const maxTitle = 200

// registerProject is POST /api/machine/projects: it makes the project when it doesn't exist.
func (h *Hub) registerProject(w http.ResponseWriter, r *http.Request, ri reqInfo, m accounts.MachineToken) {
	var req struct {
		Name  string `json:"name"`
		Title string `json:"title"`
		// Where it is in the computer's folder (spec/home.md, the tree); absent from an older retake connect.
		Path    *string `json:"path"`
		Project *string `json:"project"`
	}
	if err := readJSON(r, &req); err != nil {
		h.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	name, title := strings.TrimSpace(req.Name), strings.TrimSpace(req.Title)
	if !accounts.ValidName(name) {
		h.fail(w, r, http.StatusBadRequest, "a project's name is lower-case letters, digits and -, at most 63")
		return
	}
	if len(title) > maxTitle {
		h.fail(w, r, http.StatusBadRequest, fmt.Sprintf("a title is at most %d characters", maxTitle))
		return
	}
	place := req.Path != nil || req.Project != nil
	var path, proj string
	if place {
		if req.Path != nil {
			path = *req.Path
		}
		proj = path
		if req.Project != nil {
			proj = *req.Project
		}
		if !home.ValidPath(path) || !home.ValidPath(proj) || !home.Within(proj, path) {
			h.fail(w, r, http.StatusBadRequest, "path and project are folders relative to the connected folder, "+
				"with / between names, and project is path or a folder above it")
			return
		}
	}
	p, err := h.st.Accounts.Project(name)
	created := false
	if errors.Is(err, accounts.ErrNotFound) {
		p, err = h.st.CreateProject(r.Context(), name, title)
		created = err == nil
		if errors.Is(err, accounts.ErrExists) { // another machine made it a moment ago
			p, err = h.st.Accounts.Project(name)
		}
		if created {
			h.logf("project %s added by a connected machine", name)
		}
	}
	if err == nil && place && (p.Path != path || p.ProjectPath != proj) {
		if err = h.st.Accounts.SetProjectPlace(p.Name, path, proj); err == nil {
			p.Path, p.ProjectPath = path, proj
		}
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if place {
		h.machines.placed(m.ID, path, proj) // where a new video of this project is made (the machine channel)
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	h.json(w, r, code, map[string]any{"name": p.Name, "title": p.Title, "path": p.Path, "project": p.ProjectPath,
		"created": created, "url": strings.TrimSuffix(h.baseURL(ri), "/") + "/p/" + p.Name})
}

// MaxPoster is the largest poster a machine may send.
const MaxPoster = 2 << 20

// posterFile is where a project's poster from a machine is kept, in its workspace.
func posterFile(root string) string { return filepath.Join(root, ".retake", "poster.jpg") }

// putPoster is PUT /api/machine/projects/<name>/poster: a JPEG of the project's first frame.
func (h *Hub) putPoster(w http.ResponseWriter, r *http.Request, name string) {
	p, err := h.project(name)
	if errors.Is(err, accounts.ErrNotFound) {
		h.fail(w, r, http.StatusNotFound, "no such project")
		return
	}
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "image/jpeg" {
		h.fail(w, r, http.StatusUnsupportedMediaType, "a poster is a JPEG (Content-Type: image/jpeg)")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPoster+1))
	if err != nil {
		h.fail(w, r, http.StatusBadRequest, "reading the poster failed")
		return
	}
	if len(body) > MaxPoster {
		h.fail(w, r, http.StatusRequestEntityTooLarge, "a poster is at most 2 MiB")
		return
	}
	if !bytes.HasPrefix(body, []byte{0xff, 0xd8, 0xff}) {
		h.fail(w, r, http.StatusBadRequest, "that isn't a JPEG")
		return
	}
	dst := posterFile(p.root)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		h.storeFailed(w, r, err)
		return
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		h.storeFailed(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// hasPoster reports whether a machine sent the project a poster.
func (p *proj) hasPoster() bool {
	st, err := os.Stat(posterFile(p.root))
	return err == nil && st.Mode().IsRegular()
}

// servePoster answers GET /p/<name>/api/poster with the machine's poster.
func (p *proj) servePoster(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(posterFile(p.root))
	if err != nil {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no poster"})
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no poster"})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", st.ModTime().Truncate(time.Second), f)
}
