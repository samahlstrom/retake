// Package desktop is what makes retake behave as an installed app (spec/setup.md): the tools that ship with it come
// first on its PATH, an app started from Finder or a desktop menu still finds the person's own programs, and it can
// start at login.
package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AppID is the app's identifier: the Mac app's bundle id and the login item's name. The product name isn't final;
// this is the one place it's set in Go (scripts/macos-app.sh sets the same in Info.plist).
const AppID = "io.github.samahlstrom.retake"

// ManifestName is the file that marks a folder as a Retake bundle: retake and the tools that ship with it.
const ManifestName = "retake-bundle.json"

// Manifest is retake-bundle.json: what a release put next to retake.
type Manifest struct {
	Bundle  int             `json:"bundle"` // 1
	Version string          `json:"version"`
	Tools   map[string]Tool `json:"tools"`
}

// Tool is one program that ships with retake.
type Tool struct {
	Version string `json:"version"`
	License string `json:"license"`
	Source  string `json:"source"`
}

// Exe is the running retake binary with its links resolved ("" when it can't be found).
func Exe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}

// StableExe is the retake binary as the person's PATH names it (~/.local/bin/retake, a link into a versioned install
// folder), so a login item outlives an update; else Exe.
func StableExe() string { return stableExe(Exe(), os.Getenv("PATH")) }

// stableExe is the first link to exe on path, outside exe's own folder (which a bundle puts first); else exe.
func stableExe(exe, path string) string {
	if exe == "" {
		return ""
	}
	own := filepath.Dir(exe)
	for _, d := range filepath.SplitList(path) {
		if d == "" || d == own || !filepath.IsAbs(d) {
			continue
		}
		p := filepath.Join(d, filepath.Base(exe))
		if r, err := filepath.EvalSymlinks(p); err == nil && r == exe && isExec(p) {
			return p
		}
	}
	return exe
}

// BundleDir is the folder of retake's own tools when exe runs from a bundle: the folder holding exe, when it (or, in a
// Mac app, Contents/Resources) has retake-bundle.json. "" and nil otherwise.
func BundleDir(exe string) (string, *Manifest) {
	if exe == "" {
		return "", nil
	}
	dir := filepath.Dir(exe)
	for _, p := range []string{filepath.Join(dir, ManifestName), filepath.Join(filepath.Dir(dir), "Resources", ManifestName)} {
		b, err := os.ReadFile(p) //nolint:gosec // next to our own binary
		if err != nil {
			continue
		}
		var m Manifest
		if json.Unmarshal(b, &m) != nil || m.Bundle < 1 {
			continue
		}
		return dir, &m
	}
	return "", nil
}

// UseBundle puts the bundle's folder first on this process's PATH, so retake, its renders and the agents it starts
// use the tools that came with it. It returns the folder ("" when retake isn't in a bundle; nothing changes then).
func UseBundle(exe string) string {
	dir, _ := BundleDir(exe)
	if dir == "" {
		return ""
	}
	_ = os.Setenv("PATH", JoinPath(dir, os.Getenv("PATH")))
	return dir
}

// Find returns the program name next to the retake binary, else on PATH. bundled says it's the copy next to retake.
func Find(name string) (path string, bundled bool, err error) {
	if exe := Exe(); exe != "" {
		p := filepath.Join(filepath.Dir(exe), name)
		if isExec(p) {
			return p, true, nil
		}
	}
	p, err := exec.LookPath(name)
	return p, false, err
}

func isExec(p string) bool {
	st, err := os.Stat(p) //nolint:gosec // a program next to retake or on PATH: only whether it runs
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

// AppBundle is the .app folder when exe is a Mac app's main program (X.app/Contents/MacOS/<exe>), else "".
func AppBundle(exe string) string {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" && strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}

// AppArgs are the arguments retake runs with: started as a Mac app's program with no arguments (macOS may add one
// -psn_… argument), it's `retake start --app`; anything else stays as it is.
func AppArgs(exe string, args []string) []string {
	if AppBundle(exe) == "" {
		return args
	}
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			return args
		}
	}
	return []string{"start", "--app"}
}

// JoinPath joins PATH lists, each folder once, in order, leaving out empty entries.
func JoinPath(lists ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, d := range filepath.SplitList(l) {
			if d == "" || seen[d] {
				continue
			}
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}
