//go:build windows

package worker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/samahlstrom/retake/internal/desktop"
)

// Windows has no process groups to signal and no setsid. An agent that outlives Retake (spec/automation.md, "Agents
// outlive Retake") runs with a hidden console of its own (so Ctrl+C or closing Retake's console doesn't reach it),
// outside any job Retake runs in when that job allows it, and in a named job object of its own: the job is its
// "group", which a person's stop ends with every process the agent started. The job isn't killed when its last handle
// closes, so it lives as long as its processes, and a later worker finds it again by its name.

const stillActive = 259 // GetExitCodeProcess of a running process

var procOpenJobObject = windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW")

// lockProject takes the worker's lock on the project: an exclusive LockFileEx, released when the process ends.
func lockProject(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // fixed worker lock within the selected project
	if e != nil {
		return nil, e
	}
	ol := new(windows.Overlapped)
	h := windows.Handle(f.Fd())
	if e = windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol); e != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %w", ErrBusy, e)
	}
	return func() { _ = windows.UnlockFileEx(h, 0, 1, 0, ol); _ = f.Close() }, nil
}

const detachFlags = windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW

// startOwn starts c detached: a hidden console and process group of its own, and out of the job Retake runs in. A job
// that doesn't allow that refuses the start; c then starts in it (as a copy, since a Cmd starts once).
func startOwn(c *exec.Cmd) (*exec.Cmd, error) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachFlags | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := c.Start()
	if err == nil || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return c, err
	}
	again := exec.Command(c.Path, c.Args[1:]...) //nolint:gosec // the same trusted argv
	again.Dir, again.Env, again.Stdin, again.Stdout, again.Stderr = c.Dir, c.Env, c.Stdin, c.Stdout, c.Stderr
	again.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachFlags}
	return again, again.Start()
}

// jobs are the job objects of the agents this process started, by name: the handle keeps the job until killGroup or
// endGroup is done with it.
var jobs sync.Map

func jobName(pid int, start string) string { return "retake-agent-" + strconv.Itoa(pid) + "-" + start }

// track puts the agent pid, which started at start, in a job object of its own. Its children join it as they start.
// Without a job (it can't be made), stopping it falls back to taskkill /T.
func track(pid int, start string) {
	if start == "" {
		return
	}
	name, err := windows.UTF16PtrFromString(jobName(pid, start))
	if err != nil {
		return
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		return
	}
	p, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		_ = windows.CloseHandle(job)
		return
	}
	defer func() { _ = windows.CloseHandle(p) }()
	if windows.AssignProcessToJobObject(job, p) != nil {
		_ = windows.CloseHandle(job)
		return
	}
	jobs.Store(jobName(pid, start), job)
}

// openJob is the job of the agent pid that started at start: this process's handle, else the named job a worker
// before it made. 0 when there's none.
func openJob(pid int, start string) (windows.Handle, bool) {
	if h, ok := jobs.LoadAndDelete(jobName(pid, start)); ok {
		return h.(windows.Handle), true
	}
	name, err := windows.UTF16PtrFromString(jobName(pid, start))
	if err != nil {
		return 0, false
	}
	// JOB_OBJECT_TERMINATE | JOB_OBJECT_QUERY; the name is NUL-terminated UTF-16 for the Win32 call.
	const access = 0x0008 | 0x0004
	r, _, _ := procOpenJobObject.Call(access, 0, uintptr(unsafe.Pointer(name))) //nolint:gosec // see above
	return windows.Handle(r), r != 0
}

// killGroup kills what is left of the agent pid that started at start: every process in its job (its children, after
// it has exited itself). Without its job, the process tree of pid when it's still that process. Nothing when start is
// unknown: a pid alone may be another program's by now.
func killGroup(pid int, start string) {
	if start == "" {
		return
	}
	if job, ok := openJob(pid, start); ok {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
		return
	}
	if procStart(pid) == start {
		taskkill(pid)
	}
}

// taskkill ends the process tree of pid at once. Windows has no SIGTERM for a console program in another console.
func taskkill(pid int) {
	cmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)) //nolint:gosec // taskkill with a number
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	_ = cmd.Run()
}

// procStart is when process pid started, as Windows says it (its creation time, in 100 ns since 1601), so a reused
// pid is never taken for it. "" when there's no such process, it has exited, or it isn't this user's to look at.
func procStart(pid int) string {
	if pid <= 0 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code != stillActive {
		return ""
	}
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return ""
	}
	return strconv.FormatUint(uint64(created.HighDateTime)<<32|uint64(created.LowDateTime), 10)
}

// endGroup stops the agent pid that started at start, with everything it started, and waits up to a few seconds for
// it to go. It does nothing when pid is another process now (and lets go of the agent's job then).
func endGroup(pid int, start string) {
	if start == "" || procStart(pid) != start {
		if job, ok := jobs.LoadAndDelete(jobName(pid, start)); ok {
			_ = windows.CloseHandle(job.(windows.Handle))
		}
		return
	}
	killGroup(pid, start)
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if procStart(pid) != start {
			return
		}
	}
}

// sessionEnv is the variables a herdr session gets on creation: the profile's own, and, when retake's folder isn't on
// PATH and the profile doesn't set PATH, this process's PATH with retake's folder first (withSelfOnPath), so the
// agent's `retake ask` there is this program.
func sessionEnv(profile []string) []string {
	for _, kv := range profile {
		if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			return profile
		}
	}
	own := []string{"PATH=" + os.Getenv("PATH")}
	if with := withSelfOnPath(own); with[len(with)-1] != own[0] {
		return append(profile, with[len(with)-1])
	}
	return profile
}

// withSelfOnPath puts retake's own folder first on env's PATH, so the `retake ask`, `retake working` and `retake
// done` an agent runs (in PowerShell, cmd or Git Bash) are this program, with no quoting of a path with spaces.
func withSelfOnPath(env []string) []string {
	exe := desktop.Exe()
	if exe == "" {
		return env
	}
	dir := filepath.Dir(exe)
	path := ""
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); strings.EqualFold(k, "PATH") {
			path = v // the last one counts, as for the program that gets env
		}
	}
	for _, d := range filepath.SplitList(path) {
		if strings.EqualFold(filepath.Clean(d), filepath.Clean(dir)) {
			return env
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !strings.EqualFold(k, "PATH") {
			out = append(out, kv)
		}
	}
	if path == "" {
		return append(out, "PATH="+dir)
	}
	return append(out, "PATH="+dir+string(os.PathListSeparator)+path)
}
