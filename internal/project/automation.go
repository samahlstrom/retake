package project

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
)

// WorkerLease is how long an attempt's lease lasts without a poll. A worker polls every second.
const WorkerLease = 30 * time.Second

// ReconnectGrace is how long a lapsed attempt or chat turn stays running for its worker to come back and take it back
// (spec/automation.md, "Agents outlive Retake"): a restart of Retake never stops an agent, so a lapse is usually one.
// After it the task pauses (LostError) and the turn ends.
var ReconnectGrace = 10 * time.Minute

// LostError is a task's error when it paused because its worker didn't come back within the grace.
const LostError = "Worker disconnected; retry or switch agent"

// lapsed reports whether a lease that ran out at until is past its grace at now.
func lapsed(until, now int64) bool { return until+int64(ReconnectGrace.Seconds()) <= now }

// AgentProfile is a locally configured executable. The server only receives its identity, never its command.
type AgentProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// EditingWorker advertises the agents available on a designated machine.
type EditingWorker struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Session string         `json:"session,omitempty"`
	Agents  []AgentProfile `json:"agents"`
	Seen    int64          `json:"seen"`
	// Extensions are the ones turned on on that computer (spec/extensions.md, "Turning them on"): their kinds,
	// agent instructions and tools run there.
	Extensions []ExtRef `json:"extensions,omitempty"`
}

// ExtRef is an extension a worker has: its id, version and extension API.
type ExtRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	API     int    `json:"api"`
}

// AutomationConfig selects the one machine allowed to edit this project.
type AutomationConfig struct {
	Enabled  bool   `json:"enabled"`
	Worker   string `json:"worker"`
	Agent    string `json:"agent"`
	Fallback string `json:"fallback,omitempty"`
}

// TaskEvent is a durable, provider-independent handoff entry.
type TaskEvent struct {
	At    int64  `json:"at"`
	Agent string `json:"agent,omitempty"`
	Text  string `json:"text"`
}

// EditingTask keeps its identity when execution changes providers.
type EditingTask struct {
	ID      string  `json:"id"`
	Notes   []int64 `json:"notes"`
	Version string  `json:"version"`
	// Target is the version the task makes or updates: its notes' version + 1 (spec/automation.md, "Comments on vN
	// become edits in vN+1"). "" for a task an older store queued before targets.
	Target    string      `json:"target,omitempty"`
	State     string      `json:"state"`
	Agent     string      `json:"agent"`
	Worker    string      `json:"worker"`
	Lease     string      `json:"lease,omitempty"`
	Until     int64       `json:"until,omitempty"`
	Attempt   int         `json:"attempt"`
	NextAgent string      `json:"next_agent,omitempty"`
	Stop      bool        `json:"stop,omitempty"`
	Error     string      `json:"error,omitempty"`
	Question  string      `json:"question,omitempty"`
	Result    string      `json:"result,omitempty"`
	Events    []TaskEvent `json:"events"`
	Usage     *AgentUsage `json:"usage,omitempty"`
	// Asked is the questions its agents asked from inside the task (retake ask), Told the answers to them that
	// reached an agent, and Done the notes an earlier attempt finished while others waited on an answer
	// (spec/automation.md, "Questions from inside a task").
	Asked []TaskAsk `json:"asked,omitempty"`
	Told  []int64   `json:"told,omitempty"`
	Done  []int64   `json:"done,omitempty"`
	// Dropped is the notes that left the batch: retracted or answered outside it (spec/automation.md, "Batches").
	Dropped []int64 `json:"dropped,omitempty"`
	// Lost says it paused because its worker didn't come back within the grace: that worker may take it back
	// (spec/automation.md, "Agents outlive Retake").
	Lost bool `json:"lost,omitempty"`
	// Working is the note the running attempt's agent works on now, and Marked the notes it says are done, in the
	// order it said so (retake working, retake done: spec/automation.md, "Marking progress"). The report is the
	// source of truth: both go when the attempt ends.
	Working *NoteDoing `json:"working,omitempty"`
	Marked  []NoteMark `json:"marked,omitempty"`
}

// lost reports whether t is paused because its worker went away (Lost, or the error an older version gave it).
func (t *EditingTask) lost() bool { return t.State == "paused" && (t.Lost || t.Error == LostError) }

// AgentUsage reports provider telemetry, never an invented remaining subscription balance.
type AgentUsage struct {
	Input  int64    `json:"input_tokens,omitempty"`
	Output int64    `json:"output_tokens,omitempty"`
	Cost   *float64 `json:"estimated_cost_usd,omitempty"`
}

// LiveRender describes immutable media retained on a worker and served through the relay. A draft is a render that
// isn't a version yet (spec/automation.md, "Versions versus drafts"); Alias is the draft label a promoted one had.
type LiveRender struct {
	Draft    bool             `json:"draft,omitempty"`
	Alias    string           `json:"alias,omitempty"`
	Stopped  bool             `json:"stopped,omitempty"`
	Reused   int              `json:"reused"`
	Rendered int              `json:"rendered"`
	Manifest *bridge.Manifest `json:"manifest,omitempty"`
	ID       string           `json:"id"`
	Worker   string           `json:"worker"`
	Task     string           `json:"task,omitempty"`
	Label    string           `json:"label"`
	N        int              `json:"n"`
	Source   string           `json:"source"`
	Duration float64          `json:"duration"`
	FPS      float64          `json:"fps"`
	Width    int              `json:"width"`
	Height   int              `json:"height"`
	Done     int              `json:"done"`
	Total    int              `json:"total"`
	Seconds  float64          `json:"seconds"`
	Complete bool             `json:"complete"`
	Error    string           `json:"error,omitempty"`
	// Media is the render's one media file when it isn't an HLS playlist (index.m3u8): a plain project's revision
	// is the agent's video as it was at a `retake done`, video.mp4 (spec/automation.md, "Versions versus drafts").
	Media string `json:"media,omitempty"`
}

// MediaName is the file the render plays from: its Media, else its playlist.
func (v LiveRender) MediaName() string {
	if v.Media != "" {
		return v.Media
	}
	return "index.m3u8"
}

// FileVersion is a version file on a connected computer (spec/automation.md, "Version files"): the host keeps its
// metadata, and relays its bytes from that worker when a page plays it. Modified is its mtime in Unix ms.
type FileVersion struct {
	Worker   string  `json:"worker"`
	Label    string  `json:"label"`
	N        int     `json:"n"`
	Name     string  `json:"name"`
	Size     int64   `json:"size"`
	Modified int64   `json:"modified"`
	Duration float64 `json:"duration"`
	FPS      float64 `json:"fps"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	// Kind is an extension's version (v0): a folder, not a video file (spec/extensions.md, "Version kinds").
	Kind string `json:"kind,omitempty"`
}

// ReviewSubmission waits for final transcription before scheduling its notes. Send means the reviewer asked for
// the notes to go to the agent, whatever the automation config says.
type ReviewSubmission struct {
	Take    string  `json:"take"`
	Version string  `json:"version"`
	After   []int64 `json:"after"`
	Done    bool    `json:"done"`
	Send    bool    `json:"send,omitempty"`
	Agent   string  `json:"agent,omitempty"` // the agent this send picked ("" = the configured one)
}

// AutomationState is stored transactionally with the project's event log.
type AutomationState struct {
	Config      AutomationConfig   `json:"config"`
	Workers     []EditingWorker    `json:"workers"`
	Tasks       []EditingTask      `json:"tasks"`
	Renders     []LiveRender       `json:"renders"`
	Files       []FileVersion      `json:"files,omitempty"`
	Submissions []ReviewSubmission `json:"submissions,omitempty"`
	// Chat is the video's chat with the agent: its turn and session (spec/chat.md). Its messages are the chat table.
	Chat *ChatState `json:"chat,omitempty"`
	// VideoAgent is the agent the owner picked for this video ("" = the configured one, Config.Agent): a task queued
	// without an agent takes it at its claim (spec/automation.md, "An agent for one video").
	VideoAgent string `json:"video_agent,omitempty"`
}

// queueAgent is the agent a send without one queues its task with: the video's own agent while the selected worker
// (or, before one is selected, any worker) offers it, else the configured one.
func (a *AutomationState) queueAgent() string {
	if a.VideoAgent != "" && a.offers(a.VideoAgent) {
		return a.VideoAgent
	}
	return a.Config.Agent
}

// agentFor is the agent a task (or a chat turn) without one runs with on w: the video's own agent when w offers it,
// else the configured one.
func (a *AutomationState) agentFor(w *EditingWorker) string {
	if a.VideoAgent != "" && w.HasAgent(a.VideoAgent) {
		return a.VideoAgent
	}
	return a.Config.Agent
}

// NewWorkID returns an unguessable identifier for an attempt or media revision.
func NewWorkID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// ValidWorkID permits only flat identifiers in worker and media routes.
func ValidWorkID(s string) bool {
	if len(s) < 1 || len(s) > 80 {
		return false
	}
	for _, c := range s {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
		if !valid {
			return false
		}
	}
	return true
}

func readAutomation(q querier) (AutomationState, error) {
	a := AutomationState{Workers: []EditingWorker{}, Tasks: []EditingTask{}, Renders: []LiveRender{}}
	rows, err := q.Query(`SELECT data FROM automation WHERE id = 1`)
	if err != nil {
		return a, err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var data string
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal([]byte(data), &a)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	return a, err
}

// Automation returns the current persisted state. Callers must redact leases before sending it to reviewers.
func (s *Store) Automation() (AutomationState, error) { return readAutomation(s.db) }

// UpdateAutomation serializes assignments across processes and persists their events atomically.
func (s *Store) UpdateAutomation(fn func(*AutomationState) (bool, error)) error {
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		reconciled, err := s.reconcileWork(t, &a)
		if err != nil {
			return err
		}
		changed, err := fn(&a)
		if err != nil {
			return err
		}
		changed = a.mergeQueued(s.Now().Unix()) || changed || reconciled
		b, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = t.Exec(`INSERT INTO automation (id,data) VALUES (1,?) ON CONFLICT (id) DO UPDATE SET data = excluded.data`, string(b))
		if err == nil && changed {
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "automation"})
		}
		return err
	})
}

func (s *Store) reconcileWork(t *tx, a *AutomationState) (bool, error) {
	now := s.Now().Unix()
	changed, err := t.reconcileChat(a, now)
	if err != nil {
		return changed, err
	}
	for i := range a.Tasks {
		j := &a.Tasks[i]
		if j.State == "completed" || j.State == "cancelled" {
			continue
		}
		// A lapsed lease is usually Retake restarting: the agent works on, and its worker takes it back. Only after the
		// grace does the task pause (spec/automation.md, "Agents outlive Retake").
		if (j.State == "running" || j.State == "stopping") && lapsed(j.Until, now) {
			j.State = "paused"
			j.Error = LostError
			j.Lost = true
			j.Lease = ""
			j.Stop = false
			j.AddEvent(now, j.Error)
			changed = true
		}
		// A note retracted or answered outside the batch drops out of it; the rest carry on (spec/automation.md,
		// "Batches"). A question (its own agent's, waiting for the person) doesn't withdraw the note.
		var dropped []int64
		for _, id := range j.Pending() {
			n, e := noteByID(t, id)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return changed, e
			}
			if e == nil && (n.Status == StatusOpen || n.Status == StatusQuestion) {
				continue
			}
			dropped = append(dropped, id)
		}
		if len(dropped) == 0 {
			continue
		}
		j.Dropped = append(j.Dropped, dropped...)
		j.unmark(dropped...)
		j.AddEvent(now, "Dropped "+noteList(dropped)+": resolved or deleted outside this task")
		changed = true
		if len(j.Pending()) > 0 {
			continue
		}
		if j.State == "running" || j.State == "stopping" {
			if !j.Stop {
				j.State = "stopping"
				j.Stop = true
				j.NextAgent = ""
			}
		} else {
			j.State = "cancelled"
			j.Error = "Feedback was resolved or retracted outside this task"
			j.AddEvent(now, j.Error)
		}
	}
	return changed, nil
}

// queuedFor is the task of target waiting to run, if any: after mergeQueued there is at most one per target.
func (a *AutomationState) queuedFor(target string) *EditingTask {
	for i := range a.Tasks {
		if a.Tasks[i].State == "queued" && a.Tasks[i].Target == target {
			return &a.Tasks[i]
		}
	}
	return nil
}

// owner is the unfinished task that still has note id to do, if any.
func (a *AutomationState) owner(id int64) *EditingTask {
	for i := len(a.Tasks) - 1; i >= 0; i-- {
		j := &a.Tasks[i]
		if j.State != "completed" && j.State != "cancelled" && slices.Contains(j.Pending(), id) {
			return j
		}
	}
	return nil
}

// add puts note id in the batch, to do: a note it finished or dropped before is to do again.
func (t *EditingTask) add(id int64) {
	if !slices.Contains(t.Notes, id) {
		t.Notes = append(t.Notes, id)
	}
	t.Done = slices.DeleteFunc(t.Done, func(n int64) bool { return n == id })
	t.Dropped = slices.DeleteFunc(t.Dropped, func(n int64) bool { return n == id })
}

// absorb takes another batch's notes, questions, told answers and finished notes into this one.
func (t *EditingTask) absorb(o EditingTask, now int64) {
	var added []int64
	pending := o.Pending()
	for _, id := range o.Notes {
		if slices.Contains(pending, id) {
			if !slices.Contains(t.Pending(), id) {
				t.add(id)
			}
			added = append(added, id)
			continue
		}
		if slices.Contains(t.Pending(), id) {
			continue // to do here, whatever the other batch did with it
		}
		if !slices.Contains(t.Notes, id) {
			t.Notes = append(t.Notes, id)
		}
		if slices.Contains(o.Done, id) && !slices.Contains(t.Done, id) {
			t.Done = append(t.Done, id)
		} else if slices.Contains(o.Dropped, id) && !slices.Contains(t.Dropped, id) && !slices.Contains(t.Done, id) {
			t.Dropped = append(t.Dropped, id)
		}
	}
	for _, ask := range o.Asked {
		if !slices.ContainsFunc(t.Asked, func(x TaskAsk) bool { return x.ID == ask.ID }) {
			t.Asked = append(t.Asked, ask)
		}
	}
	t.tell(o.Told)
	if t.Version == "" {
		t.Version = o.Version
	}
	t.AddEvent(now, "Joined task "+o.ID+": "+noteList(added))
}

// pausedFor is the paused batch of target, if any: after mergeQueued there is at most one per target.
func (a *AutomationState) pausedFor(target string) *EditingTask {
	for i := range a.Tasks {
		if a.Tasks[i].State == "paused" && a.Tasks[i].Target == target {
			return &a.Tasks[i]
		}
	}
	return nil
}

// mergeQueued makes the tasks of one target waiting in queued one task, and its paused tasks one paused batch
// (spec/automation.md, "Merging queued tasks"): the oldest keeps its id, agent and history and absorbs the others,
// which are removed. It reports whether it merged any.
func (a *AutomationState) mergeQueued(now int64) bool {
	out := make([]EditingTask, 0, len(a.Tasks))
	into := map[[2]string]int{} // state and target -> the oldest task waiting so, in out
	merged := false
	for _, j := range a.Tasks {
		if j.State == "queued" || j.State == "paused" {
			k := [2]string{j.State, j.Target}
			if i, ok := into[k]; ok {
				out[i].absorb(j, now)
				merged = true
				continue
			}
			into[k] = len(out)
		}
		out = append(out, j)
	}
	if merged {
		a.Tasks = out
	}
	return merged
}

// join moves the notes of the queued batch that a worker gave the running task's live session (ids) into that task
// (spec/automation.md, "Batches"), and drops the queued batch once it has nothing left to do. Pointers into a.Tasks
// may be stale afterwards. It reports whether it moved any.
func (a *AutomationState) join(task string, ids []int64, now int64) bool {
	j := a.Task(task)
	if j == nil || len(ids) == 0 {
		return false
	}
	q := a.queuedFor(j.Target)
	if q == nil || q.ID == j.ID {
		return false
	}
	var moved []int64
	for _, id := range ids {
		if !slices.Contains(q.Pending(), id) || slices.Contains(j.Pending(), id) || slices.Contains(moved, id) {
			continue
		}
		q.Notes = slices.DeleteFunc(q.Notes, func(n int64) bool { return n == id })
		j.add(id)
		moved = append(moved, id)
	}
	if len(moved) == 0 {
		return false
	}
	j.AddEvent(now, "Added "+noteList(moved)+" in the agent's session")
	if len(q.Pending()) > 0 {
		q.AddEvent(now, "Moved "+noteList(moved)+" to the running task "+j.ID)
		return true
	}
	id := q.ID
	a.Tasks = slices.DeleteFunc(a.Tasks, func(t EditingTask) bool { return t.ID == id })
	return true
}

// next is the queued task the running attempt of task may take in: of its target, and one that never ran.
func (a *AutomationState) next(task string) *EditingTask {
	j := a.Task(task)
	if j == nil {
		return nil
	}
	q := a.queuedFor(j.Target)
	if q == nil || j.State != "running" || j.Stop || q.Attempt > 0 || len(q.Asked) > 0 || len(q.Pending()) == 0 {
		return nil
	}
	clone := *q
	clone.Lease = ""
	return &clone
}

// batchQueued gives the tasks an older store queued their targets and merges the queued ones (one per note, then) per
// target, and the paused ones per target, when the store opens (spec/automation.md, "Comments on vN become edits in vN+1"). It writes nothing when
// there's nothing to do.
func (s *Store) batchQueued() error {
	a, err := s.Automation()
	if err != nil {
		return err
	}
	untargeted := slices.ContainsFunc(a.Tasks, func(j EditingTask) bool {
		return j.Target == "" && j.State != "completed" && j.State != "cancelled"
	})
	waiting := func(state string) int {
		return len(slices.DeleteFunc(slices.Clone(a.Tasks), func(j EditingTask) bool { return j.State != state }))
	}
	if !untargeted && waiting("queued") < 2 && waiting("paused") < 2 {
		return nil
	}
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		// A task from before targets gets its first note's (comments on vN become edits in vN+1).
		targeted := false
		for i := range a.Tasks {
			j := &a.Tasks[i]
			if j.Target != "" || j.State == "completed" || j.State == "cancelled" || len(j.Pending()) == 0 {
				continue
			}
			n, e := noteByID(t, j.Pending()[0])
			if e != nil && !errors.Is(e, ErrNotFound) {
				return e
			}
			if e == nil {
				j.Target = n.Target()
				targeted = true
			}
		}
		if !a.mergeQueued(s.Now().Unix()) && !targeted {
			return nil
		}
		if err = saveAutomation(t, a); err == nil {
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "automation"})
		}
		return err
	})
}

// ReconcileWork expires disconnected attempts and cancels feedback that was withdrawn or resolved elsewhere.
func (s *Store) ReconcileWork() error {
	return s.UpdateAutomation(func(*AutomationState) (bool, error) { return false, nil })
}

// Task finds a mutable task within a transaction's state.
func (a *AutomationState) Task(id string) *EditingTask {
	for i := range a.Tasks {
		if a.Tasks[i].ID == id {
			return &a.Tasks[i]
		}
	}
	return nil
}

// Worker finds a registered machine.
func (a *AutomationState) Worker(id string) *EditingWorker {
	for i := range a.Workers {
		if a.Workers[i].ID == id {
			return &a.Workers[i]
		}
	}
	return nil
}

// Render finds a retained render by ID, public label, or the draft label it had before it became a version.
func (a *AutomationState) Render(id string) *LiveRender {
	for i := range a.Renders {
		if a.Renders[i].ID == id || a.Renders[i].Label == id {
			return &a.Renders[i]
		}
	}
	for i := range a.Renders {
		if a.Renders[i].Alias != "" && a.Renders[i].Alias == id {
			return &a.Renders[i]
		}
	}
	return nil
}

// FileSource is the worker whose version files the family lists: the selected one when it sent files, else the one
// seen last of those that did; "" when none did.
func (a *AutomationState) FileSource() string {
	src, seen := "", int64(-1)
	for _, f := range a.Files {
		if f.Worker == a.Config.Worker {
			return f.Worker
		}
		if w := a.Worker(f.Worker); w != nil && w.Seen > seen {
			src, seen = f.Worker, w.Seen
		} else if w == nil && src == "" {
			src = f.Worker
		}
	}
	return src
}

// ListedFiles are the version files the family lists: FileSource's.
func (a *AutomationState) ListedFiles() []FileVersion {
	src := a.FileSource()
	var out []FileVersion
	for _, f := range a.Files {
		if f.Worker == src {
			out = append(out, f)
		}
	}
	return out
}

// FileVersion finds a listed version file by label.
func (a *AutomationState) FileVersion(label string) *FileVersion {
	src := a.FileSource()
	for i := range a.Files {
		if a.Files[i].Worker == src && a.Files[i].Label == label {
			return &a.Files[i]
		}
	}
	return nil
}

// SetFiles replaces a worker's version files, and reports whether they changed.
func (a *AutomationState) SetFiles(worker string, files []FileVersion) bool {
	var old, kept []FileVersion
	for _, f := range a.Files {
		if f.Worker == worker {
			old = append(old, f)
		} else {
			kept = append(kept, f)
		}
	}
	for i := range files {
		files[i].Worker = worker
	}
	if slices.Equal(old, files) {
		return false
	}
	a.Files = append(kept, files...)
	return true
}

// NextVersion is the number the next version gets: one past floor (the highest version the family has on disk)
// and every render that is a version.
func (a *AutomationState) NextVersion(floor int) int {
	n := floor
	for _, v := range a.Renders {
		if !v.Draft {
			n = max(n, v.N)
		}
	}
	return n + 1
}

// HasVersion reports whether any render is a version (not a draft).
func (a *AutomationState) HasVersion() bool {
	return slices.ContainsFunc(a.Renders, func(v LiveRender) bool { return !v.Draft })
}

// AddRender registers a new render and returns it with its label. The first render of a project that has no
// version at all (disk counts the versions on disk, floor is their highest number), outside a task, is the version
// v<N>, so an empty project gets its first video. Every other render is a draft, v<N>-draft<K>, of the next version.
func (a *AutomationState) AddRender(v LiveRender, disk, floor int) LiveRender {
	next := a.NextVersion(floor)
	if j := a.Task(v.Task); j != nil && LabelNumber(j.Target) > 0 {
		next = LabelNumber(j.Target) // a draft heads for its task's target
	}
	v.N, v.Alias = next, ""
	if v.Task == "" && disk == 0 && !a.HasVersion() {
		v.Draft, v.Label = false, fmt.Sprintf("v%d", next)
	} else {
		v.Draft, v.Label = true, fmt.Sprintf("v%d-draft%d", next, a.nextDraft(next))
	}
	a.Renders = append(a.Renders, v)
	return v
}

// nextDraft is K of the next draft label of version n, v<n>-draft<K>: one past every draft label of n there is, as a
// label or as the alias a promoted draft keeps.
func (a *AutomationState) nextDraft(n int) int {
	k := 0
	for _, d := range a.Renders {
		for _, label := range []string{d.Label, d.Alias} {
			var vn, dk int
			if _, err := fmt.Sscanf(label, "v%d-draft%d", &vn, &dk); err == nil && vn == n && label == fmt.Sprintf("v%d-draft%d", vn, dk) {
				k = max(k, dk)
			}
		}
	}
	return k + 1
}

// Listed reports whether the family lists render i: every version, and the newest draft and newest complete draft
// while they are ahead of the newest version (floor is the highest version number on disk). Other drafts stay
// playable by URL but aren't listed.
func (a *AutomationState) Listed(i, floor int) bool {
	v := a.Renders[i]
	if !v.Draft {
		return true
	}
	if v.N < a.NextVersion(floor) {
		// A version was made since, unless the draft's task is still updating that version.
		j := a.Task(v.Task)
		if j == nil || LabelNumber(j.Target) != v.N || j.State == "completed" || j.State == "cancelled" {
			return false
		}
	}
	for _, later := range a.Renders[i+1:] {
		if later.Draft && later.N == v.N && (!v.Complete || later.Complete) {
			return false
		}
	}
	return true
}

// promote turns a task's final draft into its target v<N> (else the next version), keeping its media and its draft
// label as an alias. When v<N> existed, its earlier render becomes one of its drafts (spec/automation.md, "Comments
// on vN become edits in vN+1"). Drafts left behind by it lose their manifests: they aren't listed any more.
func (a *AutomationState) promote(v *LiveRender, floor int, target string) {
	if !v.Draft {
		return
	}
	n := a.NextVersion(floor)
	if t := LabelNumber(target); t > 0 {
		n = t
	}
	label := fmt.Sprintf("v%d", n)
	for i := range a.Renders {
		if old := &a.Renders[i]; !old.Draft && old.Label == label && old.ID != v.ID {
			old.Draft, old.Label, old.Manifest = true, fmt.Sprintf("v%d-draft%d", n, a.nextDraft(n)), nil
		}
	}
	v.Alias, v.Label, v.N, v.Draft = v.Label, label, n, false
	for i := range a.Renders {
		if a.Renders[i].Draft && a.Renders[i].N <= n {
			a.Renders[i].Manifest = nil
		}
	}
}

// HasAgent checks the worker's local allowlist.
func (w *EditingWorker) HasAgent(id string) bool {
	return slices.ContainsFunc(w.Agents, func(p AgentProfile) bool { return p.ID == id })
}

// AddEvent retains useful task history without retaining unbounded provider output.
func (t *EditingTask) AddEvent(now int64, text string) {
	if len(text) > 4000 {
		text = text[:4000]
	}
	t.Events = append(t.Events, TaskEvent{At: now, Agent: t.Agent, Text: text})
	if len(t.Events) > 100 {
		t.Events = append([]TaskEvent(nil), t.Events[len(t.Events)-100:]...)
	}
}

// QueueFeedback submits existing open notes as one batch (spec/automation.md, "Batches"): they join the project's
// queued task, or make it when none waits. A note a queued, running or asking task still has to do stays with it. A
// paused batch is resumed instead (spec/automation.md, "Send resumes a paused batch"): the notes for its target, and
// its own, go in it and it is queued again, with agent when one is picked. It returns the tasks that own the notes now.
func (s *Store) QueueFeedback(ids []int64, agent string) ([]EditingTask, error) {
	var out []EditingTask
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		a.mergeQueued(now) // an older store's
		picked := agent
		if agent == "" {
			agent = a.queueAgent()
		}
		// The notes, by target: comments on vN become edits in vN+1.
		fresh := map[string][]int64{}
		versions := map[string]string{}
		var sent []int64
		var targets []string
		resume := map[string][]int64{} // a paused batch -> the notes this send added to it
		var resumeOrder []string
		resumeTask := func(id string) {
			if _, ok := resume[id]; !ok {
				resume[id] = nil
				resumeOrder = append(resumeOrder, id)
			}
		}
		for _, id := range ids {
			n, err := noteByID(t, id)
			if err != nil {
				return err
			}
			if n.Status != StatusOpen || slices.Contains(sent, id) {
				continue
			}
			sent = append(sent, id)
			if o := a.owner(id); o != nil {
				if o.State == "paused" {
					resumeTask(o.ID) // its own note, sent again: the batch goes on
				}
				continue
			}
			target := n.Target()
			if !slices.Contains(targets, target) {
				targets = append(targets, target)
				versions[target] = n.Version
			}
			fresh[target] = append(fresh[target], id)
		}
		for _, target := range targets {
			ids := fresh[target]
			if j := a.queuedFor(target); j != nil {
				for _, id := range ids {
					j.add(id)
				}
				j.AddEvent(now, "Added "+noteList(ids))
				continue
			}
			if j := a.pausedFor(target); j != nil {
				for _, id := range ids {
					j.add(id)
				}
				resumeTask(j.ID)
				resume[j.ID] = append(resume[j.ID], ids...)
				continue
			}
			a.Tasks = append(a.Tasks, EditingTask{ID: NewWorkID(), Notes: ids, Version: versions[target], Target: target,
				State: "queued", Agent: agent, Worker: a.Config.Worker, Events: []TaskEvent{}})
			a.Tasks[len(a.Tasks)-1].AddEvent(now, "Feedback submitted: "+noteList(ids)+targetWords(target))
		}
		for _, id := range resumeOrder {
			j := a.Task(id)
			j.State, j.Error, j.Lease, j.Until, j.Stop, j.NextAgent = "queued", "", "", 0, false, ""
			if picked != "" {
				j.Agent = picked // else it keeps its own (a claim gives one without an agent the configured one)
			}
			text := "Resumed by a send"
			if added := resume[id]; len(added) > 0 {
				text += ", with " + noteList(added)
			}
			j.AddEvent(now, text)
		}
		a.mergeQueued(now) // a resumed batch joins the one waiting for its target
		var owners []string
		for _, id := range sent {
			if o := a.owner(id); o != nil && !slices.Contains(owners, o.ID) {
				owners = append(owners, o.ID)
				out = append(out, *o)
			}
		}
		if err = saveAutomation(t, a); err == nil {
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "submitted"})
		}
		return err
	})
	return out, err
}

// SubmitReview records the final processing dependencies; it does not queue incremental draft notes. send asks
// for the notes to go to the agent once processed, whatever the automation config says; a take that was already
// considered without it is considered again.
func (s *Store) SubmitReview(version, take string, after []int64, send bool, agent ...string) error {
	pick := ""
	if len(agent) > 0 {
		pick = agent[0]
	}
	return s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		for i := range a.Submissions {
			sub := &a.Submissions[i]
			if sub.Take == take && sub.Version == version {
				if !sub.Done {
					sub.After = after
				}
				if send && !sub.Send {
					sub.Send, sub.Done, sub.Agent = true, false, pick
					return true, nil
				}
				return false, nil
			}
		}
		a.Submissions = append(a.Submissions, ReviewSubmission{Version: version, Take: take, After: after, Send: send,
			Agent: pick})
		return true, nil
	})
}

// TakeRef names one take of one version.
type TakeRef struct {
	Version string `json:"version"`
	Take    string `json:"take"`
}

// Sending lists the takes finished with send whose processing hasn't finished yet and hasn't failed: their notes
// are on their way to the agent (spec/automation.md, "HTTP and UI").
func (s *Store) Sending() ([]TakeRef, error) {
	a, err := s.Automation()
	if err != nil {
		return nil, err
	}
	out := []TakeRef{}
	for _, sub := range a.Submissions {
		if !sub.Send || sub.Done {
			continue
		}
		failed := false
		for _, id := range sub.After {
			if j, e := s.Job(id); e == nil && j.State == JobFailed {
				failed = true
				break
			}
		}
		if !failed {
			out = append(out, TakeRef{Version: sub.Version, Take: sub.Take})
		}
	}
	return out, nil
}

// ReadyFeedback considers each finished review once its final processing jobs succeeded: its open notes are queued
// when it was sent, or when automation is enabled; otherwise they stay for a manual Send. It is safe to call
// repeatedly. The job runner calls it after every job, and a worker's poll calls it too.
func (s *Store) ReadyFeedback() error {
	a, err := s.Automation()
	if err != nil {
		return err
	}
	for _, sub := range a.Submissions {
		if sub.Done {
			continue
		}
		ready := true
		for _, id := range sub.After {
			j, e := s.Job(id)
			if e != nil || j.State != JobDone {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		if sub.Send || a.Config.Enabled {
			ns, e := s.Notes(NoteFilter{})
			if e != nil {
				return e
			}
			var ids []int64
			for _, n := range ns {
				if n.Take == sub.Take && n.Version == sub.Version && n.Status == StatusOpen {
					ids = append(ids, n.ID)
				}
			}
			if _, e = s.QueueFeedback(ids, sub.Agent); e != nil {
				return e
			}
		}
		if e := s.UpdateAutomation(func(st *AutomationState) (bool, error) {
			for i := range st.Submissions {
				if st.Submissions[i].Take == sub.Take && st.Submissions[i].Version == sub.Version {
					st.Submissions[i].Done = true
				}
			}
			return false, nil
		}); e != nil {
			return e
		}
	}
	return nil
}

// WorkPoll is a worker's poll: its heartbeat, the attempt it runs (Task, Lease), the answers that reached that
// attempt's agent since the last poll (Told), and the notes of the next batch it gave the agent's live session (Join).
type WorkPoll struct {
	Worker EditingWorker `json:"worker"`
	Task   string        `json:"task"`
	Lease  string        `json:"lease"`
	Told   []int64       `json:"told,omitempty"`
	Join   []int64       `json:"join,omitempty"`
	// Chat and ChatLease are the chat turn the worker runs (spec/chat.md), which the poll renews.
	Chat      string `json:"chat,omitempty"`
	ChatLease string `json:"chat_lease,omitempty"`
	// Adopt asks for an attempt back after the worker restarted, in place of Task (spec/automation.md, "Agents outlive
	// Retake").
	Adopt *WorkAdopt `json:"adopt,omitempty"`
}

// WorkAdopt is an attempt a worker found in its records and wants back: its lease, the notes its agent has had, and
// whether its agent is alive or has written its report.
type WorkAdopt struct {
	Task    string  `json:"task"`
	Attempt int     `json:"attempt"`
	Lease   string  `json:"lease,omitempty"`
	Notes   []int64 `json:"notes,omitempty"`
	Alive   bool    `json:"alive"`
}

// owns reports whether p proves w runs an attempt or a turn here: its lease. Such a poll may come from a new session of
// the same worker (a restart).
func (a *AutomationState) owns(w string, p WorkPoll) bool {
	lease := p.Lease
	task := p.Task
	if p.Adopt != nil {
		task, lease = p.Adopt.Task, p.Adopt.Lease
	}
	if j := a.Task(task); j != nil && lease != "" && j.Worker == w && j.Lease == lease &&
		(j.State == "running" || j.State == "stopping") {
		return true
	}
	c := a.chatTurn()
	return p.Chat != "" && c != nil && c.ID == p.Chat && c.Worker == w && c.Lease != "" && c.Lease == p.ChatLease
}

// adopt gives w the attempt it asks back for (spec/automation.md, "Agents outlive Retake"): one still running with its
// lease, renewed, or one paused because w went away, running again with a new lease while nothing else runs. nil when
// it isn't w's to take.
func (a *AutomationState) adopt(w EditingWorker, ad WorkAdopt, now int64) *EditingTask {
	j := a.Task(ad.Task)
	if j == nil || j.Worker != w.ID || j.Attempt != ad.Attempt {
		return nil
	}
	until := now + int64(WorkerLease.Seconds())
	switch {
	case (j.State == "running" || j.State == "stopping") && j.Lease != "" && j.Lease == ad.Lease:
		if j.Until <= now {
			j.AddEvent(now, "Reconnected to the agent")
		}
		j.Until = until
		return j
	case j.lost() && ad.Alive:
		busy := slices.ContainsFunc(a.Tasks, func(o EditingTask) bool { return o.State == "running" || o.State == "stopping" })
		if c := a.chatTurn(); c != nil && c.State != ChatQueued {
			busy = true
		}
		// A note to do that the agent never had (a batch merged in while it was paused) needs a new attempt.
		if busy || slices.ContainsFunc(j.Pending(), func(id int64) bool { return !slices.Contains(ad.Notes, id) }) {
			return nil
		}
		j.State, j.Lease, j.Until, j.Error, j.Lost, j.Stop, j.NextAgent = "running", NewWorkID(), until, "", false, false, ""
		j.AddEvent(now, "Reconnected to the agent after a restart")
		return j
	}
	return nil
}

// PollWork registers a heartbeat and renews or claims an exclusive task attempt. told is the answers to the active
// task's questions that reached its agent since the last poll.
func (s *Store) PollWork(w EditingWorker, active, lease string, told ...int64) (*EditingTask, error) {
	t, _, err := s.PollWorker(WorkPoll{Worker: w, Task: active, Lease: lease, Told: told})
	return t, err
}

// PollWorker registers a heartbeat and renews or claims an exclusive task attempt. For a running attempt it also returns
// the queued batch its live session may take (next), and moves the notes the worker says it gave the session (Join)
// into the attempt's task (spec/automation.md, "Batches").
func (s *Store) PollWorker(p WorkPoll) (task, next *EditingTask, err error) {
	task, next, _, err = s.PollWorkerChat(p)
	return task, next, err
}

// PollWorkerChat is PollWorker that also renews or claims the video's chat turn (spec/chat.md): one agent per video,
// so a worker runs a task or a turn, never both, and a queued turn goes before queued tasks. chat is the turn the
// worker owns after the poll (nil when its turn ended or isn't its own any more).
func (s *Store) PollWorkerChat(p WorkPoll) (task, next *EditingTask, chat *ChatTurn, err error) {
	w := p.Worker
	var stale bool
	hold, err := s.processing()
	if err != nil {
		return nil, nil, nil, err
	}
	err = s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		now := s.Now().Unix()
		changed := false
		old := a.Worker(w.ID)
		if old != nil && old.Session != w.Session && old.Seen+int64(WorkerLease.Seconds()) > now && !a.owns(w.ID, p) {
			return false, errors.New("worker already connected")
		}
		w.Seen = now
		if old == nil {
			a.Workers = append(a.Workers, w)
			if a.Config.Worker == "" {
				a.Config.Worker = w.ID
				if len(w.Agents) > 0 {
					a.Config.Agent = w.Agents[0].ID
				}
			}
			changed = true
		} else {
			*old = w
		}
		if p.Chat != "" {
			// A running turn: renewed while it's still this worker's. Otherwise chat stays nil, and the worker stops it.
			if c := a.chatTurn(); c != nil && c.ID == p.Chat && c.Lease == p.ChatLease && c.Lease != "" && c.Worker == w.ID &&
				(c.State == ChatRunning || c.State == ChatStopping) {
				c.Until = now + int64(WorkerLease.Seconds())
				clone := *c
				chat = &clone
			}
			return changed, nil
		}
		if p.Adopt != nil {
			// Taking an attempt back: this poll claims nothing else.
			if j := a.adopt(w, *p.Adopt, now); j != nil {
				clone := *j
				task = &clone
				next = a.next(j.ID)
			}
			return true, nil
		}
		if p.Task != "" {
			j := a.Task(p.Task)
			if j == nil || j.Worker != w.ID || j.Lease != p.Lease || j.Lease == "" || j.State != "running" && j.State != "stopping" {
				stale = true
				return changed, nil
			}
			if j.Until <= now {
				j.AddEvent(now, "Reconnected to the agent")
			}
			j.Until = now + int64(WorkerLease.Seconds())
			changed = j.tell(p.Told) || changed
			if (j.State == "running" || j.State == "stopping") && a.join(p.Task, p.Join, now) {
				changed = true
			}
			j = a.Task(p.Task)
			clone := *j
			task = &clone
			next = a.next(p.Task)
			return changed, nil
		}
		// Sent feedback always runs on the selected worker; Enabled only governs finished reviews (ReadyFeedback).
		if a.Config.Worker != w.ID {
			return changed, nil
		}
		if slices.ContainsFunc(a.Tasks, func(j EditingTask) bool { return j.State == "running" || j.State == "stopping" }) {
			return changed, nil
		}
		if c := a.chatTurn(); c != nil {
			if c.State != ChatQueued {
				return changed, nil // the video's agent is replying in the chat
			}
			if c.claim(w, a.agentFor(&w), now) {
				clone := *c
				chat = &clone
				return true, nil
			}
		}
		if hold {
			return changed, nil // a sent take's notes join their tasks once processed, then they start
		}
		// The queued tasks in the order of their targets, lowest first (v8 before v9), then as they came.
		order := make([]int, 0, len(a.Tasks))
		for i := range a.Tasks {
			order = append(order, i)
		}
		slices.SortStableFunc(order, func(x, y int) int {
			return LabelNumber(a.Tasks[x].Target) - LabelNumber(a.Tasks[y].Target)
		})
		for _, i := range order {
			j := &a.Tasks[i]
			if j.State != "queued" {
				continue
			}
			if j.Agent == "" {
				j.Agent = a.agentFor(&w)
			}
			if !w.HasAgent(j.Agent) {
				continue
			}
			j.Worker = w.ID
			j.State = "running"
			j.Lease = NewWorkID()
			j.Until = now + int64(WorkerLease.Seconds())
			j.Attempt++
			j.Error = ""
			j.Lost = false
			j.Stop = false
			j.clearProgress()
			j.AddEvent(now, "Agent picked up the task")
			clone := *j
			task = &clone
			return true, nil
		}
		return changed, nil
	})
	if err == nil && stale {
		err = errors.New("attempt no longer owns this task")
	}
	if err != nil {
		return nil, nil, nil, err
	}
	return task, next, chat, nil
}

// MaxSendWait is how long a queued batch waits at most for a sent take's processing.
const MaxSendWait = time.Hour

// processing reports whether a take sent with send is still being processed (one of its jobs queued or running):
// its notes are on their way to the queued batch, which waits for them (spec/automation.md, "Batches").
func (s *Store) processing() (bool, error) {
	a, err := s.Automation()
	if err != nil {
		return false, err
	}
	for _, sub := range a.Submissions {
		if !sub.Send || sub.Done {
			continue
		}
		for _, id := range sub.After {
			j, e := s.Job(id)
			if e != nil && !errors.Is(e, ErrNotFound) {
				return false, e
			}
			if e != nil || j.State != JobQueued && j.State != JobRunning {
				continue
			}
			// A job stuck for an hour (its runner gone) never holds the video's agent back for good.
			if at, pe := ParseTime(j.Created); pe != nil || s.Now().Sub(at) < MaxSendWait {
				return true, nil
			}
		}
	}
	return false, nil
}

// WorkAttempt verifies fencing before accepting any worker mutation.
func (a *AutomationState) WorkAttempt(id, worker, lease string, now int64) (*EditingTask, error) {
	j := a.Task(id)
	// A lapsed lease still counts within the grace: its worker is back (spec/automation.md, "Agents outlive Retake").
	if j == nil || j.Worker != worker || j.Lease == "" || j.Lease != lease || lapsed(j.Until, now) || j.State != "running" && j.State != "stopping" {
		return nil, errors.New("stale task attempt")
	}
	return j, nil
}

// ControlTask requests a switch, pause, retry or answers a question.
func (s *Store) ControlTask(id, action, agent, message string) error {
	return s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		j := a.Task(id)
		if j == nil {
			return false, ErrNotFound
		}
		now := s.Now().Unix()
		if j.State == "completed" {
			return false, errors.New("task is completed; reopen the comment first")
		}
		named := agent != ""
		if named {
			w := a.Worker(a.Config.Worker)
			if w == nil || !w.HasAgent(agent) {
				return false, errors.New("agent is unavailable on the designated worker")
			}
		}
		switch action {
		case "switch", "retry":
			if agent == "" {
				agent = j.Agent
			}
			j.AddEvent(now, "Requested "+action+" with "+agent)
			if j.State == "running" || j.State == "stopping" {
				j.State = "stopping"
				j.Stop = true
				j.NextAgent = agent
			} else {
				j.State = "queued"
				j.Agent = agent
				j.Error = ""
				j.Lease = ""
				// It joins the video's queued batch, if one waits; an agent named here is the batch's now.
				target := j.Target
				if a.mergeQueued(now) && named {
					a.queuedFor(target).Agent = agent
				}
			}
		case "pause":
			j.NextAgent = ""
			if j.State == "running" || j.State == "stopping" {
				j.State = "stopping"
				j.Stop = true
			} else {
				j.State = "paused"
			}
			// The person's pause: its worker coming back doesn't take it back (spec/automation.md, "Agents outlive
			// Retake").
			j.Lost = false
			if j.Error == LostError {
				j.Error = "Paused by owner"
			}
			j.AddEvent(now, "Paused by owner")
		case "answer":
			if j.State != "question" || strings.TrimSpace(message) == "" {
				return false, errors.New("an unanswered question and a reply are required")
			}
			j.AddEvent(now, "Reviewer answered: "+message)
			j.Question = ""
			j.State = "queued"
			j.Error = ""
		default:
			return false, errors.New("unknown task action")
		}
		return true, nil
	})
}

// WorkReport is an attempt's checkpoint. Completed results must name a fully rendered revision (Result), or in a
// project without a timeline the version file the attempt added (Version). VersionChecked is set by the server once
// it found Version in its family; it never comes from the worker.
type WorkReport struct {
	Worker         string      `json:"worker"`
	Task           string      `json:"task"`
	Lease          string      `json:"lease"`
	State          string      `json:"state"`
	Message        string      `json:"message"`
	Result         string      `json:"result,omitempty"`
	Version        string      `json:"version,omitempty"`
	VersionChecked bool        `json:"-"`
	Usage          *AgentUsage `json:"usage,omitempty"`
	// Told is the answers that reached the agent, as in a poll: the report counts them before it decides. Join is the
	// notes of the next batch given to the agent's live session, as in a poll, counted before it decides too.
	Told []int64 `json:"told,omitempty"`
	Join []int64 `json:"join,omitempty"`
	// Notes is what changed for each note, from the agent's report; a note without an entry gets Message. Said is
	// the notes the agent wrote its own entry for: any other note marked done with retake done gets its mark's words
	// (spec/automation.md, "Marking progress").
	Notes map[int64]string `json:"notes,omitempty"`
	Said  []int64          `json:"said,omitempty"`
}

// MaxNoteChanges is how many per-note changes a report may carry; MaxChange is the longest one.
const (
	MaxNoteChanges = 1000
	MaxChange      = 4000
)

// change is what changed for note id: its own entry, else the message.
func (r WorkReport) change(id int64) string {
	if c := strings.TrimSpace(r.Notes[id]); c != "" {
		if len(c) > MaxChange {
			c = c[:MaxChange]
		}
		return c
	}
	return r.Message
}

// WorkNote includes the original annotations alongside the note text.
type WorkNote struct {
	Note
	Detail Detail      `json:"detail"`
	Images []TakeImage `json:"images,omitempty"`
	// Missing says, in plain words, which of the note's files the worker couldn't give its agent and what it gave
	// instead (spec/automation.md, "A note's stills and images"). Only the task context the worker writes has it.
	Missing []string `json:"missing,omitempty"`
}

// WorkAssignment is an execution lease and its original feedback, including other open notes for context. Versions
// counts the family's versions (drafts not counted): a worker renders outside a task only while it is 0.
type WorkAssignment struct {
	Config   AutomationConfig `json:"config"`
	Task     *EditingTask     `json:"task"`
	Notes    []WorkNote       `json:"notes,omitempty"`
	Versions int              `json:"versions"`
	// Questions is the task's questions from inside it (retake ask), with their answers.
	Questions []TaskQuestion `json:"questions,omitempty"`
	// Next is the queued task of the same target a running attempt may take in (spec/automation.md, "Comments on vN
	// become edits in vN+1").
	Next *EditingTask `json:"next,omitempty"`
	// From is the render whose source a timeline task starts from, when its target is older than the newest version
	// (a comment on v7 while v9 exists updates v8 from v8's own source).
	From string `json:"from,omitempty"`
	// Chat is the chat turn the worker owns (spec/chat.md): a new one to run, or the running one, with Stop.
	Chat *ChatWork `json:"chat,omitempty"`
}

// ReportWork atomically updates task and note completion, preserving reviewer approval. A completed task's final
// draft becomes the next version (floor is the highest version number on disk), which it returns; other reports
// return a zero LiveRender.
func (s *Store) ReportWork(r WorkReport, floor int) (LiveRender, error) {
	var result LiveRender
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		j, err := a.WorkAttempt(r.Task, r.Worker, r.Lease, now)
		if err != nil {
			return err
		}
		if len(r.Notes) > MaxNoteChanges {
			return fmt.Errorf("a report says what changed for %d notes at most", MaxNoteChanges)
		}
		if a.join(j.ID, r.Join, now) {
			j = a.Task(r.Task)
		}
		if r.Usage != nil {
			j.Usage = r.Usage
		}
		if len(r.Message) > 4000 {
			r.Message = r.Message[:4000]
		}
		j.tell(r.Told)
		switch r.State {
		case "progress":
			j.AddEvent(now, r.Message)
		case "stopped":
			j.Lease = ""
			j.Until = 0
			j.Stop = false
			j.State = "paused"
			if j.NextAgent != "" {
				j.Agent = j.NextAgent
				j.NextAgent = ""
				j.State = "queued"
			}
			j.AddEvent(now, "Previous agent stopped; working files preserved")
		case "paused", "question":
			j.State = r.State
			j.Lease = ""
			j.Until = 0
			j.Stop = false
			j.Error = r.Message
			if r.State == "question" {
				j.Question = r.Message
				j.Error = ""
			}
			j.AddEvent(now, r.Message)
			if r.State == "paused" && strings.HasPrefix(r.Message, "Usage limit") && a.Config.Fallback != "" && j.Agent != a.Config.Fallback {
				j.Agent = a.Config.Fallback
				j.State = "queued"
				j.AddEvent(now, "Using the configured fallback agent")
			}
		case "completed":
			if j.Stop {
				return errors.New("task is stopping")
			}
			// Notes held by the task's own questions aren't finished by this report (spec/automation.md, "How a task
			// ends"); the rest are, with a render or a new version.
			hold, waiting, herr := held(t, j)
			if herr != nil {
				return herr
			}
			var free []int64
			for _, id := range j.Pending() {
				if !slices.Contains(hold, id) {
					free = append(free, id)
				}
			}
			if r.Result == "" && r.Version == "" {
				// Typed comments on an extension's version are done by the report alone; a note made from a take, or
				// one that wants a version, needs a render or a new version (spec/extensions.md).
				docs, cuts, derr := splitDocs(t, free)
				if derr != nil {
					return derr
				}
				if len(cuts) == 0 && strings.TrimSpace(r.Message) != "" {
					for _, id := range docs {
						n, e := noteByID(t, id)
						if e != nil {
							return e
						}
						if n.Status != StatusOpen && n.Status != StatusQuestion {
							j.Dropped = append(j.Dropped, id) // withdrawn meanwhile: the batch carries on
							continue
						}
						if _, e = t.Exec(`UPDATE notes SET status='answered', answered_in=?, change=?, updated=? WHERE id=?`, n.Version, r.changeFor(j, id), s.stamp(), id); e != nil {
							return e
						}
					}
					if len(hold) > 0 {
						j.Done = append(j.Done, docs...)
						j.wait(waiting, now, r.Message)
						break
					}
					if len(docs) > 0 {
						j.State = "completed"
						j.Lease = ""
						j.Until = 0
						j.Error = ""
						j.AddEvent(now, r.Message)
						break
					}
				}
			}
			if r.Result == "" && r.Version == "" && len(j.Asked) > 0 {
				if len(free) > 0 {
					// The agent was told an answer just before it reported: it goes again rather than failing.
					j.wait(false, now, r.Message)
				} else {
					j.wait(waiting, now, r.Message)
				}
				break
			}
			if len(free) == 0 && len(hold) > 0 {
				j.wait(waiting, now, r.Message) // its render stays a draft: nothing it holds is done
				break
			}
			var label string
			if r.Result == "" && r.Version != "" {
				// A plain project's new version file: the server has seen it in its family.
				if !r.VersionChecked || strings.TrimSpace(r.Message) == "" {
					return errors.New("completion requires a new version that plays and a change summary")
				}
				if j.Target != "" && r.Version != j.Target {
					return fmt.Errorf("this task's comments go in %s, not %s", j.Target, r.Version)
				}
				label = r.Version
			} else {
				v := a.Render(r.Result)
				if v == nil || !v.Complete || v.Error != "" || v.Worker != r.Worker || v.Task != j.ID || strings.TrimSpace(r.Message) == "" {
					return errors.New("completion requires a checked playable render and a change summary")
				}
				a.promote(v, floor, j.Target)
				result = *v
				label = v.Label
			}
			var finished []int64
			for _, id := range free {
				n, e := noteByID(t, id)
				if e != nil {
					return e
				}
				if n.Status != StatusOpen && n.Status != StatusQuestion {
					j.Dropped = append(j.Dropped, id) // withdrawn meanwhile: the batch carries on
					continue
				}
				if _, e = t.Exec(`UPDATE notes SET status='answered', answered_in=?, change=?, updated=? WHERE id=?`, label, r.changeFor(j, id), s.stamp(), id); e != nil {
					return e
				}
				finished = append(finished, id)
			}
			free = finished
			j.Result = label
			if len(hold) > 0 {
				// Some notes are Done; the others still wait on their questions, in this same task.
				j.Done = append(j.Done, free...)
				j.wait(waiting, now, r.Message)
				break
			}
			j.State = "completed"
			j.Lease = ""
			j.Until = 0
			j.Error = ""
			j.AddEvent(now, r.Message)
		default:
			return fmt.Errorf("unknown report state %q", r.State)
		}
		if j.State != "running" && j.State != "stopping" {
			j.clearProgress() // the report has settled every note (spec/automation.md, "Marking progress")
		}
		notes := slices.Clone(j.Notes)
		if err = saveAutomation(t, a); err == nil { // a task queued again joins the video's queued batch
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "automation", Notes: notes})
		}
		return err
	})
	if err != nil {
		return LiveRender{}, err
	}
	return result, nil
}

// splitDocs sorts notes into typed comments that a report alone finishes (docs) and the rest, which need a render or
// a new version (cuts).
func splitDocs(q querier, ids []int64) (docs, cuts []int64, err error) {
	for _, id := range ids {
		n, e := noteByID(q, id)
		if e != nil {
			return nil, nil, e
		}
		if n.WantsVersion() {
			cuts = append(cuts, id)
		} else {
			docs = append(docs, id)
		}
	}
	return docs, cuts, nil
}

// DocOnly reports whether every note of a task's that is still to do is a typed comment a report alone finishes: the
// worker then waits for no render and pushes no version.
func (s *Store) DocOnly(j EditingTask) (bool, error) {
	_, cuts, err := splitDocs(s.db, j.Pending())
	return len(cuts) == 0 && len(j.Pending()) > 0, err
}

// targetWords says, in an event, which version the task's comments go in.
func targetWords(target string) string {
	if target == "" {
		return ""
	}
	return " (edits in " + target + ")"
}
