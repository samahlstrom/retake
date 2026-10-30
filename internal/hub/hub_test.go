package hub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// fakeProber gives every existing file a 10 s duration: test versions are bytes, not video.
type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d, w, h := 10.0, 64, 36
	return probe.Info{Duration: &d, FPS: 30, Width: &w, Height: &h}
}

// clock is a settable, concurrency-safe time.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

const pageHTML = "<!doctype html><title>review page</title>"

// th is a running hub on SQLite and local files, with its public URL set to the test server's own URL.
type th struct {
	t     *testing.T
	h     *Hub
	st    *Stores
	ts    *httptest.Server
	url   string
	clock *clock
}

func newTH(t *testing.T, cfg Config, mod func(*Options)) *th {
	t.Helper()
	if cfg.Data == "" {
		cfg.Data = t.TempDir()
	}
	st, err := OpenStores(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	x := &th{t: t, st: st, clock: &clock{t: time.Now().UTC()}}
	st.Accounts.Now = x.clock.now
	ts := httptest.NewUnstartedServer(nil)
	x.url = "http://" + ts.Listener.Addr().String()
	o := Options{Stores: st, PublicURL: x.url, UI: fstest.MapFS{"index.html": {Data: []byte(pageHTML)}},
		Prober: fakeProber{}, Version: "test", Now: x.clock.now, EventPoll: 20 * time.Millisecond,
		SyncEvery: time.Hour}
	if mod != nil {
		mod(&o)
	}
	if x.h, err = New(o); err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = x.h
	ts.Start()
	x.ts = ts
	t.Cleanup(func() {
		ts.Close()
		_ = x.h.Close()
		_ = st.Close()
	})
	return x
}

type reqOpt func(*http.Request)

func cookie(name, value string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: name, Value: value}) }
}
func origin(o string) reqOpt { return func(r *http.Request) { r.Header.Set("Origin", o) } }
func bearerTok(t string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+t) }
}
func header(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func host(h string) reqOpt      { return func(r *http.Request) { r.Host = h } }

type res struct {
	code int
	hdr  http.Header
	body string
}

func (r res) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON (%d): %q", r.code, r.body)
	}
	return m
}

// do sends one request without following redirects.
func (x *th) do(method, path, body string, opts ...reqOpt) res {
	x.t.Helper()
	req, err := http.NewRequest(method, x.url+path, strings.NewReader(body))
	if err != nil {
		x.t.Fatal(err)
	}
	for _, o := range opts {
		o(req)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout: 10 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		x.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return res{resp.StatusCode, resp.Header, string(b)}
}

// post sends a same-origin POST, as the page does.
func (x *th) post(path, body string, opts ...reqOpt) res {
	return x.do(http.MethodPost, path, body, append([]reqOpt{origin(x.url)}, opts...)...)
}

func (x *th) user(email string, admin bool) accounts.User {
	x.t.Helper()
	u, err := x.st.Accounts.CreateUser(email, strings.Split(email, "@")[0], admin)
	if err != nil {
		x.t.Fatal(err)
	}
	return u
}

// session starts a session for u and returns its cookie's value.
func (x *th) session(u accounts.User) string {
	x.t.Helper()
	v, _, err := x.st.Accounts.StartSession(u.ID, "test", "127.0.0.1")
	if err != nil {
		x.t.Fatal(err)
	}
	return v
}

// project adds a project, with a first version (bytes, not video) unless empty.
func (x *th) project(name string, empty bool) accounts.Project {
	x.t.Helper()
	p, err := x.st.CreateProject(context.Background(), name, "")
	if err != nil {
		x.t.Fatal(err)
	}
	if empty {
		return p
	}
	src := filepath.Join(x.t.TempDir(), "first.mp4")
	if err := os.WriteFile(src, make([]byte, 4096), 0o600); err != nil {
		x.t.Fatal(err)
	}
	ps, err := x.st.ProjectStore(context.Background(), p)
	if err != nil {
		x.t.Fatal(err)
	}
	defer func() { _ = ps.Close() }()
	if _, err := FirstVersion(ps, x.st.Workspace(name), name, src, "", "admin", "", false); err != nil {
		x.t.Fatal(err)
	}
	return p
}

func (x *th) member(p accounts.Project, u accounts.User, r team.Role) {
	x.t.Helper()
	if err := x.st.Accounts.SetMember(p.ID, u.ID, r); err != nil {
		x.t.Fatal(err)
	}
}

// token makes a project token.
func (x *th) token(name string, role team.Role, label string) string {
	x.t.Helper()
	p, err := x.h.project(name)
	if err != nil {
		x.t.Fatal(err)
	}
	res, err := MakeToken(p.store, x.url, name, role, label, time.Hour)
	if err != nil {
		x.t.Fatal(err)
	}
	return res.Value
}

func setCookie(t *testing.T, r res, name string) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: r.hdr}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestSignInLink(t *testing.T) {
	x := newTH(t, Config{}, nil)
	u := x.user("ana@example.com", false)
	code, _, err := x.st.Accounts.CreateLink(u.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	page := x.do(http.MethodGet, "/signin/"+code, "")
	if page.code != 200 || !strings.Contains(page.body, "Sign in as ana@example.com") ||
		!strings.Contains(page.body, `method="post"`) || page.hdr.Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("link page: %d %s", page.code, page.body)
	}
	// Opening the link (a mail scanner) doesn't use it; another site can't post it.
	if r := x.do(http.MethodPost, "/signin/"+code, ""); r.code != 403 {
		t.Fatalf("post without Origin: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/signin/"+code, "", origin("https://evil.example")); r.code != 403 {
		t.Fatalf("cross-site post: %d", r.code)
	}
	r := x.post("/signin/"+code, "")
	if r.code != 303 || r.hdr.Get("Location") != "/" {
		t.Fatalf("sign in: %d %v", r.code, r.hdr)
	}
	c := setCookie(t, r, SessionCookie)
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge <= 0 {
		t.Fatalf("session cookie: %+v", c)
	}
	if home := x.do(http.MethodGet, "/", "", cookie(SessionCookie, c.Value)); home.code != 200 ||
		!strings.Contains(home.body, "ana@example.com") {
		t.Fatalf("home: %d %s", home.code, home.body)
	}
	if again := x.post("/signin/"+code, ""); again.code != 401 || !strings.Contains(again.body, "expired or was used") {
		t.Fatalf("second use: %d", again.code)
	}
	// Signing in again replaces the session the browser had.
	code2, _, _ := x.st.Accounts.CreateLink(u.ID, 0)
	r2 := x.post("/signin/"+code2, "", cookie(SessionCookie, c.Value))
	c2 := setCookie(t, r2, SessionCookie)
	if c2 == nil || c2.Value == c.Value {
		t.Fatal("a new sign-in reuses the old session")
	}
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, c.Value)); r.code != 401 {
		t.Fatalf("the replaced session still works: %d", r.code)
	}
	// Bad codes are rate limited per client: the second use above was one failure, nine more make ten.
	for i := range 9 {
		if r := x.do(http.MethodGet, fmt.Sprintf("/signin/rs_%043d", i), ""); r.code != 401 {
			t.Fatalf("bad code %d: %d", i, r.code)
		}
	}
	if r := x.do(http.MethodGet, "/signin/"+code2, ""); r.code != 429 || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("after 10 failures: %d", r.code)
	}
}

func TestSessionsRotateAndEnd(t *testing.T) {
	x := newTH(t, Config{}, nil)
	u := x.user("ana@example.com", false)
	v := x.session(u)
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, v)); r.code != 200 || r.json(t)["email"] !=
		"ana@example.com" {
		t.Fatalf("me: %d %s", r.code, r.body)
	}
	x.clock.add(accounts.RotateEvery)
	r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, v))
	nc := setCookie(t, r, SessionCookie)
	if r.code != 200 || nc == nil || nc.Value == v || !nc.HttpOnly || !nc.Secure {
		t.Fatalf("rotation: %d %+v", r.code, nc)
	}
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, v)); r.code != 200 {
		t.Fatalf("old value within the grace: %d", r.code)
	}
	x.clock.add(accounts.RotateGrace)
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, v)); r.code != 401 {
		t.Fatalf("old value after the grace: %d", r.code)
	}
	// Sign out ends it and clears the cookie.
	out := x.post("/signout", "", cookie(SessionCookie, nc.Value))
	if out.code != 303 || out.hdr.Get("Location") != "/signin" {
		t.Fatalf("sign out: %d", out.code)
	}
	if c := setCookie(t, out, SessionCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie not cleared: %+v", c)
	}
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, nc.Value)); r.code != 401 {
		t.Fatalf("after sign out: %d", r.code)
	}
	// Revoking every session of a user (retake user signout), and disabling them.
	a, b := x.session(u), x.session(u)
	if n, _ := x.st.Accounts.EndSessions(u.ID); n < 2 {
		t.Fatalf("ended %d", n)
	}
	for _, s := range []string{a, b} {
		if r := x.do(http.MethodGet, "/", "", cookie(SessionCookie, s)); r.code != 303 {
			t.Fatalf("revoked session on /: %d", r.code)
		}
	}
	c := x.session(u)
	_, _ = x.st.Accounts.SetDisabled(u.Email, true)
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, c)); r.code != 401 {
		t.Fatalf("disabled user: %d", r.code)
	}
}

func TestCSRF(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin := x.user("boss@example.com", true)
	s := x.session(admin)
	x.project("demo", false)
	agentTok := x.token("demo", team.Agent, "")
	body := `{"name":"made"}`
	cases := []struct {
		name string
		opts []reqOpt
		want int
	}{
		{"no origin, no fetch metadata", nil, 403},
		{"another site", []reqOpt{origin("https://evil.example")}, 403},
		{"null origin", []reqOpt{origin("null")}, 403},
		{"same host, other scheme", []reqOpt{origin(strings.Replace(x.url, "http://", "https://", 1))}, 403},
		{"cross-site fetch metadata", []reqOpt{header("Sec-Fetch-Site", "cross-site")}, 403},
		{"same-site isn't same-origin", []reqOpt{header("Sec-Fetch-Site", "same-site")}, 403},
		{"our origin", []reqOpt{origin(x.url)}, 201},
		{"same-origin fetch metadata", []reqOpt{header("Sec-Fetch-Site", "same-origin")}, 409},
	}
	for _, c := range cases {
		r := x.do(http.MethodPost, "/api/projects", body, append(c.opts, cookie(SessionCookie, s))...)
		if r.code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, r.code, r.body, c.want)
		}
	}
	// Every state-changing method, every kind of route.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, p := range []string{"/signout", "/api/projects/demo/members/x@y.z", "/p/demo/api/session/2026-10-30_10-00-00/data",
			"/p/demo/api/versions/v1/approve", "/signin"} {
			if r := x.do(m, p, "{}", cookie(SessionCookie, s), origin("https://evil.example")); r.code != 403 ||
				!strings.Contains(r.body, "bad origin") {
				t.Errorf("%s %s cross-site: %d %s", m, p, r.code, r.body)
			}
		}
	}
	// A Bearer token carries no ambient credentials: no origin needed (the CLI, agents).
	if r := x.do(http.MethodPost, "/p/demo/api/push", `{}`, bearerTok(agentTok)); r.code == 403 {
		t.Fatalf("bearer post refused by the origin check: %s", r.body)
	}
	// Reads need no origin.
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, s), origin("https://evil.example")); r.code != 200 {
		t.Fatalf("GET with a foreign origin: %d", r.code)
	}
}

func TestHostsAndProxies(t *testing.T) {
	x := newTH(t, Config{}, func(o *Options) { o.Proxies = []string{"127.0.0.1"} })
	if r := x.do(http.MethodGet, "/signin", "", host("evil.example")); r.code != 403 || !strings.Contains(r.body,
		"bad host") {
		t.Fatalf("DNS rebinding host: %d", r.code)
	}
	for _, p := range []string{"/healthz", "/api/ping"} {
		if r := x.do(http.MethodGet, p, "", host("10.0.0.5:8080")); r.code != 200 {
			t.Fatalf("%s on any host: %d", p, r.code)
		}
	}
	// From a trusted proxy, X-Forwarded-For names the client: one client's failures don't block another.
	for i := range 10 {
		x.do(http.MethodGet, fmt.Sprintf("/signin/rs_%043d", i), "", header("X-Forwarded-For", "203.0.113.7"))
	}
	if r := x.do(http.MethodGet, "/signin/rs_"+strings.Repeat("Z", 43), "", header("X-Forwarded-For",
		"203.0.113.7")); r.code != 429 {
		t.Fatalf("the failing client: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/signin/rs_"+strings.Repeat("Z", 43), "", header("X-Forwarded-For",
		"198.51.100.9, 203.0.113.7")); r.code != 429 {
		t.Fatalf("a client can't hide behind a forged left-most entry: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/signin/rs_"+strings.Repeat("Z", 43), "", header("X-Forwarded-For",
		"198.51.100.9")); r.code != 401 {
		t.Fatalf("another client: %d", r.code)
	}

	// Without trusted proxies the header is ignored: everyone is the peer.
	y := newTH(t, Config{}, nil)
	for i := range 10 {
		y.do(http.MethodGet, fmt.Sprintf("/signin/rs_%043d", i), "", header("X-Forwarded-For",
			fmt.Sprintf("198.51.100.%d", i)))
	}
	if r := y.do(http.MethodGet, "/signin/rs_"+strings.Repeat("Z", 43), "", header("X-Forwarded-For",
		"192.0.2.1")); r.code != 429 {
		t.Fatalf("untrusted X-Forwarded-For was believed: %d", r.code)
	}
}

func TestRequestHelpers(t *testing.T) {
	ps, err := ParseProxies([]string{"10.0.0.0/8, 172.17.0.1", "::1"})
	if err != nil || len(ps) != 3 {
		t.Fatal(ps, err)
	}
	if _, err := ParseProxies([]string{"nope"}); err == nil {
		t.Fatal("bad proxy accepted")
	}
	for _, c := range []struct {
		xff  string
		want string
	}{
		{"203.0.113.7", "203.0.113.7"},
		{"203.0.113.7, 10.1.2.3", "203.0.113.7"},
		{"198.51.100.1, 203.0.113.7, 10.1.2.3", "203.0.113.7"},
		{"10.9.9.9, 10.1.2.3", "10.9.9.9"},
		{"garbage", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Forwarded-For", c.xff)
		if got := forwardedClient(r, ps); got != c.want {
			t.Errorf("%q: %q, want %q", c.xff, got, c.want)
		}
	}
	for _, c := range []struct {
		in     string
		origin string
		ok     bool
	}{
		{"https://Review.Example.com", "https://review.example.com", true},
		{"https://review.example.com/", "https://review.example.com", true},
		{"http://127.0.0.1:8941", "http://127.0.0.1:8941", true},
		{"https://review.example.com/sub", "", false},
		{"ftp://x", "", false},
		{"review.example.com", "", false},
		{"https://user:pw@review.example.com", "", false},
	} {
		p, err := ParsePublicURL(c.in)
		if (err == nil) != c.ok || p.Origin != c.origin {
			t.Errorf("%q: %+v %v", c.in, p, err)
		}
	}
	if p, _ := ParsePublicURL("http://localhost:8080"); !p.Loopback() {
		t.Error("localhost is loopback")
	}
	if p, _ := ParsePublicURL("http://review.lan"); p.Loopback() {
		t.Error("review.lan isn't loopback")
	}
	if logPath("/signin/rs_secret") != "/signin/<secret>" || logPath("/p/demo/s/rt_x") != "/p/demo/s/<secret>" {
		t.Error(logPath("/signin/rs_secret"), logPath("/p/demo/s/rt_x"))
	}
}

// Membership × role × route: who may do what on a project's routes (spec/server.md, "Who a request is").
func TestMembershipRoleRoute(t *testing.T) {
	x := newTH(t, Config{}, nil)
	demo := x.project("demo", false)
	other := x.project("other", false)
	admin := x.user("boss@example.com", true)
	ids := map[string][]reqOpt{"anonymous": nil}
	for _, r := range []team.Role{team.Viewer, team.Reviewer, team.Approver, team.Owner} {
		u := x.user(string(r)+"@example.com", false)
		x.member(demo, u, r)
		ids["member "+string(r)] = []reqOpt{cookie(SessionCookie, x.session(u))}
	}
	outsider := x.user("outsider@example.com", false)
	x.member(other, outsider, team.Owner)
	ids["other project's owner"] = []reqOpt{cookie(SessionCookie, x.session(outsider))}
	ids["admin"] = []reqOpt{cookie(SessionCookie, x.session(admin))}
	ids["agent token"] = []reqOpt{bearerTok(x.token("demo", team.Agent, "bot"))}
	ids["other project's agent"] = []reqOpt{bearerTok(x.token("other", team.Agent, "bot"))}
	ids["share viewer"] = []reqOpt{cookie(ShareCookie, x.token("demo", team.Viewer, "guest"))}
	ids["share reviewer"] = []reqOpt{cookie(ShareCookie, x.token("demo", team.Reviewer, "alex"))}
	ids["bearer reviewer"] = []reqOpt{bearerTok(x.token("demo", team.Reviewer, "alex"))}
	ids["bad token"] = []reqOpt{bearerTok("rt_" + strings.Repeat("A", 43))}

	take := "2026-10-30_10-00-00"
	type route struct {
		name, method, path, body string
		page                     bool
	}
	routes := []route{
		{"page", "GET", "/p/demo/", "", true},
		{"info", "GET", "/p/demo/api/info", "", false},
		{"video", "GET", "/p/demo/video", "", false},
		{"events", "HEAD", "/p/demo/api/events", "", false},
		{"record", "POST", "/p/demo/api/session/" + take + "/data?v=v1", `{"session":{}}`, false},
		{"approve", "POST", "/p/demo/api/versions/v1/approve", "", false},
		{"mcp", "POST", "/p/demo/mcp", `{}`, false},
		{"push", "POST", "/p/demo/api/push", `{"upload":"up_nope"}`, false},
		{"upload", "POST", "/p/demo/api/uploads", "", false},
		{"members", "GET", "/api/projects/demo/members", "", false},
		{"tokens", "POST", "/api/projects/demo/tokens", `{"role":"viewer"}`, false},
	}
	// What each identity gets: "ok" (the route ran), or a status.
	want := map[string]map[string]string{
		"anonymous": {"page": "303", "info": "401", "video": "401", "events": "401", "record": "401",
			"approve": "401", "mcp": "401", "push": "401", "upload": "401", "members": "401", "tokens": "401"},
		"member viewer": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "403", "approve": "403",
			"mcp": "401", "push": "401", "upload": "401", "members": "403", "tokens": "403"},
		"member reviewer": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok", "approve": "403",
			"mcp": "401", "push": "401", "upload": "401", "members": "403", "tokens": "403"},
		"member approver": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok", "approve": "ok",
			"mcp": "401", "push": "401", "upload": "401", "members": "403", "tokens": "403"},
		"member owner": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok", "approve": "ok",
			"mcp": "401", "push": "401", "upload": "401", "members": "ok", "tokens": "ok"},
		"admin": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok", "approve": "ok",
			"mcp": "401", "push": "401", "upload": "401", "members": "ok", "tokens": "ok"},
		"other project's owner": {"page": "403", "info": "403", "video": "403", "events": "403", "record": "403",
			"approve": "403", "mcp": "401", "push": "401", "upload": "401", "members": "403", "tokens": "403"},
		"agent token": {"page": "403", "info": "403", "video": "403", "events": "403", "record": "403",
			"approve": "403", "mcp": "ok", "push": "ok", "upload": "ok", "members": "401", "tokens": "401"},
		"other project's agent": {"page": "401", "info": "401", "video": "401", "events": "401", "record": "401",
			"approve": "401", "mcp": "401", "push": "401", "upload": "401", "members": "401", "tokens": "401"},
		"share viewer": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "403", "approve": "403",
			"mcp": "401", "push": "401", "upload": "401", "members": "401", "tokens": "401"},
		"share reviewer": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok", "approve": "403",
			"mcp": "401", "push": "401", "upload": "401", "members": "401", "tokens": "401"},
		"bearer reviewer": {"page": "ok", "info": "ok", "video": "ok", "events": "ok", "record": "ok",
			"approve": "403", "mcp": "403", "push": "403", "upload": "403", "members": "401", "tokens": "401"},
		"bad token": {"page": "401", "info": "401", "video": "401", "events": "401", "record": "401",
			"approve": "401", "mcp": "401", "push": "401", "upload": "401", "members": "401", "tokens": "401"},
	}
	for who, opts := range ids {
		x.h.tokenFails = team.NewLimiter() // the bad-token rows must not trip the rate limit for the others
		for _, rt := range routes {
			o := append([]reqOpt{origin(x.url), header("Tus-Resumable", "1.0.0"), header("Upload-Length", "1"),
				header("Upload-Metadata", "filename Y2xpcC5tcDQ=,sha256 "+b64(strings.Repeat("a", 64)))}, opts...)
			r := x.do(rt.method, rt.path, rt.body, o...)
			got := fmt.Sprint(r.code)
			if r.code != 401 && r.code != 403 && (!rt.page || r.code != 303) {
				got = "ok"
			}
			if exp := want[who][rt.name]; got != exp {
				t.Errorf("%s → %s %s: %s (%d %.80s), want %s", who, rt.method, rt.path, got, r.code, r.body, exp)
			}
		}
	}
}

func TestShareLinksAndAuthors(t *testing.T) {
	x := newTH(t, Config{}, nil)
	demo := x.project("demo", false)
	x.project("other", false)
	tok := x.token("demo", team.Reviewer, "alex")
	r := x.do(http.MethodGet, "/p/demo/s/"+tok, "")
	c := setCookie(t, r, ShareCookie)
	if r.code != 200 || c == nil || c.Path != "/p/demo/" || c.SameSite != http.SameSiteStrictMode || !c.HttpOnly ||
		!c.Secure || !strings.Contains(r.body, `location.replace("/p/demo/")`) {
		t.Fatalf("share link: %d %+v %s", r.code, c, r.body)
	}
	if info := x.do(http.MethodGet, "/p/demo/api/info", "", cookie(ShareCookie, tok)); info.code != 200 ||
		info.json(t)["role"] != "reviewer" || !strings.Contains(info.hdr.Get("Cache-Control"), "private") {
		t.Fatalf("info: %d %s %v", info.code, info.body, info.hdr)
	}
	// The cookie of one project is no key to another.
	if r := x.do(http.MethodGet, "/p/other/api/info", "", cookie(ShareCookie, tok)); r.code != 401 {
		t.Fatalf("other project with demo's token: %d", r.code)
	}
	// An agent token isn't a share link.
	if r := x.do(http.MethodGet, "/p/demo/s/"+x.token("demo", team.Agent, ""), ""); r.code != 403 {
		t.Fatalf("agent token as a link: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/p/demo/s/rt_"+strings.Repeat("B", 43), ""); r.code != 401 {
		t.Fatalf("bad link: %d", r.code)
	}

	// Takes carry the author: a member's account name, a guest's label.
	take := "2026-10-30_10-00-00"
	if r := x.post("/p/demo/api/session/"+take+"/data?v=v1", `{"session":{}}`, cookie(ShareCookie, tok)); r.code != 200 {
		t.Fatalf("guest take: %d %s", r.code, r.body)
	}
	ana := x.user("ana@example.com", false)
	_, _ = x.st.Accounts.UpdateUser(ana.Email, "Ana Lima", false)
	x.member(demo, ana, team.Reviewer)
	take2 := "2026-10-30_10-05-00"
	if r := x.post("/p/demo/api/session/"+take2+"/data?v=v1", `{"session":{}}`, cookie(SessionCookie,
		x.session(ana))); r.code != 200 {
		t.Fatalf("member take: %d %s", r.code, r.body)
	}
	for tk, name := range map[string]string{take: "alex", take2: "Ana Lima"} {
		b, err := os.ReadFile(filepath.Join(x.st.Workspace("demo"), "media", "demo_v1.review", tk, "session.json"))
		if err != nil {
			t.Fatal(err)
		}
		var s struct {
			Author struct{ Name string } `json:"author"`
		}
		_ = json.Unmarshal(b, &s)
		if s.Author.Name != name {
			t.Errorf("take %s: author %q, want %q", tk, s.Author.Name, name)
		}
	}
	// Take image URLs carry the project's prefix.
	dir := filepath.Join(x.st.Workspace("demo"), "media", "demo_v1.review", take)
	_ = os.WriteFile(filepath.Join(dir, "images.json"), []byte(`[{"seg":"S1","file":"img_01.png","video_t":1,"box":[0,0,1,1]}]`), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "img_01.png"), []byte("\x89PNG\r\n\x1a\n"), 0o600)
	hist := x.do(http.MethodGet, "/p/demo/api/history?v=v1", "", cookie(ShareCookie, tok))
	if !strings.Contains(hist.body, `"/p/demo/api/takes/`+take+`/img_01.png?v=v1"`) {
		t.Fatalf("history image URL: %s", hist.body)
	}
	if img := x.do(http.MethodGet, "/p/demo/api/takes/"+take+"/img_01.png?v=v1", "", cookie(ShareCookie, tok)); img.code != 200 {
		t.Fatalf("take image: %d", img.code)
	}
}

func TestProjectsPagesAndAdminRoutes(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin := x.user("boss@example.com", true)
	s := x.session(admin)
	x.project("empty", true)
	if r := x.do(http.MethodGet, "/p/empty", "", cookie(SessionCookie, s)); r.code != 308 || r.hdr.Get("Location") !=
		"/p/empty/" {
		t.Fatalf("no slash: %d %v", r.code, r.hdr)
	}
	if r := x.do(http.MethodGet, "/p/empty/", "", cookie(SessionCookie, s)); r.code != 200 ||
		!strings.Contains(r.body, "No versions yet") {
		t.Fatalf("empty project page: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/p/empty/api/info", "", cookie(SessionCookie, s)); r.code != 404 ||
		!strings.Contains(r.body, "no versions yet") {
		t.Fatalf("empty project API: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/p/nope/", "", cookie(SessionCookie, s)); r.code != 404 {
		t.Fatalf("unknown project: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/p/Bad..Name/api/info", ""); r.code != 404 {
		t.Fatalf("bad name: %d", r.code)
	}
	r := x.post("/api/users", `{"email":"ana@example.com","name":"Ana"}`, cookie(SessionCookie, s))
	if r.code != 201 || !strings.Contains(r.json(t)["link"].(string), x.url+"/signin/rs_") {
		t.Fatalf("add user: %d %s", r.code, r.body)
	}
	ana, _ := x.st.Accounts.User("ana@example.com")
	as := x.session(ana)
	if r := x.post("/api/users", `{"email":"bo@example.com"}`, cookie(SessionCookie, as)); r.code != 403 {
		t.Fatalf("non-admin adds a user: %d", r.code)
	}
	if r := x.post("/api/projects", `{"name":"mine"}`, cookie(SessionCookie, as)); r.code != 403 {
		t.Fatalf("non-admin adds a project: %d", r.code)
	}
	if r := x.post("/api/projects", `{"name":"Bad Name"}`, cookie(SessionCookie, s)); r.code != 400 {
		t.Fatalf("bad project name: %d", r.code)
	}
	if r := x.post("/api/projects/empty/members", `{"email":"ana@example.com","role":"owner"}`, cookie(SessionCookie,
		s)); r.code != 200 {
		t.Fatalf("add member: %d %s", r.code, r.body)
	}
	// Ana owns it now: she makes a link, lists tokens and revokes.
	tr := x.post("/api/projects/empty/tokens", `{"role":"viewer","label":"client"}`, cookie(SessionCookie, as))
	if tr.code != 201 || !strings.HasPrefix(tr.json(t)["url"].(string), x.url+"/p/empty/s/rt_") {
		t.Fatalf("make token: %d %s", tr.code, tr.body)
	}
	id := int(tr.json(t)["id"].(float64))
	if r := x.do(http.MethodDelete, fmt.Sprintf("/api/projects/empty/tokens/%d", id), "", origin(x.url),
		cookie(SessionCookie, as)); r.code != 200 || r.json(t)["revoked"] == "" {
		t.Fatalf("revoke: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/api/projects", "", cookie(SessionCookie, as)); r.code != 200 ||
		!strings.Contains(r.body, `"name":"empty"`) {
		t.Fatalf("projects: %s", r.body)
	}
	if r := x.do(http.MethodDelete, "/api/projects/empty/members/ana@example.com", "", origin(x.url),
		cookie(SessionCookie, s)); r.code != 200 {
		t.Fatalf("remove member: %d", r.code)
	}
	if home := x.do(http.MethodGet, "/", "", cookie(SessionCookie, as)); !strings.Contains(home.body, "No projects yet") {
		t.Fatalf("home after removal: %s", home.body)
	}
}

// fakeMailer records what it sends.
type fakeMailer struct {
	mu   sync.Mutex
	sent []string
}

func (m *fakeMailer) Send(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+subject+"|"+body)
	return nil
}

func TestMailedLinks(t *testing.T) {
	m := &fakeMailer{}
	x := newTH(t, Config{}, func(o *Options) { o.Mailer = m })
	x.user("ana@example.com", false)
	if r := x.do(http.MethodGet, "/signin", ""); !strings.Contains(r.body, `name="email"`) {
		t.Fatalf("sign-in form: %s", r.body)
	}
	for _, email := range []string{"Ana@Example.com", "nobody@example.com"} {
		r := x.post("/signin", "email="+email, header("Content-Type", "application/x-www-form-urlencoded"))
		if r.code != 200 || !strings.Contains(r.body, "a sign-in link is on its way") {
			t.Fatalf("%s: %d %s", email, r.code, r.body)
		}
	}
	if len(m.sent) != 1 || !strings.HasPrefix(m.sent[0], "ana@example.com|") || !strings.Contains(m.sent[0],
		x.url+"/signin/rs_") {
		t.Fatalf("sent: %q", m.sent)
	}
	for range 3 {
		x.post("/signin", "email=ana@example.com", header("Content-Type", "application/x-www-form-urlencoded"))
	}
	if r := x.post("/signin", "email=ana@example.com", header("Content-Type",
		"application/x-www-form-urlencoded")); r.code != 429 {
		t.Fatalf("sixth mail in a minute: %d", r.code)
	}
	if _, err := ParseSMTP("smtp://u:p@mail.example.com", "", "review.example.com:443"); err != nil {
		t.Fatal(err)
	}
	if s, _ := ParseSMTP("smtps://mail.example.com", "", ""); s.Addr != "mail.example.com:465" || !s.Implicit ||
		s.From != "retake@localhost" {
		t.Fatalf("%+v", s)
	}
	if _, err := ParseSMTP("http://mail", "", ""); err == nil {
		t.Fatal("http URL accepted")
	}
	if err := (&SMTP{Addr: "127.0.0.1:1"}).Send(context.Background(), "a@b.c\r\nBcc: x@y.z", "s", "b"); err == nil {
		t.Fatal("header injection")
	}
}

func TestAnchorAndFirstVersion(t *testing.T) {
	root := t.TempDir()
	if Anchor(root) != "" {
		t.Fatal("empty workspace has an anchor")
	}
	for _, rel := range []string{"media/x_v1.mp4", "media/x_v3_draft.mp4", "media/x_v3.mp4", "media/x_v2.mov",
		"media/x_v1.review/take/clip_v9.mp4", ".retake/x_v9.mp4", "notes.txt"} {
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o750)
		_ = os.WriteFile(p, []byte("x"), 0o600)
	}
	if got := Anchor(root); got != filepath.Join(root, "media", "x_v3.mp4") {
		t.Fatalf("anchor: %s", got)
	}
	st, err := project.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ws := t.TempDir()
	src := filepath.Join(t.TempDir(), "Intro Final.MOV")
	_ = os.WriteFile(src, []byte("video"), 0o600)
	row, err := FirstVersion(st, ws, "demo", src, "v3", "admin", "", true)
	if err != nil || row.Label != "v3" || row.Path != filepath.Join(ws, "media", "demo_v3.mov") || row.SHA256 == "" {
		t.Fatal(row, err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("moved file still there")
	}
	if _, err := FirstVersion(st, ws, "demo", src, "", "admin", "", false); err == nil {
		t.Fatal("a second first version")
	}
	if _, err := FirstVersion(st, t.TempDir(), "demo", filepath.Join(ws, "media", "demo_v3.mov"), "x1", "", "",
		false); err == nil {
		t.Fatal("bad label")
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
