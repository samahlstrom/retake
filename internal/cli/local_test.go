package cli

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/worker"
)

// fakeAgent is a stand-in for Claude Code or Codex: it makes the first shot blue (the colour comes from its profile's
// env, which names a variable of the worker's own environment) and reports. It never calls a provider.
const fakeAgent = `#!/bin/sh
cat >/dev/null
sed "s/#aa3322/#2255dd/" edit/timeline.json > edit/timeline.json.tmp && mv edit/timeline.json.tmp edit/timeline.json
printf '{"state":"completed","message":"Made the first shot %s."}' "$SHOT_COLOUR" > "$RETAKE_REPORT"
`

// TestOpenFolderEditsWithItsOwnWorker is the local story end to end: retake open <folder>, Send a comment, and the
// in-process worker runs the agent, renders drafts and makes one new version, with nothing else to start. A restart
// with unchanged source makes no new version.
func TestOpenFolderEditsWithItsOwnWorker(t *testing.T) {
	testmedia.NeedShell(t)
	editor, err := filepath.Abs("../../editor/target/release/retake-edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(editor); err != nil {
		t.Skip("build retake-edit to run the local editing test")
	}
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(scratch, "local-worker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "demo")
	testmedia.Video(t, filepath.Join(root, "clip_v1.mp4"), 6)
	testmedia.Video(t, filepath.Join(root, "edit", "voice.mp4"), 6)
	timeline := `{"schema":"timeline/0.1","id":"local-fixture","rev":1,"format":{"size":[320,180],"fps":12,"sample_rate":48000,"background":"#000000"},"assets":{"vo":{"path":"voice.mp4"}},"spine":[{"id":"a","asset":"vo","in":0,"out":2},{"id":"b","asset":"vo","in":2,"out":4},{"id":"c","asset":"vo","in":4,"out":6}],"scenes":[{"id":"scene","shots":[{"id":"first","at":"start:a","picture":{"background":"#aa3322","layers":[]}},{"id":"second","at":"start:b","picture":{"background":"#336633","layers":[]}},{"id":"third","at":"start:c","picture":{"background":"#333333","layers":[]}}]}]}`
	if err = os.WriteFile(filepath.Join(root, "edit", "timeline.json"), []byte(timeline), 0o600); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(dir, "fake-agent")
	if err = os.WriteFile(agentPath, []byte(fakeAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RETAKE_TEST_COLOUR", "blue")
	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser"}
	w.config(t, "idle_exit_minutes = 0\n[edit]\nretake_edit = "+tomlPath(editor)+"\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "fake", Name: "Fake agent"}, Command: []string{agentPath},
			Env: map[string]string{"SHOT_COLOUR": "$RETAKE_TEST_COLOUR"}}}
	}
	host, _ := os.Hostname()

	type view struct {
		project.AutomationState
		Local   bool   `json:"local"`
		Blocked string `json:"blocked"`
	}
	waitFor := func(url, what string, ok func(view) bool) view {
		t.Helper()
		var v view
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			v = view{}
			if getJSON(t, url+"p/demo/api/automation", &v) == 200 && ok(v) {
				return v
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("waiting for %s: %+v\nstderr: %s", what, v, w.stderr)
		return v
	}
	url, stop, done := w.serve(t, env, "open", dir, "--port", "0", "--no-open")
	v := waitFor(url, "the local worker", func(v view) bool {
		return v.Local && v.Blocked == "" && len(v.Workers) == 1 && v.Config.Worker == v.Workers[0].ID
	})
	if v.Workers[0].Name != host || v.Workers[0].Agents[0].Name != "Fake agent" || v.Config.Enabled {
		t.Fatalf("local worker: %+v", v)
	}
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err = st.Sync(project.Run{Version: "v1", Comments: []project.Comment{{Key: "blue", Text: "Make the first shot blue", T0: 0, T1: 1}}}); err != nil {
		t.Fatal(err)
	}
	// Send, with auto-send off.
	res, err := http.Post(url+"p/demo/api/automation/tasks", "application/json", strings.NewReader(`{"notes":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("send: %d", res.StatusCode)
	}
	v = waitFor(url, "the edit", func(v view) bool { return len(v.Tasks) == 1 && v.Tasks[0].State == "completed" })
	if v.Tasks[0].Result != "v2" {
		t.Fatalf("result %q, renders %+v", v.Tasks[0].Result, v.Renders)
	}
	n, err := st.Note(1)
	if err != nil || n.Status != project.StatusAnswered || n.AnsweredIn != "v2" || n.Change != "Made the first shot blue." {
		t.Fatalf("note: %+v %v", n, err)
	}
	var info struct {
		Versions []struct{ Label string } `json:"versions"`
	}
	getJSON(t, url+"p/demo/api/info", &info)
	var labels []string
	for _, x := range info.Versions {
		labels = append(labels, x.Label)
	}
	if !slices.Equal(labels, []string{"v1", "v2"}) {
		t.Fatalf("family after one task: %v", labels)
	}
	res, err = http.Get(url + "p/demo/video/v2/index.m3u8")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "#EXT-X-ENDLIST") {
		t.Fatalf("v2 playlist: %d %s", res.StatusCode, body)
	}
	renders := len(v.Renders)
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}

	// A restart with the source unchanged renders and registers nothing.
	restarted := time.Now().Unix()
	url, stop, done = w.serve(t, env, "open", dir, "--port", "0", "--no-open")
	waitFor(url, "the restarted worker", func(v view) bool { return len(v.Workers) == 1 && v.Workers[0].Seen > restarted })
	time.Sleep(2 * time.Second) // the render loop looks every 500 ms
	v = waitFor(url, "the state", func(view) bool { return true })
	if len(v.Renders) != renders {
		t.Fatalf("restart rendered again: %d renders, had %d", len(v.Renders), renders)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

// plainAgent is a stand-in agent for a folder without a timeline: it writes its video (here a copy of v2) to
// $RETAKE_OUTPUT and reports. It never calls a provider.
const plainAgent = `#!/bin/sh
cat >/dev/null
cp clip_v2.mp4 "$RETAKE_OUTPUT"
printf '{"state":"completed","message":"Made v2 again as v3."}' > "$RETAKE_REPORT"
`

// TestOpenPlainFolderSendsToItsAgent: a folder of finished videos, no timeline and no retake-edit. retake open runs
// its worker anyway, Send reaches the agent, and the agent's video becomes the next version file, which answers the
// comment.
func TestOpenPlainFolderSendsToItsAgent(t *testing.T) {
	testmedia.NeedShell(t)
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(scratch, "local-plain-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "promo")
	testmedia.Video(t, filepath.Join(root, "clip_v1.mp4"), 2)
	testmedia.Video(t, filepath.Join(root, "clip_v2.mp4"), 3)
	agentPath := filepath.Join(dir, "fake-agent")
	if err = os.WriteFile(agentPath, []byte(plainAgent), 0o700); err != nil {
		t.Fatal(err)
	}
	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser",
		"RETAKE_EDIT": filepath.Join(dir, "no-retake-edit")}
	w.config(t, "idle_exit_minutes = 0\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "fake", Name: "Fake agent"}, Command: []string{agentPath}}}
	}
	type view struct {
		project.AutomationState
		Local   bool   `json:"local"`
		Blocked string `json:"blocked"`
	}
	url, stop, done := w.serve(t, env, "open", dir, "--port", "0", "--no-open")
	var v view
	deadline := time.Now().Add(30 * time.Second)
	for !v.Local || v.Blocked != "" || len(v.Workers) != 1 || v.Config.Agent != "fake" {
		if time.Now().After(deadline) {
			t.Fatalf("no local worker: %+v\n%s", v, w.stderr)
		}
		time.Sleep(100 * time.Millisecond)
		v = view{}
		getJSON(t, url+"p/promo/api/automation", &v)
	}
	st, err := project.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err = st.Sync(project.Run{Version: "v2", Comments: []project.Comment{{Key: "again", Text: "Once more", T0: 0, T1: 1}}}); err != nil {
		t.Fatal(err)
	}
	res, err := http.Post(url+"p/promo/api/automation/tasks", "application/json", strings.NewReader(`{"notes":[1]}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	deadline = time.Now().Add(30 * time.Second)
	for len(v.Tasks) == 0 || v.Tasks[0].State != "completed" {
		if time.Now().After(deadline) {
			t.Fatalf("the task never completed: %+v\n%s", v.Tasks, w.stderr)
		}
		time.Sleep(100 * time.Millisecond)
		v = view{}
		getJSON(t, url+"p/promo/api/automation", &v)
	}
	if v.Tasks[0].Result != "v3" {
		t.Fatalf("result %q", v.Tasks[0].Result)
	}
	if _, err = os.Stat(filepath.Join(root, "clip_v3.mp4")); err != nil {
		t.Fatal("the agent's video isn't the next version file:", err)
	}
	n, err := st.Note(1)
	if err != nil || n.Status != project.StatusAnswered || n.AnsweredIn != "v3" || n.Change != "Made v2 again as v3." {
		t.Fatalf("note: %+v %v", n, err)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

func TestAgentsComeFromTheUserConfig(t *testing.T) {
	c, warnings, err := config.Parse(`find_agents = false
[[agents]]
id = "openrouter"
name = "Claude Code (OpenRouter)"
command = ["claude", "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"]
env = { ANTHROPIC_BASE_URL = "https://openrouter.ai/api", ANTHROPIC_AUTH_TOKEN = "$OPENROUTER_API_KEY", ANTHROPIC_API_KEY = "" }
`)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	ps := agentProfiles(Env{}, c)
	if len(ps) != 1 || ps[0].ID != "openrouter" || ps[0].Name != "Claude Code (OpenRouter)" ||
		ps[0].Env["ANTHROPIC_AUTH_TOKEN"] != "$OPENROUTER_API_KEY" || ps[0].Command[0] != "claude" {
		t.Fatalf("profiles: %+v", ps)
	}
	for _, bad := range []string{"[[agents]]\nid = \"a b\"\ncommand = [\"x\"]\n", "[[agents]]\nid = \"a\"\n",
		"[[agents]]\nid = \"a\"\ncommand = [\"x\"]\n[[agents]]\nid = \"a\"\ncommand = [\"y\"]\n"} {
		if _, _, err := config.Parse(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// default_agent goes first, so a project's first poll picks it (spec/home.md, "Settings").
func TestDefaultAgentFirst(t *testing.T) {
	c, _, err := config.Parse(`find_agents = false
default_agent = "b"
[[agents]]
id = "a"
command = ["a"]
[[agents]]
id = "b"
command = ["b"]
[[agents]]
id = "c"
command = ["c"]
`)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(c config.Config) string {
		var out []string
		for _, p := range agentProfiles(Env{}, c) {
			out = append(out, p.ID)
		}
		return strings.Join(out, " ")
	}
	if got := ids(c); got != "b a c" {
		t.Errorf("order: %s", got)
	}
	gone := "zz"
	c.DefaultAgent = &gone
	if got := ids(c); got != "a b c" {
		t.Errorf("an agent that isn't there: %s", got)
	}
}
