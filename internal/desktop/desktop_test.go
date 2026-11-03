package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func TestBundleDir(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "retake")
	write(t, exe, "", 0o755)
	if d, m := BundleDir(exe); d != "" || m != nil {
		t.Errorf("no manifest: %q %v", d, m)
	}
	write(t, filepath.Join(dir, ManifestName), "not json", 0o644)
	if d, _ := BundleDir(exe); d != "" {
		t.Errorf("a broken manifest made a bundle: %q", d)
	}
	write(t, filepath.Join(dir, ManifestName), `{"bundle":1,"version":"0.1.0","tools":{"herdr":{"version":"0.8.2"}}}`, 0o644)
	d, m := BundleDir(exe)
	if d != dir || m == nil || m.Version != "0.1.0" || m.Tools["herdr"].Version != "0.8.2" {
		t.Errorf("bundle: %q %+v", d, m)
	}

	// A Mac app keeps the manifest in Contents/Resources: data files can't sit next to signed code.
	app := filepath.Join(t.TempDir(), "Retake.app", "Contents")
	write(t, filepath.Join(app, "MacOS", "Retake"), "", 0o755)
	write(t, filepath.Join(app, "Resources", ManifestName), `{"bundle":1}`, 0o644)
	if d, _ := BundleDir(filepath.Join(app, "MacOS", "Retake")); d != filepath.Join(app, "MacOS") {
		t.Errorf("app bundle dir %q", d)
	}
}

func TestUseBundlePutsItFirst(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "retake")
	write(t, exe, "", 0o755)
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+dir)
	if got := UseBundle(exe); got != "" || os.Getenv("PATH") != "/usr/bin"+string(os.PathListSeparator)+dir {
		t.Errorf("without a manifest PATH changed: %q %q", got, os.Getenv("PATH"))
	}
	write(t, filepath.Join(dir, ManifestName), `{"bundle":1}`, 0o644)
	if got := UseBundle(exe); got != dir {
		t.Errorf("UseBundle = %q", got)
	}
	if want := dir + string(os.PathListSeparator) + "/usr/bin"; os.Getenv("PATH") != want {
		t.Errorf("PATH %q, want %q (once, first)", os.Getenv("PATH"), want)
	}
}

func TestAppArgs(t *testing.T) {
	exe := "/Applications/Retake.app/Contents/MacOS/Retake"
	if AppBundle(exe) != "/Applications/Retake.app" || AppBundle("/usr/local/bin/retake") != "" ||
		AppBundle("/x/Contents/MacOS/Retake") != "" {
		t.Error("AppBundle")
	}
	for _, tc := range []struct {
		exe  string
		args []string
		want string
	}{
		{exe, nil, "start --app"},
		{exe, []string{"-psn_0_12345"}, "start --app"},
		{exe, []string{"version"}, "version"},
		{exe, []string{"autostart", "on"}, "autostart on"},
		{"/usr/local/bin/retake", nil, ""},
	} {
		if got := strings.Join(AppArgs(tc.exe, tc.args), " "); got != tc.want {
			t.Errorf("AppArgs(%s, %v) = %q, want %q", tc.exe, tc.args, got, tc.want)
		}
	}
}

func TestFindPrefersTheCopyNextToRetake(t *testing.T) {
	// The test binary is "retake" here: a tool next to it is found first.
	exe := Exe()
	if exe == "" {
		t.Skip("no executable path")
	}
	name := "retake-test-tool-" + filepath.Base(t.TempDir())
	elsewhere := t.TempDir()
	write(t, filepath.Join(elsewhere, name), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", elsewhere)
	p, bundled, err := Find(name)
	if err != nil || bundled || p != filepath.Join(elsewhere, name) {
		t.Errorf("on PATH: %q %v %v", p, bundled, err)
	}
	next := filepath.Join(filepath.Dir(exe), name)
	write(t, next, "#!/bin/sh\n", 0o755)
	t.Cleanup(func() { _ = os.Remove(next) })
	p, bundled, err = Find(name)
	if err != nil || !bundled || p != next {
		t.Errorf("next to retake: %q %v %v", p, bundled, err)
	}
}

func TestMergePath(t *testing.T) {
	home := "/home/ana"
	exists := func(d string) bool { return d == "/home/ana/.local/bin" || d == "/opt/homebrew/bin" }
	sep := string(os.PathListSeparator)
	got := MergePath("/app/bin", "/home/ana/.local/bin"+sep+"/usr/bin", "/usr/bin"+sep+"/bin"+sep+"/app/bin", home, exists)
	want := strings.Join([]string{"/app/bin", "/home/ana/.local/bin", "/usr/bin", "/bin", "/opt/homebrew/bin"}, sep)
	if got != want {
		t.Errorf("MergePath:\n got %s\nwant %s", got, want)
	}
}

func TestLoginShellPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no login shell")
	}
	dir := t.TempDir()
	shell := filepath.Join(dir, "fakesh")
	// A chatty profile, then the PATH between the marks.
	write(t, shell, "#!/bin/sh\necho 'welcome to your shell'\nPATH=/from/profile:/usr/bin\neval \"$2\"\necho bye\n", 0o755)
	got, err := LoginShellPath(context.Background(), shell)
	if err != nil || got != "/from/profile:/usr/bin" {
		t.Errorf("LoginShellPath = %q, %v", got, err)
	}
	// A shell that prints nothing useful.
	write(t, shell, "#!/bin/sh\necho nothing\n", 0o755)
	if got, err := LoginShellPath(context.Background(), shell); err == nil || got != "" {
		t.Errorf("no PATH: %q %v", got, err)
	}
	// A shell that hangs is given up on.
	old := ShellTimeout
	ShellTimeout = 200 * time.Millisecond
	t.Cleanup(func() { ShellTimeout = old })
	write(t, shell, "#!/bin/sh\nexec sleep 30\n", 0o755)
	start := time.Now()
	if _, err := LoginShellPath(context.Background(), shell); err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("hanging shell: %v after %v", err, time.Since(start))
	}
}

func TestAutostart(t *testing.T) {
	home := t.TempDir()
	linux := Autostart{GOOS: "linux", Home: home, Exe: "/home/ana/.local/bin/retake"}
	p, err := linux.Path()
	if err != nil || p != filepath.Join(home, ".config", "autostart", "retake.desktop") {
		t.Fatalf("linux path %q %v", p, err)
	}
	if linux.On() {
		t.Error("on before Set")
	}
	if err := linux.Set(true); err != nil || !linux.On() {
		t.Fatalf("Set(true): %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "Exec=/home/ana/.local/bin/retake start --app\n") {
		t.Errorf("desktop entry:\n%s", b)
	}
	if err := linux.Set(false); err != nil || linux.On() {
		t.Errorf("Set(false): %v", err)
	}
	if err := linux.Set(false); err != nil {
		t.Errorf("off twice: %v", err)
	}
	spaced := Autostart{GOOS: "linux", Home: home, ConfigHome: filepath.Join(home, "cfg"), Exe: "/opt/My Apps/retake"}
	if err := spaced.Set(true); err != nil {
		t.Fatal(err)
	}
	p, _ = spaced.Path()
	b, _ = os.ReadFile(p)
	if !strings.HasPrefix(p, filepath.Join(home, "cfg")) || !strings.Contains(string(b), `Exec="/opt/My Apps/retake" start --app`) {
		t.Errorf("quoted entry at %s:\n%s", p, b)
	}

	mac := Autostart{GOOS: "darwin", Home: home, Exe: "/Applications/Retake & Co.app/Contents/MacOS/Retake"}
	if err := mac.Set(true); err != nil {
		t.Fatal(err)
	}
	p, _ = mac.Path()
	b, _ = os.ReadFile(p)
	if p != filepath.Join(home, "Library", "LaunchAgents", AppID+".plist") ||
		!strings.Contains(string(b), "<string>/usr/bin/open</string>") ||
		!strings.Contains(string(b), "<string>/Applications/Retake &amp; Co.app</string>") ||
		!strings.Contains(string(b), "<key>RunAtLoad</key>") {
		t.Errorf("plist at %s:\n%s", p, b)
	}
	if _, err := (Autostart{GOOS: "windows", Home: home}).Path(); err == nil {
		t.Error("windows has a path")
	}
}

func TestJoinPath(t *testing.T) {
	sep := string(os.PathListSeparator)
	got := filepath.SplitList(JoinPath("a"+sep+"b", "", "b"+sep+sep+"c", "a"))
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("JoinPath = %v", got)
	}
}
