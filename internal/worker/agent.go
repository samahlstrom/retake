package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/agent"
	"github.com/samahlstrom/retake/internal/project"
)

// DefaultProfiles uses the installed CLIs' own authentication and model settings.
func DefaultProfiles() []Profile {
	var out []Profile
	if bin, e := exec.LookPath("codex"); e == nil {
		out = append(out, Profile{AgentProfile: project.AgentProfile{ID: "codex", Name: "Codex"}, Command: []string{bin, "exec", "--json", "--sandbox", "workspace-write", "-c", "approval_policy=\"never\"", "--skip-git-repo-check", "-"}})
	}
	if bin, e := exec.LookPath("claude"); e == nil {
		out = append(out, Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"}, Command: []string{bin, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "acceptEdits"}})
	}
	return out
}

type agentResult struct {
	State   string `json:"state"`
	Message string `json:"message"`
}

// boundedLog retains a local capped diagnostic log and a short tail for provider telemetry.
type boundedLog struct {
	file    *os.File
	written int
	tail    []byte
}

func (w *boundedLog) Write(p []byte) (int, error) {
	if w.written < 8<<20 {
		part := p[:min(len(p), (8<<20)-w.written)]
		n, e := w.file.Write(part)
		w.written += n
		if e != nil {
			return 0, e
		}
	}
	w.tail = append(w.tail, p...)
	if len(w.tail) > 128<<10 {
		w.tail = append([]byte(nil), w.tail[len(w.tail)-(128<<10):]...)
	}
	return len(p), nil
}

func (r *runner) execute(ctx context.Context, a project.WorkAssignment) outcome {
	t := a.Task
	paused := func(s string) outcome { return outcome{state: "paused", message: s} }
	index := slices.IndexFunc(r.opts.Profiles, func(p Profile) bool { return p.ID == t.Agent })
	if index < 0 {
		return paused("Selected agent is not installed on this machine")
	}
	dir := filepath.Join(r.dir, "tasks", t.ID)
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return paused("Cannot create the local task directory")
	}
	// Preserve each attempt's result and context for provider-independent handoff.
	reportPath := filepath.Join(dir, fmt.Sprintf("report-%d.json", t.Attempt))
	contextPath := filepath.Join(dir, fmt.Sprintf("context-%d.json", t.Attempt))
	// A plain project's agent writes its video to output; the worker pushes it as the next version.
	plain, output := r.timeline == "", filepath.Join(dir, "output.mp4")
	var before []string
	var taskContext any = a
	prompt := timelinePrompt(contextPath, r.timeline, reportPath)
	if plain {
		vs := r.onDisk(false)
		if len(vs) == 0 {
			return paused("The project has no version that plays")
		}
		next, _, err := agent.NextVersionFile(vs, ".mp4")
		if err != nil {
			return paused("Couldn't name the next version: " + err.Error())
		}
		for _, v := range vs {
			before = append(before, v.Label)
		}
		_ = os.Remove(output) // an earlier attempt's: this attempt writes its own
		taskContext = r.plainContext(a, vs, output)
		prompt = plainPrompt(contextPath, vs[len(vs)-1].Path, output, next, reportPath)
	}
	if e := writeJSON(contextPath, taskContext); e != nil {
		return paused("Cannot save task context")
	}
	for _, n := range a.Notes {
		if !slices.Contains(t.Notes, n.ID) {
			continue
		}
		if n.Detail.Snapshot != "" {
			if e := r.fetchArtifact(ctx, fmt.Sprintf("/api/worker/notes/%d/snapshot", n.ID), filepath.Join(dir, fmt.Sprintf("note-%d.png", n.ID))); e != nil {
				return paused("Could not retrieve the annotated review frame: " + e.Error())
			}
		}
		for _, im := range n.Images {
			if !project.ValidImageName(im.File) {
				return paused("Review image has an invalid file name")
			}
			if e := r.fetchArtifact(ctx, fmt.Sprintf("/api/worker/notes/%d/images/%s", n.ID, im.File), filepath.Join(dir, fmt.Sprintf("note-%d-%s", n.ID, im.File))); e != nil {
				return paused("Could not retrieve an attached review image: " + e.Error())
			}
		}
	}
	p := r.opts.Profiles[index]
	cmd := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...) //nolint:gosec // trusted local argv, never supplied by the host
	cmd.Dir = r.opts.Root
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = agentEnv(os.Environ(), contextPath, reportPath, p.Env)
	if plain {
		cmd.Env = append(cmd.Env, "RETAKE_OUTPUT="+output)
	}
	ownGroup(cmd)
	cmd.Cancel = func() error { return stopGroup(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	log, e := os.OpenFile(filepath.Join(dir, fmt.Sprintf("agent-%d.log", t.Attempt)), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // generated name under a validated task ID
	if e != nil {
		return paused("Cannot open the local agent log")
	}
	w := &boundedLog{file: log}
	cmd.Stdout = w
	cmd.Stderr = w
	e = cmd.Run()
	if cmd.Process != nil {
		killGroup(cmd.Process)
	}
	_ = log.Close()
	if ctx.Err() != nil {
		return outcome{state: "stopped", message: "Agent stopped"}
	}
	usage, limited := telemetry(w.tail)
	if limited {
		return outcome{state: "paused", message: "Usage limit reached. Switch agents or retry after the provider resets your allowance.", usage: usage}
	}
	if e != nil {
		return outcome{state: "paused", message: "Agent stopped without finishing. Check its local task log, then retry or switch agents.", usage: usage}
	}
	b, e := os.ReadFile(reportPath) //nolint:gosec // generated report path in this task's directory
	if e != nil {
		return paused("Agent exited without a completion report or question; working files are preserved")
	}
	var report agentResult
	if len(b) > 64<<10 || json.Unmarshal(b, &report) != nil || strings.TrimSpace(report.Message) == "" {
		return paused("Agent returned an invalid task report")
	}
	if report.State == "question" {
		return outcome{state: "question", message: report.Message, usage: usage}
	}
	if report.State != "completed" {
		return paused("Agent report must be completed or question")
	}
	if plain {
		// The video, not the agent's exit, completes the task: it must play, as the next version.
		label, problem := r.pushPlain(output, before)
		if problem != "" {
			return outcome{state: "paused", message: problem, usage: usage}
		}
		if r.opts.Handler == nil {
			for e := r.sendFiles(ctx); e != nil; e = r.sendFiles(ctx) {
				select {
				case <-ctx.Done():
					return outcome{state: "stopped"}
				case <-time.After(time.Second):
				}
			}
		}
		return outcome{state: "completed", message: report.Message, version: label, usage: usage}
	}
	r.mu.Lock()
	r.finalTask = t.ID
	r.mu.Unlock()
	// The renderer runs throughout editing. Completion additionally requires a stable final revision belonging
	// to this task, with its ENDLIST verified, not just a successful agent process exit.
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return outcome{state: "stopped"}
		case <-tick.C:
			source, _, e := fingerprint(r.opts.Root, r.timeline)
			if e != nil {
				return paused("Final source is not a valid timeline: " + e.Error())
			}
			r.mu.Lock()
			v, renderErr := r.render, r.renderErr
			r.mu.Unlock()
			if v.Task == t.ID && v.Source == source {
				if renderErr != "" {
					return paused("Render failed: " + renderErr)
				}
				if v.Complete {
					return outcome{state: "completed", message: report.Message, result: v.ID, usage: usage}
				}
			}
		}
	}
}

// timelinePrompt is the task prompt of a project with a native timeline.
func timelinePrompt(contextPath, timeline, reportPath string) string {
	return fmt.Sprintf(`You are editing a native Retake video project in the current working directory.
Read the project instructions, then the durable task context at %q. The native timeline is %q.
Only the note IDs in task.notes are assigned. Other notes are context: check them and prior events for conflicting or already-applied feedback. Original note versions, timing, strokes and targets must be respected. Saved annotated stills, when available, are note-<id>.png in this task directory. Attached reference images are note-<id>-<file>; their boxes and original video times are in the context.
Original local video revisions are retained in .retake/worker/renders/: each state.json gives its label (a draft such as v4-draft2 until a task completes it as v4), source.json is its timeline snapshot, and index.m3u8 is its video. Inspect the original frame there when needed; do not assume an old comment's time refers to the current source.
Make the requested source edits in this project. Preserve all existing working changes, media and other people's work. Retake automatically renders valid saved source changes and streams them to reviewers. Do not run retake push or mark notes complete yourself.
If feedback conflicts, the target is ambiguous, or needed material is missing, ask a concise specific question instead of guessing.
Before exiting, write a JSON object to %q with exactly: {"state":"completed","message":"concrete description of the change and checks"} OR {"state":"question","message":"your question"}.
A completed report is only a request: Retake waits for a checked playable render before resolving the comment.
The next agent may differ from you; leave source changes and useful context on disk. Do not delete or edit the worker's identity, lock, logs, context files or render output.
`, contextPath, timeline, reportPath)
}

func (r *runner) fetchArtifact(ctx context.Context, path, destination string) error {
	resp, e := r.request(ctx, http.MethodGet, path, nil)
	if e != nil {
		return e
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host returned %d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (25<<20)+1))
	if e != nil {
		return e
	}
	if len(b) > 25<<20 {
		return fmt.Errorf("review image exceeds size limit")
	}
	return os.WriteFile(destination, b, 0o600)
}

// agentEnv is the agent's environment: the worker's own without its token, the profile's extra variables (their
// $VAR and ${VAR} read from that same environment), then the task's context and report paths.
func agentEnv(env []string, contextPath, reportPath string, extra map[string]string) []string {
	reserved := func(k string) bool {
		return k == "RETAKE_TOKEN" || k == "RETAKE_CONTEXT" || k == "RETAKE_REPORT" || k == "RETAKE_OUTPUT"
	}
	own := map[string]string{}
	out := make([]string, 0, len(env)+len(extra)+2)
	for _, v := range env {
		k, val, _ := strings.Cut(v, "=")
		if !reserved(k) && !hasKey(extra, k) {
			out = append(out, v)
		}
		if !reserved(k) {
			own[k] = val
		}
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if k != "" && !strings.ContainsAny(k, "=\x00") && !reserved(k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		out = append(out, k+"="+os.Expand(extra[k], func(name string) string { return own[name] }))
	}
	return append(out, "RETAKE_CONTEXT="+contextPath, "RETAKE_REPORT="+reportPath)
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// telemetry reads only structured provider records. Subscription balances are never inferred from token usage.
func telemetry(data []byte) (*project.AgentUsage, bool) {
	var usage *project.AgentUsage
	limited := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		var event struct {
			Type    string          `json:"type"`
			IsError bool            `json:"is_error"`
			Result  string          `json:"result"`
			Message string          `json:"message"`
			Error   json.RawMessage `json:"error"`
			Usage   *struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
			Cost *float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.Usage != nil {
			usage = &project.AgentUsage{Input: event.Usage.Input, Output: event.Usage.Output, Cost: event.Cost}
		}
		if event.IsError || event.Type == "error" || event.Type == "turn.failed" {
			message := strings.ToLower(event.Result + event.Message + string(event.Error))
			for _, phrase := range []string{"rate_limit", "rate limit", "usage limit", "usage_limit", "quota exceeded", "limit reached", "limit has been reached", "you've hit your limit"} {
				if strings.Contains(message, phrase) {
					limited = true
				}
			}
		}
	}
	return usage, limited
}
