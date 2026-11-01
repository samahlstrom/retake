// Package server is the local HTTP API of spec/api.md: the review page, the version files with range support,
// the family/take reads, and the take writes. It is byte-compatible with the legacy Python player, so the legacy
// page runs against it unchanged.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/store"
	"github.com/samahlstrom/retake/internal/stream"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/thumb"
	"github.com/samahlstrom/retake/internal/ui"
)

// DefaultMaxBody is the largest POST body accepted (1 GiB).
const DefaultMaxBody int64 = 1 << 30

// DefaultCheckEvery is how often the idle watchdog looks at the last contact.
const DefaultCheckEvery = 10 * time.Second

// Clock tells the time. Tests swap it to drive the idle watchdog.
type Clock interface{ Now() time.Time }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Options configure a Server.
type Options struct {
	Video    string   // the opened file: absolute, symlinks resolved
	Explicit []string // --versions files, absolute and resolved; nil = detect the family from file names
	Port     int      // the port the page is served on; POSTs must come from this origin
	MaxBody  int64    // 0 = DefaultMaxBody
	UI       fs.FS    // the page's files; nil serves ui.NotBuiltHTML
	Author   string   // written to session.json "author"; "" leaves it out
	// AllowHosts are extra hostnames the page may be reached at over HTTPS, e.g. a tunnel's (abc.trycloudflare.com):
	// their Host header is accepted and https://<host> counts as our origin for writes. Requests through them need a
	// token (spec/team.md), as do those through the hostnames of running shares (the project's hosts table).
	AllowHosts []string
	Prober     family.Prober

	IdleExit   time.Duration // exit this long after the last request, once there was one; 0 = never
	CheckEvery time.Duration // 0 = DefaultCheckEvery
	Clock      Clock         // nil = the real clock

	Log     io.Writer // request log (with Verbose) and server errors; nil = discard
	Verbose bool

	// Root is the project root (spec/notes.md); "" = project.Root(Video). Its store, <Root>/.retake/project.db, is
	// opened if it exists, and created by the first request that writes to it (Done).
	Root string
	// Jobs are the engines behind the transcribe and notes jobs that Done queues; the server runs those jobs while
	// it has the project store open. nil: jobs are queued but not run.
	Jobs *jobs.Engines
	// EventPoll is how often /api/events looks for events written by other processes; 0 = 250 ms.
	EventPoll time.Duration
	// KeepAlive is how often /api/events sends a comment to keep idle proxies from closing it; 0 = 15 s.
	KeepAlive time.Duration

	// Version is the build's version, which /mcp reports to agents.
	Version string
	// Limiter counts failed token attempts; nil = team.NewLimiter().
	Limiter *team.Limiter

	// The options below are for retake serve (spec/server.md), which mounts one Server per project under
	// /p/<name>/. retake open and retake share leave them unset.

	// External says the caller has already decided who each request is and what it may do, and put that on the
	// request with Grant: the Host, origin and token checks of spec/team.md are skipped, and a request without a
	// grant is refused.
	External bool
	// BasePath is the path the Server is mounted at ("/p/demo"), for the URLs it makes (take images).
	BasePath string
	// Store is the project store to use instead of opening <Root>/.retake/project.db; Close leaves it open.
	Store *project.Store
	// FollowNewest makes the newest version of the family the current one, instead of the opened file.
	FollowNewest bool
	// Media, when set, may serve a version file or take image itself (from object storage, say): it returns false
	// to let the Server serve the file at path from disk.
	Media func(w http.ResponseWriter, r *http.Request, path, contentType string) bool
	// Editor finds retake-edit, for the streamed versions it renders (spec/bridge.md, "Streaming") and /mcp's pushes.
	Editor bridge.Editor
	// Streams renders streamed versions while pages watch them; nil = the Server runs its own (and stops it on Close).
	// A home that serves many projects can share one.
	Streams *stream.Manager
	// MCPFiles resolves version_push's file argument on /mcp (an uploaded file's reference) to a path, and whether
	// the push may move it. nil keeps spec/team.md's rule: a file inside the project root.
	MCPFiles func(file string) (path string, move bool, err error)

	// The home screen (spec/home.md). Thumbs makes the versions' posters for /api/poster; nil = thumb.Default.
	// Unwatch is told when a page leaves a version (POST /api/versions/<label>/unwatch): the streaming side's hook to
	// stop a render no one watches. nil: the call does nothing.
	Thumbs  *thumb.Maker
	Unwatch func(label, viewer string)

	// LocalWorker runs the project's editing worker inside this process (retake open and share; spec/automation.md,
	// "Local mode"). New starts it once with the project root and WorkerHandler; Close cancels ctx and waits for it.
	// It calls blocked with why it can't run ("" once it runs).
	LocalWorker func(ctx context.Context, root string, h http.Handler, blocked func(string))

	// MediaCache keeps the chunks this Server relays from workers, under CacheScope (the project's name), so a later
	// viewer doesn't cost the worker's upload again (spec/automation.md, "The server's cache"). nil caches nothing.
	MediaCache *relay.Cache
	CacheScope string
}

// Server serves one version family.
type Server struct {
	mediaRelay relay.Hub
	opts       Options
	origins    map[string]bool // our own origins, and https://<h> of each AllowHosts name
	local      map[string]bool // the local Host headers: 127.0.0.1:<port>, localhost:<port>
	allow      map[string]bool // AllowHosts, lower case; with the hosts table, the shared ones. Any other Host is refused.
	limiter    *team.Limiter
	fam        atomic.Pointer[[]family.Version]
	writeMu    sync.Mutex // serializes every take write

	contactMu sync.Mutex
	contact   time.Time // zero until the first request

	root     string
	projMu   sync.Mutex
	proj     *project.Store // nil until the store exists
	ctx      context.Context
	cancel   context.CancelFunc
	jobsDone chan struct{} // closed when the job runner returns; nil if it never started
	quit     chan struct{} // closed when the server shuts down: ends the event streams
	quitOnce sync.Once

	mcpMu sync.Mutex
	mcpH  http.Handler // /mcp, built on first use

	streamsMu  sync.Mutex
	streams    *stream.Manager // opts.Streams, or one made on the first watch (then ownStreams)
	ownStreams bool

	inproc localWorker // Options.LocalWorker's state
}

// New returns a Server and scans the family once.
func New(opts Options) *Server {
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.CheckEvery <= 0 {
		opts.CheckEvery = DefaultCheckEvery
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Limiter == nil {
		opts.Limiter = team.NewLimiter()
	}
	s := &Server{opts: opts, origins: map[string]bool{
		fmt.Sprintf("http://127.0.0.1:%d", opts.Port): true,
		fmt.Sprintf("http://localhost:%d", opts.Port): true,
	}, local: map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", opts.Port): true,
		fmt.Sprintf("localhost:%d", opts.Port): true,
	}, allow: map[string]bool{}, limiter: opts.Limiter}
	for _, h := range opts.AllowHosts {
		h = strings.ToLower(h)
		s.allow[h] = true
		s.origins["https://"+h] = true
	}
	s.root = opts.Root
	if s.root == "" {
		s.root = project.Root(opts.Video)
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.quit = make(chan struct{})
	s.Rescan()
	if _, err := s.store(false); err != nil && !errors.Is(err, project.ErrNoProject) {
		_, _ = fmt.Fprintf(s.opts.Log, "retake: project store: %v\n", err)
	}
	s.startLocalWorker()
	return s
}

// store returns the project store, opening it if it exists, or creating it when create is set. Opening it starts
// the job runner. Without create, a project that has no store yet gives project.ErrNoProject.
func (s *Server) store(create bool) (*project.Store, error) {
	s.projMu.Lock()
	defer s.projMu.Unlock()
	if s.proj != nil {
		return s.proj, nil
	}
	if s.ctx.Err() != nil {
		return nil, errors.New("the server is closed")
	}
	st := s.opts.Store
	if st == nil {
		open := project.OpenExisting
		if create {
			open = project.Open
		}
		var err error
		if st, err = open(s.root); err != nil {
			return nil, err
		}
	}
	if s.opts.EventPoll > 0 {
		st.Poll = s.opts.EventPoll
	}
	s.proj = st
	if s.opts.Jobs != nil {
		r := &jobs.Runner{Store: st, Handlers: jobs.Handlers(st, s.Rescan, *s.opts.Jobs), Log: s.opts.Log}
		s.jobsDone = make(chan struct{})
		go func() {
			defer close(s.jobsDone)
			if err := r.Run(s.ctx); err != nil {
				_, _ = fmt.Fprintf(s.opts.Log, "retake: jobs: %v\n", err)
			}
		}()
	}
	return st, nil
}

// Close stops the job runner (a running job is queued again) and closes the project store. Serve doesn't call it,
// so call it once Serve has returned.
func (s *Server) Close() error {
	s.stopStreams()
	s.cancel()
	s.inproc.wait() // before the store closes: the worker's last reports go through it
	s.streamsMu.Lock()
	if s.ownStreams {
		s.streams.Close() // renders stop; what they published stays
		s.streams, s.ownStreams = nil, false
	}
	s.streamsMu.Unlock()
	s.projMu.Lock()
	defer s.projMu.Unlock()
	if s.jobsDone != nil {
		<-s.jobsDone
	}
	if s.proj == nil {
		return nil
	}
	var err error
	if s.opts.Store == nil {
		err = s.proj.Close() // a store given in Options belongs to the caller
	}
	s.proj = nil
	return err
}

// stopStreams ends the open event streams, so a shutdown doesn't wait for them.
func (s *Server) stopStreams() { s.quitOnce.Do(func() { close(s.quit) }) }

// Rescan detects the family again and returns it. Only GET /api/info rescans; other routes use the last scan.
func (s *Server) Rescan() []family.Version {
	f := []family.Version{}
	if s.opts.Video != "" {
		f = family.Detect(s.opts.Video, s.opts.Explicit, s.opts.Prober)
	}
	f = s.remoteFamily(f)
	if s.opts.FollowNewest && len(f) > 0 {
		for i := range f {
			f[i].Current = i == len(f)-1
		}
	}
	s.fam.Store(&f)
	return f
}

// Family returns the last scanned family.
func (s *Server) Family() []family.Version { return *s.fam.Load() }

// Current returns the opened file's version.
func (s *Server) Current() family.Version {
	for _, v := range s.Family() {
		if v.Current {
			return v
		}
	}
	// Detect always keeps the opened file; this is only reached if it vanished from a hand-built family.
	return family.Version{Label: "current", Path: s.opts.Video, Name: filepath.Base(s.opts.Video), Current: true}
}

// version finds a version by label; an empty label means the current one.
func (s *Server) version(label string) (family.Version, bool) {
	if label == "" {
		return s.Current(), true
	}
	for _, v := range s.Family() {
		if v.Label == label {
			return v, true
		}
	}
	return s.unlistedRender(label) // a draft the family no longer lists still takes its takes
}

func (s *Server) touch() {
	s.contactMu.Lock()
	s.contact = s.opts.Clock.Now()
	s.contactMu.Unlock()
}

// Idle reports whether the page has been gone for longer than IdleExit (never before the first request).
func (s *Server) Idle() bool {
	if s.opts.IdleExit <= 0 {
		return false
	}
	s.contactMu.Lock()
	idle := !s.contact.IsZero() && s.opts.Clock.Now().Sub(s.contact) > s.opts.IdleExit
	s.contactMu.Unlock()
	return idle && !s.Busy() // a local agent at work keeps the server up
}

// Serve serves on ln until ctx is done, or until the page has been gone for IdleExit. idle reports which.
func (s *Server) Serve(ctx context.Context, ln net.Listener) (idle bool, err error) {
	hs := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(s.errorLog(), "retake: ", 0),
	}
	hs.RegisterOnShutdown(s.stopStreams)
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	tick := time.NewTicker(s.opts.CheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown(hs)
			return false, nil
		case err := <-errc:
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			return false, err
		case <-tick.C:
			if s.Idle() {
				shutdown(hs)
				return true, nil
			}
		}
	}
}

func shutdown(hs *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := hs.Shutdown(ctx); err != nil {
		_ = hs.Close() // a video stream still open: cut it
	}
}

func (s *Server) errorLog() io.Writer {
	if s.opts.Verbose {
		return s.opts.Log
	}
	return io.Discard
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.opts.External {
		s.serveGranted(w, r)
		return
	}
	// A page on another site can point its own hostname at 127.0.0.1 (DNS rebinding) and then read our videos and
	// takes as "same origin". Its requests still carry its hostname, so only answer to our own and our shares'.
	shared, ok := s.classify(r)
	if !ok {
		s.fail(w, r, http.StatusForbidden, "bad host")
		return
	}
	if s.opts.Verbose {
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		w = sw
		defer func() {
			uri := r.RequestURI
			if strings.HasPrefix(r.URL.Path, "/s/") {
				uri = "/s/<token>" // a share link's token stays out of the log
			}
			_, _ = fmt.Fprintf(s.opts.Log, "retake: \"%s %s %s\" %d -\n", r.Method, uri, r.Proto, sw.code)
		}()
	}
	if shared {
		w = &privateWriter{ResponseWriter: w}
	}
	parts := splitPath(r.URL.Path)
	if len(parts) == 2 && parts[0] == "s" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		s.shareLink(w, r, parts[1])
		return
	}
	a, ok := s.authorize(w, r, parts, shared)
	if !ok {
		return // refused requests don't count as contact
	}
	s.touch()
	s.route(w, withAccess(r, a), parts)
}

// serveGranted serves a request that the caller (retake serve) has already authorized and granted (Options.External).
// The route's permission is checked once more against the granted role.
func (s *Server) serveGranted(w http.ResponseWriter, r *http.Request) {
	w = &privateWriter{ResponseWriter: w} // every answer depends on who asks
	a, ok := r.Context().Value(accessKey{}).(access)
	parts := splitPath(r.URL.Path)
	if !ok || !a.role.Can(permFor(r.Method, parts)) {
		s.fail(w, r, http.StatusForbidden, "not allowed")
		return
	}
	if s.opts.Verbose {
		_, _ = fmt.Fprintf(s.opts.Log, "retake: %s %s%s (%s)\n", r.Method, s.opts.BasePath, r.URL.Path, a.role)
	}
	s.touch()
	s.route(w, r, parts)
}

// route sends an authorized request to its handler.
func (s *Server) route(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) >= 2 && parts[0] == "api" && (parts[1] == "automation" || parts[1] == "worker") {
		s.automationRoute(w, r, parts)
		return
	}
	if len(parts) == 1 && parts[0] == "mcp" {
		s.mcpRoute(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.get(w, r, parts)
	case http.MethodPost:
		s.post(w, r, parts)
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// splitPath splits a URL path into its non-empty segments, like the legacy server ("//video/" is "video").
func splitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func is(parts []string, want ...string) bool { return slices.Equal(parts, want) }

// query returns a query parameter like Python's dict(parse_qsl(...)): blank values are dropped and the last wins.
func query(r *http.Request, key string) string {
	vals := r.URL.Query()[key]
	for i := len(vals) - 1; i >= 0; i-- {
		if vals[i] != "" {
			return vals[i]
		}
	}
	return ""
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case len(parts) == 0 || is(parts, "index.html"):
		s.page(w, r)
	case parts[0] == "video" && len(parts) <= 2:
		label := ""
		if len(parts) == 2 {
			label = parts[1]
		}
		v, ok := s.version(label)
		if !ok {
			s.fail(w, r, http.StatusNotFound, "no such version")
			return
		}
		ctype, ok := family.ContentTypes[strings.ToLower(family.Suffix(v.Name))]
		if s.remoteMedia(w, r, v, "") {
			return
		}
		if !ok {
			ctype = "application/octet-stream"
		}
		s.serveFile(w, r, v.Path, ctype)
	case parts[0] == "video" && len(parts) == 3:
		if v, ok := s.version(parts[1]); ok && s.remoteMedia(w, r, v, parts[2]) {
			return
		}
		s.playlistFile(w, r, parts[1], parts[2])
	case is(parts, "api", "ping"):
		s.json(w, r, http.StatusOK, map[string]bool{"ok": true})
	case is(parts, "api", "info"):
		s.info(w, r)
	case is(parts, "api", "history"):
		v, ok := s.version(query(r, "v"))
		if !ok {
			s.fail(w, r, http.StatusNotFound, "no such version")
			return
		}
		history := s.history(v)
		if query(r, "scope") == "review" {
			history = s.reviewHistory(v, history)
		}
		s.json(w, r, http.StatusOK, historyResponse{Label: v.Label, History: history})
	case is(parts, "favicon.ico"):
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	case is(parts, "api", "notes"):
		s.sidebar(w, r)
	case is(parts, "api", "events"):
		s.events(w, r)
	case is(parts, "api", "timemap"):
		s.timemap(w, r)
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "notes" && parts[3] == "snapshot":
		s.noteSnapshot(w, r, parts[2])
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "takes":
		s.takeFile(w, r, parts[2], parts[3])
	case len(parts) == 4 && parts[0] == "api" && parts[1] == "questions":
		s.questionFile(w, r, parts[2], parts[3])
	case is(parts, "api", "poster"):
		s.poster(w, r)
	case parts[0] == "api" || parts[0] == "video":
		s.fail(w, r, http.StatusNotFound, "not found")
	default:
		s.static(w, r, parts)
	}
}

type infoResponse struct {
	family.Version
	Versions  []family.Version `json:"versions"`
	Stem      string           `json:"stem"`
	ReviewDir string           `json:"review_dir"`
	Sessions  []store.Summary  `json:"sessions"`
	History   []historyEntry   `json:"history"`
	Role      team.Role        `json:"role"` // spec/team.md
}

type historyResponse struct {
	Label   string         `json:"label"`
	History []historyEntry `json:"history"`
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	fam := s.Rescan()
	cur := s.Current()
	s.json(w, r, http.StatusOK, infoResponse{
		Version:   cur,
		Versions:  fam,
		Stem:      family.Stem(filepath.Base(cur.Path)),
		ReviewDir: store.ReviewDir(cur.Path),
		Sessions:  store.Sessions(cur.Path),
		History:   s.history(cur),
		Role:      accessOf(r).role,
	})
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	body := []byte(ui.NotBuiltHTML)
	if s.opts.UI != nil {
		if b, err := fs.ReadFile(s.opts.UI, "index.html"); err == nil {
			body = b
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // the page's own index.html, not request data
	}
}

func (s *Server) json(w http.ResponseWriter, r *http.Request, code int, v any) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		code, body = http.StatusInternalServerError, []byte(`{"error": "encoding failed"}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) //nolint:gosec // encoded JSON with application/json and nosniff, never HTML
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	s.json(w, r, code, map[string]string{"error": msg})
}

func (s *Server) originOK(r *http.Request) bool {
	if s.opts.External {
		return true // the caller checked the origin (spec/server.md, "CSRF")
	}
	vals := r.Header.Values("Origin")
	switch len(vals) {
	case 0:
		return true
	case 1:
		if s.origins[vals[0]] {
			return true
		}
		h, ok := strings.CutPrefix(vals[0], "https://")
		return ok && h != "" && s.shareHost(strings.ToLower(h)) // a running share's hostname
	}
	return false
}

func (s *Server) post(w http.ResponseWriter, r *http.Request, parts []string) {
	if !s.originOK(r) {
		s.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	limit := s.opts.MaxBody
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "session" && parts[3] == "image" {
		limit = min(limit, MaxImage)
	}
	if r.ContentLength > limit {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			s.fail(w, r, http.StatusRequestEntityTooLarge, "body too large")
			return
		}
		s.fail(w, r, http.StatusBadRequest, "bad body")
		return
	}
	if len(parts) == 2 && parts[0] == "api" && parts[1] == "erase" {
		s.erase(w, r, body)
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "notes" && parts[3] == "reopen" {
		s.reopen(w, r, parts[2])
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "notes" && (parts[3] == "delete" || parts[3] == "restore") {
		s.deleteComment(w, r, parts[2], parts[3] == "restore")
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "versions" && parts[3] == "watch" {
		s.watch(w, r, parts[2], true)
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "versions" && parts[3] == "approve" {
		s.approve(w, r, parts[2])
		return
	}
	if isUnwatch(parts) {
		s.unwatch(w, r, parts[2])
		return
	}
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "session" || !store.ValidTakeID(parts[2]) {
		s.fail(w, r, http.StatusNotFound, "not found")
		return
	}
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "bad version")
		return
	}
	dir := store.TakeDir(v.Path, parts[2])
	switch parts[3] {
	case "data":
		s.saveData(w, r, v, dir, body)
	case "audio":
		s.saveAudio(w, r, dir, body)
	case "image":
		s.saveImage(w, r, dir, body)
	case "transcribe":
		s.transcribeClips(w, r, v, parts[2], dir)
	case "done":
		s.done(w, r, v, parts[2], dir, body)
	default:
		s.fail(w, r, http.StatusNotFound, "not found")
	}
}

func (s *Server) serverFields(v family.Version) store.ServerFields {
	fam := s.Family()
	refs := make([]store.VersionRef, 0, len(fam))
	for _, x := range fam {
		refs = append(refs, store.VersionRef{Label: x.Label, Path: x.Path, Duration: x.Duration})
	}
	sf := store.ServerFields{
		Version:  v.Label,
		Versions: refs,
		Video: store.VideoRef{
			Path: v.Path, Name: v.Name, Duration: v.Duration, FPS: v.FPS, Width: v.Width, Height: v.Height,
		},
	}
	if s.opts.Author != "" {
		sf.Author = &store.Author{Name: s.opts.Author}
	}
	return sf
}

func (s *Server) saveData(w http.ResponseWriter, r *http.Request, v family.Version, dir string, body []byte) {
	sf := s.serverFields(v)
	if a := accessOf(r); a.author != "" {
		sf.Author = &store.Author{Name: a.author} // a signed-in member's name, or a guest's (spec/server.md)
	} else if a.token != nil && a.token.Label != "" {
		sf.Author = &store.Author{Name: a.token.Label} // a share link's person (spec/team.md)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	closed := takeClosed(dir)
	err := store.SaveData(dir, body, sf)
	if err == nil && closed && !takeClosed(dir) {
		err = markClosed(dir) // a save after Done keeps the take closed
	}
	switch {
	case errors.Is(err, store.ErrBadJSON):
		s.fail(w, r, http.StatusBadRequest, "bad json")
	case err != nil:
		_, _ = fmt.Fprintf(s.opts.Log, "retake: saving %s failed: %v\n", dir, err)
		s.fail(w, r, http.StatusInternalServerError, "save failed")
	default:
		s.json(w, r, http.StatusOK, struct {
			OK  bool   `json:"ok"`
			Dir string `json:"dir"`
		}{true, dir})
	}
}

func (s *Server) saveAudio(w http.ResponseWriter, r *http.Request, dir string, body []byte) {
	name := query(r, "name")
	if !store.ValidSegName(name) {
		s.fail(w, r, http.StatusBadRequest, "bad name")
		return
	}
	offset, ok := parseOffset(query(r, "offset"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "bad offset")
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	size, err := store.WriteAudio(filepath.Join(dir, name), offset, body)
	var past *store.OffsetError
	switch {
	case errors.As(err, &past):
		s.json(w, r, http.StatusConflict, struct {
			Size  int64  `json:"size"`
			Error string `json:"error"`
		}{past.Size, "offset past end"})
	case err != nil:
		_, _ = fmt.Fprintf(s.opts.Log, "retake: writing %s failed: %v\n", filepath.Join(dir, name), err)
		s.fail(w, r, http.StatusInternalServerError, "save failed")
	default:
		s.json(w, r, http.StatusOK, struct {
			Size int64 `json:"size"`
		}{size})
	}
}

// parseOffset parses ?offset= like Python's int(): missing is 0, surrounding spaces and a sign are fine. A number
// too big for int64 is past any file's end, so it maps to the largest offset (409) rather than "bad offset".
func parseOffset(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			if strings.HasPrefix(s, "-") {
				return -1, true
			}
			return 1<<63 - 1, true
		}
		return 0, false
	}
	return n, true
}

// erase removes (or, with "erased": false, brings back) one line of a take shown on a version: POST /api/erase?v=
// with {"take", "id", "erased"}. The take stays as it is; the line goes in the version's erased.json, which history
// and the notes leave out (spec/take-format.md, "Erased lines").
func (s *Server) erase(w http.ResponseWriter, r *http.Request, body []byte) {
	v, ok := s.version(query(r, "v"))
	if !ok {
		s.fail(w, r, http.StatusBadRequest, "bad version")
		return
	}
	var in struct {
		Take   string `json:"take"`
		ID     int64  `json:"id"`
		Erased *bool  `json:"erased"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		s.fail(w, r, http.StatusBadRequest, "bad json")
		return
	}
	if !store.ValidTakeID(in.Take) || in.ID <= 0 {
		s.fail(w, r, http.StatusBadRequest, "bad line")
		return
	}
	if !shownTake(v.Path, in.Take) {
		s.fail(w, r, http.StatusNotFound, "no such take on this version")
		return
	}
	by := s.opts.Author
	if a := accessOf(r); a.author != "" {
		by = a.author
	} else if a.token != nil && a.token.Label != "" {
		by = a.token.Label
	}
	on := in.Erased == nil || *in.Erased
	s.writeMu.Lock()
	err := store.SetErased(store.ReviewDir(v.Path), in.Take, in.ID, on, by, time.Now())
	s.writeMu.Unlock()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "couldn't save the erase")
		return
	}
	s.json(w, r, http.StatusOK, map[string]any{"ok": true, "take": in.Take, "id": in.ID, "erased": on})
}

// shownTake reports whether a take folder is one of the version's own or carried over onto it by inherit.json.
func shownTake(video, take string) bool {
	if fi, err := os.Stat(store.TakeDir(video, take)); err == nil && fi.IsDir() {
		return true
	}
	for _, x := range store.InheritedTakes(video) {
		if filepath.Base(x.Dir) == take {
			return true
		}
	}
	return false
}
