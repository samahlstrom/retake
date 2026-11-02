//go:build !unix

package worker

import (
	"errors"
	"os/exec"
)

// Automated switching requires process-tree cancellation and a crash-safe project lock.
func lockProject(string) (func(), error) {
	return nil, errors.New("editing workers currently require Linux or macOS")
}
func detach(*exec.Cmd)     {}
func stopGroup(int) error  { return errors.ErrUnsupported }
func killGroup(int)        {}
func procStart(int) string { return "" }
func endGroup(int, string) {}
