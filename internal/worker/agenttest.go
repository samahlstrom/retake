package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The agent test of the setup page and Settings (spec/setup.md, "The agent test"): run an agent once with a
// one-line prompt, so a missing install or sign-in shows before the first real edit. It's cheap: one reply, Claude
// Code on its smallest model with no tools.

// TryPrompt is what the test asks.
const TryPrompt = "Reply with the single word: ready"

// TryTimeout is how long the test may take.
var TryTimeout = 90 * time.Second

// TryResult is the test's answer.
type TryResult struct {
	OK      bool    `json:"ok"`
	Reply   string  `json:"reply,omitempty"`
	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
}

// TryAgent runs p once with TryPrompt in a new empty folder and says whether it answered.
func TryAgent(ctx context.Context, p Profile) TryResult {
	start := time.Now()
	res := tryAgent(ctx, p)
	res.Seconds = float64(time.Since(start).Round(100*time.Millisecond)) / float64(time.Second)
	return res
}

func tryAgent(ctx context.Context, p Profile) TryResult {
	if len(p.Command) == 0 {
		return TryResult{Error: "This agent has no command."}
	}
	dir, err := os.MkdirTemp("", "retake-agent-test-")
	if err != nil {
		return TryResult{Error: err.Error()}
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := p.Command[0]
	kind := filepath.Base(bin)
	var args []string
	var stdin string
	reply := filepath.Join(dir, "reply.txt")
	switch kind {
	case "claude":
		args = []string{"-p", "--model", "haiku", "--tools", "", "--no-session-persistence", "--output-format", "json", TryPrompt}
	case "codex":
		args = []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "--color", "never",
			"-o", reply, TryPrompt}
	default:
		args, stdin = p.Command[1:], TryPrompt+"\n"
	}
	ctx, cancel := context.WithTimeout(ctx, TryTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // the person's own agent, with a fixed prompt
	cmd.Dir = dir
	cmd.Env = append(agentEnv(os.Environ(), filepath.Join(dir, "context.json"), filepath.Join(dir, "report.json"), p.Env),
		"RETAKE_AGENT_TEST=1")
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = 2 * time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	text := out.String() + "\n" + errOut.String()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return TryResult{Error: fmt.Sprintf("No answer in %d s.", int(TryTimeout.Seconds()))}
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
		return TryResult{Error: kindName(kind) + " isn't installed here (" + bin + " not found)."}
	}
	if kind == "claude" {
		var r struct {
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if json.Unmarshal(lastJSON(out.Bytes()), &r) == nil && !r.IsError && err == nil {
			return TryResult{OK: true, Reply: firstLine(r.Result)}
		}
		if r.Result != "" {
			text = r.Result + "\n" + text
		}
	}
	if err == nil {
		got := out.String()
		if kind == "codex" {
			if b, e := os.ReadFile(reply); e == nil { //nolint:gosec // our own temporary folder
				got = string(b)
			}
		}
		if s := firstLine(got); s != "" || kind != "codex" {
			return TryResult{OK: true, Reply: s}
		}
	}
	if signInProblem(text) {
		return TryResult{Error: "Not signed in: run " + kind + " once in Terminal and sign in."}
	}
	if s := lastLine(text); s != "" {
		return TryResult{Error: s}
	}
	if err != nil {
		return TryResult{Error: err.Error()}
	}
	return TryResult{Error: "It answered nothing."}
}

func kindName(kind string) string {
	switch kind {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	}
	return kind
}

// signInProblem reports whether an agent's output says it isn't signed in.
func signInProblem(s string) bool {
	s = strings.ToLower(s)
	for _, w := range []string{"/login", "not logged in", "log in", "login", "sign in", "signed in", "api key",
		"authentication", "unauthorized", "401"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// lastJSON is the last line of out that is a JSON object (Claude Code's json output is one line).
func lastJSON(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := bytes.TrimSpace(lines[i]); len(l) > 0 && l[0] == '{' {
			return l
		}
	}
	return nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return s
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return clip(l)
		}
	}
	return ""
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return clip(l)
		}
	}
	return ""
}
