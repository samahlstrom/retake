package home

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinkedScanDoesNotDuplicateInternalSymlink(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "shots", "v1", "shots_v1.mp4"), time.Now())
	if err := os.Symlink(filepath.Join(root, "shots"), filepath.Join(root, "alias")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(root, filepath.Join(root, "self")); err != nil {
		t.Fatal(err)
	}
	scan := ScanLinkedDir(root, true, nil)
	if len(scan.Videos) != 1 || scan.Videos[0].Path != "shots" {
		t.Fatalf("linked scan repeated aliased source: %+v", scan.Videos)
	}
}
