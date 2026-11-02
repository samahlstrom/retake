package hub

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/team"
)

func settingsOf(t *testing.T, body string) SettingsView {
	t.Helper()
	var v SettingsView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

// pollAs registers a worker on a project of the folder, as its in-process worker does.
func pollAs(t *testing.T, root string, agents ...project.AgentProfile) {
	t.Helper()
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	w := project.EditingWorker{ID: "w-" + filepath.Base(root), Name: "sam-pc", Session: "s", Agents: agents}
	if _, err := st.PollWork(w, "", ""); err != nil {
		t.Fatal(err)
	}
}

func automationOf(t *testing.T, root string) project.AutomationConfig {
	t.Helper()
	st, err := project.OpenExisting(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	a, err := st.Automation()
	if err != nil {
		t.Fatal(err)
	}
	return a.Config
}

var testAgents = []project.AgentProfile{{ID: "claude", Name: "Claude Code"}, {ID: "codex", Name: "Codex"}}

// The local settings page: the owner reads and writes the config file, and the default agent goes to every project.
func TestFolderSettings(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "retake", "config.toml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("# mine\nname = \"Ana\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	themeDir := filepath.Join(cfgDir, "retake", "theme")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(themeDir, "theme.toml"), []byte("name = \"Studio\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hello := &ext.Ext{Manifest: ext.Manifest{API: ext.API, ID: "hello", Name: "Hello", Description: "Says hi"},
		FS: fstest.MapFS{}, Where: ext.WhereUser}
	x := newFT(t, func(o *FolderOptions) {
		o.Settings = &LocalSettings{ConfigPath: cfgPath, ThemeDir: themeDir, Home: cfgDir,
			Agents:     func() []project.AgentProfile { return testAgents },
			Extensions: func() []*ext.Ext { return []*ext.Ext{hello} }}
	})
	pollAs(t, filepath.Join(x.dir, "alpha"), testAgents...)
	pollAs(t, filepath.Join(x.dir, "bravo"), testAgents[0]) // its computer offers only Claude Code

	r := x.get("/api/settings")
	if r.code != 200 {
		t.Fatalf("GET: %d %s", r.code, r.body)
	}
	v := settingsOf(t, r.body)
	if v.Hosted || len(v.Agents) != 2 || v.Agent != "" || v.Fallback != "" || v.OpenIn != "background" ||
		!v.OpenInEditable || !v.ExtensionsEditable || v.Config != "~/retake/config.toml" ||
		v.Theme != (SettingsTheme{Folder: "~/retake/theme", Used: true, Name: "Studio"}) ||
		len(v.Extensions) != 1 || v.Extensions[0] != (SettingsExt{ID: "hello", Name: "Hello", Description: "Says hi"}) {
		t.Fatalf("settings: %+v", v)
	}

	r = x.do(http.MethodPost, "/api/settings", `{"agent": "codex", "fallback": "claude", "open_in": "herdr",
		"extensions": {"hello": true}}`)
	if r.code != 200 {
		t.Fatalf("POST: %d %s", r.code, r.body)
	}
	if v = settingsOf(t, r.body); v.Agent != "codex" || v.Fallback != "claude" || v.OpenIn != "herdr" ||
		!v.Extensions[0].On || v.Extensions[0].Loaded {
		t.Fatalf("after: %+v", v)
	}
	b, _ := os.ReadFile(cfgPath)
	c, _, err := config.Parse(string(b))
	if err != nil || !strings.HasPrefix(string(b), "# mine\nname = \"Ana\"\n") || *c.DefaultAgent != "codex" ||
		*c.FallbackAgent != "claude" || *c.OpenAgentsIn != "herdr" || len(c.Extensions.Enabled) != 1 {
		t.Fatalf("config.toml (%v):\n%s", err, b)
	}
	if a := automationOf(t, filepath.Join(x.dir, "alpha")); a.Agent != "codex" || a.Fallback != "claude" {
		t.Errorf("alpha's config: %+v", a)
	}
	// bravo's computer has no Codex: it keeps Claude Code, and its fallback can't be the agent itself.
	if a := automationOf(t, filepath.Join(x.dir, "bravo")); a.Agent != "claude" || a.Fallback != "" {
		t.Errorf("bravo's config: %+v", a)
	}

	// Back to the defaults: the lines go.
	r = x.do(http.MethodPost, "/api/settings", `{"fallback": "", "open_in": "background"}`)
	if r.code != 200 {
		t.Fatalf("POST: %d %s", r.code, r.body)
	}
	b, _ = os.ReadFile(cfgPath)
	if strings.Contains(string(b), "fallback_agent") || strings.Contains(string(b), "open_agents_in") ||
		!strings.Contains(string(b), `default_agent = "codex"`) {
		t.Fatalf("config.toml:\n%s", b)
	}
	if a := automationOf(t, filepath.Join(x.dir, "alpha")); a.Fallback != "" {
		t.Errorf("alpha's fallback: %+v", a)
	}

	// Everything is checked before anything is written.
	for _, body := range []string{
		`{"agent": "gemini"}`,
		`{"fallback": "codex"}`, // the default agent itself
		`{"open_in": "screen"}`,
		`{"extensions": {"nope": true}}`,    // not installed
		`{"agent": "claude", "theme": "x"}`, // an unknown key
		`{"agent": 3}`,
		`not json`,
		`{"agent": "claude", "extensions": {"zz": true}}`, // the valid part isn't written either
	} {
		before, _ := os.ReadFile(cfgPath)
		if r := x.do(http.MethodPost, "/api/settings", body); r.code != 400 || !strings.Contains(r.body, `"error"`) {
			t.Errorf("%s: %d %s", body, r.code, r.body)
		}
		if after, _ := os.ReadFile(cfgPath); string(after) != string(before) {
			t.Errorf("%s changed the file:\n%s", body, after)
		}
	}

	// The owner only, from our own origin.
	tok := homeToken(t, x.dir, team.Reviewer, "guest")
	if r := x.get("/api/settings", shared(), bearerTok(tok)); r.code != 403 {
		t.Errorf("a guest's GET: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/api/settings", `{"agent": "claude"}`, shared(), bearerTok(tok)); r.code != 403 {
		t.Errorf("a guest's POST: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/api/settings", `{"agent": "claude"}`, origin("http://evil.example")); r.code != 403 {
		t.Errorf("another origin: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPut, "/api/settings", `{}`); r.code != 405 {
		t.Errorf("PUT: %d %s", r.code, r.body)
	}
}

// A new default agent that was the fallback leaves no fallback; a folder without settings has none.
func TestFolderSettingsEdges(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfgPath, []byte("default_agent = \"claude\"\nfallback_agent = \"codex\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	x := newFT(t, func(o *FolderOptions) {
		o.Settings = &LocalSettings{ConfigPath: cfgPath,
			Agents: func() []project.AgentProfile { return testAgents }}
	})
	r := x.do(http.MethodPost, "/api/settings", `{"agent": "codex"}`)
	if v := settingsOf(t, r.body); r.code != 200 || v.Agent != "codex" || v.Fallback != "" || v.Theme.Folder != "" {
		t.Fatalf("%d %s", r.code, r.body)
	}
	y := newFT(t, nil)
	if r := y.get("/api/settings"); r.code != 404 {
		t.Errorf("no settings: %d %s", r.code, r.body)
	}
}

// The team server's settings: the admin's, kept in the server, applied to their projects; where agents run and the
// extensions are each computer's and the server's own, so they're read-only here.
func TestHubSettings(t *testing.T) {
	x := newTH(t, Config{}, nil)
	admin := x.session(x.user("admin@example.com", true))
	member := x.session(x.user("ana@example.com", false))
	x.project("demo", true)
	p, err := x.h.project("demo")
	if err != nil {
		t.Fatal(err)
	}
	w := project.EditingWorker{ID: "m1", Name: "sam-pc", Session: "s", Agents: testAgents}
	if _, err := p.store.PollWork(w, "", ""); err != nil {
		t.Fatal(err)
	}
	r := x.do(http.MethodGet, "/api/settings", "", cookie(SessionCookie, admin))
	v := settingsOf(t, r.body)
	if r.code != 200 || !v.Hosted || v.OpenInEditable || v.ExtensionsEditable || len(v.Agents) != 2 ||
		len(v.Machines) != 1 || v.Config != "" {
		t.Fatalf("GET: %d %s", r.code, r.body)
	}
	r = x.post("/api/settings", `{"agent": "codex", "fallback": "claude"}`, cookie(SessionCookie, admin))
	if v = settingsOf(t, r.body); r.code != 200 || v.Agent != "codex" || v.Fallback != "claude" {
		t.Fatalf("POST: %d %s", r.code, r.body)
	}
	a, _ := p.store.Automation()
	if a.Config.Agent != "codex" || a.Config.Fallback != "claude" {
		t.Errorf("demo's config: %+v", a.Config)
	}
	if v = settingsOf(t, x.do(http.MethodGet, "/api/settings", "", cookie(SessionCookie, admin)).body); v.Agent != "codex" {
		t.Errorf("not kept: %+v", v)
	}
	for _, body := range []string{`{"open_in": "tmux"}`, `{"extensions": {"scripts": false}}`, `{"agent": "x"}`} {
		if r := x.post("/api/settings", body, cookie(SessionCookie, admin)); r.code != 400 {
			t.Errorf("%s: %d %s", body, r.code, r.body)
		}
	}
	if r := x.do(http.MethodGet, "/api/settings", "", cookie(SessionCookie, member)); r.code != 403 {
		t.Errorf("a member: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodGet, "/api/settings", ""); r.code != 401 {
		t.Errorf("no session: %d %s", r.code, r.body)
	}
	if r := x.do(http.MethodPost, "/api/settings", `{"agent": "claude"}`, cookie(SessionCookie, admin),
		origin("https://evil.example")); r.code != 403 {
		t.Errorf("another origin: %d %s", r.code, r.body)
	}
}
