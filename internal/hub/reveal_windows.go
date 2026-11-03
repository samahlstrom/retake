//go:build windows

package hub

import (
	"os/exec"
	"syscall"
)

// explorerLine gives explorer its command line as it parses it, `explorer.exe /select,"<path>"`: Go would quote the
// whole `/select,<path>` argument when the path has a space, and explorer then opens Documents instead.
func explorerLine(cmd *exec.Cmd, path string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
}
