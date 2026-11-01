package hub

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/thumb"
)

const homeHTML = "<!doctype html><title>home</title>"

// ft is a running Folder over a scratch folder of three projects:
//
//	alpha/v1/alpha_v1.mp4, alpha/v2/alpha_v2.mp4   (pushed last)
//	bravo/bravo_v1.mp4                             (pushed first)
//	charlie/v3/charlie_v3.mp4                      (with a closed take: reviewed)
//
// and broll/ (unnumbered clips, not a project). Videos are bytes; the prober is fake.
type ft struct {
	t      *testing.T
	f      *Folder
	dir    string
	url    string
	port   int
	ts     *httptest.Server
	thumbs atomic.Int32

	mu       sync.Mutex
	unwatchs []string
}

func file(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("video "+filepath.Base(path)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// closedTake writes a take of video that someone pressed Done on.
func closedTake(t *testing.T, video, take string) {
	t.Helper()
	dir := filepath.Join(filepath.Dir(video), strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))+".review",
		take)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"format": 3, "closed": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newFT(t *testing.T, mod func(*FolderOptions)) *ft {
	t.Helper()
	x := &ft{t: t, dir: filepath.Join(t.TempDir(), "Shows")}
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	file(t, filepath.Join(x.dir, "alpha", "v1", "alpha_v1.mp4"), base)
	file(t, filepath.Join(x.dir, "alpha", "v2", "alpha_v2.mp4"), base.Add(2*time.Hour))
	file(t, filepath.Join(x.dir, "bravo", "bravo_v1.mp4"), base.Add(time.Hour))
	charlie := filepath.Join(x.dir, "charlie", "v3", "charlie_v3.mp4")
	file(t, charlie, base.Add(3*time.Hour))
	closedTake(t, charlie, "2026-10-01_15-00-00")
	file(t, filepath.Join(x.dir, "broll", "sunset.mp4"), base)

	ts := httptest.NewUnstartedServer(nil)
	x.port = ts.Listener.Addr().(*net.TCPAddr).Port
	x.url = "http://127.0.0.1:" + strconv.Itoa(x.port)
	o := FolderOptions{Dir: x.dir, Port: x.port, Prober: fakeProber{}, Version: "test", Author: "Owner",
		UI: fstest.MapFS{"index.html": {Data: []byte(pageHTML)}, "home.html": {Data: []byte(homeHTML)},
			"assets/home.js": {Data: []byte("console.log(1)")}, ".secret": {Data: []byte("x")}},
		AllowHosts: []string{"share.test"}, EventPoll: 20 * time.Millisecond,
		Thumbs: &thumb.Maker{Run: func(_ context.Context, video, out string) error {
			x.thumbs.Add(1)
			return os.WriteFile(out, append([]byte{0xff, 0xd8, 0xff}, filepath.Base(video)...), 0o600)
		}},
		Project: func(_ string, o *server.Options) {
			o.Unwatch = func(label, viewer string) {
				x.mu.Lock()
				x.unwatchs = append(x.unwatchs, label+"/"+viewer)
				x.mu.Unlock()
			}
		},
	}
	if mod != nil {
		mod(&o)
	}
	x.f = NewFolder(o)
	ts.Config.Handler = x.f
	ts.Start()
	x.ts = ts
	t.Cleanup(func() {
		ts.Close()
		_ = x.f.Close()
	})
	return x
}

func (x *ft) do(method, path, body string, opts ...reqOpt) res {
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

func (x *ft) get(path string, opts ...reqOpt) res { return x.do(http.MethodGet, path, "", opts...) }

func (x *ft) home(t *testing.T, opts ...reqOpt) home.Response {
	t.Helper()
	r := x.get("/api/home", opts...)
	if r.code != 200 {
		t.Fatalf("/api/home: %d %s", r.code, r.body)
	}
	var h home.Response
	if err := json.Unmarshal([]byte(r.body), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func summary(h home.Response) []string {
	var out []string
	for _, c := range h.Projects {
		out = append(out, c.Name+" "+c.Version+" "+string(c.Status))
	}
	return out
}

// shared makes a request look like it came through the share's tunnel.
func shared() reqOpt { return host("share.test") }

// homeToken makes a token in the folder's home store, as retake share <folder> does.
func homeToken(t *testing.T, dir string, role team.Role, label string) string {
	t.Helper()
	st, err := project.OpenHome(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	tok, hash, err := team.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateToken(project.NewToken{Hash: hash, Role: string(role), Label: label}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func projectToken(t *testing.T, root string, role team.Role) string {
	t.Helper()
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	tok, hash, err := team.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateToken(project.NewToken{Hash: hash, Role: string(role)}); err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestFolderHome(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.RescanEvery = time.Nanosecond })
	h := x.home(t)
	if h.Title != "Shows" || h.Folder != x.dir || h.Role != "owner" {
		t.Errorf("home %+v", h)
	}
	// To review first, the newest push first; then the reviewed one. broll isn't a project.
	if got, want := strings.Join(summary(h), ", "), "alpha v2 to_review, bravo v1 to_review, charlie v3 reviewed"; got !=
		want {
		t.Errorf("projects: %s, want %s", got, want)
	}
	a := h.Projects[0]
	if a.URL != "p/alpha/" || a.Thumb != "p/alpha/api/poster" || a.Versions != 2 || a.Pushed != "2026-10-01T14:00:00Z" ||
		a.Duration == nil || *a.Duration != 10 || a.Title != "alpha" {
		t.Errorf("alpha's card %+v", a)
	}
	if h.Projects[2].Takes != 1 {
		t.Errorf("charlie's card %+v", h.Projects[2])
	}

	// The thumbnail is the project's poster, made once.
	for range 2 {
		if r := x.get("/" + a.Thumb); r.code != 200 || r.hdr.Get("Content-Type") != "image/jpeg" ||
			!strings.HasSuffix(r.body, "alpha_v2.mp4") {
			t.Fatalf("thumb: %d %v %q", r.code, r.hdr, r.body)
		}
	}
	if x.thumbs.Load() != 1 {
		t.Errorf("thumbnails made %d times", x.thumbs.Load())
	}
	if _, err := os.Stat(thumb.Dir(filepath.Join(x.dir, "alpha"))); err != nil {
		t.Errorf("not cached under the project's .retake/: %v", err)
	}

	// The home page, its files, and the review page of each project under /p/<name>/.
	if r := x.get("/"); r.code != 200 || r.body != homeHTML || r.hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("home page: %d %q", r.code, r.body)
	}
	if r := x.get("/assets/home.js"); r.code != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/javascript") {
		t.Errorf("asset: %d %v", r.code, r.hdr)
	}
	for _, p := range []string{"/.secret", "/assets/missing.js", "/api/nope", "/s"} {
		if r := x.get(p); r.code != 404 {
			t.Errorf("%s: %d", p, r.code)
		}
	}
	if r := x.get("/p/alpha"); r.code != 308 || r.hdr.Get("Location") != "/p/alpha/" {
		t.Errorf("no slash: %d %v", r.code, r.hdr)
	}
	if r := x.get("/p/alpha/"); r.code != 200 || r.body != pageHTML {
		t.Errorf("review page: %d %q", r.code, r.body)
	}
	r := x.get("/p/alpha/api/info")
	if r.code != 200 || r.json(t)["label"] != "v2" || r.json(t)["role"] != "owner" {
		t.Fatalf("alpha's info: %d %s", r.code, r.body)
	}
	if r := x.get("/p/nope/api/info"); r.code != 404 || r.json(t)["error"] != "no such project" {
		t.Errorf("unknown project: %d %s", r.code, r.body)
	}
	if r := x.get("/api/ping"); r.code != 200 {
		t.Errorf("ping: %d", r.code)
	}

	// A take with Done on alpha: it is reviewed, and moves after bravo.
	take := "2026-10-02_10-00-00"
	body := `{"session": {"format": 3, "closed": true}, "segments": [{"id": "S1"}], "strokes": []}`
	if r := x.do(http.MethodPost, "/p/alpha/api/session/"+take+"/data?v=v2", body, origin(x.url)); r.code != 200 {
		t.Fatalf("save: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/p/alpha/api/session/"+take+"/done?v=v2", "", origin(x.url)); r.code != 200 {
		t.Fatalf("done: %d %s", r.code, r.body)
	}
	b, _ := os.ReadFile(filepath.Join(x.dir, "alpha", "v2", "alpha_v2.review", take, "session.json"))
	if !strings.Contains(string(b), `"name": "Owner"`) {
		t.Errorf("author: %s", b)
	}
	if got := strings.Join(summary(x.home(t)), ", "); got !=
		"bravo v1 to_review, charlie v3 reviewed, alpha v2 reviewed" {
		t.Errorf("after Done: %s", got)
	}

	// A new project and a new version show up without a restart.
	file(t, filepath.Join(x.dir, "delta", "delta_v1.mp4"), time.Now())
	file(t, filepath.Join(x.dir, "alpha", "v3", "alpha_v3.mp4"), time.Now())
	if got := strings.Join(summary(x.home(t)), ", "); !strings.HasPrefix(got, "alpha v3 to_review, delta v1 to_review, ") {
		t.Errorf("after a push: %s", got)
	}
}

// The home's tree: folders, projects and their videos, nested, with the shorts of a project under it and a
// timeline project with no version yet; each video is served at its own /p/<name>/.
func TestFolderTree(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.RescanEvery = time.Nanosecond })
	now := time.Now()
	file(t, filepath.Join(x.dir, "2026-10-19", "fullgame", "fullgame_v2.mp4"), now)
	file(t, filepath.Join(x.dir, "alpha", "shorts", "one", "v1", "one_v1.mp4"), now)
	file(t, filepath.Join(x.dir, "alpha", "assets", "deep", "er", "x_v1.mp4"), now) // too deep in a project
	file(t, filepath.Join(x.dir, "drafts", "cut", "edit", "timeline.json"), now)
	h := x.home(t)
	byName := map[string]home.Card{}
	for _, c := range h.Projects {
		byName[c.Name] = c
	}
	if c := byName["2026-10-19-fullgame"]; c.Path != "2026-10-19/fullgame" || c.Project != "2026-10-19/fullgame" ||
		c.Video != "fullgame" || c.Title != "fullgame" || c.URL != "p/2026-10-19-fullgame/" {
		t.Errorf("a nested video: %+v", c)
	}
	if c := byName["alpha-shorts-one"]; c.Path != "alpha/shorts/one" || c.Project != "alpha" || c.Video != "one" {
		t.Errorf("a project's short: %+v", c)
	}
	if c := byName["alpha"]; c.Path != "alpha" || c.Project != "alpha" || c.Video != "alpha" || c.Questions != 0 {
		t.Errorf("alpha: %+v", c)
	}
	if len(h.Projects) != 5 {
		t.Errorf("videos: %v", summary(h))
	}
	if h.Tree == nil {
		t.Fatal("no tree")
	}
	var draw func(n *home.Node, indent string) []string
	draw = func(n *home.Node, indent string) []string {
		line := indent + n.Title
		if n.Project {
			var names []string
			for _, v := range n.Videos {
				names = append(names, v.Name+"@"+v.Version)
			}
			line += " [" + strings.Join(names, " ") + "]"
		}
		out := []string{line}
		for _, c := range n.Children {
			out = append(out, draw(c, indent+"  ")...)
		}
		return out
	}
	if got := strings.Join(draw(h.Tree, ""), "\n"); got != strings.Join([]string{
		"Shows",
		"  2026-10-19", "    fullgame [2026-10-19-fullgame@v2]",
		"  alpha [alpha@v2 alpha-shorts-one@v1]",
		"  bravo [bravo@v1]",
		"  charlie [charlie@v3]",
		"  drafts", "    cut []",
	}, "\n") {
		t.Errorf("tree:\n%s", got)
	}
	if r := x.get("/p/alpha-shorts-one/api/info"); r.code != 200 || r.json(t)["label"] != "v1" {
		t.Errorf("a short's review: %d %s", r.code, r.body)
	}
	if r := x.get("/p/2026-10-19-fullgame/"); r.code != 200 {
		t.Errorf("a nested video's page: %d", r.code)
	}

	// A link scoped to one video (its own store's token) sees only it, and only the folders on its way.
	tok := projectToken(t, filepath.Join(x.dir, "2026-10-19", "fullgame"), team.Viewer)
	h = x.home(t, shared(), bearerTok(tok))
	if got := strings.Join(draw(h.Tree, ""), "\n"); got != "Shows\n  2026-10-19\n    fullgame [2026-10-19-fullgame@v2]" {
		t.Errorf("a scoped link's tree:\n%s", got)
	}
}

func TestFolderStartsWithoutProjects(t *testing.T) {
	dir := t.TempDir()
	f := NewFolder(FolderOptions{Dir: dir, Prober: fakeProber{}})
	defer func() { _ = f.Close() }()
	if cards := f.Cards(); len(cards) != 0 {
		t.Errorf("cards %+v", cards)
	}
	ts := httptest.NewServer(f)
	defer ts.Close()
	// Not built: the stub; a Host other than ours: refused.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a Host that isn't 127.0.0.1:<port>: %d", res.StatusCode)
	}
}

func TestFolderShareTokens(t *testing.T) {
	x := newFT(t, nil)
	other := newFT(t, nil) // another folder's server, with its own home store
	reviewer := homeToken(t, x.dir, team.Reviewer, "alex")
	viewer := homeToken(t, x.dir, team.Viewer, "")
	foreign := homeToken(t, other.dir, team.Approver, "")
	alphaOnly := projectToken(t, filepath.Join(x.dir, "alpha"), team.Reviewer)

	// Through the share's host nothing works without a token: the home page says so, the API is 401.
	if r := x.get("/", shared()); r.code != 401 || !strings.Contains(r.body, "need a share link") {
		t.Errorf("home page without a token: %d %s", r.code, r.body)
	}
	for _, p := range []string{"/api/home", "/p/alpha/api/info", "/p/alpha/api/poster", "/assets/home.js"} {
		if r := x.get(p, shared()); r.code != 401 || r.hdr.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s without a token: %d", p, r.code)
		}
	}
	if r := x.get("/api/home", host("evil.test")); r.code != 403 || r.json(t)["error"] != "bad host" {
		t.Errorf("another host: %d %s", r.code, r.body)
	}

	// The share link sets the cookie for / and bounces to the home.
	r := x.get("/s/"+reviewer, shared())
	c := setCookie(t, r, ShareCookie)
	if r.code != 200 || c == nil || c.Value != reviewer || c.Path != "/" || !c.HttpOnly || !c.Secure ||
		c.SameSite != http.SameSiteStrictMode || !strings.Contains(r.body, `location.replace("/")`) {
		t.Fatalf("share link: %d %+v %s", r.code, c, r.body)
	}
	if r := x.get("/s/rt_"+strings.Repeat("A", 43), shared()); r.code != 401 {
		t.Errorf("a made-up link: %d", r.code)
	}

	// The folder's token works on the home and on every project, with its role; the cookie never leaves this folder.
	h := x.home(t, shared(), cookie(ShareCookie, reviewer))
	if h.Role != "reviewer" || h.Folder != "" || len(h.Projects) != 3 {
		t.Errorf("shared home: %+v", h)
	}
	if r := x.get("/", shared(), cookie(ShareCookie, reviewer)); r.code != 200 || !strings.Contains(r.hdr.Get(
		"Cache-Control"), "private") {
		t.Errorf("shared home page: %d %v", r.code, r.hdr)
	}
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		r := x.get("/p/"+name+"/api/info", shared(), cookie(ShareCookie, reviewer))
		if r.code != 200 || r.json(t)["role"] != "reviewer" || !strings.Contains(r.hdr.Get("Cache-Control"), "private") {
			t.Errorf("%s with the folder's token: %d %s", name, r.code, r.body)
		}
	}
	// A reviewer records under their label; a viewer can't record but can leave (unwatch).
	take := "2026-10-02_11-00-00"
	if r := x.do(http.MethodPost, "/p/bravo/api/session/"+take+"/data", `{"session": {}}`, shared(),
		cookie(ShareCookie, reviewer), origin("https://share.test")); r.code != 200 {
		t.Errorf("reviewer's take: %d %s", r.code, r.body)
	}
	b, _ := os.ReadFile(filepath.Join(x.dir, "bravo", "bravo_v1.review", take, "session.json"))
	if !strings.Contains(string(b), `"name": "alex"`) {
		t.Errorf("author: %s", b)
	}
	if r := x.do(http.MethodPost, "/p/bravo/api/session/"+take+"/data", `{"session": {}}`, shared(),
		cookie(ShareCookie, viewer), origin("https://share.test")); r.code != 403 {
		t.Errorf("viewer's take: %d", r.code)
	}
	if r := x.do(http.MethodPost, "/p/bravo/api/versions/v1/unwatch?viewer=v9", "", shared(),
		cookie(ShareCookie, viewer), origin("https://share.test")); r.code != 204 {
		t.Errorf("viewer's unwatch: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/p/bravo/api/versions/v1/unwatch", "", shared(), cookie(ShareCookie, viewer),
		origin("https://evil.test")); r.code != 403 || r.json(t)["error"] != "bad origin" {
		t.Errorf("unwatch from another site: %d %s", r.code, r.body)
	}
	x.mu.Lock()
	if len(x.unwatchs) != 1 || x.unwatchs[0] != "v1/v9" {
		t.Errorf("unwatch calls %v", x.unwatchs)
	}
	x.mu.Unlock()

	// Another folder's token opens nothing here, and this folder's opens nothing there.
	if r := x.get("/api/home", shared(), cookie(ShareCookie, foreign)); r.code != 401 {
		t.Errorf("another folder's token: %d", r.code)
	}
	if r := other.get("/api/home", shared(), cookie(ShareCookie, reviewer)); r.code != 401 {
		t.Errorf("this folder's token there: %d", r.code)
	}
	// A video's own token opens a filtered home and its video, never a different video.
	scoped := x.home(t, shared(), bearerTok(alphaOnly))
	if len(scoped.Projects) != 1 || scoped.Projects[0].Name != "alpha" {
		t.Fatal(scoped)
	}
	for _, credential := range []reqOpt{bearerTok(alphaOnly), cookie(ShareCookie, alphaOnly)} {
		if r := x.get("/p/alpha/api/info", shared(), credential); r.code != 200 {
			t.Errorf("alpha's token on alpha: %d %s", r.code, r.body)
		}
		if r := x.get("/p/bravo/api/info", shared(), credential); r.code != 403 {
			t.Errorf("alpha's token on bravo: %d %s", r.code, r.body)
		}
	}
	// Locally, nothing is needed: the owner.
	if h := x.home(t); h.Role != "owner" {
		t.Errorf("local role %s", h.Role)
	}
	// Proxy headers on a local Host make the request shared.
	if r := x.get("/api/home", header("X-Forwarded-For", "1.2.3.4")); r.code != 401 {
		t.Errorf("forwarded: %d", r.code)
	}
}

func TestFolderSharesFromTheHostsTable(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.AllowHosts = nil })
	tok := homeToken(t, x.dir, team.Viewer, "")
	if r := x.get("/api/home", host("words.trycloudflare.com"), cookie(ShareCookie, tok)); r.code != 403 {
		t.Errorf("before the share: %d", r.code)
	}
	st, err := project.OpenHome(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.AddHost("words.trycloudflare.com", "cloudflare", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if r := x.get("/api/home", host("words.trycloudflare.com"), cookie(ShareCookie, tok)); r.code != 200 {
		t.Errorf("during the share: %d %s", r.code, r.body)
	}
	// An agent's token is not a share link.
	agentTok := homeToken(t, x.dir, team.Agent, "")
	if r := x.get("/s/"+agentTok, host("words.trycloudflare.com")); r.code != 403 {
		t.Errorf("agent token as a link: %d", r.code)
	}
	// Ten bad tokens in a minute and the client waits.
	bad := "rt_" + strings.Repeat("B", 43)
	for range 10 {
		x.get("/api/home", host("words.trycloudflare.com"), cookie(ShareCookie, bad))
	}
	if r := x.get("/api/home", host("words.trycloudflare.com"), cookie(ShareCookie, tok)); r.code != 429 ||
		r.hdr.Get("Retry-After") == "" {
		t.Errorf("rate limit: %d", r.code)
	}
}

func TestFolderIdleExit(t *testing.T) {
	now := time.Now()
	var mu sync.Mutex
	x := newFT(t, func(o *FolderOptions) {
		o.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
		o.IdleExit = time.Minute
	})
	if x.f.Idle() {
		t.Error("idle before any request")
	}
	x.get("/api/ping")
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	if !x.f.Idle() {
		t.Error("not idle after the pages left")
	}
	x.get("/p/alpha/api/ping")
	if x.f.Idle() {
		t.Error("a project's request isn't contact")
	}
}

func TestFolderServeStops(t *testing.T) {
	dir := t.TempDir()
	file(t, filepath.Join(dir, "a", "a_v1.mp4"), time.Now())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := NewFolder(FolderOptions{Dir: dir, Port: ln.Addr().(*net.TCPAddr).Port, Prober: fakeProber{},
		IdleExit: 50 * time.Millisecond, CheckEvery: 10 * time.Millisecond})
	done := make(chan bool, 1)
	go func() {
		idle, err := f.Serve(context.Background(), ln)
		if err != nil {
			t.Error(err)
		}
		done <- idle
	}()
	res, err := http.Get("http://" + ln.Addr().String() + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	select {
	case idle := <-done:
		if !idle {
			t.Error("stopped, but not for idling")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("didn't exit when idle")
	}
	if err := f.Close(); err != nil {
		t.Error(err)
	}
}

// The team server answers /api/home for a signed-in person: their projects' cards.
func TestHubHome(t *testing.T) {
	x := newTH(t, Config{}, nil)
	ana := x.user("ana@example.com", false)
	s := x.session(ana)
	demo := x.project("demo", false)
	empty := x.project("empty", true)
	x.project("hidden", false)
	x.member(demo, ana, team.Reviewer)
	x.member(empty, ana, team.Viewer)
	r := x.do(http.MethodGet, "/api/home", "", cookie(SessionCookie, s))
	if r.code != 200 {
		t.Fatalf("/api/home: %d %s", r.code, r.body)
	}
	var h home.Response
	if err := json.Unmarshal([]byte(r.body), &h); err != nil {
		t.Fatal(err)
	}
	if h.User == nil || h.User.Email != "ana@example.com" || h.Folder != "" || len(h.Projects) != 2 {
		t.Fatalf("home %+v", h)
	}
	if got := strings.Join(summary(h), ", "); got != "demo v1 to_review, empty  to_review" {
		t.Errorf("projects: %s", got)
	}
	if h.Projects[0].Thumb != "p/demo/api/poster" || h.Projects[1].Thumb != "" {
		t.Errorf("thumbs %+v", h.Projects)
	}
	if r := x.do(http.MethodGet, "/api/home", ""); r.code != 401 {
		t.Errorf("without a session: %d", r.code)
	}
}
