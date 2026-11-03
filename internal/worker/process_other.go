//go:build !unix && !windows

package worker

import (
	"errors"
	"os/exec"
)

// Automated switching requires process-tree cancellation and a crash-safe project lock.
func lockProject(string) (func(), error) {
	return nil, errors.New("editing workers currently require Linux, macOS or Windows")
}
func startOwn(c *exec.Cmd) (*exec.Cmd, error) { return c, c.Start() }
func track(int, string)                       {}
func withSelfOnPath(env []string) []string    { return env }
func sessionEnv(profile []string) []string    { return profile }
func killGroup(int, string)                   {}
func procStart(int) string                    { return "" }
func endGroup(int, string)                    {}
