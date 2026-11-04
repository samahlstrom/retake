package hub

import (
	"os"
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

func TestLibraryIdentitySurvivesMissingRootUnderAlias(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "actual")
	selected := filepath.Join(target, "selected")
	if err := os.MkdirAll(selected, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Skip(err)
	}
	original := filepath.Join(alias, "selected")
	config := filepath.Join(parent, "config.toml")
	before := libraryPath(config, original)
	if err := os.Rename(selected, selected+"-offline"); err != nil {
		t.Fatal(err)
	}
	if after := libraryPath(config, original); after != before {
		t.Fatalf("offline library identity changed from %s to %s", before, after)
	}
}
