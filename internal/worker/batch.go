package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/project"
)

// Batches (spec/automation.md, "Batches"): a task is every comment sent together, done in one agent run that ends in
// one version. Comments sent while an agent works in herdr or tmux join its task as one more message in the same
// session; in the background they wait in the video's next batch.

// batchState is what the running attempt knows of the video's next batch: the server offers it in each poll (next,
// with the notes' details), and the worker says which of its notes it gave the session (join).
type batchState struct {
	next   *project.EditingTask
	notes  map[int64]project.WorkNote
	given  []int64 // given to this attempt's session
	unsent []int64 // given, not yet acknowledged by a poll or report
}

// resetBatch starts a new attempt's bookkeeping.
func (r *runner) resetBatch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batch = batchState{}
}

// offer keeps the latest poll's next batch and notes, for the running attempt's session.
func (r *runner) offer(next *project.EditingTask, notes []project.WorkNote) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batch.next = next
	if r.batch.notes == nil {
		r.batch.notes = map[int64]project.WorkNote{}
	}
	for _, n := range notes {
		r.batch.notes[n.ID] = n
	}
}

// offered is the next batch's notes the session hasn't been given. A task of only typed comments (doc) takes only
// typed comments, so a report alone still finishes it.
func (r *runner) offered(doc bool) []project.WorkNote {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.batch.next == nil {
		return nil
	}
	var out []project.WorkNote
	for _, id := range r.batch.next.Pending() {
		n, ok := r.batch.notes[id]
		if !ok || slices.Contains(r.batch.given, id) || doc && n.WantsVersion() {
			continue
		}
		out = append(out, n)
	}
	return out
}

// gave records notes given to the session: the next poll or report tells the server (join).
func (r *runner) gave(ids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		if !slices.Contains(r.batch.given, id) {
			r.batch.given = append(r.batch.given, id)
			r.batch.unsent = append(r.batch.unsent, id)
		}
	}
}

func (r *runner) unsentJoin() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.batch.unsent)
}

// joined drops notes the server acknowledged from the ones to tell it.
func (r *runner) joined(ids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batch.unsent = slices.DeleteFunc(r.batch.unsent, func(id int64) bool { return slices.Contains(ids, id) })
}

// withGiven is t with the notes given to its session since the attempt started.
func (r *runner) withGiven(t *project.EditingTask) *project.EditingTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	clone := *t
	clone.Notes = slices.Clone(t.Notes)
	for _, id := range r.batch.given {
		if !slices.Contains(clone.Notes, id) {
			clone.Notes = append(clone.Notes, id)
		}
	}
	return &clone
}

// addition is what an interactive attempt needs to give its session more notes of its task.
type addition struct {
	a      project.WorkAssignment                     // the attempt's assignment, with every note given so far
	dir    string                                     // the task folder
	doc    bool                                       // a task of only typed comments
	output string                                     // a plain task's video file, "" otherwise
	write  func(project.WorkAssignment, string) error // writes a context file as the attempt's was written
	k      int                                        // additions so far, counting the attempt's own prompt as 1
}

// more gives the session the next batch's notes it hasn't had: their stills and images, a context and a prompt with
// a new report file, and one message. It returns the new report path, "" when there was nothing to give.
func (r *runner) more(ctx context.Context, v *view, m *addition) (string, []int64, error) {
	add := r.offered(m.doc)
	if len(add) == 0 {
		return "", nil, nil
	}
	t := m.a.Task
	task := *t
	task.Notes = slices.Clone(t.Notes)
	var ids []int64
	for _, n := range add {
		ids = append(ids, n.ID)
		if !slices.Contains(task.Notes, n.ID) {
			task.Notes = append(task.Notes, n.ID)
		}
	}
	sub := project.WorkAssignment{Task: &task, Notes: slices.Clone(add)}
	r.noteFiles(ctx, &sub, m.dir)
	next := m.a
	next.Task = &task
	next.Notes = slices.DeleteFunc(slices.Clone(m.a.Notes), func(n project.WorkNote) bool { return slices.Contains(ids, n.ID) })
	next.Notes = append(next.Notes, sub.Notes...)
	k := m.k + 1
	contextPath := filepath.Join(m.dir, fmt.Sprintf("context-%d-%d.json", t.Attempt, k))
	reportPath := filepath.Join(m.dir, fmt.Sprintf("report-%d-%d.json", t.Attempt, k))
	promptPath := filepath.Join(m.dir, fmt.Sprintf("prompt-%d-%d.md", t.Attempt, k))
	if err := m.write(next, contextPath); err != nil {
		return "", nil, fmt.Errorf("can't save the context of the added comments: %w", err)
	}
	if err := os.WriteFile(promptPath, []byte(morePrompt(t.ID, ids, contextPath, reportPath, m.output)), 0o600); err != nil {
		return "", nil, fmt.Errorf("can't save the prompt of the added comments: %w", err)
	}
	line := fmt.Sprintf("More comments for Retake task %s (attempt %d): follow the instructions in %s", t.ID, t.Attempt, promptPath)
	if err := v.say(ctx, line); err != nil {
		return "", nil, err
	}
	m.a, m.k = next, k
	r.gave(ids)
	r.mu.Lock()
	if r.ask.notes == nil {
		r.ask.notes = map[int64]project.WorkNote{}
	}
	for _, n := range sub.Notes {
		r.ask.notes[n.ID] = n
	}
	r.mu.Unlock()
	return reportPath, ids, nil
}

// morePrompt is the prompt of comments added to a running task in its session.
func morePrompt(task string, ids []int64, contextPath, reportPath, output string) string {
	video := ""
	if output != "" {
		video = fmt.Sprintf("The video still goes to %q, once, with every change in it.\n", output)
	}
	return fmt.Sprintf(`More comments were sent while you work. They are part of this same Retake task (%s), one batch with the notes you have: note IDs %s.
The task context is now %q: the same format as before, with task.notes listing every note of the task, old and new. Their saved stills and images are in this task directory, named as before.
Do them along with the rest, in this same run. %sWhen every note of the task is done or asked about, write your one report to %q, not to the earlier report file, which no longer counts. Use the same JSON as before, covering every note of the task.
`, task, idList(ids), contextPath, video, reportPath)
}

// noteIDs is "#4, #5".
func noteIDs(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = "#" + strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ", ")
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ", ")
}

// noteChanges reads a report's per-note changes ("notes": {"12": "..."}): ids that aren't numbers are left out.
func noteChanges(m map[string]string) map[int64]string {
	if len(m) == 0 {
		return nil
	}
	out := map[int64]string{}
	for k, v := range m {
		id, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(k), "#"), 10, 64)
		if err != nil || id <= 0 || strings.TrimSpace(v) == "" || len(out) >= project.MaxNoteChanges {
			continue
		}
		if len(v) > project.MaxChange {
			v = v[:project.MaxChange]
		}
		out[id] = v
	}
	return out
}
