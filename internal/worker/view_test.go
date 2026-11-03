package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// syncLog is a worker log the test can read while the worker writes it.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// fakeViews puts testdata's fake herdr and tmux first on PATH, with their state in a fresh folder, and returns it.
// The real herdr and tmux, were they ever run, would find no server: HERDR_SOCKET_PATH and TMUX_TMPDIR point here.
func fakeViews(t *testing.T) string {
	t.Helper()
	testmedia.NeedShell(t) // the fake herdr, tmux and agent; herdr on Windows is covered by CI's smoke test

	testdata, e := filepath.Abs("testdata")
	if e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	for _, d := range []string{"herdr", "tmux", "lifeline"} {
		if e = os.Mkdir(filepath.Join(dir, d), 0o700); e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("PATH", testdata+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_HERDR_DIR", filepath.Join(dir, "herdr"))
	t.Setenv("FAKE_TMUX_DIR", filepath.Join(dir, "tmux"))
	t.Setenv("FAKE_AGENT", filepath.Join(testdata, "agent"))
	t.Setenv("FAKE_AGENT_SEEN", filepath.Join(dir, "seen"))
	t.Setenv("FAKE_AGENT_MODE", "question")
	t.Setenv("FAKE_LIFELINE", filepath.Join(dir, "lifeline"))
	t.Setenv("HERDR_SOCKET_PATH", filepath.Join(dir, "no-herdr.sock"))
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("RETAKE_TEST_COLOUR", "blue")
	oldPoll, oldCheck := viewPoll, viewCheck
	viewPoll, viewCheck = 20*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() {
		viewPoll, viewCheck = oldPoll, oldCheck
		_ = os.Remove(filepath.Join(dir, "lifeline"))
		for _, glob := range []string{"herdr/pids-*", "tmux/pid-*", "tmux/typed-*"} {
			files, _ := filepath.Glob(filepath.Join(dir, glob))
			for _, f := range files {
				b, _ := os.ReadFile(f)
				for _, pid := range strings.Fields(string(b)) {
					if n, e := strconv.Atoi(pid); e == nil {
						if p, e := os.FindProcess(n); e == nil {
							_ = p.Kill()
						}
					}
				}
			}
		}
	})
	return dir
}

// calls is what a fake was run with, one argv per call.
func calls(t *testing.T, path string) [][]string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil && !os.IsNotExist(e) {
		t.Fatal(e)
	}
	var out [][]string
	for l := range strings.Lines(string(b)) {
		out = append(out, strings.Split(strings.TrimSuffix(strings.TrimSuffix(l, "\n"), "\x1f"), "\x1f"))
	}
	return out
}

// since drops the polls (herdr agent get, tmux list-panes) from the calls after the first n.
func since(all [][]string, n int) [][]string {
	var out [][]string
	for _, c := range all[n:] {
		if c[0] == "list-panes" || len(c) > 1 && c[0] == "agent" && c[1] == "get" && len(out) > 0 && out[len(out)-1][1] == "prompt" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func sameCalls(t *testing.T, label string, got, want [][]string) {
	t.Helper()
	if !slices.EqualFunc(got, want, slices.Equal) {
		g, _ := json.MarshalIndent(got, "", " ")
		w, _ := json.MarshalIndent(want, "", " ")
		t.Fatalf("%s: calls\n%s\nwant\n%s", label, g, w)
	}
}

func viewRunner(t *testing.T, name string, profiles ...Profile) (*runner, *syncLog) {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if e := os.MkdirAll(filepath.Join(root, ".retake", "worker"), 0o700); e != nil {
		t.Fatal(e)
	}
	log := &syncLog{}
	return &runner{opts: Options{Root: root, Profiles: profiles, Log: log}, dir: filepath.Join(root, ".retake", "worker"),
		timeline: filepath.Join(root, "edit", "timeline.json")}, log
}

func task(id string) project.WorkAssignment {
	return project.WorkAssignment{Task: &project.EditingTask{ID: id, Agent: "claude", Attempt: 1}}
}

func lineFor(r *runner, id string) string {
	return "New Retake task for " + filepath.Base(r.opts.Root) + " (task " + id + ", attempt 1): follow the instructions in " +
		filepath.Join(r.dir, "tasks", id, "prompt-1.md")
}

var claudeInHerdr = Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"},
	Command:     []string{"claude", "-p", "--output-format", "stream-json"},
	Interactive: []string{"claude", "--permission-mode", "acceptEdits"}, OpenIn: OpenHerdr,
	Env: map[string]string{"SHOT_COLOUR": "$RETAKE_TEST_COLOUR"}}

// TestHerdrRunsEachVideosAgentInItsOwnWorkspace: the exact herdr commands, completion from the report alone (no
// agent output at all), and the next task reusing the session.
func TestHerdrRunsEachVideosAgentInItsOwnWorkspace(t *testing.T) {
	dir := fakeViews(t)
	r, _ := viewRunner(t, "item-shop", claudeInHerdr)
	argv := filepath.Join(dir, "herdr", "argv.log")
	name := herdrName(r.opts.Root)
	if !strings.HasPrefix(name, "retake-item-shop-") || len(name) != len("retake-item-shop-")+4 {
		t.Fatal(name)
	}
	got := r.execute(context.Background(), task("t1"))
	if got.state != "question" || got.message != "Blue or teal?" || got.usage != nil {
		t.Fatalf("%+v", got)
	}
	sameCalls(t, "first task", since(calls(t, argv), 0), [][]string{
		{"workspace", "create", "--cwd", r.opts.Root, "--label", "retake · item-shop", "--no-focus", "--env", "SHOT_COLOUR=blue"},
		{"agent", "start", name, "--kind", "claude", "--pane", "w1:p2", "--timeout", "60000", "--", "--permission-mode", "acceptEdits"},
		{"agent", "prompt", name, lineFor(r, "t1")},
		{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"},
	})
	if _, e := os.Stat(filepath.Join(r.dir, "tasks", "t1", "agent-1.log")); !os.IsNotExist(e) {
		t.Fatal("an interactive agent's output was captured", e)
	}
	prompt, e := os.ReadFile(filepath.Join(r.dir, "tasks", "t1", "prompt-1.md"))
	if e != nil || !strings.Contains(string(prompt), filepath.Join(r.dir, "tasks", "t1", "report-1.json")) ||
		!strings.Contains(string(prompt), "interactive session") {
		t.Fatalf("prompt: %s %v", prompt, e)
	}
	var saved viewState
	if b, e := os.ReadFile(filepath.Join(r.dir, "view.json")); e != nil || json.Unmarshal(b, &saved) != nil ||
		saved != (viewState{Mode: OpenHerdr, Profile: "claude", Pane: "w1:p2", Workspace: "w1", Agent: name}) {
		t.Fatalf("view.json: %+v %v", saved, e)
	}

	// The next task for this video goes to the agent that's still open there.
	n := len(calls(t, argv))
	if got = r.execute(context.Background(), task("t2")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	sameCalls(t, "second task", since(calls(t, argv), n), [][]string{
		{"pane", "get", "w1:p2"},
		{"agent", "get", name},
		{"agent", "prompt", name, lineFor(r, "t2")},
		{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"},
	})
}

// TestStoppingAHerdrTaskClosesItsPane: pause or switch before the report closes the agent's pane, and the next task
// opens a new tab in the video's workspace.
func TestStoppingAHerdrTaskClosesItsPane(t *testing.T) {
	dir := fakeViews(t)
	t.Setenv("FAKE_AGENT_MODE", "hang")
	r, log := viewRunner(t, "item-shop", claudeInHerdr)
	argv := filepath.Join(dir, "herdr", "argv.log")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan outcome, 1)
	go func() { done <- r.execute(ctx, task("t1")) }()
	pids := filepath.Join(dir, "herdr", "pids-w1:p2")
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0; time.Sleep(20 * time.Millisecond) {
		if b, e := os.ReadFile(pids); e == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never got its line", log)
		}
	}
	cancel(errStop) // a person's pause or switch
	select {
	case got := <-done:
		if got.state != "stopped" {
			t.Fatalf("%+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the task didn't stop")
	}
	all := calls(t, argv)
	if last := all[len(all)-1]; !slices.Equal(last, []string{"pane", "close", "w1:p2"}) {
		t.Fatalf("last call %q", last)
	}
	time.Sleep(100 * time.Millisecond)
	if procStart(pid) != "" {
		t.Fatal("the agent kept running after its pane was closed")
	}

	t.Setenv("FAKE_AGENT_MODE", "question")
	n := len(all)
	if got := r.execute(context.Background(), task("t2")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	name := herdrName(r.opts.Root)
	sameCalls(t, "after a stop", since(calls(t, argv), n), [][]string{
		{"workspace", "get", "w1"},
		{"tab", "create", "--workspace", "w1", "--cwd", r.opts.Root, "--label", "retake · item-shop", "--no-focus", "--env", "SHOT_COLOUR=blue"},
		{"agent", "start", name, "--kind", "claude", "--pane", "w1:p3", "--timeout", "60000", "--", "--permission-mode", "acceptEdits"},
		{"agent", "prompt", name, lineFor(r, "t2")},
		{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"},
	})
}

func TestHerdrSessionEndingWithoutAReportPausesTheTask(t *testing.T) {
	fakeViews(t)
	t.Setenv("FAKE_AGENT_MODE", "exit")
	r, _ := viewRunner(t, "item-shop", claudeInHerdr)
	got := r.execute(context.Background(), task("t1"))
	if got.state != "paused" || !strings.Contains(got.message, "session ended without a report") {
		t.Fatalf("%+v", got)
	}
}

// TestHerdrWaitsForTheAgentsStartupQuestion: an agent stopped at "trust this folder?" gets its line once the person
// has answered it in herdr.
func TestHerdrWaitsForTheAgentsStartupQuestion(t *testing.T) {
	dir := fakeViews(t)
	if e := os.WriteFile(filepath.Join(dir, "herdr", "not-ready"), nil, 0o600); e != nil {
		t.Fatal(e)
	}
	r, log := viewRunner(t, "item-shop", claudeInHerdr)
	if got := r.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	name := herdrName(r.opts.Root)
	got := since(calls(t, filepath.Join(dir, "herdr", "argv.log")), 1)
	sameCalls(t, "startup question", got[1:], [][]string{
		{"agent", "wait", name, "--until", "idle", "--until", "done"},
		{"agent", "prompt", name, lineFor(r, "t1")},
		{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"},
	})
	if !strings.Contains(log.String(), "item-shop: the agent is waiting for you in herdr (retake · item-shop)") {
		t.Fatal(log)
	}
}

// TestHerdrSendsALostLineAgain: right after its startup question, an agent can lose the line typed into it and sit
// idle. The line goes again, once, and the task carries on.
func TestHerdrSendsALostLineAgain(t *testing.T) {
	dir := fakeViews(t)
	for _, f := range []string{"not-ready", "drop-prompt"} {
		if e := os.WriteFile(filepath.Join(dir, "herdr", f), nil, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	r, log := viewRunner(t, "item-shop", claudeInHerdr)
	if got := r.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	name := herdrName(r.opts.Root)
	wait := []string{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"}
	got := since(calls(t, filepath.Join(dir, "herdr", "argv.log")), 1)
	sameCalls(t, "lost line", got[1:], [][]string{
		{"agent", "wait", name, "--until", "idle", "--until", "done"},
		{"agent", "prompt", name, lineFor(r, "t1")},
		wait,
		{"agent", "prompt", name, lineFor(r, "t1")},
		wait,
	})
	if !strings.Contains(log.String(), "item-shop: the agent in herdr (retake · item-shop) didn't start on its line; sending it again") {
		t.Fatal(log)
	}
}

// TestSwitchingAgentsReplacesTheVideosSession: a different agent closes the old one's pane and starts in a new tab.
func TestSwitchingAgentsReplacesTheVideosSession(t *testing.T) {
	dir := fakeViews(t)
	codex := Profile{AgentProfile: project.AgentProfile{ID: "codex", Name: "Codex"}, Command: []string{"codex", "exec"},
		Interactive: []string{"/opt/bin/codex", "--sandbox", "workspace-write", "-c", `approval_policy="never"`}, OpenIn: OpenHerdr}
	r, _ := viewRunner(t, "item-shop", claudeInHerdr, codex)
	argv := filepath.Join(dir, "herdr", "argv.log")
	if got := r.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	n := len(calls(t, argv))
	a := task("t2")
	a.Task.Agent = "codex"
	if got := r.execute(context.Background(), a); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	name := herdrName(r.opts.Root)
	sameCalls(t, "switch", since(calls(t, argv), n), [][]string{
		{"pane", "get", "w1:p2"},
		{"pane", "close", "w1:p2"},
		{"workspace", "get", "w1"},
		{"tab", "create", "--workspace", "w1", "--cwd", r.opts.Root, "--label", "retake · item-shop", "--no-focus"},
		{"agent", "start", name, "--kind", "codex", "--pane", "w1:p3", "--timeout", "60000", "--", "--sandbox", "workspace-write", "-c", `approval_policy="never"`},
		{"agent", "prompt", name, lineFor(r, "t2")},
		{"agent", "wait", name, "--until", "working", "--until", "blocked", "--until", "done", "--timeout", "15000"},
	})
}

// TestAgentsWithoutAnInteractiveCommandStayInTheBackground: a configured agent can't open in herdr without one.
func TestAgentsWithoutAnInteractiveCommandStayInTheBackground(t *testing.T) {
	dir := fakeViews(t)
	bin, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	p := Profile{AgentProfile: project.AgentProfile{ID: "claude"}, Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "question"}, OpenIn: OpenHerdr}
	r, log := viewRunner(t, "item-shop", p)
	if got := r.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	if got := r.execute(context.Background(), task("t2")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	if len(calls(t, filepath.Join(dir, "herdr", "argv.log"))) != 0 {
		t.Fatal("ran herdr")
	}
	if strings.Count(log.String(), "Agent claude has no interactive command, so it runs in the background, not in herdr") != 1 {
		t.Fatal(log)
	}
}

func TestHerdrMissingPausesTheTask(t *testing.T) {
	fakeViews(t)
	t.Setenv("PATH", t.TempDir())
	r, _ := viewRunner(t, "item-shop", claudeInHerdr)
	if got := r.execute(context.Background(), task("t1")); got.state != "paused" || got.message != "herdr isn't installed, but this agent is set to open in it" {
		t.Fatalf("%+v", got)
	}
}

func tmuxProfile(t *testing.T, id string) Profile {
	t.Helper()
	agent, e := filepath.Abs("testdata/agent")
	if e != nil {
		t.Fatal(e)
	}
	return Profile{AgentProfile: project.AgentProfile{ID: id, Name: "Fake"}, Command: []string{agent},
		Interactive: []string{agent, "--permission-mode", "acceptEdits"}, OpenIn: OpenTmux,
		Env: map[string]string{"SHOT_COLOUR": "$RETAKE_TEST_COLOUR"}}
}

// TestTmuxRunsEachVideosAgentInAWindow: the exact tmux commands for two videos, the next task typed into the running
// agent, another agent respawning the pane, and a stop killing it.
func TestTmuxRunsEachVideosAgentInAWindow(t *testing.T) {
	dir := fakeViews(t)
	argv := filepath.Join(dir, "tmux", "argv.log")
	p := tmuxProfile(t, "claude")
	r, _ := viewRunner(t, "item-shop", p)
	if got := r.execute(context.Background(), task("t1")); got.state != "question" || got.usage != nil {
		t.Fatalf("%+v", got)
	}
	agent := p.Interactive[0]
	sameCalls(t, "first video", since(calls(t, argv), 0), [][]string{
		{"has-session", "-t", "=retake"},
		{"new-session", "-d", "-s", "retake", "-P", "-F", "#{pane_id}", "-n", "item-shop", "-c", r.opts.Root, "-e", "SHOT_COLOUR=blue",
			"--", agent, "--permission-mode", "acceptEdits", lineFor(r, "t1")},
		{"set-option", "-w", "-t", "%1", "remain-on-exit", "on"},
	})
	other, _ := viewRunner(t, "dash-cam", p)
	n := len(calls(t, argv))
	if got := other.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	sameCalls(t, "second video", since(calls(t, argv), n), [][]string{
		{"has-session", "-t", "=retake"},
		{"new-window", "-d", "-t", "=retake:", "-P", "-F", "#{pane_id}", "-n", "dash-cam", "-c", other.opts.Root, "-e", "SHOT_COLOUR=blue",
			"--", agent, "--permission-mode", "acceptEdits", lineFor(other, "t1")},
		{"set-option", "-w", "-t", "%2", "remain-on-exit", "on"},
	})

	n = len(calls(t, argv))
	if got := r.execute(context.Background(), task("t2")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	sameCalls(t, "next task", since(calls(t, argv), n), [][]string{
		{"send-keys", "-t", "%1", "-l", lineFor(r, "t2")},
		{"send-keys", "-t", "%1", "Enter"},
	})

	// Another agent replaces this one in the same pane. Stopped before its report, its pane is killed.
	t.Setenv("FAKE_AGENT_MODE", "hang")
	codex := tmuxProfile(t, "codex")
	r.opts.Profiles = append(r.opts.Profiles, codex)
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan outcome, 1)
	a := task("t3")
	a.Task.Agent = "codex"
	n = len(calls(t, argv))
	go func() { done <- r.execute(ctx, a) }()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if b, _ := os.ReadFile(filepath.Join(dir, "seen")); strings.Contains(string(b), "(task t3,") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never started")
		}
	}
	cancel(errStop) // a person's pause or switch
	if got := <-done; got.state != "stopped" {
		t.Fatalf("%+v", got)
	}
	sameCalls(t, "switch and stop", since(calls(t, argv), n), [][]string{
		{"respawn-pane", "-k", "-t", "%1", "-c", r.opts.Root, "-e", "SHOT_COLOUR=blue", "--", agent, "--permission-mode", "acceptEdits", lineFor(r, "t3")},
		{"kill-pane", "-t", "%1"},
	})
}

func TestTmuxSessionEndingWithoutAReportPausesTheTask(t *testing.T) {
	fakeViews(t)
	t.Setenv("FAKE_AGENT_MODE", "exit")
	r, _ := viewRunner(t, "item-shop", tmuxProfile(t, "claude"))
	if got := r.execute(context.Background(), task("t1")); got.state != "paused" || !strings.Contains(got.message, "session ended without a report") {
		t.Fatalf("%+v", got)
	}
}

// TestTmuxForReal runs the tmux mode against a real, private tmux server (its own socket folder; no window opens):
// a window, a line typed into the running agent, and a stop.
func TestTmuxForReal(t *testing.T) {
	if _, e := exec.LookPath("tmux"); e != nil {
		t.Skip("tmux isn't installed")
	}
	path := os.Getenv("PATH")
	dir := fakeViews(t)
	t.Setenv("PATH", path) // the real tmux
	// A private server, in a short folder: a socket path has a length limit.
	sock, e := os.MkdirTemp("", "rtx")
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("TMUX_TMPDIR", sock)
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-server").Run()
		_ = os.RemoveAll(sock)
	})
	r, _ := viewRunner(t, "item-shop", tmuxProfile(t, "claude"))
	if got := r.execute(context.Background(), task("t1")); got.state != "question" {
		t.Fatalf("%+v", got)
	}
	if got := r.execute(context.Background(), task("t2")); got.state != "question" {
		t.Fatalf("typed line: %+v", got)
	}
	seen, _ := os.ReadFile(filepath.Join(dir, "seen"))
	if strings.Count(string(seen), "New Retake task for item-shop") != 2 {
		t.Fatalf("the agent saw %q", seen)
	}
	out, e := exec.Command("tmux", "list-windows", "-t", "=retake", "-F", "#{window_name}").Output()
	if e != nil || strings.TrimSpace(string(out)) != "item-shop" {
		t.Fatalf("windows %q %v", out, e)
	}
	t.Setenv("FAKE_AGENT_MODE", "hang")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errStop) // a person's pause or switch
	if got := r.execute(ctx, task("t3")); got.state != "stopped" {
		t.Fatalf("%+v", got)
	}
	if out, _ := exec.Command("tmux", "list-panes", "-a").Output(); len(out) != 0 {
		t.Fatalf("pane left after a stop: %s", out)
	}
}
