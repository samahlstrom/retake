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
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/samahlstrom/retake/internal/ext"
	"github.com/samahlstrom/retake/internal/family"
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
	// Notes is what changed for each note, by note id (spec/automation.md, "Batches"); optional.
	Notes map[string]string `json:"notes,omitempty"`
}

// maxReport is the largest report file read: a batch's report may say what changed for each of its notes.
const maxReport = 1 << 20

// attempt is one run of a task's agent, from its start, or from an earlier worker's record, to its end.
type attempt struct {
	r      *runner
	a      project.WorkAssignment // its assignment, with the notes' stills
	t      *project.EditingTask
	p      Profile
	rec    *runRecord
	dir    string // the task's folder
	mode   string
	extra  string // the extensions' section of the prompt
	vs     []family.Version
	more   *addition
	v      *view
	askEnv []string
	endAsk func(keep bool)
	snaps  []*ext.Snap
}

// execute runs a new attempt of a's task.
func (r *runner) execute(ctx context.Context, a project.WorkAssignment) outcome {
	return r.resume(ctx, a, nil)
}

// resume runs an attempt: a new one, or with rec one an earlier worker started, carried on where it was, with no new
// prompt (spec/automation.md, "Agents outlive Retake"). It returns how the attempt ended, or the outcome detached when
// the worker is going away and the agent works on: ctx ending without errStop.
func (r *runner) resume(ctx context.Context, a project.WorkAssignment, rec *runRecord) outcome {
	t := a.Task
	index := slices.IndexFunc(r.opts.Profiles, func(p Profile) bool { return p.ID == t.Agent })
	if index < 0 {
		return outcome{state: "paused", message: "Selected agent is not installed on this machine"}
	}
	at := &attempt{r: r, a: a, t: t, p: r.opts.Profiles[index], dir: filepath.Join(r.dir, "tasks", t.ID), rec: rec}
	if e := os.MkdirAll(at.dir, 0o700); e != nil {
		return outcome{state: "paused", message: "Cannot create the local task directory"}
	}
	var o *outcome
	switch {
	case rec != nil && rec.Outcome != nil:
		// It ended before the server heard: say it again, and finish what its end didn't.
		at.resnap()
		back := rec.Outcome.back()
		o = &back
	case rec != nil:
		o = at.takeBack()
	default:
		o = at.start(ctx)
	}
	if o == nil {
		end := at.rounds(ctx)
		o = &end
	}
	if o.state == detached {
		if at.endAsk != nil {
			at.endAsk(true) // the agent may still ask; the next worker answers
		}
		return *o
	}
	at.end(*o)
	return *o
}

// start begins a new attempt: its context and prompt, then the agent with its line (herdr, tmux) or (background) the
// process the first round starts. A non-nil outcome ends it.
func (at *attempt) start(ctx context.Context) *outcome {
	r, t := at.r, at.t
	paused := func(s string) *outcome { return &outcome{state: "paused", message: s} }
	r.stopOrphans(t.ID, "") // one agent per video
	// Preserve each attempt's result and context for provider-independent handoff.
	rec := &runRecord{Task: t.ID, Attempt: t.Attempt, Lease: t.Lease, Agent: t.Agent, Phase: phaseAgent, K: 1,
		Prompt:  filepath.Join(at.dir, fmt.Sprintf("prompt-%d.md", t.Attempt)),
		Context: filepath.Join(at.dir, fmt.Sprintf("context-%d.json", t.Attempt)),
		Report:  filepath.Join(at.dir, fmt.Sprintf("report-%d.json", t.Attempt)),
		Log:     filepath.Join(at.dir, fmt.Sprintf("agent-%d.log", t.Attempt)),
		Notes:   slices.Clone(t.Notes), Round: t.Pending(), Doc: docOnly(at.a)}
	at.rec = rec
	at.extra = r.extSection(at.a)
	// A plain project's agent writes its video to output; the worker pushes it as the next version.
	// A task of only typed comments on an extension's version (v0's documents) renders and pushes nothing.
	if r.timeline == "" && !rec.Doc {
		at.vs = r.onDisk(false)
		if len(at.vs) == 0 {
			if _, ok := r.kindVersion(); !ok {
				return paused("The project has no version that plays")
			}
		}
		plan, err := r.plainPlan(t.Target, at.vs, filepath.Join(at.dir, "output.mp4"))
		if err != nil {
			return paused("Couldn't name the version: " + err.Error())
		}
		rec.Plain = plan
		_ = os.Remove(plan.Output) // an earlier attempt's: this attempt writes its own
	}
	// A target older than the newest version starts from its own source; the newest one's comes back after.
	if r.timeline != "" && !rec.Doc && at.a.From != "" {
		before, err := r.checkout(at.a.From, filepath.Join(at.dir, "timeline-before.json"))
		if err != nil {
			return paused("Couldn't start from that version's source: " + err.Error())
		}
		rec.Restore = before
	}
	// The notes' stills and images: a missing one never stops the task (spec/automation.md, "A note's stills and
	// images").
	r.noteFiles(ctx, &at.a, at.dir)
	var fresh []project.TaskQuestion
	at.askEnv, fresh, rec.Secret, rec.AskURL, at.endAsk = r.beginAsk(at.a, "", false)
	if e := at.writeContext(at.a, rec.Context); e != nil {
		return paused("Cannot save task context")
	}
	at.mode = r.openIn(at.p)
	rec.Mode = at.mode
	prompt := at.fullPrompt(rec.Context, rec.Report)
	if at.mode != OpenBackground {
		prompt += `This is an interactive session that the person can watch and type into. It stays open after your report: later Retake tasks for this video arrive here as new messages, each with its own context and report file.
`
	}
	if e := os.WriteFile(rec.Prompt, []byte(prompt), 0o600); e != nil {
		return paused("Cannot save the task prompt")
	}
	// A copy of the extensions' files before the agent touches them, so a person can undo its edit of a document: one
	// edit per round, as each was one attempt before rounds.
	rec.Snaps = at.snapshot()
	at.batch()
	r.setRecord(rec)
	// The target is live at once: a plain project's first revision is the video the agent starts from.
	r.startRevision(ctx, t, rec)
	if at.mode != OpenBackground {
		v, stop := r.openSession(ctx, at.mode, at.p, t, rec.Prompt, fresh)
		if stop != nil {
			return stop
		}
		at.v = v
		r.update(rec, func(x *runRecord) { x.Pane = v.state.Pane }) // the agent has its line: a later worker can take it back
	}
	return nil
}

// takeBack carries on with an attempt an earlier worker started: the same attempt and report file, no new prompt.
func (at *attempt) takeBack() *outcome {
	r, t, rec := at.r, at.t, at.rec
	r.update(rec, func(x *runRecord) { x.Lease = t.Lease })
	at.mode = rec.Mode
	at.extra = r.extSection(at.a)
	if rec.Plain != nil {
		at.vs = r.onDisk(false)
		if rec.Plain.Into == "" { // an older worker's: where it goes, as the attempt's start said
			if plan, err := r.plainPlan(t.Target, at.vs, rec.Plain.Output); err == nil {
				plan.Before = rec.Plain.Before
				rec.Plain = plan
			}
		}
	}
	at.askEnv, _, rec.Secret, rec.AskURL, at.endAsk = r.beginAsk(at.a, rec.Secret, true)
	at.resnap()
	at.batch()
	// Notes given to its session that the server may not have heard of yet: the next poll says so.
	r.mu.Lock()
	r.batch.given = slices.Clone(rec.Given)
	r.batch.unsent = slices.DeleteFunc(slices.Clone(rec.Given), func(id int64) bool { return slices.Contains(t.Notes, id) })
	r.mu.Unlock()
	r.setRecord(rec)
	r.saveRecord(rec)
	if at.mode != OpenBackground {
		at.v = r.viewOf(rec) // nil when its session is gone: its report may still be there
	}
	r.log("%s: took task %s back (attempt %d): the agent works on", filepath.Base(r.opts.Root), t.ID, t.Attempt)
	return nil
}

// resnap is the record's undo snapshots, to finish.
func (at *attempt) resnap() {
	for _, st := range at.rec.Snaps {
		if s := ext.ResumeSnap(at.r.opts.Root, st); s != nil {
			at.snaps = append(at.snaps, s)
		}
	}
}

// batch is what the attempt needs to give its agent the comments sent while it works (spec/automation.md, "Batches"):
// in herdr or tmux a message in the session; in the background, a follow-up round of the same agent once its run ends.
func (at *attempt) batch() {
	out := ""
	if at.rec.Plain != nil {
		out = at.rec.Plain.Output
	}
	at.more = &addition{a: at.a, dir: at.dir, doc: at.rec.Doc, output: out, write: at.writeContext, k: at.rec.K,
		full: at.fullPrompt}
}

func (at *attempt) writeContext(a project.WorkAssignment, path string) error {
	if at.rec.Plain != nil {
		return writeJSON(path, at.r.plainContext(a, at.vs, at.rec.Plain.Output))
	}
	return writeJSON(path, a)
}

// fullPrompt is the whole task prompt with these context and report files.
func (at *attempt) fullPrompt(contextPath, reportPath string) string {
	switch pl := at.rec.Plain; {
	case at.rec.Doc:
		return docPrompt(contextPath, reportPath, at.extra)
	case pl != nil:
		return plainPrompt(contextPath, pl.From, pl.Updating, pl.Output, pl.Into, reportPath, at.extra)
	}
	return timelinePrompt(contextPath, at.r.timeline, reportPath, at.extra)
}

// snapshot keeps a copy of the extensions' files before a round, and returns it as the record keeps it.
func (at *attempt) snapshot() []ext.SnapState {
	at.snaps = at.r.snapshot(at.t.ID)
	var out []ext.SnapState
	for _, s := range at.snaps {
		out = append(out, s.State())
	}
	return out
}

// finishSnaps records what the agent changed in the extensions' files since the snapshot.
func (at *attempt) finishSnaps() {
	for _, s := range at.snaps {
		if _, err := s.Finish(); err != nil {
			at.r.log("%s: couldn't record the agent's edits: %v", filepath.Base(at.r.opts.Root), err)
		}
	}
	at.snaps = nil
}

// end finishes an attempt that ended (not one left to its agent): its end kept until the server has it, the undo
// snapshots finished, an older target's checkout put back, and its ask secret and file gone.
func (at *attempt) end(o outcome) {
	r, rec := at.r, at.rec
	if rec != nil {
		r.update(rec, func(x *runRecord) { x.Outcome, x.Proc = o.saved(), nil })
	}
	at.finishSnaps()
	if rec != nil && rec.Restore != "" {
		r.restore(rec.Restore)
	}
	if at.endAsk != nil {
		at.endAsk(false)
	} else {
		_ = os.Remove(filepath.Join(r.dir, AskFile))
	}
	if rec != nil {
		r.update(rec, func(x *runRecord) { x.Snaps, x.Restore = nil, "" })
	}
	r.setRecord(nil)
}

// rounds runs the attempt from its record's phase to its end: the agent's rounds, then the version.
func (at *attempt) rounds(ctx context.Context) outcome {
	r, t, rec := at.r, at.t, at.rec
	paused := func(s string) outcome { return outcome{state: "paused", message: s, usage: rec.Usage} }
	done := func(o outcome) outcome {
		o.state, o.message, o.changes, o.said, o.usage = "completed", strings.Join(rec.Messages, " "), rec.Changes, rec.Said, rec.Usage
		return o
	}
	leave := func() outcome {
		if stopped(ctx) {
			return outcome{state: "stopped", usage: rec.Usage}
		}
		return outcome{state: detached}
	}
	for {
		switch rec.Phase {
		case phaseAgent:
			if at.mode == OpenBackground {
				if stop := at.background(ctx); stop != nil {
					return *stop
				}
			} else if stop := at.waitSession(ctx); stop != nil {
				return *stop
			}
			report, problem := readReport(rec.Report)
			if problem != "" {
				return paused(problem)
			}
			if report.State == "question" {
				return outcome{state: "question", message: report.Message, usage: rec.Usage}
			}
			if report.State != "completed" {
				return paused("Agent report must be completed or question")
			}
			// What each note changed: the report's own entry, else the message of the round that did it.
			r.update(rec, func(x *runRecord) {
				if x.Changes == nil {
					x.Changes = map[int64]string{}
				}
				for id, c := range noteChanges(report.Notes) {
					x.Changes[id] = c
					if !slices.Contains(x.Said, id) {
						x.Said = append(x.Said, id)
					}
				}
				for _, id := range x.Round {
					if _, ok := x.Changes[id]; !ok {
						x.Changes[id] = report.Message
					}
				}
				x.Messages = append(x.Messages, report.Message)
				x.Phase = phaseReported
			})
		case phaseReported:
			if at.nextRound(ctx, true) {
				continue
			}
			if r.allHeld(r.withGiven(t)) || rec.Doc {
				// Every note waits on the agent's own questions: nothing is done yet, so there's no render or version to
				// wait for. The server keeps the task for the answers.
				return done(outcome{})
			}
			if rec.Plain != nil {
				// The video, not the agent's exit, completes the task: it must play, as the next version.
				label, problem := r.pushPlain(rec.Plain.Output, t.Target, rec.Plain.Before)
				if problem != "" {
					return paused(problem)
				}
				if r.opts.Handler == nil {
					for e := r.sendFiles(ctx); e != nil; e = r.sendFiles(ctx) {
						select {
						case <-ctx.Done():
							return leave()
						case <-time.After(time.Second):
						}
					}
				}
				return done(outcome{version: label})
			}
			r.update(rec, func(x *runRecord) { x.Phase = phaseRender })
		case phaseRender:
			r.mu.Lock()
			r.finalTask = t.ID
			r.mu.Unlock()
			// The renderer runs throughout editing. Completion additionally requires a stable final revision belonging
			// to this task, with its ENDLIST verified, not just a successful agent process exit. Comments sent while it
			// renders still go in this version: the agent gets another round.
			if o := at.render(ctx); o != nil {
				if o.state == "" {
					return leave()
				}
				if o.state == "completed" {
					return done(*o)
				}
				return *o
			}
		default:
			return paused("The attempt's record is in an unknown phase")
		}
	}
}

// render waits for the final render of the task's source. nil when the agent got another round; an empty state when
// ctx ended.
func (at *attempt) render(ctx context.Context) *outcome {
	r, t, rec := at.r, at.t, at.rec
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return &outcome{}
		case <-tick.C:
		}
		if at.nextRound(ctx, false) {
			return nil
		}
		source, _, e := fingerprint(r.opts.Root, r.timeline)
		if e != nil {
			return &outcome{state: "paused", message: "Final source is not a valid timeline: " + e.Error(), usage: rec.Usage}
		}
		r.mu.Lock()
		rv, renderErr := r.render, r.renderErr
		r.mu.Unlock()
		if rv.Task == t.ID && rv.Source == source {
			if renderErr != "" {
				return &outcome{state: "paused", message: "Render failed: " + renderErr, usage: rec.Usage}
			}
			if rv.Complete {
				return &outcome{state: "completed", result: rv.ID}
			}
		}
	}
}

// nextRound gives the agent what arrived meanwhile, if anything: the notes it gets in the next round, which the record
// then waits for. With settle, a send the worker hasn't heard of yet gets one more poll to arrive.
func (at *attempt) nextRound(ctx context.Context, settle bool) bool {
	r, rec := at.r, at.rec
	if at.mode != OpenBackground && at.v == nil {
		return false // its session is gone: what came meanwhile waits for the next task
	}
	for {
		ids, rd, err := r.nextRound(ctx, at.mode, at.v, at.more)
		if err != nil {
			r.log("%s: can't give the agent the comments sent meanwhile: %v", filepath.Base(r.opts.Root), err)
			return false
		}
		if rd != nil {
			at.finishSnaps()
			snaps := at.snapshot()
			r.update(rec, func(x *runRecord) {
				x.Phase, x.Round, x.Proc, x.Snaps = phaseAgent, ids, nil, snaps
				x.Prompt, x.Context, x.Report, x.Log = rd.promptPath, rd.contextPath, rd.reportPath, rd.logPath
				for _, e := range rd.edits {
					// Its words changed after the agent did it: what this round says about it replaces the earlier.
					delete(x.Changes, e.ID)
					x.Said = slices.DeleteFunc(x.Said, func(id int64) bool { return id == e.ID })
				}
				at.given(x)
			})
			return true
		}
		if !settle {
			return false
		}
		settle = false
		r.freshPoll(ctx)
	}
}

// given copies what the batch gave the agent into the record: its round count and notes.
func (at *attempt) given(x *runRecord) {
	r := at.r
	x.K = at.more.k
	r.mu.Lock()
	x.Given = slices.Clone(r.batch.given)
	r.mu.Unlock()
	for _, id := range x.Given {
		if !slices.Contains(x.Notes, id) {
			x.Notes = append(x.Notes, id)
		}
	}
}

// background runs the round's process, or (taken back) watches the one an earlier worker started, until it exits. A
// non-nil outcome ends the attempt.
func (at *attempt) background(ctx context.Context) *outcome {
	r, rec := at.r, at.rec
	var proc *agentProc
	_, statErr := os.Stat(rec.Log)
	switch {
	case rec.Proc.alive():
		proc = r.watch(rec.Proc)
	case rec.Proc != nil || statErr == nil:
		// It ran, and ended while no worker watched it: its report says how it went.
	default:
		env := agentEnv(os.Environ(), rec.Context, rec.Report, at.p.Env)
		if rec.Plain != nil {
			env = append(env, "RETAKE_OUTPUT="+rec.Plain.Output)
		}
		p, err := startDetached(at.p.Command, r.opts.Root, append(env, at.askEnv...), rec.Prompt, rec.Log, rec.Log)
		if err != nil {
			return &outcome{state: "paused", message: "Couldn't start the agent: " + err.Error(), usage: rec.Usage}
		}
		proc = p
		r.update(rec, func(x *runRecord) { x.Proc = p }) // a later worker can take it back now
	}
	if proc != nil {
		select {
		case <-proc.done:
		case <-ctx.Done():
			if !stopped(ctx) {
				return &outcome{state: detached}
			}
			proc.stop()
			r.update(rec, func(x *runRecord) { x.Proc = nil })
			return &outcome{state: "stopped", message: "Agent stopped", usage: rec.Usage}
		}
	}
	var exit error
	if proc != nil && proc.own {
		exit = proc.err
		killGroup(proc.PID, proc.Start) // what it left running in its group
	}
	usage, limited := telemetry(tailOf(rec.Log, 128<<10))
	r.update(rec, func(x *runRecord) { x.Proc, x.Usage = nil, addUsage(x.Usage, usage) })
	if limited {
		return &outcome{state: "paused", message: "Usage limit reached. Switch agents or retry after the provider resets your allowance.", usage: rec.Usage}
	}
	if exit != nil {
		return &outcome{state: "paused", message: "Agent stopped without finishing. Check its local task log, then retry or switch agents.", usage: rec.Usage}
	}
	return nil
}

// waitSession waits for the session's report. The session stays open after the report; a person's stop before one
// closes its pane, the worker going away leaves it. A non-nil outcome ends the attempt. Interactive agents report no
// usage: there is no JSON stream to read it from. Comments sent while it works reach the session as one more message
// each time, and move the report to a new file: the record's is the one that counts (spec/automation.md, "Batches").
func (at *attempt) waitSession(ctx context.Context) *outcome {
	r, v, rec, mode := at.r, at.v, at.rec, at.mode
	if v == nil {
		if reported(rec.Report) {
			return nil
		}
		return &outcome{state: "paused", message: "The agent's session ended without a report; working files are preserved"}
	}
	poll := time.NewTicker(viewPoll)
	defer poll.Stop()
	checked, misses, sayErr, moreErr := time.Now(), 0, "", ""
	for {
		select {
		case <-ctx.Done():
			if !stopped(ctx) {
				return &outcome{state: detached}
			}
			v.stop()
			return &outcome{state: "stopped", message: "Agent stopped"}
		case <-poll.C:
		}
		if reported(rec.Report) {
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
		// Comments sent while it works: one more message for the same task, with a new report file.
		if rd, e := r.more(ctx, v, at.more, false); e != nil {
			if e.Error() != moreErr { // tried again at the next look; said once
				r.log("%s: can't give the agent the comments sent meanwhile in %s yet: %v", v.name, mode, e)
				moreErr = e.Error()
			}
		} else if rd != nil {
			r.update(rec, func(x *runRecord) {
				x.Prompt, x.Context, x.Report = rd.promptPath, rd.contextPath, rd.reportPath
				at.given(x)
			})
			if len(rd.ids) > 0 {
				r.log("%s: gave the agent %d more comments (%s) in %s", v.name, len(rd.ids), noteIDs(rd.ids), mode)
			}
			r.logEdits(rd)
		}
		if time.Since(checked) < viewCheck {
			continue
		}
		checked = time.Now()
		if v.alive(ctx) {
			misses = 0
		} else if misses++; misses >= 2 && ctx.Err() == nil && !reported(rec.Report) {
			return &outcome{state: "paused", message: "The agent's session ended without a report; working files are preserved"}
		}
	}
}

// openSession opens p's agent as a session in herdr or tmux, with the task's line. A non-nil outcome ends the task.
func (r *runner) openSession(ctx context.Context, mode string, p Profile, t *project.EditingTask, promptPath string, fresh []project.TaskQuestion) (*view, *outcome) {
	v, e := r.newView(mode, p)
	if e != nil {
		return nil, &outcome{state: "paused", message: e.Error()}
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
		switch {
		case stopped(ctx):
			v.stop()
			return nil, &outcome{state: "stopped", message: "Agent stopped"}
		case ctx.Err() != nil:
			return nil, &outcome{state: detached}
		}
		return nil, &outcome{state: "paused", message: fmt.Sprintf("Couldn't open the agent in %s: %v", mode, e)}
	}
	r.log("%s: %s is working in %s (%s)", v.name, p.Name, mode, v.label())
	return v, nil
}

// readReport reads the agent's report, or says in a sentence why there's none that counts.
func readReport(path string) (agentResult, string) {
	b, e := os.ReadFile(path) //nolint:gosec // generated report path in this task's directory
	if e != nil {
		return agentResult{}, "Agent exited without a completion report or question; working files are preserved"
	}
	var report agentResult
	if len(b) > maxReport || json.Unmarshal(b, &report) != nil || strings.TrimSpace(report.Message) == "" {
		return agentResult{}, "Agent returned an invalid task report"
	}
	return report, ""
}

// addUsage adds a round's usage to the attempt's so far.
func addUsage(total, round *project.AgentUsage) *project.AgentUsage {
	if round == nil {
		return total
	}
	if total == nil {
		clone := *round
		return &clone
	}
	sum := project.AgentUsage{Input: total.Input + round.Input, Output: total.Output + round.Output}
	if total.Cost != nil || round.Cost != nil {
		c := 0.0
		if total.Cost != nil {
			c += *total.Cost
		}
		if round.Cost != nil {
			c += *round.Cost
		}
		sum.Cost = &c
	}
	return &sum
}

func newestPath(vs []family.Version) string {
	if len(vs) == 0 {
		return ""
	}
	return vs[len(vs)-1].Path
}

// reported is whether the agent has written its report: a whole JSON object, not one still being written.
func reported(path string) bool {
	b, e := os.ReadFile(path) //nolint:gosec // generated report path in this task's directory
	var report agentResult
	return e == nil && len(b) <= maxReport && json.Unmarshal(b, &report) == nil
}

// docPrompt is the task prompt of a task whose notes are all typed comments on an extension's version: the
// extension's section says what to do with its documents.
func docPrompt(contextPath, reportPath, extra string) string {
	return fmt.Sprintf(`You are working on the documents of a Retake video in the current working directory (its folder).
Read the project instructions, then the durable task context at %q. Only the note IDs in task.notes are assigned (minus task.done, finished in an earlier attempt); each is a typed comment, and its "anchor" says which file and place it is about. Other notes are context.
Make the requested changes to the files the notes are about, as the section below says. Don't render, push or change any video version. Preserve other people's work.
The next agent may differ from you; leave useful context on disk. Do not delete or edit the worker's identity, lock, logs, context files, ask.json or anything under .retake/.
`, promptPath(contextPath)) + extra + askRules(reportPath, "A completed report finishes these comments with your message as their change.")
}

// timelinePrompt is the task prompt of a project with a native timeline. extra is the extensions' section.
func timelinePrompt(contextPath, timeline, reportPath, extra string) string {
	return fmt.Sprintf(`You are editing a native Retake video project in the current working directory.
Read the project instructions, then the durable task context at %q. The native timeline is %q.
Only the note IDs in task.notes are assigned (minus task.done, finished in an earlier attempt). Other notes are context: check them and prior events for conflicting or already-applied feedback. Original note versions, timing, strokes and targets must be respected. Saved annotated stills, when available, are note-<id>.png in this task directory. Attached reference images are note-<id>-<file>; their boxes and original video times are in the context. A note's "missing" says which of its files couldn't be given to you and what you have instead.
Original local video revisions are retained in .retake/worker/renders/: each state.json gives its label (a draft such as v4-draft2 until a task completes it as v4), source.json is its timeline snapshot, and index.m3u8 is its video. Inspect the original frame there when needed; do not assume an old comment's time refers to the current source.
Make the requested source edits in this project. Preserve all existing working changes, media and other people's work. Retake automatically renders valid saved source changes and streams them to reviewers. Do not run retake push or retake note yourself.
The next agent may differ from you; leave source changes and useful context on disk. Do not delete or edit the worker's identity, lock, logs, context files, ask.json or render output.
`, promptPath(contextPath), promptPath(timeline)) + extra + askRules(reportPath, "A completed report is only a request: Retake waits for a checked playable render before resolving the comment.")
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

// promptPath is a path in a prompt. %q quotes it as Go does, except on Windows, where a path has no quotes or control
// characters to escape: there it's only put in double quotes, so the agent reads C:\Users\… with single backslashes.
type promptPath string

// windowsQuote is whether %q of a promptPath quotes it the Windows way.
var windowsQuote = runtime.GOOS == "windows"

func (p promptPath) Format(f fmt.State, verb rune) {
	s := string(p)
	if verb == 'q' {
		if windowsQuote && !strings.ContainsFunc(s, func(r rune) bool { return r == '"' || !unicode.IsPrint(r) }) {
			s = `"` + s + `"`
		} else {
			s = strconv.Quote(s)
		}
	}
	_, _ = io.WriteString(f, s)
}
