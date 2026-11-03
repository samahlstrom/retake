package worker

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeProgram puts a program called name in dir that exits 1, and returns its path: a shell script, or on Windows an
// empty name.exe, which PATH lookups and file checks find (the tests using it never run it).
func fakeProgram(t *testing.T, dir, name string) string {
	t.Helper()
	p, body := filepath.Join(dir, name), "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		p, body = p+".exe", ""
	}
	if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}
