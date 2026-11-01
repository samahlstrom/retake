//go:build unix

package worker

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func lockProject(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed worker lock within the selected project
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("a worker already owns this project: %w", e)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
func ownGroup(c *exec.Cmd)          { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func stopGroup(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGTERM) }
func killGroup(p *os.Process)       { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }
