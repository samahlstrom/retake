package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/tus"
)

// Options configure a Hub.
type Options struct {
	Stores    *Stores
	PublicURL string   // "" = none (spec/server.md, "Requests")
	Proxies   []string // trusted proxy addresses and ranges
	UI        fs.FS    // the review page; nil serves the "page not built" stub
	Jobs      *jobs.Engines
	Prober    family.Prober
	Mailer    Mailer // nil: sign-in links are only made by the CLI and the admin routes
	Version   string
	Log       io.Writer // nil = discard
	Verbose   bool
	// Now tells the time; nil = time.Now. Tests swap it.
	Now func() time.Time
	// SyncEvery is how often each project's workspace is compared with the object store; 0 = 30 s.
	SyncEvery time.Duration
	// EventPoll is how often the per-project event streams look for events of other processes; 0 = 250 ms.
	EventPoll time.Duration
	// RelayCache is the most bytes of relayed video kept under the data folder's relay-cache/ (spec/automation.md,
	// "The server's cache"); 0 keeps none.
	RelayCache int64
	// Theme is the host's theme folder (spec/theme.md), for the server's own pages (sign-in, setup): its name, logo
	// and theme.css. "" = none. The review page and the home get it through UI (theme.Page).
	Theme string
	// Exts are the extensions the admin allows (spec/extensions.md, "Turning them on"): on for everyone, each person
	// can turn one off for themselves.
	Exts *ext.Set
}

// Hub is a running team server.
type Hub struct {
	opts    Options
	st      *Stores
	public  PublicURL
	proxies Proxies
	uploads *tus.Handler
	secure  bool // cookies get Secure

	tokenFails  *team.Limiter // bad Bearer tokens, share links and cookies, sign-in and setup codes
	signinMails *team.Limiter // POST /signin

	setupMu   sync.Mutex
	setupCode string // the first-run setup code while there are no users; "" before SetupURL and once used

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	cache *relay.Cache // nil without RelayCache

	machines   machineChannel // requests for the connected computers themselves (a new video's folder)
	createWait time.Duration  // how long a new video waits for its worker to report it; 0 = 15 s (tests shorten it)

	mu        sync.Mutex
	projects  map[string]*slot
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// New makes a Hub. It doesn't open any project yet; Start does, in the background.
func New(opts Options) (*Hub, error) {
	if opts.Stores == nil {
		return nil, errors.New("hub: no stores")
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.SyncEvery <= 0 {
		opts.SyncEvery = 30 * time.Second
	}
	pub, err := ParsePublicURL(opts.PublicURL)
	if err != nil {
		return nil, err
	}
	proxies, err := ParseProxies(opts.Proxies)
	if err != nil {
		return nil, err
	}
	h := &Hub{opts: opts, st: opts.Stores, public: pub, proxies: proxies, uploads: opts.Stores.Uploads(),
		secure: pub.Scheme != "http" || pub.Loopback(), projects: map[string]*slot{}}
	h.uploads.Now = opts.Now
	h.uploads.Log = opts.Log
	h.tokenFails = team.NewLimiter()
	h.signinMails = newLimiter(5)
	h.ctx, h.cancel = context.WithCancel(context.Background())
	if opts.RelayCache > 0 {
		if h.cache, err = relay.OpenCache(opts.Stores.RelayCacheDir(), opts.RelayCache); err != nil {
			return nil, fmt.Errorf("the relay cache: %w", err)
		}
	}
	return h, nil
}

// dropGoneProjects deletes the relay cache of every project that no longer exists.
func (h *Hub) dropGoneProjects() {
	if h.cache == nil {
		return
	}
	entries, _ := os.ReadDir(h.st.RelayCacheDir())
	for _, e := range entries {
		if _, err := h.st.Accounts.Project(e.Name()); errors.Is(err, accounts.ErrNotFound) {
			if err := h.cache.Drop(e.Name()); err != nil {
				h.logf("dropping the relay cache of %s: %v", e.Name(), err)
			}
		}
	}
}

// DropProjectCache deletes a project's relayed video from the cache: for when the project is deleted.
func (h *Hub) DropProjectCache(name string) error { return h.cache.Drop(name) }

func newLimiter(maxFails int) *team.Limiter {
	l := team.NewLimiter()
	l.Max = maxFails
	return l
}

// Start opens every project in the background (downloading the files their workspaces lack, with object storage,
// and starting their job runners), records the public URL for the CLI's links, and starts the hourly sweep of
// expired uploads and sessions.
func (h *Hub) Start() {
	if h.public.Origin != "" {
		if err := h.st.Accounts.SetSetting(accounts.SettingPublicURL, h.public.Origin); err != nil {
			h.logf("recording the public URL: %v", err)
		}
	}
	h.wg.Go(func() {
		h.dropGoneProjects()
		ps, err := h.st.Accounts.Projects()
		if err != nil {
			h.logf("listing projects: %v", err)
			return
		}
		for _, p := range ps {
			if h.ctx.Err() != nil {
				return
			}
			pr, err := h.project(p.Name)
			if err != nil {
				h.logf("opening project %s: %v", p.Name, err)
				continue
			}
			pr.server() // starts its job runner: jobs queued before a restart run without waiting for a visit
		}
	})
	h.wg.Go(func() {
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			h.sweep()
			select {
			case <-h.ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
}

func (h *Hub) sweep() {
	if n, err := h.uploads.Sweep(); err != nil {
		h.logf("sweeping uploads: %v", err)
	} else if n > 0 && h.opts.Verbose {
		h.logf("removed %d expired uploads", n)
	}
	if _, err := h.st.Accounts.Prune(); err != nil {
		h.logf("pruning sessions: %v", err)
	}
}

// Close stops every project (their jobs are queued again and their files uploaded one last time) and the
// background work. It doesn't close the stores.
func (h *Hub) Close() error {
	h.closeOnce.Do(func() { h.closeErr = h.close() })
	return h.closeErr
}

func (h *Hub) close() error {
	h.mu.Lock()
	h.closed = true
	slots := make([]*slot, 0, len(h.projects))
	for _, s := range h.projects {
		slots = append(slots, s)
	}
	h.mu.Unlock()
	h.cancel()
	var errs []error
	for _, s := range slots {
		<-s.ready
		if s.p != nil {
			errs = append(errs, s.p.close())
		}
	}
	h.wg.Wait()
	return errors.Join(errs...)
}

// Serve serves on ln until ctx is done.
func (h *Hub) Serve(ctx context.Context, ln net.Listener) error {
	hs := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(h.errorLog(), "retake: ", 0),
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.stopStreams()
		if err := hs.Shutdown(sctx); err != nil {
			_ = hs.Close()
		}
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// stopStreams stops the projects, which ends their open event streams, so a shutdown doesn't wait for them.
func (h *Hub) stopStreams() {
	if err := h.Close(); err != nil {
		h.logf("stopping: %v", err)
	}
}

func (h *Hub) errorLog() io.Writer {
	if h.opts.Verbose {
		return h.opts.Log
	}
	return io.Discard
}

func (h *Hub) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(h.opts.Log, "retake: "+format+"\n", args...)
}

// statusWriter records the status code, for the verbose log and the publisher's triggers.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

// ServeHTTP implements http.Handler: the routes of spec/server.md.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ri := h.inspect(r)
	sw := &statusWriter{ResponseWriter: w}
	w = sw
	if h.opts.Verbose {
		defer func() {
			_, _ = fmt.Fprintf(h.opts.Log, "retake: %s \"%s %s\" %d\n", ri.client, r.Method, logPath(r.URL.Path),
				sw.status())
		}()
	}
	h.secureHeaders(w)
	if r.URL.Path == "/healthz" {
		h.healthz(w, r)
		return
	}
	if r.URL.Path == "/api/ping" {
		h.json(w, r, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if !h.hostOK(ri) {
		h.fail(w, r, http.StatusForbidden, "bad host")
		return
	}
	// CSRF: a state-changing request without a Bearer token must come from our own origin.
	if stateChanging(r.Method) && bearer(r) == "" && !sameOrigin(r, ri) {
		h.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	parts := splitPath(r.URL.Path)
	switch {
	case len(parts) >= 2 && parts[0] == "p":
		h.projectRoute(w, r, ri, parts[1], strings.TrimPrefix(r.URL.Path, "/p/"+parts[1]))
	case len(parts) == 1 && parts[0] == "p":
		h.notFound(w, r)
	case len(parts) == 0:
		h.home(w, r, ri)
	case parts[0] == "signin":
		h.signinRoute(w, r, ri, parts[1:])
	case parts[0] == "setup":
		h.setupRoute(w, r, ri, parts[1:])
	case parts[0] == "assets" || parts[0] == "theme":
		h.static(w, r, parts)
	case parts[0] == "ext":
		s, err := h.session(w, r)
		if err != nil {
			h.storeFailed(w, r, err)
			return
		}
		if s == nil {
			h.fail(w, r, http.StatusUnauthorized, "sign in required")
			return
		}
		h.homeExt(w, r, s, parts)
	case len(parts) == 1 && parts[0] == "signout":
		h.signout(w, r, ri)
	case parts[0] == "api":
		h.apiRoute(w, r, ri, parts[1:])
	case len(parts) == 1 && parts[0] == "favicon.ico":
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	default:
		h.notFound(w, r)
	}
}

// logPath keeps secrets out of the log: sign-in codes and share tokens.
func logPath(p string) string {
	parts := strings.Split(p, "/")
	for i := 1; i < len(parts); i++ {
		if parts[i-1] == "signin" || parts[i-1] == "s" {
			parts[i] = "<secret>"
		}
	}
	return strings.Join(parts, "/")
}

func (h *Hub) secureHeaders(w http.ResponseWriter) {
	hd := w.Header()
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set(server.APIHeader, server.APIVersion)
	hd.Set("Referrer-Policy", "same-origin")
	hd.Set("X-Frame-Options", "SAMEORIGIN")
}

// splitPath splits a URL path into its non-empty segments.
func splitPath(p string) []string {
	var out []string
	for s := range strings.SplitSeq(p, "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (h *Hub) json(w http.ResponseWriter, r *http.Request, code int, v any) { writeJSON(w, r, code, v) }

// writeJSON sends v as JSON with Cache-Control: no-store.
func writeJSON(w http.ResponseWriter, r *http.Request, code int, v any) {
	body, err := jsonx.Marshal(v)
	if err != nil {
		code, body = http.StatusInternalServerError, []byte(`{"error": "encoding failed"}`)
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/json")
	hd.Set("Content-Length", strconv.Itoa(len(body)))
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body) // JSON with Content-Type application/json and nosniff
	}
}

func (h *Hub) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	h.json(w, r, code, map[string]string{"error": msg})
}

func (h *Hub) notFound(w http.ResponseWriter, r *http.Request) {
	h.fail(w, r, http.StatusNotFound, "not found")
}

func (h *Hub) storeFailed(w http.ResponseWriter, r *http.Request, err error) {
	h.logf("store: %v", err)
	h.fail(w, r, http.StatusInternalServerError, "store failed")
}

// healthz answers 200 while the stores answer, else 503.
func (h *Hub) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	err := h.st.Accounts.Ping(ctx)
	if err == nil && h.st.Blobs != nil {
		if _, serr := h.st.Blobs.Stat(ctx, "healthz"); serr != nil && !errors.Is(serr, blob.ErrNotFound) {
			err = serr
		}
	}
	if err != nil {
		h.json(w, r, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	h.json(w, r, http.StatusOK, map[string]bool{"ok": true})
}

// baseURL is the origin links are made for: the public URL, else this request's own.
func (h *Hub) baseURL(ri reqInfo) string { return ri.origin }
