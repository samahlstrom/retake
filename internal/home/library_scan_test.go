package home

import (
	"path/filepath"
	"testing"
	"time"
)

// A non-recursive library link is one selected project folder. Its own vN
// directories define that project's versions; child project trees are not part
// of the link and must not be discovered.
func TestScanLinkedDirWithoutSubfolders(t *testing.T) {
	root := filepath.Join(t.TempDir(), "selected")
	now := time.Now()
	touch(t, filepath.Join(root, "v1", "root_v1.mp4"), now)
	touch(t, filepath.Join(root, "child", "v1", "child_v1.mp4"), now)

	got := ScanLinkedDir(root, false).Videos
	if len(got) != 1 || got[0].Root != root || got[0].Path != "" || got[0].Name != "selected" {
		t.Fatalf("non-recursive linked scan: %+v", got)
	}
}
