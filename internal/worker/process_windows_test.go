//go:build windows

package worker

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/desktop"
)

// TestAgentsFindThisRetakeOnWindows: an agent's PATH gets retake's folder first (whatever case the PATH variable has),
// once; a herdr session gets it unless its profile sets PATH; and the prompt names plain `retake` for retake.exe.
func TestAgentsFindThisRetakeOnWindows(t *testing.T) {
	dir := filepath.Dir(desktop.Exe())
	env := withSelfOnPath([]string{`Path=C:\Windows\system32`, "HOME=x"})
	if !slices.Contains(env, `PATH=`+dir+`;C:\Windows\system32`) || slices.Contains(env, `Path=C:\Windows\system32`) {
		t.Fatalf("%q", env)
	}
	if again := withSelfOnPath(env); !slices.Equal(again, env) {
		t.Fatalf("added twice: %q", again)
	}
	t.Setenv("PATH", `C:\Windows\system32`)
	if got := sessionEnv([]string{"A=1"}); !slices.Equal(got, []string{"A=1", `PATH=` + dir + `;C:\Windows\system32`}) {
		t.Fatalf("session %q", got)
	}
	if got := sessionEnv([]string{`Path=C:\mine`}); !slices.Equal(got, []string{`Path=C:\mine`}) {
		t.Fatalf("the profile's PATH: %q", got)
	}
	want := "retake"
	if !strings.EqualFold(filepath.Base(desktop.Exe()), "retake.exe") {
		want = desktop.Exe() // the test binary
	}
	if got := askCommand(); got != want {
		t.Fatalf("askCommand %q, want %q", got, want)
	}
}
