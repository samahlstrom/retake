package project

import "testing"

// claimAgent queues notes with a held Send's agent (or none) and returns the agent the worker's claim runs.
func claimAgent(t *testing.T, s *Store, w EditingWorker, notes []int64, held string) string {
	t.Helper()
	if _, err := s.QueueFeedback(notes, held); err != nil {
		t.Fatal(err)
	}
	task, err := s.PollWork(w, "", "")
	if err != nil || task == nil {
		t.Fatal("no claim", task, err)
	}
	if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: task.ID, Lease: task.Lease, State: "paused",
		Message: "test"}, 0); err != nil {
		t.Fatal(err)
	}
	return task.Agent
}

func TestVideoAgentOrder(t *testing.T) {
	s, _, w := automationFixture(t) // the default (config.agent) is claude; the worker offers claude and codex

	// No video agent: the default.
	if got := claimAgent(t, s, w, []int64{1}, ""); got != "claude" {
		t.Fatalf("no video agent: got %q, want the default claude", got)
	}
	s, _, w = automationFixture(t) // a fresh video (a send for the same target would join the paused batch)
	if err := s.SetVideoAgent("codex"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Automation()
	if a.VideoAgent != "codex" || a.Config.Agent != "claude" {
		t.Fatalf("video agent %q, config %q", a.VideoAgent, a.Config.Agent)
	}
	// A task queued without an agent takes the video's at its claim.
	if got := claimAgent(t, s, w, []int64{2}, ""); got != "codex" {
		t.Fatalf("video agent: got %q, want codex", got)
	}
}

func TestVideoAgentHeldSendWins(t *testing.T) {
	s, _, w := automationFixture(t)
	if err := s.SetVideoAgent("codex"); err != nil {
		t.Fatal(err)
	}
	if got := claimAgent(t, s, w, []int64{1}, "claude"); got != "claude" {
		t.Fatalf("a held Send's agent: got %q, want claude", got)
	}
}

func TestVideoAgentLeavesAQueuedTask(t *testing.T) {
	s, _, w := automationFixture(t)
	if _, err := s.QueueFeedback([]int64{1}, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVideoAgent("codex"); err != nil { // after the send: the queued task keeps the agent it got
		t.Fatal(err)
	}
	task, err := s.PollWork(w, "", "")
	if err != nil || task == nil || task.Agent != "claude" {
		t.Fatal("the queued task changed agent", task, err)
	}
}

func TestVideoAgentAtClaimForAnOlderTask(t *testing.T) {
	s, _, w := automationFixture(t)
	if err := s.SetVideoAgent("codex"); err != nil {
		t.Fatal(err)
	}
	// A task an older store queued without an agent takes the video's at its claim.
	if err := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Tasks = append(a.Tasks, EditingTask{ID: NewWorkID(), Notes: []int64{1}, Version: "v6", Target: "v7",
			State: "queued", Worker: w.ID, Events: []TaskEvent{}})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	task, err := s.PollWork(w, "", "")
	if err != nil || task == nil || task.Agent != "codex" {
		t.Fatal("an agentless task didn't take the video's agent", task, err)
	}
}

func TestVideoAgentRefusesAnAgentNoComputerOffers(t *testing.T) {
	s, _, _ := automationFixture(t)
	if err := s.SetVideoAgent("gemini"); err == nil {
		t.Fatal("an agent the worker doesn't offer was taken")
	}
	if err := s.SetVideoAgent("codex"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVideoAgent(""); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Automation(); a.VideoAgent != "" {
		t.Fatalf("cleared, but it's %q", a.VideoAgent)
	}
}

func TestVideoAgentNotOfferedFallsBack(t *testing.T) {
	s, _, w := automationFixture(t)
	if err := s.SetVideoAgent("codex"); err != nil {
		t.Fatal(err)
	}
	// Another computer is selected later that offers only claude: the default runs.
	w2 := EditingWorker{ID: "other", Session: "s2", Agents: []AgentProfile{{ID: "claude"}}}
	if _, err := s.PollWork(w2, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.Config.Worker = w2.ID
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueFeedback([]int64{1}, ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.PollWork(w2, "", "")
	if err != nil || task == nil || task.Agent != "claude" {
		t.Fatal("not offered: want the default", task, err)
	}
	_ = w
}
