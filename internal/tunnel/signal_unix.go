//go:build unix

package tunnel

import (
	"os"
	"syscall"
)

// interrupt asks a process to exit, as Ctrl+C in its terminal would.
func interrupt(p *os.Process) error { return p.Signal(syscall.SIGINT) }
