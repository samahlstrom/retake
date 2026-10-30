// Package cli is the retake command line: open, sessions, version. cmd/retake only wires it to the process.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/server"
)

// Exit codes.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2 // bad arguments, a missing file, a busy --port, no free port
)

// version is set at build time: -ldflags "-X github.com/samahlstrom/retake/internal/cli.version=v0.1.0".
var version = ""

// Env is everything the CLI touches outside its arguments. Zero fields get the real thing.
type Env struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	OpenURL        func(url string) error // the OS opener
	PortTries      int                    // ports tried from the default one when it's busy; 0 = 20
	Ready          func(url string)       // called once the server listens (tests)
	Prober         family.Prober          // nil = ffprobe
	Clock          server.Clock           // nil = the real clock
	CheckEvery     time.Duration          // idle watchdog period; 0 = 10 s
}

func (e Env) withDefaults() Env {
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.OpenURL == nil {
		e.OpenURL = openURL
	}
	return e
}

const usage = `retake: review a video by talking over it. Press R, talk and draw, press R again.

usage:
  retake open <video> [--versions a.mp4 b.mp4 ...] [--port N] [--no-open] [--idle-exit MIN] [--ui-dir DIR] [--verbose]
      serve the review page for <video> and its version family (clip_v5, v6/clip_v6, ...)
  retake sessions <video> [--json]
      list the takes saved for <video>
  retake version
      print the version

open:
  --versions F...   the family is exactly <video> plus these files (default: detect *_vN files)
  --port N          serve on port N, or exit 2 if it's busy (default 8777, or the next free one up to 8796)
  --no-open         don't open the page in the browser
  --idle-exit MIN   exit MIN minutes after the page is gone; 0 = never (default 15)
  --ui-dir DIR      serve the page from DIR instead of the one built into the binary
  --verbose         log every request

config: $XDG_CONFIG_HOME/retake/config.toml (default ~/.config/retake/config.toml), keys name, port,
idle_exit_minutes, open_browser. Flags override it.
`

// Main runs the command line and returns the exit code.
func Main(ctx context.Context, args []string, env Env) int {
	env = env.withDefaults()
	if len(args) == 0 {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	switch args[0] {
	case "open":
		return openCmd(ctx, args[1:], env)
	case "sessions":
		return sessionsCmd(args[1:], env)
	case "version", "--version":
		_, _ = fmt.Fprintf(env.Stdout, "retake %s\n", Version())
		return ExitOK
	case "help", "-h", "--help":
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	_, _ = fmt.Fprintf(env.Stderr, "retake: unknown command %q\n\n%s", args[0], usage)
	return ExitUsage
}

// Version returns the build's version: the -X value, else the module version or VCS revision, else "dev".
func Version() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	rev, dirty := "", false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev-" + rev
}

// absPath expands a leading ~, makes p absolute and resolves symlinks when the file exists (Python's
// Path.expanduser().resolve()).
func absPath(p string, getenv func(string) string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home := getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home != "" {
			p = filepath.Join(home, p[1:])
		}
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// fmtDur formats seconds like the legacy player: m:ss.s, or h:mm:ss.s from an hour; "?" for none or zero.
func fmtDur(t float64, ok bool) string {
	if !ok || t == 0 {
		return "?"
	}
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%04.1f", int64(t/3600), int64(modf(t, 3600)/60), modf(t, 60))
	}
	return fmt.Sprintf("%d:%04.1f", int64(t/60), modf(t, 60))
}

func modf(a, b float64) float64 {
	m := a - b*float64(int64(a/b))
	if m < 0 {
		m += b
	}
	return m
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
