package hub

import (
	"path/filepath"
	"testing"
)

func TestLibraryStablePathPreservesPrivateNamedDirectories(t *testing.T) {
	for _, path := range []string{"/private-footage/shoot", "/private/shoot", "/private/tmp-other/shoot", "/private/various/shoot"} {
		if got := stablePath(path); got != filepath.Clean(path) {
			t.Errorf("folder identity changed from %q to %q", path, got)
		}
	}
}
