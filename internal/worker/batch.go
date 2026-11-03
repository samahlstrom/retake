package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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

// edited is the notes of the attempt's task whose words the latest poll has changed since its agent got them (a person
// edited them while they waited: spec/automation.md, "Words changed while they wait"), with the words as they are now.
func (r *runner) edited(a project.WorkAssignment) []project.WorkNote {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []project.WorkNote
	for _, g := range a.Notes {
		if a.Task == nil || !slices.Contains(a.Task.Notes, g.ID) {
			continue // another open note, there only as context
		}
		if n, ok := r.batch.notes[g.ID]; ok && n.Text != g.Text {
			out = append(out, n)
		}
	}
	return out
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

// freshPoll waits (3 s at most) for a poll that starts from now to answer, so what was sent just before is known. A
// runner that never polled (a test's) doesn't wait.
func (r *runner) freshPoll(ctx context.Context) {
	r.mu.Lock()
	never := r.connected.IsZero()
	r.mu.Unlock()
	if never {
		return
	}
	from := time.Now()
	for deadline := from.Add(3 * time.Second); time.Now().Before(deadline); {
		r.mu.Lock()
		polled := r.connected.After(from)
		r.mu.Unlock()
		if polled {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// addition is what an attempt needs to give its agent more of its task while it runs: the notes sent meanwhile, and
// (after a report) answers to its questions.
type addition struct {
	a      project.WorkAssignment                      // the attempt's assignment, with every note given so far
	dir    string                                      // the task folder
	doc    bool                                        // a task of only typed comments
	output string                                      // a plain task's video file, "" otherwise
	write  func(project.WorkAssignment, string) error  // writes a context file as the attempt's was written
	full   func(contextPath, reportPath string) string // the whole task prompt, for a background follow-up round
	k      int                                         // rounds so far, counting the attempt's own prompt as 1
}

// round is one more part of an attempt: what the agent is given and where it reports.
type round struct {
	ids                                                  []int64            // notes it gets
	edits                                                []project.WorkNote // its notes whose words changed: the new words
	answers                                              []project.TaskQuestion
	notes                                                []project.WorkNote // their details, with stills
	next                                                 project.WorkAssignment
	k                                                    int
	contextPath, reportPath, promptPath, logPath, prompt string
}

// prepare writes the next round's files, when there is anything to give: the next batch's notes not given yet, the
// new words of its notes a person edited meanwhile, and with answers, the answers to the task's questions that
// haven't reached the agent. Their stills and images, a context with every note of the task, and a prompt with a new
// report file (follow-up: the whole task prompt, for a new process of the agent). nil when there is nothing to give.
func (r *runner) prepare(ctx context.Context, m *addition, answers, followUp bool) (*round, error) {
	add := r.offered(m.doc)
	edits := r.edited(m.a)
	var told []project.TaskQuestion
	if answers {
		told = r.untold()
	}
	if len(add) == 0 && len(told) == 0 && len(edits) == 0 {
		return nil, nil
	}
	t := m.a.Task
	task := *t
	task.Notes = slices.Clone(t.Notes)
	rd := &round{answers: told, edits: edits, k: m.k + 1}
	for _, n := range add {
		rd.ids = append(rd.ids, n.ID)
		if !slices.Contains(task.Notes, n.ID) {
			task.Notes = append(task.Notes, n.ID)
		}
	}
	sub := project.WorkAssignment{Task: &task, Notes: slices.Clone(add)}
	r.noteFiles(ctx, &sub, m.dir)
	rd.notes = sub.Notes
	rd.next = m.a
	rd.next.Task = &task
	rd.next.Notes = slices.DeleteFunc(slices.Clone(m.a.Notes), func(n project.WorkNote) bool { return slices.Contains(rd.ids, n.ID) })
	rd.next.Notes = append(rd.next.Notes, sub.Notes...)
	for _, e := range edits { // the words as they are now; its stills stay as the worker saved them
		if i := slices.IndexFunc(rd.next.Notes, func(n project.WorkNote) bool { return n.ID == e.ID }); i >= 0 {
			rd.next.Notes[i].Text, rd.next.Notes[i].Edited = e.Text, e.Edited
		}
	}
	r.mu.Lock()
	if len(r.ask.questions) > 0 {
		rd.next.Questions = slices.Clone(r.ask.questions) // with the answers that came meanwhile
	}
	r.mu.Unlock()
	rd.contextPath = filepath.Join(m.dir, fmt.Sprintf("context-%d-%d.json", t.Attempt, rd.k))
	rd.reportPath = filepath.Join(m.dir, fmt.Sprintf("report-%d-%d.json", t.Attempt, rd.k))
	rd.promptPath = filepath.Join(m.dir, fmt.Sprintf("prompt-%d-%d.md", t.Attempt, rd.k))
	rd.logPath = filepath.Join(m.dir, fmt.Sprintf("agent-%d-%d.log", t.Attempt, rd.k))
	if err := m.write(rd.next, rd.contextPath); err != nil {
		return nil, fmt.Errorf("can't save the context of the added comments: %w", err)
	}
	rd.prompt = morePrompt(t.ID, rd.ids, edits, told, rd.contextPath, rd.reportPath, m.output)
	if followUp {
		rd.prompt = followUpPrompt(t.ID, rd.ids, edits, told, m.output) + m.full(rd.contextPath, rd.reportPath)
	}
	if err := os.WriteFile(rd.promptPath, []byte(rd.prompt), 0o600); err != nil {
		return nil, fmt.Errorf("can't save the prompt of the added comments: %w", err)
	}
	return rd, nil
}

// commit records a round the agent got: the next one builds on it, the next poll or report tells the server its notes
// (join), and its answers are told.
func (r *runner) commit(m *addition, rd *round) {
	m.a, m.k = rd.next, rd.k
	r.gave(rd.ids)
	for _, q := range rd.answers {
		r.tell(q.Answer.ID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ask.notes == nil {
		r.ask.notes = map[int64]project.WorkNote{}
	}
	for _, n := range rd.notes {
		r.ask.notes[n.ID] = n
	}
}

// more gives a live session what it hasn't had (answers too, with answers set) as one message for the same task. It
// returns the round, whose report file counts now; nil when there was nothing to give.
func (r *runner) more(ctx context.Context, v *view, m *addition, answers bool) (*round, error) {
	rd, err := r.prepare(ctx, m, answers, false)
	if rd == nil || err != nil {
		return nil, err
	}
	t := m.a.Task
	what := "More comments"
	switch {
	case len(rd.ids) > 0:
	case len(rd.edits) > 0:
		what = "Changed comments"
	default:
		what = "Answers"
	}
	line := fmt.Sprintf("%s for Retake task %s (attempt %d): follow the instructions in %s", what, t.ID, t.Attempt, rd.promptPath)
	if err = v.say(ctx, line); err != nil {
		return nil, err
	}
	r.commit(m, rd)
	return rd, nil
}

// nextRound starts another round of the attempt after its agent reported, when comments were sent meanwhile (or, in
// the background, answers came to its questions): in herdr or tmux a message in the session, in the background a
// follow-up run of the same agent, so they go in the same version. It returns the notes the round does and the round,
// whose prompt, context, report and log are the attempt's now; a nil round when there is none.
func (r *runner) nextRound(ctx context.Context, mode string, v *view, m *addition) ([]int64, *round, error) {
	if mode != OpenBackground {
		rd, err := r.more(ctx, v, m, true)
		if err != nil || rd == nil {
			return nil, nil, err
		}
		if len(rd.ids) > 0 {
			r.log("%s: gave the agent %d more comments (%s) after its report", filepath.Base(r.opts.Root), len(rd.ids), noteIDs(rd.ids))
		}
		r.logEdits(rd)
		return rd.ids, rd, nil
	}
	rd, err := r.prepare(ctx, m, true, true)
	if rd == nil || err != nil {
		return nil, nil, err
	}
	r.commit(m, rd)
	ids := slices.Clone(rd.ids)
	for _, q := range rd.answers {
		if !slices.Contains(ids, q.Note) {
			ids = append(ids, q.Note)
		}
	}
	for _, e := range rd.edits {
		if !slices.Contains(ids, e.ID) {
			ids = append(ids, e.ID)
		}
	}
	if len(rd.ids) > 0 {
		r.log("%s: the agent carries on with %d more comments (%s) in the same version", filepath.Base(r.opts.Root), len(rd.ids), noteIDs(rd.ids))
	}
	r.logEdits(rd)
	return ids, rd, nil
}

// logEdits says which notes' new words a round gave the agent.
func (r *runner) logEdits(rd *round) {
	if len(rd.edits) == 0 {
		return
	}
	ids := make([]int64, len(rd.edits))
	for i, e := range rd.edits {
		ids[i] = e.ID
	}
	r.log("%s: the agent got the new words of %s", filepath.Base(r.opts.Root), noteIDs(ids))
}

// editLines says, one line each, which notes a person reworded meanwhile and what they say now.
func editLines(b *strings.Builder, edits []project.WorkNote) {
	for _, e := range edits {
		words := strconv.Quote(e.Text)
		if strings.TrimSpace(e.Text) == "" {
			words = "nothing (only its drawing)"
		}
		fmt.Fprintf(b, "The person changed the words of note %d while it waited. It now says: %s. Do it as it says now; if you already did it, check that your change still fits these words, and change it if not.\n", e.ID, words)
	}
}

// morePrompt is the prompt of comments (and answers, and new words of its notes) added to a running task in its
// session.
func morePrompt(task string, ids []int64, edits []project.WorkNote, answers []project.TaskQuestion, contextPath, reportPath, output string) string {
	var b strings.Builder
	if len(ids) > 0 {
		fmt.Fprintf(&b, "More comments were sent while you work. They are part of this same Retake task (%s), one batch with the notes you have, and go in the same new version: note IDs %s.\n", task, idList(ids))
	}
	editLines(&b, edits)
	for _, q := range answers {
		fmt.Fprintf(&b, "%s.\n", answerLine(q))
	}
	video := ""
	if output != "" {
		video = fmt.Sprintf("The video still goes to %q, once, with every change in it. ", promptPath(output))
	}
	fmt.Fprintf(&b, `The task context is now %q: the same format as before, with task.notes listing every note of the task, old and new. Their saved stills and images are in this task directory, named as before.
Do this along with the rest, in this same run. %sWhen every note of the task is done or asked about, write your one report to %q, not to the earlier report file, which no longer counts. Use the same JSON as before, covering every note of the task.
`, promptPath(contextPath), video, promptPath(reportPath))
	return b.String()
}

// followUpPrompt starts the prompt of a background follow-up round: what is new, before the whole task prompt.
func followUpPrompt(task string, ids []int64, edits []project.WorkNote, answers []project.TaskQuestion, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This is a follow-up round of Retake task %s. You, or an agent before you, already did its other notes in this run: their changes are on disk, so keep them. ", task)
	if output != "" {
		fmt.Fprintf(&b, "The video so far, with those changes, is %q: start from it, not from the versions, and write the new result to that same file. ", promptPath(output))
	}
	if len(ids) > 0 {
		fmt.Fprintf(&b, "Now do the notes sent meanwhile, note IDs %s, in the same new version.", idList(ids))
	}
	b.WriteString("\n")
	editLines(&b, edits)
	for _, q := range answers {
		fmt.Fprintf(&b, "%s. Use it for that note now.\n", answerLine(q))
	}
	b.WriteString("\n")
	return b.String()
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
