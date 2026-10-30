package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestPath(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	xdg := filepath.Join(t.TempDir(), "xdg")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"XDG_CONFIG_HOME", map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": home}, filepath.Join(xdg, "retake", "config.toml")},
		{"relative XDG is ignored", map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": home}, filepath.Join(home, ".config", "retake", "config.toml")},
		{"HOME", map[string]string{"HOME": home}, filepath.Join(home, ".config", "retake", "config.toml")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Path(env(tc.env))
			if err != nil || got != tc.want {
				t.Errorf("Path = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		check    func(t *testing.T, c Config)
		warnings int
		wantErr  string
	}{
		{name: "empty", in: "", check: func(t *testing.T, c Config) {
			if c.Name != nil || c.Port != nil || c.IdleExitMinutes != nil || c.OpenBrowser != nil {
				t.Errorf("%+v", c)
			}
		}},
		{name: "all keys", in: "name = \"Sam\"\nport = 8811\nidle_exit_minutes = 15\nopen_browser = false\n",
			check: func(t *testing.T, c Config) {
				if *c.Name != "Sam" || *c.Port != 8811 || *c.IdleExitMinutes != 15 || *c.OpenBrowser {
					t.Errorf("%+v", c)
				}
			}},
		{name: "fractional minutes", in: "idle_exit_minutes = 0.5", check: func(t *testing.T, c Config) {
			if *c.IdleExitMinutes != 0.5 {
				t.Errorf("idle = %v", *c.IdleExitMinutes)
			}
		}},
		{name: "unknown key warns", in: "name = \"x\"\ncolour = \"red\"\n[extra]\nk = 1\n", warnings: 2,
			check: func(t *testing.T, c Config) {
				if *c.Name != "x" {
					t.Errorf("%+v", c)
				}
			}},
		{name: "transcribe table", in: "[transcribe]\nengine = \"whisper.cpp\"\nmodel = \"small\"\ndevice = \"cpu\"\n" +
			"command = \"fw --model x\"\nwhisper_cli = \"/opt/w/whisper-cli\"\n",
			check: func(t *testing.T, c Config) {
				tr := c.Transcribe
				if *tr.Engine != "whisper.cpp" || *tr.Model != "small" || *tr.Device != "cpu" || *tr.Command != "fw --model x" ||
					*tr.WhisperCLI != "/opt/w/whisper-cli" {
					t.Errorf("%+v", tr)
				}
			}},
		{name: "transcribe unset", in: "", check: func(t *testing.T, c Config) {
			if c.Transcribe != (Transcribe{}) {
				t.Errorf("%+v", c.Transcribe)
			}
		}},
		{name: "unknown transcribe key warns with its table", in: "[transcribe]\nbeam_size = 5\n", warnings: 1,
			check: func(t *testing.T, c Config) {
				if c.Transcribe.Model != nil {
					t.Errorf("%+v", c.Transcribe)
				}
			}},
		{name: "bad engine", in: "[transcribe]\nengine = \"vosk\"", wantErr: "transcribe.engine"},
		{name: "bad device", in: "[transcribe]\ndevice = \"gpu\"", wantErr: "transcribe.device"},
		{name: "wrong type", in: `port = "8777"`, wantErr: "port"},
		{name: "bad toml", in: `name = `, wantErr: "expected"},
		{name: "port out of range", in: `port = 70000`, wantErr: "out of range"},
		{name: "negative idle", in: `idle_exit_minutes = -1`, wantErr: "negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, warnings, err := Parse(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(warnings) != tc.warnings {
				t.Errorf("warnings = %v", warnings)
			}
			if tc.name == "unknown transcribe key warns with its table" && warnings[0] != `unknown key "transcribe.beam_size"` {
				t.Errorf("warning = %q", warnings[0])
			}
			tc.check(t, c)
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, w, err := Load(filepath.Join(dir, "missing.toml")); err != nil || w != nil || c.Name != nil {
		t.Errorf("missing file: %+v %v %v", c, w, err)
	}
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte(`name = "Tester"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, _, err := Load(p); err != nil || c.Name == nil || *c.Name != "Tester" {
		t.Errorf("Load = %+v, %v", c, err)
	}
}

func TestResolvePrecedence(t *testing.T) {
	s, i, f, b := "Sam", 9000, 2.5, false
	flagPort, flagIdle := 8811, 0.0
	for _, tc := range []struct {
		name  string
		cfg   Config
		flags Flags
		want  Settings
	}{
		{"defaults", Config{}, Flags{}, Settings{Port: 8777, IdleExit: 15, OpenBrowser: true}},
		{"file", Config{Name: &s, Port: &i, IdleExitMinutes: &f, OpenBrowser: &b}, Flags{},
			Settings{Name: "Sam", Port: 9000, IdleExit: 2.5, OpenBrowser: false}},
		{"flags win", Config{Name: &s, Port: &i, IdleExitMinutes: &f}, Flags{Port: &flagPort, IdleExit: &flagIdle, NoOpen: true},
			Settings{Name: "Sam", Port: 8811, PortStrict: true, IdleExit: 0, OpenBrowser: false}},
		{"file port is a preference, not strict", Config{Port: &i}, Flags{},
			Settings{Port: 9000, IdleExit: 15, OpenBrowser: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.cfg, tc.flags); got != tc.want {
				t.Errorf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
}
