package project

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Batches (spec/automation.md, "Batches"): one Send is one task, one agent run and one version, never a task per
// comment; one task per video at a time, and one next batch.

// batchFixture is a store with five open notes on v6 (1..5, the first two in take1, the rest in take2) and a
// connected worker selected with Claude Code.
func batchFixture(t *testing.T) (*Store, EditingWorker) {
	t.Helper()
	s, _ := newStore(t)
	var cs []Comment
	for i := range 5 {
		take := take1
		if i >= 2 {
			take = take2
		}
		cs = append(cs, Comment{Key: fmt.Sprintf("n%d", i+1), Take: take, Text: fmt.Sprintf("note %d", i+1), T0: float64(i), T1: float64(i) + 1})
	}
	if _, err := s.Sync(Run{Version: "v6", Comments: cs}); err != nil {
		t.Fatal(err)
	}
	w := EditingWorker{ID: "machine", Session: "session", Agents: []AgentProfile{{ID: "claude"}, {ID: "codex"}}}
	if _, err := s.PollWork(w, "", ""); err != nil {
		t.Fatal(err)
	}
	return s, w
}

// draft registers a complete draft render of task j, as the worker does while its agent edits.
func draft(t *testing.T, s *Store, w EditingWorker, j *EditingTask, id string) {
	t.Helper()
	if err := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.AddRender(LiveRender{ID: id, Worker: w.ID, Task: j.ID, Complete: true}, 6, 6)
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func versionsMade(t *testing.T, s *Store) []string {
	t.Helper()
	a, err := s.Automation()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, v := range a.Renders {
		if !v.Draft {
			out = append(out, v.Label)
		}
	}
	return out
}

// TestSendIsOneBatchAndOneVersion: a send of three notes is one task; two more sent (one at a time) while it runs wait
// in one next batch (the worker gives them to the running agent with join, TestLiveSessionTakesTheNextBatch; one it
// didn't take runs next); each completed task makes exactly one version, and each note gets its own change when the
// report says one.
func TestSendIsOneBatchAndOneVersion(t *testing.T) {
	s, w := batchFixture(t)
	sent, err := s.QueueFeedback([]int64{1, 2, 3}, "")
	if err != nil || len(sent) != 1 || !slices.Equal(sent[0].Notes, []int64{1, 2, 3}) {
		t.Fatal("three notes, one task:", sent, err)
	}
	if again, _ := s.QueueFeedback([]int64{2, 3}, ""); len(again) != 1 || again[0].ID != sent[0].ID {
		t.Fatal("a second send of the same notes made a task:", again)
	}
	running, err := s.PollWork(w, "", "")
	if err != nil || running == nil || running.ID != sent[0].ID {
		t.Fatal(running, err)
	}
	// Sent while it runs (a background agent): one next batch, however many sends.
	n4, _ := s.QueueFeedback([]int64{4}, "")
	n5, _ := s.QueueFeedback([]int64{5}, "")
	if len(n4) != 1 || len(n5) != 1 || n4[0].ID != n5[0].ID || n4[0].ID == running.ID {
		t.Fatal("the next batch:", n4, n5)
	}
	a, _ := s.Automation()
	if len(a.Tasks) != 2 || !slices.Equal(a.Task(n4[0].ID).Notes, []int64{4, 5}) || a.Task(n4[0].ID).State != "queued" {
		t.Fatalf("tasks: %+v", a.Tasks)
	}
	if again, _ := s.PollWork(w, "", ""); again != nil {
		t.Fatal("a second agent on the same video:", again)
	}

	// Two drafts, one report: one version, v7.
	draft(t, s, w, running, "r1")
	draft(t, s, w, running, "r2")
	if _, err = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "completed",
		Message: "Did all three", Result: "r2", Notes: map[int64]string{2: "Trimmed the pause"}}, 6); err != nil {
		t.Fatal(err)
	}
	if got := versionsMade(t, s); !slices.Equal(got, []string{"v7"}) {
		t.Fatal("versions:", got)
	}
	for id, change := range map[int64]string{1: "Did all three", 2: "Trimmed the pause", 3: "Did all three"} {
		if n, _ := s.Note(id); n.Status != StatusAnswered || n.AnsweredIn != "v7" || n.Change != change {
			t.Fatalf("note %d: %+v", id, n)
		}
	}

	// The next batch starts when it ends, and makes the next version.
	next, err := s.PollWork(w, "", "")
	if err != nil || next == nil || next.ID != n4[0].ID || !slices.Equal(next.Notes, []int64{4, 5}) {
		t.Fatal(next, err)
	}
	draft(t, s, w, next, "r3")
	if _, err = s.ReportWork(WorkReport{Worker: w.ID, Task: next.ID, Lease: next.Lease, State: "completed",
		Message: "Did both", Result: "r3"}, 6); err != nil {
		t.Fatal(err)
	}
	if got := versionsMade(t, s); !slices.Equal(got, []string{"v7", "v8"}) {
		t.Fatal("versions:", got)
	}
	if len(versionsMade(t, s)) != 2 {
		t.Fatal("one version per batch")
	}
	if _, err = s.ReportWork(WorkReport{Worker: w.ID, Task: running.ID, Lease: running.Lease, State: "completed",
		Message: "x", Notes: map[int64]string{}}, 6); err == nil {
		t.Fatal("a finished attempt reported again")
	}
}

// TestOlderStoresQueuedTasksBecomeOneBatch: an older version queued one task per note; opening the store merges the
// queued ones (not the running or paused one) into the oldest, in order, with their questions and history.
func TestOlderStoresQueuedTasksBecomeOneBatch(t *testing.T) {
	s, w := batchFixture(t)
	legacy := AutomationState{Config: AutomationConfig{Worker: w.ID, Agent: "claude"}, Workers: []EditingWorker{w},
		Tasks: []EditingTask{{ID: "running", Notes: []int64{1}, State: "running", Agent: "claude", Worker: w.ID, Lease: "l", Attempt: 1, Events: []TaskEvent{}}}}
	for _, id := range []int64{2, 3, 4, 5} {
		legacy.Tasks = append(legacy.Tasks, EditingTask{ID: fmt.Sprintf("t%d", id), Notes: []int64{id}, Version: "v6",
			State: "queued", Agent: "claude", Events: []TaskEvent{{At: 1, Text: "Feedback submitted"}}})
	}
	legacy.Tasks[2].Asked = []TaskAsk{{ID: 40, Note: 3}}
	legacy.Tasks[2].Told = []int64{41}
	legacy.Tasks = slices.Insert(legacy.Tasks, 2, EditingTask{ID: "paused", Notes: []int64{6}, State: "paused", Events: []TaskEvent{}})
	b, _ := json.Marshal(legacy)
	if err := s.write(func(t *tx) error {
		_, err := t.Exec(`INSERT INTO automation (id,data) VALUES (1,?) ON CONFLICT (id) DO UPDATE SET data = excluded.data`, string(b))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Automation(); len(a.Tasks) != 6 {
		t.Fatalf("the legacy state: %+v", a.Tasks)
	}
	reopened, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	a, err := reopened.Automation()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range a.Tasks {
		ids = append(ids, j.ID+":"+j.State)
	}
	if !slices.Equal(ids, []string{"running:running", "t2:queued", "paused:paused"}) {
		t.Fatalf("tasks after opening: %v", ids)
	}
	batch := a.Task("t2")
	if !slices.Equal(batch.Notes, []int64{2, 3, 4, 5}) || !slices.Equal(batch.Pending(), []int64{2, 3, 4, 5}) ||
		len(batch.Asked) != 1 || batch.Asked[0].ID != 40 || !slices.Equal(batch.Told, []int64{41}) {
		t.Fatalf("the batch: %+v", batch)
	}
	if last := batch.Events[len(batch.Events)-1].Text; last != "Joined task t5: #5" {
		t.Fatalf("events: %+v", batch.Events)
	}
	// Opening it again changes nothing.
	again, err := Open(s.root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	if b, _ := again.Automation(); len(b.Tasks) != 3 || len(b.Task("t2").Events) != len(batch.Events) {
		t.Fatalf("reopened: %+v", b.Tasks)
	}
}

// TestRetryAndSendJoinTheQueuedBatch: a paused batch retried while another waits becomes one batch with it (the agent
// named in the retry is the batch's), and a send of one more note (the ⋯ menu's) joins it too.
func TestRetryAndSendJoinTheQueuedBatch(t *testing.T) {
	s, w := batchFixture(t)
	first, _ := s.QueueFeedback([]int64{1}, "")
	j, _ := s.PollWork(w, "", "")
	if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}, 6); err != nil {
		t.Fatal(err)
	}
	second, _ := s.QueueFeedback([]int64{2}, "")
	if second[0].ID == first[0].ID {
		t.Fatal("a paused batch took new notes")
	}
	if err := s.ControlTask(first[0].ID, "retry", "codex", ""); err != nil {
		t.Fatal(err)
	}
	one, _ := s.QueueFeedback([]int64{3}, "")
	a, _ := s.Automation()
	if len(a.Tasks) != 1 || a.Tasks[0].ID != first[0].ID || a.Tasks[0].Agent != "codex" ||
		!slices.Equal(a.Tasks[0].Notes, []int64{1, 2, 3}) || one[0].ID != first[0].ID {
		t.Fatalf("tasks: %+v", a.Tasks)
	}
	run, err := s.PollWork(w, "", "")
	if err != nil || run == nil || run.Agent != "codex" || len(run.Pending()) != 3 {
		t.Fatal(run, err)
	}
}

// TestLiveSessionTakesTheNextBatch: notes sent while an agent works in herdr or tmux reach its session: the poll
// offers the queued batch that never ran (next), and the notes the worker gave the session (join) move into the
// running task, so its one report finishes them all in one version.
func TestLiveSessionTakesTheNextBatch(t *testing.T) {
	s, w := batchFixture(t)
	if _, err := s.QueueFeedback([]int64{1}, ""); err != nil {
		t.Fatal(err)
	}
	run, _ := s.PollWork(w, "", "")
	more, _ := s.QueueFeedback([]int64{2, 3}, "")
	poll := WorkPoll{Worker: w, Task: run.ID, Lease: run.Lease}
	task, next, err := s.PollWorker(poll)
	if err != nil || task.ID != run.ID || next == nil || next.ID != more[0].ID || !slices.Equal(next.Notes, []int64{2, 3}) {
		t.Fatal(task, next, err)
	}
	poll.Join = []int64{2, 3, 99}
	task, next, err = s.PollWorker(poll)
	if err != nil || !slices.Equal(task.Notes, []int64{1, 2, 3}) || next != nil {
		t.Fatal(task, next, err)
	}
	a, _ := s.Automation()
	if len(a.Tasks) != 1 || !strings.Contains(a.Tasks[0].Events[len(a.Tasks[0].Events)-1].Text, "Added #2, #3 in the agent's session") {
		t.Fatalf("tasks: %+v", a.Tasks)
	}
	// Part of a batch: the rest stays queued; the report takes the last note in before it decides.
	later, _ := s.QueueFeedback([]int64{4, 5}, "")
	poll.Join = []int64{4}
	if task, next, _ = s.PollWorker(poll); !slices.Equal(task.Notes, []int64{1, 2, 3, 4}) || next == nil || !slices.Equal(next.Notes, []int64{5}) {
		t.Fatal(task, next)
	}
	draft(t, s, w, run, "r1")
	if _, err = s.ReportWork(WorkReport{Worker: w.ID, Task: run.ID, Lease: run.Lease, State: "completed",
		Message: "All five", Result: "r1", Join: []int64{5}}, 6); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Automation()
	if len(a.Tasks) != 1 || a.Task(later[0].ID) != nil || a.Tasks[0].State != "completed" {
		t.Fatalf("tasks: %+v", a.Tasks)
	}
	for id := int64(1); id <= 5; id++ {
		if n, _ := s.Note(id); n.Status != StatusAnswered || n.AnsweredIn != "v7" {
			t.Fatalf("note %d: %+v", id, n)
		}
	}
	if got := versionsMade(t, s); !slices.Equal(got, []string{"v7"}) {
		t.Fatal("versions:", got)
	}
	// A stopping attempt is offered nothing.
	s2, w2 := batchFixture(t)
	_, _ = s2.QueueFeedback([]int64{1}, "")
	r2, _ := s2.PollWork(w2, "", "")
	_, _ = s2.QueueFeedback([]int64{2}, "")
	if err = s2.ControlTask(r2.ID, "pause", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, next, _ = s2.PollWorker(WorkPoll{Worker: w2, Task: r2.ID, Lease: r2.Lease}); next != nil {
		t.Fatal("a stopping attempt was offered the next batch")
	}
}

// TestWithdrawnNoteDropsOutOfItsBatch: deleting one comment of a running batch drops it; the batch carries on and its
// report finishes the rest. A batch with nothing left is stopped.
func TestWithdrawnNoteDropsOutOfItsBatch(t *testing.T) {
	s, w := batchFixture(t)
	_, _ = s.QueueFeedback([]int64{1, 2, 3}, "")
	run, _ := s.PollWork(w, "", "")
	if err := s.DeleteNote(2); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Automation()
	if j := a.Task(run.ID); j.State != "running" || !slices.Equal(j.Pending(), []int64{1, 3}) || !slices.Equal(j.Dropped, []int64{2}) {
		t.Fatalf("%+v", j)
	}
	draft(t, s, w, run, "r1")
	if _, err := s.ReportWork(WorkReport{Worker: w.ID, Task: run.ID, Lease: run.Lease, State: "completed", Message: "Done", Result: "r1"}, 6); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.Note(2); n.Status == StatusAnswered {
		t.Fatal("a deleted comment was answered")
	}
	if n, _ := s.Note(3); n.Status != StatusAnswered {
		t.Fatal(n)
	}
}

// TestQueuedBatchWaitsForASentTake: Send with a take in progress sends the take (send: true) and the other open
// comments at once; the batch waits for the take's processing, takes its notes, and runs once.
func TestQueuedBatchWaitsForASentTake(t *testing.T) {
	s, w := batchFixture(t)
	jobs, err := s.QueueTake("v6", take1, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var after []int64
	for _, j := range jobs {
		after = append(after, j.ID)
	}
	if err = s.SubmitReview("v6", take1, after, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.QueueFeedback([]int64{3, 4, 5}, ""); err != nil {
		t.Fatal(err)
	}
	if j, err := s.PollWork(w, "", ""); err != nil || j != nil {
		t.Fatal("the batch started before the sent take was processed:", j, err)
	}
	for _, j := range jobs {
		if _, err = s.Finish(j.ID, Succeeded, "", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.ReadyFeedback(); err != nil {
		t.Fatal(err)
	}
	j, err := s.PollWork(w, "", "")
	if err != nil || j == nil {
		t.Fatal(j, err)
	}
	got := slices.Clone(j.Notes)
	slices.Sort(got)
	if !slices.Equal(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatal("one batch of the take and the others:", j.Notes)
	}
}
