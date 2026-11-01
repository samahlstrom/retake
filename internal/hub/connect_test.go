package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

// jpeg is the start of a JPEG: enough for the poster check.
var jpeg = string([]byte{0xff, 0xd8, 0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0})

// connect makes a machine token through POST /api/connect as an admin.
func (x *th) connect(adminSession string) string {
	x.t.Helper()
	r := x.post("/api/connect", "", cookie(SessionCookie, adminSession))
	if r.code != 201 {
		x.t.Fatalf("connect: %d %s", r.code, r.body)
	}
	m := r.json(x.t)
	cmd, _ := m["command"].(string)
	tok, _ := m["token"].(string)
	if cmd != "retake connect "+x.url+" --token "+tok || !strings.HasPrefix(tok, "rk_") || m["url"] != x.url {
		x.t.Fatalf("connect: %v", m)
	}
	return tok
}

func TestConnectIsForAdmins(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin, member := x.user("admin@example.com", true), x.user("member@example.com", false)
	if r := x.post("/api/connect", ""); r.code != 401 {
		t.Fatalf("no session: %d", r.code)
	}
	if r := x.post("/api/connect", "", cookie(SessionCookie, x.session(member))); r.code != 403 {
		t.Fatalf("a member: %d", r.code)
	}
	as := x.session(admin)
	tok := x.connect(as)
	if tok == x.connect(as) {
		t.Fatal("two calls gave the same token")
	}
	list := x.do(http.MethodGet, "/api/connect", "", cookie(SessionCookie, as))
	if list.code != 200 || strings.Contains(list.body, tok) || strings.Count(list.body, `"id"`) != 2 {
		t.Fatalf("list: %d %s", list.code, list.body)
	}
	if r := x.do(http.MethodDelete, "/api/connect/1", "", cookie(SessionCookie, as), origin(x.url)); r.code != 200 {
		t.Fatalf("revoke: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/api/machine", "", bearerTok(tok)); r.code != 401 {
		t.Fatalf("a revoked token: %d", r.code)
	}
	if r := x.do(http.MethodDelete, "/api/connect/99", "", cookie(SessionCookie, as), origin(x.url)); r.code != 404 {
		t.Fatalf("unknown id: %d", r.code)
	}
}

func TestMachineTokenScope(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin := x.user("admin@example.com", true)
	as := x.session(admin)
	tok := x.connect(as)
	mt := bearerTok(tok)

	if r := x.do(http.MethodGet, "/api/machine", "", mt); r.code != 200 || r.json(t)["url"] != x.url {
		t.Fatalf("check: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/api/machine", ""); r.code != 401 || !strings.Contains(r.body, "machine token required") {
		t.Fatalf("no token: %d %s", r.code, r.body)
	}

	// It registers projects: made when missing, kept when they exist.
	r := x.do(http.MethodPost, "/api/machine/projects", `{"name":"intro","title":"Intro cut"}`, mt)
	if r.code != 201 || r.json(t)["created"] != true || r.json(t)["url"] != x.url+"/p/intro" {
		t.Fatalf("register: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/api/machine/projects", `{"name":"intro","title":"Other"}`, mt); r.code != 200 ||
		r.json(t)["created"] != false || r.json(t)["title"] != "Intro cut" {
		t.Fatalf("again: %d %s", r.code, r.body)
	}
	for _, bad := range []string{`{"name":"Bad Name"}`, `{"name":""}`, `{"name":"ok","title":"` + strings.Repeat("t", 201) + `"}`, `nope`} {
		if r := x.do(http.MethodPost, "/api/machine/projects", bad, mt); r.code != 400 {
			t.Errorf("%s: %d", bad, r.code)
		}
	}

	// A poster: a JPEG, nothing else.
	put := func(name, ctype, body string) res {
		return x.do(http.MethodPut, "/api/machine/projects/"+name+"/poster", body, mt, header("Content-Type", ctype))
	}
	if r := put("intro", "text/plain", jpeg); r.code != 415 {
		t.Fatalf("wrong type: %d", r.code)
	}
	if r := put("intro", "image/jpeg", "not a jpeg"); r.code != 400 {
		t.Fatalf("not a jpeg: %d", r.code)
	}
	if r := put("intro", "image/jpeg", jpeg+strings.Repeat("x", MaxPoster)); r.code != 413 {
		t.Fatalf("too big: %d", r.code)
	}
	if r := put("nope", "image/jpeg", jpeg); r.code != 404 {
		t.Fatalf("unknown project: %d", r.code)
	}
	if r := put("intro", "image/jpeg", jpeg); r.code != 204 {
		t.Fatalf("poster: %d %s", r.code, r.body)
	}
	// The project's poster and card use it until there's a version.
	if r := x.do(http.MethodGet, "/p/intro/api/poster", "", cookie(SessionCookie, as)); r.code != 200 ||
		r.hdr.Get("Content-Type") != "image/jpeg" || r.body != jpeg {
		t.Fatalf("poster: %d %v", r.code, r.hdr)
	}
	var hr home.Response
	if r := x.do(http.MethodGet, "/api/home", "", cookie(SessionCookie, as)); r.code != 200 ||
		json.Unmarshal([]byte(r.body), &hr) != nil || len(hr.Projects) != 1 || hr.Projects[0].Thumb != "p/intro/api/poster" ||
		hr.Projects[0].Title != "Intro cut" || hr.Role != "owner" {
		t.Fatalf("home: %d %s", r.code, r.body)
	}

	// On a project it is the agent: the worker routes and MCP, not the page.
	worker := project.EditingWorker{ID: "w1", Name: "sam-pc", Session: "s1",
		Agents: []project.AgentProfile{{ID: "claude", Name: "Claude Code"}}}
	poll, _ := json.Marshal(map[string]any{"worker": worker})
	if r := x.do(http.MethodPost, "/p/intro/api/worker/poll", string(poll), mt); r.code != 200 {
		t.Fatalf("worker poll: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/p/intro/mcp", `{}`, mt, header("Content-Type", "application/json")); r.code == 401 ||
		r.code == 403 {
		t.Fatalf("mcp: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/p/intro/", "", mt); r.code != 403 {
		t.Fatalf("page: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/p/intro/api/info", "", mt); r.code != 403 {
		t.Fatalf("info: %d", r.code)
	}

	// It administers nothing: it is never a session.
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/me", ""}, {http.MethodGet, "/api/projects", ""}, {http.MethodGet, "/api/home", ""},
		{http.MethodPost, "/api/projects", `{"name":"x"}`}, {http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"email":"eve@example.com","admin":true}`},
		{http.MethodPost, "/api/connect", ""}, {http.MethodGet, "/api/connect", ""},
		{http.MethodGet, "/api/projects/intro/tokens", ""},
		{http.MethodPost, "/api/projects/intro/tokens", `{"role":"approver"}`},
		{http.MethodPost, "/api/projects/intro/members", `{"email":"admin@example.com","role":"owner"}`},
	} {
		if r := x.do(c.method, c.path, c.body, mt); r.code != 401 {
			t.Errorf("%s %s: %d %s", c.method, c.path, r.code, r.body)
		}
	}
	if r := x.do(http.MethodGet, "/", "", mt); r.code != 303 {
		t.Errorf("projects page: %d", r.code)
	}

	// A project's own token isn't a machine token, and a bad one counts as a failed attempt.
	agent := x.token("intro", team.Agent, "ci")
	if r := x.do(http.MethodGet, "/api/machine", "", bearerTok(agent)); r.code != 401 {
		t.Fatalf("a project token: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/p/intro/api/worker/poll", string(poll), bearerTok("rk_"+strings.Repeat("Z", 43))); r.code != 401 {
		t.Fatalf("a bad machine token: %d", r.code)
	}

	// Demoting the admin who made it ends it.
	if _, err := x.st.Accounts.UpdateUser("admin@example.com", "", false); err != nil {
		t.Fatal(err)
	}
	if r := x.do(http.MethodPost, "/p/intro/api/worker/poll", string(poll), mt); r.code != 401 {
		t.Fatalf("after demotion: %d", r.code)
	}
}

func TestHomeMachines(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin, member := x.user("admin@example.com", true), x.user("member@example.com", false)
	as := x.session(admin)
	tok := x.connect(as)
	for _, name := range []string{"one", "two", "three"} {
		if r := x.do(http.MethodPost, "/api/machine/projects", `{"name":"`+name+`"}`, bearerTok(tok)); r.code != 201 {
			t.Fatalf("%s: %d", name, r.code)
		}
	}
	p3, _ := x.st.Accounts.Project("three")
	x.member(p3, member, team.Reviewer)
	machines := func(session string) []home.Machine {
		t.Helper()
		var hr home.Response
		r := x.do(http.MethodGet, "/api/home", "", cookie(SessionCookie, session))
		if r.code != 200 || json.Unmarshal([]byte(r.body), &hr) != nil || hr.Machines == nil {
			t.Fatalf("home: %d %s", r.code, r.body)
		}
		return hr.Machines
	}
	if m := machines(as); len(m) != 0 {
		t.Fatalf("no workers yet: %+v", m)
	}
	// One computer runs a worker per project; another one edits project three.
	poll := func(project, id, name string, agents ...string) {
		t.Helper()
		w := map[string]any{"id": id, "name": name, "session": id + "-s", "agents": []map[string]string{}}
		for _, a := range agents {
			w["agents"] = append(w["agents"].([]map[string]string), map[string]string{"id": a, "name": strings.ToUpper(a)})
		}
		b, _ := json.Marshal(map[string]any{"worker": w})
		if r := x.do(http.MethodPost, "/p/"+project+"/api/worker/poll", string(b), bearerTok(tok)); r.code != 200 {
			t.Fatalf("poll %s: %d %s", project, r.code, r.body)
		}
	}
	poll("one", "b-one", "sam-pc", "claude")
	poll("two", "a-two", "sam-pc", "codex", "claude")
	poll("three", "c-three", "studio", "codex")
	// Grouped by name, with the smallest worker id, every agent once; online first, then by name.
	want := `[{a-two sam-pc true [{claude CLAUDE} {codex CODEX}]} {c-three studio true [{codex CODEX}]}]`
	if got := machines(as); fmt.Sprint(got) != want {
		t.Fatalf("machines:\n%v\nwant\n%v", got, want)
	}
	// A member sees the machines of their projects only.
	if m := machines(x.session(member)); len(m) != 1 || m[0].Name != "studio" {
		t.Fatalf("member: %+v", m)
	}
	// After the worker lease they are offline, still listed.
	x.clock.add(project.WorkerLease + time.Second)
	for _, m := range machines(as) {
		if m.Online {
			t.Fatalf("still online: %+v", m)
		}
	}
}

func TestHubServesTheHomePage(t *testing.T) {
	x := newTH(t, Config{}, func(o *Options) {
		o.UI = fstestUI()
	})
	as := x.session(x.user("admin@example.com", true))
	if r := x.do(http.MethodGet, "/", ""); r.code != 303 || r.hdr.Get("Location") != "/signin" {
		t.Fatalf("signed out: %d %v", r.code, r.hdr)
	}
	if r := x.do(http.MethodGet, "/", "", cookie(SessionCookie, as)); r.code != 200 || r.body != "<!doctype html><title>home</title>" {
		t.Fatalf("home: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/assets/home.js", ""); r.code != 200 || r.body != "console.log(1)" {
		t.Fatalf("asset: %d %s", r.code, r.body)
	}
	for _, p := range []string{"/assets/.secret", "/assets/missing.js", "/assets/../index.html"} {
		if r := x.do(http.MethodGet, p, ""); r.code != 404 {
			t.Errorf("%s: %d", p, r.code)
		}
	}
}

func fstestUI() fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte(pageHTML)}, "home.html": {Data: []byte("<!doctype html><title>home</title>")},
		"assets/home.js": {Data: []byte("console.log(1)")}, "assets/.secret": {Data: []byte("x")}}
}

func TestSignOutPage(t *testing.T) {
	x := newTH(t, Config{}, nil)
	s := x.session(x.user("ana@example.com", false))
	if r := x.do(http.MethodGet, "/signout", ""); r.code != 303 || r.hdr.Get("Location") != "/signin" {
		t.Fatalf("signed out: %d", r.code)
	}
	r := x.do(http.MethodGet, "/signout", "", cookie(SessionCookie, s))
	if r.code != 200 || !strings.Contains(r.body, `action="/signout"`) || !strings.Contains(r.body, "ana@example.com") {
		t.Fatalf("page: %d %s", r.code, r.body)
	}
	if r := x.post("/signout", "", cookie(SessionCookie, s)); r.code != 303 || r.hdr.Get("Location") != "/signin" {
		t.Fatalf("post: %d", r.code)
	}
	if r := x.do(http.MethodGet, "/api/me", "", cookie(SessionCookie, s)); r.code != 401 {
		t.Fatalf("after: %d", r.code)
	}
}
