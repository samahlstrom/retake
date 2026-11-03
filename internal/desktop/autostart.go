package desktop

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Autostart is starting Retake at login (spec/setup.md, `retake autostart`): a LaunchAgent on macOS, an XDG autostart
// entry on Linux, a value of the Run key on Windows.
type Autostart struct {
	GOOS       string // runtime.GOOS
	Home       string // the home folder
	ConfigHome string // $XDG_CONFIG_HOME, "" for ~/.config
	Exe        string // the retake binary
	// Run is the person's Run key on Windows (RunKeyPath); nil = this computer's registry.
	Run RunKey
}

// RunKey is the values of the Run key: the programs Windows starts when the person signs in.
type RunKey interface {
	Value(name string) (string, error) // fs.ErrNotExist when it has none of that name
	SetValue(name, value string) error
	DeleteValue(name string) error // nil when it had none
}

// RunKeyPath is the Run key Retake's value is in on Windows, and RunValue its name.
const (
	RunKeyPath = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	RunValue   = "Retake"
)

// Path is the file that turns it on (on Windows, the registry value).
func (a Autostart) Path() (string, error) {
	if a.GOOS == "windows" {
		return RunKeyPath + `\` + RunValue, nil
	}
	if a.Home == "" {
		return "", errors.New("no home folder")
	}
	switch a.GOOS {
	case "darwin":
		return filepath.Join(a.Home, "Library", "LaunchAgents", AppID+".plist"), nil
	}
	cfg := a.ConfigHome
	if cfg == "" || !filepath.IsAbs(cfg) {
		cfg = filepath.Join(a.Home, ".config")
	}
	return filepath.Join(cfg, "autostart", "retake.desktop"), nil
}

// On reports whether it's turned on.
func (a Autostart) On() bool {
	if a.GOOS == "windows" {
		v, err := a.runKey().Value(RunValue)
		return err == nil && v != ""
	}
	p, err := a.Path()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Set turns it on (writing the file) or off (removing it).
func (a Autostart) Set(on bool) error {
	if a.GOOS == "windows" {
		if !on {
			return a.runKey().DeleteValue(RunValue)
		}
		if a.Exe == "" {
			return errors.New("can't find the retake program")
		}
		return a.runKey().SetValue(RunValue, a.runCommand())
	}
	p, err := a.Path()
	if err != nil {
		return err
	}
	if !on {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if a.Exe == "" {
		return errors.New("can't find the retake program")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // the person's own LaunchAgents or autostart folder
		return err
	}
	return os.WriteFile(p, []byte(a.content()), 0o644) //nolint:gosec // read by launchd or the desktop, not secret
}

// content is the LaunchAgent plist (macOS) or the .desktop entry (Linux).
func (a Autostart) content() string {
	if a.GOOS == "darwin" {
		args := []string{a.Exe, "start", "--app"}
		if app := AppBundle(a.Exe); app != "" {
			args = []string{"/usr/bin/open", "-a", app} // the app, so macOS knows it as the app that's running
		}
		var b strings.Builder
		for _, x := range args {
			fmt.Fprintf(&b, "\t\t<string>%s</string>\n", html.EscapeString(x))
		}
		return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + AppID + `</string>
	<key>ProgramArguments</key>
	<array>
` + b.String() + `	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
`
	}
	return "[Desktop Entry]\nType=Application\nName=Retake\nComment=Review videos with an AI agent\n" +
		"Exec=" + desktopQuote(a.Exe) + " start --app\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
}

func (a Autostart) runKey() RunKey {
	if a.Run != nil {
		return a.Run
	}
	return systemRunKey()
}

// runCommand is the Run value: retakew.exe next to retake.exe (Retake without a console window), else retake.exe,
// with start --app.
func (a Autostart) runCommand() string {
	exe := a.Exe
	if i := strings.LastIndexAny(exe, `\/`); i >= 0 {
		w := exe[:i+1] + "retakew.exe"
		if st, err := os.Stat(w); err == nil && st.Mode().IsRegular() {
			exe = w
		}
	}
	return `"` + exe + `" start --app`
}

// desktopQuote quotes an argument for a .desktop file's Exec line when it needs it.
func desktopQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n\"'\\><~|&;$*?#()`") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`)
	return `"` + r.Replace(s) + `"`
}
