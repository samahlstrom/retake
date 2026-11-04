package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStartKeepsLinkedLibraryWhenOriginalFolderIsOffline(t *testing.T) {
	w, dir := newFolderWorld(t)
	w.config(t, fmt.Sprintf("idle_exit_minutes = 0\nfolder = %q\n", filepath.ToSlash(dir)))
	env := w.startEnv()
	url, stop, done := w.serve(t, env, "start", "--no-open", "--port", "0")
	var library struct {
		Revision int              `json:"revision"`
		Folders  []map[string]any `json:"folders"`
	}
	if code := getJSON(t, url+"api/library", &library); code != 200 {
		stop()
		wait(t, done)
		t.Fatalf("library: %d", code)
	}
	other := filepath.Join(w.root, "other-drive")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	library.Folders = append(library.Folders, map[string]any{"path": other, "subfolders": true})
	body, _ := json.Marshal(library)
	if code := postTest(t, url+"api/library", string(body), nil); code != 200 {
		stop()
		wait(t, done)
		t.Fatalf("link second drive: %d", code)
	}
	stop()
	wait(t, done)
	if err := os.Rename(dir, dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(dir+"-offline", dir) }()
	url, stop, done = w.serve(t, env, "start", "--no-open", "--port", "0")
	defer func() { stop(); wait(t, done) }()
	waitHome(t, url, dir)
	if code := getJSON(t, url+"api/library", &library); code != 200 || len(library.Folders) != 2 {
		t.Fatalf("offline source forced setup/lost links: %d %+v", code, library)
	}
}
