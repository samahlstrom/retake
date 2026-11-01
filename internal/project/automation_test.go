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
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "stopped"}, 0); e != nil {
		t.Fatal(e)
	}
	next, e := s.PollWork(w, "", "")
	if e != nil || next.ID != running.ID || next.Agent != "codex" || next.Lease == running.Lease {
		t.Fatal(next, e)
	}
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "question", Message: "late response"}, 0); e == nil {
		t.Fatal("stale agent mutated task")
	}
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: next.ID, Lease: next.Lease, State: "question", Message: "Blue or teal?"}, 0); e != nil {
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
	if _, e = s.ReportWork(report, 0); e == nil {
		t.Fatal("completed without render")
	}
	e = s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Renders = append(a.Renders, LiveRender{ID: "render", Worker: w.ID, Task: "another-task", Label: "v7", Complete: true})
		return false, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReportWork(report, 0); e == nil {
		t.Fatal("completed with another task's render")
	}
	e = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Renders[0].Task = j.ID; return false, nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ReportWork(report, 0); e != nil {
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
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}, 0); e != nil {
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
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}, 0); e != nil {
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
	if e = s.SubmitReview("v6", take1, after, false); e != nil {
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

func TestSendRunsWithAutoSendOffAndFinishedReviewsWaitForSendOrAutoSend(t *testing.T) {
	s, _, w := automationFixture(t)
	if e := s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Config.Enabled = false; return true, nil }); e != nil {
		t.Fatal(e)
	}
	final, e := s.QueueTake("v6", take1, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	var after []int64
	for _, j := range final {
		after = append(after, j.ID)
		if _, e = s.Finish(j.ID, Succeeded, "", "", 1); e != nil {
			t.Fatal(e)
		}
	}
	// Finished without send while auto-send is off: considered once, nothing queued, and enabling later
	// doesn't sweep it up.
	if e = s.SubmitReview("v6", take1, after, false); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	a, _ := s.Automation()
	if len(a.Tasks) != 0 || !a.Submissions[0].Done {
		t.Fatalf("unsent review queued: %+v", a)
	}
	if e = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Config.Enabled = true; return true, nil }); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	if a, _ = s.Automation(); len(a.Tasks) != 0 {
		t.Fatal("turning auto-send on swept up an old review")
	}
	if e = s.UpdateAutomation(func(a *AutomationState) (bool, error) { a.Config.Enabled = false; return true, nil }); e != nil {
		t.Fatal(e)
	}
	// Finishing again with send queues it, and the worker runs it although auto-send is off.
	if e = s.SubmitReview("v6", take1, after, true); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	if e = s.ReadyFeedback(); e != nil {
		t.Fatal(e)
	}
	if a, _ = s.Automation(); len(a.Tasks) != 2 {
		t.Fatalf("sent review: %+v", a.Tasks)
	}
	j, e := s.PollWork(w, "", "")
	if e != nil || j == nil {
		t.Fatal("sent feedback didn't run with auto-send off", j, e)
	}
}

func TestDraftLabelsAndPromotion(t *testing.T) {
	var a AutomationState
	// An empty project's first render is its first version.
	first := a.AddRender(LiveRender{ID: "r1"}, 0, 0)
	if first.Label != "v1" || first.Draft {
		t.Fatalf("first render: %+v", first)
	}
	// With versions on disk (v1..v3), renders are drafts of v4.
	a = AutomationState{}
	plain := a.AddRender(LiveRender{ID: "r1"}, 3, 3)
	d2 := a.AddRender(LiveRender{ID: "r2", Task: "t", Complete: true}, 3, 3)
	d3 := a.AddRender(LiveRender{ID: "r3", Task: "t"}, 3, 3)
	if plain.Label != "v4-draft1" || d2.Label != "v4-draft2" || d3.Label != "v4-draft3" || !d3.Draft || d3.N != 4 {
		t.Fatalf("drafts: %q %q %q", plain.Label, d2.Label, d3.Label)
	}
	listed := func() []string {
		var out []string
		for i := range a.Renders {
			if a.Listed(i, 3) {
				out = append(out, a.Renders[i].Label)
			}
		}
		return out
	}
	if got := strings.Join(listed(), " "); got != "v4-draft2 v4-draft3" {
		t.Fatalf("listed while working: %s", got)
	}
	a.promote(a.Render("r2"), 3)
	if v := a.Render("v4-draft2"); v == nil || v.Label != "v4" || v.Alias != "v4-draft2" || v.Draft || v.N != 4 {
		t.Fatalf("promoted: %+v", v)
	}
	if got := strings.Join(listed(), " "); got != "v4" {
		t.Fatalf("listed after completion: %s", got)
	}
	if next := a.AddRender(LiveRender{ID: "r4", Task: "u"}, 3, 3); next.Label != "v5-draft1" {
		t.Fatalf("next draft: %q", next.Label)
	}
	// A version on disk numbered past the drafts (a push) hides them too.
	if got := len(listed()); got != 2 {
		t.Fatalf("listed: %v", listed())
	}
	for i := range a.Renders {
		if a.Renders[i].Draft && a.Listed(i, 9) {
			t.Fatalf("draft %s listed behind v9", a.Renders[i].Label)
		}
	}
}

// Sent feedback queues without a computer, and starts by itself when one connects: the first one ever (it becomes
// the selected one, with its first agent), or the selected one coming back after being offline.
func TestQueuedFeedbackWaitsForAComputer(t *testing.T) {
	s, c := newStore(t)
	if _, e := s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Text: "make the title blue"}, {Key: "second", Take: take1, Text: "trim the pause"}}}); e != nil {
		t.Fatal(e)
	}
	queued, e := s.QueueFeedback([]int64{1}, "")
	if e != nil || len(queued) != 1 || queued[0].State != "queued" || queued[0].Worker != "" {
		t.Fatal("no computer, no queue:", queued, e)
	}
	w := EditingWorker{ID: "machine", Session: "one", Agents: []AgentProfile{{ID: "codex"}, {ID: "claude"}}}
	j, e := s.PollWork(w, "", "")
	if e != nil || j == nil || j.ID != queued[0].ID || j.Agent != "codex" || j.Worker != "machine" {
		t.Fatal("the first computer didn't pick the queue up:", j, e)
	}
	if _, e = s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "question", Message: "which blue?"}, 0); e != nil {
		t.Fatal(e)
	}
	// The computer goes away; a send waits for it, and it picks the send up when it comes back.
	c.add(WorkerLease + time.Minute)
	if _, e = s.QueueFeedback([]int64{2}, ""); e != nil {
		t.Fatal(e)
	}
	w.Session = "two"
	j, e = s.PollWork(w, "", "")
	if e != nil || j == nil || j.Notes[0] != 2 || j.State != "running" {
		t.Fatal("the returning computer didn't pick the send up:", j, e)
	}
}

// Sending lists the takes sent with Send until their processing finished; a failed processing job ends it, so its
// comments don't look like they're on their way forever.
func TestSendingListsSentTakesUntilProcessed(t *testing.T) {
	s, _ := newStore(t)
	submit := func(take string, send bool) []Job {
		t.Helper()
		jobs, e := s.QueueTake("v6", take, t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		var after []int64
		for _, j := range jobs {
			after = append(after, j.ID)
		}
		if e = s.SubmitReview("v6", take, after, send); e != nil {
			t.Fatal(e)
		}
		return jobs
	}
	sent := submit(take1, true)
	submit(take2, false)
	got, e := s.Sending()
	if e != nil || len(got) != 1 || got[0] != (TakeRef{Version: "v6", Take: take1}) {
		t.Fatal("sending:", got, e)
	}
	if _, e = s.Finish(sent[0].ID, Fatal, "whisper.cpp isn't installed", "", 1); e != nil {
		t.Fatal(e)
	}
	if got, e = s.Sending(); e != nil || len(got) != 0 {
		t.Fatal("a failed take is still sending:", got, e)
	}
}
