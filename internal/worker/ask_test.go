package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/project"
)

// The built-in agents never stop for a permission prompt, headless or interactive, and Claude Code's own question
// tool is off (spec/automation.md, "Permissions").
func TestBuiltInProfilesNeverAskPermission(t *testing.T) {
	bin := t.TempDir()
	claude, codex := fakeProgram(t, bin, "claude"), fakeProgram(t, bin, "codex")
	t.Setenv("PATH", bin)
	got := map[string]Profile{}
	for _, p := range DefaultProfiles() {
		got[p.ID] = p
	}
	for _, tc := range []struct {
		name      string
		got, want []string
	}{
		{"claude", got["claude"].Command, []string{claude, "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions", "--disallowedTools=AskUserQuestion"}},
		{"claude interactive", got["claude"].Interactive, []string{claude, "--dangerously-skip-permissions", "--disallowedTools=AskUserQuestion"}},
		{"codex", got["codex"].Command, []string{codex, "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-"}},
		{"codex interactive", got["codex"].Interactive, []string{codex, "--dangerously-bypass-approvals-and-sandbox"}},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s: %q", tc.name, tc.got)
		}
	}
}

// The ask endpoint takes the running attempt's secret only, and passes the question on with the worker's own
// credentials; the agent never sees them.
func TestAskEndpointNeedsTheAttemptsSecret(t *testing.T) {
	var forwarded []map[string]any
	var auth []string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		forwarded = append(forwarded, b)
		auth = append(auth, r.Header.Get("Authorization"))
		if b["note"] == float64(9) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"note #9 isn't one of this task's notes (#1)"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(project.TaskQuestion{ID: 7, Note: 1, Version: "v2", At: 1, Text: "Which?"})
	}))
	defer host.Close()
	root := t.TempDir()
	r := &runner{opts: Options{URL: host.URL, Token: "rk_worker_secret", Root: root, HTTP: host.Client(), Log: io.Discard},
		dir: filepath.Join(root, ".retake", "worker"), identity: project.EditingWorker{ID: "w", Session: "s"}}
	if e := os.MkdirAll(r.dir, 0o700); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.life = ctx
	task := &project.EditingTask{ID: "task", Lease: "lease", Notes: []int64{1}}
	r.setActive(task)
	env, _, _, _, end := r.beginAsk(project.WorkAssignment{Task: task}, "", false)
	var url, token string
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "RETAKE_ASK_URL":
			url = v
		case "RETAKE_ASK_TOKEN":
			token = v
		}
	}
	b, e := os.ReadFile(filepath.Join(r.dir, AskFile))
	var file AskEndpoint
	if e != nil || json.Unmarshal(b, &file) != nil || file.URL != url || file.Token != token || !strings.HasPrefix(url, "http://127.0.0.1:") || len(token) != 32 {
		t.Fatalf("env %q, file %s %v", env, b, e)
	}
	if info, _ := os.Stat(filepath.Join(r.dir, AskFile)); info.Mode().Perm() != 0o600 {
		t.Fatalf("ask.json is %v", info.Mode())
	}
	post := func(secret, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, url+"/ask", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = res.Body.Close() }()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}
	for _, secret := range []string{"", "wrong", "rk_worker_secret", token[:31]} {
		if code, _ := post(secret, `{"text":"Which?"}`); code != http.StatusForbidden {
			t.Fatalf("secret %q: %d", secret, code)
		}
	}
	if len(forwarded) != 0 {
		t.Fatal("a refused question reached the host")
	}
	if code, out := post(token, `{"text":"Which?","at":1,"boxes":[{"box":[0,0,0.5,0.5],"label":"here"}],"task":"other","lease":"stolen"}`); code != 200 || !strings.Contains(out, `"id":7`) {
		t.Fatalf("%d %s", code, out)
	}
	if f := forwarded[0]; f["task"] != "task" || f["lease"] != "lease" || auth[0] != "Bearer rk_worker_secret" || f["text"] != "Which?" {
		t.Fatalf("forwarded %v %q", f, auth)
	}
	if code, out := post(token, `{"note":9,"text":"Which?"}`); code != http.StatusConflict || !strings.Contains(out, "isn't one of this task's notes") {
		t.Fatalf("the host's refusal: %d %s", code, out)
	}
	if len(r.ask.questions) != 1 || r.ask.questions[0].ID != 7 || !r.allHeld(task) {
		t.Fatalf("questions %+v", r.ask.questions)
	}
	// The attempt ends: its secret and file go with it.
	end(false)
	if _, e = os.Stat(filepath.Join(r.dir, AskFile)); !os.IsNotExist(e) {
		t.Fatal("ask.json outlived its attempt")
	}
	if code, _ := post(token, `{"text":"Which?"}`); code != http.StatusForbidden {
		t.Fatalf("an old secret: %d", code)
	}
}

func TestAnswersAreToldOnce(t *testing.T) {
	r := &runner{}
	r.ask.secret = "x"
	q := project.TaskQuestion{ID: 7, Note: 3, At: 83.4, Answer: &project.TaskAnswer{ID: 9, Text: "The new\n  one"}}
	task := &project.EditingTask{ID: "t", Notes: []int64{3}}
	r.sawQuestions(task, []project.TaskQuestion{q})
	if got := r.untold(); len(got) != 1 || answerLine(got[0]) != "Answer to your question on note 3 at 1:23: The new one" {
		t.Fatalf("%+v", got)
	}
	if !r.allHeld(task) {
		t.Fatal("an answer not told yet doesn't hold its note")
	}
	r.tell(9)
	if len(r.untold()) != 0 || r.allHeld(task) || !slices.Equal(r.unsentTold(), []int64{9}) {
		t.Fatal("told", r.untold(), r.unsentTold())
	}
	r.acked([]int64{9})
	if len(r.unsentTold()) != 0 {
		t.Fatal("acked")
	}
	// The server's own told list counts too.
	r2 := &runner{}
	r2.ask.secret = "x"
	r2.sawQuestions(&project.EditingTask{Told: []int64{9}}, []project.TaskQuestion{q})
	if len(r2.untold()) != 0 {
		t.Fatal("the server's told answer was told again")
	}
}

// The prompt names the retake an agent will actually run (spec/setup.md, "Retake's own tools come first"): a bundle's
// own folder, first on retake's PATH, isn't on the agent's (herdr, tmux, the person's shell), so it doesn't count.
func TestCommandForNamesTheBinaryAnAgentRuns(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(p, body string, mode os.FileMode) string {
		if e := os.MkdirAll(filepath.Dir(p), 0o750); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), mode); e != nil {
			t.Fatal(e)
		}
		return p
	}
	// An npm install: the platform package holds the Go binary and the manifest; the global bin's `retake` is the node
	// launcher, a script elsewhere.
	pkg := filepath.Join(root, "lib", "node_modules", "retake-cli", "node_modules", "retake-cli-linux-x64", "bin")
	self := write(filepath.Join(pkg, "retake"), "\x7fELF", 0o755)
	write(filepath.Join(pkg, "retake-bundle.json"), `{"bundle": 1}`, 0o644)
	launcher := write(filepath.Join(root, "lib", "node_modules", "retake-cli", "bin", "retake.js"), "#!/usr/bin/env node\n", 0o755)
	npmBin := filepath.Join(root, "npm-bin")
	if e := os.MkdirAll(npmBin, 0o750); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(launcher, filepath.Join(npmBin, "retake")); e != nil {
		t.Fatal(e)
	}
	// Homebrew or install.sh: a link on PATH to the bundle's own binary.
	brewBin := filepath.Join(root, "brew-bin")
	if e := os.MkdirAll(brewBin, 0o750); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(self, filepath.Join(brewBin, "retake")); e != nil {
		t.Fatal(e)
	}
	// A build from source: bin/retake, no manifest, its folder on PATH.
	dev := write(filepath.Join(root, "src", "bin", "retake"), "\x7fELF", 0o755)
	empty := filepath.Join(root, "empty")
	join := func(dirs ...string) string { return strings.Join(dirs, string(os.PathListSeparator)) }

	for _, tc := range []struct {
		name, self, path, want string
	}{
		{"npm: the launcher is first after the bundle", self, join(pkg, npmBin, "/usr/bin"), self},
		{"npm: no retake on the agent's PATH at all", self, join(pkg, empty), self},
		{"Homebrew or install.sh: the link is this binary", self, join(pkg, brewBin, npmBin), "retake"},
		{"a relative PATH entry doesn't count", self, join(".", brewBin), "retake"},
		{"from source, its own folder on PATH", dev, join(filepath.Dir(dev), npmBin), "retake"},
		{"from source, another retake first", dev, join(npmBin, filepath.Dir(dev)), dev},
		{"unknown binary", "", join(npmBin), "retake"},
	} {
		if got := commandFor(tc.self, tc.path); got != tc.want {
			t.Errorf("%s: commandFor = %q, want %q", tc.name, got, tc.want)
		}
	}
	spaced := write(filepath.Join(root, "Retake App", "retake"), "\x7fELF", 0o755)
	if got, want := commandFor(spaced, empty), "'"+spaced+"'"; got != want {
		t.Errorf("a path with a space: %q, want %q", got, want)
	}
}
