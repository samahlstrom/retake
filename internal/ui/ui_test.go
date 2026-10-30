package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<p>hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := Files(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := fs.ReadFile(page, "index.html"); err != nil || string(b) != "<p>hi" {
		t.Errorf("index.html = %q, %v", b, err)
	}
	if _, err := Files(filepath.Join(dir, "index.html")); err == nil {
		t.Error("a file was accepted as the page folder")
	}
	if _, err := Files(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing folder was accepted")
	}
	// Without --ui-dir it's the embedded build: nil in a plain `go test` (no -tags webdist).
	if page, err := Files(""); err != nil || (page == nil) != (Embedded() == nil) {
		t.Errorf("Files(\"\") = %v, %v", page, err)
	}
}
