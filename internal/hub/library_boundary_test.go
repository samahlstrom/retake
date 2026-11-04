package hub

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/team"
)

func TestLibraryCanonicalizesLinksBeforeSharing(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.RescanEvery = time.Nanosecond })
	outside := filepath.Join(t.TempDir(), "private")
	file(t, filepath.Join(outside, "v1", "secret_v1.mp4"), time.Now())
	outside, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(x.dir, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	initial := libraryView(t, x.get("/api/library"))
	viewer := homeToken(t, x.dir, team.Viewer, "")
	chosen := []map[string]any{{"path": alias, "subfolders": true}, {"path": outside, "subfolders": true}}
	saved := libraryView(t, replaceLibrary(t, x, libraryRevision(t, initial), chosen))
	links := libraryFolders(t, saved)
	if len(links) != 1 {
		t.Fatalf("aliases were not canonicalized/deduplicated: %#v", links)
	}
	resolved, err := filepath.EvalSymlinks(libraryString(t, links[0], "path"))
	if err != nil || resolved != outside {
		t.Fatalf("linked path does not resolve to the selected directory: %q %v", resolved, err)
	}
	if cards := x.home(t, shared(), bearerTok(viewer)).Projects; len(cards) != 0 {
		t.Fatalf("alias broadened old share: %+v", cards)
	}
}

func TestLibraryDoesNotReuseRemovedProjectsShareNames(t *testing.T) {
	x := newFT(t, func(o *FolderOptions) { o.RescanEvery = time.Nanosecond })
	initial := libraryView(t, x.get("/api/library"))
	oldShare := scopedToken(t, x.dir, "video:alpha")
	outside := filepath.Join(t.TempDir(), "alpha")
	file(t, filepath.Join(outside, "v1", "unshared_v1.mp4"), time.Now())
	result := replaceLibrary(t, x, libraryRevision(t, initial), []map[string]any{{"path": outside, "subfolders": true}})
	if result.code != http.StatusOK {
		t.Fatalf("replace source: %d %s", result.code, result.body)
	}
	if r := x.get("/p/alpha/api/info", shared(), bearerTok(oldShare)); r.code == http.StatusOK {
		t.Fatalf("old video link opened a different source: %s", r.body)
	}
	restartLibraryFT(t, x)
	if r := x.get("/p/alpha/api/info", shared(), bearerTok(oldShare)); r.code == http.StatusOK {
		t.Fatal("restart allowed stale share to open replacement source")
	}
}
