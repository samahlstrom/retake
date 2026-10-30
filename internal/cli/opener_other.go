//go:build !unix && !windows

package cli

import (
	"errors"
	"runtime"
)

// openURL has no opener to call on this platform; the caller prints the URL instead.
func openURL(string) error {
	return errors.New("no browser opener on " + runtime.GOOS)
}
