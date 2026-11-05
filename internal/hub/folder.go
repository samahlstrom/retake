package hub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/feedback"
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/theme"
	"github.com/samahlstrom/retake/internal/thumb"
	"github.com/samahlstrom/retake/internal/transcribe"
	"github.com/samahlstrom/retake/internal/trash"
	"github.com/samahlstrom/retake/internal/ui"
)

// retake open <folder> (spec/home.md): every project in a folder, each the review page of retake open <video> mounted
// at /p/<name>/ as the team server mounts its projects, and the home screen at /. There are no accounts: local
// requests are the owner, and requests through a share's host carry a token of the folder's home store.

// FolderOptions configure a Folder.
type FolderOptions struct {
	Dir  string // the folder: absolute, symlinks resolved
	Port int    // the port it's served on: the local Host headers and origins
	UI   fs.FS  // the page (index.html, home.html, assets); nil serves the "page not built" stub
	// AllowHosts are extra hostnames reached over HTTPS (a tunnel's or a reverse proxy's). Requests through them need
	// a token, as do those through the hosts of running shares (the home store's hosts table).
	AllowHosts []string
	Prober     family.Prober
	Jobs       *jobs.Engines
	Author     string // session.json's author.name for the owner's takes
	Version    string

	IdleExit   time.Duration // exit this long after the last request, once there was one; 0 = never
	CheckEvery time.Duration // 0 = server.DefaultCheckEvery
	Now        func() time.Time

	Log     io.Writer // nil = discard
	Verbose bool

	Thumbs    *thumb.Maker  // nil = thumb.Default
	EventPoll time.Duration // the projects' event streams' poll; 0 = their default
	// RescanEvery is the least time between two scans of the folder for /api/home; 0 = 2 s. After a scan that took
	// over 200 ms, the next waits at least ten times as long as it took.
	RescanEvery time.Duration
	// Project adjusts a project's server options before its server is made (the streaming side's hooks, tests).
	Project func(name string, o *server.Options)
	// LocalWorker is each project's server.Options.LocalWorker: its editing worker, in this process. Working says
	// whether a project has agent work in flight, whose worker Serve starts at once to take it back
	// (spec/automation.md, "Agents outlive Retake").
	LocalWorker func(ctx context.Context, root string, h http.Handler, blocked func(string))
	Working     func(root string) bool
	// Exts are the extensions in effect (spec/extensions.md): their kinds are found by the scan, and their files,
	// list and routes are served at the home and under every video. FileTranscriber transcribes owned audio files.
	Exts            *ext.Set
	FileTranscriber func() (transcribe.Transcriber, error)
	// Settings is what the settings page reads and writes (settings.go); nil: it has none.
	Settings *LocalSettings
	// A video's ⋯ (videomenu.go): how share links made in the page reach people, where Move to Trash moves a video
	// (the zero value: this computer's trash), and Show in folder (nil: RevealDefault).
	Share  FolderShare
	Trash  trash.Options
	Reveal func(dir string) error
	// NativePicker opens the local desktop's folder chooser. It is set only by
	// retake start --app on macOS, never by a shared request.
	NativePicker func(context.Context) ([]string, error)
}

// Folder serves a folder of projects.
type Folder struct {
	opts     FolderOptions
	local    map[string]bool // 127.0.0.1:<port>, localhost:<port>
	origins  map[string]bool // http://127.0.0.1:<port>, http://localhost:<port>
	allow    map[string]bool
	limiter  *team.Limiter
	feedback http.Handler

	mu       sync.Mutex
	found    []home.Found // the videos
	scanned  time.Time
	took     time.Duration             // how long the last scan took
	open     map[string]*folderProject // by root
	closed   bool
	close    sync.Once
	closeErr error

	storeMu sync.Mutex
	store   *project.Store // the home store; nil until it exists

	contactMu sync.Mutex
	contact   time.Time

	tryMu  sync.Mutex      // the agent tests running (Settings' Test)
	trying map[string]bool // by agent id

	menus menuState // a video's ⋯: share links' address, Move to Trash (videomenu.go)

	libraryMu sync.Mutex
	// libraryChangeMu serializes validation, durable replacement and name writes.
	libraryChangeMu sync.Mutex
	library         libraryState
	removedRoots    map[string]bool // ids removed this run; stale create forms get conflict, never a path fallback
	pickerMu        sync.Mutex
	picking         bool
}

// folderProject is one project's server, made on first use, and a handle on its store for the cards.
type folderProject struct {
	found home.Found
	srv   *server.Server

	stMu sync.Mutex
	st   *project.Store // nil until the project has a store
}

// NewFolder makes a Folder and scans the folder once.
func NewFolder(opts FolderOptions) *Folder {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CheckEvery <= 0 {
		opts.CheckEvery = server.DefaultCheckEvery
	}
	if opts.RescanEvery <= 0 {
		opts.RescanEvery = 2 * time.Second
	}
	if opts.Thumbs == nil {
		opts.Thumbs = thumb.Default
	}
	f := &Folder{opts: opts, local: map[string]bool{}, origins: map[string]bool{}, allow: map[string]bool{},
		feedback: feedback.New(opts.Version), removedRoots: map[string]bool{},
		trying: map[string]bool{}, limiter: team.NewLimiter(), open: map[string]*folderProject{}}
	f.library = f.loadLibrary()
	for _, h := range []string{"127.0.0.1", "localhost"} {
		hp := net.JoinHostPort(h, strconv.Itoa(opts.Port))
		f.local[hp] = true
		f.origins["http://"+hp] = true
	}
	for _, h := range opts.AllowHosts {
		f.allow[strings.ToLower(h)] = true
	}
	f.initMenus()
	f.rescan(true)
	return f
}

// Dir returns the folder.
func (f *Folder) Dir() string { return f.opts.Dir }

// rescan scans the folder's tree again, unless the last scan is younger than RescanEvery, or than ten times as long
// as it took (and force isn't set). It returns the videos.
func (f *Folder) rescan(force bool) []home.Found {
	f.mu.Lock()
	wait := f.opts.RescanEvery
	if f.took > 200*time.Millisecond { // a big folder: at most a tenth of the time goes to scanning it
		wait = max(wait, 10*f.took)
	}
	if !force && !f.scanned.IsZero() && f.opts.Now().Sub(f.scanned) < wait {
		found := f.found
		f.mu.Unlock()
		return found
	}
	first := f.scanned.IsZero()
	f.mu.Unlock()
	f.libraryChangeMu.Lock()
	defer f.libraryChangeMu.Unlock()
	scan := f.scanLibraryLocked() // serialized with replacement through publication
	if f.opts.Verbose || scan.Capped && first {
		capped := ""
		if scan.Capped {
			capped = fmt.Sprintf(" (stopped at %d folders)", home.MaxDirs)
		}
		f.logf("scanned %s: %d videos, %d folders in %s%s", f.opts.Dir, len(scan.Videos), scan.Dirs,
			scan.Took.Round(time.Millisecond), capped)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.found, f.scanned, f.took = append(scan.Videos, scan.Empty...), f.opts.Now(), scan.Took
	return f.found
}

// projectNamed returns a project by name, scanning the folder again when it doesn't know the name.
func (f *Folder) projectNamed(name string) *folderProject {
	for _, force := range []bool{false, true} {
		for _, fd := range f.rescan(force) {
			if fd.Name == name {
				return f.projectOf(fd)
			}
		}
	}
	return nil
}

// projectOf returns the open project of a found folder, making its server on first use.
func (f *Folder) projectOf(fd home.Found) *folderProject {
	f.mu.Lock()
	defer f.mu.Unlock()
	if fp, ok := f.open[fd.Root]; ok && fp.found.Name == fd.Name {
		return fp
	}
	if f.closed || f.leavingRoot(fd) {
		return nil
	}
	o := server.Options{Video: fd.Anchor, Root: fd.Root, Port: f.opts.Port, UI: f.opts.UI, Author: f.opts.Author,
		Prober: f.opts.Prober, Jobs: f.opts.Jobs, Version: f.opts.Version, Log: f.opts.Log, Verbose: f.opts.Verbose,
		EventPoll: f.opts.EventPoll, External: true, BasePath: "/p/" + fd.Name, FollowNewest: true,
		Thumbs: f.opts.Thumbs, LocalWorker: f.opts.LocalWorker, Exts: f.opts.Exts, FindAnchor: home.Anchor,
		FileTranscriber: f.opts.FileTranscriber, Sharing: f.menus.sharing}
	if f.opts.Project != nil {
		f.opts.Project(fd.Name, &o)
	}
	if old, ok := f.open[fd.Root]; ok {
		// The folder was renamed under the same root: its address changed, so its server is made again.
		_ = old.close()
	}
	fp := &folderProject{found: fd, srv: server.New(o)}
	f.open[fd.Root] = fp
	return fp
}

// projectStore returns the project's store for the cards and its own tokens, or nil before it has one.
func (fp *folderProject) projectStore() *project.Store {
	fp.stMu.Lock()
	defer fp.stMu.Unlock()
	if fp.st == nil {
		if st, err := project.OpenExisting(fp.found.Root); err == nil {
			fp.st = st
		}
	}
	return fp.st
}

func (fp *folderProject) close() error {
	errs := []error{fp.srv.Close()}
	fp.stMu.Lock()
	if fp.st != nil {
		errs = append(errs, fp.st.Close())
		fp.st = nil
	}
	fp.stMu.Unlock()
	return errors.Join(errs...)
}

// homeStore returns the folder's home store (its share tokens and hosts), or nil while it doesn't exist.
func (f *Folder) homeStore() *project.Store {
	f.storeMu.Lock()
	defer f.storeMu.Unlock()
	if f.store == nil {
		if st, err := project.OpenHomeExisting(f.opts.Dir); err == nil {
			f.store = st
		} else if !errors.Is(err, project.ErrNoProject) {
			f.logf("home store: %v", err)
		}
	}
	return f.store
}

// Close stops every project's server (their jobs are queued again) and closes the stores.
func (f *Folder) Close() error {
	f.close.Do(func() { f.closeErr = f.closeAll() })
	return f.closeErr
}

func (f *Folder) closeAll() error {
	f.mu.Lock()
	f.closed = true
	open := f.open
	f.open = map[string]*folderProject{}
	f.mu.Unlock()
	errs := []error{f.closeMenus()}
	for _, fp := range open {
		errs = append(errs, fp.close())
	}
	f.storeMu.Lock()
	if f.store != nil {
		errs = append(errs, f.store.Close())
		f.store = nil
	}
	f.storeMu.Unlock()
	return errors.Join(errs...)
}

func (f *Folder) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(f.opts.Log, "retake: "+format+"\n", args...)
}

func (f *Folder) touch() {
	f.contactMu.Lock()
	f.contact = f.opts.Now()
	f.contactMu.Unlock()
}

// Idle reports whether every page has been gone for longer than IdleExit (never before the first request), and no
// project's agent is at work.
func (f *Folder) Idle() bool {
	if f.opts.IdleExit <= 0 {
		return false
	}
	f.contactMu.Lock()
	idle := !f.contact.IsZero() && f.opts.Now().Sub(f.contact) > f.opts.IdleExit
	f.contactMu.Unlock()
	if !idle {
		return false
	}
	f.mu.Lock()
	open := make([]*folderProject, 0, len(f.open))
	for _, fp := range f.open {
		open = append(open, fp)
	}
	f.mu.Unlock()
	for _, fp := range open {
		if fp.srv.Busy() {
			return false
		}
	}
	return true
}

// wake starts the workers of the projects with agent work in flight, without waiting for a page to open them: an agent
// that outlived the last run is taken back at once (spec/automation.md, "Agents outlive Retake").
func (f *Folder) wake() {
	if f.opts.LocalWorker == nil || f.opts.Working == nil {
		return
	}
	for _, fd := range f.rescan(false) {
		if f.opts.Working(fd.Root) {
			f.projectOf(fd)
		}
	}
}

// Serve serves on ln until ctx is done, or until the pages have been gone for IdleExit. idle reports which. Call Close
// once it has returned.
func (f *Folder) Serve(ctx context.Context, ln net.Listener) (idle bool, err error) {
	go f.wake()
	errLog := io.Discard
	if f.opts.Verbose {
		errLog = f.opts.Log
	}
	hs := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: log.New(errLog, "retake: ", 0)}
	hs.RegisterOnShutdown(func() { _ = f.Close() }) // ends the projects' event streams, so shutdown doesn't wait
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	tick := time.NewTicker(f.opts.CheckEvery)
	defer tick.Stop()
	stop := func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
	}
	for {
		select {
		case <-ctx.Done():
			stop()
			return false, nil
		case err := <-errc:
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			return false, err
		case <-tick.C:
			if f.Idle() {
				stop()
				return true, nil
			}
		}
	}
}

// Cards returns the home's cards, in the home's order (spec/home.md, "GET /api/home").
func (f *Folder) Cards() []home.Card { return f.cards(nil) }

func (f *Folder) cards(allowed func(string) bool) []home.Card {
	return f.cardsFrom(f.rescan(false), allowed)
}

func (f *Folder) cardsFrom(found []home.Found, allowed func(string) bool) []home.Card {
	cards := make([]home.Card, 0, len(found))
	for _, fd := range found {
		if allowed != nil && !allowed(fd.Name) {
			continue
		}
		fp := f.projectOf(fd)
		if fp == nil {
			continue
		}
		cards = append(cards, home.Make(home.Source{Name: fd.Name, Title: fd.Title, Path: fd.Path,
			Project: fd.Project, Info: fp.srv.InfoJSON(), Store: fp.projectStore()}))
	}
	home.Sort(cards)
	return cards
}

// --- requests

// classify says whether a request is local or shared (spec/team.md, "Hosts"); ok is false for a Host we don't
// answer to.
func (f *Folder) classify(r *http.Request) (shared, ok bool) {
	if viaLAN(r) {
		return true, true // the local network listener checked the Host; nothing there is the owner's
	}
	host := strings.ToLower(r.Host)
	if f.local[host] {
		return server.Forwarded(r), true
	}
	if f.shareHost(host) {
		return true, true
	}
	return false, false
}

// originOK is the POST rule of spec/api.md and spec/team.md: no Origin, or our own (local, or https://<share host>).
func (f *Folder) originOK(r *http.Request) bool {
	vals := r.Header.Values("Origin")
	switch len(vals) {
	case 0:
		return true
	case 1:
		if f.origins[vals[0]] {
			return true
		}
		if viaLAN(r) {
			return f.lanOrigin(vals[0])
		}
		h, ok := strings.CutPrefix(vals[0], "https://")
		if !ok || h == "" {
			return false
		}
		h = strings.ToLower(h)
		return f.shareHost(h)
	}
	return false
}

// ServeHTTP implements http.Handler: the routes of spec/home.md.
func (f *Folder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(server.APIHeader, server.APIVersion)
	shared, ok := f.classify(r)
	sw := &statusWriter{ResponseWriter: w}
	w = sw
	if f.opts.Verbose {
		defer func() {
			_, _ = fmt.Fprintf(f.opts.Log, "retake: \"%s %s\" %d\n", r.Method, logPath(r.URL.Path), sw.status())
		}()
	}
	if !ok {
		writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "bad host"})
		return
	}
	if shared {
		w = &privateWriter{ResponseWriter: w}
	}
	parts := splitPath(r.URL.Path)
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	if len(parts) == 2 && parts[0] == "s" && get {
		f.shareLink(w, r, parts[1])
		return
	}
	if stateChanging(r.Method) && !f.originOK(r) {
		writeJSON(w, r, http.StatusForbidden, map[string]string{"error": "bad origin"})
		return
	}
	if len(parts) >= 2 && parts[0] == "p" {
		f.projectRoute(w, r, shared, parts[1], strings.TrimPrefix(r.URL.Path, "/p/"+parts[1]))
		return
	}
	page := get && (len(parts) == 0 || len(parts) == 1 && parts[0] == "index.html")
	perm := team.PermWatch
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "ping" {
		perm = team.PermAny
	}
	if isHomeExt(parts) && !get {
		perm = team.PermRecord // the extension's own role is checked inside
	}
	if isVideoMenu(parts) {
		perm = team.PermManage // Move to Trash and Show in folder are the owner's
	}
	role, _, scope, ok := f.identify(w, r, shared, nil, perm, page)
	if !ok {
		return
	}
	f.touch()
	switch {
	case page:
		f.homePage(w, r)
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "home" && get:
		f.apiHome(w, r, role, scope)
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "projects" && r.Method == http.MethodPost:
		f.createProject(w, r, shared, role)
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "library":
		f.libraryRoute(w, r, shared, role, false)
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "library" && parts[2] == "pick":
		f.libraryRoute(w, r, shared, role, true)
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "ping" && get:
		writeJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "feedback":
		f.feedback.ServeHTTP(w, r)
	case isHomeExt(parts):
		f.homeExt(w, r, role, parts)
	case isVideoMenu(parts):
		f.videoMenu(w, r, parts)
	case len(parts) == 2 && parts[0] == "api" && parts[1] == "settings":
		f.settingsRoute(w, r, role, "")
	case len(parts) == 3 && parts[0] == "api" && parts[1] == "settings":
		f.settingsRoute(w, r, role, parts[2])
	case len(parts) == 1 && parts[0] == "favicon.ico" && get:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	case !get || len(parts) == 0 || parts[0] == "api" || parts[0] == "p" || parts[0] == "s":
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		f.static(w, r, parts)
	}
}

// identify decides who a request is and checks it may do perm (spec/home.md, "Who a request is"). fp is the project
// the request is for (nil on the home's own routes): its own store's Bearer tokens count there too. It answers a
// refused request itself and returns ok false.
func (f *Folder) identify(w http.ResponseWriter, r *http.Request, shared bool, fp *folderProject, perm team.Perm,
	page bool) (role team.Role, author, scope string, ok bool) {
	tok, isBearer := "", false
	if b := bearer(r); b != "" {
		tok, isBearer = b, true
	} else if shared && perm != team.PermAgent {
		if c, err := r.Cookie(ShareCookie); err == nil && c.Value != "" {
			tok = c.Value
		}
	}
	if tok == "" {
		if shared || perm == team.PermAgent {
			w.Header().Set("WWW-Authenticate", "Bearer")
			f.refuse(w, r, http.StatusUnauthorized, "token required", page)
			return "", "", "", false
		}
		role = team.Owner
	} else {
		t, found := f.check(w, r, tok, isBearer, fp, page)
		if !found {
			return "", "", "", false
		}
		role, author, scope = team.Role(t.Role), t.Label, t.Scope
	}
	if !role.Can(perm) {
		f.refuse(w, r, http.StatusForbidden, "not allowed", page)
		return "", "", "", false
	}
	if retiredScope(scope) {
		f.refuse(w, r, http.StatusGone, "retired link", page)
		return "", "", "", false
	}
	if fp != nil && !scopeAllows(scope, fp.found.Name) {
		f.refuse(w, r, http.StatusForbidden, "not allowed", page)
		return "", "", "", false
	}
	return role, author, scope, true
}

// check looks up folder and legacy video tokens under the same rate limit. Video tokens always carry a video scope.
func (f *Folder) check(w http.ResponseWriter, r *http.Request, tok string, isBearer bool, fp *folderProject,
	page bool) (project.Token, bool) {
	key, now := server.ClientKey(r), f.opts.Now()
	if blocked, wait := f.limiter.Blocked(key, now); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		f.refuse(w, r, http.StatusTooManyRequests, "too many attempts", page)
		return project.Token{}, false
	}
	type tokenStore struct {
		st    *project.Store
		video string
	}
	stores := []tokenStore{{st: f.homeStore()}}
	if fp != nil {
		stores = append(stores, tokenStore{fp.projectStore(), fp.found.Name})
	}
	if team.WellFormed(tok) {
		for _, fd := range f.rescan(false) {
			if fp != nil && fd.Name == fp.found.Name {
				continue
			}
			if video := f.projectOf(fd); video != nil {
				stores = append(stores, tokenStore{video.projectStore(), fd.Name})
			}
		}
	}
	if team.WellFormed(tok) {
		for _, entry := range stores {
			st := entry.st
			if st == nil {
				continue
			}
			t, err := st.TokenByHash(team.Hash(tok))
			if err == nil && t.Valid(now) {
				if entry.video != "" {
					t.Scope = "video:" + entry.video
				}
				return t, true
			}
			if err != nil && !errors.Is(err, project.ErrNotFound) {
				f.logf("token store: %v", err)
				writeJSON(w, r, http.StatusInternalServerError, map[string]string{"error": "store failed"})
				return project.Token{}, false
			}
		}
	}
	f.limiter.Fail(key, now)
	if !isBearer {
		http.SetCookie(w, &http.Cookie{Name: ShareCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true,
			SameSite: http.SameSiteStrictMode})
	}
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	f.refuse(w, r, http.StatusUnauthorized, "bad token", page)
	return project.Token{}, false
}

// refuse answers a refused request: JSON, or a short page for the page routes.
func (f *Folder) refuse(w http.ResponseWriter, r *http.Request, code int, msg string, page bool) {
	if !page {
		writeJSON(w, r, code, map[string]string{"error": msg})
		return
	}
	text := map[string]string{ //nolint:gosec // messages about tokens, not credentials
		"token required":    "These reviews need a share link. Ask whoever sent you here for one.",
		"bad token":         "This link has expired or was revoked. Ask whoever sent it for a new one.",
		"too many attempts": "Too many tries with a bad link. Wait a minute, then open your link again.",
		"not allowed":       "This link can't open the reviews.",
		"retired link": "This link was for a project or group that Retake no longer has (folders on disk organize " +
			"videos now). Ask whoever sent it for a new link.",
	}[msg]
	writePage(w, r, code, "Retake", "<p>"+esc(text)+"</p>")
}

// shareLink is GET /s/<token>: a share link for the whole home. It checks the token against the folder's home store,
// sets the share cookie for / and bounces to the home.
func (f *Folder) shareLink(w http.ResponseWriter, r *http.Request, tok string) {
	t, ok := f.check(w, r, tok, true, nil, true)
	if !ok {
		return
	}
	if team.Role(t.Role) == team.Agent {
		writePage(w, r, http.StatusForbidden, "Retake", "<p>This is an agent's token, not a share link.</p>")
		return
	}
	if retiredScope(t.Scope) {
		f.refuse(w, r, http.StatusGone, "retired link", true)
		return
	}
	f.touch()
	age := maxCookieAge
	if exp, ok := t.ExpiresAt(); ok {
		age = min(age, exp.Sub(f.opts.Now()))
	}
	// On the local network the page is plain HTTP, where a browser drops a Secure cookie (spec/team.md).
	//nolint:gosec // Secure except on the local network's plain HTTP address, where a browser would drop it
	http.SetCookie(w, &http.Cookie{Name: ShareCookie, Value: tok, Path: "/", MaxAge: max(1, int(age.Seconds())),
		HttpOnly: true, Secure: !viaLAN(r), SameSite: http.SameSiteStrictMode})
	to := "/"
	if video, ok := strings.CutPrefix(t.Scope, "video:"); ok && video != "" {
		to = "/p/" + video + "/" // a video's link opens its page (spec/home.md, "Sharing the home")
	}
	body := bouncePage(to)
	hd := w.Header()
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

// projectRoute is everything under /p/<name>/: the project's review page and API, as retake open <video> serves them.
func (f *Folder) projectRoute(w http.ResponseWriter, r *http.Request, shared bool, name, rest string) {
	fp := f.projectNamed(name)
	if fp == nil {
		writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "no such project"})
		return
	}
	if rest == "" {
		target := "/p/" + fp.found.Name + "/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect) //nolint:gosec // our own path: /p/<valid name>/
		return
	}
	parts := splitPath(rest)
	page := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(len(parts) == 0 || len(parts) == 1 && parts[0] == "index.html")
	role, author, scope, ok := f.identify(w, r, shared, fp, server.PermFor(r.Method, rest), page)
	if !ok {
		return
	}
	if shared && scope == "" && !f.legacyProject(fp.found) {
		f.refuse(w, r, http.StatusForbidden, "not allowed", page)
		return
	}
	f.touch()
	r2 := r.Clone(r.Context())
	r2.URL.Path = rest
	r2.URL.RawPath = ""
	fp.srv.ServeHTTP(w, server.Grant(r2, role, author))
}

// apiHome is GET /api/home.
func (f *Folder) apiHome(w http.ResponseWriter, r *http.Request, role team.Role, scope string) {
	if f.libraryError() != nil {
		f.libraryUnavailable(w, r)
		return
	}
	found := f.rescan(false)
	legacy := map[string]bool(nil)
	if role != team.Owner && scope == "" {
		legacy = make(map[string]bool, len(found))
		for _, fd := range found {
			if f.legacyProject(fd) {
				legacy[fd.Name] = true
			}
		}
	}
	cards := f.cardsFrom(found, func(video string) bool {
		return scopeAllows(scope, video) && (role == team.Owner || scope != "" || legacy[video])
	})
	title := filepath.Base(f.opts.Dir)
	projects := []string(nil)
	if role == team.Owner {
		projects = f.libraryTreeProjects()
	}
	res := home.Response{Title: title, Role: string(role), Projects: cards, Tree: home.BuildTree(title, cards, projects)}
	res.Machines = []home.Machine{}
	if role == team.Owner {
		res.Folder = f.opts.Dir
		res.Trash = f.opts.Trash.Name()
		res.Machines = f.machines()
	}
	writeJSON(w, r, http.StatusOK, res)
}

// HomePage is the page's home screen entry.
const HomePage = "home.html"

// homePage is GET /: the home screen.
func (f *Folder) homePage(w http.ResponseWriter, r *http.Request) {
	body := []byte(ui.NotBuiltHTML)
	if f.opts.UI != nil {
		if b, err := fs.ReadFile(f.opts.UI, HomePage); err == nil {
			body = b
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// static serves one of the page's files at the home's root (/assets/...). Hidden files and folders are never served.
func (f *Folder) static(w http.ResponseWriter, r *http.Request, parts []string) {
	notFound := func() { writeJSON(w, r, http.StatusNotFound, map[string]string{"error": "not found"}) }
	for _, p := range parts {
		if strings.HasPrefix(p, ".") || strings.ContainsAny(p, "\\:\x00") {
			notFound()
			return
		}
	}
	name := path.Join(parts...)
	if f.opts.UI == nil || !fs.ValidPath(name) {
		notFound()
		return
	}
	b, err := fs.ReadFile(f.opts.UI, name)
	if err != nil {
		notFound()
		return
	}
	var mod time.Time
	if st, err := fs.Stat(f.opts.UI, name); err == nil {
		mod = st.ModTime()
	}
	w.Header().Set("Content-Type", server.MediaType(name))
	w.Header().Set("Cache-Control", "no-cache")
	theme.Headers(w.Header(), name) // a theme file: its own type, nosniff and a CSP (spec/theme.md)
	http.ServeContent(w, r, name, mod, bytes.NewReader(b))
}

// privateWriter adds "private" to a shared response's Cache-Control, so a CDN in front of a tunnel never caches it.
type privateWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *privateWriter) mark() {
	if w.wrote {
		return
	}
	w.wrote = true
	h := w.Header()
	switch cc := h.Get("Cache-Control"); {
	case cc == "":
		h.Set("Cache-Control", "private")
	case !strings.Contains(cc, "private"):
		h.Set("Cache-Control", "private, "+cc)
	}
}

func (w *privateWriter) WriteHeader(code int) {
	w.mark()
	w.ResponseWriter.WriteHeader(code)
}

func (w *privateWriter) Write(b []byte) (int, error) {
	w.mark()
	return w.ResponseWriter.Write(b)
}

func (w *privateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
