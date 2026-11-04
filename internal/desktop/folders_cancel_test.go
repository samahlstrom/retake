package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Execute the chooser JavaScript in Node with fake Standard Additions. Node
// cannot open a native dialog; cancellation must return an empty selection.
func TestChooseFoldersCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is needed for JavaScript fixture")
	}
	dir := t.TempDir()
	fake := `#!/bin/sh
exec node -e 'const ObjC = {import() {}}; const Application = {currentApplication() {return {chooseFolder() {const e = new Error("User canceled"); e.errorNumber = -128; throw e;}};}}; console.log(eval(process.argv[1]));' "$4"
`
	if err := os.WriteFile(filepath.Join(dir, "osascript"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	paths, err := ChooseFolders(context.Background())
	if err != nil || len(paths) != 0 {
		t.Fatalf("cancel should return no candidates without an error: %q, %v", paths, err)
	}
}
