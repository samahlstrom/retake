package home

import (
	"path/filepath"
	"testing"
	"time"
)

func TestNonrecursiveLibraryDoesNotLookForChildProjects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	touch(t, filepath.Join(root, "child", "v1", "child_v1.mp4"), time.Now())
	// No video in the selected root does not grant permission to search children.
	scan := ScanLinkedDir(root, false)
	if len(scan.Videos) != 0 || len(scan.Empty) != 0 {
		t.Fatalf("child projects in nonrecursive link: %+v", scan)
	}
	if scan.Dirs > 2 {
		t.Fatalf("nonrecursive scan traversed child trees: %d directories", scan.Dirs)
	}
}
