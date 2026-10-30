//go:build unix

package cli

import (
	"os/exec"
	"runtime"
	"syscall"
)

// openURL opens url with the desktop's opener (open on macOS, xdg-open elsewhere). The opener runs in its own
// session, so Ctrl+C on retake doesn't also kill a browser it started.
func openURL(url string) error {
	name := "xdg-open"
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		name = "open"
	}
	cmd := exec.Command(name, url) //nolint:gosec // fixed opener; url is our own http://127.0.0.1:<port>/
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
