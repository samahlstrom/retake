package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// TestPromptPathsReadAsTheyAre: a prompt's paths are %q-quoted as Go does, but on Windows without doubled
// backslashes, which an agent could take for part of the path.
func TestPromptPathsReadAsTheyAre(t *testing.T) {
	old := windowsQuote
	t.Cleanup(func() { windowsQuote = old })
	windowsQuote = false
	if got := fmt.Sprintf("to %q and %s", promptPath("/home/a b/r.json"), promptPath("x")); got != `to "/home/a b/r.json" and x` {
		t.Fatal(got)
	}
	windowsQuote = true
	if got := fmt.Sprintf("%q", promptPath(`C:\Users\Ana Lee\Videos\.retake\report-1.json`)); got != `"C:\Users\Ana Lee\Videos\.retake\report-1.json"` {
		t.Fatal(got)
	}
	if got := fmt.Sprintf("%q", promptPath("odd\"name\n")); got != `"odd\"name\n"` {
		t.Fatal(got) // anything a Windows path can't hold is still escaped
	}
	if got := timelinePrompt(`C:\v\ctx.json`, `C:\v\edit\timeline.json`, `C:\v\r.json`, ""); !strings.Contains(got, `context at "C:\v\ctx.json". The native timeline is "C:\v\edit\timeline.json".`) ||
		!strings.Contains(got, `write a JSON object to "C:\v\r.json"`) {
		t.Fatal(got)
	}
}
