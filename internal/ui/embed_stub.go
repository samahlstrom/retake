//go:build !webdist

package ui

import "io/fs"

// Built without -tags webdist: no embedded page.
func embedded() fs.FS { return nil }
