package project

import (
	"errors"
	"testing"
)

func TestDeleteSurvivesRebuildAndRestoresPreviousState(t *testing.T) {
	s, _ := newStore(t)
	run := Run{Version: "v1", Comments: []Comment{{Key: "saved/seg/1", Version: "v1", Text: "", Detail: Detail{Strokes: []Stroke{{Shape: "line"}}}}, {Key: "saved/seg/2", Text: "Keep this note"}}}
	if _, err := s.Sync(run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNote(1, NoteUpdate{Status: StatusAnswered, In: "v2", Change: "Checked the drawing"}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.DeleteNote(1); err != nil {
			t.Fatal(err)
		}
	}
	run.Comments[0].Text = "Later transcription"
	if _, err := s.Sync(run); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Note(1)
	if n.Status != StatusRetracted || n.Text != "" || len(n.Detail.Strokes) != 1 {
		t.Fatal(n)
	}
	second, err := Open(s.Root())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if err = second.RestoreNote(1); err != nil {
		t.Fatal(err)
	}
	n, _ = s.Note(1)
	if n.Status != StatusAnswered || n.AnsweredIn != "v2" || n.Change != "Checked the drawing" {
		t.Fatal(n)
	}
	if err = s.RestoreNote(1); err != nil {
		t.Fatal(err)
	}
	other, _ := s.Note(2)
	if other.Status != StatusOpen || other.Text != "Keep this note" {
		t.Fatal(other)
	}
	if err = s.DeleteNote(999); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestDeletingFeedbackCancelsQueuedWorkAndStopsRunningWork(t *testing.T) {
	s, _, worker := automationFixture(t)
	if _, err := s.QueueFeedback([]int64{1}, ""); err != nil {
		t.Fatal(err)
	}
	running, err := s.PollWork(worker, "", "")
	if err != nil || running == nil {
		t.Fatal(running, err)
	}
	queued, err := s.QueueFeedback([]int64{2}, "") // the next batch
	if err != nil || len(queued) != 1 || queued[0].ID == running.ID {
		t.Fatal(queued, err)
	}
	if err = s.DeleteNote(1); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteNote(2); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Automation()
	if a.Task(running.ID).State != "stopping" || !a.Task(running.ID).Stop || a.Task(queued[0].ID).State != "cancelled" {
		t.Fatal(a.Tasks)
	}
	if err = s.RestoreNote(2); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Automation()
	if a.Task(queued[0].ID).State != "cancelled" {
		t.Fatal("undo restarted queued work")
	}
}
