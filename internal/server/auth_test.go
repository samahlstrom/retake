package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// tokenAt stores a token in the project at root, with the store's clock at now, and returns it.
func tokenAt(t *testing.T, root, role, label string, ttl time.Duration, now func() time.Time) (string, project.Token) {
	t.Helper()
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	st.Now = now
	tok, hash, err := team.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.CreateToken(project.NewToken{Hash: hash, Role: role, Label: label, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	return tok, row
}

// makeToken stores a token in the harness's project.
func makeToken(t *testing.T, h *harness, role, label string, ttl time.Duration) string {
	t.Helper()
	tok, _ := tokenAt(t, h.srv.root, role, label, ttl, time.Now)
	return tok
}

// startTeam starts a server with share.test allowed (--allow-host) and closes its store at the end.
func startTeam(t *testing.T, opts Options) *harness {
	t.Helper()
	if opts.Video == "" {
		opts.Video = newFixture(t).video
	}
	opts.AllowHosts = append(opts.AllowHosts, "share.test")
	h := start(t, opts)
	t.Cleanup(func() { _ = h.srv.Close() })
	return h
}

func (h *harness) port() int { return h.ts.Listener.Addr().(*net.TCPAddr).Port }

// req sends one request with a Host header and other headers, and returns the response.
func (h *harness) req(method, path, host string, hdr map[string]string) resp {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, strings.NewReader(""))
	if err != nil {
		h.t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var b []byte
	if method != http.MethodHead {
		b = make([]byte, 0, 512)
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			b = append(b, buf[:n]...)
			if err != nil {
				break
			}
		}
	}
	return resp{res.StatusCode, res.Header, b}
}

// Every route checks the role (spec/team.md): routes x roles x how the token comes x local or shared host.
func TestRoleChecksOnEveryRoute(t *testing.T) {
	h := startTeam(t, Options{})
	tokens := map[team.Role]string{}
	for _, r := range team.TokenRoles {
		tokens[r] = makeToken(t, h, string(r), "", time.Hour)
	}
	st, err := project.Open(h.srv.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddHost("abc.trycloudflare.com", "cloudflare", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	routes := []struct {
		method, path string
		perm         team.Perm
	}{
		{"GET", "/", team.PermWatch},
		{"HEAD", "/index.html", team.PermWatch},
		{"GET", "/assets/app.js", team.PermWatch},
		{"GET", "/favicon.ico", team.PermWatch},
		{"GET", "/video", team.PermWatch},
		{"HEAD", "/video/v5", team.PermWatch},
		{"GET", "/video/v6/x.m4s", team.PermWatch},
		{"GET", "/api/ping", team.PermAny},
		{"GET", "/api/info", team.PermWatch},
		{"GET", "/api/history?v=v5", team.PermWatch},
		{"GET", "/api/notes", team.PermWatch},
		{"HEAD", "/api/events", team.PermWatch},
		{"GET", "/api/takes/" + take + "/img_01.png", team.PermWatch},
		{"GET", "/api/nope", team.PermWatch},
		{"PUT", "/api/ping", team.PermWatch},
		{"POST", "/api/session/nope/data", team.PermRecord},
		{"POST", "/api/session/" + take + "/audio?name=bad", team.PermRecord},
		{"POST", "/api/session/nope/image", team.PermRecord},
		{"POST", "/api/session/nope/done", team.PermRecord},
		{"POST", "/api/nope", team.PermRecord},
		{"POST", "/api/notes/99/reopen", team.PermApprove},
		{"POST", "/api/versions/v99/approve", team.PermApprove},
		{"POST", "/api/automation/tasks", team.PermRecord},
		{"POST", "/api/automation/tasks/nope/answer", team.PermRecord},
		{"POST", "/api/automation/tasks/nope/retry", team.PermRecord},
		{"POST", "/api/automation/tasks/nope/switch", team.PermManage},
		{"POST", "/api/automation/tasks/nope/pause", team.PermManage},
		{"POST", "/api/automation", team.PermManage},
		{"POST", "/mcp", team.PermAgent},
		{"GET", "/mcp", team.PermAgent},
	}
	port := h.port()
	hosts := []struct {
		name, host string
		hdr        map[string]string
		shared     bool
	}{
		{"local", fmt.Sprintf("127.0.0.1:%d", port), nil, false},
		{"localhost", fmt.Sprintf("localhost:%d", port), nil, false},
		{"allow-host", "share.test", nil, true},
		{"share table", "abc.trycloudflare.com", nil, true},
		{"proxied local", fmt.Sprintf("127.0.0.1:%d", port), map[string]string{"X-Forwarded-For": "203.0.113.9"}, true},
	}
	type caller struct {
		role team.Role
		via  string // none, bearer, cookie
	}
	callers := []caller{{team.Owner, "none"}}
	for _, r := range team.TokenRoles {
		callers = append(callers, caller{r, "bearer"}, caller{r, "cookie"})
	}
	// want is what spec/team.md says: 401 without a usable token, 403 for a role without the permission, else the
	// route's own answer.
	want := func(perm team.Perm, c caller, shared bool) string {
		role, via := c.role, c.via
		if via == "cookie" && !shared {
			via, role = "none", team.Owner // cookies are ignored on local requests
		}
		if via == "cookie" && perm == team.PermAgent {
			via = "none" // /mcp takes Bearer only
		}
		if via == "none" && (shared || perm == team.PermAgent) {
			return "401"
		}
		if !role.Can(perm) {
			return "403"
		}
		return "ok"
	}
	n := 0
	for _, ho := range hosts {
		for _, rt := range routes {
			for _, c := range callers {
				hdr := map[string]string{}
				for k, v := range ho.hdr {
					hdr[k] = v
				}
				switch c.via {
				case "bearer":
					hdr["Authorization"] = "Bearer " + tokens[c.role]
				case "cookie":
					hdr["Cookie"] = ShareCookie + "=" + tokens[c.role]
				}
				r := h.req(rt.method, rt.path, ho.host, hdr)
				exp := want(rt.perm, c, ho.shared)
				var got string
				switch {
				case r.code == http.StatusUnauthorized:
					got = "401"
				case r.code == http.StatusForbidden && !strings.Contains(string(r.body), `"bad `): // not bad host/origin
					got = "403"
				case r.code == http.StatusTooManyRequests:
					got = "429"
				default:
					got = "ok"
				}
				if got != exp {
					t.Errorf("%s %s %s as %s by %s: %s (%d %q), want %s", ho.name, rt.method, rt.path, c.role, c.via, got,
						r.code, r.body, exp)
				}
				n++
			}
		}
	}
	if n < 900 {
		t.Errorf("only %d cases", n)
	}
}

func TestRoleInInfo(t *testing.T) {
	h := startTeam(t, Options{})
	for _, r := range []team.Role{team.Viewer, team.Reviewer, team.Approver} {
		tok := makeToken(t, h, string(r), "", 0)
		res := h.req("GET", "/api/info", "share.test", map[string]string{"Cookie": ShareCookie + "=" + tok})
		if res.code != 200 || res.json(t)["role"] != string(r) {
			t.Errorf("%s: %d %v", r, res.code, res.json(t)["role"])
		}
	}
	if res := h.get("/api/info", nil); res.json(t)["role"] != "owner" {
		t.Errorf("local: %v", res.json(t)["role"])
	}
}

// A share link sets an HttpOnly, Secure, SameSite=Strict cookie for the server's root, and bounces to the page.
func TestShareLinkCookie(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	h := startTeam(t, Options{Clock: clock})
	tok, _ := tokenAt(t, h.srv.root, "viewer", "alex", 2*time.Hour, clock.Now)
	r := h.req("GET", "/s/"+tok, "share.test", nil)
	if r.code != 200 || !strings.Contains(string(r.body), "location.replace('/')") {
		t.Fatalf("share link: %d %s", r.code, r.body)
	}
	if r.header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(r.header.Get("Cache-Control"), "no-store") {
		t.Errorf("headers: %v", r.header)
	}
	cookies := (&http.Response{Header: r.header}).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies: %v", r.header.Values("Set-Cookie"))
	}
	c := cookies[0]
	if c.Name != ShareCookie || c.Value != tok || c.Path != "/" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteStrictMode || c.Domain != "" || c.MaxAge < 7190 || c.MaxAge > 7200 {
		t.Errorf("cookie: %q", r.header.Get("Set-Cookie"))
	}
	// The cookie opens the page and the API as a viewer.
	ck := map[string]string{"Cookie": c.Name + "=" + c.Value}
	if p := h.req("GET", "/", "share.test", ck); p.code != 200 {
		t.Errorf("page with the cookie: %d", p.code)
	}
	if i := h.req("GET", "/api/info", "share.test", ck); i.code != 200 || i.json(t)["role"] != "viewer" {
		t.Errorf("info with the cookie: %d %s", i.code, i.body)
	}
	// Without it, the page says it needs a link (HTML), and the API says so in JSON.
	p := h.req("GET", "/", "share.test", nil)
	if p.code != 401 || !strings.HasPrefix(p.header.Get("Content-Type"), "text/html") ||
		!strings.Contains(string(p.body), "needs a share link") || p.header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("page without a token: %d %v %s", p.code, p.header, p.body)
	}
	if a := h.req("GET", "/api/info", "share.test", nil); a.code != 401 || a.json(t)["error"] != "token required" {
		t.Errorf("api without a token: %d %s", a.code, a.body)
	}
	// A bad link: 401 HTML, no cookie.
	bad := h.req("GET", "/s/rt_"+strings.Repeat("A", 43), "share.test", nil)
	if bad.code != 401 || !strings.Contains(string(bad.body), "expired or was revoked") || bad.header.Get("Set-Cookie") != "" {
		t.Errorf("bad link: %d %v %s", bad.code, bad.header, bad.body)
	}
	// A token that never expires keeps its cookie for 400 days (the browsers' cap).
	never, _ := tokenAt(t, h.srv.root, "reviewer", "", 0, clock.Now)
	r = h.req("GET", "/s/"+never, "share.test", nil)
	if c := (&http.Response{Header: r.header}).Cookies(); len(c) != 1 || c[0].MaxAge != 400*24*3600 {
		t.Errorf("never-expiring cookie: %q", r.header.Get("Set-Cookie"))
	}
	// A share link also works on the local host (the cookie has no effect there).
	if r := h.req("GET", "/s/"+tok, "", nil); r.code != 200 {
		t.Errorf("local share link: %d", r.code)
	}
}

func TestExpiryAndRevoke(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	h := startTeam(t, Options{Clock: clock})
	tok, _ := tokenAt(t, h.srv.root, "reviewer", "", time.Hour, clock.Now)
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	cookie := map[string]string{"Cookie": ShareCookie + "=" + tok}
	if r := h.req("GET", "/api/info", "share.test", bearer); r.code != 200 {
		t.Fatalf("fresh token: %d", r.code)
	}
	clock.Add(59 * time.Minute)
	if r := h.req("GET", "/api/info", "share.test", cookie); r.code != 200 {
		t.Fatalf("a minute before expiry: %d", r.code)
	}
	clock.Add(time.Minute)
	r := h.req("GET", "/api/info", "share.test", cookie)
	if r.code != 401 || r.json(t)["error"] != "bad token" {
		t.Errorf("expired: %d %s", r.code, r.body)
	}
	// The stale cookie is cleared.
	if c := (&http.Response{Header: r.header}).Cookies(); len(c) != 1 || c[0].Name != ShareCookie || c[0].MaxAge >= 0 {
		t.Errorf("expired cookie not cleared: %q", r.header.Values("Set-Cookie"))
	}
	if r := h.req("GET", "/s/"+tok, "share.test", nil); r.code != 401 {
		t.Errorf("expired link: %d", r.code)
	}
	// Local requests with an expired Bearer token are refused too (a token must be valid wherever it comes).
	if r := h.req("GET", "/api/info", "", bearer); r.code != 401 {
		t.Errorf("expired bearer, local: %d", r.code)
	}

	tok2, row := tokenAt(t, h.srv.root, "approver", "", 0, clock.Now)
	if r := h.req("GET", "/api/info", "share.test", map[string]string{"Authorization": "Bearer " + tok2}); r.code != 200 {
		t.Fatalf("before revoking: %d", r.code)
	}
	st, err := project.Open(h.srv.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevokeToken(row.ID); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if r := h.req("GET", "/api/info", "share.test", map[string]string{"Authorization": "Bearer " + tok2}); r.code != 401 {
		t.Errorf("revoked: %d", r.code)
	}
}

func TestRateLimitFailedTokens(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	lim := team.NewLimiter()
	lim.Max = 3
	h := startTeam(t, Options{Clock: clock, Limiter: lim})
	good, _ := tokenAt(t, h.srv.root, "viewer", "", 0, clock.Now)
	bad := "rt_" + strings.Repeat("B", 43)
	from := func(ip, tok string) resp {
		hdr := map[string]string{"Cf-Connecting-Ip": ip}
		if tok != "" {
			hdr["Authorization"] = "Bearer " + tok
		}
		return h.req("GET", "/api/info", "share.test", hdr)
	}
	// Requests without a token aren't attempts.
	for range 5 {
		if r := from("198.51.100.1", ""); r.code != 401 {
			t.Fatalf("no token: %d", r.code)
		}
	}
	for i, tok := range []string{bad, "garbage", "rt_short"} {
		if r := from("198.51.100.1", tok); r.code != 401 {
			t.Fatalf("bad token %d: %d", i, r.code)
		}
	}
	// Blocked now, even with a good token, and before the token is looked up; a link counts the same.
	r := from("198.51.100.1", good)
	if r.code != 429 || r.json(t)["error"] != "too many attempts" || r.header.Get("Retry-After") != "61" {
		t.Errorf("blocked: %d %s %q", r.code, r.body, r.header.Get("Retry-After"))
	}
	if r := h.req("GET", "/s/"+good, "share.test", map[string]string{"Cf-Connecting-Ip": "198.51.100.1"}); r.code != 429 {
		t.Errorf("blocked link: %d", r.code)
	}
	// Another client is not.
	if r := from("198.51.100.2", good); r.code != 200 {
		t.Errorf("other client: %d", r.code)
	}
	// X-Forwarded-For: the last entry is the client (the one our proxy added).
	for range 3 {
		h.req("GET", "/api/info", "share.test", map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.7",
			"Authorization": "Bearer " + bad})
	}
	if r := from("203.0.113.7", good); r.code != 429 {
		t.Errorf("X-Forwarded-For client not blocked: %d", r.code)
	}
	if r := from("198.51.100.3", good); r.code != 200 {
		t.Errorf("spoofed first X-Forwarded-For entry was blocked: %d", r.code)
	}
	// A minute later the client may try again.
	clock.Add(61 * time.Second)
	if r := from("198.51.100.1", good); r.code != 200 {
		t.Errorf("after the window: %d", r.code)
	}
}

// A local Host that carries a proxy header came through a proxy that rewrote Host (tailscale serve has): it is shared.
func TestProxyHeadersMakeLocalShared(t *testing.T) {
	h := startTeam(t, Options{})
	tok := makeToken(t, h, "viewer", "", 0)
	for _, hd := range proxyHeaders {
		if r := h.req("GET", "/api/info", "", map[string]string{hd: "x"}); r.code != 401 {
			t.Errorf("%s without a token: %d", hd, r.code)
		}
		r := h.req("GET", "/api/info", "", map[string]string{hd: "x", "Cookie": ShareCookie + "=" + tok})
		if r.code != 200 || r.json(t)["role"] != "viewer" {
			t.Errorf("%s with the cookie: %d %s", hd, r.code, r.body)
		}
	}
	if r := h.get("/api/info", nil); r.code != 200 || r.json(t)["role"] != "owner" {
		t.Errorf("plain local: %d", r.code)
	}
}

// The hosts table: a running share's hostname is answered and is an origin for writes, until the share stops.
func TestShareHostsFromTheStore(t *testing.T) {
	h := startTeam(t, Options{})
	tok := makeToken(t, h, "reviewer", "", 0)
	auth := map[string]string{"Authorization": "Bearer " + tok}
	if r := h.req("GET", "/api/info", "xyz.trycloudflare.com", auth); r.code != 403 || r.json(t)["error"] != "bad host" {
		t.Fatalf("before the share: %d %s", r.code, r.body)
	}
	st, err := project.Open(h.srv.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.AddHost("XYZ.trycloudflare.com", "cloudflare", 1); err != nil {
		t.Fatal(err)
	}
	if r := h.req("GET", "/api/info", "xyz.trycloudflare.com", auth); r.code != 200 {
		t.Errorf("during the share: %d", r.code)
	}
	post := func(origin string) resp {
		hdr := map[string]string{"Authorization": "Bearer " + tok, "Origin": origin}
		return h.req("POST", "/api/session/nope/data", "xyz.trycloudflare.com", hdr)
	}
	if r := post("https://xyz.trycloudflare.com"); r.code != 404 {
		t.Errorf("share origin: %d %s", r.code, r.body)
	}
	for _, o := range []string{"http://xyz.trycloudflare.com", "https://evil.example", "https://", "null"} {
		if r := post(o); r.code != 403 || r.json(t)["error"] != "bad origin" {
			t.Errorf("origin %q: %d %s", o, r.code, r.body)
		}
	}
	if err := st.RemoveHost("xyz.trycloudflare.com", 0); err != nil {
		t.Fatal(err)
	}
	if r := h.req("GET", "/api/info", "xyz.trycloudflare.com", auth); r.code != 403 {
		t.Errorf("after the share: %d", r.code)
	}
}

// A shared response is private, so a CDN in front of the tunnel never caches it; local ones are as before.
func TestSharedResponsesArePrivate(t *testing.T) {
	h := startTeam(t, Options{})
	tok := makeToken(t, h, "viewer", "", 0)
	auth := map[string]string{"Authorization": "Bearer " + tok}
	for path, cc := range map[string]string{"/api/info": "private, no-store", "/video": "private, no-cache",
		"/favicon.ico": "private, no-store", "/nope": "private, no-store"} {
		if r := h.req("GET", path, "share.test", auth); r.header.Get("Cache-Control") != cc {
			t.Errorf("shared %s: %q, want %q", path, r.header.Get("Cache-Control"), cc)
		}
	}
	if r := h.req("GET", "/api/info", "share.test", nil); r.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("shared refusal: %q", r.header.Get("Cache-Control"))
	}
	if r := h.get("/video", nil); r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("local video: %q", r.header.Get("Cache-Control"))
	}
}

// A reviewer's takes carry the token's label as their author; without a label, the server's name.
func TestAuthorFromLabel(t *testing.T) {
	f := newFixture(t)
	h := startTeam(t, Options{Video: f.video, Author: "Sam"})
	body := `{"session":{"format":3},"segments":[],"events":[],"strokes":[],"voice":{}}`
	for i, c := range []struct{ label, want string }{{"alex", "alex"}, {"", "Sam"}} {
		tok := makeToken(t, h, "reviewer", c.label, 0)
		id := fmt.Sprintf("2026-10-30_10-00-0%d", i)
		req, err := http.NewRequest(http.MethodPost, h.url+"/api/session/"+id+"/data", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "share.test"
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Origin", "https://share.test")
		res, err := h.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("save: %d", res.StatusCode)
		}
		b, err := os.ReadFile(filepath.Join(f.root, "v6", "clip_v6.review", id, "session.json"))
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Author struct{ Name string } `json:"author"`
		}
		if err := json.Unmarshal(b, &s); err != nil || s.Author.Name != c.want {
			t.Errorf("label %q: author %q (%v)", c.label, s.Author.Name, err)
		}
	}
}

// Refused requests don't count as contact for the idle timer.
func TestRefusedRequestsAreNotContact(t *testing.T) {
	h := startTeam(t, Options{})
	h.req("GET", "/api/info", "share.test", nil)
	h.req("GET", "/api/info", "share.test", map[string]string{"Authorization": "Bearer rt_x"})
	h.req("GET", "/mcp", "", nil)
	h.srv.contactMu.Lock()
	zero := h.srv.contact.IsZero()
	h.srv.contactMu.Unlock()
	if !zero {
		t.Error("a refused request counted as contact")
	}
	h.get("/api/ping", nil)
	h.srv.contactMu.Lock()
	zero = h.srv.contact.IsZero()
	h.srv.contactMu.Unlock()
	if zero {
		t.Error("an allowed request didn't count")
	}
}

// headerTransport adds headers (and a Host) to every request of an HTTP client: an MCP client's configured token.
type headerTransport struct {
	host string
	hdr  map[string]string
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.host != "" {
		r.Host = t.host
	}
	for k, v := range t.hdr {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func mcpClient(t *testing.T, h *harness, host, tok string) (*sdk.ClientSession, error) {
	t.Helper()
	hdr := map[string]string{}
	if tok != "" {
		hdr["Authorization"] = "Bearer " + tok
	}
	tr := &sdk.StreamableClientTransport{Endpoint: h.url + "/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: headerTransport{host, hdr}, Timeout: 10 * time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "remote-agent", Version: "1"}, nil).Connect(ctx, tr, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

// Remote MCP: a real SDK client over HTTP, refused without an agent token, served with one, on the local host and
// through a share's host.
func TestRemoteMCP(t *testing.T) {
	f := newFixture(t)
	h := startTeam(t, Options{Video: f.video, Version: "test"})
	agentTok := makeToken(t, h, "agent", "ci", 0)
	viewerTok := makeToken(t, h, "viewer", "", 0)
	for _, c := range []struct{ name, host, tok string }{
		{"no token, local", "", ""}, {"no token, shared", "share.test", ""},
		{"viewer token", "share.test", viewerTok}, {"bad token", "", "rt_" + strings.Repeat("C", 43)},
	} {
		if _, err := mcpClient(t, h, c.host, c.tok); err == nil {
			t.Errorf("%s: connected", c.name)
		}
	}
	// The share cookie never counts on /mcp.
	cookieOnly := &sdk.StreamableClientTransport{Endpoint: h.url + "/mcp", MaxRetries: -1, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Transport: headerTransport{"share.test",
			map[string]string{"Cookie": ShareCookie + "=" + agentTok}}}}
	if _, err := sdk.NewClient(&sdk.Implementation{Name: "x", Version: "1"}, nil).Connect(context.Background(),
		cookieOnly, nil); err == nil {
		t.Error("connected with the cookie only")
	}

	for _, host := range []string{"", "share.test"} {
		cs, err := mcpClient(t, h, host, agentTok)
		if err != nil {
			t.Fatalf("agent token, host %q: %v", host, err)
		}
		ctx := context.Background()
		tools, err := cs.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range tools.Tools {
			names = append(names, tl.Name)
		}
		// The asset and library tools come too, but not plug-in tools, plugin_query or lib_add: their arguments can
		// name any file, or write into the project from anywhere.
		if strings.Join(names, ",") != "asset_get,asset_import,asset_preview,asset_search,component_describe,"+
			"component_list,lib_search,license_report,lint,note_ask,note_set,notes_list,notes_wait,span_add,span_find,"+
			"version_push,versions_list" {
			t.Errorf("tools: %v", names)
		}
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "versions_list", Arguments: map[string]any{}})
		if err != nil || res.IsError {
			t.Fatalf("versions_list: %v %+v", err, res)
		}
		b, _ := json.Marshal(res.StructuredContent)
		if !strings.Contains(string(b), `"label":"v6"`) {
			t.Errorf("versions_list: %s", b)
		}
		// A file outside the project folder can't be pushed over HTTP.
		outside := filepath.Join(t.TempDir(), "clip_v9.mp4")
		if err := os.WriteFile(outside, []byte("nine"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err = cs.CallTool(ctx, &sdk.CallToolParams{Name: "version_push",
			Arguments: map[string]any{"file": outside}})
		if err != nil || !res.IsError || !strings.Contains(fmt.Sprint(res.Content[0]), "inside the project folder") {
			t.Errorf("push from outside: %v %+v", err, res)
		}
		if _, err := os.Stat(outside); err != nil {
			t.Errorf("the outside file was touched: %v", err)
		}
	}
	// The Origin check applies to /mcp too.
	r := h.req("POST", "/mcp", "", map[string]string{"Authorization": "Bearer " + agentTok, "Origin": "https://evil.example"})
	if r.code != 403 || !strings.Contains(string(r.body), "bad origin") {
		t.Errorf("evil origin: %d %s", r.code, r.body)
	}
}

func TestPermFor(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         team.Perm
	}{
		{"GET", "/", team.PermWatch}, {"GET", "/api/ping", team.PermAny}, {"HEAD", "/api/ping", team.PermAny},
		{"POST", "/api/ping", team.PermRecord}, {"POST", "/api/notes/3/reopen", team.PermApprove},
		{"POST", "/api/versions/v7/approve", team.PermApprove}, {"POST", "/api/notes/3/other", team.PermRecord},
		{"DELETE", "/mcp", team.PermAgent}, {"GET", "/mcp/", team.PermAgent}, {"DELETE", "/video", team.PermWatch},
	} {
		if got := permFor(c.method, splitPath(c.path)); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.method, c.path, got, c.want)
		}
	}
}
