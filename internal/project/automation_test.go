package project

import (
	"strings"
	"testing"
	"time"
)

func automationFixture(t *testing.T) (*Store, *clock, EditingWorker) {
	t.Helper()
	s, c := newStore(t)
	_, e := s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Text: "make the title blue"}, {Key: "second", Take: take1, Text: "trim the pause"}}})
	if e != nil {
		t.Fatal(e)
	}
	w := EditingWorker{ID: "machine", Session: "session", Agents: []AgentProfile{{ID: "claude"}, {ID: "codex"}}}
	if _, e = s.PollWork(w, "", ""); e != nil {
		t.Fatal(e)
	}
	e = s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Config = AutomationConfig{Enabled: true, Worker: w.ID, Agent: "claude"}
		return true, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return s, c, w
}

func TestAutomationSwitchIsFencedUntilPreviousAgentStops(t *testing.T) {
	s, _, w := automationFixture(t)
	first, e := s.QueueFeedback([]int64{1, 2}, "")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.QueueFeedback([]int64{1}, "")
	if e != nil || again[0].ID != first[0].ID {
		t.Fatal("duplicate submission", again, e)
	}
	running, e := s.PollWork(w, "", "")
	if e != nil || running == nil {
		t.Fatal(running, e)
	}
	if t2, e := s.PollWork(w, "", ""); e != nil || t2 != nil {
		t.Fatal("second simultaneous claim", t2, e)
	}
	if e = s.ControlTask(running.ID, "switch", "codex", ""); e != nil {
		t.Fatal(e)
	}
	heartbeat, e := s.PollWork(w, running.ID, running.Lease)
	if e != nil || !heartbeat.Stop {
		t.Fatal(heartbeat, e)
	}
	if t2, _ := s.PollWork(w, "", ""); t2 != nil {
		t.Fatal("replacement started before acknowledgement")
	}
	if e = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "stopped"}); e != nil {
		t.Fatal(e)
	}
	next, e := s.PollWork(w, "", "")
	if e != nil || next.ID != running.ID || next.Agent != "codex" || next.Lease == running.Lease {
		t.Fatal(next, e)
	}
	if e = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "question", Message: "late response"}); e == nil {
		t.Fatal("stale agent mutated task")
	}
	if e = s.ReportWork(WorkReport{Worker: w.ID, Task: next.ID, Lease: next.Lease, State: "question", Message: "Blue or teal?"}); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlTask(next.ID, "answer", "", "Use teal"); e != nil {
		t.Fatal(e)
	}
	resumed, e := s.PollWork(w, "", "")
	if e != nil || resumed.ID != next.ID || !strings.Contains(resumed.Events[len(resumed.Events)-2].Text, "Use teal") {
		t.Fatal(resumed, e)
	}
}

func TestAutomationLeaseExpiryPersistsAndRejectsDuplicateWorker(t *testing.T) {
	s, c, w := automationFixture(t)
	if _, e := s.QueueFeedback([]int64{1}, ""); e != nil {
		t.Fatal(e)
	}
	j, e := s.PollWork(w, "", "")
	if e != nil {
		t.Fatal(e)
	}
	other := w
	other.Session = "replacement"
	if _, e = s.PollWork(other, "", ""); e == nil {
		t.Fatal("simultaneous duplicate worker allowed")
	}
	c.add(WorkerLease + time.Second)
	if _, e = s.PollWork(w, j.ID, j.Lease); e == nil {
		t.Fatal("expired lease renewed")
	}
	a, e := s.Automation()
	if e != nil || a.Tasks[0].State != "paused" || a.Tasks[0].Lease != "" {
		t.Fatal(a, e)
	}
	// A second store simulates a restarted host; the queue and pause survive.
	reopened, e := Open(s.root)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = reopened.Close() }()
	a, e = reopened.Automation()
	if e != nil || a.Tasks[0].ID != j.ID || a.Config.Worker != w.ID {
		t.Fatal(a, e)
	}
}

func TestAutomationCompletionRequiresOwnPlayableResultAndKeepsApprovalSeparate(t *testing.T) {
	s, _, w := automationFixture(t)
	if _, e := s.QueueFeedback([]int64{1}, ""); e != nil {
		t.Fatal(e)
	}
	j, e := s.PollWork(w, "", "")
	if e != nil {
		t.Fatal(e)
	}
	report := WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Title is now blue", Result: "render"}
	if e = s.ReportWork(report); e == nil {
		t.Fatal("completed without render")
	}
	e = s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Renders = append(a.Renders, LiveRender{ID: "render", Worker: w.ID, Task: "another-task", Label: "v7", Complete: true})
		return false, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReportWork(report); e == nil {
		t.Fatal("completed with another task's render")
	}
	e = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Renders[0].Task = j.ID; return false, nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReportWork(report); e != nil {
		t.Fatal(e)
	}
	n, e := s.Note(1)
	if e != nil || n.Status != StatusAnswered || n.AnsweredIn != "v7" || n.Change != report.Message {
		t.Fatal(n, e)
	}
	other, e := s.Note(2)
	if e != nil || other.Status != StatusOpen {
		t.Fatal("unrelated note changed", other, e)
	}
}

func TestAutomationUsageLimitUsesOnlyConfiguredFallback(t *testing.T) {
	s, _, w := automationFixture(t)
	if _, e := s.QueueFeedback([]int64{1}, ""); e != nil {
		t.Fatal(e)
	}
	j, e := s.PollWork(w, "", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}); e != nil {
		t.Fatal(e)
	}
	a, _ := s.Automation()
	if a.Tasks[0].State != "paused" {
		t.Fatal("implicit provider switch")
	}
	if e = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Config.Fallback = "codex"; return true, nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.ControlTask(j.ID, "retry", "", ""); e != nil {
		t.Fatal(e)
	}
	j, e = s.PollWork(w, "", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}); e != nil {
		t.Fatal(e)
	}
	next, e := s.PollWork(w, "", "")
	if e != nil || next.Agent != "codex" || next.ID != j.ID {
		t.Fatal(next, e)
	}
}

func TestOnlyFinalSubmissionQueuesFeedbackAfterProcessing(t *testing.T) {
	s, _, _ := automationFixture(t)
	live, e := s.QueueClips("v6", take1, t.TempDir(), "clip-revision")
	if e != nil {
		t.Fatal(e)
	}
	for _, j := range live {
		if _, e = s.Finish(j.ID, Succeeded, "", "", 1); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	a, _ := s.Automation()
	if len(a.Tasks) != 0 {
		t.Fatal("incremental transcription launched editing")
	}
	final, e := s.QueueTake("v6", take1, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	var after []int64
	for _, j := range final {
		after = append(after, j.ID)
	}
	if e = s.SubmitReview("v6", take1, after); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	a, _ = s.Automation()
	if len(a.Tasks) != 0 {
		t.Fatal("editing started before final processing")
	}
	for _, j := range final {
		if _, e = s.Finish(j.ID, Succeeded, "", "", 1); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	a, _ = s.Automation()
	if len(a.Tasks) != 2 {
		t.Fatalf("wanted one task per final note: %+v", a.Tasks)
	}
}

func TestWithdrawnFeedbackNeverStartsAndOfflineStateExpiresWithoutWorkerPolling(t *testing.T) {
	s, c, w := automationFixture(t)
	if _, e := s.QueueFeedback([]int64{1, 2}, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Retracted: true}, {Key: "second", Take: take1, Text: "trim the pause"}}}); e != nil {
		t.Fatal(e)
	}
	j, e := s.PollWork(w, "", "")
	if e != nil || j == nil || j.Notes[0] != 2 {
		t.Fatal("retracted feedback was executed", j, e)
	}
	c.add(WorkerLease + time.Second)
	if e = s.ReconcileWork(); e != nil {
		t.Fatal(e)
	}
	a, e := s.Automation()
	if e != nil || a.Tasks[0].State != "cancelled" || a.Tasks[1].State != "paused" {
		t.Fatal(a, e)
	}
}
