//go:build windows

package sharing

import (
	"errors"

	"golang.org/x/sys/windows"
)

// alive reports whether a process with that pid exists and hasn't exited (a share's, from the hosts table).
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED) // it's there, but another user's
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}
