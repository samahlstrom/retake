package hub

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryTreePreservesSelectedFolderName(t *testing.T) {
	x := newFT(t, nil)
	folder := filepath.Join(t.TempDir(), "Drive One")
	file(t, filepath.Join(folder, "v1", "cut_v1.mp4"), time.Now())
	initial := libraryView(t, x.get("/api/library"))
	libraryView(t, replaceLibrary(t, x, libraryRevision(t, initial), []map[string]any{{"path": folder, "subfolders": true}}))
	h := x.home(t)
	if len(h.Tree.Children) != 1 || h.Tree.Children[0].Title != "Drive One" {
		t.Fatalf("selected folder label changed: %+v", h.Tree.Children)
	}
}
