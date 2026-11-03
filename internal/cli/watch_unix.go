//go:build unix

package cli

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// ownGroup runs the command in a process group of its own, so stopping it reaches everything it started.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// interrupt sends the command's process group SIGINT, as Ctrl+C would.
func interrupt(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGINT) }

// killGroup kills whatever is left of the command's process group.
func killGroup(p *os.Process) { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }

// shell returns the command that runs line through the shell.
func shell(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "/bin/sh", "-c", line) //nolint:gosec // running the user's --exec is the point
}
