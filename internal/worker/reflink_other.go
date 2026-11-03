//go:build !linux

package worker

import "os"

// reflink is a plain copy's job elsewhere.
func reflink(_, _ *os.File) bool { return false }
