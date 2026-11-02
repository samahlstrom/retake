// Package worker executes locally configured agents and relays live renderer output to a Retake host.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/bridge"
	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/extwork"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// Profile is a trusted local executable and argument vector. Retake never executes a server-supplied command. Env
// is added to the agent's environment; its values may name the worker's own variables ($VAR, ${VAR}), so keys
// needn't be written into files. OpenIn is where the agent runs ("" is the background; spec/automation.md,
// "Watching the agents"), and Interactive is its command there when that's herdr or tmux.
type Profile struct {
	project.AgentProfile
	Command     []string          `json:"command"`
	Env         map[string]string `json:"env,omitempty"`
	OpenIn      string            `json:"open_in,omitempty"`
	Interactive []string          `json:"interactive,omitempty"`
}

// Options connects a native project to one hosted project using an agent token, or (Handler) to the project served
// by this process.
type Options struct {
	URL, Token, Root, Name string
	Profiles               []Profile
	Editor                 bridge.Editor
	Log                    io.Writer
	HTTP                   *http.Client
	// Handler serves the project's /api/worker/* routes in this process (retake open: spec/automation.md, "Local
	// mode"). Requests go straight to it, with no network and no token; URL, Token and HTTP are then unused.
	Handler http.Handler
	// Prober probes the project's version files; nil = ffprobe.
	Prober family.Prober
	// Still makes the still of a question's moment (retake ask); nil = FFmpegStill. NoStill turns it off.
	Still   Still
	NoStill bool
	// Exts are this computer's extensions in effect (spec/extensions.md): their kinds, owned files, agent
	// instructions and history. Transcriber transcribes owned audio files (a talk-out); nil: none is set up.
	Exts        *ext.Set
	Transcriber func() (transcribe.Transcriber, error)
}

// Errors that say why a project can't be edited here (Reason turns them into a sentence for the page).
var (
	ErrNoAgents = errors.New("no agents installed: install Claude Code or Codex, or configure an agent")
	ErrBusy     = errors.New("another retake worker is editing this project")
)

type runner struct {
	opts                  Options
	identity              project.EditingWorker
	dir, timeline, editor string
	mu                    sync.Mutex
	logMu                 sync.Mutex
	active                *project.EditingTask
	connected             time.Time
	selected              bool
	render                project.LiveRender
	renderErr             string
	finalTask             string
	versions              int             // the family's versions, from the last poll: outside a task, render only while 0
	warned                map[string]bool // profiles already logged as having no interactive command
	ask                   askState        // questions from inside a task (ask.go)
	batch                 batchState      // the video's next batch, for a live session (batch.go)
	chat                  *chatRun        // the chat turn it runs (chat.go)
	life                  context.Context // the worker's whole run: its question endpoint lives this long
	extJobs               *extwork.Jobs   // transcriptions of owned audio files, for the relay's ext.transcribe

	recMu sync.Mutex // the running attempt's record, which the control loop gives a new lease (durable.go)
	rec   *runRecord

	famMu  sync.Mutex
	fam    []family.Version // the project's versions at famAt (family)
	famAt  time.Time
	sent   []project.FileVersion // what the server was told last, at sentAt
	sentAt time.Time
}

type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return fmt.Sprintf("host returned %d: %s", e.status, e.message) }

func (r *runner) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, e := http.NewRequestWithContext(ctx, method, strings.TrimRight(r.opts.URL, "/")+path, body)
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+r.opts.Token)
	req.Header.Set("X-Retake-Worker", r.identity.ID)
	req.Header.Set("X-Retake-Session", r.identity.Session)
	req.Header.Set("Content-Type", "application/json")
	return r.opts.HTTP.Do(req)
}

func (r *runner) call(ctx context.Context, path string, in, out any) error {
	b, e := json.Marshal(in)
	if e != nil {
		return e
	}
	resp, e := r.request(ctx, http.MethodPost, path, bytes.NewReader(b))
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &httpError{resp.StatusCode, string(data)}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
	}
	_, e = io.Copy(io.Discard, resp.Body)
	return e
}

func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.WriteFile(path+".tmp", b, 0o600); e != nil { //nolint:gosec // fixed worker paths composed with validated task/render IDs
		return e
	}
	return os.Rename(path+".tmp", path) //nolint:gosec // the same locally constructed worker path
}

// Run blocks until cancellation. It holds the project lock until all child processes have stopped.
func Run(ctx context.Context, o Options) error {
	if o.Handler != nil {
		o.URL, o.Token, o.HTTP = "http://retake.invalid", "in-process", &http.Client{Transport: handlerTransport{o.Handler}}
	}
	u, e := url.Parse(o.URL)
	if e != nil || u.Host == "" || u.User != nil || u.Scheme != "http" && u.Scheme != "https" || o.Token == "" {
		return errors.New("a project URL and agent token are required")
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 20 * time.Second}
	}
	// A redirect must never move the bearer token to a different origin or a sign-in page.
	client := *o.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	o.HTTP = &client
	if o.Log == nil {
		o.Log = io.Discard
	}
	root, e := filepath.Abs(o.Root)
	if e != nil {
		return e
	}
	o.Root, e = filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	if o.Prober == nil {
		o.Prober = probe.New(io.Discard)
	}
	if o.Still == nil && !o.NoStill {
		o.Still = FFmpegStill
	}
	r := &runner{opts: o, dir: filepath.Join(o.Root, ".retake", "worker"),
		extJobs: &extwork.Jobs{Transcriber: o.Transcriber}}
	// A project without a timeline is a plain one (spec/automation.md, "Plain projects"): no renderer.
	r.timeline, e = bridge.FindTimeline(o.Root)
	if errors.Is(e, bridge.ErrNoTimeline) {
		r.timeline, e = "", nil
	}
	if e != nil {
		return e
	}
	if r.timeline != "" {
		if r.editor, e = o.Editor.Find(); e != nil {
			return e
		}
	}
	if e = os.MkdirAll(r.dir, 0o700); e != nil {
		return e
	}
	unlock, e := lockProject(filepath.Join(r.dir, "lock"))
	if e != nil {
		return e
	}
	defer unlock()
	id, e := savedID(filepath.Join(r.dir, "identity"))
	if e != nil {
		return e
	}
	session := project.NewWorkID()
	if o.Handler != nil {
		// In process, the project lock already excludes a second worker, so a restart takes over its own session at
		// once instead of waiting for the old one's heartbeat to run out.
		if session, e = savedID(filepath.Join(r.dir, "session")); e != nil {
			return e
		}
	}
	if o.Name == "" {
		o.Name, _ = os.Hostname()
	}
	r.identity = project.EditingWorker{ID: id, Name: o.Name, Session: session}
	r.identity.Extensions = r.extRefs()
	seen := map[string]bool{}
	for _, p := range o.Profiles {
		if !project.ValidWorkID(p.ID) || len(p.Command) == 0 || seen[p.ID] ||
			p.OpenIn != "" && p.OpenIn != OpenBackground && p.OpenIn != OpenHerdr && p.OpenIn != OpenTmux ||
			p.Interactive != nil && (len(p.Interactive) == 0 || p.Interactive[0] == "") {
			return errors.New("invalid or duplicate local agent profile")
		}
		seen[p.ID] = true
		r.identity.Agents = append(r.identity.Agents, p.AgentProfile)
	}
	if len(seen) == 0 && o.Handler != nil {
		return ErrNoAgents // in process the server reads the folder itself: a worker without an agent adds nothing
	}
	// Without an agent, a worker that talks to a server still relays the project's video, so it plays there as it
	// does in retake open; it just never takes a task.
	r.render = lastRender(r.dir) // a restart with unchanged source renders and registers nothing
	if b, err := os.ReadFile(filepath.Join(r.dir, AskFile)); err == nil {
		var was AskEndpoint
		if json.Unmarshal(b, &was) == nil {
			r.askWas(was.URL) // an agent that outlived the last worker may ask there
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r.life = ctx
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { r.mediaLoop(ctx) })
	}
	if r.timeline != "" {
		wg.Go(func() { r.renderLoop(ctx) })
	}
	if o.Handler == nil {
		wg.Go(func() { r.filesLoop(ctx) })
	}
	defer wg.Wait()
	defer cancel()
	if o.Handler == nil {
		r.log("Connecting %s to %s", r.identity.Name, o.URL)
	}
	return r.controlLoop(ctx)
}

type outcome struct {
	state, message, result string
	version                string           // a plain task's new version file
	changes                map[int64]string // what changed for each note, from the report
	usage                  *project.AgentUsage
}

func (r *runner) log(format string, args ...any) {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintf(r.opts.Log, format+"\n", args...)
}

func (r *runner) controlLoop(ctx context.Context) error {
	tick := time.After(0) // the first poll at once: an agent that outlived the last worker is taken back first
	var current *project.EditingTask
	var cancel context.CancelCauseFunc
	var done chan outcome
	var pending *outcome
	var abandoned, stopping, readopt bool
	var lastError string
	registered := false
	// What an earlier worker left running, to take back (spec/automation.md, "Agents outlive Retake").
	adopt := r.adoptable(ctx)
	turn := r.readTurn()
	if adopt != nil {
		r.askWas(adopt.AskURL)
	} else {
		_ = os.Remove(filepath.Join(r.dir, AskFile)) // no attempt to take back: an older crash's
	}
	if turn != nil {
		r.askWas(turn.AskURL)
	}
	defer func() {
		// The worker is going away: its agent works on, for the next worker to take back.
		if cancel != nil {
			cancel(nil)
		}
		if done != nil {
			<-done
		}
		r.leaveChat()
	}()
	run := func(a project.WorkAssignment, rec *runRecord) {
		current = a.Task
		r.setActive(current)
		r.resetBatch()
		stopping, abandoned, readopt = false, false, false
		child, childCancel := context.WithCancelCause(ctx)
		cancel = childCancel
		done = make(chan outcome, 1)
		go func(ch chan outcome) {
			defer childCancel(nil)
			ch <- r.resume(child, a, rec)
		}(done)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-done:
			done = nil
			if result.state == detached {
				continue // the worker is going away: there's nothing to report
			}
			pending = &result
			if abandoned {
				// The attempt isn't this worker's any more: nothing to report.
				r.finished(current.ID)
				current, pending, abandoned = nil, nil, false
				r.setActive(nil)
			}
		case <-tick:
			tick = time.After(time.Second)
			poll := project.WorkPoll{Worker: r.identity}
			poll.Chat, poll.ChatLease = r.chatTurn()
			if poll.Chat == "" && turn != nil {
				poll.Chat, poll.ChatLease = turn.Turn, turn.Lease
			}
			switch {
			case current != nil && readopt:
				poll.Adopt = &project.WorkAdopt{Task: current.ID, Attempt: current.Attempt, Lease: current.Lease,
					Notes: r.withGiven(current).Notes, Alive: true}
			case current != nil:
				poll.Task, poll.Lease = current.ID, current.Lease
				poll.Told, poll.Join = r.unsentTold(), r.unsentJoin()
			case adopt != nil && poll.Chat == "":
				poll.Adopt = r.adoption(ctx, adopt)
			}
			var assignment project.WorkAssignment
			pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
			started := time.Now()
			e := r.call(pollCtx, "/api/worker/poll", poll, &assignment)
			pollCancel()
			if e != nil {
				if lastError != e.Error() {
					r.log("Worker connection: %v", e)
					lastError = e.Error()
				}
				// Lost connectivity stops nothing: the agent works on, and the lease is renewed (or the attempt taken
				// back) once the server answers. A lease the server no longer has is asked back for.
				var he *httpError
				if current != nil && poll.Task != "" && errors.As(e, &he) && he.status == 409 && strings.Contains(he.message, "no longer owns") {
					readopt = true
				}
				continue
			}
			if !registered || lastError != "" {
				if r.opts.Handler == nil {
					r.log("Connected. Choose this machine and agent in the review's Agent panel.")
				}
				registered = true
				lastError = ""
			}
			r.mu.Lock()
			r.connected = started
			r.selected = assignment.Config.Worker == r.identity.ID
			r.versions = assignment.Versions
			r.mu.Unlock()
			if poll.Task != "" {
				r.acked(poll.Told) // the server keeps them on the task now
				r.joined(poll.Join)
			}
			if turn != nil && poll.Chat == turn.Turn {
				r.resumeChat(ctx, assignment.Chat, turn)
				turn = nil
			} else {
				r.takeChat(ctx, assignment.Chat)
			}
			switch {
			case poll.Adopt != nil && current == nil:
				// A record of an earlier worker: its attempt, taken back, or not this worker's any more.
				rec := adopt
				adopt = nil
				if assignment.Task != nil && assignment.Task.ID == rec.Task && assignment.Task.Worker == r.identity.ID &&
					project.ValidWorkID(assignment.Task.Lease) {
					run(assignment, rec)
				} else {
					r.refused(rec)
				}
				continue
			case poll.Adopt != nil:
				// Its lease had gone: the attempt carries on with the one it got back, or stops.
				readopt = false
				if assignment.Task != nil && assignment.Task.ID == current.ID && project.ValidWorkID(assignment.Task.Lease) {
					current = assignment.Task
					r.setActive(current)
					r.setLease(current.ID, current.Lease)
				} else if !abandoned {
					r.log("%s: task %s isn't this worker's any more; stopping its agent", filepath.Base(r.opts.Root), current.ID)
					abandoned = true
					if done != nil {
						cancel(errStop)
					} else {
						r.finished(current.ID)
						current, pending, abandoned = nil, nil, false
						r.setActive(nil)
					}
				}
				continue
			}
			if current == nil && assignment.Task != nil {
				if !project.ValidWorkID(assignment.Task.ID) || !project.ValidWorkID(assignment.Task.Lease) || assignment.Task.Worker != r.identity.ID {
					return errors.New("host returned an invalid task assignment")
				}
				run(assignment, nil)
			} else if current != nil && assignment.Task != nil {
				current = assignment.Task
				r.setActive(current)
				r.sawQuestions(current, assignment.Questions)
				r.offer(assignment.Next, assignment.Notes)
				if current.Stop && done != nil {
					cancel(errStop) // a person's pause, switch or withdrawal
					stopping = true
				}
			}
			if pending != nil && current != nil && !abandoned {
				result := *pending
				if stopping {
					result = outcome{state: "stopped", message: "Agent process stopped; working files retained", usage: result.usage}
				}
				told, join := r.unsentTold(), r.unsentJoin()
				report := project.WorkReport{Worker: r.identity.ID, Task: current.ID, Lease: current.Lease, State: result.state,
					Message: result.message, Result: result.result, Version: result.version, Usage: result.usage, Told: told,
					Join: join, Notes: result.changes}
				if r.call(ctx, "/api/worker/report", report, nil) == nil {
					r.acked(told)
					r.joined(join)
					r.finished(current.ID)
					current = nil
					pending = nil
					r.setActive(nil)
				}
			}
		}
	}
}

// setRecord makes rec the running attempt's record (nil: none).
func (r *runner) setRecord(rec *runRecord) {
	r.recMu.Lock()
	defer r.recMu.Unlock()
	r.rec = rec
}

// setLease gives the running attempt's record the lease the server gave it back with.
func (r *runner) setLease(task, lease string) {
	r.recMu.Lock()
	rec := r.rec
	if rec == nil || rec.Task != task {
		r.recMu.Unlock()
		return
	}
	rec.Lease = lease
	r.recMu.Unlock()
	r.saveRecord(rec)
}

func (r *runner) setActive(t *project.EditingTask) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t == nil {
		r.active = nil
	} else {
		clone := *t
		r.active = &clone
	}
}
func (r *runner) state() (*project.EditingTask, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var t *project.EditingTask
	if r.active != nil {
		clone := *r.active
		t = &clone
	}
	return t, r.selected && time.Since(r.connected) < 10*time.Second
}

// savedID reads a random identifier kept in the locked worker directory, making it on first use.
func savedID(path string) (string, error) {
	id, e := os.ReadFile(path) //nolint:gosec // a fixed file in the locked project worker directory
	if errors.Is(e, os.ErrNotExist) {
		id = []byte(project.NewWorkID())
		e = os.WriteFile(path, id, 0o600)
	}
	if e != nil {
		return "", e
	}
	if !project.ValidWorkID(string(id)) {
		return "", fmt.Errorf("invalid saved worker identity in %s", path)
	}
	return string(id), nil
}
