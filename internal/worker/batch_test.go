package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// Batches (spec/automation.md, "Batches"), end to end through a server and a worker on plain projects: one Send is one
// task, one agent process and one new version; comments sent while it works make one next batch in the background,
// and reach the same session in herdr and tmux; two videos run their batches at the same time.

// batchVideo is one plain video (<name>_v1.mp4 and five notes on v1) on a server, with a worker running these profiles.
type batchVideo struct {
	st    *project.Store
	local string
	log   *syncLog
}

func scratchDir(t *testing.T, prefix string) string {
	t.Helper()
	scratch, e := filepath.Abs("../../.retake-work")
	if e == nil {
		e = os.MkdirAll(scratch, 0o700)
	}
	if e != nil {
		t.Fatal(e)
	}
	base, e := os.MkdirTemp(scratch, prefix)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	return base
}

func startBatchVideo(t *testing.T, base, name string, profiles ...Profile) *batchVideo {
	t.Helper()
	local, hosted := filepath.Join(base, name), filepath.Join(base, name+"-hosted")
	testmedia.Video(t, filepath.Join(local, name+"_v1.mp4"), 2) // a family per video: never one across sibling folders
	st, e := project.Open(hosted)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = st.Close() })
	var cs []project.Comment
	for i := range 5 {
		cs = append(cs, project.Comment{Key: fmt.Sprintf("n%d", i+1), Text: fmt.Sprintf("Fix number %d", i+1), T0: float64(i) / 4, T1: float64(i)/4 + 0.2})
	}
	if _, e = st.Sync(project.Run{Version: "v1", Comments: cs}); e != nil {
		t.Fatal(e)
	}
	token, hash, e := team.NewToken()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = st.CreateToken(project.NewToken{Hash: hash, Role: "agent"}); e != nil {
		t.Fatal(e)
	}
	ts := httptest.NewUnstartedServer(nil)
	srv := server.New(server.Options{Root: hosted, Store: st, Port: ts.Listener.Addr().(*net.TCPAddr).Port, FollowNewest: true})
	ts.Config.Handler = srv
	ts.Start()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	log := &syncLog{}
	go func() {
		finished <- Run(ctx, Options{URL: ts.URL, Token: token, Root: local, Name: "Test machine", Profiles: profiles,
			Editor: bridge.Editor{Configured: filepath.Join(base, "no-retake-edit")}, Log: log})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(15 * time.Second):
			t.Error("worker did not stop")
		}
		ts.Close()
		_ = srv.Close()
	})
	v := &batchVideo{st: st, local: local, log: log}
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" && len(a.ListedFiles()) == 1 })
	return v
}

func (v *batchVideo) wait(t *testing.T, label string, test func(project.AutomationState) bool) project.AutomationState {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		a, err := v.st.Automation()
		if err != nil {
			t.Fatal(err)
		}
		if test(a) {
			return a
		}
	}
	a, _ := v.st.Automation()
	b, _ := json.Marshal(a)
	t.Fatalf("waiting for %s: %s\nworker log:\n%s", label, b, v.log)
	return project.AutomationState{}
}

func (v *batchVideo) send(t *testing.T, ids ...int64) {
	t.Helper()
	if _, err := v.st.QueueFeedback(ids, ""); err != nil {
		t.Fatal(err)
	}
}

// versions is the video's version files on the editing computer.
func (v *batchVideo) versions(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(v.local, "*_v*.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		files[i] = filepath.Base(files[i])
	}
	slices.Sort(files)
	return files
}

func (v *batchVideo) answeredIn(t *testing.T, ids ...int64) string {
	t.Helper()
	label := ""
	for _, id := range ids {
		n, err := v.st.Note(id)
		if err != nil || n.Status != project.StatusAnswered || label != "" && n.AnsweredIn != label {
			t.Fatalf("note %d: %+v %v", id, n, err)
		}
		label = n.AnsweredIn
	}
	return label
}

func lines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return slices.DeleteFunc(strings.Split(string(b), "\n"), func(l string) bool { return l == "" })
}

func batchProfile(t *testing.T, dir string, peers int) Profile {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Profile{AgentProfile: project.AgentProfile{ID: "batch", Name: "Batch"},
		Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "batch"},
		Env:     map[string]string{"BATCH_DIR": dir, "BATCH_PEERS": fmt.Sprint(peers)}}
}

// TestSendIsOneAgentRunAndOneVersion: three notes sent at once are one task, one agent process and one new version.
// Two more sent one at a time while it works (a background agent can't be given them) make one next batch, which
// starts when the first ends and makes the next version: two launches and two versions for five comments.
func TestSendIsOneAgentRunAndOneVersion(t *testing.T) {
	testmedia.Need(t)
	base := scratchDir(t, "batch-worker-test-")
	gate := filepath.Join(base, "gate")
	if err := os.MkdirAll(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	v := startBatchVideo(t, base, "shop", batchProfile(t, gate, 1))
	launches := filepath.Join(gate, "launches")
	v.send(t, 1, 2, 3)
	v.wait(t, "the agent to start", func(project.AutomationState) bool { return len(lines(t, launches)) == 1 })
	v.send(t, 4)
	v.send(t, 5)
	a, _ := v.st.Automation()
	if len(a.Tasks) != 2 || a.Tasks[0].State != "running" || !slices.Equal(a.Tasks[0].Notes, []int64{1, 2, 3}) ||
		a.Tasks[1].State != "queued" || !slices.Equal(a.Tasks[1].Notes, []int64{4, 5}) {
		b, _ := json.Marshal(a.Tasks)
		t.Fatalf("one running batch and one next batch: %s", b)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	first := a.Tasks[0].ID
	v.wait(t, "the first batch", func(a project.AutomationState) bool {
		return a.Task(first) != nil && a.Task(first).State == "completed"
	})
	if got := v.answeredIn(t, 1, 2, 3); got != "v2" {
		t.Fatalf("the first batch's version: %s", got)
	}
	if n, _ := v.st.Note(1); n.Change != "Fixed note 1" {
		t.Fatalf("note 1's own change: %q", n.Change)
	}
	if n, _ := v.st.Note(2); n.Change != "Did the batch: notes 1, 2, 3" {
		t.Fatalf("note 2's change: %q", n.Change)
	}
	a = v.wait(t, "the next batch", func(a project.AutomationState) bool {
		return len(a.Tasks) == 2 && a.Tasks[1].State == "completed"
	})
	if got := v.answeredIn(t, 4, 5); got != "v3" {
		t.Fatalf("the next batch's version: %s", got)
	}
	if got := lines(t, launches); !slices.Equal(got, []string{"shop [1 2 3]", "shop [4 5]"}) {
		t.Fatalf("agent launches: %q", got)
	}
	if got := v.versions(t); !slices.Equal(got, []string{"shop_v1.mp4", "shop_v2.mp4", "shop_v3.mp4"}) {
		t.Fatalf("versions: %q", got)
	}
}

// TestTwoVideosRunTheirBatchesAtTheSameTime: each video has its own agent; neither waits for the other (each agent
// waits until both have started).
func TestTwoVideosRunTheirBatchesAtTheSameTime(t *testing.T) {
	testmedia.Need(t)
	base := scratchDir(t, "batch-parallel-test-")
	gate := filepath.Join(base, "gate")
	if err := os.MkdirAll(gate, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alpha := startBatchVideo(t, base, "alpha", batchProfile(t, gate, 2))
	beta := startBatchVideo(t, base, "beta", batchProfile(t, gate, 2))
	alpha.send(t, 1, 2, 3)
	beta.send(t, 1, 2, 3)
	for _, v := range []*batchVideo{alpha, beta} {
		v.wait(t, "its batch", func(a project.AutomationState) bool { return len(a.Tasks) == 1 && a.Tasks[0].State == "completed" })
		if got := v.answeredIn(t, 1, 2, 3); got != "v2" {
			t.Fatalf("version: %s", got)
		}
		if got := v.versions(t); len(got) != 2 || !strings.HasSuffix(got[1], "_v2.mp4") {
			t.Fatalf("versions: %q", got)
		}
	}
	got := lines(t, filepath.Join(gate, "launches"))
	slices.Sort(got)
	if !slices.Equal(got, []string{"alpha [1 2 3]", "beta [1 2 3]"}) {
		t.Fatalf("launches: %q", got)
	}
}

// TestCommentsSentMidRunReachTheSameSession: in herdr and in tmux, two comments sent while the agent works reach its
// session as one more message for the same task, with a new report file; the agent's one report finishes all five
// in one version. The agent is started once.
func TestCommentsSentMidRunReachTheSameSession(t *testing.T) {
	testmedia.Need(t)
	for _, mode := range []string{OpenHerdr, OpenTmux} {
		t.Run(mode, func(t *testing.T) {
			dir := fakeViews(t)
			gate := filepath.Join(dir, "barrier")
			if err := os.Mkdir(gate, 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("FAKE_AGENT_MODE", "batch")
			t.Setenv("FAKE_BARRIER", gate)
			agent, err := filepath.Abs("testdata/agent")
			if err != nil {
				t.Fatal(err)
			}
			p := Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"}, Command: []string{agent},
				Interactive: []string{agent}, OpenIn: mode}
			if mode == OpenHerdr {
				p.Interactive = []string{"claude"}
			}
			v := startBatchVideo(t, scratchDir(t, "batch-session-test-"), "shop", p)
			v.send(t, 1, 2, 3)
			v.wait(t, "the agent to start", func(project.AutomationState) bool {
				return len(lines(t, filepath.Join(gate, "launches"))) == 1
			})
			v.send(t, 4, 5)
			seen := filepath.Join(dir, "seen")
			v.wait(t, "the comments to reach the session", func(project.AutomationState) bool {
				return slices.ContainsFunc(lines(t, seen), func(l string) bool { return strings.HasPrefix(l, "More comments for Retake task") })
			})
			a := v.wait(t, "the server to move them into the task", func(a project.AutomationState) bool {
				return len(a.Tasks) == 1 && len(a.Tasks[0].Notes) == 5
			})
			task := a.Tasks[0]
			if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			v.wait(t, "the batch", func(a project.AutomationState) bool { return len(a.Tasks) == 1 && a.Tasks[0].State == "completed" })
			if got := v.answeredIn(t, 1, 2, 3, 4, 5); got != "v2" {
				t.Fatalf("version: %s", got)
			}
			if got := v.versions(t); !slices.Equal(got, []string{"shop_v1.mp4", "shop_v2.mp4"}) {
				t.Fatalf("versions: %q", got)
			}
			// The message names the same task and a new prompt, whose report file replaces the first one.
			more := filepath.Join(v.local, ".retake", "worker", "tasks", task.ID, "prompt-1-2.md")
			want := fmt.Sprintf("More comments for Retake task %s (attempt 1): follow the instructions in %s", task.ID, more)
			if got := lines(t, seen); !slices.Contains(got, want) {
				t.Fatalf("seen %q, want %q", got, want)
			}
			prompt, err := os.ReadFile(more)
			if err != nil || !strings.Contains(string(prompt), "note IDs 4, 5") ||
				!strings.Contains(string(prompt), filepath.Join(filepath.Dir(more), "report-1-2.json")) {
				t.Fatalf("prompt: %s %v", prompt, err)
			}
			var c plainContext
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(more), "context-1-2.json")); err != nil || json.Unmarshal(b, &c) != nil ||
				!slices.Equal(c.Task.Notes, []int64{1, 2, 3, 4, 5}) || c.Output == "" {
				t.Fatalf("context: %+v %v", c.Task, err)
			}
			// One agent: started once, given the task's line and then the comments.
			starts := 0
			for _, call := range calls(t, filepath.Join(dir, mode, "argv.log")) {
				if call[0] == "agent" && call[1] == "start" || call[0] == "new-session" || call[0] == "new-window" || call[0] == "respawn-pane" {
					starts++
				}
			}
			if starts != 1 {
				t.Fatalf("the agent started %d times", starts)
			}
			if got := lines(t, filepath.Join(gate, "launches")); len(got) != 1 {
				t.Fatalf("launches: %q", got)
			}
		})
	}
}
