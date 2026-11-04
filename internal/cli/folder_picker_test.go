package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A fake native dialog keeps this test headless. Only an app-mode local owner
// can request it; choosing folders stages candidates without changing links.
func TestAppFolderPickerStagesMultipleFolders(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	w, dir := newFolderWorld(t)
	w.config(t, fmt.Sprintf("idle_exit_minutes = 0\nfolder = %q\n", filepath.ToSlash(dir)))
	selected := []string{filepath.Join(w.root, "First folder"), filepath.Join(w.root, "Second folder")}
	for _, p := range selected {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	payload, _ := json.Marshal(selected)
	fakeBin := filepath.Join(w.root, "fake-native")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$*\" in *multipleSelectionsAllowed*) ;; *) exit 12;; esac\nprintf '%s\\n' '" + string(payload) + "'\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "osascript"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := w.startEnv()
	env.GOOS = "darwin"
	url, stop, done := w.serve(t, env, "start", "--app", "--no-open", "--port", "0")
	defer func() { stop(); <-done }()
	var reply struct {
		Paths []string `json:"paths"`
	}
	if code := postTest(t, url+"api/library/pick", `{}`, &reply); code != 200 {
		t.Fatalf("native folder picker: got %d, want 200", code)
	}
	if strings.Join(reply.Paths, "|") != strings.Join(selected, "|") {
		t.Fatalf("picker candidates: %q", reply.Paths)
	}
	var library struct {
		Folders []struct {
			Path string `json:"path"`
		} `json:"folders"`
	}
	if code := getJSON(t, url+"api/library", &library); code != 200 || len(library.Folders) != 1 || library.Folders[0].Path != dir {
		t.Fatalf("picker linked without confirmation: %d %+v", code, library)
	}
	for _, header := range []struct{ name, value string }{{"Origin", "https://foreign.example"}, {"X-Forwarded-For", "192.0.2.1"}} {
		req, err := http.NewRequest(http.MethodPost, url+"api/library/pick", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(header.name, header.value)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusForbidden && res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("remote dialog request: %s got %d", header.name, res.StatusCode)
		}
	}
}
