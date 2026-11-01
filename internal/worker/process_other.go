//go:build !unix

package worker

import (
	"errors"
	"os"
	"os/exec"
)

// Automated switching requires process-tree cancellation and a crash-safe project lock.
func lockProject(string) (func(), error) {
	return nil, errors.New("editing workers currently require Linux or macOS")
}
func ownGroup(*exec.Cmd)            {}
func stopGroup(p *os.Process) error { return p.Kill() }
func killGroup(p *os.Process)       { _ = p.Kill() }
