//go:build unix

package cli

import (
	"errors"
	"syscall"
)

// processAlive reports whether a process with that pid exists (a share's, from the hosts table).
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
