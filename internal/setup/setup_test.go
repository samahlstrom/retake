package setup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/worker"
)

type world struct {
	t      *testing.T
	home   string
	cfg    string
	h      *Handler
	srv    *httptest.Server
	host   string
	agents []project.AgentProfile
	tools  map[string]bool // found tools; herdr's value says it's bundled
	done   chan string
	checks int
	mu     sync.Mutex
}

func newWorld(t *testing.T, goos string) *world {
	t.Helper()
	w := &world{t: t, home: t.TempDir(), tools: map[string]bool{"ffprobe": false}, done: make(chan string, 1)}
	w.cfg = filepath.Join(w.home, ".config", "retake", "config.toml")
	w.agents = []project.AgentProfile{{ID: "claude", Name: "Claude Code"}, {ID: "codex", Name: "Codex"}}
	w.srv = httptest.NewUnstartedServer(nil)
	w.srv.Start()
	t.Cleanup(w.srv.Close)
	port := w.srv.Listener.Addr().(*net.TCPAddr).Port
	w.host = "127.0.0.1:" + strconv.Itoa(port)
	w.h = New(Options{
		UI:         fstest.MapFS{"setup.html": {Data: []byte("<!doctype html><title>setup</title>")}, "assets/a.js": {Data: []byte("1")}},
		Port:       port,
		ConfigPath: w.cfg,
		Home:       w.home,
		GOOS:       goos,
		Agents: func() []project.AgentProfile {
			w.mu.Lock()
			defer w.mu.Unlock()
			return append([]project.AgentProfile(nil), w.agents...)
		},
		Try: func(_ context.Context, id string) worker.TryResult {
			if id == "claude" {
				return worker.TryResult{OK: true, Reply: "ready", Seconds: 1.5}
			}
			return worker.TryResult{Error: "Not signed in: run codex once in Terminal and sign in."}
		},
		Check: func(context.Context) {
			w.mu.Lock()
			w.checks++
			w.agents = append(w.agents, project.AgentProfile{ID: "gemini", Name: "Gemini"})
			w.mu.Unlock()
		},
		Find: func(name string) (string, bool, error) {
			bundled, ok := w.tools[name]
			if !ok {
				return "", false, errors.New("not found")
			}
			return "/x/" + name, bundled, nil
		},
		Done: func(folder string) { w.done <- folder },
	})
	w.srv.Config.Handler = w.h
	return w
}

func (w *world) do(method, path, body string, hdr map[string]string) (int, map[string]any) {
	w.t.Helper()
	req, err := http.NewRequest(method, w.srv.URL+path, strings.NewReader(body))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Host = w.host
	if method == http.MethodPost {
		req.Header.Set("Origin", "http://"+w.host)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{"_body": string(b)}
	}
	return res.StatusCode, out
}

func (w *world) config() config.Config {
	w.t.Helper()
	c, _, err := config.Load(w.cfg)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestStateFirstTime(t *testing.T) {
	w := newWorld(t, "darwin")
	mkdir(t, filepath.Join(w.home, "Movies", "clip", "v1"))
	if err := os.WriteFile(filepath.Join(w.home, "Movies", "clip", "v1", "clip_v1.mp4"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, s := w.do("GET", "/api/setup", "", nil)
	if code != 200 || s["folder"] != "~/Movies" || s["folder_exists"] != true || s["videos"] != 1.0 ||
		s["agent"] != "claude" || s["open_in"] != "background" || s["os"] != "darwin" || s["ffmpeg"] != true ||
		s["config"] != "~/.config/retake/config.toml" {
		t.Fatalf("state: %d %v", code, s)
	}
	// herdr that came with retake is the default place.
	w.tools["herdr"] = true
	_, s = w.do("GET", "/api/setup", "", nil)
	places, _ := s["places"].([]any)
	if s["open_in"] != "herdr" || len(places) != 3 || places[0].(map[string]any)["bundled"] != true ||
		places[1].(map[string]any)["available"] != false {
		t.Errorf("with herdr: %v", s)
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Error("looking wrote the config")
	}
}

func TestSuggest(t *testing.T) {
	home := t.TempDir()
	if got := Suggest(home, "darwin"); got != filepath.Join(home, "Movies") {
		t.Errorf("mac %s", got)
	}
	if got := Suggest(home, "linux"); got != filepath.Join(home, "Videos") {
		t.Errorf("linux, none yet: %s", got)
	}
}

func TestHostsAndOrigins(t *testing.T) {
	w := newWorld(t, "linux")
	if code, _ := w.do("GET", "/api/setup", "", map[string]string{"Host": "evil.example"}); code != 403 {
		t.Errorf("bad host: %d", code)
	}
	if code, _ := w.do("GET", "/api/setup", "", map[string]string{"X-Forwarded-For": "1.2.3.4"}); code != 403 {
		t.Errorf("through a proxy: %d", code)
	}
	if code, _ := w.do("POST", "/api/setup", `{"agent":"codex"}`, map[string]string{"Origin": "https://evil.example"}); code != 403 {
		t.Errorf("bad origin: %d", code)
	}
	if code, s := w.do("GET", "/", "", nil); code != 200 || !strings.Contains(s["_body"].(string), "<title>setup</title>") {
		t.Errorf("page: %d %v", code, s)
	}
	if code, _ := w.do("GET", "/assets/a.js", "", nil); code != 200 {
		t.Errorf("asset: %d", code)
	}
	for _, p := range []string{"/.git/config", "/api/nope", "/assets/missing.js"} {
		if code, _ := w.do("GET", p, "", nil); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
	if code, s := w.do("GET", "/api/home", "", nil); code != 503 || s["error"] != "setting up" {
		t.Errorf("api/home: %d %v", code, s)
	}
	if code, s := w.do("GET", "/api/ping", "", nil); code != 200 || s["setup"] != true {
		t.Errorf("ping: %d %v", code, s)
	}
}

func TestSaveChecksEverythingFirst(t *testing.T) {
	w := newWorld(t, "linux")
	mkdir(t, filepath.Join(w.home, "Clips"))
	for _, body := range []string{
		`{"folder":"relative/path"}`, `{"folder":"~/missing/deeper"}`, `{"agent":"nope"}`,
		`{"open_in":"screen"}`, `{"open_in":"tmux"}`, // tmux isn't installed here
		`{"folder":"~/Clips","agent":"nope"}`, `{"unknown":1}`, `not json`,
	} {
		if code, s := w.do("POST", "/api/setup", body, nil); code != 400 {
			t.Errorf("%s: %d %v", body, code, s)
		}
	}
	if _, err := os.Stat(w.cfg); err == nil {
		t.Fatal("a refused answer wrote the config")
	}
	code, s := w.do("POST", "/api/setup", `{"folder":"~/Clips","agent":"codex","open_in":"background"}`, nil)
	if code != 200 || s["folder"] != "~/Clips" || s["agent"] != "codex" || s["videos"] != 0.0 {
		t.Fatalf("save: %d %v", code, s)
	}
	c := w.config()
	if c.Folder == nil || *c.Folder != "~/Clips" || *c.DefaultAgent != "codex" || c.OpenAgentsIn != nil {
		t.Errorf("config: %+v", c)
	}
}

func TestDone(t *testing.T) {
	w := newWorld(t, "linux")
	w.tools["herdr"] = true
	// ~/Videos doesn't exist yet: Done makes it.
	code, s := w.do("POST", "/api/setup/done", `{}`, nil)
	if code != 200 || s["ok"] != true {
		t.Fatalf("done: %d %v", code, s)
	}
	select {
	case f := <-w.done:
		if f != filepath.Join(w.home, "Videos") {
			t.Errorf("done with %s", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Done wasn't called")
	}
	if st, err := os.Stat(filepath.Join(w.home, "Videos")); err != nil || !st.IsDir() {
		t.Error("Done didn't make the folder")
	}
	c := w.config()
	if *c.Folder != "~/Videos" || *c.DefaultAgent != "claude" || *c.OpenAgentsIn != "herdr" {
		t.Errorf("config after Done: folder %v agent %v open_in %v", *c.Folder, *c.DefaultAgent, *c.OpenAgentsIn)
	}
	if code, _ := w.do("POST", "/api/setup", `{"agent":"codex"}`, nil); code != 409 {
		t.Errorf("after Done: %d", code)
	}
}

func TestCheckAgainAndTest(t *testing.T) {
	w := newWorld(t, "linux")
	w.agents = nil
	_, s := w.do("GET", "/api/setup", "", nil)
	if a, _ := s["agents"].([]any); len(a) != 0 || s["agent"] != "" {
		t.Errorf("no agents: %v", s)
	}
	_, s = w.do("POST", "/api/setup/check", "", nil)
	if a, _ := s["agents"].([]any); len(a) != 1 || s["agent"] != "gemini" || w.checks != 1 {
		t.Errorf("check again: %v (checks %d)", s, w.checks)
	}
	w.agents = []project.AgentProfile{{ID: "claude", Name: "Claude Code"}, {ID: "codex", Name: "Codex"}}
	if code, s := w.do("POST", "/api/setup/test", `{"agent":"claude"}`, nil); code != 200 || s["ok"] != true || s["reply"] != "ready" {
		t.Errorf("test claude: %d %v", code, s)
	}
	if code, s := w.do("POST", "/api/setup/test", `{"agent":"codex"}`, nil); code != 200 || s["ok"] != false ||
		!strings.HasPrefix(s["error"].(string), "Not signed in") {
		t.Errorf("test codex: %d %v", code, s)
	}
	if code, _ := w.do("POST", "/api/setup/test", `{"agent":"nope"}`, nil); code != 400 {
		t.Errorf("test nope: %d", code)
	}
}

func TestFolders(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"b", "A", ".hidden", "clip.review", "c/d"} {
		mkdir(t, filepath.Join(home, "Movies", d))
	}
	if err := os.WriteFile(filepath.Join(home, "Movies", "file.mp4"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := ListFolders("~/Movies", home)
	if err != nil || l.Path != "~/Movies" || l.Parent != "~" || strings.Join(l.Folders, ",") != "A,b,c" {
		t.Errorf("list: %+v %v", l, err)
	}
	if l, _ := ListFolders("", home); l.Path != "~" {
		t.Errorf("empty is home: %+v", l)
	}
	if _, err := ListFolders("~/Movies/file.mp4", home); err == nil {
		t.Error("listed a file")
	}
	if l, err := ListFolders("/", home); err != nil || l.Parent != "" {
		t.Errorf("the top: %+v %v", l, err)
	}
	w := newWorld(t, "linux")
	mkdir(t, filepath.Join(w.home, "x"))
	if code, s := w.do("GET", "/api/setup/folders?path=~", "", nil); code != 200 || s["path"] != "~" {
		t.Errorf("route: %d %v", code, s)
	}
	if code, _ := w.do("GET", "/api/setup/folders?path=~/nope", "", nil); code != 404 {
		t.Errorf("missing: %d", code)
	}
}

func TestCheckFolder(t *testing.T) {
	home := t.TempDir()
	mkdir(t, filepath.Join(home, "v"))
	if p, err := CheckFolder("~/v", home, false); err != nil || p != filepath.Join(home, "v") {
		t.Errorf("~/v: %q %v", p, err)
	}
	if _, err := CheckFolder("~/new", home, false); err == nil {
		t.Error("a missing folder passed")
	}
	if p, err := CheckFolder("~/new", home, true); err != nil || p != filepath.Join(home, "new") {
		t.Errorf("may make: %q %v", p, err)
	}
	if _, err := CheckFolder("~/a/b", home, true); err == nil {
		t.Error("a missing parent passed")
	}
	if Tilde(home, home) != "~" || Tilde("/elsewhere", home) != "/elsewhere" {
		t.Error("Tilde")
	}
}
