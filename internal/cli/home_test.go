package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
)

// newFolderWorld is a world whose root is a folder of two projects: the v5/v6/v7 family of newWorld under clip/, and
// a flat one under "Other Cut/".
func newFolderWorld(t *testing.T) (*world, string) {
	t.Helper()
	w := newWorld(t)
	dir := filepath.Join(w.root, "videos")
	if err := os.MkdirAll(filepath.Join(dir, "clip"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v5", "v6", "v7"} {
		if err := os.Rename(filepath.Join(w.root, v), filepath.Join(dir, "clip", v)); err != nil {
			t.Fatal(err)
		}
	}
	w.video = filepath.Join(dir, "clip", "v6", "clip_v6.mp4")
	other := filepath.Join(dir, "Other Cut", "other_v2.mp4")
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	return w, dir
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if v != nil {
		_ = json.NewDecoder(res.Body).Decode(v)
	}
	return res.StatusCode
}

func TestOpenFolderServesTheHome(t *testing.T) {
	w, dir := newFolderWorld(t)
	w.config(t, "name = \"Tester\"\nidle_exit_minutes = 0\n")
	url, stop, done := w.serve(t, w.environment(nil), "open", dir, "--port", "0")

	var h home.Response
	if code := getJSON(t, url+"api/home", &h); code != 200 || h.Folder != dir || h.Title != "videos" ||
		len(h.Projects) != 2 {
		t.Fatalf("/api/home: %d %+v", code, h)
	}
	names := []string{h.Projects[0].Name, h.Projects[1].Name}
	if strings.Join(names, " ") != "clip other-cut" && strings.Join(names, " ") != "other-cut clip" {
		t.Errorf("names %v", names)
	}
	var info struct{ Label, Role string }
	if code := getJSON(t, url+"p/clip/api/info", &info); code != 200 || info.Label != "v7" || info.Role != "owner" {
		t.Errorf("clip's info: %d %+v", code, info)
	}
	// A take saved through /p/clip/ lands beside its version, with the config's name.
	res, err := http.Post(url+"p/clip/api/session/2026-10-29_20-48-01/data?v=v6", "application/json",
		strings.NewReader(`{"session":{"format":3}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	b, err := os.ReadFile(filepath.Join(dir, "clip", "v6", "clip_v6.review", "2026-10-29_20-48-01", "session.json"))
	if res.StatusCode != http.StatusOK || err != nil || !strings.Contains(string(b), `"Tester"`) {
		t.Errorf("save: %d %v %s", res.StatusCode, err, b)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Errorf("exit %d: %s", code, w.stderr)
	}
	out := w.stdout.String()
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`(?m)^retake: ` + regexp.QuoteMeta(dir) + `  \(2 videos, 2 to review\)$`),
		regexp.MustCompile(`(?m)^  clip +v7 +to review$`),
		regexp.MustCompile(`(?m)^  other-cut +v2 +to review$`),
		regexp.MustCompile(`(?m)^  page   http://127\.0\.0\.1:\d+/$`),
		regexp.MustCompile(`(?m)^  Ctrl\+C stops the server$`),
		regexp.MustCompile(`(?m)^retake: stopped$`),
	} {
		if !want.MatchString(out) {
			t.Errorf("stdout lacks %s:\n%s", want, out)
		}
	}
	if len(w.opened) != 1 || w.opened[0] != url {
		t.Errorf("opener calls = %v", w.opened)
	}
}

func TestOpenFolderArguments(t *testing.T) {
	w, dir := newFolderWorld(t)
	if code := w.run("open", dir, "--versions", w.video, "--no-open"); code != ExitUsage ||
		!strings.Contains(w.stderr.String(), "--versions takes a video, not a folder") {
		t.Errorf("--versions with a folder: %d %s", code, w.stderr)
	}
	// An empty folder still serves a home (videos may arrive later), and says what a video looks like.
	empty := filepath.Join(w.root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	w.stdout.b.Reset()
	url, stop, done := w.serve(t, w.environment(nil), "open", empty, "--port", "0", "--no-open")
	var h home.Response
	if code := getJSON(t, url+"api/home", &h); code != 200 || len(h.Projects) != 0 {
		t.Errorf("empty home: %d %+v", code, h)
	}
	stop()
	wait(t, done)
	if !strings.Contains(w.stdout.String(), "(0 videos, 0 to review)") ||
		!strings.Contains(w.stdout.String(), "no videos yet: a video is a file named like clip_v1.mp4") {
		t.Errorf("stdout: %s", w.stdout)
	}
}

func TestFolderTokens(t *testing.T) {
	w, dir := newFolderWorld(t)
	if code := w.run("token", "create", dir, "--role", "viewer", "--label", "client", "--host", "share.example",
		"--json"); code != ExitOK {
		t.Fatalf("create: %d %s", code, w.stderr)
	}
	var created tokenJSON
	if err := json.Unmarshal([]byte(w.stdout.String()), &created); err != nil || created.URL !=
		"https://share.example/s/"+created.Secret {
		t.Errorf("--json: %v %+v", err, created)
	}
	// It is in the folder's home store, not in a project's.
	st, err := project.OpenHomeExisting(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if ts, _ := st.Tokens(); len(ts) != 1 || ts[0].Label != "client" {
		t.Errorf("home tokens %+v", ts)
	}
	if _, err := os.Stat(project.DBPath(filepath.Join(dir, "clip"))); err == nil {
		t.Error("a project store was made")
	}
	w.stdout.b.Reset()
	if code := w.run("share", "list", dir); code != ExitOK || !regexp.MustCompile(`#1 +viewer +client`).MatchString(
		w.stdout.String()) {
		t.Errorf("share list: %d %s", code, w.stdout)
	}
	if code := w.run("share", "revoke", dir, "1"); code != ExitOK {
		t.Errorf("revoke: %d", code)
	}
	if code := w.run("token", "create", dir, "--role", "agent"); code != ExitUsage ||
		!strings.Contains(w.stderr.String(), "an agent works on one project") {
		t.Errorf("agent token for a folder: %d %s", code, w.stderr)
	}
}
