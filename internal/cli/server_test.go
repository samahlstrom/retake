package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/tus"
)

// Reset empties the buffer.
func (b *buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b.Reset()
}

// runJSON runs a command with --json and decodes its output.
func (w *world) runJSON(t *testing.T, out any, args ...string) {
	t.Helper()
	w.stdout.Reset()
	if code := w.run(append(args, "--json")...); code != ExitOK {
		t.Fatalf("%v: exit %d\n%s%s", args, code, w.stdout, w.stderr)
	}
	if err := json.Unmarshal([]byte(w.stdout.String()), out); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, w.stdout)
	}
}

func writeVideo(t *testing.T, name string, size int, seed byte) string {
	t.Helper()
	b := make([]byte, size)
	for i := range b {
		b[i] = byte(i*13) ^ seed
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The team server from the command line: the admin commands, retake serve, remotes and push --remote, with an
// interrupted upload resumed by the next run.
func TestServeAdminRemotePush(t *testing.T) {
	w := newWorld(t)
	data := filepath.Join(w.root, "data")
	w.env["XDG_STATE_HOME"] = filepath.Join(w.root, "state")
	w.env["RETAKE_PUBLIC_URL"] = "https://review.example.com" // the admin commands make links for it

	var added struct {
		Link string `json:"link"`
		User struct {
			Email string `json:"email"`
			Admin bool   `json:"admin"`
		} `json:"user"`
	}
	w.runJSON(t, &added, "user", "add", "Ana@Example.com", "--name", "Ana", "--admin", "--data", data,
		"--public-url", "https://review.example.com")
	if added.User.Email != "ana@example.com" || !added.User.Admin ||
		!strings.HasPrefix(added.Link, "https://review.example.com/signin/rs_") {
		t.Fatalf("user add: %+v", added)
	}
	if code := w.run("user", "add", "ana@example.com", "--data", data); code != ExitError ||
		!strings.Contains(w.stderr.String(), "retake user link") {
		t.Fatalf("a second add: %d %s", code, w.stderr)
	}
	var proj struct {
		Version struct {
			Label string `json:"label"`
			Path  string `json:"path"`
		} `json:"version"`
	}
	w.runJSON(t, &proj, "project", "add", "demo", "--video", w.video, "--data", data)
	if proj.Version.Label != "v1" || proj.Version.Path != filepath.Join(data, "projects", "demo", "media",
		"demo_v1.mp4") {
		t.Fatalf("project add: %+v", proj)
	}
	if code := w.run("project", "add", "Bad Name", "--data", data); code == ExitOK {
		t.Fatal("a bad project name")
	}
	w.runJSON(t, &struct{}{}, "user", "add", "bo@example.com", "--data", data)
	if code := w.run("project", "member", "add", "demo", "bo@example.com", "--role", "approver", "--data",
		data); code != ExitOK || !strings.Contains(w.stdout.String(), "is an approver of demo") {
		t.Fatalf("member add: %d %s %s", code, w.stdout, w.stderr)
	}
	var members struct {
		Members []struct {
			Role string `json:"role"`
		} `json:"members"`
	}
	w.runJSON(t, &members, "project", "member", "list", "demo", "--data", data)
	if len(members.Members) != 1 || members.Members[0].Role != "approver" {
		t.Fatalf("members: %+v", members)
	}
	var tok struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	w.runJSON(t, &tok, "project", "token", "create", "demo", "--role", "agent", "--label", "editor", "--data", data)
	if !strings.HasPrefix(tok.Token, "rt_") || tok.URL != "https://review.example.com/p/demo" {
		t.Fatalf("token: %+v", tok)
	}
	if code := w.run("project", "token", "create", "demo", "--role", "reviewer", "--data", data); code != ExitOK ||
		!strings.Contains(w.stdout.String(), "https://review.example.com/p/demo/s/rt_") {
		t.Fatalf("share link: %s", w.stdout)
	}

	// retake serve, on a free port; links are for localhost without a public URL.
	delete(w.env, "RETAKE_PUBLIC_URL")
	url, stop, done := w.serve(t, w.environment(nil), "serve", "--data", data, "--listen", "127.0.0.1:0")
	if !strings.HasPrefix(url, "http://localhost:") || !strings.Contains(w.stdout.String(), "1 project, 2 users") {
		t.Fatalf("serve: %s\n%s", url, w.stdout)
	}
	base := strings.Replace(strings.TrimSuffix(url, "/"), "localhost", "127.0.0.1", 1)

	if code := w.run("remote", "add", "demo", base+"/p/demo/", "--token", tok.Token); code != ExitOK {
		t.Fatalf("remote add: %s", w.stderr)
	}
	rp := filepath.Join(w.env["XDG_CONFIG_HOME"], "retake", "remotes.toml")
	if st, err := os.Stat(rp); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("remotes file: %v %v", st, err)
	}
	if code := w.run("remote", "add", "x", "https://review.example.com/notaproject", "--token", "t"); code !=
		ExitUsage {
		t.Fatal("a URL without /p/<name>")
	}
	v2 := writeVideo(t, "cut_v2.mp4", 200_000, 1)
	var pushed struct {
		Version struct {
			Label, Parent, SHA256, By string
		} `json:"version"`
	}
	w.runJSON(t, &pushed, "push", "--remote", "demo", v2)
	if pushed.Version.Label != "v2" || pushed.Version.Parent != "v1" || pushed.Version.By != "editor" {
		t.Fatalf("push: %+v", pushed)
	}
	if code := w.run("push", "--remote", "nope", v2); code != ExitError || !strings.Contains(w.stderr.String(),
		"no remote nope") {
		t.Fatalf("unknown remote: %d %s", code, w.stderr)
	}

	// An upload that a crashed run left half-sent: the next push of the same file goes on from the server's offset.
	v3 := writeVideo(t, "cut_v3.mp4", 300_000, 2)
	sum, _ := tus.FileSHA256(v3)
	meta := "filename " + base64.StdEncoding.EncodeToString([]byte("cut_v3.mp4")) + ",sha256 " +
		base64.StdEncoding.EncodeToString([]byte(sum))
	req, _ := http.NewRequest(http.MethodPost, base+"/p/demo/api/uploads", nil)
	req.Header = http.Header{"Authorization": {"Bearer " + tok.Token}, "Tus-Resumable": {"1.0.0"},
		"Upload-Length": {"300000"}, "Upload-Metadata": {meta}}
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("create: %v %v", res, err)
	}
	_ = res.Body.Close()
	upURL := base + res.Header.Get("Location")
	body, _ := os.ReadFile(v3)
	patch, _ := http.NewRequest(http.MethodPatch, upURL, strings.NewReader(string(body[:100_000])))
	patch.Header = http.Header{"Authorization": {"Bearer " + tok.Token}, "Tus-Resumable": {"1.0.0"},
		"Upload-Offset": {"0"}, "Content-Type": {"application/offset+octet-stream"}}
	pres, err := http.DefaultClient.Do(patch)
	if err != nil || pres.StatusCode != http.StatusNoContent {
		t.Fatalf("patch: %v %v", pres, err)
	}
	_ = pres.Body.Close()
	st, _ := os.Stat(v3)
	state := fmt.Sprintf(`[{"remote":"demo","url":%q,"file":%q,"size":300000,"mtime":%q,"sha256":%q,"upload":%q}]`,
		base+"/p/demo", v3, st.ModTime().UTC().Format(time.RFC3339Nano), sum, upURL)
	sp := filepath.Join(w.env["XDG_STATE_HOME"], "retake", "uploads.json")
	_ = os.MkdirAll(filepath.Dir(sp), 0o700)
	if err := os.WriteFile(sp, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	w.stderr.Reset()
	if code := w.run("push", "--remote", "demo", v3, "--change", "x=1"); code != ExitUsage {
		t.Fatal("a bad --change")
	}
	if code := w.run("push", "--remote", "demo", v3); code != ExitOK ||
		!strings.Contains(w.stderr.String(), "resuming the upload") || !strings.Contains(w.stdout.String(),
		"pushed v3 from v2") {
		t.Fatalf("resumed push: %d\n%s\n%s", code, w.stdout, w.stderr)
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "uploads")); len(entries) != 0 {
		t.Fatalf("uploads left: %v (the push started over instead of resuming)", entries)
	}
	if b, _ := os.ReadFile(sp); strings.Contains(string(b), "cut_v3") {
		t.Fatalf("state after the push: %s", b)
	}

	// The rest of the user commands.
	var sessions struct {
		Sessions []any `json:"sessions"`
	}
	w.runJSON(t, &sessions, "user", "sessions", "ana@example.com", "--data", data)
	for _, args := range [][]string{{"user", "link", "bo@example.com"}, {"user", "list"}, {"user", "signout",
		"bo@example.com"}, {"user", "disable", "bo@example.com"}, {"user", "enable", "bo@example.com"},
		{"project", "token", "list", "demo"}, {"project", "token", "revoke", "demo", "2"}, {"project", "list"},
		{"project", "member", "remove", "demo", "bo@example.com"}, {"user", "remove", "bo@example.com"},
		{"remote", "list"}, {"remote", "remove", "demo"}} {
		if args[0] != "remote" {
			args = append(args, "--data", data)
		}
		if code := w.run(args...); code != ExitOK {
			t.Fatalf("%v: %d %s", args, code, w.stderr)
		}
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("serve exit: %d %s", code, w.stderr)
	}
}

func TestServeSettings(t *testing.T) {
	w := newWorld(t)
	w.config(t, "[server]\ndata = \"/srv/retake\"\nlisten = \"9000\"\ntrusted_proxies = [\"10.0.0.0/8\"]\n")
	env := w.environment(nil)
	s, err := resolveServer(env, loadConfig(env), serverFlags{})
	if err != nil || s.Data != "/srv/retake" || s.Listen != ":9000" || len(s.Proxies) != 1 || s.S3 != nil {
		t.Fatalf("%+v %v", s, err)
	}
	w.env["RETAKE_DATA"] = "/env/data"
	w.env["RETAKE_S3_ENDPOINT"] = "minio:9000"
	if _, err := resolveServer(env, loadConfig(env), serverFlags{}); err == nil ||
		!strings.Contains(err.Error(), "RETAKE_S3_ACCESS_KEY") {
		t.Fatalf("S3 without keys: %v", err)
	}
	w.env["RETAKE_S3_ACCESS_KEY"], w.env["RETAKE_S3_SECRET_KEY"], w.env["RETAKE_S3_INSECURE"] = "a", "b", "1"
	s, err = resolveServer(env, loadConfig(env), serverFlags{data: "/flag/data"})
	if err != nil || s.Data != "/flag/data" || s.S3 == nil || s.S3.Bucket != "retake" || !s.S3.Insecure ||
		s.S3.Region != "us-east-1" {
		t.Fatalf("%+v %v", s, err)
	}
	delete(w.env, "RETAKE_DATA")
	w.config(t, "")
	if code := w.run("serve"); code != ExitUsage || !strings.Contains(w.stderr.String(), "no data folder") {
		t.Fatalf("serve without data: %d %s", code, w.stderr)
	}
	if code := w.run("serve", "--data", w.root, "--public-url", "https://x.example/path"); code != ExitUsage {
		t.Fatalf("bad public URL: %d", code)
	}
}
