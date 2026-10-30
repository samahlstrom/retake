//go:build !unix

package tunnel

import "os"

// interrupt ends a process. Windows has no interrupt for another process, so it is killed.
func interrupt(p *os.Process) error { return p.Kill() }
