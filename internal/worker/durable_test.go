package worker

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// Agents outlive Retake (spec/automation.md): a worker that quits leaves its agent working, and the next one takes the
// attempt back from its record, through a server over HTTP (a new session each time, as retake worker and retake
// connect have): herdr and tmux sessions, a detached background agent, and a chat turn. A person's stop still stops
// them (view_test.go, worker_test.go and the chat's stop below), and no test leaves a process behind.

// waitFor polls cond for up to 30 s.
func waitFor(t *testing.T, label string, log fmt.Stringer, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !cond(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s\nworker log:\n%s", label, log)
		}
	}
}

// record is the run.json of the video's only task.
func (v *batchVideo) record(t *testing.T) *runRecord {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(v.local, ".retake", "worker", "tasks", "*", recordFile))
	if len(paths) != 1 {
		return nil
	}
	return readRecord(paths[0])
}

func (v *batchVideo) task(t *testing.T) project.EditingTask {
	t.Helper()
	a, err := v.st.Automation()
	if err != nil || len(a.Tasks) != 1 {
		t.Fatalf("tasks %+v %v", a.Tasks, err)
	}
	return a.Tasks[0]
}

// herdrBatch is the fake agent in herdr (testdata/agent, batch: it waits for $FAKE_BARRIER/go, then makes the next
// version and reports).
func herdrBatch(t *testing.T, mode string) (Profile, string) {
	t.Helper()
	agent, err := filepath.Abs("testdata/agent")
	if err != nil {
		t.Fatal(err)
	}
	barrier := t.TempDir()
	t.Setenv("FAKE_AGENT_MODE", "batch")
	t.Setenv("FAKE_BARRIER", barrier)
	return Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"}, Command: []string{agent},
		Interactive: []string{agent}, OpenIn: mode}, barrier
}

// TestHerdrAgentOutlivesItsWorker: a worker quitting mid-task leaves the agent's pane open and its process running;
// the task stays Working. The next worker (a new session) takes the attempt back at once with its lease: no new prompt,
// no line typed into the agent. The agent then reports, and that worker makes v2.
func TestHerdrAgentOutlivesItsWorker(t *testing.T) {
	testmedia.Need(t)
	dir := fakeViews(t)
	p, barrier := herdrBatch(t, OpenHerdr)
	v := serveVideo(t, scratchDir(t, "durable-herdr-"), "shop", 1)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	v.send(t, 1)
	waitFor(t, "the agent's line", v.log, func() bool { return len(lines(t, filepath.Join(barrier, "launches"))) == 1 })
	waitFor(t, "the record", v.log, func() bool { rec := v.record(t); return rec != nil && rec.Pane != "" })
	stop()

	argv := filepath.Join(dir, "herdr", "argv.log")
	for _, c := range calls(t, argv) {
		if slices.Equal(c[:min(2, len(c))], []string{"pane", "close"}) {
			t.Fatal("quitting closed the agent's pane")
		}
	}
	rec := v.record(t)
	pids := lines(t, filepath.Join(dir, "herdr", "pids-"+rec.Pane))
	if len(pids) != 1 || exec.Command("kill", "-0", pids[0]).Run() != nil {
		t.Fatalf("the agent didn't outlive its worker: %v", pids)
	}
	if j := v.task(t); j.State != "running" {
		t.Fatalf("the task isn't Working after its worker quit: %+v", j)
	}

	n := len(calls(t, argv))
	v.start(t, p)
	waitFor(t, "the task taken back", v.log, func() bool { return strings.Contains(v.log.String(), "took task") })
	if j := v.task(t); j.State != "running" || j.Lease != rec.Lease || j.Attempt != 1 {
		t.Fatalf("not the same attempt: %+v", j)
	}
	if err := os.WriteFile(filepath.Join(barrier, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v.wait(t, "v2", func(a project.AutomationState) bool { return a.Tasks[0].State == "completed" })
	if got := v.answeredIn(t, 1); got != "v2" {
		t.Fatal(got)
	}
	for _, c := range since(calls(t, argv), n) {
		if c[0] == "agent" && (c[1] == "prompt" || c[1] == "start") || c[0] == "workspace" || c[0] == "tab" {
			t.Fatalf("the next worker gave the agent something: %q", c)
		}
	}
	if got := lines(t, filepath.Join(barrier, "launches")); len(got) != 1 {
		t.Fatalf("launches %q", got)
	}
	if rec := v.record(t); rec == nil || !rec.Done {
		t.Fatalf("the record isn't done: %+v", rec)
	}
}

// TestReportWrittenWhileRetakeWasDown: in tmux, the agent reports while no worker runs; the next worker reads the
// report as if it had watched, and makes v2. The pane is never killed and nothing is typed into it.
func TestReportWrittenWhileRetakeWasDown(t *testing.T) {
	testmedia.Need(t)
	dir := fakeViews(t)
	p, barrier := herdrBatch(t, OpenTmux)
	v := serveVideo(t, scratchDir(t, "durable-tmux-"), "shop", 1)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	v.send(t, 1)
	waitFor(t, "the agent's line", v.log, func() bool { return len(lines(t, filepath.Join(barrier, "launches"))) == 1 })
	waitFor(t, "the record", v.log, func() bool { rec := v.record(t); return rec != nil && rec.Pane != "" })
	stop()
	rec := v.record(t)
	if err := os.WriteFile(filepath.Join(barrier, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the report", v.log, func() bool { return reported(rec.Report) })

	argv := filepath.Join(dir, "tmux", "argv.log")
	n := len(calls(t, argv))
	v.start(t, p)
	v.wait(t, "v2", func(a project.AutomationState) bool { return a.Tasks[0].State == "completed" })
	if got := v.answeredIn(t, 1); got != "v2" {
		t.Fatal(got)
	}
	for _, c := range calls(t, argv) {
		if c[0] == "kill-pane" || c[0] == "respawn-pane" {
			t.Fatalf("the pane was replaced: %q", c)
		}
	}
	for _, c := range since(calls(t, argv), n) {
		if c[0] != "list-panes" {
			t.Fatalf("the next worker typed into the session: %q", c)
		}
	}
}

// TestDeadAgentPausesTheTask: an agent that ended without a report while no worker ran pauses its task, as when the
// worker watched its session end.
func TestDeadAgentPausesTheTask(t *testing.T) {
	testmedia.Need(t)
	fakeViews(t)
	p, barrier := herdrBatch(t, OpenHerdr)
	v := serveVideo(t, scratchDir(t, "durable-dead-"), "shop", 1)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	v.send(t, 1)
	waitFor(t, "the agent's line", v.log, func() bool { return len(lines(t, filepath.Join(barrier, "launches"))) == 1 })
	waitFor(t, "the record", v.log, func() bool { rec := v.record(t); return rec != nil && rec.Pane != "" })
	stop()
	// The person closes the pane while Retake is down.
	if out, err := exec.Command("herdr", "pane", "close", v.record(t).Pane).CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	v.start(t, p)
	v.wait(t, "the pause", func(a project.AutomationState) bool { return a.Tasks[0].State == "paused" })
	if j := v.task(t); !strings.Contains(j.Error, "session ended without a report") || j.Lost {
		t.Fatalf("%+v", j)
	}
}

// TestAnOlderWorkersTaskIsTakenBack: a task an older Retake left (no run.json; paused "Worker disconnected" without
// lost), whose agent still works in herdr, is taken back from its context file and view.json when the new worker
// starts, and finishes there.
func TestAnOlderWorkersTaskIsTakenBack(t *testing.T) {
	testmedia.Need(t)
	fakeViews(t)
	p, barrier := herdrBatch(t, OpenHerdr)
	v := serveVideo(t, scratchDir(t, "durable-older-"), "shop", 1)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	v.send(t, 1)
	waitFor(t, "the agent's line", v.log, func() bool { return len(lines(t, filepath.Join(barrier, "launches"))) == 1 })
	waitFor(t, "the record", v.log, func() bool { rec := v.record(t); return rec != nil && rec.Pane != "" })
	stop()
	// What an older Retake leaves: no record, and the task paused once its lease ran out.
	rec := v.record(t)
	if err := os.Remove(v.local + "/.retake/worker/tasks/" + rec.Task + "/" + recordFile); err != nil {
		t.Fatal(err)
	}
	if err := v.st.UpdateAutomation(func(a *project.AutomationState) (bool, error) {
		j := a.Task(rec.Task)
		j.State, j.Error, j.Lease, j.Until = "paused", project.LostError, "", 0
		for i := range a.Workers {
			a.Workers[i].Seen -= 60 // it went away a while ago: a new session needs no lease to come in
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	v.start(t, p)
	v.wait(t, "the task taken back", func(a project.AutomationState) bool { return a.Tasks[0].State == "running" })
	if j := v.task(t); j.Attempt != 1 || j.Lease == rec.Lease || j.Events[len(j.Events)-1].Text != "Reconnected to the agent after a restart" {
		t.Fatalf("%+v", j)
	}
	if err := os.WriteFile(filepath.Join(barrier, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v.wait(t, "v2", func(a project.AutomationState) bool { return a.Tasks[0].State == "completed" })
	if got := lines(t, filepath.Join(barrier, "launches")); len(got) != 1 {
		t.Fatalf("launches %q", got)
	}
}

// durableProfile is the test binary as a background agent (TestAgentProcess, durable): it waits for $DURABLE_DIR/go,
// then makes the next version, reports, and prints its usage.
func durableProfile(t *testing.T, dir string) Profile {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Profile{AgentProfile: project.AgentProfile{ID: "durable", Name: "Durable"},
		Command: []string{bin, "-test.run=^TestAgentProcess$", "--", "durable"}, Env: map[string]string{"DURABLE_DIR": dir}}
}

// gone kills what a test's detached agent left, should the test fail before it ends.
func gone(t *testing.T, p *agentProc) {
	t.Helper()
	t.Cleanup(func() {
		if p.alive() {
			endGroup(p.PID, p.Start)
		}
	})
}

// TestBackgroundAgentOutlivesItsWorker: a background agent runs detached (its own session, its output in its log):
// quitting the worker leaves it running, the next worker takes it back by its pid and start time and waits for it,
// and the task gets its usage from its log.
func TestBackgroundAgentOutlivesItsWorker(t *testing.T) {
	testmedia.Need(t)
	gate := t.TempDir()
	p := durableProfile(t, gate)
	v := serveVideo(t, scratchDir(t, "durable-bg-"), "shop", 1)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	v.send(t, 1)
	waitFor(t, "the agent", v.log, func() bool { return len(lines(t, filepath.Join(gate, "launches"))) == 1 })
	waitFor(t, "the record", v.log, func() bool { rec := v.record(t); return rec != nil && rec.Proc != nil })
	proc := v.record(t).Proc
	gone(t, proc)
	if proc.Start == "" || !proc.alive() {
		t.Fatalf("no identity for the agent: %+v", proc)
	}
	if sid, err := exec.Command("ps", "-o", "sid=", "-p", fmt.Sprint(proc.PID)).Output(); err != nil ||
		strings.TrimSpace(string(sid)) != fmt.Sprint(proc.PID) {
		t.Fatalf("the agent isn't in a session of its own: %q %v", sid, err)
	}
	stop()
	time.Sleep(200 * time.Millisecond)
	if !proc.alive() {
		t.Fatal("the agent stopped with its worker")
	}
	if j := v.task(t); j.State != "running" {
		t.Fatalf("%+v", j)
	}
	v.start(t, p)
	waitFor(t, "the task taken back", v.log, func() bool { return strings.Contains(v.log.String(), "took task") })
	if err := os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	v.wait(t, "v2", func(a project.AutomationState) bool { return a.Tasks[0].State == "completed" })
	j := v.task(t)
	if j.Usage == nil || j.Usage.Input != 120 || j.Usage.Output != 24 {
		t.Fatalf("usage %+v", j.Usage)
	}
	if got := lines(t, filepath.Join(gate, "launches")); len(got) != 1 {
		t.Fatalf("launches %q", got)
	}
	if proc.alive() {
		t.Fatal("the agent is still running")
	}
}

// TestChatTurnOutlivesItsWorker: a chat turn's agent runs detached and writes its output to a file; the next worker
// takes the turn back and streams on from where the server has its words, with none twice. Stop still kills a turn.
func TestChatTurnOutlivesItsWorker(t *testing.T) {
	testmedia.Need(t)
	gate := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p := Profile{AgentProfile: project.AgentProfile{ID: "chatty", Name: "Chatty"},
		Command: []string{bin, "-test.run=^TestChatAgentProcess$"}, Env: map[string]string{"FAKE_CHAT_DIR": gate}}
	v := serveVideo(t, scratchDir(t, "durable-chat-"), "shop", 0)
	stop := v.start(t, p)
	v.wait(t, "the worker", func(a project.AutomationState) bool { return a.Config.Worker != "" })
	if _, err = v.st.PostChat(project.ChatPost{Text: "Make the hook shorter", By: "Sam"}); err != nil {
		t.Fatal(err)
	}
	reply := func() string {
		ms, err := v.st.ChatMessages(50)
		if err != nil {
			t.Fatal(err)
		}
		text := ""
		for _, m := range ms {
			if m.Role == project.ChatAgent {
				text += m.Text
			}
		}
		return text
	}
	waitFor(t, "the first words", v.log, func() bool { return reply() == "first words\n" })
	turnPath := filepath.Join(v.local, ".retake", "worker", "chat", turnFile)
	var rec turnRecord
	waitFor(t, "the turn's record", v.log, func() bool {
		b, _ := os.ReadFile(turnPath)
		return json.Unmarshal(b, &rec) == nil && rec.Proc != nil && rec.Offset > 0
	})
	gone(t, rec.Proc)
	stop()
	if !rec.Proc.alive() {
		t.Fatal("the turn's agent stopped with its worker")
	}
	view, err := v.st.ChatView()
	if err != nil || view.Turn == nil || view.Turn.State != project.ChatRunning {
		t.Fatalf("the turn ended with its worker: %+v %v", view.Turn, err)
	}
	v.start(t, p)
	waitFor(t, "the turn taken back", v.log, func() bool { return strings.Contains(v.log.String(), "took the chat's turn back") })
	if err = os.WriteFile(filepath.Join(gate, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the turn's end", v.log, func() bool {
		view, err := v.st.ChatView()
		return err == nil && view.Turn == nil
	})
	if got := reply(); got != "first words\nsecond words\n" {
		t.Fatalf("the reply %q", got)
	}
	ms, _ := v.st.ChatMessages(50)
	for _, m := range ms {
		if m.Role == project.ChatInfo {
			t.Fatalf("the turn ended with %q", m.Text)
		}
	}
	if _, err = os.Stat(turnPath); !os.IsNotExist(err) {
		t.Fatal("the turn's record stays after its end", err)
	}

	// Stop, a person's, kills the turn's agent.
	_ = os.Remove(filepath.Join(gate, "go"))
	if _, err = v.st.PostChat(project.ChatPost{Text: "And the title", By: "Sam"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the second turn", v.log, func() bool {
		b, _ := os.ReadFile(turnPath)
		var r turnRecord
		return json.Unmarshal(b, &r) == nil && r.Proc != nil && r.Turn != rec.Turn && r.Offset > 0
	})
	b, _ := os.ReadFile(turnPath)
	var second turnRecord
	if err = json.Unmarshal(b, &second); err != nil {
		t.Fatal(err)
	}
	gone(t, second.Proc)
	if err = v.st.StopChat(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the stop", v.log, func() bool {
		view, err := v.st.ChatView()
		return err == nil && view.Turn == nil
	})
	waitFor(t, "the agent to end", v.log, func() bool { return !second.Proc.alive() })
}

// TestChatAgentProcess is a stand-in for a chat turn's agent (a plain one: its stdout is its words): "first words",
// then, once $FAKE_CHAT_DIR/go exists, "second words".
func TestChatAgentProcess(t *testing.T) {
	dir := os.Getenv("FAKE_CHAT_DIR")
	if dir == "" {
		return
	}
	fmt.Println("first words")
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "go")); err == nil {
			fmt.Println("second words")
			os.Exit(0) // without the test framework's own words on stdout
		}
	}
	os.Exit(1)
}

// TestProcessIdentity: a process is known by its pid and start time, so a pid reused by another process isn't it.
func TestProcessIdentity(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	p := &agentProc{PID: cmd.Process.Pid, Start: procStart(cmd.Process.Pid)}
	if p.Start == "" || !p.alive() {
		t.Fatalf("%+v", p)
	}
	if (&agentProc{PID: p.PID, Start: p.Start + "0"}).alive() {
		t.Fatal("another start time counts as the same process")
	}
	endGroup(p.PID, p.Start+"0") // not that process: nothing happens
	if !p.alive() {
		t.Fatal("ended a process that wasn't the one recorded")
	}
	endGroup(p.PID, p.Start)
	<-done
	if p.alive() || procStart(p.PID) != "" {
		t.Fatal("still there")
	}
}

// TestInFlightAndSnapshotState: what makes retake open start a project's worker at once, and an undo snapshot finished
// by a later worker.
func TestInFlightAndSnapshotState(t *testing.T) {
	root := t.TempDir()
	if InFlight(root) {
		t.Fatal("an empty folder has work")
	}
	for _, f := range []string{AskFile, filepath.Join("chat", turnFile)} {
		path := filepath.Join(root, ".retake", "worker", f)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if !InFlight(root) {
			t.Fatal(f)
		}
		_ = os.Remove(path)
	}
	doc := filepath.Join(root, "v0", "script.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("# Hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := ext.Snapshot(root, []string{"v0/*.md"}, "t1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s.State())
	var st ext.SnapState
	if err = json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(doc, []byte("# Shorter hook\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	edits, err := ext.ResumeSnap(root, st).Finish()
	if err != nil || len(edits) != 1 || edits[0].Path != "v0/script.md" || edits[0].Task != "t1" {
		t.Fatalf("%+v %v", edits, err)
	}
}
