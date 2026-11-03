//go:build unix

package sharing

import (
	"errors"
	"syscall"
)

// alive reports whether a process with that pid exists (a share's, from the hosts table).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
