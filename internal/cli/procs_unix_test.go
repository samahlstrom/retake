//go:build unix

package cli

import "syscall"

// killPID kills a fake program a test started (by its pid, from the file it wrote).
func killPID(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// pidAlive reports whether a process with that pid is still there.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }
