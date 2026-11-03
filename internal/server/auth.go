package server

import (
	"context"
	"errors"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/mcp"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// Team mode (spec/team.md): which hosts are local and which are shared, where a token comes from, what each route
// needs, the share link, and the /mcp route.

// ShareCookie is the cookie a share link sets.
const ShareCookie = "retake_share"

// maxCookieAge is the longest a browser keeps a cookie (400 days, as Chrome caps it).
const maxCookieAge = 400 * 24 * time.Hour

// proxyHeaders mark a request that came through a proxy. A proxy may rewrite Host to the backend's address
// (tailscale serve has), and such a request must not pass as local.
var proxyHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "Forwarded", "X-Real-Ip",
	"Cf-Connecting-Ip", "Cf-Ray", "Tailscale-User-Login"}

// access is who a request acts as, once its token (or its absence) is checked.
type access struct {
	role   team.Role
	shared bool
	token  *project.Token // nil for the owner
	author string         // granted: the name the request's takes carry ("" = the Server's own)
}

type accessKey struct{}

// Grant returns r marked as authorized by the caller of a Server with Options.External: it acts as role, and the
// takes it saves carry author as session.json's author.name ("" = the Server's Author).
func Grant(r *http.Request, role team.Role, author string) *http.Request {
	return withAccess(r, access{role: role, shared: true, author: author})
}

// PermFor returns what a request needs on a project's routes (spec/team.md's route table): path is the path inside
// the project, such as /api/info or /mcp.
func PermFor(method, path string) team.Perm { return permFor(method, splitPath(path)) }

// accessOf returns a request's access; a request that never went through ServeHTTP is the owner's.
func accessOf(r *http.Request) access {
	if a, ok := r.Context().Value(accessKey{}).(access); ok {
		return a
	}
	return access{role: team.Owner}
}

// forwarded reports whether a request carries a proxy header.
func forwarded(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if _, ok := r.Header[h]; ok {
			return true
		}
	}
	return false
}

// shareHost reports whether host belongs to a share: an --allow-host name, or one a running share put in the
// project's hosts table.
func (s *Server) shareHost(host string) bool {
	if s.allow[host] {
		return true
	}
	st, err := s.store(false)
	if err != nil {
		return false
	}
	ok, err := st.HostAllowed(host)
	return err == nil && ok
}

// classify says whether a request is local or shared; ok is false for a Host we don't answer to.
func (s *Server) classify(r *http.Request) (shared, ok bool) {
	host := strings.ToLower(r.Host)
	if s.local[host] {
		return forwarded(r), true
	}
	if s.shareHost(host) {
		return true, true
	}
	return false, false
}

// permFor is what a route needs (spec/team.md's route table).
func permFor(method string, parts []string) team.Perm {
	if isExtRoute(parts) {
		return extPermFor(method, parts)
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "worker" {
		return team.PermAgent
	}
	if len(parts) >= 2 && parts[0] == "api" && parts[1] == "shares" {
		return team.PermApprove // the owner's and approvers' links (spec/team.md, "Sharing a video from the page")
	}
	if method == http.MethodPost && len(parts) >= 2 && parts[0] == "api" && parts[1] == "automation" {
		// Anyone who records sends, answers and retries (spec/automation.md); the rest is the owner's.
		if is(parts, "api", "automation", "tasks") || len(parts) == 5 && (parts[4] == "answer" || parts[4] == "retry") {
			return team.PermRecord
		}
		return team.PermManage
	}
	if len(parts) == 1 && parts[0] == "mcp" {
		return team.PermAgent
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		if is(parts, "api", "ping") {
			return team.PermAny
		}
		return team.PermWatch
	case http.MethodPost:
		if len(parts) == 4 && parts[0] == "api" && parts[1] == "versions" && (parts[3] == "watch" || parts[3] == "unwatch") {
			return team.PermWatch // a viewer's page watches a streamed version (spec/bridge.md, "Streaming")
		}
		if is(parts, "api", "download") {
			return team.PermWatch // getting a download ready; [server] downloads narrows it (spec/api.md, "Download")
		}
		if len(parts) == 4 && parts[0] == "api" &&
			(parts[1] == "notes" && (parts[3] == "reopen" || parts[3] == "delete" || parts[3] == "restore") || parts[1] == "versions" && parts[3] == "approve") {
			return team.PermApprove
		}
		return team.PermRecord
	}
	return team.PermWatch // anything else is a 404 once allowed
}

// presented returns the token a request carries: a Bearer token on any request, else the share cookie on a shared
// one. bearer says which. Other Authorization schemes are ignored.
func presented(r *http.Request, shared bool) (tok string, bearer bool) {
	scheme, v, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(v), true
	}
	if shared {
		if c, err := r.Cookie(ShareCookie); err == nil && c.Value != "" {
			return c.Value, false
		}
	}
	return "", false
}

// clientKey is who failed attempts are counted against: the address the tunnel saw, else the remote address.
func clientKey(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); ip != "" {
		return ip
	}
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(xff[len(xff)-1], ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip // the last entry: the one our own proxy added
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

var errBadToken = errors.New("bad token")

// lookup finds a valid token: errBadToken for a malformed, unknown, expired or revoked one.
func (s *Server) lookup(tok string) (project.Token, error) {
	if !team.WellFormed(tok) {
		return project.Token{}, errBadToken
	}
	st, err := s.store(false)
	if errors.Is(err, project.ErrNoProject) {
		return project.Token{}, errBadToken
	}
	if err != nil {
		return project.Token{}, err
	}
	t, err := st.TokenByHash(team.Hash(tok))
	if errors.Is(err, project.ErrNotFound) || err == nil && !t.Valid(s.opts.Clock.Now()) {
		return project.Token{}, errBadToken
	}
	return t, err
}

// check looks a presented token up under the rate limit. It answers the request itself when that fails, and then
// returns ok false.
func (s *Server) check(w http.ResponseWriter, r *http.Request, tok string, fromCookie, page bool) (project.Token, bool) {
	key, now := clientKey(r), s.opts.Clock.Now()
	if blocked, wait := s.limiter.Blocked(key, now); blocked {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		s.refuse(w, r, http.StatusTooManyRequests, "too many attempts", page)
		return project.Token{}, false
	}
	t, err := s.lookup(tok)
	if errors.Is(err, errBadToken) {
		s.limiter.Fail(key, now)
		if fromCookie {
			http.SetCookie(w, &http.Cookie{Name: ShareCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
				Secure: true, SameSite: http.SameSiteStrictMode})
		}
		w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
		s.refuse(w, r, http.StatusUnauthorized, "bad token", page)
		return project.Token{}, false
	}
	if err != nil {
		s.storeFailed(w, r, err)
		return project.Token{}, false
	}
	return t, true
}

// authorize decides who a request acts as and whether its route allows that. It answers refused requests itself.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, parts []string, shared bool) (access, bool) {
	perm := permFor(r.Method, parts)
	page := (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		(len(parts) == 0 || is(parts, "index.html"))
	a := access{shared: shared}
	tok, bearer := presented(r, shared)
	if perm == team.PermAgent && !bearer {
		tok = "" // /mcp takes a Bearer token only, never the share cookie
	}
	if tok == "" {
		if shared || perm == team.PermAgent {
			w.Header().Set("WWW-Authenticate", "Bearer")
			s.refuse(w, r, http.StatusUnauthorized, "token required", page)
			return a, false
		}
		a.role = team.Owner
	} else {
		t, ok := s.check(w, r, tok, !bearer, page)
		if !ok {
			return a, false
		}
		a.role, a.token = team.Role(t.Role), &t
	}
	if !a.role.Can(perm) {
		s.refuse(w, r, http.StatusForbidden, "not allowed", page)
		return a, false
	}
	return a, true
}

// refuse answers a refused request: JSON, or a short HTML page for the page route.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, code int, msg string, page bool) {
	if !page {
		s.fail(w, r, code, msg)
		return
	}
	text := map[string]string{ //nolint:gosec // messages about tokens, not credentials
		"token required":    "This review needs a share link. Ask whoever sent you here for one.",
		"bad token":         "This link has expired or was revoked. Ask whoever sent it for a new one.",
		"too many attempts": "Too many tries with a bad link. Wait a minute, then open your link again.",
		"not allowed":       "This link can't open the review page.",
	}[msg]
	s.htmlPage(w, r, code, text)
}

// htmlPage sends a one-line page in the review page's colors.
func (s *Server) htmlPage(w http.ResponseWriter, r *http.Request, code int, text string) {
	body := `<!doctype html><html lang="en"><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1"><title>Retake</title>` +
		`<style>body{margin:0;min-height:100vh;display:grid;place-items:center;padding:0 16px;background:#0b0b0c;` +
		`color:#ddd;font:16px/1.5 system-ui,sans-serif;text-align:center}</style><p>` + html.EscapeString(text) + `</p>`
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

// bouncePage takes the browser from /s/<token> to /. A same-origin navigation is same-site, so the SameSite=Strict
// cookie goes along even when the link was opened from another site (a redirect from there would not carry it).
const bouncePage = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="referrer" content="no-referrer">` +
	`<title>Retake</title><script>location.replace('/')</script><meta http-equiv="refresh" content="0;url=/">` +
	`<a href="/">Open the review</a>`

// shareLink is GET /s/<token>: it checks the token, sets the share cookie, and bounces to the page.
func (s *Server) shareLink(w http.ResponseWriter, r *http.Request, tok string) {
	t, ok := s.check(w, r, tok, false, true)
	if !ok {
		return
	}
	s.touch()
	age := maxCookieAge
	if exp, ok := t.ExpiresAt(); ok {
		age = min(age, exp.Sub(s.opts.Clock.Now()))
	}
	http.SetCookie(w, &http.Cookie{Name: ShareCookie, Value: tok, Path: "/", MaxAge: max(1, int(age.Seconds())),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	h := w.Header()
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(bouncePage)))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(bouncePage))
	}
}

// mcpRoute is /mcp: the agent tools over streamable HTTP, for an agent token (checked already).
func (s *Server) mcpRoute(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		s.fail(w, r, http.StatusForbidden, "bad origin")
		return
	}
	h, err := s.mcpHandler()
	if err != nil {
		s.storeFailed(w, r, err)
		return
	}
	h.ServeHTTP(w, r)
}

// mcpHandler builds the MCP handler once, on the project store (created if needed: the agent may write).
func (s *Server) mcpHandler() (http.Handler, error) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpH != nil {
		return s.mcpH, nil
	}
	st, err := s.store(true)
	if err != nil {
		return nil, err
	}
	p := &agent.Project{Video: s.video(), Root: s.root, Store: st, Prober: s.opts.Prober, Editor: s.opts.Editor}
	s.mcpH = mcp.HTTPHandlerWith(p, s.opts.Version, mcp.Options{Root: s.root, MaxWait: mcp.RemoteWait,
		Files: s.opts.MCPFiles})
	return s.mcpH, nil
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

// withAccess returns r carrying its access, for the handlers that need the role (info) or the label (author).
func withAccess(r *http.Request, a access) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), accessKey{}, a))
}
