//go:build windows

package cli

import "os/exec"

// openURL opens url in the default browser, like `start`, without going through cmd.exe's parser.
func openURL(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // fixed opener; url is ours
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
