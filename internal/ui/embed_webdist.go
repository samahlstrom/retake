//go:build webdist

package ui

import (
	"io/fs"

	"github.com/samahlstrom/retake/web"
)

func embedded() fs.FS {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
