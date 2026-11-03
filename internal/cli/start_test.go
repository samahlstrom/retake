package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/worker"
)

// startEnv is the world's environment for retake start: a state folder, no real PATH lookups.
func (w *world) startEnv() Env {
	w.env["XDG_STATE_HOME"] = filepath.Join(w.root, "state")
	env := w.environment(nil)
	env.GOOS = "linux"
	env.RefreshPath = func(context.Context) {}
	env.Find = func(name string) (string, bool, error) {
		if name == "herdr" {
			return "/bundle/herdr", true, nil
		}
		return "", false, errors.New("not found")
	}
	ok := []string{"/bin/true"} // an agent that answers the agent test
	if runtime.GOOS == "windows" {
		ok = []string{"cmd.exe", "/c", "rem"}
	}
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"}, Command: ok}}
	}
	return env
}

func postTest(t *testing.T, url, body string, v any) int {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if v != nil {
		_ = json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

// waitHome polls api/home until it answers 200 with the folder.
func waitHome(t *testing.T, url, folder string) home.Response {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var h home.Response
		if code := getJSON(t, url+"api/home", &h); code == 200 && h.Folder == folder {
			return h
		}
		if time.Now().After(deadline) {
			t.Fatalf("the home of %s never came", folder)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartFirstRunSetsUpThenServesTheFolder(t *testing.T) {
	w, dir := newFolderWorld(t)
	w.config(t, "idle_exit_minutes = 0\n")
	env := w.startEnv()
	url, stop, done := w.serve(t, env, "start", "--port", "0")
	if len(w.opened) != 1 || w.opened[0] != url {
		t.Errorf("opened %v", w.opened)
	}

	// The setup page, at /, on a fresh config.
	var s map[string]any
	if code := getJSON(t, url+"api/setup", &s); code != 200 || s["folder"] != "~/Videos" || s["agent"] != "claude" ||
		s["open_in"] != "herdr" {
		t.Fatalf("setup state: %d %v", code, s)
	}
	if code := getJSON(t, url+"api/home", nil); code != http.StatusServiceUnavailable {
		t.Errorf("api/home while setting up: %d", code)
	}
	// A second start opens the first one's page and exits.
	w.opened = nil
	if code := Main(context.Background(), []string{"start"}, env); code != ExitOK ||
		!strings.Contains(w.stdout.String(), "retake: already running at "+url) || len(w.opened) != 1 || w.opened[0] != url {
		t.Errorf("second start: %d %v %s", code, w.opened, w.stdout)
	}

	// Done: the same address becomes the folder's home.
	body, _ := json.Marshal(map[string]string{"folder": dir, "open_in": "background"}) // a Windows path has backslashes
	if code := postTest(t, url+"api/setup/done", string(body), nil); code != 200 {
		t.Fatalf("done: %d", code)
	}
	h := waitHome(t, url, dir)
	if len(h.Projects) != 2 {
		t.Errorf("projects %+v", h.Projects)
	}
	cfg := loadConfig(env)
	if cfg.Folder == nil || *cfg.Folder != "~/videos" || *cfg.DefaultAgent != "claude" || cfg.OpenAgentsIn != nil {
		t.Errorf("config: %+v", cfg)
	}

	// Settings shows the folder, and picking another one switches the home to it.
	var set map[string]any
	if code := getJSON(t, url+"api/settings", &set); code != 200 || set["folder"] != "~/videos" ||
		set["folder_editable"] != true || set["test"] != true {
		t.Errorf("settings: %d %v", code, set)
	}
	other := filepath.Join(w.root, "other")
	if err := os.MkdirAll(filepath.Join(other, "c", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "c", "v1", "c_v1.mp4"), []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := postTest(t, url+"api/settings", `{"folder":"~/other"}`, nil); code != 200 {
		t.Fatalf("settings folder: %d", code)
	}
	if h := waitHome(t, url, other); len(h.Projects) != 1 {
		t.Errorf("other's projects: %+v", h.Projects)
	}
	var tr worker.TryResult
	if code := postTest(t, url+"api/settings/test", `{"agent":"claude"}`, &tr); code != 200 || !tr.OK {
		t.Errorf("settings test: %d %+v", code, tr)
	}
	var fl map[string]any
	if code := getJSON(t, url+"api/settings/folders?path=~", &fl); code != 200 || fl["path"] != "~" {
		t.Errorf("settings folders: %d %v", code, fl)
	}

	state := filepath.Join(w.root, "state", "retake", "start.json")
	if _, err := os.Stat(state); err != nil {
		t.Errorf("no state file: %v", err)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Errorf("exit %d: %s", code, w.stderr)
	}
	if _, err := os.Stat(state); err == nil {
		t.Error("the state file outlived retake start")
	}
}

func TestStartWithAFolderAndAStaleState(t *testing.T) {
	w, dir := newFolderWorld(t)
	w.config(t, "idle_exit_minutes = 0\nfolder = "+tomlPath(dir)+"\n")
	env := w.startEnv()
	// A state file from a retake that's gone is ignored.
	state := filepath.Join(w.root, "state", "retake", "start.json")
	if err := writeState(state, startState{PID: 1, URL: "http://127.0.0.1:1/"}); err != nil {
		t.Fatal(err)
	}
	url, stop, done := w.serve(t, env, "start", "--port", "0", "--no-open")
	if h := waitHome(t, url, dir); len(h.Projects) != 2 {
		t.Errorf("home %+v", h)
	}
	if len(w.opened) != 0 {
		t.Errorf("--no-open opened %v", w.opened)
	}
	// Under retake open the folder is read-only in Settings; under start it's editable.
	var set map[string]any
	if getJSON(t, url+"api/settings", &set); set["folder_editable"] != true {
		t.Errorf("settings %v", set)
	}
	if code := postTest(t, url+"api/settings", `{"folder":"~/missing"}`, nil); code != 400 {
		t.Errorf("a missing folder: %d", code)
	}
	stop()
	wait(t, done)
}

func TestOpenFolderSettingsFolderIsReadOnly(t *testing.T) {
	w, dir := newFolderWorld(t)
	w.config(t, "idle_exit_minutes = 0\n")
	url, stop, done := w.serve(t, w.environment(nil), "open", dir, "--port", "0", "--no-open")
	var set map[string]any
	if code := getJSON(t, url+"api/settings", &set); code != 200 || set["folder"] != "~/videos" || set["folder_editable"] != false {
		t.Errorf("settings: %d %v", code, set)
	}
	if code := postTest(t, url+"api/settings", `{"folder":"~"}`, nil); code != 400 {
		t.Errorf("changing it: %d", code)
	}
	stop()
	wait(t, done)
}

func TestStartArguments(t *testing.T) {
	w := newWorld(t)
	if code := w.run("start", "~/Videos"); code != ExitUsage || !strings.Contains(w.stderr.String(), "retake open <folder>") {
		t.Errorf("a folder argument: %d %s", code, w.stderr)
	}
	if code := w.run("start", "--port", "x"); code != ExitUsage {
		t.Errorf("bad port: %d", code)
	}
	if code := w.run("autostart", "maybe"); code != ExitUsage {
		t.Errorf("autostart maybe: %d", code)
	}
}

func TestAutostartCommand(t *testing.T) {
	w := newWorld(t)
	env := w.environment(nil)
	env.GOOS = "linux"
	if code := Main(context.Background(), []string{"autostart", "on"}, env); code != ExitOK || w.stdout.String() != "on\n" {
		t.Fatalf("on: %d %q %s", code, w.stdout, w.stderr)
	}
	b, err := os.ReadFile(filepath.Join(w.env["XDG_CONFIG_HOME"], "autostart", "retake.desktop"))
	if err != nil || !strings.Contains(string(b), " start --app\n") {
		t.Errorf("entry: %v\n%s", err, b)
	}
	w.stdout.b.Reset()
	if code := Main(context.Background(), []string{"autostart", "off"}, env); code != ExitOK || w.stdout.String() != "off\n" {
		t.Errorf("off: %d %q", code, w.stdout)
	}
}

func TestMenuCommand(t *testing.T) {
	got := strings.Join(menuCommand("/Applications/Retake.app", 42, "http://127.0.0.1:8777/", "/Applications/Retake.app/Contents/MacOS/Retake"), " ")
	want := "/usr/bin/osascript -l JavaScript /Applications/Retake.app/Contents/Resources/menubar.js 42 " +
		"http://127.0.0.1:8777/ /Applications/Retake.app/Contents/MacOS/Retake"
	if got != want {
		t.Errorf("menu command:\n got %s\nwant %s", got, want)
	}
	if stop := menuBar(Env{GOOS: "linux"}, "http://x/"); stop != nil {
		t.Error("a menu bar off the Mac")
	}
}

func TestHandoffKeepsTheAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := newHandoff(ln)
	defer h.close()
	serve := func(name string) (net.Listener, chan error) {
		p := h.phase()
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(name))
		}), ReadHeaderTimeout: time.Second}
		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(p) }()
		return p, errc
	}
	get := func() string {
		c := http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 5 * time.Second}
		res, err := c.Get("http://" + ln.Addr().String() + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		b := make([]byte, 16)
		n, _ := res.Body.Read(b)
		return string(b[:n])
	}
	p1, e1 := serve("one")
	if got := get(); got != "one" {
		t.Errorf("first: %q", got)
	}
	_ = p1.Close()
	if err := <-e1; err == nil {
		t.Error("the first server didn't stop")
	}
	_, _ = serve("two")
	if got := get(); got != "two" {
		t.Errorf("second: %q", got)
	}
}
