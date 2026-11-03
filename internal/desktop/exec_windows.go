//go:build windows

package desktop

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// isExec reports whether p is a program. Windows has no execute bits: it's a regular file whose extension is one of
// PATHEXT's (.exe, .cmd, …).
func isExec(p string) bool {
	st, err := os.Stat(p) //nolint:gosec // a program next to retake or on PATH: only whether it runs
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	ext := strings.ToLower(filepath.Ext(p))
	return ext != "" && slices.Contains(pathExt(), ext)
}

// pathExt is PATHEXT's extensions, lower case (.com .exe .bat .cmd when it isn't set).
func pathExt() []string {
	x := os.Getenv("PATHEXT")
	if x == "" {
		x = ".COM;.EXE;.BAT;.CMD"
	}
	var out []string
	for _, e := range strings.Split(strings.ToLower(x), ";") {
		if strings.HasPrefix(e, ".") {
			out = append(out, e)
		}
	}
	return out
}

// programIn is the program name in dir, as Windows runs it (Program). "" when there's none.
func programIn(dir, name string) string { return Program(filepath.Join(dir, name)) }

// Program is the program at p, as Windows runs it: p itself when it has a program's extension, else p with the first
// of PATHEXT's extensions that's there (retake-edit: retake-edit.exe). "" when there's none.
func Program(p string) string {
	if isExec(p) {
		return p
	}
	for _, e := range pathExt() {
		if isExec(p + e) {
			return p + e
		}
	}
	return ""
}
