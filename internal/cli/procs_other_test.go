//go:build !unix

package cli

import "os"

// killPID kills a fake program a test started (by its pid, from the file it wrote).
func killPID(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// pidAlive can't tell here without the Windows API. The tests that ask run fake programs that are POSIX shell
// scripts, so they skip on Windows (testmedia.NeedShell) before they get here.
func pidAlive(int) bool { return false }
