package cli

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
	"github.com/samahlstrom/retake/internal/worker"
)

// TestVideosEditAtTheSameTimeInTheirOwnHerdrWorkspaces: retake open on a folder of two videos, with agents set to open
// in herdr (internal/worker/testdata's fake herdr on PATH; no herdr server and no real agent). Both tasks run at once:
// each video's fake agent waits until the other's has started. Each completes from its report file and render alone,
// in its own workspace, with nothing read from the agent's terminal.
func TestVideosEditAtTheSameTimeInTheirOwnHerdrWorkspaces(t *testing.T) {
	editor, err := filepath.Abs("../../editor/target/release/retake-edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(editor); err != nil {
		t.Skip("build retake-edit to run the local editing test")
	}
	testmedia.Need(t)
	testdata, err := filepath.Abs("../worker/testdata")
	if err != nil {
		t.Fatal(err)
	}
	scratch, err := filepath.Abs("../../.retake-work")
	if err == nil {
		err = os.MkdirAll(scratch, 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(scratch, "agent-view-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	videos := filepath.Join(dir, "videos")
	timeline := `{"schema":"timeline/0.1","id":"view-fixture","rev":1,"format":{"size":[320,180],"fps":12,"sample_rate":48000,"background":"#000000"},"assets":{"vo":{"path":"voice.mp4"}},"spine":[{"id":"a","asset":"vo","in":0,"out":2},{"id":"b","asset":"vo","in":2,"out":4}],"scenes":[{"id":"scene","shots":[{"id":"first","at":"start:a","picture":{"background":"#aa3322","layers":[]}},{"id":"second","at":"start:b","picture":{"background":"#336633","layers":[]}}]}]}`
	names := []string{"alpha", "beta"}
	for _, name := range names {
		root := filepath.Join(videos, name)
		testmedia.Video(t, filepath.Join(root, name+"_v1.mp4"), 4) // one family per folder
		testmedia.Video(t, filepath.Join(root, "edit", "voice.mp4"), 4)
		if err = os.WriteFile(filepath.Join(root, "edit", "timeline.json"), []byte(timeline), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(dir, "herdr")
	for _, d := range []string{fake, filepath.Join(dir, "barrier"), filepath.Join(dir, "lifeline")} {
		if err = os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", testdata+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "no-herdr.sock"))
	t.Setenv("FAKE_HERDR_DIR", fake)
	t.Setenv("FAKE_AGENT", filepath.Join(testdata, "agent"))
	t.Setenv("FAKE_AGENT_MODE", "edit")
	t.Setenv("FAKE_BARRIER", filepath.Join(dir, "barrier"))
	t.Setenv("FAKE_LIFELINE", filepath.Join(dir, "lifeline"))
	t.Setenv("RETAKE_TEST_COLOUR", "blue")
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(dir, "lifeline"))
		files, _ := filepath.Glob(filepath.Join(fake, "pids-*"))
		for _, f := range files {
			b, _ := os.ReadFile(f)
			for _, pid := range strings.Fields(string(b)) {
				if n, e := strconv.Atoi(pid); e == nil {
					_ = syscall.Kill(n, syscall.SIGKILL)
				}
			}
		}
	})

	w := &world{root: dir, stdout: &buffer{}, stderr: &buffer{}}
	w.env = map[string]string{"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "HOME": dir, "USER": "osuser"}
	w.config(t, "idle_exit_minutes = 0\n[edit]\nretake_edit = \""+editor+"\"\n")
	env := w.environment(nil)
	env.Agents = func() []worker.Profile {
		return []worker.Profile{{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"},
			Command: []string{"claude", "-p"}, Interactive: []string{"claude", "--permission-mode", "acceptEdits"},
			OpenIn: worker.OpenHerdr, Env: map[string]string{"SHOT_COLOUR": "$RETAKE_TEST_COLOUR"}}}
	}
	type view struct {
		project.AutomationState
		Blocked string `json:"blocked"`
	}
	waitFor := func(url, what string, ok func(view) bool) {
		t.Helper()
		var v view
		for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			v = view{}
			if getJSON(t, url+"api/automation", &v) == 200 && ok(v) {
				return
			}
		}
		t.Fatalf("waiting for %s: %+v\nstderr: %s", what, v, w.stderr)
	}
	url, stop, done := w.serve(t, env, "open", videos, "--port", "0", "--no-open")
	stores := map[string]*project.Store{}
	for _, name := range names {
		waitFor(url+"p/"+name+"/", name+"'s worker", func(v view) bool {
			return v.Blocked == "" && len(v.Workers) == 1 && v.Config.Worker == v.Workers[0].ID
		})
		st, err := project.Open(filepath.Join(videos, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		stores[name] = st
		if _, err = st.Sync(project.Run{Version: "v1", Comments: []project.Comment{{Key: "blue", Text: "Make the first shot blue", T0: 0, T1: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		res, err := http.Post(url+"p/"+name+"/api/automation/tasks", "application/json", strings.NewReader(`{"notes":[1]}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("send %s: %d", name, res.StatusCode)
		}
	}
	for _, name := range names {
		waitFor(url+"p/"+name+"/", name+"'s edit", func(v view) bool {
			if len(v.Tasks) == 1 && v.Tasks[0].State != "running" && v.Tasks[0].State != "queued" && v.Tasks[0].State != "completed" {
				t.Fatalf("%s: %+v", name, v.Tasks[0])
			}
			return len(v.Tasks) == 1 && v.Tasks[0].State == "completed" && v.Tasks[0].Result == "v2" && v.Tasks[0].Usage == nil
		})
		if n, err := stores[name].Note(1); err != nil || n.AnsweredIn != "v2" || n.Change != "Made the first shot blue." {
			t.Fatalf("%s's note: %+v %v", name, n, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(fake, "argv.log"))
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for l := range strings.Lines(string(b)) {
		if args := strings.Split(l, "\x1f"); args[0] == "workspace" && args[1] == "create" {
			labels = append(labels, args[5])
		}
	}
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"retake · alpha", "retake · beta"}) {
		t.Fatalf("workspaces %q", labels)
	}
	stop()
	if code := wait(t, done); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr)
	}
}

// TestOpenAgentsInComesFromTheUserConfig: open_agents_in reaches the agents found on PATH and the configured ones
// that don't say, and an entry's own open_in wins.
func TestOpenAgentsInComesFromTheUserConfig(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	c, warnings, err := config.Parse(`open_agents_in = "herdr"
[[agents]]
id = "codex"
command = ["codex", "exec", "-"]
interactive = ["codex", "--model", "gpt-5.6-sol"]
open_in = "tmux"
[[agents]]
id = "script"
command = ["my-agent"]
`)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	got := map[string]worker.Profile{}
	for _, p := range agentProfiles(Env{}, c) {
		got[p.ID] = p
	}
	if p := got["codex"]; p.OpenIn != worker.OpenTmux || !slices.Equal(p.Interactive, []string{"codex", "--model", "gpt-5.6-sol"}) {
		t.Errorf("codex: %+v", p)
	}
	if p := got["script"]; p.OpenIn != worker.OpenHerdr || p.Interactive != nil {
		t.Errorf("script: %+v", p)
	}
	if p := got["claude"]; p.OpenIn != worker.OpenHerdr ||
		!slices.Equal(p.Interactive, []string{filepath.Join(bin, "claude"), "--dangerously-skip-permissions", "--disallowedTools=AskUserQuestion"}) {
		t.Errorf("claude: %+v", p)
	}
	none, _, _ := config.Parse("")
	for _, p := range agentProfiles(Env{}, none) {
		if p.OpenIn != "" {
			t.Errorf("%s opens in %q by default", p.ID, p.OpenIn)
		}
	}
}
