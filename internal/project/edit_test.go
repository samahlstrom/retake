package project

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// notesEvents is the "why" of every notes event so far.
func notesEvents(t *testing.T, s *Store) []string {
	t.Helper()
	evs, err := s.Events(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		if e.Kind != EventNotes {
			continue
		}
		var d NotesEvent
		if err := json.Unmarshal(e.Data, &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d.Why)
	}
	return out
}

// A person's words survive every notes rebuild (a new transcription, a changed engine), which still updates the rest
// of the note; the same words again change nothing; deleting and restoring the comment keeps them; and the edit says
// who, keeping the words before it.
func TestEditedWordsSurviveRebuilds(t *testing.T) {
	s, _ := newStore(t)
	run := Run{Version: "v1", Comments: []Comment{
		{Key: "take/S1/1", Take: "take", Seg: "S1", Text: "make the logo bigr", T0: 1, T1: 2, Author: "Ana"},
		{Key: "take/S1/2", Take: "take", Seg: "S1", Text: "", T0: 3, T1: 3, Author: "Ana",
			Detail: Detail{Strokes: []Stroke{{Shape: "circle", Color: "red"}}}},
	}}
	if _, err := s.Sync(run); err != nil {
		t.Fatal(err)
	}
	n, changed, err := s.EditNote(1, NoteEdit{Text: "  Make the logo bigger, and centered  ", By: "Ana"})
	if err != nil || !changed || n.Text != "Make the logo bigger, and centered" || !n.Edited {
		t.Fatal(n, changed, err)
	}
	// A drawing-only comment gets words.
	if _, _, err = s.EditNote(2, NoteEdit{Text: "Circle the logo", By: "Ana"}); err != nil {
		t.Fatal(err)
	}
	// The same words: nothing changes, no event.
	before := len(notesEvents(t, s))
	if _, changed, err = s.EditNote(1, NoteEdit{Text: "Make the logo bigger, and centered", By: "Ana"}); err != nil || changed {
		t.Fatal(changed, err)
	}
	if got := notesEvents(t, s); len(got) != before || got[len(got)-1] != "edit" {
		t.Fatal("events:", got)
	}
	// The final transcription hears it differently and the times move: the times follow, the words stay.
	run.Comments[0].Text, run.Comments[0].T0, run.Comments[0].T1 = "make the logo bigger", 1.2, 2.4
	run.Comments[1].Detail.Strokes = append(run.Comments[1].Detail.Strokes, Stroke{Shape: "arrow", Color: "red"})
	res, err := s.Sync(run)
	if err != nil {
		t.Fatal(err)
	}
	n, _ = s.Note(1)
	if n.Text != "Make the logo bigger, and centered" || n.T0 != 1.2 || n.T1 != 2.4 || !n.Edited || n.Status != StatusOpen {
		t.Fatal("after a rebuild:", n, res)
	}
	if d, _ := s.Note(2); d.Text != "Circle the logo" || len(d.Detail.Strokes) != 2 {
		t.Fatal("the drawing-only one:", d)
	}
	var original, by string
	if err = s.db.QueryRow(`SELECT original, by FROM note_edits WHERE note = 1`).Scan(&original, &by); err != nil ||
		original != "make the logo bigr" || by != "Ana" {
		t.Fatal(original, by, err)
	}
	// A second edit keeps the first words as the original.
	if _, _, err = s.EditNote(1, NoteEdit{Text: "Logo bigger", By: "Owner"}); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT original, by FROM note_edits WHERE note = 1`).Scan(&original, &by); err != nil ||
		original != "make the logo bigr" || by != "Owner" {
		t.Fatal(original, by, err)
	}
	// Deleted, it can't be edited; restored, it has its edited words and can be again.
	if err = s.DeleteNote(1); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.EditNote(1, NoteEdit{Text: "anything"}); !errors.Is(err, ErrNotEditable) || !strings.Contains(err.Error(), "deleted") {
		t.Fatal("a deleted note:", err)
	}
	if err = s.RestoreNote(1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sync(run); err != nil {
		t.Fatal(err)
	}
	if n, _ = s.Note(1); n.Text != "Logo bigger" || n.Status != StatusOpen {
		t.Fatal("restored:", n)
	}
	// The sidebar says so.
	items, err := s.Sidebar(SidebarQuery{Version: "v1"})
	if err != nil || len(items) != 2 || !items[0].Edited || items[0].Text != "Logo bigger" {
		t.Fatal(items, err)
	}
}

// Only a comment that is still to do can change, and only with words a comment may have.
func TestEditRefusals(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Sync(Run{Version: "v1", Comments: []Comment{
		{Key: "a", Text: "one"}, {Key: "b", Text: "two"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditNote(99, NoteEdit{Text: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown:", err)
	}
	if _, _, err := s.EditNote(1, NoteEdit{Text: "   "}); !errors.Is(err, ErrBadEdit) {
		t.Fatal("no words and no drawing:", err)
	}
	if _, _, err := s.EditNote(1, NoteEdit{Text: "", Pictured: true}); err != nil {
		t.Fatal("no words but an image:", err)
	}
	if _, _, err := s.EditNote(1, NoteEdit{Text: strings.Repeat("é", MaxDocNote+1)}); !errors.Is(err, ErrBadEdit) {
		t.Fatal("too long:", err)
	}
	if _, _, err := s.EditNote(1, NoteEdit{Text: strings.Repeat("é", MaxDocNote)}); err != nil {
		t.Fatal("as long as it may be:", err)
	}
	if _, err := s.SetNote(2, NoteUpdate{Status: StatusAnswered, In: "v2", Change: "Done"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditNote(2, NoteEdit{Text: "too late"}); !errors.Is(err, ErrNotEditable) {
		t.Fatal("a done note:", err)
	}
	if n, _ := s.Note(2); n.Text != "two" || n.Edited {
		t.Fatal(n)
	}
}

// While a batch runs, a note the agent hasn't reached can change (its task's history says so); one it says it's on,
// or marked done, can't. A queued task's notes can change too.
func TestEditWhileTheAgentWorks(t *testing.T) {
	s, _, w := automationFixture(t)
	j := running(t, s, w, 1, 2)
	if _, _, err := s.EditNote(2, NoteEdit{Text: "trim the pause to half a second", By: "Ana"}); err != nil {
		t.Fatal("a note the agent hasn't reached:", err)
	}
	a, _ := s.Automation()
	if !slices.ContainsFunc(a.Task(j.ID).Events, func(e TaskEvent) bool { return e.Text == "Edited #2" }) {
		t.Fatal("history:", a.Task(j.ID).Events)
	}
	mark(t, s, w, j, 1, ProgressWorking, "making it blue")
	if _, _, err := s.EditNote(1, NoteEdit{Text: "make the title red"}); !errors.Is(err, ErrNotEditable) ||
		!strings.Contains(err.Error(), "the agent is working on it") {
		t.Fatal("the note it's on:", err)
	}
	mark(t, s, w, j, 2, ProgressDone, "Trimmed the pause")
	if _, _, err := s.EditNote(2, NoteEdit{Text: "never mind"}); !errors.Is(err, ErrNotEditable) {
		t.Fatal("a note it marked done:", err)
	}
	if err := EditBlock(Note{ID: 2, Status: StatusOpen}, false, a); err != nil {
		t.Fatal("before the mark:", err)
	}
	// A queued task (the next batch): editable until a worker picks it up and the agent gets to it.
	if _, err := s.Sync(Run{Version: "v6", Comments: []Comment{{Key: "first", Take: take1, Text: "make the title blue"},
		{Key: "second", Take: take1, Text: "trim the pause"}, {Key: "third", Take: take1, Text: "louder music"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueFeedback([]int64{3}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EditNote(3, NoteEdit{Text: "a little louder music"}); err != nil {
		t.Fatal("queued:", err)
	}
	if n, _ := s.Note(2); n.Text != "trim the pause to half a second" {
		t.Fatal("a rebuild put the engine's words back:", n.Text)
	}
}
