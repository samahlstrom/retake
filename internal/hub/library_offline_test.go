package hub

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLibraryKeepsOtherLinksWhenOriginalDriveIsOffline(t *testing.T) {
	configHome := t.TempDir()
	x := newFT(t, func(o *FolderOptions) {
		o.RescanEvery = time.Nanosecond
		o.Settings = &LocalSettings{Home: configHome, ConfigPath: filepath.Join(configHome, "retake", "config.toml")}
	})
	external := filepath.Join(t.TempDir(), "Other drive")
	file(t, filepath.Join(external, "separate", "v1", "separate_v1.mp4"), time.Now())
	initial := libraryView(t, x.get("/api/library"))
	folders := libraryFolders(t, initial)
	folders = append(folders, map[string]any{"path": external, "subfolders": true})
	if r := replaceLibrary(t, x, libraryRevision(t, initial), folders); r.code != http.StatusOK {
		t.Fatalf("link second drive: %d %s", r.code, r.body)
	}
	// The selected source can be removable, but its manifest must remain on the
	// application/config disk so the other linked drive is still usable.
	moved := x.dir + "-offline"
	if err := os.Rename(x.dir, moved); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(moved, x.dir) }()
	restartLibraryFT(t, x)
	links := libraryFolders(t, libraryView(t, x.get("/api/library")))
	if len(links) != 2 || folderAt(t, links, x.dir)["available"] != false || folderAt(t, links, external)["available"] != true {
		t.Fatalf("drive loss discarded the library: %#v", links)
	}
	if r := x.get("/api/home"); r.code != http.StatusOK || !strings.Contains(r.body, `"video":"separate"`) {
		t.Fatalf("other drive no longer visible: %d %s", r.code, r.body)
	}
}
