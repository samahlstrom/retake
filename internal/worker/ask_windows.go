//go:build windows

package worker

import (
	"path/filepath"
	"strings"

	"github.com/samahlstrom/retake/internal/desktop"
)

// On Windows an agent runs its commands in PowerShell, cmd or Git Bash, which quote a path with spaces each their own
// way. So retake's folder goes first on every agent's PATH (withSelfOnPath), and the prompt names plain `retake`.
func init() {
	askCommand = func() string {
		if exe := desktop.Exe(); exe != "" && !strings.EqualFold(filepath.Base(exe), "retake.exe") {
			return exe // a renamed binary; such a name has no spaces in practice
		}
		return "retake"
	}
}
