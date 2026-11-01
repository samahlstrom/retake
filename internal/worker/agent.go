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

// DefaultProfiles uses the installed CLIs' own authentication and model settings. Interactive is the same agent as
// a session the person can watch (herdr, tmux), with the same permissions as the headless command: none are asked
// for, since nobody is there to answer (spec/automation.md, "Permissions"). Claude Code's own question tool is off;
// an agent asks with retake ask.
func DefaultProfiles() []Profile {
	var out []Profile
	if bin, e := exec.LookPath("codex"); e == nil {
		out = append(out, Profile{AgentProfile: project.AgentProfile{ID: "codex", Name: "Codex"},
			Command:     []string{bin, "exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check", "-"},
			Interactive: []string{bin, "--dangerously-bypass-approvals-and-sandbox"}})
	}
	if bin, e := exec.LookPath("claude"); e == nil {
		// The = keeps the tool list from taking the task's line, tmux's last argument, as a second tool.
		out = append(out, Profile{AgentProfile: project.AgentProfile{ID: "claude", Name: "Claude Code"},
			Command:     []string{bin, "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions", "--disallowedTools=AskUserQuestion"},
			Interactive: []string{bin, "--dangerously-skip-permissions", "--disallowedTools=AskUserQuestion"}})
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
	askEnv, fresh, endAsk := r.beginAsk(a)
	defer endAsk()
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
	mode := r.openIn(p)
	if mode != OpenBackground {
		prompt += `This is an interactive session that the person can watch and type into. It stays open after your report: later Retake tasks for this video arrive here as new messages, each with its own context and report file.
`
	}
	promptPath := filepath.Join(dir, fmt.Sprintf("prompt-%d.md", t.Attempt))
	if e := os.WriteFile(promptPath, []byte(prompt), 0o600); e != nil {
		return paused("Cannot save the task prompt")
	}
	var usage *project.AgentUsage
	if mode == OpenBackground {
		var stop *outcome
		out := ""
		if plain {
			out = output
		}
		if usage, stop = r.background(ctx, p, prompt, dir, contextPath, reportPath, out, t.Attempt, askEnv); stop != nil {
			return *stop
		}
	} else if stop := r.interactive(ctx, mode, p, t, promptPath, reportPath, fresh); stop != nil {
		return *stop
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
	if r.allHeld(t) {
		// Every note waits on the agent's own questions: nothing is done yet, so there's no render or version to wait
		// for. The server keeps the task for the answers.
		return outcome{state: "completed", message: report.Message, usage: usage}
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

// background runs p's headless command with the prompt on stdin until it exits. A non-nil outcome ends the task.
// output, when set, is where a plain project's agent writes its video (RETAKE_OUTPUT).
func (r *runner) background(ctx context.Context, p Profile, prompt, dir, contextPath, reportPath, output string, attempt int, askEnv []string) (*project.AgentUsage, *outcome) {
	cmd := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...) //nolint:gosec // trusted local argv, never supplied by the host
	cmd.Dir = r.opts.Root
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = agentEnv(os.Environ(), contextPath, reportPath, p.Env)
	if output != "" {
		cmd.Env = append(cmd.Env, "RETAKE_OUTPUT="+output)
	}
	cmd.Env = append(cmd.Env, askEnv...)
	ownGroup(cmd)
	cmd.Cancel = func() error { return stopGroup(cmd.Process) }
	cmd.WaitDelay = 3 * time.Second
	log, e := os.OpenFile(filepath.Join(dir, fmt.Sprintf("agent-%d.log", attempt)), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // generated name under a validated task ID
	if e != nil {
		return nil, &outcome{state: "paused", message: "Cannot open the local agent log"}
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
		return nil, &outcome{state: "stopped", message: "Agent stopped"}
	}
	usage, limited := telemetry(w.tail)
	if limited {
		return usage, &outcome{state: "paused", message: "Usage limit reached. Switch agents or retry after the provider resets your allowance.", usage: usage}
	}
	if e != nil {
		return usage, &outcome{state: "paused", message: "Agent stopped without finishing. Check its local task log, then retry or switch agents.", usage: usage}
	}
	return usage, nil
}

// interactive runs p's agent as a session in herdr or tmux and waits for its report. The session stays open after
// the report; stopping before one closes its pane. A non-nil outcome ends the task. Interactive agents report no
// usage: there is no JSON stream to read it from.
func (r *runner) interactive(ctx context.Context, mode string, p Profile, t *project.EditingTask, promptPath, reportPath string, fresh []project.TaskQuestion) *outcome {
	v, e := r.newView(mode, p)
	if e != nil {
		return &outcome{state: "paused", message: e.Error()}
	}
	line := fmt.Sprintf("New Retake task for %s (task %s, attempt %d): follow the instructions in %s", v.name, t.ID, t.Attempt, promptPath)
	if len(fresh) > 0 {
		// The task resumes with answers to its questions: they come first, in the same session.
		var answers []string
		for _, q := range fresh {
			answers = append(answers, answerLine(q))
		}
		line = fmt.Sprintf("%s. Carry on with Retake task %s (attempt %d): follow the instructions in %s", strings.Join(answers, ". "), t.ID, t.Attempt, promptPath)
	}
	if e = v.start(ctx, p, line); e != nil {
		if ctx.Err() != nil {
			v.stop()
			return &outcome{state: "stopped", message: "Agent stopped"}
		}
		return &outcome{state: "paused", message: fmt.Sprintf("Couldn't open the agent in %s: %v", mode, e)}
	}
	r.log("%s: %s is working in %s (%s)", v.name, p.Name, mode, v.label())
	poll := time.NewTicker(viewPoll)
	defer poll.Stop()
	checked, misses, sayErr := time.Now(), 0, ""
	for {
		select {
		case <-ctx.Done():
			v.stop()
			return &outcome{state: "stopped", message: "Agent stopped"}
		case <-poll.C:
		}
		if reported(reportPath) {
			return nil
		}
		// An answer to the agent's question while it works: a new message in its session.
		for _, q := range r.untold() {
			if e := v.say(ctx, answerLine(q)); e != nil {
				if e.Error() != sayErr { // tried again at the next look; said once
					r.log("%s: can't give the agent its answer in %s yet: %v", v.name, mode, e)
					sayErr = e.Error()
				}
				break
			}
			r.tell(q.Answer.ID)
			r.log("%s: gave the agent the answer to its question on #%d", v.name, q.Note)
		}
		if time.Since(checked) < viewCheck {
			continue
		}
		checked = time.Now()
		if v.alive(ctx) {
			misses = 0
		} else if misses++; misses >= 2 && ctx.Err() == nil && !reported(reportPath) {
			return &outcome{state: "paused", message: "The agent's session ended without a report; working files are preserved"}
		}
	}
}

// reported is whether the agent has written its report: a whole JSON object, not one still being written.
func reported(path string) bool {
	b, e := os.ReadFile(path) //nolint:gosec // generated report path in this task's directory
	var report agentResult
	return e == nil && len(b) <= 64<<10 && json.Unmarshal(b, &report) == nil
}

// timelinePrompt is the task prompt of a project with a native timeline.
func timelinePrompt(contextPath, timeline, reportPath string) string {
	return fmt.Sprintf(`You are editing a native Retake video project in the current working directory.
Read the project instructions, then the durable task context at %q. The native timeline is %q.
Only the note IDs in task.notes are assigned (minus task.done, finished in an earlier attempt). Other notes are context: check them and prior events for conflicting or already-applied feedback. Original note versions, timing, strokes and targets must be respected. Saved annotated stills, when available, are note-<id>.png in this task directory. Attached reference images are note-<id>-<file>; their boxes and original video times are in the context.
Original local video revisions are retained in .retake/worker/renders/: each state.json gives its label (a draft such as v4-draft2 until a task completes it as v4), source.json is its timeline snapshot, and index.m3u8 is its video. Inspect the original frame there when needed; do not assume an old comment's time refers to the current source.
Make the requested source edits in this project. Preserve all existing working changes, media and other people's work. Retake automatically renders valid saved source changes and streams them to reviewers. Do not run retake push or mark notes complete yourself.
The next agent may differ from you; leave source changes and useful context on disk. Do not delete or edit the worker's identity, lock, logs, context files, ask.json or render output.
`, contextPath, timeline) + askRules(reportPath, "A completed report is only a request: Retake waits for a checked playable render before resolving the comment.")
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

func reservedEnv(k string) bool {
	return k == "RETAKE_TOKEN" || k == "RETAKE_CONTEXT" || k == "RETAKE_REPORT" || k == "RETAKE_OUTPUT" ||
		k == "RETAKE_ASK_URL" || k == "RETAKE_ASK_TOKEN"
}

// agentEnv is the agent's environment: the worker's own without its token, the profile's extra variables (their
// $VAR and ${VAR} read from that same environment), then the task's context and report paths.
func agentEnv(env []string, contextPath, reportPath string, extra map[string]string) []string {
	out := make([]string, 0, len(env)+len(extra)+2)
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		if !reservedEnv(k) && !hasKey(extra, k) {
			out = append(out, v)
		}
	}
	out = append(out, profileEnv(env, extra)...)
	return append(out, "RETAKE_CONTEXT="+contextPath, "RETAKE_REPORT="+reportPath)
}

// profileEnv is a profile's extra variables as sorted K=V, their $VAR and ${VAR} read from env without the worker's
// own reserved variables.
func profileEnv(env []string, extra map[string]string) []string {
	own := map[string]string{}
	for _, v := range env {
		if k, val, _ := strings.Cut(v, "="); !reservedEnv(k) {
			own[k] = val
		}
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		if k != "" && !strings.ContainsAny(k, "=\x00") && !reservedEnv(k) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+os.Expand(extra[k], func(name string) string { return own[name] }))
	}
	return out
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
