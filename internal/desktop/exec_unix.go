//go:build !windows

package desktop

import (
	"os"
	"path/filepath"
)

// isExec reports whether p is a program: a regular file with an execute bit.
func isExec(p string) bool {
	st, err := os.Stat(p) //nolint:gosec // a program next to retake or on PATH: only whether it runs
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// programIn is the program name in dir, "" when there's none.
func programIn(dir, name string) string { return Program(filepath.Join(dir, name)) }

// Program is the program at p, as the system runs it: p when it's a regular file with an execute bit (on Windows,
// p or p with one of PATHEXT's extensions: retake-edit is retake-edit.exe). "" when there's none.
func Program(p string) string {
	if isExec(p) {
		return p
	}
	return ""
}
