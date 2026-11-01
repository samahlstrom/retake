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

// WorkerLease is deliberately longer than the worker's disconnect cancellation deadline.
const WorkerLease = 30 * time.Second

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
	ID        string      `json:"id"`
	Notes     []int64     `json:"notes"`
	Version   string      `json:"version"`
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
}

// AgentUsage reports provider telemetry, never an invented remaining subscription balance.
type AgentUsage struct {
	Input  int64    `json:"input_tokens,omitempty"`
	Output int64    `json:"output_tokens,omitempty"`
	Cost   *float64 `json:"estimated_cost_usd,omitempty"`
}

// LiveRender describes immutable media retained on a worker and served through the relay.
type LiveRender struct {
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
}

// ReviewSubmission waits for final transcription before scheduling its notes.
type ReviewSubmission struct {
	Take    string  `json:"take"`
	Version string  `json:"version"`
	After   []int64 `json:"after"`
	Done    bool    `json:"done"`
}

// AutomationState is stored transactionally with the project's event log.
type AutomationState struct {
	Config      AutomationConfig   `json:"config"`
	Workers     []EditingWorker    `json:"workers"`
	Tasks       []EditingTask      `json:"tasks"`
	Renders     []LiveRender       `json:"renders"`
	Submissions []ReviewSubmission `json:"submissions,omitempty"`
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
		changed = changed || reconciled
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
	changed := false
	now := s.Now().Unix()
	for i := range a.Tasks {
		j := &a.Tasks[i]
		if j.State == "completed" || j.State == "cancelled" {
			continue
		}
		if (j.State == "running" || j.State == "stopping") && j.Until <= now {
			j.State = "paused"
			j.Error = "Worker disconnected; retry or switch agent"
			j.Lease = ""
			j.Stop = false
			j.AddEvent(now, j.Error)
			changed = true
		}
		for _, id := range j.Notes {
			n, e := noteByID(t, id)
			if e != nil {
				return changed, e
			}
			if n.Status == StatusOpen {
				continue
			}
			if j.State == "running" || j.State == "stopping" {
				if !j.Stop {
					j.State = "stopping"
					j.Stop = true
					j.NextAgent = ""
					changed = true
				}
			} else {
				j.State = "cancelled"
				j.Error = "Feedback was resolved or retracted outside this task"
				j.AddEvent(now, j.Error)
				changed = true
			}
			break
		}
	}
	return changed, nil
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

// Render finds a retained render by ID or public version label.
func (a *AutomationState) Render(id string) *LiveRender {
	for i := range a.Renders {
		if a.Renders[i].ID == id || a.Renders[i].Label == id {
			return &a.Renders[i]
		}
	}
	return nil
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

// QueueFeedback submits existing open notes. One unfinished task owns a note at a time.
func (s *Store) QueueFeedback(ids []int64, agent string) ([]EditingTask, error) {
	var out []EditingTask
	err := s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		if agent == "" {
			agent = a.Config.Agent
		}
		for _, id := range ids {
			n, err := noteByID(t, id)
			if err != nil {
				return err
			}
			if n.Status != StatusOpen {
				continue
			}
			existing := slices.IndexFunc(a.Tasks, func(j EditingTask) bool {
				return j.State != "completed" && j.State != "cancelled" && slices.Contains(j.Notes, id)
			})
			if existing >= 0 {
				out = append(out, a.Tasks[existing])
				continue
			}
			j := EditingTask{ID: NewWorkID(), Notes: []int64{id}, Version: n.Version, State: "queued", Agent: agent, Worker: a.Config.Worker, Events: []TaskEvent{}}
			j.AddEvent(s.Now().Unix(), "Feedback submitted")
			a.Tasks = append(a.Tasks, j)
			out = append(out, j)
		}
		b, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = t.Exec(`INSERT INTO automation (id,data) VALUES (1,?) ON CONFLICT (id) DO UPDATE SET data = excluded.data`, string(b))
		if err == nil {
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "submitted"})
		}
		return err
	})
	return out, err
}

// SubmitReview records the final processing dependencies; it does not queue incremental draft notes.
func (s *Store) SubmitReview(version, take string, after []int64) error {
	return s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		for i := range a.Submissions {
			if a.Submissions[i].Take == take && a.Submissions[i].Version == version {
				if !a.Submissions[i].Done {
					a.Submissions[i].After = after
				}
				return false, nil
			}
		}
		a.Submissions = append(a.Submissions, ReviewSubmission{Version: version, Take: take, After: after})
		return true, nil
	})
}

// ReadyFeedback submits notes only after the final processing jobs succeeded. It is safe to call repeatedly.
func (s *Store) ReadyFeedback() error {
	a, err := s.Automation()
	if err != nil || !a.Config.Enabled {
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
		if _, e = s.QueueFeedback(ids, ""); e != nil {
			return e
		}
		if e = s.UpdateAutomation(func(st *AutomationState) (bool, error) {
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

// PollWork registers a heartbeat and renews or claims an exclusive task attempt.
func (s *Store) PollWork(w EditingWorker, active, lease string) (*EditingTask, error) {
	var result *EditingTask
	var stale bool
	err := s.UpdateAutomation(func(a *AutomationState) (bool, error) {
		now := s.Now().Unix()
		changed := false
		old := a.Worker(w.ID)
		if old != nil && old.Session != w.Session && old.Seen+int64(WorkerLease.Seconds()) > now {
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
		if active != "" {
			j := a.Task(active)
			if j == nil || j.Worker != w.ID || j.Lease != lease || j.Lease == "" {
				stale = true
				return changed, nil
			}
			j.Until = now + int64(WorkerLease.Seconds())
			clone := *j
			result = &clone
			return changed, nil
		}
		if !a.Config.Enabled || a.Config.Worker != w.ID {
			return changed, nil
		}
		if slices.ContainsFunc(a.Tasks, func(j EditingTask) bool { return j.State == "running" || j.State == "stopping" }) {
			return changed, nil
		}
		for i := range a.Tasks {
			j := &a.Tasks[i]
			if j.State != "queued" {
				continue
			}
			if j.Agent == "" {
				j.Agent = a.Config.Agent
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
			j.Stop = false
			j.AddEvent(now, "Agent picked up the task")
			clone := *j
			result = &clone
			return true, nil
		}
		return changed, nil
	})
	if err == nil && stale {
		err = errors.New("attempt no longer owns this task")
	}
	return result, err
}

// WorkAttempt verifies fencing before accepting any worker mutation.
func (a *AutomationState) WorkAttempt(id, worker, lease string, now int64) (*EditingTask, error) {
	j := a.Task(id)
	if j == nil || j.Worker != worker || j.Lease == "" || j.Lease != lease || j.Until <= now || j.State != "running" && j.State != "stopping" {
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
		if agent != "" {
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
			if j.State == "running" || j.State == "stopping" {
				j.State = "stopping"
				j.Stop = true
				j.NextAgent = agent
			} else {
				j.State = "queued"
				j.Agent = agent
				j.Error = ""
				j.Lease = ""
			}
			j.AddEvent(now, "Requested "+action+" with "+agent)
		case "pause":
			j.NextAgent = ""
			if j.State == "running" || j.State == "stopping" {
				j.State = "stopping"
				j.Stop = true
			} else {
				j.State = "paused"
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

// WorkReport is an attempt's checkpoint. Completed results must name a fully rendered revision.
type WorkReport struct {
	Worker  string      `json:"worker"`
	Task    string      `json:"task"`
	Lease   string      `json:"lease"`
	State   string      `json:"state"`
	Message string      `json:"message"`
	Result  string      `json:"result,omitempty"`
	Usage   *AgentUsage `json:"usage,omitempty"`
}

// WorkNote includes the original annotations alongside the note text.
type WorkNote struct {
	Note
	Detail Detail      `json:"detail"`
	Images []TakeImage `json:"images,omitempty"`
}

// WorkAssignment is an execution lease and its original feedback, including other open notes for context.
type WorkAssignment struct {
	Config AutomationConfig `json:"config"`
	Task   *EditingTask     `json:"task"`
	Notes  []WorkNote       `json:"notes,omitempty"`
}

// ReportWork atomically updates task and note completion, preserving reviewer approval.
func (s *Store) ReportWork(r WorkReport) error {
	return s.write(func(t *tx) error {
		a, err := readAutomation(t)
		if err != nil {
			return err
		}
		now := s.Now().Unix()
		j, err := a.WorkAttempt(r.Task, r.Worker, r.Lease, now)
		if err != nil {
			return err
		}
		if r.Usage != nil {
			j.Usage = r.Usage
		}
		if len(r.Message) > 4000 {
			r.Message = r.Message[:4000]
		}
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
			v := a.Render(r.Result)
			if v == nil || !v.Complete || v.Error != "" || v.Worker != r.Worker || v.Task != j.ID || strings.TrimSpace(r.Message) == "" {
				return errors.New("completion requires a checked playable render and a change summary")
			}
			for _, id := range j.Notes {
				n, e := noteByID(t, id)
				if e != nil {
					return e
				}
				if n.Status == StatusRetracted {
					return errors.New("feedback was retracted")
				}
				if _, e = t.Exec(`UPDATE notes SET status='answered', answered_in=?, change=?, updated=? WHERE id=?`, v.Label, r.Message, s.stamp(), id); e != nil {
					return e
				}
			}
			j.State = "completed"
			j.Result = v.Label
			j.Lease = ""
			j.Until = 0
			j.Error = ""
			j.AddEvent(now, r.Message)
		default:
			return fmt.Errorf("unknown report state %q", r.State)
		}
		b, err := json.Marshal(a)
		if err != nil {
			return err
		}
		_, err = t.Exec(`UPDATE automation SET data=? WHERE id=1`, string(b))
		if err == nil {
			t.emit("automation", map[string]bool{"changed": true})
			t.emit(EventNotes, NotesEvent{Why: "automation", Notes: j.Notes})
		}
		return err
	})
}
