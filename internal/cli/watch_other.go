//go:build !unix

package cli

import (
	"os"
	"os/exec"
)

// ownGroup does nothing here: there are no process groups to signal.
func ownGroup(*exec.Cmd) {}

// interrupt stops the command; without SIGINT to send, it kills it.
func interrupt(p *os.Process) error { return p.Kill() }

// killGroup does nothing here: interrupt already killed the command.
func killGroup(*os.Process) {}
