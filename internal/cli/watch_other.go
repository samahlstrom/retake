//go:build !unix && !windows

package cli

import (
	"context"
	"os"
	"os/exec"
)

// ownGroup does nothing here: there are no process groups to signal.
func ownGroup(*exec.Cmd) {}

// interrupt stops the command; without SIGINT to send, it kills it.
func interrupt(p *os.Process) error { return p.Kill() }

// killGroup does nothing here: interrupt already killed the command.
func killGroup(*os.Process) {}

// shell returns the command that runs line through the shell.
func shell(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", line) //nolint:gosec // running the user's --exec is the point
}
