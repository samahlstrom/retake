package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryStablePathPreservesUnavailableDrive(t *testing.T) {
	for drive := 'Z'; drive >= 'D'; drive-- {
		root := string(drive) + `:\`
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			continue
		}
		path := filepath.Join(root, "Footage", "Selected")
		if got := stablePath(path); got != path {
			t.Fatalf("unavailable drive path changed from %q to %q", path, got)
		}
		return
	}
	t.Skip("no unavailable drive")
}
