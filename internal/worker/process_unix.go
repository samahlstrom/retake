//go:build unix

package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func lockProject(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed worker lock within the selected project
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %w", ErrBusy, e)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// detach starts c in a session and process group of its own, with no parent-death signal: the agent outlives the
// worker (spec/automation.md, "Agents outlive Retake"), and the group is how a person's stop ends it with its children.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// startOwn starts c detached (detach). It returns c.
func startOwn(c *exec.Cmd) (*exec.Cmd, error) {
	detach(c)
	return c, c.Start()
}

// track does nothing here: the process group detach made is the agent's group.
func track(int, string) {}

// withSelfOnPath is env as it is: retake's own folder is on an agent's PATH as the person set it up.
func withSelfOnPath(env []string) []string { return env }

// sessionEnv is the variables a herdr or tmux session gets on creation: the profile's own, as they are.
func sessionEnv(profile []string) []string { return profile }

func stopGroup(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }

// killGroup kills what is left of the process group led by pid. The group outlives its leader, and its id isn't
// reused while it has members, so start isn't needed here.
func killGroup(pid int, _ string) { _ = syscall.Kill(-pid, syscall.SIGKILL) }

// procStart is when process pid started, as the system says it, so a reused pid is never taken for it: on Linux the
// boot's id and its start in clock ticks after boot (/proc/<pid>/stat), elsewhere ps's lstart. "" when there is no such
// process, or it has exited (a zombie).
func procStart(pid int) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err == nil {
		s := string(b)
		// The name in parentheses may hold spaces and parentheses: the fields start after the last ')'.
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			return ""
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 20 || f[0] == "Z" || f[0] == "X" {
			return ""
		}
		boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
		return strings.TrimSpace(string(boot)) + ":" + f[19] // field 22, starttime
	}
	if runtime.GOOS == "linux" {
		return "" // no /proc entry: it's gone
	}
	out, err := exec.Command("ps", "-o", "stat=,lstart=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // ps with a number
	if err != nil {
		return ""
	}
	stat, start, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
	if strings.HasPrefix(stat, "Z") {
		return ""
	}
	return strings.Join(strings.Fields(start), " ")
}

// endGroup stops the process group led by pid, the process that started at start: SIGTERM, then SIGKILL after a few
// seconds. It does nothing when pid is another process now.
func endGroup(pid int, start string) {
	if start == "" || procStart(pid) != start {
		return
	}
	if errors.Is(stopGroup(pid), syscall.ESRCH) {
		return
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if procStart(pid) != start {
			killGroup(pid, start) // what it left in its group
			return
		}
	}
	killGroup(pid, start)
}
