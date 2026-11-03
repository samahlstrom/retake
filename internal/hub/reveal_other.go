//go:build !windows

package hub

import "os/exec"

// explorerLine: explorer is Windows' (RevealDefault only runs it there).
func explorerLine(*exec.Cmd, string) {}
