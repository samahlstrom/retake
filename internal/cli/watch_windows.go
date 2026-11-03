//go:build windows

package cli

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// shell returns the command that runs line through cmd.exe exactly as it was typed: `cmd.exe /S /C "<line>"` (with
// /S, cmd takes everything between the first and the last quote as it is), not re-quoted the way Go quotes arguments.
func shell(ctx context.Context, line string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + line + `"`}
	return cmd
}

// ownGroup does nothing here: the command stays in the console's group, so Ctrl+C reaches it as it reaches retake.
func ownGroup(*exec.Cmd) {}

// interrupt stops the command and everything it started (Windows has no SIGINT to send another process).
func interrupt(p *os.Process) error {
	if err := killTree(p); err != nil {
		return p.Kill()
	}
	return nil
}

// killGroup kills whatever is left of the command's tree.
func killGroup(p *os.Process) { _ = killTree(p) }

func killTree(p *os.Process) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run() //nolint:gosec // a pid
}
