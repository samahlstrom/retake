package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/samahlstrom/retake/internal/agent"
)

// DefaultDebounce is how long watch waits for things to settle after notes land before it runs the command.
const DefaultDebounce = 2 * time.Second

// stopGrace is how long a running command gets to exit after an interrupt before it is killed.
const stopGrace = 5 * time.Second

// watchCmd is `retake watch <video> --exec CMD`: every time notes turn open or answers land, it waits until things
// have been quiet for the debounce time, then runs CMD through the shell with RETAKE_VIDEO, RETAKE_PROJECT and
// RETAKE_SINCE (the cursor before what triggered it) set. Runs never overlap: what lands meanwhile triggers the next
// run once this one ends. Ctrl+C (ctx) interrupts a running command and stops.
func watchCmd(ctx context.Context, args []string, env Env) int {
	var command, since, debounce string
	f := flags{values: map[string]*string{"--exec": &command, "--since": &since, "--debounce": &debounce}}
	pos, help, err := f.parse(args)
	if help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	if err == nil && len(pos) != 1 {
		err = errors.New("give one video")
	}
	if err == nil && command == "" {
		err = errors.New(`--exec is required, e.g. --exec "claude -p 'handle the new review notes'"`)
	}
	var quiet time.Duration
	if err == nil {
		quiet, err = parseSeconds("--debounce", debounce)
		if debounce == "" {
			quiet = DefaultDebounce
		}
	}
	var cursor int64
	if err == nil && since != "" {
		cursor, err = parseSince(since)
	}
	if err != nil {
		return usageError(env, "watch", err)
	}
	p, code := openProject(env, "watch", pos[0])
	if p == nil {
		return code
	}
	defer func() { _ = p.Close() }()
	if since == "" {
		if cursor, err = p.Store.LastSeq(); err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake watch: %v\n", err)
			return ExitError
		}
	}
	_, _ = fmt.Fprintf(env.Stderr, "retake watch: waiting for notes on %s after cursor %d (Ctrl+C stops)\n", p.Video, cursor)
	for {
		res, err := p.NotesWait(ctx, agent.WaitArgs{Since: cursor})
		if ctx.Err() != nil {
			return ExitOK
		}
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake watch: %v\n", err)
			return ExitError
		}
		notes, answers := len(res.Notes), len(res.Answers)
		next := res.Cursor
		for quiet > 0 { // let a burst settle: more takes finishing, a rebuild after the answer
			more, err := p.NotesWait(ctx, agent.WaitArgs{Since: next, Timeout: quiet})
			if ctx.Err() != nil {
				return ExitOK
			}
			if err != nil {
				_, _ = fmt.Fprintf(env.Stderr, "retake watch: %v\n", err)
				return ExitError
			}
			next = more.Cursor
			if more.TimedOut {
				break
			}
			notes, answers = notes+len(more.Notes), answers+len(more.Answers)
		}
		_, _ = fmt.Fprintf(env.Stderr, "retake watch: %s, %s after cursor %d: running %s\n", plural(notes, "note"),
			plural(answers, "answer"), cursor, command)
		if !runHook(ctx, env, p, command, cursor) {
			return ExitOK
		}
		cursor = next
	}
}

// runHook runs the command once and waits for it. It returns false when ctx ended (the watch is stopping).
func runHook(ctx context.Context, env Env, p *agent.Project, command string, since int64) bool {
	cmd := shell(ctx, command)
	cmd.Env = append(os.Environ(), "RETAKE_VIDEO="+p.Video, "RETAKE_PROJECT="+p.Root,
		"RETAKE_SINCE="+strconv.FormatInt(since, 10))
	cmd.Stdout, cmd.Stderr = env.Stdout, env.Stderr
	ownGroup(cmd)
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = stopGrace
	err := cmd.Run()
	if ctx.Err() != nil {
		if cmd.Process != nil {
			killGroup(cmd.Process) // anything that ignored the interrupt
		}
		_, _ = fmt.Fprintln(env.Stderr, "retake watch: stopped")
		return false
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		_, _ = fmt.Fprintf(env.Stderr, "retake watch: the command exited with status %d; still watching\n", exit.ExitCode())
	case err != nil:
		_, _ = fmt.Fprintf(env.Stderr, "retake watch: running the command: %v; still watching\n", err)
	}
	return true
}
