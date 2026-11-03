//go:build unix

package desktop

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in its own session, with no controlling terminal: an interactive shell can't stop on the tty.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
