//go:build !unix && !windows

package cli

// processAlive can't tell on this platform, so a share's host is kept until the share removes it.
func processAlive(int) bool { return true }
