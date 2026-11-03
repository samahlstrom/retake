package project

import (
	"slices"
	"strings"
	"testing"
)

// mark says retake working or retake done for the running attempt j.
func mark(t *testing.T, s *Store, w EditingWorker, j *EditingTask, note int64, state, text string, extra ...func(*WorkProgress)) ProgressMark {
	t.Helper()
	p := WorkProgress{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: note, State: state, Text: text}
	for _, f := range extra {
		f(&p)
	}
	m, e := s.MarkProgress(p)
	if e != nil {
		t.Fatalf("%s #%d: %v", state, note, e)
	}
	return m
}

// addRender registers a complete render of task j, a revision of its target, and returns its id.
func addRender(t *testing.T, s *Store, w EditingWorker, j *EditingTask, id string) string {
	t.Helper()
	e := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		a.AddRender(LiveRender{ID: id, Worker: w.ID, Task: j.ID, Source: strings.Repeat("a", 64), Duration: 4, FPS: 30,
			Width: 64, Height: 36, Complete: true, Media: "video.mp4"}, 1, 6)
		return true, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return id
}

// One working note at a time; done clears it; a done note is ready once a complete render of the task has it.
func TestProgressMarksOneWorkingNoteAndDoneNotes(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	if m := mark(t, s, w, j, 1, ProgressWorking, "  making   the title\nblue "); m.Note != 1 || m.Target != "v7" {
		t.Fatalf("%+v", m)
	}
	got := taskByID(t, s, j.ID)
	if got.Working == nil || got.Working.Note != 1 || got.Working.Text != "making the title blue" {
		t.Fatalf("working %+v", got.Working)
	}
	mark(t, s, w, j, 2, ProgressWorking, "trimming the pause")
	if got = taskByID(t, s, j.ID); got.Working.Note != 2 {
		t.Fatalf("two working notes: %+v", got.Working)
	}
	// Done while its render is on the way: marked, not ready.
	if m := mark(t, s, w, j, 2, ProgressDone, "Trimmed the pause to 0.4 s"); m.Ready {
		t.Fatal("ready without a render")
	}
	got = taskByID(t, s, j.ID)
	if got.Working != nil || len(got.Marked) != 1 || got.Marked[0].Note != 2 || got.Marked[0].Ready {
		t.Fatalf("after done: %+v %+v", got.Working, got.Marked)
	}
	// The render that has it: ready.
	id := addRender(t, s, w, j, "render-1")
	if m := mark(t, s, w, j, 2, ProgressDone, "Trimmed the pause to 0.4 s", func(p *WorkProgress) { p.Render = id }); !m.Ready {
		t.Fatal("not ready with its render")
	}
	if got = taskByID(t, s, j.ID); len(got.Marked) != 1 || !got.Marked[0].Ready || got.Marked[0].Render != id {
		t.Fatalf("ready: %+v", got.Marked)
	}
	// Working on it again takes the mark back.
	mark(t, s, w, j, 2, ProgressWorking, "fixing the pause again")
	if got = taskByID(t, s, j.ID); len(got.Marked) != 0 || got.Working.Note != 2 {
		t.Fatalf("working again: %+v %+v", got.Working, got.Marked)
	}
	// The note stays open in the store: only the report finishes it.
	if n, _ := s.Note(2); n.Status != StatusOpen {
		t.Fatalf("a mark finished the note: %s", n.Status)
	}
}

func TestProgressRefusesWhatIsntTheAttempts(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1)
	for name, p := range map[string]WorkProgress{
		"stale lease":   {Worker: w.ID, Task: j.ID, Lease: "old", Note: 1, State: ProgressWorking},
		"other worker":  {Worker: "other", Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressWorking},
		"not its note":  {Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 2, State: ProgressWorking},
		"no change":     {Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressDone, Text: "  "},
		"long line":     {Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressWorking, Text: strings.Repeat("x", MaxDoing+1)},
		"unknown state": {Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: "finished", Text: "x"},
		"not a render":  {Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressDone, Text: "x", Render: "nope"},
	} {
		if _, e := s.MarkProgress(p); e == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// Note 0 means the task's one note.
	if m := mark(t, s, w, j, 0, ProgressWorking, ""); m.Note != 1 {
		t.Fatalf("%+v", m)
	}
	// A note waiting on the agent's question can't be done.
	if _, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Text: "Which blue?"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.MarkProgress(WorkProgress{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressDone, Text: "Blue"}); e == nil ||
		!strings.Contains(e.Error(), "waits for the answer") {
		t.Fatalf("done on a question: %v", e)
	}
	// A stopping task takes no marks.
	if e := s.ControlTask(j.ID, "pause", "", ""); e != nil {
		t.Fatal(e)
	}
	if _, e := s.MarkProgress(WorkProgress{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, State: ProgressWorking}); e == nil {
		t.Fatal("a stopping task took a mark")
	}
}

// A question takes back the note's mark, and its working line.
func TestAskTakesBackADoneMark(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	mark(t, s, w, j, 1, ProgressDone, "Made the title blue", func(p *WorkProgress) { p.Ready = true })
	mark(t, s, w, j, 2, ProgressWorking, "trimming")
	if _, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 1, Text: "Which blue?"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 2, Text: "Which pause?"}); e != nil {
		t.Fatal(e)
	}
	if got := taskByID(t, s, j.ID); len(got.Marked) != 0 || got.Working != nil {
		t.Fatalf("marks after the questions: %+v %+v", got.Marked, got.Working)
	}
}

// A note retracted mid-batch drops out with its marks; the others keep theirs.
func TestRetractedNoteLeavesItsMarks(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	mark(t, s, w, j, 1, ProgressDone, "Made the title blue", func(p *WorkProgress) { p.Ready = true })
	mark(t, s, w, j, 2, ProgressWorking, "trimming")
	if e := s.DeleteNote(2); e != nil {
		t.Fatal(e)
	}
	if e := s.ReconcileWork(); e != nil {
		t.Fatal(e)
	}
	got := taskByID(t, s, j.ID)
	if got.Working != nil || len(got.Marked) != 1 || got.Marked[0].Note != 1 || !slices.Contains(got.Dropped, 2) {
		t.Fatalf("after the retraction: %+v %+v %v", got.Working, got.Marked, got.Dropped)
	}
	if _, e := s.MarkProgress(WorkProgress{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 2, State: ProgressWorking}); e == nil {
		t.Fatal("marked a retracted note")
	}
}

// The report is the source of truth: a note the agent never marked gets the report's words, a marked one its own
// words unless the report has an entry for it, and a marked note held by a question stays Question. The marks go.
func TestReportReconcilesTheMarks(t *testing.T) {
	s, _, w := automationFixture(t)
	if _, e := s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Text: "make the title blue"},
		{Key: "second", Take: take1, Text: "trim the pause"}, {Key: "third", Take: take1, Text: "louder music"}}}); e != nil {
		t.Fatal(e)
	}
	j := running(t, s, w, 1, 2, 3)
	mark(t, s, w, j, 1, ProgressDone, "Title is blue now", func(p *WorkProgress) { p.Ready = true })
	mark(t, s, w, j, 2, ProgressDone, "Trimmed the pause", func(p *WorkProgress) { p.Ready = true })
	// The report has its own entry for #2 (it wins over the mark); #3 was never marked: the round's message.
	report := WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Edited three things",
		Version: "v7", VersionChecked: true, Notes: map[int64]string{1: "Edited three things", 2: "Cut the pause to 0.4 s",
			3: "Edited three things"}, Said: []int64{2}}
	if _, e := s.ReportWork(report, 0); e != nil {
		t.Fatal(e)
	}
	want := map[int64]string{1: "Title is blue now", 2: "Cut the pause to 0.4 s", 3: "Edited three things"}
	for id, change := range want {
		n, _ := s.Note(id)
		if n.Status != StatusAnswered || n.Change != change || n.AnsweredIn != "v7" {
			t.Errorf("#%d: %s %q in %s", id, n.Status, n.Change, n.AnsweredIn)
		}
	}
	if got := taskByID(t, s, j.ID); got.Working != nil || got.Marked != nil {
		t.Fatalf("marks outlived the report: %+v", got)
	}
}

func TestMarkedNoteHeldByAQuestionStaysQuestion(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	mark(t, s, w, j, 1, ProgressDone, "Title is blue", func(p *WorkProgress) { p.Ready = true })
	mark(t, s, w, j, 2, ProgressDone, "Trimmed", func(p *WorkProgress) { p.Ready = true })
	if _, e := s.AskInTask(WorkAsk{Worker: w.ID, Task: j.ID, Lease: j.Lease, Note: 2, Text: "Which pause, after all?"}); e != nil {
		t.Fatal(e)
	}
	report := WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "completed", Message: "Blue title", Version: "v7",
		VersionChecked: true, Said: []int64{}}
	if _, e := s.ReportWork(report, 0); e != nil {
		t.Fatal(e)
	}
	one, _ := s.Note(1)
	two, _ := s.Note(2)
	if one.Status != StatusAnswered || one.Change != "Title is blue" || two.Status != StatusQuestion {
		t.Fatalf("%s %q, %s", one.Status, one.Change, two.Status)
	}
	if got := taskByID(t, s, j.ID); got.State != "question" || got.Marked != nil {
		t.Fatalf("%+v", got)
	}
}

// A paused attempt finishes nothing: its marks go, and the notes show what the task shows. A new attempt starts
// without marks.
func TestPausedAttemptFinishesNothing(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	mark(t, s, w, j, 1, ProgressDone, "Title is blue", func(p *WorkProgress) { p.Ready = true })
	mark(t, s, w, j, 2, ProgressWorking, "trimming")
	if _, e := s.ReportWork(WorkReport{Worker: w.ID, Task: j.ID, Lease: j.Lease, State: "paused", Message: "Usage limit reached"}, 0); e != nil {
		t.Fatal(e)
	}
	got := taskByID(t, s, j.ID)
	if got.State != "paused" || got.Marked != nil || got.Working != nil {
		t.Fatalf("%+v", got)
	}
	for _, id := range []int64{1, 2} {
		if n, _ := s.Note(id); n.Status != StatusOpen {
			t.Fatalf("#%d is %s", id, n.Status)
		}
	}
}
