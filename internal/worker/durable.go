package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/project"
)

// Agents outlive Retake (spec/automation.md): an agent's work is files. A background agent and a chat turn run
// detached, their output in files; each attempt keeps a record of what a later worker needs to take it back; and a
// worker that starts takes its attempt and its turn back instead of letting them pause.

// errStop is the cause a person's stop cancels an attempt or a turn with: pause, switch agent, Stop in the chat, a
// task whose comments were withdrawn. Any other end of their context is the worker going away, which leaves the agent
// running.
var errStop = errors.New("stopped by a person")

// stopped reports whether ctx ended because a person stopped the work (not because the worker is going away).
func stopped(ctx context.Context) bool { return errors.Is(context.Cause(ctx), errStop) }

// detached is the outcome of an attempt whose worker went away while its agent works on: nothing is reported.
const detached = "detached"

// The phases of an attempt in its record.
const (
	phaseAgent    = "agent"    // waiting for the round's report
	phaseReported = "reported" // the round's report is counted
	phaseRender   = "render"   // waiting for the final render (a timeline project)
)

const recordFile = "run.json"

// runRecord is an attempt as a later worker needs it to take it back, kept in tasks/<id>/run.json.
type runRecord struct {
	Task    string `json:"task"`
	Attempt int    `json:"attempt"`
	Lease   string `json:"lease"`
	Agent   string `json:"agent"`
	Mode    string `json:"mode"`
	Pane    string `json:"pane,omitempty"` // herdr, tmux: the pane it runs in
	Phase   string `json:"phase"`
	K       int    `json:"k"` // rounds so far, the attempt's own prompt counting as 1
	// The round's files in the task's folder: the prompt (a background agent's stdin), context, report and log.
	Prompt  string `json:"prompt"`
	Context string `json:"context"`
	Report  string `json:"report"`
	Log     string `json:"log,omitempty"`
	// Notes is every note the agent has had; Given the ones given in rounds (the server hears of them in a poll's
	// join); Round the notes of the round in progress.
	Notes    []int64             `json:"notes"`
	Given    []int64             `json:"given,omitempty"`
	Round    []int64             `json:"round,omitempty"`
	Changes  map[int64]string    `json:"changes,omitempty"`
	Messages []string            `json:"messages,omitempty"`
	Usage    *project.AgentUsage `json:"usage,omitempty"`
	Secret   string              `json:"secret,omitempty"` // its retake ask secret
	AskURL   string              `json:"ask_url,omitempty"`
	Proc     *agentProc          `json:"proc,omitempty"` // background: the round's process
	Doc      bool                `json:"doc,omitempty"`
	Plain    *plainRun           `json:"plain,omitempty"`
	Restore  string              `json:"restore,omitempty"` // the timeline to put back at the end (an older target's)
	Snaps    []ext.SnapState     `json:"snaps,omitempty"`
	Outcome  *savedOutcome       `json:"outcome,omitempty"` // its end, until the server has it
	Done     bool                `json:"done,omitempty"`    // the server has its end
	legacy   bool                // made from an older worker's files
}

// plainRun is a plain task's video: where it goes and where the agent started.
type plainRun struct {
	Output   string   `json:"output"`
	From     string   `json:"from,omitempty"`
	Into     string   `json:"into"`
	Updating bool     `json:"updating,omitempty"`
	Before   []string `json:"before,omitempty"`
}

// savedOutcome is an attempt's end, kept until it reached the server.
type savedOutcome struct {
	State   string              `json:"state"`
	Message string              `json:"message,omitempty"`
	Result  string              `json:"result,omitempty"`
	Version string              `json:"version,omitempty"`
	Changes map[int64]string    `json:"changes,omitempty"`
	Usage   *project.AgentUsage `json:"usage,omitempty"`
}

func (o outcome) saved() *savedOutcome {
	return &savedOutcome{State: o.state, Message: o.message, Result: o.result, Version: o.version, Changes: o.changes, Usage: o.usage}
}

func (s *savedOutcome) back() outcome {
	return outcome{state: s.State, message: s.Message, result: s.Result, version: s.Version, changes: s.Changes, usage: s.Usage}
}

func (r *runner) recordPath(task string) string {
	return filepath.Join(r.dir, "tasks", task, recordFile)
}

// saveRecord writes rec, under the record lock: the attempt and the control loop (a new lease) both change it.
func (r *runner) saveRecord(rec *runRecord) {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	if err := writeJSON(r.recordPath(rec.Task), rec); err != nil {
		r.log("%s: can't save the attempt's record: %v", filepath.Base(r.opts.Root), err)
	}
}

// update changes rec under the record lock and saves it.
func (r *runner) update(rec *runRecord, fn func(*runRecord)) {
	r.recMu.Lock()
	fn(rec)
	r.recMu.Unlock()
	r.saveRecord(rec)
}

func readRecord(path string) *runRecord {
	b, err := os.ReadFile(path) //nolint:gosec // a record in the worker's own folder
	var rec runRecord
	if err != nil || len(b) > 64<<20 || json.Unmarshal(b, &rec) != nil || !project.ValidWorkID(rec.Task) ||
		filepath.Base(filepath.Dir(path)) != rec.Task {
		return nil
	}
	return &rec
}

// finished marks task's record done: the server has its end.
func (r *runner) finished(task string) {
	rec := readRecord(r.recordPath(task))
	if rec == nil {
		return
	}
	rec.Done, rec.Proc = true, nil
	r.saveRecord(rec)
}

// --- detached processes

// agentProc is a detached agent process: one this worker started (it waits for it), or an earlier worker's (watched
// by its pid and start time).
type agentProc struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
	own   bool
	done  chan struct{}
	err   error // how an own process exited, once done
}

// startDetached runs argv in dir in a session of its own (spec/automation.md, "Agents outlive Retake"): stdin is the
// file in, stdout and stderr go to the files out and errs (the same file when they're equal), never pipes.
func startDetached(argv []string, dir string, env []string, in, out, errs string) (*agentProc, error) {
	stdin, err := os.Open(in) //nolint:gosec // the prompt this worker wrote
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdin.Close() }()
	stdout, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o600) //nolint:gosec // the task's own log
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdout.Close() }()
	stderr := stdout
	if errs != out {
		if stderr, err = os.OpenFile(errs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0o600); err != nil { //nolint:gosec // the turn's own log
			return nil, err
		}
		defer func() { _ = stderr.Close() }()
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // trusted local argv, never supplied by the host
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = dir, env, stdin, stdout, stderr
	detach(cmd)
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	p := &agentProc{PID: cmd.Process.Pid, Start: procStart(cmd.Process.Pid), own: true, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// alive reports whether p is still the process it was.
func (p *agentProc) alive() bool { return p != nil && p.Start != "" && procStart(p.PID) == p.Start }

// watch is an earlier worker's process: its done closes once it has gone (or life ends).
func (r *runner) watch(p *agentProc) *agentProc {
	w := &agentProc{PID: p.PID, Start: p.Start, done: make(chan struct{})}
	life := r.life
	if life == nil {
		life = context.Background()
	}
	go func() {
		for w.alive() {
			select {
			case <-life.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		close(w.done)
	}()
	return w
}

// stop ends p and its process group (a person's stop), and waits for it.
func (p *agentProc) stop() {
	if p == nil {
		return
	}
	if p.own && p.Start == "" {
		killGroup(p.PID) // it started and ended before its start time could be read
	}
	endGroup(p.PID, p.Start)
	if p.own {
		<-p.done
	}
}

// tailOf is the last n bytes of the file at path.
func tailOf(path string, n int64) []byte {
	f, err := os.Open(path) //nolint:gosec // a log in the worker's own folder
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(st.Size()-n, io.SeekStart)
	}
	b, _ := io.ReadAll(io.LimitReader(f, n))
	return b
}

// stopOrphans stops the background agents of earlier records still running, but task's and turn's: one agent per
// video. A new attempt or turn replacing them is a person's doing (a retry, a send, a message).
func (r *runner) stopOrphans(task, turn string) {
	paths, _ := filepath.Glob(filepath.Join(r.dir, "tasks", "*", recordFile))
	for _, path := range paths {
		rec := readRecord(path)
		if rec == nil || rec.Task == task || rec.Done || !rec.Proc.alive() {
			continue
		}
		r.log("%s: stopping the agent of task %s (attempt %d), which a new one replaces", filepath.Base(r.opts.Root), rec.Task, rec.Attempt)
		rec.Proc.stop()
		rec.Proc = nil
		rec.Done = true
		r.saveRecord(rec)
	}
	if tr := r.readTurn(); tr != nil && tr.Turn != turn && tr.Proc.alive() {
		r.log("%s: stopping the agent of an earlier chat turn", filepath.Base(r.opts.Root))
		tr.Proc.stop()
		_ = os.Remove(r.turnPath())
	}
}

// --- taking an attempt back

// adoptable is the attempt this worker may take back (spec/automation.md, "Agents outlive Retake"): the newest record
// not done, else, in a folder from before records, one made from the newest task folder's files when its agent is
// alive or its report is there. nil when there's none.
func (r *runner) adoptable(ctx context.Context) *runRecord {
	dirs, _ := os.ReadDir(filepath.Join(r.dir, "tasks"))
	var newest *runRecord
	var at time.Time
	var legacyDir string
	var legacyAt time.Time
	for _, d := range dirs {
		if !d.IsDir() || !project.ValidWorkID(d.Name()) {
			continue
		}
		dir := filepath.Join(r.dir, "tasks", d.Name())
		if st, err := os.Stat(filepath.Join(dir, recordFile)); err == nil {
			if rec := readRecord(filepath.Join(dir, recordFile)); rec != nil && !rec.Done && st.ModTime().After(at) {
				newest, at = rec, st.ModTime()
			}
			continue
		}
		if _, t := newestPrompt(dir); t.After(legacyAt) {
			legacyDir, legacyAt = dir, t
		}
	}
	if newest != nil {
		return newest
	}
	if legacyDir == "" {
		return nil
	}
	rec := r.legacyRecord(legacyDir)
	if rec == nil || !reported(rec.Report) && !r.recordAlive(ctx, rec) {
		return nil
	}
	return rec
}

var promptName = regexp.MustCompile(`^prompt-(\d+)(?:-(\d+))?\.md$`)

// newestPrompt is the newest prompt file in a task folder and its time.
func newestPrompt(dir string) (string, time.Time) {
	entries, _ := os.ReadDir(dir)
	name, at := "", time.Time{}
	for _, e := range entries {
		if !promptName.MatchString(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(at) {
			name, at = e.Name(), info.ModTime()
		}
	}
	return name, at
}

// legacyRecord makes a record of the attempt an older worker left in dir: its newest prompt, the context beside it
// (which has the task, attempt and lease) and view.json.
func (r *runner) legacyRecord(dir string) *runRecord {
	name, _ := newestPrompt(dir)
	m := promptName.FindStringSubmatch(name)
	if m == nil {
		return nil
	}
	n, _ := strconv.Atoi(m[1])
	suffix, k := m[1], 1
	if m[2] != "" {
		suffix += "-" + m[2]
		k, _ = strconv.Atoi(m[2])
	}
	contextPath := filepath.Join(dir, "context-"+suffix+".json")
	b, err := os.ReadFile(contextPath) //nolint:gosec // the task's own context
	var a project.WorkAssignment
	if err != nil || json.Unmarshal(b, &a) != nil || a.Task == nil || a.Task.ID != filepath.Base(dir) || a.Task.Attempt != n {
		return nil
	}
	t := a.Task
	rec := &runRecord{Task: t.ID, Attempt: n, Lease: t.Lease, Agent: t.Agent, Mode: OpenBackground, Phase: phaseAgent, K: k,
		Prompt: filepath.Join(dir, name), Context: contextPath, Report: filepath.Join(dir, "report-"+suffix+".json"),
		Log: filepath.Join(dir, "agent-"+suffix+".log"), Notes: slices.Clone(t.Notes), Doc: docOnly(a), legacy: true}
	var view viewState
	if b, err := os.ReadFile(filepath.Join(r.dir, "view.json")); err == nil && json.Unmarshal(b, &view) == nil &&
		(view.Mode == OpenHerdr || view.Mode == OpenTmux) && view.Profile == t.Agent && view.Pane != "" {
		rec.Mode, rec.Pane, rec.Log = view.Mode, view.Pane, ""
	}
	if r.timeline == "" && !rec.Doc {
		var c plainContext
		_ = json.Unmarshal(b, &c)
		rec.Plain = &plainRun{Output: filepath.Join(dir, "output.mp4")}
		for _, v := range c.Family {
			rec.Plain.Before = append(rec.Plain.Before, v.Label)
		}
	}
	return rec
}

// recordAlive reports whether rec's agent is still there: its pid with the same start, or its herdr agent or tmux pane.
func (r *runner) recordAlive(ctx context.Context, rec *runRecord) bool {
	if rec.Mode == OpenBackground {
		return rec.Proc.alive()
	}
	v := r.viewOf(rec)
	return v != nil && v.alive(ctx)
}

// viewOf is the session rec's agent runs in, nil when the video's session is another one now.
func (r *runner) viewOf(rec *runRecord) *view {
	i := slices.IndexFunc(r.opts.Profiles, func(p Profile) bool { return p.ID == rec.Agent })
	if i < 0 {
		return nil
	}
	v, err := r.newView(rec.Mode, r.opts.Profiles[i])
	if err != nil || v.state.Pane == "" || v.state.Pane != rec.Pane {
		return nil
	}
	return v
}

// adoption is the poll's ask for rec's attempt back.
func (r *runner) adoption(ctx context.Context, rec *runRecord) *project.WorkAdopt {
	alive := reported(rec.Report) || r.recordAlive(ctx, rec)
	return &project.WorkAdopt{Task: rec.Task, Attempt: rec.Attempt, Lease: rec.Lease, Notes: rec.Notes, Alive: alive}
}

// refused drops a record the server didn't give back: its attempt isn't running any more (someone cancelled or retried
// it), so its background agent is stopped. A herdr or tmux session is left open.
func (r *runner) refused(rec *runRecord) {
	if rec.legacy {
		return // nothing of ours to stop or mark
	}
	if rec.Proc.alive() {
		r.log("%s: task %s (attempt %d) isn't running any more; stopping its agent", filepath.Base(r.opts.Root), rec.Task, rec.Attempt)
		rec.Proc.stop()
	}
	rec.Proc, rec.Done = nil, true
	r.saveRecord(rec)
}

// InFlight reports whether the project at root has agent work a worker should take back when it starts: a task's
// ask.json (written for every attempt, removed when it ends) or a chat turn's record.
func InFlight(root string) bool {
	for _, f := range []string{AskFile, filepath.Join("chat", turnFile)} {
		if _, err := os.Stat(filepath.Join(root, ".retake", "worker", f)); err == nil {
			return true
		}
	}
	return false
}

// --- a chat turn's record

const turnFile = "turn.json"

// turnRecord is the chat turn a worker runs, as a later worker needs it to take it back: chat/turn.json.
type turnRecord struct {
	Turn    string          `json:"turn"`
	Lease   string          `json:"lease"`
	Agent   string          `json:"agent"`
	Run     int             `json:"run"`     // its process: 1, or 2 once a resumed session failed and a new one started
	Session string          `json:"session"` // the session that process resumes ("" = a new one)
	Out     string          `json:"out"`     // its stdout, which the worker reads
	Log     string          `json:"log"`     // its stderr
	Offset  int64           `json:"offset"`  // how far into Out the server has the words
	Proc    *agentProc      `json:"proc,omitempty"`
	Secret  string          `json:"secret"`
	AskURL  string          `json:"ask_url,omitempty"`
	Snaps   []ext.SnapState `json:"snaps,omitempty"`
}

func (r *runner) turnPath() string { return filepath.Join(r.dir, "chat", turnFile) }

// readTurn is the turn record an earlier worker left, nil when there is none.
func (r *runner) readTurn() *turnRecord {
	b, err := os.ReadFile(r.turnPath())
	var rec turnRecord
	if err != nil || json.Unmarshal(b, &rec) != nil || !project.ValidWorkID(rec.Turn) || rec.Lease == "" {
		return nil
	}
	return &rec
}

// saveTurn changes rec and writes it.
func (r *runner) saveTurn(rec *turnRecord, fn func(*turnRecord)) {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	fn(rec)
	if err := os.MkdirAll(filepath.Dir(r.turnPath()), 0o700); err == nil {
		err = writeJSON(r.turnPath(), rec)
		if err != nil {
			r.log("%s: can't save the chat turn's record: %v", filepath.Base(r.opts.Root), err)
		}
	}
}
