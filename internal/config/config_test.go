package config

import (
	"os"
	"path/filepath"
	"slices"
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
		{name: "all keys", in: "name = \"Ana\"\nport = 8811\nidle_exit_minutes = 15\nopen_browser = false\n",
			check: func(t *testing.T, c Config) {
				if *c.Name != "Ana" || *c.Port != 8811 || *c.IdleExitMinutes != 15 || *c.OpenBrowser {
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
		{name: "share table", in: "[share]\ncloudflared = \"/opt/cf/cloudflared\"\ntailscale = \"/opt/ts/tailscale\"\n",
			check: func(t *testing.T, c Config) {
				if *c.Share.Cloudflared != "/opt/cf/cloudflared" || *c.Share.Tailscale != "/opt/ts/tailscale" {
					t.Errorf("%+v", c.Share)
				}
			}},
		{name: "where agents open", in: "open_agents_in = \"herdr\"\n[[agents]]\nid = \"cc\"\ncommand = [\"claude\", \"-p\"]\n" +
			"open_in = \"tmux\"\ninteractive = [\"claude\"]\n[[agents]]\nid = \"bg\"\ncommand = [\"x\"]\n",
			check: func(t *testing.T, c Config) {
				if *c.OpenAgentsIn != OpenHerdr || c.Agents[0].OpenIn != OpenTmux || c.Agents[0].Interactive[0] != "claude" ||
					c.Agents[1].OpenIn != "" || c.Agents[1].Interactive != nil {
					t.Errorf("%+v", c)
				}
			}},
		{name: "render table", in: "[render]\nencoder = \"nvenc\"\n", check: func(t *testing.T, c Config) {
			if *c.Render.Encoder != "nvenc" {
				t.Errorf("%+v", c.Render)
			}
		}},
		{name: "render unset", in: "", check: func(t *testing.T, c Config) {
			if c.Render.Encoder != nil {
				t.Errorf("%+v", c.Render)
			}
		}},
		{name: "bad encoder", in: "[render]\nencoder = \"h265\"", wantErr: "render.encoder"},
		{name: "bad open_agents_in", in: `open_agents_in = "kitty"`, wantErr: "open_agents_in"},
		{name: "bad open_in", in: "[[agents]]\nid = \"a\"\ncommand = [\"x\"]\nopen_in = \"window\"\n", wantErr: "open_in"},
		{name: "open_in herdr needs interactive", in: "[[agents]]\nid = \"a\"\ncommand = [\"x\"]\nopen_in = \"herdr\"\n",
			wantErr: "needs an interactive command"},
		{name: "empty interactive", in: "[[agents]]\nid = \"a\"\ncommand = [\"x\"]\ninteractive = []\n", wantErr: "interactive is empty"},
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
	s, i, f, b := "Ana", 9000, 2.5, false
	flagPort, flagIdle := 8811, 0.0
	for _, tc := range []struct {
		name  string
		cfg   Config
		flags Flags
		want  Settings
	}{
		{"defaults", Config{}, Flags{}, Settings{Port: 8777, IdleExit: 15, OpenBrowser: true}},
		{"file", Config{Name: &s, Port: &i, IdleExitMinutes: &f, OpenBrowser: &b}, Flags{},
			Settings{Name: "Ana", Port: 9000, IdleExit: 2.5, OpenBrowser: false}},
		{"flags win", Config{Name: &s, Port: &i, IdleExitMinutes: &f}, Flags{Port: &flagPort, IdleExit: &flagIdle, NoOpen: true},
			Settings{Name: "Ana", Port: 8811, PortStrict: true, IdleExit: 0, OpenBrowser: false}},
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

func TestParseExtensions(t *testing.T) {
	in := `[server]
extensions = ["scripts", "board"]

[extensions]
enabled = ["scripts", "hello"]
enable = ["typo"]

[extensions.pins]
hello = "b3:00"

[extensions.scripts]
model = "small"
words = 3
`
	c, warnings, err := Parse(in)
	if err != nil {
		t.Fatal(err)
	}
	e := c.Extensions
	if !slices.Equal(e.Enabled, []string{"scripts", "hello"}) || e.Pins["hello"] != "b3:00" ||
		e.Settings["scripts"]["model"] != "small" || e.Settings["scripts"]["words"] != int64(3) {
		t.Errorf("extensions %+v", e)
	}
	if !slices.Equal(c.Server.Extensions, []string{"scripts", "board"}) {
		t.Errorf("server extensions %v", c.Server.Extensions)
	}
	if !slices.Equal(warnings, []string{`unknown key "extensions.enable"`}) {
		t.Errorf("warnings %v", warnings)
	}
	for _, bad := range []string{"[extensions]\nenabled = \"scripts\"\n", "[extensions]\nenabled = [1]\n",
		"[extensions]\npins = 3\n", "[extensions.pins]\nx = 1\n", "extensions = 3\n"} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("no error for %q", bad)
		}
	}
	if c, _, err := Parse(""); err != nil || c.Extensions.Enabled != nil || c.Extensions.Pins != nil {
		t.Errorf("empty: %+v %v", c.Extensions, err)
	}
}

func TestSetExtension(t *testing.T) {
	pin := "b3:" + strings.Repeat("ab", 32)
	pin2 := "b3:" + strings.Repeat("cd", 32)
	for _, tc := range []struct {
		name, before string
		id           string
		on           bool
		pin          string
		after        string
	}{
		{name: "a new file", id: "scripts", on: true, after: "[extensions]\nenabled = [\"scripts\"]\n"},
		{name: "keeps the rest", id: "scripts", on: true,
			before: "# my config\nname = \"Ana\" # me\n\n[transcribe]\nmodel = \"small\"\n",
			after:  "# my config\nname = \"Ana\" # me\n\n[transcribe]\nmodel = \"small\"\n\n[extensions]\nenabled = [\"scripts\"]\n"},
		{name: "adds to the list", id: "hello", on: true,
			before: "[extensions] # tools\nenabled = [\"scripts\"] # on\n\n[render]\nencoder = \"x264\"\n",
			after:  "[extensions] # tools\nenabled = [\"scripts\", \"hello\"]  # on\n\n[render]\nencoder = \"x264\"\n"},
		{name: "a list over lines", id: "scripts", on: false,
			before: "[extensions]\nenabled = [\n  \"scripts\", # the first\n  \"hello\",\n]\nother = 1\n",
			after:  "[extensions]\nenabled = [\"hello\"]\nother = 1\n"},
		{name: "a table without enabled", id: "hello", on: true,
			before: "[extensions]\n\n[extensions.hello]\ngreeting = \"hi\"\n",
			after:  "[extensions]\nenabled = [\"hello\"]\n\n[extensions.hello]\ngreeting = \"hi\"\n"},
		{name: "before its settings table", id: "hello", on: true,
			before: "port = 9000\n\n[extensions.hello]\ngreeting = \"hi\"\n",
			after:  "port = 9000\n\n[extensions]\nenabled = [\"hello\"]\n\n[extensions.hello]\ngreeting = \"hi\"\n"},
		{name: "a dotted key", id: "hello", on: true,
			before: "extensions.enabled = [\"scripts\"]\nport = 9000\n",
			after:  "extensions.enabled = [\"scripts\", \"hello\"]\nport = 9000\n"},
		{name: "already on", id: "scripts", on: true,
			before: "[extensions]\nenabled = [\"scripts\", \"hello\"]\n",
			after:  "[extensions]\nenabled = [\"scripts\", \"hello\"]\n"},
		{name: "a pin", id: "proj", on: true, pin: pin, before: "port = 9000\n",
			after: "port = 9000\n\n[extensions]\nenabled = [\"proj\"]\n\n[extensions.pins]\nproj = \"" + pin + "\"\n"},
		{name: "a new pin", id: "proj", on: true, pin: pin2,
			before: "[extensions]\nenabled = [\"proj\"]\n\n[extensions.pins]\nproj = \"" + pin + "\" # old\nother = \"" + pin + "\"\n",
			after:  "[extensions]\nenabled = [\"proj\"]\n\n[extensions.pins]\nproj = \"" + pin2 + "\"  # old\nother = \"" + pin + "\"\n"},
		{name: "a pin in its table", id: "proj", on: true, pin: pin2,
			before: "[extensions]\nenabled = []\n\n[extensions.pins]\nother = \"" + pin + "\"\n",
			after:  "[extensions]\nenabled = [\"proj\"]\n\n[extensions.pins]\nproj = \"" + pin2 + "\"\nother = \"" + pin + "\"\n"},
		{name: "off drops the pin", id: "proj", on: false,
			before: "[extensions]\nenabled = [\"proj\", \"scripts\"]\n\n[extensions.pins]\nproj = \"" + pin + "\"\n",
			after:  "[extensions]\nenabled = [\"scripts\"]\n\n[extensions.pins]\n"},
		{name: "off when it isn't there", id: "hello", on: false, before: "port = 9000\n", after: "port = 9000\n"},
		// Ignition is Scripts now (spec/extensions.md, "Renamed ids"): the old id in the file or the command is the new one.
		{name: "the old id on is the new one", id: "ignition", on: true, after: "[extensions]\nenabled = [\"scripts\"]\n"},
		{name: "the old id in the file is already on", id: "scripts", on: true,
			before: "[extensions]\nenabled = [\"ignition\"]\n", after: "[extensions]\nenabled = [\"ignition\"]\n"},
		{name: "off by the new id takes the old one out", id: "scripts", on: false,
			before: "[extensions]\nenabled = [\"ignition\", \"hello\"]\n", after: "[extensions]\nenabled = [\"hello\"]\n"},
		{name: "off by the old id", id: "ignition", on: false,
			before: "[extensions]\nenabled = [\"scripts\"]\n", after: "[extensions]\nenabled = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "retake", "config.toml")
			if tc.before != "" {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(tc.before), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if err := SetExtension(p, tc.id, tc.on, tc.pin); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.after {
				t.Errorf("file:\n%s\nwant:\n%s", b, tc.after)
			}
			st, _ := os.Stat(p)
			if want := map[bool]os.FileMode{true: 0o600, false: 0o640}[tc.before == ""]; st.Mode().Perm() != want {
				t.Errorf("mode %v", st.Mode())
			}
			if _, _, err := Load(p); err != nil {
				t.Errorf("the file doesn't load: %v", err)
			}
		})
	}
}

func TestSetExtensionRefuses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := SetExtension(p, "Bad_ID", true, ""); err == nil {
		t.Error("bad id")
	}
	if err := SetExtension(p, "ok", true, "b3:short"); err == nil {
		t.Error("bad pin")
	}
	for _, before := range []string{"name = \n", "extensions = { enabled = [\"a\"] }\n"} {
		if err := os.WriteFile(p, []byte(before), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SetExtension(p, "hello", true, ""); err == nil {
			t.Errorf("edited %q", before)
		}
		if b, _ := os.ReadFile(p); string(b) != before {
			t.Errorf("changed %q to %q", before, b)
		}
	}
	// Turning off what's off in no file makes no file.
	none := filepath.Join(dir, "none", "config.toml")
	if err := SetExtension(none, "hello", false, ""); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Dir(none)); err == nil {
		t.Error("made a folder for nothing")
	}
}

func TestSetTop(t *testing.T) {
	for _, tc := range []struct {
		name, before, key, value, after string
	}{
		{name: "a new file", key: "default_agent", value: "codex", after: "default_agent = \"codex\"\n"},
		{name: "after the top keys", key: "open_agents_in", value: "herdr",
			before: "# mine\nname = \"Ana\"\n\n# how it renders\n[render]\nencoder = \"x264\"\n",
			after:  "# mine\nname = \"Ana\"\nopen_agents_in = \"herdr\"\n\n# how it renders\n[render]\nencoder = \"x264\"\n"},
		{name: "a file that starts with a table", key: "fallback_agent", value: "claude",
			before: "[render]\nencoder = \"x264\"\n",
			after:  "fallback_agent = \"claude\"\n\n[render]\nencoder = \"x264\"\n"},
		{name: "replaces, keeps its comment", key: "default_agent", value: "claude",
			before: "default_agent = \"codex\" # pick\nport = 9000\n",
			after:  "default_agent = \"claude\"  # pick\nport = 9000\n"},
		{name: "empty removes it", key: "fallback_agent", value: "",
			before: "fallback_agent = \"codex\"\nport = 9000\n", after: "port = 9000\n"},
		{name: "the videos folder", key: "folder", value: `~/My "Videos"`,
			before: "port = 9000\n", after: "port = 9000\nfolder = \"~/My \\\"Videos\\\"\"\n"},
		{name: "a table's own key is another key", key: "default_agent", value: "codex",
			before: "[extensions.x]\ndefault_agent = \"y\"\n",
			after:  "default_agent = \"codex\"\n\n[extensions.x]\ndefault_agent = \"y\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "retake", "config.toml")
			if tc.before != "" {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(tc.before), 0o640); err != nil {
					t.Fatal(err)
				}
			}
			if err := SetTop(p, tc.key, tc.value); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.after {
				t.Errorf("file:\n%s\nwant:\n%s", b, tc.after)
			}
			c, _, err := Load(p)
			if err != nil {
				t.Fatalf("the file doesn't load: %v", err)
			}
			if got := topValue(c, tc.key); got != tc.value {
				t.Errorf("%s = %q", tc.key, got)
			}
		})
	}
}

func TestSetTopRefuses(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	for _, tc := range []struct{ key, value string }{
		{"port", "1"}, {"default_agent", "no spaces"}, {"open_agents_in", "screen"}, {"fallback_agent", "a\"b"},
	} {
		if err := SetTop(p, tc.key, tc.value); err == nil {
			t.Errorf("%s = %q was written", tc.key, tc.value)
		}
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused value made the file")
	}
	if err := os.WriteFile(p, []byte("name = \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTop(p, "default_agent", "codex"); err == nil {
		t.Error("edited a broken file")
	}
	if _, _, err := Parse("default_agent = \"a b\"\n"); err == nil {
		t.Error("a bad default_agent parsed")
	}
}

// Ignition is Scripts now (spec/extensions.md, "Renamed ids"): a config written before reads as the new id.
func TestRenamedExtensions(t *testing.T) {
	c, warnings, err := Parse(`[server]
extensions = ["ignition", "scripts", "hello"]

[extensions]
enabled = ["ignition", "hello", "scripts"]

[extensions.pins]
ignition = "b3:` + strings.Repeat("ab", 32) + `"

[extensions.ignition]
voice = "mine"

[extensions.hello]
greeting = "hi"
`)
	if err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	if want := []string{"scripts", "hello"}; !slices.Equal(c.Extensions.Enabled, want) {
		t.Errorf("enabled %v, want %v", c.Extensions.Enabled, want)
	}
	if want := []string{"scripts", "hello"}; !slices.Equal(c.Server.Extensions, want) {
		t.Errorf("server extensions %v, want %v", c.Server.Extensions, want)
	}
	if _, old := c.Extensions.Pins["ignition"]; old || c.Extensions.Pins["scripts"] == "" {
		t.Errorf("pins %v", c.Extensions.Pins)
	}
	if _, old := c.Extensions.Settings["ignition"]; old || c.Extensions.Settings["scripts"]["voice"] != "mine" ||
		c.Extensions.Settings["hello"]["greeting"] != "hi" {
		t.Errorf("settings %v", c.Extensions.Settings)
	}
	// A table under the new id wins over the old one's.
	c, _, err = Parse("[extensions.ignition]\nvoice = \"old\"\n\n[extensions.scripts]\nvoice = \"new\"\n")
	if err != nil || c.Extensions.Settings["scripts"]["voice"] != "new" || len(c.Extensions.Settings) != 1 {
		t.Errorf("both tables: %v %v", c.Extensions.Settings, err)
	}
	if ExtensionID("ignition") != "scripts" || ExtensionID("hello") != "hello" || ExtensionIDs(nil) != nil {
		t.Error("ExtensionID")
	}
}
