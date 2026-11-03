package project

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The agent's progress inside a task (spec/automation.md, "Marking progress"): `retake working` says which note it is
// on, `retake done` that a note's edit is in. They only change what the page shows while the attempt runs; the report
// still finishes the notes.

// NoteDoing is the note an attempt's agent works on now, with a few words of its own.
type NoteDoing struct {
	Note int64  `json:"note"`
	Text string `json:"text,omitempty"`
	At   int64  `json:"at"`
}

// NoteMark is a note the agent says is done, with what changed. Ready says the edit is in a playable revision of the
// task's target (Render, a render of this task), or that the note needs none (a document's comment); until then the
// worker is putting it in.
type NoteMark struct {
	Note   int64  `json:"note"`
	Change string `json:"change"`
	At     int64  `json:"at"`
	Ready  bool   `json:"ready,omitempty"`
	Render string `json:"render,omitempty"`
}

// MaxDoing is the longest working line, in characters.
const MaxDoing = 200

// Progress states.
const (
	ProgressWorking = "working"
	ProgressDone    = "done"
)

// WorkProgress is a worker's POST /api/worker/progress: a `retake working` or `retake done` from the task's running
// attempt. Render is the revision that has a done note's edit ("" while it's being put in); Join is the notes of the
// next batch the worker gave the session, as in a poll. Worker comes from the request.
type WorkProgress struct {
	Worker string  `json:"-"`
	Task   string  `json:"task"`
	Lease  string  `json:"lease"`
	Note   int64   `json:"note"`
	State  string  `json:"state"`
	Text   string  `json:"text"`
	Render string  `json:"render,omitempty"`
	Ready  bool    `json:"ready,omitempty"`
	Join   []int64 `json:"join,omitempty"`
}

// ProgressMark is what the server answers: the note, its state, and the version its edit goes in.
type ProgressMark struct {
	Note   int64  `json:"note"`
	State  string `json:"state"`
	Target string `json:"target,omitempty"`
	Ready  bool   `json:"ready,omitempty"`
}

// mark is note id's mark, if the agent marked it done.
func (t *EditingTask) mark(id int64) *NoteMark {
	for i := range t.Marked {
		if t.Marked[i].Note == id {
			return &t.Marked[i]
		}
	}
	return nil
}

// unmark takes back the agent's marks on ids: done, and working.
func (t *EditingTask) unmark(ids ...int64) {
	t.Marked = slices.DeleteFunc(t.Marked, func(m NoteMark) bool { return slices.Contains(ids, m.Note) })
	if len(t.Marked) == 0 {
		t.Marked = nil
	}
	if t.Working != nil && slices.Contains(ids, t.Working.Note) {
		t.Working = nil
	}
}

// clearProgress forgets the agent's marks: its attempt ended, or a new one starts.
func (t *EditingTask) clearProgress() {
	t.Working, t.Marked = nil, nil
}

// changeFor is what changed for note id when a report finishes it: the agent's own report entry (said), else what it
// said with retake done, else the report's words for it (the round's message, filled in by the worker).
func (r WorkReport) changeFor(j *EditingTask, id int64) string {
	if m := j.mark(id); m != nil && !slices.Contains(r.Said, id) && strings.TrimSpace(m.Change) != "" {
		return m.Change
	}
	return r.change(id)
}

// MarkProgress records a `retake working` or `retake done` from the task's running attempt. Notes the worker gave the
// session (Join) join the task first, as in a poll.
func (s *Store) MarkProgress(p WorkProgress) (ProgressMark, error) {
	text := strings.Join(strings.Fields(p.Text), " ")
	switch p.State {
	case ProgressWorking:
		if utf8.RuneCountInString(text) > MaxDoing {
			return ProgressMark{}, fmt.Errorf("say what you're doing in a few words (at most %d characters)", MaxDoing)
		}
	case ProgressDone:
		text = strings.TrimSpace(p.Text)
		if text == "" || utf8.RuneCountInString(text) > MaxChange {
			return ProgressMark{}, fmt.Errorf("say what changed (1 to %d characters)", MaxChange)
		}
	default:
		return ProgressMark{}, errors.New(`the state is "working" or "done"`)
	}
	if len(p.Join) > 1000 {
		return ProgressMark{}, errors.New("too many notes to join")
	}
	var out ProgressMark
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		j, err := a.WorkAttempt(p.Task, p.Worker, p.Lease, now)
		if err != nil {
			return err
		}
		if j.Stop {
			return errors.New("task is stopping")
		}
		if a.join(j.ID, p.Join, now) {
			j = a.Task(p.Task)
		}
		pending := j.Pending()
		note := p.Note
		if note == 0 {
			if len(pending) != 1 {
				return fmt.Errorf("say which note: this task has %s", noteList(pending))
			}
			note = pending[0]
		}
		if !slices.Contains(pending, note) {
			return fmt.Errorf("note #%d isn't one of this task's notes to do (%s)", note, noteList(pending))
		}
		n, err := noteByID(t, note)
		if err != nil {
			return err
		}
		switch {
		case n.Status != StatusOpen && n.Status != StatusQuestion:
			return fmt.Errorf("note #%d is %s: it isn't to do any more", note, n.Status)
		case p.State == ProgressDone && n.Status == StatusQuestion:
			return fmt.Errorf("note #%d waits for the answer to your question: it can't be done before that", note)
		}
		ready := p.Ready
		if p.State == ProgressWorking {
			j.unmark(note)
			j.Working = &NoteDoing{Note: note, Text: text, At: now}
		} else {
			if p.Render != "" {
				v := a.Render(p.Render)
				if v == nil || v.Task != j.ID || v.Worker != p.Worker || !v.Complete || v.Error != "" {
					return errors.New("that revision isn't a playable render of this task")
				}
				ready = true
			}
			// Said again, it replaces the mark: a new edit is being put in.
			m := NoteMark{Note: note, Change: text, At: now, Ready: ready, Render: p.Render}
			if old := j.mark(note); old != nil {
				*old = m
			} else {
				j.Marked = append(j.Marked, m)
			}
			if j.Working != nil && j.Working.Note == note {
				j.Working = nil
			}
		}
		if err = saveAutomation(t, a); err != nil {
			return err
		}
		t.emit("automation", map[string]bool{"changed": true})
		out = ProgressMark{Note: note, State: p.State, Target: j.Target, Ready: ready}
		return nil
	})
	return out, err
}
