package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/tus"
)

// proj is an open project: its row, its workspace and store, the Server mounted at /p/<name>/ once it has a
// version, and its publisher with object storage.
type proj struct {
	h     *Hub
	row   accounts.Project
	base  string // /p/<name>
	root  string // the workspace
	store *project.Store
	pub   *publisher // nil with local media

	mu  sync.Mutex // guards srv; serializes pushes
	srv *server.Server
}

// project returns an open project, opening it on first use (and downloading the files its workspace lacks, with
// object storage). An unknown name gives accounts.ErrNotFound.
func (h *Hub) project(name string) (*proj, error) {
	if !accounts.ValidName(name) {
		return nil, fmt.Errorf("project %s: %w", name, accounts.ErrNotFound)
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, errors.New("the server is stopping")
	}
	if s, ok := h.projects[name]; ok {
		h.mu.Unlock()
		<-s.ready // another request is opening it
		return s.p, s.err
	}
	// Open it outside the lock: downloading a project's files must not hold up requests to the others.
	s := &slot{ready: make(chan struct{})}
	h.projects[name] = s
	h.mu.Unlock()
	s.p, s.err = h.open(name)
	if s.err != nil {
		h.mu.Lock()
		delete(h.projects, name) // try again next time: the project may be added, or the store come back
		h.mu.Unlock()
	}
	close(s.ready)
	return s.p, s.err
}

// slot is a project being opened, or open.
type slot struct {
	ready chan struct{} // closed once p or err is set
	p     *proj
	err   error
}

func (h *Hub) open(name string) (*proj, error) {
	row, err := h.st.Accounts.Project(name)
	if err != nil {
		return nil, err
	}
	st, err := h.st.ProjectStore(h.ctx, row)
	if err != nil {
		return nil, err
	}
	if h.opts.EventPoll > 0 {
		st.Poll = h.opts.EventPoll
	}
	p := &proj{h: h, row: row, base: "/p/" + name, root: h.st.Workspace(name), store: st}
	if h.st.Blobs != nil {
		p.pub = newPublisher(h.st.Blobs, name, p.root, h.opts.Log)
		if err := p.pub.hydrate(h.ctx); err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("downloading the project's files: %w", err)
		}
		h.wg.Go(func() { p.pub.run(h.ctx, st, h.opts.SyncEvery) })
	}
	return p, nil
}

// SyncProject uploads what changed in a project's workspace to object storage now (it happens on its own after
// writes and every SyncEvery). Without object storage it does nothing.
func (h *Hub) SyncProject(ctx context.Context, name string) error {
	p, err := h.project(name)
	if err != nil || p.pub == nil {
		return err
	}
	return p.pub.sync(ctx)
}

// server returns the project's mounted Server, making it once the project has a version. nil: no version yet.
func (p *proj) server() *server.Server {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.serverLocked()
}

func (p *proj) serverLocked() *server.Server {
	return p.makeServerLocked(false)
}

// An outbound worker can provide the first version without uploading any media to the host.
func (p *proj) makeServerLocked(allowEmpty bool) *server.Server {
	if p.srv != nil {
		return p.srv
	}
	anchor := Anchor(p.root)
	if anchor == "" && !allowEmpty {
		a, err := p.store.Automation()
		if err != nil || len(a.Renders) == 0 && len(a.Files) == 0 { // a connected computer's v0 counts
			return nil
		}
	}
	o := p.h.opts
	so := server.Options{Video: anchor, Root: p.root, Store: p.store, External: true, BasePath: p.base,
		FollowNewest: true, UI: o.UI, Prober: o.Prober, Jobs: o.Jobs, Version: o.Version, Log: o.Log,
		Verbose: o.Verbose, EventPoll: o.EventPoll, MCPFiles: p.uploadFile, MediaCache: p.h.cache, CacheScope: p.row.Name,
		Exts: o.Exts, RemoteFiles: true, WhoDownloads: o.Downloads}
	if p.pub != nil {
		so.Media = p.pub.serve
	}
	p.srv = server.New(so)
	return p.srv
}

// close stops the project's Server (its jobs are queued again), uploads its files one last time, and closes its
// store.
func (p *proj) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	if p.srv != nil {
		errs = append(errs, p.srv.Close())
		p.srv = nil
	}
	if p.pub != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		errs = append(errs, p.pub.sync(ctx))
		cancel()
	}
	errs = append(errs, p.store.Close())
	return errors.Join(errs...)
}

// projectRoute is everything under /p/<name>/. rest is the path inside the project ("" for /p/<name>).
func (h *Hub) projectRoute(w http.ResponseWriter, r *http.Request, ri reqInfo, name, rest string) {
	p, err := h.project(name)
	if errors.Is(err, accounts.ErrNotFound) {
		h.fail(w, r, http.StatusNotFound, "no such project")
		return
	}
	if err != nil {
		h.logf("project %s: %v", name, err)
		h.fail(w, r, http.StatusInternalServerError, "project unavailable")
		return
	}
	if rest == "" {
		target := p.base + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect) //nolint:gosec // our own path: /p/<valid name>/
		return
	}
	parts := splitPath(rest)
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	page := get && (len(parts) == 0 || len(parts) == 1 && parts[0] == "index.html")
	switch {
	case len(parts) == 2 && parts[0] == "s" && get:
		h.shareLink(w, r, ri, p, parts[1])
		return
	case len(parts) >= 2 && parts[0] == "api" && parts[1] == "uploads" && len(parts) <= 3:
		id := ""
		if len(parts) == 3 {
			id = parts[2]
		}
		if r.Method != http.MethodOptions { // OPTIONS is discovery; it needs no token
			if who, err := h.identify(w, r, ri, p, false, true); err != nil || !h.allowed(w, r, who, team.PermAgent,
				false) {
				return
			}
		}
		h.uploads.Serve(w, r, p.base+"/api/uploads", p.row.Name, id)
		return
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "push":
		who, err := h.identify(w, r, ri, p, false, true)
		if err != nil || !h.allowed(w, r, who, team.PermAgent, false) {
			return
		}
		h.push(w, r, p, who)
		return
	}
	isMCP := len(parts) == 1 && parts[0] == "mcp"
	isWorker := len(parts) >= 2 && parts[0] == "api" && parts[1] == "worker"
	who, err := h.identify(w, r, ri, p, page, isMCP || isWorker)
	if err != nil || !h.allowed(w, r, who, server.PermFor(r.Method, rest), page) {
		return
	}
	srv := p.server()
	if get && len(parts) == 2 && parts[0] == "api" && parts[1] == "poster" && p.hasPoster() {
		// The poster a connected machine sent stands in until a version has a poster of its own.
		if srv == nil || len(srv.Family()) == 0 || !serveIfFound(w, server.Grant(r, who.role, who.author), srv, rest) {
			p.servePoster(w, r)
		}
		return
	}
	if srv == nil && isWorker {
		p.mu.Lock()
		srv = p.makeServerLocked(true)
		p.mu.Unlock()
	}
	if srv == nil {
		h.noVersions(w, r, page)
		return
	}
	if !isWorker && len(srv.Family()) == 0 {
		h.noVersions(w, r, page)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	r2 = server.GrantExts(server.Grant(r2, who.role, who.author), h.extsFor(who.user))
	sw := &statusWriter{ResponseWriter: w}
	srv.ServeHTTP(sw, r2)
	if p.pub != nil && r.Method == http.MethodPost && sw.status() < 300 && len(parts) == 4 && parts[1] == "session" {
		p.pub.kick() // a take was written: upload it soon
	}
}

// allowed checks a role against what a route needs, and answers 403 when it can't.
func (h *Hub) allowed(w http.ResponseWriter, r *http.Request, who who, perm team.Perm, page bool) bool {
	if who.role.Can(perm) {
		return true
	}
	if page {
		h.page(w, r, http.StatusForbidden, "Retake", "<p>This link can't open the review page.</p>")
	} else {
		h.fail(w, r, http.StatusForbidden, "not allowed")
	}
	return false
}

// noVersions answers a project that has no version yet.
func (h *Hub) noVersions(w http.ResponseWriter, r *http.Request, page bool) {
	if page {
		h.page(w, r, http.StatusOK, "Retake", "<h1>No versions yet</h1><p class=\"muted\">The video plays here once the "+
			"computer that edits it is connected (<code>retake connect</code>) and has rendered it, or after a "+
			"<code>retake push --remote</code>. This page looks again every 10 seconds.</p>"+
			`<meta http-equiv="refresh" content="10">`+backHome)
		return
	}
	h.fail(w, r, http.StatusNotFound, "no versions yet")
}

// uploadRef is the prefix of an upload's reference in version_push's file argument.
const uploadRef = "upload:"

// uploadFile resolves version_push's file on the project's /mcp: an upload of this project, which the push moves
// into the family (spec/server.md, "Remote MCP").
func (p *proj) uploadFile(file string) (string, bool, error) {
	id, ok := strings.CutPrefix(strings.TrimSpace(file), uploadRef)
	if !ok {
		return "", false, &agent.Error{Code: agent.CodeBadRequest, Message: "on the team server, version_push's file " +
			"is upload:<id>, an upload of this project (retake push --remote makes one, over tus at " + p.base +
			"/api/uploads)"}
	}
	u, err := p.h.uploads.Lookup(p.row.Name, id)
	if err != nil {
		return "", false, &agent.Error{Code: agent.CodeNotFound, Message: "no upload " + id + " in this project"}
	}
	if !u.Complete {
		return "", false, &agent.Error{Code: agent.CodeBadRequest, Message: fmt.Sprintf("upload %s isn't complete "+
			"(%d of %d bytes)", id, u.Offset, u.Size)}
	}
	return p.h.uploads.DataPath(u), true, nil
}

// pushRequest is the body of POST /p/<name>/api/push.
type pushRequest struct {
	Upload  string `json:"upload"`
	Parent  string `json:"parent"`
	Label   string `json:"label"`
	Changes []struct {
		Note    int64  `json:"note"`
		Summary string `json:"summary"`
	} `json:"changes"`
	Asks []struct {
		Note     int64  `json:"note"`
		Question string `json:"question"`
	} `json:"asks"`
}

// push is POST /p/<name>/api/push: version_push with an uploaded file (spec/server.md, "Pushing an upload").
func (h *Hub) push(w http.ResponseWriter, r *http.Request, p *proj, who who) {
	if r.Method != http.MethodPost {
		h.notFound(w, r)
		return
	}
	var req pushRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		h.agentFail(w, r, &agent.Error{Code: agent.CodeBadRequest, Message: "the body isn't the push's JSON"})
		return
	}
	file, _, err := p.uploadFile(uploadRef + req.Upload)
	if err != nil {
		h.agentFail(w, r, err)
		return
	}
	by := who.label
	if by == "" {
		by = "agent"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var res agent.PushResult
	if anchor := Anchor(p.root); anchor == "" {
		if len(req.Changes)+len(req.Asks) > 0 {
			h.agentFail(w, r, &agent.Error{Code: agent.CodeBadRequest, Message: "this is the project's first version: " +
				"there are no notes to change or ask about yet"})
			return
		}
		u, _ := h.uploads.Lookup(p.row.Name, req.Upload)
		row, err := FirstVersion(p.store, p.root, p.row.Name, file, req.Label, by, u.SHA256, true)
		if err != nil {
			h.agentFail(w, r, err)
			return
		}
		res = agent.PushResult{Version: row, Answered: []int64{}, Questions: []int64{}}
	} else {
		a := agent.PushArgs{File: file, Parent: req.Parent, Label: req.Label, Move: true, By: by}
		for _, c := range req.Changes {
			a.Changes = append(a.Changes, project.ChangeEntry{Note: c.Note, Summary: c.Summary})
		}
		for _, q := range req.Asks {
			a.Asks = append(a.Asks, project.PushAsk{Note: q.Note, Text: q.Question})
		}
		ap := &agent.Project{Video: anchor, Root: p.root, Store: p.store, Prober: h.opts.Prober}
		if res, err = ap.VersionPush(r.Context(), a); err != nil {
			h.agentFail(w, r, err)
			return
		}
	}
	if err := h.uploads.Remove(p.row.Name, req.Upload); err != nil {
		h.logf("removing upload %s: %v", req.Upload, err)
	}
	if srv := p.serverLocked(); srv != nil {
		srv.Rescan()
	}
	if p.pub != nil {
		p.pub.kick()
	}
	h.json(w, r, http.StatusOK, res)
}

// agentFail answers an agent operation's error with internal/agent's JSON and a fitting status.
func (h *Hub) agentFail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *agent.Error
	if !errors.As(err, &ae) {
		var pr *project.PushRuleError
		var te *project.TransitionError
		switch {
		case errors.As(err, &pr):
			ae = &agent.Error{Code: agent.CodePushRule, Message: err.Error(), Notes: pr.Notes}
		case errors.As(err, &te):
			ae = &agent.Error{Code: agent.CodeIllegal, Message: err.Error()}
		case errors.Is(err, project.ErrNotFound), errors.Is(err, tus.ErrNotFound):
			ae = &agent.Error{Code: agent.CodeNotFound, Message: err.Error()}
		default:
			ae = &agent.Error{Code: agent.CodeBadRequest, Message: err.Error()}
		}
	}
	code := http.StatusInternalServerError
	switch ae.Code {
	case agent.CodeBadRequest:
		code = http.StatusBadRequest
		if strings.Contains(ae.Message, "isn't complete") {
			code = http.StatusConflict
		}
	case agent.CodeNotFound:
		code = http.StatusNotFound
	case agent.CodePushRule, agent.CodeIllegal:
		code = http.StatusConflict
	}
	h.json(w, r, code, ae)
}
