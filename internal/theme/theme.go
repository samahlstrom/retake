// Package theme is the server's half of spec/theme.md: where a user's theme lives, its theme.toml, adding it to the
// page, serving its files safely, and checking it against this version's tokens and parts. A theme is CSS, fonts,
// images and four theme.toml keys in a folder outside the install; nothing here ever writes to it, except
// `retake theme init` into a folder it's given.
package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/samahlstrom/retake/internal/userdir"
)

// API is the Theme API's version (spec/theme.md, "Versioning").
const API = 1

// The theme folder's two files.
const (
	CSSFile  = "theme.css"
	TOMLFile = "theme.toml"
)

// Off is the --theme and RETAKE_THEME value that turns theming off.
const Off = "none"

// EnvVar names the theme folder (spec/theme.md, "Where the theme lives").
const EnvVar = "RETAKE_THEME"

// MaxFile is the largest theme file served.
const MaxFile = 10 << 20

// The modes theme.toml's mode can be.
const (
	ModeDark  = "dark"
	ModeLight = "light"
	ModeAuto  = "auto"
)

// Settings are theme.toml's keys.
type Settings struct {
	Name    string `toml:"name" json:"name,omitempty"`
	Logo    string `toml:"logo" json:"logo,omitempty"`
	Favicon string `toml:"favicon" json:"favicon,omitempty"`
	Mode    string `toml:"mode" json:"mode,omitempty"`
}

// Problem is one thing `retake theme check` reports, and the server warns about at startup.
type Problem struct {
	Kind    string `json:"kind"` // unknown-token, deprecated-token, unknown-part, deprecated-part, toml, file, folder
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Message }

// Theme is a theme folder as read at one moment.
type Theme struct {
	Dir string
	// Settings are theme.toml's, checked: Logo and Favicon are "" unless the file is there and may be served, and
	// Mode is a valid mode (dark when unset or bad).
	Settings Settings
	CSS      bool // theme.css is there
	// Problems are theme.toml's and its files' (Check adds those of theme.css).
	Problems []Problem
}

// Dir picks the theme folder: the --theme flag, else $RETAKE_THEME, else def (the local or hosted default). It
// returns "" when theming is off ("none"). The result isn't made absolute; a ~ is left to the caller.
func Dir(flag string, getenv func(string) string, def string) string {
	for _, v := range []string{flag, strings.TrimSpace(getenv(EnvVar))} {
		if v == "" {
			continue
		}
		if strings.EqualFold(v, Off) {
			return ""
		}
		return v
	}
	return def
}

// LocalDir is the theme folder of retake open and retake share: $XDG_CONFIG_HOME/retake/theme, else
// ~/.config/retake/theme (%APPDATA%\retake\theme on Windows), beside config.toml.
func LocalDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "retake", "theme"), nil
	}
	if d := userdir.Roaming(getenv); d != "" {
		return filepath.Join(d, "retake", "theme"), nil
	}
	home := userdir.Home(getenv)
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home folder for the theme: %w", err)
		}
		home = h
	}
	return filepath.Join(home, ".config", "retake", "theme"), nil
}

// HostedDir is retake serve's theme folder: <data dir>/theme.
func HostedDir(data string) string { return filepath.Join(data, "theme") }

// Load reads the theme folder dir. It returns nil when dir is "" or isn't a folder: no theme, the built-in look. It
// never fails otherwise: what's wrong is in Problems, and the rest of the theme still applies.
func Load(dir string) *Theme {
	if dir == "" {
		return nil
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return nil
	}
	t := &Theme{Dir: dir}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Problems = append(t.Problems, Problem{Kind: "folder", Message: fmt.Sprintf("can't read %s: %v", dir, err)})
		return t
	}
	defer func() { _ = root.Close() }()
	if st, err := root.Stat(CSSFile); err == nil && st.Mode().IsRegular() {
		t.CSS = true
	}
	b, err := root.ReadFile(TOMLFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: TOMLFile,
			Message: fmt.Sprintf("%s: can't read it: %v", TOMLFile, err)})
	default:
		t.settings(root, b)
	}
	if t.Settings.Mode == "" {
		t.Settings.Mode = ModeDark
	}
	return t
}

// settings reads theme.toml and keeps what's usable.
func (t *Theme) settings(root *os.Root, b []byte) {
	var s Settings
	md, err := toml.Decode(string(b), &s)
	if err != nil {
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: TOMLFile,
			Message: fmt.Sprintf("%s is ignored: %v", TOMLFile, err)})
		return
	}
	for _, k := range md.Undecoded() {
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: k.String(),
			Message: fmt.Sprintf("%s: unknown key %q (name, logo, favicon and mode are the keys)", TOMLFile, k.String())})
	}
	s.Name = strings.TrimSpace(s.Name)
	switch m := strings.ToLower(strings.TrimSpace(s.Mode)); m {
	case "", ModeDark, ModeLight, ModeAuto:
		s.Mode = m
	default:
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: "mode",
			Message: fmt.Sprintf("%s: mode %q isn't dark, light or auto; using dark", TOMLFile, s.Mode)})
		s.Mode = ""
	}
	s.Logo = t.asset(root, "logo", s.Logo, imageTypes)
	s.Favicon = t.asset(root, "favicon", s.Favicon, iconTypes)
	t.Settings = s
}

// asset checks a file theme.toml names: inside the folder, there, and of a type it may be. It returns the clean
// slash path, or "" (with a problem) when it can't be used.
func (t *Theme) asset(root *os.Root, key, name string, types []string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	clean, ok := servable(filepath.ToSlash(name))
	if !ok || !slices.Contains(types, strings.ToLower(path.Ext(clean))) {
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: key, Message: fmt.Sprintf(
			"%s: %s %q must be a file in the theme folder of type %s", TOMLFile, key, name, strings.Join(types, " "))})
		return ""
	}
	if st, err := root.Stat(clean); err != nil || !st.Mode().IsRegular() {
		t.Problems = append(t.Problems, Problem{Kind: "toml", Name: key,
			Message: fmt.Sprintf("%s: %s %q isn't in the theme folder", TOMLFile, key, name)})
		return ""
	}
	return clean
}

// contentTypes are the only files a theme serves (spec/theme.md, "Serving theme files"): CSS, fonts and images.
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".woff2": "font/woff2",
	".woff":  "font/woff",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".avif":  "image/avif",
	".ico":   "image/x-icon",
	".svg":   "image/svg+xml",
}

var (
	imageTypes = []string{".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif"}
	iconTypes  = append(slices.Clone(imageTypes), ".ico")
)

// ContentType is the Content-Type a theme file is served with, or "" for a file a theme never serves.
func ContentType(name string) string { return contentTypes[strings.ToLower(path.Ext(name))] }

// servable cleans a slash path inside the theme folder and says whether it may be served: a valid relative path, no
// hidden segment, and an allowed type.
func servable(name string) (string, bool) {
	name = strings.TrimPrefix(name, "./")
	if !fs.ValidPath(name) || name == "." || ContentType(name) == "" {
		return "", false
	}
	for seg := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(seg, ".") || strings.ContainsAny(seg, "\\:\x00") {
			return "", false
		}
	}
	return name, true
}

// Warnings are the lines the server prints at startup about the theme folder: where it is, and what's wrong.
func Warnings(dir string) (line string, problems []Problem) {
	t := Load(dir)
	if t == nil {
		return "", nil
	}
	r := Check(dir)
	name := t.Settings.Name
	if name == "" {
		name = "no name"
	}
	what := "no theme.css"
	if t.CSS {
		what = CSSFile
	}
	return fmt.Sprintf("%s (%s, %s)", dir, name, what), r.Problems
}
