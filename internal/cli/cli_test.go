package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/store"
)

// Ports reserved for these tests (the repo's test port range is 8811-8819).
const (
	portBusy  = 8817
	portAfter = 8818
	portLast  = 8819
)

type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d, w, h := 10.0, 1920, 1080
	return probe.Info{Duration: &d, FPS: 30, Width: &w, Height: &h}
}

type buffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type world struct {
	root, video string
	env         map[string]string
	stdout      *buffer
	stderr      *buffer
	opened      []string
	mu          sync.Mutex
}

// newWorld makes a v5/v6/v7 family (fake video bytes) and an empty config folder.
func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{root: t.TempDir(), stdout: &buffer{}, stderr: &buffer{}}
	for _, rel := range []string{"v5/clip_v5.mp4", "v6/clip_v6.mp4", "v7/clip_v7.mp4"} {
		p := filepath.Join(w.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("video "+rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.video = filepath.Join(w.root, "v6", "clip_v6.mp4")
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(w.root, "config"), "HOME": w.root, "USER": "osuser"}
	return w
}

func (w *world) config(t *testing.T, toml string) {
	t.Helper()
	p := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *world) environment(ready func(string)) Env {
	return Env{
		Stdout: w.stdout, Stderr: w.stderr, Getenv: func(k string) string { return w.env[k] },
		OpenURL: func(u string) error {
			w.mu.Lock()
			w.opened = append(w.opened, u)
			w.mu.Unlock()
			return nil
		},
		PortTries: 3, Ready: ready, Prober: fakeProber{}, CheckEvery: 2 * time.Millisecond,
	}
}

// run runs Main to completion (for commands that don't serve).
func (w *world) run(args ...string) int {
	return Main(context.Background(), args, w.environment(nil))
}

// serve starts `open` in the background and returns its URL, a stop function and the exit code channel.
func (w *world) serve(t *testing.T, env Env, args ...string) (string, context.CancelFunc, <-chan int) {
	t.Helper()
	ready := make(chan string, 1)
	env.Ready = func(u string) { ready <- u }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Main(ctx, args, env) }()
	select {
	case u := <-ready:
		t.Cleanup(cancel)
		return u, cancel, done
	case code := <-done:
		cancel()
		t.Fatalf("open exited with %d\nstdout: %s\nstderr: %s", code, w.stdout, w.stderr)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("server didn't start")
	}
	return "", nil, nil
}

func wait(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("server didn't stop")
	}
	return -1
}

func TestParseOpen(t *testing.T) {
	p := func(n int) *int { return &n }
	f := func(x float64) *float64 { return &x }
	for _, tc := range []struct {
		args    []string
		want    openArgs
		wantErr string
	}{
		{args: []string{"a.mp4"}, want: openArgs{video: "a.mp4"}},
		{args: []string{"a.mp4", "--no-open", "--port", "8811", "--idle-exit", "0", "--verbose", "--ui-dir", "ui"},
			want: openArgs{video: "a.mp4", noOpen: true, port: p(8811), idleExit: f(0), verbose: true, uiDir: "ui"}},
		{args: []string{"--port=8812", "--idle-exit=2.5", "a.mp4"}, want: openArgs{video: "a.mp4", port: p(8812), idleExit: f(2.5)}},
		{args: []string{"--no-open", "a.mp4", "--port", "8813", "--idle-exit", "0"}, // flags on both sides
			want: openArgs{video: "a.mp4", noOpen: true, port: p(8813), idleExit: f(0)}},
		{args: []string{"a.mp4", "--versions", "b.mp4", "c.mp4", "--no-open"},
			want: openArgs{video: "a.mp4", versions: []string{"b.mp4", "c.mp4"}, noOpen: true}},
		{args: []string{"--versions=b.mp4", "c.mp4", "--", "a.mp4"}, want: openArgs{video: "a.mp4", versions: []string{"b.mp4", "c.mp4"}}},
		{args: []string{"--", "-weird.mp4"}, want: openArgs{video: "-weird.mp4"}},
		{args: []string{"--help"}, want: openArgs{help: true}},
		{args: []string{}, wantErr: "missing the video"},
		{args: []string{"a.mp4", "b.mp4"}, wantErr: "unexpected argument"},
		{args: []string{"a.mp4", "--versions"}, wantErr: "at least one"},
		{args: []string{"a.mp4", "--port"}, wantErr: "needs a value"},
		{args: []string{"a.mp4", "--port", "http"}, wantErr: "not a port"},
		{args: []string{"a.mp4", "--port", "70000"}, wantErr: "not a port"},
		{args: []string{"a.mp4", "--idle-exit", "soon"}, wantErr: "minutes"},
		{args: []string{"a.mp4", "--no-open=yes"}, wantErr: "no value"},
		{args: []string{"a.mp4", "--bogus"}, wantErr: "unknown option"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got, err := parseOpen(tc.args)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestCommandsAndExitCodes(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{nil, 0, "usage:", ""},
		{[]string{"help"}, 0, "retake open <video>", ""},
		{[]string{"version"}, 0, "retake ", ""},
		{[]string{"frobnicate"}, 2, "", `unknown command "frobnicate"`},
		{[]string{"open"}, 2, "", "missing the video file"},
		{[]string{"open", "--help"}, 0, "usage:", ""},
		{[]string{"open", filepath.Join(w.root, "nope.mp4"), "--no-open"}, 2, "", "retake: no such file: " + filepath.Join(w.root, "nope.mp4")},
		{[]string{"open", w.video, "--versions", filepath.Join(w.root, "gone.mp4"), "--no-open"}, 2, "", "no such file"},
		{[]string{"open", w.video, "--ui-dir", filepath.Join(w.root, "no-ui"), "--no-open"}, 2, "", "no such folder"},
		{[]string{"open", w.video, "--ui-dir", w.video, "--no-open"}, 2, "", "no such folder"},
		{[]string{"sessions"}, 2, "", "missing the video file"},
		{[]string{"sessions", filepath.Join(w.root, "nope.mp4")}, 2, "", "no such file"},
		{[]string{"sessions", w.video, "--bogus"}, 2, "", "unknown option"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			w.stdout.b.Reset()
			w.stderr.b.Reset()
			if code := w.run(tc.args...); code != tc.code {
				t.Errorf("exit %d, want %d\nstderr: %s", code, tc.code, w.stderr)
			}
			if !strings.Contains(w.stdout.String(), tc.stdout) {
				t.Errorf("stdout = %q, want %q", w.stdout, tc.stdout)
			}
			if !strings.Contains(w.stderr.String(), tc.stderr) {
				t.Errorf("stderr = %q, want %q", w.stderr, tc.stderr)
			}
		})
	}
}

func TestOpenServesUntilStopped(t *testing.T) {
	w := newWorld(t)
	w.config(t, "name = \"Tester\"\nidle_exit_minutes = 15\n")
	url, stop, done := w.serve(t, w.environment(nil), "open", w.video, "--port", "0")

	res, err := http.Post(url+"api/session/2026-10-29_20-48-01/data?v=v6", "application/json", strings.NewReader(`{"session":{"format":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("save: %d", res.StatusCode)
	}
	b, err := os.ReadFile(filepath.Join(store.TakeDir(w.video, "2026-10-29_20-48-01"), "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Author struct{ Name string } `json:"author"`
	}
	if err := json.Unmarshal(b, &meta); err != nil || meta.Author.Name != "Tester" {
		t.Errorf("author from config: %s", b)
	}

	stop()
	if code := wait(t, done); code != 0 {
		t.Errorf("exit %d", code)
	}
	port := strings.TrimSuffix(strings.TrimPrefix(url, "http://127.0.0.1:"), "/")
	want := strings.Join([]string{
		"retake: clip_v6.mp4  (1920x1080, 30.0 fps, 0:10.0)",
		"  versions v5 [v6] v7",
		"  page   http://127.0.0.1:" + port + "/",
		"  saves  " + store.ReviewDir(w.video) + string(filepath.Separator) + "<take>" + string(filepath.Separator) +
			"   (other versions save next to their own file)",
		"  Ctrl+C stops the server, auto-exits 15 min after the page is closed",
		"",
		"retake: stopped",
		"",
	}, "\n")
	if got := w.stdout.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
	if len(w.opened) != 1 || w.opened[0] != url {
		t.Errorf("opener calls = %v", w.opened)
	}
}

// The e2e suite (and the legacy argparse CLI) put the flags after the video; flags before it work too.
func TestOpenFlagsAfterOrBeforeTheVideo(t *testing.T) {
	for _, args := range [][]string{
		{"open", "VIDEO", "--no-open", "--port", "0", "--idle-exit", "0"},
		{"open", "--no-open", "--port", "0", "--idle-exit", "0", "VIDEO"},
		{"open", "--no-open", "VIDEO", "--port=0", "--idle-exit=0", "--verbose"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			w := newWorld(t)
			for i, a := range args {
				if a == "VIDEO" {
					args[i] = w.video
				}
			}
			url, stop, done := w.serve(t, w.environment(nil), args...)
			res, err := http.Get(url + "api/ping")
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			stop()
			if code := wait(t, done); code != 0 {
				t.Errorf("exit %d", code)
			}
			if len(w.opened) != 0 || !strings.Contains(w.stdout.String(), "  Ctrl+C stops the server\n") {
				t.Errorf("--no-open/--idle-exit 0 not applied: opened %v\n%s", w.opened, w.stdout)
			}
		})
	}
}

func TestOpenFlagsOverrideConfig(t *testing.T) {
	w := newWorld(t)
	w.config(t, "open_browser = true\nidle_exit_minutes = 3\nbogus = 1\n")
	url, stop, done := w.serve(t, w.environment(nil), "open", w.video, "--port", "0", "--no-open", "--idle-exit", "0")
	stop()
	wait(t, done)
	if len(w.opened) != 0 {
		t.Errorf("--no-open still opened %v", w.opened)
	}
	if !strings.Contains(w.stdout.String(), "  Ctrl+C stops the server\n") {
		t.Errorf("--idle-exit 0 should drop the auto-exit note:\n%s", w.stdout)
	}
	if !strings.Contains(w.stderr.String(), `unknown key "bogus"`) {
		t.Errorf("stderr = %q", w.stderr)
	}
	if url == "" {
		t.Error("no url")
	}

	// Without a config name, the author is the OS user.
	w2 := newWorld(t)
	w2.config(t, "open_browser = false\n")
	url2, stop2, done2 := w2.serve(t, w2.environment(nil), "open", w2.video, "--port", "0")
	res, err := http.Post(url2+"api/session/2026-10-29_20-48-01/data", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	stop2()
	wait(t, done2)
	if len(w2.opened) != 0 {
		t.Errorf("open_browser = false still opened %v", w2.opened)
	}
	b, _ := os.ReadFile(filepath.Join(store.TakeDir(w2.video, "2026-10-29_20-48-01"), "session.json"))
	if !bytes.Contains(b, []byte(`"author": {`)) || bytes.Contains(b, []byte(`"name": ""`)) {
		t.Errorf("no OS-user author: %s", b)
	}
}

func TestBadConfigIsIgnored(t *testing.T) {
	w := newWorld(t)
	w.config(t, "port = \"high\"\n")
	_, stop, done := w.serve(t, w.environment(nil), "open", w.video, "--port", "0", "--no-open")
	stop()
	if code := wait(t, done); code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.Contains(w.stderr.String(), "(ignored)") {
		t.Errorf("stderr = %q", w.stderr)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestOpenIdleExit(t *testing.T) {
	w := newWorld(t)
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	env := w.environment(nil)
	env.Clock = c
	url, _, done := w.serve(t, env, "open", w.video, "--port", "0", "--no-open", "--idle-exit", "1")
	c.Add(time.Hour) // no page yet: the timer hasn't started
	time.Sleep(30 * time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("exited (%d) before the page ever connected", code)
	default:
	}
	res, err := http.Get(url + "api/ping")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	c.Add(61 * time.Second)
	if code := wait(t, done); code != 0 {
		t.Errorf("exit %d", code)
	}
	if !strings.HasSuffix(w.stdout.String(), "retake: page gone, exiting\n") {
		t.Errorf("stdout = %q", w.stdout)
	}
}

// occupy binds port on 127.0.0.1, or skips the test if something else already holds it.
func occupy(t *testing.T, port int) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Skipf("port %d is in use by something else: %v", port, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

func free(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func TestPorts(t *testing.T) {
	occupy(t, portBusy)
	if !free(portAfter) {
		t.Skipf("port %d is in use by something else", portAfter)
	}

	t.Run("busy explicit port exits 2", func(t *testing.T) {
		w := newWorld(t)
		if code := w.run("open", w.video, "--port", strconv.Itoa(portBusy), "--no-open"); code != 2 {
			t.Errorf("exit %d", code)
		}
		if got := w.stderr.String(); got != fmt.Sprintf("retake: port %d is busy\n", portBusy) {
			t.Errorf("stderr = %q", got)
		}
	})

	t.Run("busy default port falls back to the next", func(t *testing.T) {
		w := newWorld(t)
		w.config(t, fmt.Sprintf("port = %d\n", portBusy))
		url, stop, done := w.serve(t, w.environment(nil), "open", w.video, "--no-open")
		stop()
		wait(t, done)
		if want := fmt.Sprintf("http://127.0.0.1:%d/", portAfter); url != want {
			t.Errorf("url = %s, want %s", url, want)
		}
	})

	t.Run("no free port exits 2", func(t *testing.T) {
		occupy(t, portLast)
		ln, msg := listen(portLast, false, 1)
		if ln != nil {
			_ = ln.Close()
			t.Fatal("listened on a busy port")
		}
		if msg != "no free port" {
			t.Errorf("msg = %q", msg)
		}
		w := newWorld(t)
		w.config(t, fmt.Sprintf("port = %d\n", portLast))
		env := w.environment(nil)
		env.PortTries = 1
		if code := Main(context.Background(), []string{"open", w.video, "--no-open"}, env); code != 2 {
			t.Errorf("exit %d", code)
		}
		if got := w.stderr.String(); got != "retake: no free port\n" {
			t.Errorf("stderr = %q", got)
		}
	})
}

func TestSessionsCmd(t *testing.T) {
	w := newWorld(t)
	if code := w.run("sessions", w.video); code != 0 || w.stdout.String() != "no takes in "+store.ReviewDir(w.video)+"\n" {
		t.Errorf("empty: %d %q", code, w.stdout)
	}
	one := store.TakeDir(w.video, "2026-01-01_10-00-00")
	two := store.TakeDir(w.video, "2026-01-02_10-00-00")
	for p, s := range map[string]string{
		filepath.Join(one, "segments.json"): `[{"t_start":1,"t_end":4.5}]`,
		filepath.Join(one, "strokes.json"):  `[{},{}]`,
		filepath.Join(one, "notes.md"):      "x",
		filepath.Join(two, "session.json"):  `{"saved":"later"}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.stdout.b.Reset()
	if code := w.run("sessions", w.video); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := one + "  recorded 0:03.5  1 segment  2 strokes  notes.md\n" +
		two + "  recorded ?  0 segments  0 strokes  no notes yet\n"
	if got := w.stdout.String(); got != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", got, want)
	}
	w.stdout.b.Reset()
	if code := w.run("sessions", "--json", w.video); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(w.stdout.String()), &rows); err != nil {
		t.Fatalf("--json output: %v\n%s", err, w.stdout)
	}
	if len(rows) != 2 || rows[0]["id"] != "2026-01-01_10-00-00" || rows[0]["narration"] != 3.5 ||
		rows[0]["notes"] != true || rows[1]["saved"] != "later" || rows[1]["narration"] != nil {
		t.Errorf("rows = %v", rows)
	}

	// The takes outlive the video.
	if err := os.Remove(w.video); err != nil {
		t.Fatal(err)
	}
	w.stdout.b.Reset()
	if code := w.run("sessions", w.video); code != 0 || !strings.Contains(w.stdout.String(), "notes.md") {
		t.Errorf("after the video was deleted: %d %q", code, w.stdout)
	}
}

func TestFmtDur(t *testing.T) {
	// Expected values from the legacy Python fmt_dur.
	for _, tc := range []struct {
		t    float64
		ok   bool
		want string
	}{
		{40, true, "0:40.0"}, {4.75, true, "0:04.8"}, {65.25, true, "1:05.2"}, {3725.5, true, "1:02:05.5"},
		{0, true, "?"}, {12, false, "?"}, {59.96, true, "0:60.0"},
	} {
		if got := fmtDur(tc.t, tc.ok); got != tc.want {
			t.Errorf("fmtDur(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestIdleDuration(t *testing.T) {
	for _, tc := range []struct {
		min  float64
		want time.Duration
	}{{15, 15 * time.Minute}, {0.5, 30 * time.Second}, {0, 0}, {-1, 0}, {1e9, 0}} {
		if got := idleDuration(tc.min); got != tc.want {
			t.Errorf("idleDuration(%v) = %v, want %v", tc.min, got, tc.want)
		}
	}
}
