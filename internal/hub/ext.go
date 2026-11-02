package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
)

// Extensions at the home (spec/extensions.md): GET /api/ext, the page's files at /ext/…, and a new video's folder
// (POST /api/ext/<id>/videos), written on disk by retake open and on the editing computer, through the machine
// channel, by the team server.

// isHomeExt reports whether parts is a home-level extension route.
func isHomeExt(parts []string) bool {
	return len(parts) >= 1 && parts[0] == "ext" || len(parts) >= 2 && parts[0] == "api" && parts[1] == "ext"
}

// NewVideo is the body of POST /api/ext/<id>/videos.
type NewVideo struct {
	Project string            `json:"project"`
	Name    string            `json:"name"`
	Files   map[string]string `json:"files"`
}

// check validates a new video against the extension: a tree project, a slug name, and owned UTF-8 files of 1 MiB in
// all.
func (nv NewVideo) check(e *ext.Ext) error {
	total := 0
	switch {
	case !home.ValidPath(nv.Project):
		return errors.New("project is a folder of the tree, like deadlock")
	case nv.Name == "" || home.Slug(nv.Name) != nv.Name:
		return errors.New("name is the new folder's name: lower-case letters, digits and -, at most 63")
	case len(nv.Files) == 0 || len(nv.Files) > 64:
		return errors.New("a new video needs its first files (at most 64)")
	}
	for p, content := range nv.Files {
		if !ext.ValidPath(p) || !e.Owns(p) {
			return fmt.Errorf("%s isn't one of %s's files", p, e.Name)
		}
		if !utf8.ValidString(content) {
			return fmt.Errorf("%s isn't UTF-8 text", p)
		}
		total += len(content)
	}
	if total > ext.MaxCreate {
		return errors.New("a new video's files are 1 MiB at most in all")
	}
	return nil
}

// WriteVideo checks a new video against e and makes <dir>/<project>/<name>/ with its files (retake connect, for the
// machine channel). It fails with os.ErrExist when the folder is there.
func WriteVideo(dir string, e *ext.Ext, nv NewVideo) (string, error) {
	if e == nil || e.Files == nil {
		return "", errors.New("this computer doesn't have that extension turned on")
	}
	nv.Project = strings.Trim(nv.Project, "/")
	if err := nv.check(e); err != nil {
		return "", err
	}
	return writeVideo(dir, e, nv)
}

// writeVideo makes <dir>/<project>/<name>/ with the files; it fails with os.ErrExist when the folder is there.
func writeVideo(dir string, e *ext.Ext, nv NewVideo) (string, error) {
	parent := filepath.Join(dir, filepath.FromSlash(nv.Project))
	if st, err := os.Stat(parent); err != nil || !st.IsDir() {
		return "", fmt.Errorf("no project folder %q", nv.Project)
	}
	root := filepath.Join(parent, nv.Name)
	if err := os.Mkdir(root, 0o755); err != nil { //nolint:gosec // a video's folder others may read
		return "", err
	}
	folder := ext.Folder{Root: root, Owns: e.Files.Owns}
	for _, p := range slices.Sorted(mapsKeys(nv.Files)) {
		if _, err := folder.Write(p, strings.NewReader(nv.Files[p]), "*"); err != nil {
			_ = os.RemoveAll(root)
			return "", err
		}
	}
	return root, nil
}

func mapsKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// readNewVideo reads and checks POST /api/ext/<id>/videos for the extension e.
func readNewVideo(w http.ResponseWriter, r *http.Request, e *ext.Ext) (NewVideo, error) {
	var nv NewVideo
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*ext.MaxCreate)).Decode(&nv); err != nil {
		return nv, errors.New("bad json")
	}
	nv.Project = strings.Trim(nv.Project, "/")
	return nv, nv.check(e)
}

// homeExt is the local home's extension routes.
func (f *Folder) homeExt(w http.ResponseWriter, r *http.Request, role team.Role, parts []string) {
	set := f.opts.Exts
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case len(parts) == 2 && parts[0] == "api" && get:
		writeJSON(w, r, http.StatusOK, set.APIList(func(string) bool { return true }))
	case len(parts) == 2 && parts[0] == "ext" && parts[1] == "styles.css" && get:
		server.ServeStyles(w, r, set)
	case len(parts) >= 3 && parts[0] == "ext" && get:
		set.ServeUI(w, r, parts[1], strings.Join(parts[2:], "/"))
	case len(parts) == 4 && parts[0] == "api" && parts[3] == "videos" && r.Method == http.MethodPost:
		e := set.Get(parts[2])
		if e == nil || e.Files == nil {
			writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no such extension"})
			return
		}
		if !role.Can(createPerm(e)) {
			writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "not allowed"})
			return
		}
		nv, err := readNewVideo(w, r, e)
		if err != nil {
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		root, err := writeVideo(f.opts.Dir, e, nv)
		switch {
		case errors.Is(err, os.ErrExist):
			writeJSON(w, r, http.StatusConflict, map[string]string{"error": "that folder exists"})
			return
		case err != nil:
			writeJSON(w, r, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		for _, fd := range f.rescan(true) {
			if fd.Root == root {
				writeJSON(w, r, http.StatusCreated, map[string]string{"name": fd.Name, "url": "p/" + fd.Name + "/"})
				return
			}
		}
		writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "the new folder wasn't found as a video: is " +
			e.Name + " turned on?"})
	default:
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// createPerm is what making a new video with e's files needs.
func createPerm(e *ext.Ext) team.Perm {
	switch e.Files.Create {
	case "watch":
		return team.PermWatch
	case "record":
		return team.PermRecord
	case "approve":
		return team.PermApprove
	}
	return team.PermManage
}

// --- the team server

// extsFor is the extensions in effect for a person: the allowed ones minus those they turned off.
func (h *Hub) extsFor(u *accounts.User) *ext.Set {
	if u == nil || h.opts.Exts == nil {
		return h.opts.Exts
	}
	off, err := h.extOff(u.ID)
	if err != nil || len(off) == 0 {
		return h.opts.Exts
	}
	return h.opts.Exts.Without(off)
}

func extOffKey(user int64) string { return "ext-off/" + strconv.FormatInt(user, 10) }

// extOff is the extensions a person turned off for themselves.
func (h *Hub) extOff(user int64) (map[string]bool, error) {
	v, err := h.st.Accounts.Setting(extOffKey(user))
	if err != nil || v == "" {
		return map[string]bool{}, nil //nolint:nilerr // no setting: nothing turned off
	}
	off := map[string]bool{}
	if json.Unmarshal([]byte(v), &off) != nil {
		return map[string]bool{}, nil
	}
	return off, nil
}

// meExtensions is GET and PUT /api/me/extensions: {"ignition": false} turns one off for this person.
func (h *Hub) meExtensions(w http.ResponseWriter, r *http.Request, s *session) {
	off, err := h.extOff(s.user.ID)
	if err != nil {
		h.storeFailed(w, r, err)
		return
	}
	if r.Method == http.MethodPut {
		var b map[string]bool
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b) != nil || len(b) > 64 {
			h.fail(w, r, http.StatusBadRequest, `give {"<id>": true|false}`)
			return
		}
		for id, on := range b {
			if h.opts.Exts.Get(id) == nil {
				h.fail(w, r, http.StatusBadRequest, "this server doesn't allow "+id)
				return
			}
			if on {
				delete(off, id)
			} else {
				off[id] = true
			}
		}
		raw, _ := json.Marshal(off)
		if err := h.st.Accounts.SetSetting(extOffKey(s.user.ID), string(raw)); err != nil {
			h.storeFailed(w, r, err)
			return
		}
	}
	h.json(w, r, http.StatusOK, map[string]any{"off": off})
}

// homeExt is the team server's home-level extension routes; s is the signed-in person.
func (h *Hub) homeExt(w http.ResponseWriter, r *http.Request, s *session, parts []string) {
	set := h.extsFor(&s.user)
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case len(parts) == 2 && parts[0] == "api" && get:
		h.json(w, r, http.StatusOK, set.APIList(func(string) bool { return true }))
	case len(parts) == 2 && parts[0] == "ext" && parts[1] == "styles.css" && get:
		server.ServeStyles(w, r, set)
	case len(parts) >= 3 && parts[0] == "ext" && get:
		set.ServeUI(w, r, parts[1], strings.Join(parts[2:], "/"))
	case len(parts) == 4 && parts[0] == "api" && parts[3] == "videos" && r.Method == http.MethodPost:
		e := set.Get(parts[2])
		if e == nil || e.Files == nil {
			h.fail(w, r, http.StatusNotFound, "no such extension")
			return
		}
		if createPerm(e) == team.PermManage && !s.user.Admin {
			h.fail(w, r, http.StatusForbidden, "not allowed")
			return
		}
		nv, err := readNewVideo(w, r, e)
		if err != nil {
			h.fail(w, r, http.StatusBadRequest, err.Error())
			return
		}
		h.createRemote(w, r, e, nv)
	default:
		h.notFound(w, r)
	}
}

// createRemote makes a new video's folder on the editing computer that holds its project, and waits (a little) for
// its worker to report it, so the page it opens next shows it.
func (h *Hub) createRemote(w http.ResponseWriter, r *http.Request, e *ext.Ext, nv NewVideo) {
	reply, err := h.machines.ask(r.Context(), machineRequest{Op: "create", Ext: e.ID, Project: nv.Project,
		Name: nv.Name, Files: nv.Files})
	if errors.Is(err, errNoMachine) {
		h.fail(w, r, http.StatusServiceUnavailable, "editing machine is offline")
		return
	}
	if err != nil {
		h.fail(w, r, http.StatusServiceUnavailable, "the editing computer didn't answer: "+err.Error())
		return
	}
	if reply.Status != http.StatusCreated {
		msg := reply.Error
		if msg == "" {
			msg = "the editing computer couldn't make it"
		}
		h.fail(w, r, max(reply.Status, 400), msg)
		return
	}
	wait := h.createWait
	if wait == 0 {
		wait = 15 * time.Second
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) && r.Context().Err() == nil {
		if p, err := h.project(reply.Name); err == nil {
			if srv := p.server(); srv != nil && len(srv.Family()) > 0 {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	h.json(w, r, http.StatusCreated, map[string]string{"name": reply.Name, "url": "p/" + reply.Name + "/"})
}

// --- the machine channel: requests for the editing computer itself (retake connect), not one project's worker

var errNoMachine = errors.New("no editing computer holds that project")

// machineRequest is one request for a connected computer.
type machineRequest struct {
	ID      string            `json:"id"`
	Op      string            `json:"op"`
	Ext     string            `json:"ext,omitempty"`
	Project string            `json:"project,omitempty"`
	Name    string            `json:"name,omitempty"`
	Files   map[string]string `json:"files,omitempty"`

	machine   int64
	delivered bool
	reply     chan machineReply
}

// machineReply is a computer's answer: POST /api/machine/requests/<id>.
type machineReply struct {
	Status int    `json:"status"`
	Name   string `json:"name,omitempty"`
	Error  string `json:"error,omitempty"`
}

// machineChannel matches requests with the computers' long polls.
type machineChannel struct {
	mu      sync.Mutex
	pending map[string]*machineRequest
	bell    chan struct{}
	seen    map[int64]time.Time       // when each machine token last polled
	places  map[int64]map[string]bool // the projects (tree paths) each machine registered videos in
	now     func() time.Time
}

func (c *machineChannel) init() {
	if c.pending == nil {
		c.pending, c.bell = map[string]*machineRequest{}, make(chan struct{})
		c.seen, c.places = map[int64]time.Time{}, map[int64]map[string]bool{}
	}
	if c.now == nil {
		c.now = time.Now
	}
}

// placed records that machine registered a video at path in project.
func (c *machineChannel) placed(machine int64, path, proj string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if c.places[machine] == nil {
		c.places[machine] = map[string]bool{}
	}
	c.places[machine][proj] = true
	c.places[machine][path] = true
}

// pick is the online computer for a project: one that registered videos in it, else the only one online.
func (c *machineChannel) pick(proj string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	var online []int64
	for m, t := range c.seen {
		if c.now().Sub(t) < 40*time.Second {
			online = append(online, m)
		}
	}
	slices.Sort(online)
	for _, m := range online {
		if c.places[m][proj] {
			return m, true
		}
	}
	if len(online) == 1 {
		return online[0], true
	}
	return 0, false
}

// ask sends a request to the computer that holds its project and waits for the answer.
func (c *machineChannel) ask(ctx context.Context, req machineRequest) (machineReply, error) {
	m, ok := c.pick(req.Project)
	if !ok {
		return machineReply{}, errNoMachine
	}
	req.ID, req.machine, req.reply = project.NewWorkID(), m, make(chan machineReply, 1)
	c.mu.Lock()
	c.pending[req.ID] = &req
	close(c.bell)
	c.bell = make(chan struct{})
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case rep := <-req.reply:
		return rep, nil
	case <-ctx.Done():
		return machineReply{}, ctx.Err()
	}
}

// next waits (up to 25 s) for a request for machine; ok is false for a keep-alive.
func (c *machineChannel) next(ctx context.Context, machine int64) (machineRequest, bool) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		c.mu.Lock()
		c.init()
		c.seen[machine] = c.now()
		for _, p := range c.pending {
			if p.machine == machine && !p.delivered {
				p.delivered = true
				out := *p
				c.mu.Unlock()
				return out, true
			}
		}
		bell := c.bell
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.seen[machine] = c.now()
			c.mu.Unlock()
			return machineRequest{}, false
		case <-bell:
		}
	}
}

// answer hands a computer's reply to the request waiting for it.
func (c *machineChannel) answer(machine int64, id string, rep machineReply) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	p := c.pending[id]
	if p == nil || p.machine != machine || !p.delivered {
		return false
	}
	select {
	case p.reply <- rep:
		return true
	default:
		return false
	}
}

// machineRequests is GET /api/machine/requests and POST /api/machine/requests/<id>.
func (h *Hub) machineRequests(w http.ResponseWriter, r *http.Request, m accounts.MachineToken, rest []string) {
	switch {
	case len(rest) == 1 && r.Method == http.MethodGet:
		req, ok := h.machines.next(r.Context(), m.ID)
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.json(w, r, http.StatusOK, req)
	case len(rest) == 2 && r.Method == http.MethodPost:
		var rep machineReply
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&rep) != nil {
			h.fail(w, r, http.StatusBadRequest, "bad json")
			return
		}
		if !h.machines.answer(m.ID, rest[1], rep) {
			h.fail(w, r, http.StatusGone, "request expired")
			return
		}
		h.json(w, r, http.StatusOK, map[string]bool{"ok": true})
	default:
		h.notFound(w, r)
	}
}
