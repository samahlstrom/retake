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
	"github.com/samahlstrom/retake/internal/project"
)

// Profile is a trusted local executable and argument vector. Retake never executes a server-supplied command.
type Profile struct {
	project.AgentProfile
	Command []string `json:"command"`
}

// Options connects a native project to one hosted project using an agent token.
type Options struct {
	URL, Token, Root, Name string
	Profiles               []Profile
	Editor                 bridge.Editor
	Log                    io.Writer
	HTTP                   *http.Client
}

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
	r := &runner{opts: o, dir: filepath.Join(o.Root, ".retake", "worker")}
	r.timeline, e = bridge.FindTimeline(o.Root)
	if e != nil {
		return e
	}
	r.editor, e = o.Editor.Find()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(r.dir, 0o700); e != nil {
		return e
	}
	unlock, e := lockProject(filepath.Join(r.dir, "lock"))
	if e != nil {
		return e
	}
	defer unlock()
	identityPath := filepath.Join(r.dir, "identity")
	id, e := os.ReadFile(identityPath) //nolint:gosec // fixed identity file in the locked project worker directory
	if errors.Is(e, os.ErrNotExist) {
		id = []byte(project.NewWorkID())
		e = os.WriteFile(identityPath, id, 0o600)
	}
	if e != nil {
		return e
	}
	if !project.ValidWorkID(string(id)) {
		return errors.New("invalid saved worker identity")
	}
	if o.Name == "" {
		o.Name, _ = os.Hostname()
	}
	r.identity = project.EditingWorker{ID: string(id), Name: o.Name, Session: project.NewWorkID()}
	seen := map[string]bool{}
	for _, p := range o.Profiles {
		if !project.ValidWorkID(p.ID) || len(p.Command) == 0 || seen[p.ID] {
			return errors.New("invalid or duplicate local agent profile")
		}
		seen[p.ID] = true
		r.identity.Agents = append(r.identity.Agents, p.AgentProfile)
	}
	if len(seen) == 0 {
		return errors.New("no agents installed: install Claude Code or Codex, or supply --profiles")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { r.mediaLoop(ctx) })
	}
	wg.Go(func() { r.renderLoop(ctx) })
	defer wg.Wait()
	defer cancel()
	r.log("Connecting %s to %s", r.identity.Name, o.URL)
	return r.controlLoop(ctx)
}

type outcome struct {
	state, message, result string
	usage                  *project.AgentUsage
}

func (r *runner) log(format string, args ...any) {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintf(r.opts.Log, format+"\n", args...)
}

func (r *runner) controlLoop(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var current *project.EditingTask
	var cancel context.CancelFunc
	var done chan outcome
	var pending *outcome
	var abandoned, stopping bool
	var lastError string
	registered := false
	defer func() {
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case result := <-done:
			pending = &result
			done = nil
		case <-ticker.C:
			var task, lease string
			if current != nil {
				task, lease = current.ID, current.Lease
			}
			var assignment project.WorkAssignment
			pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
			started := time.Now()
			e := r.call(pollCtx, "/api/worker/poll", map[string]any{"worker": r.identity, "task": task, "lease": lease}, &assignment)
			pollCancel()
			if e != nil {
				if lastError != e.Error() {
					r.log("Worker connection: %v", e)
					lastError = e.Error()
				}
				r.mu.Lock()
				last := r.connected
				r.mu.Unlock()
				var he *httpError
				stale := errors.As(e, &he) && he.status == 409
				if current != nil && (stale || time.Since(last) > 10*time.Second) {
					cancel()
					stopping = true
					abandoned = stale
				}
				if abandoned && done == nil {
					current = nil
					pending = nil
					abandoned = false
					r.setActive(nil)
				}
				continue
			}
			if !registered || lastError != "" {
				r.log("Connected. Choose this machine and agent in the review's Agent panel.")
				registered = true
				lastError = ""
			}
			r.mu.Lock()
			r.connected = started
			r.selected = assignment.Config.Worker == r.identity.ID
			r.mu.Unlock()
			if current == nil && assignment.Task != nil {
				if !project.ValidWorkID(assignment.Task.ID) || !project.ValidWorkID(assignment.Task.Lease) || assignment.Task.Worker != r.identity.ID {
					return errors.New("host returned an invalid task assignment")
				}
				current = assignment.Task
				r.setActive(current)
				stopping = false
				abandoned = false
				child, childCancel := context.WithCancel(ctx)
				cancel = childCancel
				done = make(chan outcome, 1)
				go func(ch chan outcome, a project.WorkAssignment) { defer childCancel(); ch <- r.execute(child, a) }(done, assignment)
			} else if current != nil && assignment.Task != nil {
				current = assignment.Task
				r.setActive(current)
				if current.Stop {
					cancel()
					stopping = true
				}
			}
			if pending != nil && current != nil {
				result := *pending
				if stopping {
					result = outcome{state: "stopped", message: "Agent process stopped; working files retained"}
				}
				report := project.WorkReport{Worker: r.identity.ID, Task: current.ID, Lease: current.Lease, State: result.state, Message: result.message, Result: result.result, Usage: result.usage}
				if r.call(ctx, "/api/worker/report", report, nil) == nil {
					cancel()
					current = nil
					pending = nil
					r.setActive(nil)
				}
			}
		}
	}
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
