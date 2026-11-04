package hub

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLibrariesWithSameConfigRemainSeparate(t *testing.T) {
	appHome := t.TempDir()
	opts := func(o *FolderOptions) {
		o.RescanEvery = time.Nanosecond
		o.Settings = &LocalSettings{Home: appHome, ConfigPath: filepath.Join(appHome, "config.toml")}
	}
	first := newFT(t, opts)
	initial := libraryView(t, first.get("/api/library"))
	if r := replaceLibrary(t, first, libraryRevision(t, initial), []map[string]any{}); r.code != 200 {
		t.Fatalf("save first library: %d %s", r.code, r.body)
	}
	second := newFT(t, opts)
	links := libraryFolders(t, libraryView(t, second.get("/api/library")))
	if len(links) != 1 || links[0]["path"] != second.dir {
		t.Fatalf("another retake open reused first library: %#v", links)
	}
	if len(second.home(t).Projects) != 3 {
		t.Fatal("independent explicit folder lost its projects")
	}
	restartLibraryFT(t, first)
	if len(libraryFolders(t, libraryView(t, first.get("/api/library")))) != 0 {
		t.Fatal("empty first library changed")
	}
}
