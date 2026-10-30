//go:build unix

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
)

// fakeCloudflared writes a stand-in for cloudflared: it records its arguments and pid, prints a quick tunnel's
// banner with a made-up hostname, and sleeps until it is stopped. tail replaces the sleep (to fail, say). Nothing
// here reaches Cloudflare.
func fakeCloudflared(t *testing.T, host, tail string) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	if tail == "" {
		tail = "exec sleep 60"
	}
	script := "#!/bin/sh\n" +
		`printf '%s\n' "$*" > "` + dir + `/args"` + "\n" +
		`echo $$ > "` + dir + `/pid"` + "\n" +
		`echo "INF Requesting new quick Tunnel on trycloudflare.com..." >&2` + "\n"
	if host != "" {
		script += `echo "INF |  https://` + host + `   |" >&2` + "\n"
	}
	script += tail + "\n"
	bin = filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, dir
}

func pidGone(t *testing.T, dir string) bool {
	t.Helper()
	return syscall.Kill(tunnelPID(t, dir), 0) != nil
}

// runShare starts `retake share` in the background and waits for its link.
func runShare(t *testing.T, env Env, args ...string) (string, context.CancelFunc, <-chan int) {
	t.Helper()
	ready := make(chan string, 1)
	env.Ready = func(u string) { ready <- u }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Main(ctx, append([]string{"share"}, args...), env) }()
	select {
	case u := <-ready:
		t.Cleanup(cancel)
		return u, cancel, done
	case code := <-done:
		cancel()
		t.Fatalf("share exited with %d\nstdout: %s\nstderr: %s", code, env.Stdout, env.Stderr)
	case <-time.After(15 * time.Second):
		cancel()
		t.Fatal("share didn't print a link")
	}
	return "", nil, nil
}

// reply is a response's status and cookies.
type reply struct {
	StatusCode int
	cookies    []*http.Cookie
}

func (r reply) Cookies() []*http.Cookie { return r.cookies }

// through sends a request to the local server as if it came through the tunnel: the tunnel's Host header.
func through(t *testing.T, port int, host, path string, hdr map[string]string) reply {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return reply{res.StatusCode, res.Cookies()}
}

func storeOf(t *testing.T, w *world) *project.Store {
	t.Helper()
	st, err := project.Open(w.root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

var serverLine = regexp.MustCompile(`server http://127\.0\.0\.1:(\d+)/ \((started for this share|already running)\)`)

func TestShareStartsAServerAndATunnel(t *testing.T) {
	w := newWorld(t)
	bin, dir := fakeCloudflared(t, "fake-words-here.trycloudflare.com", "")
	env := w.environment(nil)
	env.SharePing = 20 * time.Millisecond
	link, cancel, done := runShare(t, env, w.video, "--port", "0", "--tunnel-bin", bin, "--label", "alex")

	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != "fake-words-here.trycloudflare.com" ||
		!regexp.MustCompile(`^/s/rt_[A-Za-z0-9_-]{43}$`).MatchString(u.Path) {
		t.Fatalf("link %q", link)
	}
	tok := strings.TrimPrefix(u.Path, "/s/")
	out := w.stdout.String()
	m := serverLine.FindStringSubmatch(out)
	if m == nil || m[2] != "started for this share" {
		t.Fatalf("stdout: %s", out)
	}
	port, _ := strconv.Atoi(m[1])
	for _, want := range []string{"sharing " + w.video + " with a reviewer link for \"alex\", until ", "link   " + link,
		"\x1b[30;107m", "token  #1; end it early with: retake share revoke", "Ctrl+C stops sharing"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	b, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.TrimSpace(string(b)); got != fmt.Sprintf("tunnel --no-autoupdate --url http://127.0.0.1:%d", port) {
		t.Errorf("cloudflared args %q", got)
	}

	// Through the tunnel's hostname: nothing without the link; the link sets the cookie; the cookie is a reviewer.
	if res := through(t, port, u.Host, "/api/info", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", res.StatusCode)
	}
	res := through(t, port, u.Host, u.Path, nil)
	cookies := res.Cookies()
	if res.StatusCode != http.StatusOK || len(cookies) != 1 || cookies[0].Value != tok {
		t.Fatalf("share link: %d %v", res.StatusCode, cookies)
	}
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/info", port), nil)
	req.Host = u.Host
	req.AddCookie(cookies[0])
	ir, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var info struct{ Role string }
	_ = json.NewDecoder(ir.Body).Decode(&info)
	_ = ir.Body.Close()
	if info.Role != "reviewer" {
		t.Errorf("role %q", info.Role)
	}
	st := storeOf(t, w)
	tokens, _ := st.Tokens()
	if len(tokens) != 1 || tokens[0].Label != "alex" || tokens[0].Role != "reviewer" || tokens[0].Expires == "" {
		t.Errorf("tokens %+v", tokens)
	}
	if exp, _ := tokens[0].ExpiresAt(); time.Until(exp) < 23*time.Hour || time.Until(exp) > 24*time.Hour {
		t.Errorf("expires %s", tokens[0].Expires)
	}

	// Ctrl+C: the tunnel stops, the host is forgotten, the server it started stops; the token stays.
	cancel()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
	if !pidGone(t, dir) {
		t.Error("cloudflared is still running")
	}
	if hosts, _ := st.Hosts(); len(hosts) != 0 {
		t.Errorf("hosts after the share: %+v", hosts)
	}
	if res, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port)); err == nil {
		_ = res.Body.Close()
		t.Error("the share's server still answers")
	}
	if !strings.Contains(w.stdout.String(), "stopped sharing. Token #1 is still valid (until ") {
		t.Errorf("stdout: %s", w.stdout)
	}
}

// Attached to a running `retake open`: the share keeps the server from idling out, and leaves it running.
func TestShareAttachesAndKeepsTheServerAlive(t *testing.T) {
	w := newWorld(t)
	openEnv := w.environment(nil)
	// 0.003 min = 180 ms after the last request.
	openURL, stopOpen, openDone := w.serve(t, openEnv, "open", w.video, "--port", "0", "--no-open", "--idle-exit", "0.003")
	defer stopOpen()
	port, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(openURL, "http://127.0.0.1:"), "/"))

	bin, dir := fakeCloudflared(t, "attached-share.trycloudflare.com", "")
	shareOut := &buffer{}
	env := w.environment(nil)
	env.Stdout, env.SharePing = shareOut, 20*time.Millisecond
	link, cancel, done := runShare(t, env, w.video, "--port", strconv.Itoa(port), "--tunnel-bin", bin, "--json",
		"--role", "viewer", "--expires", "2h")
	var line shareJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(shareOut.String())), &line); err != nil {
		t.Fatalf("--json: %v in %q", err, shareOut)
	}
	if !line.Attached || line.Port != port || line.URL != link || line.Role != "viewer" || line.Host !=
		"attached-share.trycloudflare.com" || line.ID != 1 {
		t.Errorf("--json %+v", line)
	}
	// The open server answers the tunnel's host now (it reads the hosts table) with the link's token.
	u, _ := url.Parse(link)
	if res := through(t, port, u.Host, u.Path, nil); res.StatusCode != http.StatusOK {
		t.Errorf("link through the attached server: %d", res.StatusCode)
	}
	// Well past the idle limit, the server still runs.
	time.Sleep(600 * time.Millisecond)
	select {
	case code := <-openDone:
		t.Fatalf("the server idled out during the share (%d)", code)
	default:
	}
	cancel()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("share exit %d: %s", code, w.stderr)
	}
	if !pidGone(t, dir) {
		t.Error("cloudflared is still running")
	}
	if res := through(t, port, u.Host, "/api/ping", nil); res.StatusCode != http.StatusForbidden {
		t.Errorf("the tunnel host is still answered after the share: %d", res.StatusCode)
	}
	// Without the share's pings, the server idles out as before.
	if code := wait(t, openDone); code != ExitOK || !strings.Contains(w.stdout.String(), "page gone, exiting") {
		t.Errorf("open: %d %s", code, w.stdout)
	}
}

func tunnelPID(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// Ctrl+C in a terminal interrupts the whole process group: cloudflared may exit before the share sees its own
// interrupt. That is a normal stop. A tunnel that dies by itself is not.
func TestShareTunnelStops(t *testing.T) {
	w := newWorld(t)
	bin, dir := fakeCloudflared(t, "ctrl-c-race.trycloudflare.com", "")
	_, cancel, done := runShare(t, w.environment(nil), w.video, "--port", "0", "--tunnel-bin", bin, "--no-qr")
	if err := syscall.Kill(tunnelPID(t, dir), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	if code := wait(t, done); code != ExitOK || strings.Contains(w.stderr.String(), "tunnel stopped") {
		t.Errorf("Ctrl+C that reached the tunnel first: %d %s", code, w.stderr)
	}

	w = newWorld(t)
	bin, dir = fakeCloudflared(t, "dies-alone.trycloudflare.com", "")
	_, _, done = runShare(t, w.environment(nil), w.video, "--port", "0", "--tunnel-bin", bin, "--no-qr")
	if err := syscall.Kill(tunnelPID(t, dir), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := wait(t, done); code != ExitError || !strings.Contains(w.stderr.String(), "the tunnel stopped") {
		t.Errorf("a tunnel that died: %d %s", code, w.stderr)
	}
	if hosts, _ := storeOf(t, w).Hosts(); len(hosts) != 0 {
		t.Errorf("the dead tunnel's host is still allowed: %+v", hosts)
	}
}

func TestShareTunnelFailure(t *testing.T) {
	w := newWorld(t)
	bin, _ := fakeCloudflared(t, "", `echo "ERR failed to request quick Tunnel: login required" >&2; exit 1`)
	code := Main(context.Background(), []string{"share", w.video, "--port", "0", "--tunnel-bin", bin}, w.environment(nil))
	if code != ExitError || !strings.Contains(w.stderr.String(), "login required") {
		t.Errorf("exit %d, stderr %s", code, w.stderr)
	}
	st := storeOf(t, w)
	if tokens, _ := st.Tokens(); len(tokens) != 0 {
		t.Errorf("a failed share made a token: %+v", tokens)
	}
	if !strings.Contains(w.stderr.String(), "stopped before it printed a public URL") {
		t.Errorf("stderr: %s", w.stderr)
	}
	// A missing binary says how to fix it.
	w2 := newWorld(t)
	code = Main(context.Background(), []string{"share", w2.video, "--port", "0", "--tunnel-bin",
		filepath.Join(t.TempDir(), "nope")}, w2.environment(nil))
	if code != ExitError || !strings.Contains(w2.stderr.String(), "not found") {
		t.Errorf("missing binary: %d %s", code, w2.stderr)
	}
}

// --tunnel none: a reverse proxy that is already there; the config file names no binary because none runs.
func TestShareWithoutATunnel(t *testing.T) {
	w := newWorld(t)
	env := w.environment(nil)
	link, cancel, done := runShare(t, env, w.video, "--port", "0", "--tunnel", "none", "--host", "Review.Example.com",
		"--no-qr", "--role", "approver")
	if !strings.HasPrefix(link, "https://review.example.com/s/rt_") {
		t.Errorf("link %q", link)
	}
	if strings.Contains(w.stdout.String(), "\x1b[") {
		t.Error("--no-qr printed a QR code")
	}
	st := storeOf(t, w)
	hosts, _ := st.Hosts()
	if len(hosts) != 1 || hosts[0].Host != "review.example.com" || hosts[0].Tunnel != "none" || hosts[0].PID != os.Getpid() {
		t.Errorf("hosts %+v", hosts)
	}
	cancel()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

func TestShareArguments(t *testing.T) {
	w := newWorld(t)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--role", "agent"}, "token create"},
		{[]string{"--role", "owner"}, "not a role"},
		{[]string{"--tunnel", "none"}, "needs --host"},
		{[]string{"--tunnel", "none", "--host", "a/b"}, "bare hostname"},
		{[]string{"--host", "x.example"}, "--host is for --tunnel none"},
		{[]string{"--funnel"}, "--funnel is for --tunnel tailscale"},
		{[]string{"--tunnel", "ngrok"}, "use cloudflare, tailscale or none"},
		{[]string{"--expires", "soon"}, "--expires"},
		{[]string{"--expires", "-1h"}, "--expires"},
		{[]string{"--port", "x"}, "not a port"},
	} {
		w.stderr.b.Reset()
		if code := w.run(append([]string{"share", w.video}, c.args...)...); code != ExitUsage ||
			!strings.Contains(w.stderr.String(), c.want) {
			t.Errorf("%v: %d %s", c.args, code, w.stderr)
		}
	}
	if code := w.run("share", filepath.Join(w.root, "missing.mp4"), "--port", "0"); code != ExitUsage {
		t.Errorf("missing video: %d", code)
	}
}

func TestTokenCommands(t *testing.T) {
	w := newWorld(t)
	w.config(t, "port = 8901\n")
	if code := w.run("token", "create", w.video, "--role", "agent", "--label", "ci"); code != ExitOK {
		t.Fatalf("create: %d %s", code, w.stderr)
	}
	out := w.stdout.String()
	tok := regexp.MustCompile(`rt_[A-Za-z0-9_-]{43}`).FindString(out)
	for _, want := range []string{
		"token #1 (agent for \"ci\", until ",
		"claude mcp add --transport http retake http://127.0.0.1:8901/mcp --header \"Authorization: Bearer " + tok + "\"",
		"export RETAKE_TOKEN=" + tok,
		"codex mcp add retake --url http://127.0.0.1:8901/mcp --bearer-token-env-var RETAKE_TOKEN",
		"shown only this once",
	} {
		if tok == "" || !strings.Contains(out, want) {
			t.Errorf("token create lacks %q:\n%s", want, out)
		}
	}

	w.stdout.b.Reset()
	if code := w.run("token", "create", w.video, "--role", "agent", "--host", "Share.Example", "--expires", "never",
		"--json"); code != ExitOK {
		t.Fatalf("create --json: %d %s", code, w.stderr)
	}
	var created tokenJSON
	if err := json.Unmarshal([]byte(w.stdout.String()), &created); err != nil || created.ID != 2 ||
		created.URL != "https://share.example/mcp" || created.Expires != "" || !strings.HasPrefix(created.Secret, "rt_") {
		t.Errorf("--json: %v %+v", err, created)
	}
	w.stdout.b.Reset()
	if code := w.run("token", "create", w.video, "--role", "viewer", "--host", "share.example", "--json"); code != ExitOK {
		t.Fatal(code)
	}
	var viewer tokenJSON
	_ = json.Unmarshal([]byte(w.stdout.String()), &viewer)
	if viewer.URL != "https://share.example/s/"+viewer.Secret {
		t.Errorf("viewer link %q", viewer.URL)
	}

	// The store has hashes only.
	st := storeOf(t, w)
	if _, err := st.TokenByHash(tok); err == nil {
		t.Error("a token was stored in the clear")
	}

	if code := w.run("share", "revoke", w.video, "2"); code != ExitOK || !strings.Contains(w.stdout.String(), "revoked #2") {
		t.Errorf("revoke: %d %s", code, w.stdout)
	}
	w.stdout.b.Reset()
	if code := w.run("share", "list", w.video); code != ExitOK {
		t.Fatal(code)
	}
	list := w.stdout.String()
	if !regexp.MustCompile(`#1 +agent +ci +until `).MatchString(list) || !regexp.MustCompile(`#2 +agent +- +revoked`).MatchString(list) ||
		!regexp.MustCompile(`#3 +viewer`).MatchString(list) {
		t.Errorf("share list:\n%s", list)
	}
	w.stdout.b.Reset()
	if code := w.run("token", "list", w.video, "--json"); code != ExitOK {
		t.Fatal(code)
	}
	var listed struct {
		Tokens []tokenView    `json:"tokens"`
		Hosts  []project.Host `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(w.stdout.String()), &listed); err != nil || len(listed.Tokens) != 3 ||
		listed.Tokens[1].State != "revoked" || listed.Tokens[0].State != "active" {
		t.Errorf("token list --json: %v %s", err, w.stdout)
	}

	for _, c := range []struct {
		args []string
		code int
	}{
		{[]string{"token", "create", w.video}, ExitUsage},
		{[]string{"token", "create", w.video, "--role", "boss"}, ExitUsage},
		{[]string{"token", "create", w.video, "--role", "agent", "--host", "x/y"}, ExitUsage},
		{[]string{"token", "revoke", w.video, "99"}, ExitError},
		{[]string{"token", "revoke", w.video, "abc"}, ExitUsage},
		{[]string{"token", "revoke", w.video}, ExitUsage},
		{[]string{"token"}, ExitUsage},
		{[]string{"token", "list", filepath.Join(w.root, "nope.mp4")}, ExitUsage},
	} {
		if code := w.run(c.args...); code != c.code {
			t.Errorf("%v: %d, want %d (%s)", c.args, code, c.code, w.stderr)
		}
	}
}

func TestParseExpiry(t *testing.T) {
	for in, want := range map[string]time.Duration{"24h": 24 * time.Hour, "90m": 90 * time.Minute, "7d": 7 * 24 * time.Hour,
		"0.5d": 12 * time.Hour, "0": 0, "never": 0, "NEVER": 0} {
		if got, err := parseExpiry(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", "-1h", "0d", "-2d", "99999d"} {
		if _, err := parseExpiry(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}
