package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/desktop"
)

// Where an agent runs (spec/automation.md, "Watching the agents"). In herdr and tmux it is an interactive session the
// person can watch and type into; completion still comes only from its report and a verified render.
const (
	OpenBackground = "background"
	OpenHerdr      = "herdr"
	OpenTmux       = "tmux"
	tmuxSession    = "retake"
)

// Intervals of an interactive task: how often the report file is read, how often the session is checked, how long a
// herdr agent may take to start, and how long it may take to start working on a line it was given.
var (
	viewPoll  = 500 * time.Millisecond
	viewCheck = 2 * time.Second
	herdrTake = 15 * time.Second
)

const herdrStartTimeout = 60 * time.Second

// viewState is a project's agent session, kept in .retake/worker/view.json so the next task reuses it.
type viewState struct {
	Mode      string `json:"mode"`
	Profile   string `json:"profile"`
	Pane      string `json:"pane"`
	Workspace string `json:"workspace,omitempty"` // herdr
	Agent     string `json:"agent,omitempty"`     // herdr's name for the agent
}

// view runs one project's agent in herdr or tmux. The project folder's name names its session.
type view struct {
	mode, bin, root, name, path string
	env                         []string // the profile's own variables, K=V, given to a new session
	state                       viewState
	log                         func(string, ...any)
}

// cliError is a failed herdr or tmux command: herdr's error code and message, or the command's stderr.
type cliError struct{ code, message string }

func (e *cliError) Error() string { return e.message }

func errCode(e error) string {
	var c *cliError
	if errors.As(e, &c) {
		return c.code
	}
	return ""
}

// openIn is where profile p runs: its OpenIn, or the background when that's unset or p has no interactive command.
func (r *runner) openIn(p Profile) string {
	if p.OpenIn != OpenHerdr && p.OpenIn != OpenTmux {
		return OpenBackground
	}
	if len(p.Interactive) == 0 {
		r.mu.Lock()
		warned := r.warned[p.ID]
		if r.warned == nil {
			r.warned = map[string]bool{}
		}
		r.warned[p.ID] = true
		r.mu.Unlock()
		if !warned {
			r.log("Agent %s has no interactive command, so it runs in the background, not in %s", p.ID, p.OpenIn)
		}
		return OpenBackground
	}
	return p.OpenIn
}

// findView finds herdr or tmux: the copy next to the retake binary, else the one on PATH.
var findView = desktop.Find

func (r *runner) newView(mode string, p Profile) (*view, error) {
	bin, _, e := findView(mode) // herdr that came with retake first (spec/setup.md)
	if e != nil {
		return nil, fmt.Errorf("%s isn't installed, but this agent is set to open in it", mode)
	}
	v := &view{mode: mode, bin: bin, root: r.opts.Root, name: filepath.Base(r.opts.Root), path: filepath.Join(r.dir, "view.json"),
		env: profileEnv(os.Environ(), p.Env), log: r.log}
	b, e := os.ReadFile(v.path)
	if e == nil && json.Unmarshal(b, &v.state) == nil && v.state.Mode != mode {
		v.state = viewState{} // the person changed where agents open: start afresh there
	}
	return v, nil
}

func (v *view) save() {
	v.state.Mode = v.mode
	if e := writeJSON(v.path, v.state); e != nil {
		v.log("%s: can't save the agent session: %v", v.name, e)
	}
}

// label is the session's name in herdr or tmux.
func (v *view) label() string {
	if v.mode == OpenTmux {
		return tmuxSession + ":" + v.name
	}
	return "retake · " + v.name
}

// cli runs herdr or tmux and returns its standard output.
func (v *view) cli(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, v.bin, args...) //nolint:gosec // herdr or tmux with arguments built here
	// Retake's own variables stay out, and so does the herdr pane retake was started from: every target is explicit.
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return reservedEnv(k) || k == "HERDR_PANE_ID" || k == "HERDR_TAB_ID" || k == "HERDR_WORKSPACE_ID"
	})
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if e := cmd.Run(); e != nil {
		var reply struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		msg := strings.TrimSpace(stderr.String())
		if json.Unmarshal(stderr.Bytes(), &reply) == nil && reply.Error.Message != "" {
			return nil, &cliError{reply.Error.Code, reply.Error.Message}
		}
		if msg == "" {
			msg = e.Error()
		}
		return nil, &cliError{"", msg}
	}
	return out.Bytes(), nil
}

// start opens the project's session, or reuses it, with p's agent, and gives the agent line.
func (v *view) start(ctx context.Context, p Profile, line string) error {
	if v.mode == OpenTmux {
		return v.startTmux(ctx, p, line)
	}
	return v.startHerdr(ctx, p, line)
}

// alive reports whether the agent's session is still there.
func (v *view) alive(ctx context.Context) bool {
	if v.mode == OpenTmux {
		dead, ok := v.tmuxPane(ctx, v.state.Pane)
		return ok && !dead
	}
	return v.herdrAgent(ctx) == nil
}

// stop closes the agent's pane, which ends the agent.
func (v *view) stop() {
	if v.state.Pane == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var e error
	if v.mode == OpenTmux {
		_, e = v.cli(ctx, 0, "kill-pane", "-t", v.state.Pane)
	} else {
		_, e = v.cli(ctx, 0, "pane", "close", v.state.Pane)
	}
	if e != nil {
		v.log("%s: closing the agent's pane: %v", v.name, e)
	}
	v.state.Pane = ""
	v.save()
}

// herdr: one workspace per project, its agent started with herdr agent start and given work with herdr agent prompt.

func (v *view) startHerdr(ctx context.Context, p Profile, line string) error {
	s := &v.state
	if s.Pane != "" && v.herdrExists(ctx, "pane", s.Pane) {
		if s.Profile == p.ID {
			if v.herdrAgent(ctx) == nil {
				return v.herdrPrompt(ctx, line)
			}
			if v.herdrStart(ctx, p) == nil {
				return v.herdrPrompt(ctx, line)
			}
			// The pane is busy with something else now: leave it to the person and open another.
		} else {
			v.stop() // a different agent replaces this one
		}
	}
	args := []string{"--cwd", v.root, "--label", v.label(), "--no-focus"}
	for _, kv := range v.env {
		args = append(args, "--env", kv)
	}
	var out []byte
	var e error
	if s.Workspace != "" && v.herdrExists(ctx, "workspace", s.Workspace) {
		out, e = v.cli(ctx, 30*time.Second, append([]string{"tab", "create", "--workspace", s.Workspace}, args...)...)
	} else {
		out, e = v.cli(ctx, 30*time.Second, append([]string{"workspace", "create"}, args...)...)
	}
	if e != nil {
		return e
	}
	var reply struct {
		Result struct {
			Pane struct {
				ID        string `json:"pane_id"`
				Workspace string `json:"workspace_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &reply) != nil || reply.Result.Pane.ID == "" {
		return fmt.Errorf("herdr didn't say which pane it opened: %s", bytes.TrimSpace(out))
	}
	*s = viewState{Profile: p.ID, Pane: reply.Result.Pane.ID, Workspace: reply.Result.Pane.Workspace, Agent: herdrName(v.root)}
	v.save()
	if e = v.herdrStart(ctx, p); e != nil {
		return e
	}
	return v.herdrPrompt(ctx, line)
}

func (v *view) herdrExists(ctx context.Context, kind, id string) bool {
	_, e := v.cli(ctx, 10*time.Second, kind, "get", id)
	return e == nil
}

// herdrAgent checks that the project's agent is running in its pane.
func (v *view) herdrAgent(ctx context.Context) error {
	out, e := v.cli(ctx, 10*time.Second, "agent", "get", v.state.Agent)
	if e != nil {
		return e
	}
	var reply struct {
		Result struct {
			Agent struct {
				Pane string `json:"pane_id"`
			} `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &reply) != nil || reply.Result.Agent.Pane != v.state.Pane {
		return errors.New("the agent isn't in its pane")
	}
	return nil
}

// herdrStart starts p's interactive agent in the project's pane. When the agent stops at a startup question, it
// waits for the person to answer it there.
func (v *view) herdrStart(ctx context.Context, p Profile) error {
	args := []string{"agent", "start", v.state.Agent, "--kind", filepath.Base(p.Interactive[0]), "--pane", v.state.Pane,
		"--timeout", fmt.Sprint(herdrStartTimeout.Milliseconds())}
	if len(p.Interactive) > 1 {
		args = append(append(args, "--"), p.Interactive[1:]...)
	}
	_, e := v.cli(ctx, herdrStartTimeout+10*time.Second, args...)
	if errCode(e) == "agent_not_ready" {
		v.log("%s: the agent is waiting for you in herdr (%s)", v.name, v.label())
		_, e = v.cli(ctx, 0, "agent", "wait", v.state.Agent, "--until", "idle", "--until", "done")
	}
	return e
}

// herdrPrompt gives the agent its line and checks that it took it. An agent that has just come past a startup question
// (Claude Code's "trust this folder?") can still be drawing its prompt box, and a line typed then is lost: the agent
// sits idle with an empty box. So a line the agent doesn't start working on is sent once more.
func (v *view) herdrPrompt(ctx context.Context, line string) error {
	for try := 1; ; try++ {
		_, e := v.cli(ctx, 30*time.Second, "agent", "prompt", v.state.Agent, line)
		if errCode(e) == "agent_blocked" {
			return fmt.Errorf("the agent is waiting for an answer in herdr (%s); answer it there, then retry", v.label())
		}
		if e != nil {
			return e
		}
		_, e = v.cli(ctx, herdrTake+10*time.Second, "agent", "wait", v.state.Agent, "--until", "working", "--until",
			"blocked", "--until", "done", "--timeout", fmt.Sprint(herdrTake.Milliseconds()))
		switch {
		case e == nil:
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case try == 2:
			return fmt.Errorf("the agent in herdr (%s) didn't start on its line: %w", v.label(), e)
		}
		v.log("%s: the agent in herdr (%s) didn't start on its line; sending it again", v.name, v.label())
	}
}

// herdrName is the agent's name in herdr: unique per project folder, and within herdr's [a-z][a-z0-9_-]{0,31}.
func herdrName(root string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(filepath.Base(root)) {
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
			b.WriteRune(c)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	slug := b.String()
	if len(slug) > 19 {
		slug = slug[:19]
	}
	if slug = strings.TrimRight(slug, "-"); slug != "" {
		slug += "-"
	}
	sum := sha256.Sum256([]byte(root))
	return "retake-" + slug + hex.EncodeToString(sum[:2])
}

// tmux: one window per project in the session "retake", running the agent with the line as its last argument.

func (v *view) startTmux(ctx context.Context, p Profile, line string) error {
	s := &v.state
	argv := append(slices.Clone(p.Interactive), line)
	var env []string
	for _, kv := range v.env {
		env = append(env, "-e", kv)
	}
	if s.Pane != "" {
		if dead, ok := v.tmuxPane(ctx, s.Pane); ok {
			if s.Profile == p.ID && !dead {
				return v.typeTmux(ctx, line) // the agent is still running: type the line into it
			}
			args := append(append([]string{"respawn-pane", "-k", "-t", s.Pane, "-c", v.root}, env...), "--")
			if _, e := v.cli(ctx, 10*time.Second, append(args, argv...)...); e != nil {
				return e
			}
			s.Profile = p.ID
			v.save()
			return nil
		}
	}
	create := func(args ...string) ([]byte, error) {
		args = append(append(append(args, "-P", "-F", "#{pane_id}", "-n", v.name, "-c", v.root), env...), "--")
		return v.cli(ctx, 10*time.Second, append(args, argv...)...)
	}
	window := func() ([]byte, error) { return create("new-window", "-d", "-t", "="+tmuxSession+":") }
	var out []byte
	var e error
	if _, has := v.cli(ctx, 10*time.Second, "has-session", "-t", "="+tmuxSession); has == nil {
		out, e = window()
	} else if out, e = create("new-session", "-d", "-s", tmuxSession); e != nil {
		// Another project's worker made the session just now.
		if o, e2 := window(); e2 == nil {
			out, e = o, nil
		}
	}
	if e != nil {
		return e
	}
	pane := string(bytes.TrimSpace(out))
	if !strings.HasPrefix(pane, "%") {
		return fmt.Errorf("tmux didn't say which pane it opened: %s", pane)
	}
	*s = viewState{Profile: p.ID, Pane: pane}
	v.save()
	v.log("%s: opened %s in tmux (%s). If it stops at a first-run question (trust this folder, bypass permissions mode), answer it there: tmux attach -t %s", v.name, filepath.Base(p.Interactive[0]), v.label(), tmuxSession)
	// Keep the pane, and what the agent printed, if the agent quits.
	if _, e = v.cli(ctx, 10*time.Second, "set-option", "-w", "-t", pane, "remain-on-exit", "on"); e != nil {
		v.log("%s: tmux remain-on-exit: %v", v.name, e)
	}
	return nil
}

// typeTmux types line into the running agent's pane, then Enter as its own keystroke.
func (v *view) typeTmux(ctx context.Context, line string) error {
	if _, e := v.cli(ctx, 10*time.Second, "send-keys", "-t", v.state.Pane, "-l", line); e != nil {
		return e
	}
	time.Sleep(300 * time.Millisecond) // a separate keystroke, not part of the typed text
	_, e := v.cli(ctx, 10*time.Second, "send-keys", "-t", v.state.Pane, "Enter")
	return e
}

// say gives the running agent a new message, as a task's line is given: herdr agent prompt, or typed into its tmux
// pane.
func (v *view) say(ctx context.Context, line string) error {
	if v.mode == OpenTmux {
		return v.typeTmux(ctx, line)
	}
	return v.herdrPrompt(ctx, line)
}

// tmuxPane finds a pane in the tmux server: whether it's there, and whether its program has exited.
func (v *view) tmuxPane(ctx context.Context, pane string) (dead, ok bool) {
	out, e := v.cli(ctx, 10*time.Second, "list-panes", "-a", "-F", "#{pane_id} #{pane_dead}")
	if e != nil || pane == "" {
		return false, false
	}
	for l := range strings.Lines(string(out)) {
		if id, d, _ := strings.Cut(strings.TrimSpace(l), " "); id == pane {
			return d == "1", true
		}
	}
	return false, false
}
