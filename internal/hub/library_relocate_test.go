package hub

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLibraryLocateExistingProjectKeepsShareURL(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.RescanEvery = time.Nanosecond })
	oldPath := filepath.Join(x.dir, "alpha")
	initial := libraryView(t, x.get("/api/library"))
	saved := libraryView(t, replaceLibrary(t, x, libraryRevision(t, initial), []map[string]any{{"path": oldPath, "subfolders": true}}))
	id := libraryString(t, libraryFolders(t, saved)[0], "id")
	for _, dest := range []string{filepath.Join(t.TempDir(), "moved-alpha"), filepath.Join(t.TempDir(), "moved-again")} {
		if err := os.Rename(oldPath, dest); err != nil {
			t.Fatal(err)
		}
		saved = libraryView(t, replaceLibrary(t, x, libraryRevision(t, saved), []map[string]any{{"id": id, "path": dest, "subfolders": true}}))
		if r := x.get("/p/alpha/api/info"); r.code != http.StatusOK {
			t.Fatalf("Locate changed the existing alpha URL: %d %s", r.code, r.body)
		}
		oldPath = dest
	}
	saved = libraryView(t, replaceLibrary(t, x, libraryRevision(t, saved), []map[string]any{}))
	libraryView(t, replaceLibrary(t, x, libraryRevision(t, saved), []map[string]any{{"path": oldPath, "subfolders": true}}))
	restartLibraryFT(t, x)
	if r := x.get("/p/alpha/api/info"); r.code != http.StatusOK {
		t.Fatalf("re-adding moved folder lost alpha URL: %d %s", r.code, r.body)
	}
}
