package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// An app started from Finder or a desktop menu gets a short PATH (/usr/bin:/bin:...), so the person's own programs,
// claude in ~/.local/bin or codex in /opt/homebrew/bin, aren't found. RefreshPath asks their login shell for its PATH
// (spec/setup.md, "The login shell's PATH").

// ShellTimeout is how long the login shell may take to print its PATH.
var ShellTimeout = 5 * time.Second

const pathMark = "__RETAKE_PATH__"

// CommonDirs are the folders agent installers put programs in, added after the shell's PATH when they exist.
func CommonDirs(home string) []string {
	var out []string
	if home != "" {
		out = append(out, filepath.Join(home, ".local", "bin"))
	}
	out = append(out, "/opt/homebrew/bin", "/usr/local/bin")
	if home != "" {
		for _, d := range []string{".npm-global/bin", ".bun/bin", ".volta/bin", ".claude/local"} {
			out = append(out, filepath.Join(home, filepath.FromSlash(d)))
		}
	}
	return out
}

// LoginShellPath runs the person's login shell ($SHELL, else /bin/sh) and returns the PATH it sets up. It reads no
// input, has no terminal, and gives up after ShellTimeout.
func LoginShellPath(ctx context.Context, shell string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", errors.New("no login shell on Windows")
	}
	flags := "-ilc"
	if shell == "" {
		shell, flags = "/bin/sh", "-lc"
	}
	ctx, cancel := context.WithTimeout(ctx, ShellTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, flags, `printf '`+pathMark+`%s`+pathMark+`' "$PATH"`) //nolint:gosec // the person's own shell
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = time.Second
	detach(cmd)
	err := cmd.Run()
	s := out.String()
	i := strings.Index(s, pathMark)
	j := strings.LastIndex(s, pathMark)
	if i < 0 || j <= i {
		if err == nil {
			err = errors.New("the shell printed no PATH")
		}
		return "", err
	}
	return strings.TrimSpace(s[i+len(pathMark) : j]), nil
}

// MergePath is the PATH an app runs with: the bundle's folder, the login shell's PATH, the current one, then the
// common folders that exist.
func MergePath(bundle, shell, current, home string, exists func(string) bool) string {
	var common []string
	for _, d := range CommonDirs(home) {
		if exists(d) {
			common = append(common, d)
		}
	}
	return JoinPath(bundle, shell, current, strings.Join(common, string(os.PathListSeparator)))
}

// RefreshPath sets this process's PATH from the login shell's (MergePath), keeping the bundle's folder first. It
// returns the new PATH; on error, PATH still gains the common folders.
func RefreshPath(ctx context.Context) (string, error) {
	shellPath, err := LoginShellPath(ctx, os.Getenv("SHELL"))
	bundle, _ := BundleDir(Exe())
	p := MergePath(bundle, shellPath, os.Getenv("PATH"), os.Getenv("HOME"), func(d string) bool {
		st, err := os.Stat(d)
		return err == nil && st.IsDir()
	})
	_ = os.Setenv("PATH", p)
	return p, err
}
