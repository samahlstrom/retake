// Package config reads $XDG_CONFIG_HOME/retake/config.toml (default ~/.config/retake/config.toml). Every key is
// optional, and command-line flags override the file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/BurntSushi/toml"
)

// Config is the file's contents. A nil field wasn't set.
type Config struct {
	Name            *string  `toml:"name"`              // author name written into session.json
	Port            *int     `toml:"port"`              // preferred port (the next free one is used if busy)
	IdleExitMinutes *float64 `toml:"idle_exit_minutes"` // 0 = never
	OpenBrowser     *bool    `toml:"open_browser"`
	// FindAgents adds Claude Code and Codex found on PATH to the agents (default true); false offers only Agents.
	FindAgents *bool `toml:"find_agents"`
	// OpenAgentsIn is where agents run (spec/automation.md, "Watching the agents"): "background" (default), "herdr"
	// or "tmux". An [[agents]] entry's open_in wins.
	OpenAgentsIn *string `toml:"open_agents_in"`
	Agents       []Agent `toml:"agents"` // [[agents]]: what retake open and retake worker may run

	Transcribe Transcribe `toml:"transcribe"`
	Share      Share      `toml:"share"`
	Edit       Edit       `toml:"edit"`
	Server     Server     `toml:"server"`
}

// Server is the [server] table (spec/server.md, "Running it"): retake serve's settings, and the admin commands'
// (retake user, retake project). Flags and RETAKE_* environment variables override it.
type Server struct {
	Data           *string  `toml:"data"`
	Listen         *string  `toml:"listen"`
	PublicURL      *string  `toml:"public_url"`
	TrustedProxies []string `toml:"trusted_proxies"`
	Database       *string  `toml:"database"`
	S3Endpoint     *string  `toml:"s3_endpoint"`
	S3Bucket       *string  `toml:"s3_bucket"`
	S3AccessKey    *string  `toml:"s3_access_key"`
	S3SecretKey    *string  `toml:"s3_secret_key"`
	S3Region       *string  `toml:"s3_region"`
	S3Insecure     *bool    `toml:"s3_insecure"`
	SMTPURL        *string  `toml:"smtp_url"`
	SMTPFrom       *string  `toml:"smtp_from"`
}

// Agent is one [[agents]] entry (docs/config.md): a program the editing worker runs for a task. Env is added to its
// environment; a value may name the worker's own variables ($VAR, ${VAR}), so keys needn't be written here. An
// entry whose id is "claude" or "codex" replaces the one found on PATH. OpenIn overrides open_agents_in for this
// agent; Interactive is its command in herdr or tmux, where it runs as an interactive session.
type Agent struct {
	ID          string            `toml:"id"`
	Name        string            `toml:"name"`
	Model       string            `toml:"model"`
	Command     []string          `toml:"command"`
	Env         map[string]string `toml:"env"`
	OpenIn      string            `toml:"open_in"`
	Interactive []string          `toml:"interactive"`
}

// OpenIn values: where an agent runs (spec/automation.md, "Watching the agents").
const (
	OpenBackground = "background"
	OpenHerdr      = "herdr"
	OpenTmux       = "tmux"
)

// ValidOpenIn reports whether s is a place an agent can run.
func ValidOpenIn(s string) bool { return s == OpenBackground || s == OpenHerdr || s == OpenTmux }

// Share is the [share] table (spec/team.md): the tunnel binaries `retake share` runs.
type Share struct {
	Cloudflared *string `toml:"cloudflared"` // unset or "" = cloudflared on $PATH
	Tailscale   *string `toml:"tailscale"`   // unset or "" = tailscale on $PATH
}

// Edit is the [edit] table (spec/bridge.md): the editor engine that renders timeline projects.
type Edit struct {
	RetakeEdit *string `toml:"retake_edit"` // the retake-edit binary; unset = $RETAKE_EDIT, $PATH, editor/target/release
}

// Transcribe is the [transcribe] table (spec/notes.md, "Config additions"). internal/transcribe reads it.
type Transcribe struct {
	Engine     *string `toml:"engine"`      // auto | whisper.cpp | command
	Model      *string `toml:"model"`       // whisper.cpp model name, or the path of a ggml .bin file
	Device     *string `toml:"device"`      // auto | cuda | cpu
	Command    *string `toml:"command"`     // engine = "command": the program (and arguments) to run
	WhisperCLI *string `toml:"whisper_cli"` // the whisper-cli binary; unset = $PATH, then the install-whisper.sh folder
}

var agentID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// Path returns the config file's path. getenv is os.Getenv in production.
func Path(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "retake", "config.toml"), nil
	}
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home folder for the config file: %w", err)
		}
		home = h
	}
	return filepath.Join(home, ".config", "retake", "config.toml"), nil
}

// Load reads the config file at path. A missing file is an empty Config. Unknown keys come back as warnings.
func Load(path string) (Config, []string, error) {
	var c Config
	b, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil, nil
	}
	if err != nil {
		return c, nil, err
	}
	return Parse(string(b))
}

// Parse parses config TOML.
func Parse(s string) (Config, []string, error) {
	var c Config
	md, err := toml.Decode(s, &c)
	if err != nil {
		return Config{}, nil, err
	}
	var warnings []string
	seen := map[string]bool{}
	for _, k := range md.Undecoded() {
		if len(k) == 0 || seen[k[0]] {
			continue // a table's keys come after the table itself
		}
		if len(k) == 1 {
			seen[k[0]] = true
		}
		warnings = append(warnings, fmt.Sprintf("unknown key %q", k.String()))
	}
	sort.Strings(warnings)
	if c.Port != nil && (*c.Port < 1 || *c.Port > 65535) {
		return Config{}, warnings, fmt.Errorf("port %d is out of range (1-65535)", *c.Port)
	}
	if c.IdleExitMinutes != nil && *c.IdleExitMinutes < 0 {
		return Config{}, warnings, fmt.Errorf("idle_exit_minutes %g is negative", *c.IdleExitMinutes)
	}
	if e := c.Transcribe.Engine; e != nil && *e != "auto" && *e != "whisper.cpp" && *e != "command" {
		return Config{}, warnings, fmt.Errorf("transcribe.engine %q isn't auto, whisper.cpp or command", *e)
	}
	if d := c.Transcribe.Device; d != nil && *d != "auto" && *d != "cuda" && *d != "cpu" {
		return Config{}, warnings, fmt.Errorf("transcribe.device %q isn't auto, cuda or cpu", *d)
	}
	if o := c.OpenAgentsIn; o != nil && !ValidOpenIn(*o) {
		return Config{}, warnings, fmt.Errorf("open_agents_in %q isn't background, herdr or tmux", *o)
	}
	ids := map[string]bool{}
	for i, a := range c.Agents {
		if !agentID.MatchString(a.ID) || ids[a.ID] {
			return Config{}, warnings, fmt.Errorf("agents[%d]: id %q must be unique letters, digits, - or _", i, a.ID)
		}
		ids[a.ID] = true
		if len(a.Command) == 0 || a.Command[0] == "" {
			return Config{}, warnings, fmt.Errorf("agents[%d] (%s): command is empty", i, a.ID)
		}
		if a.OpenIn != "" && !ValidOpenIn(a.OpenIn) {
			return Config{}, warnings, fmt.Errorf("agents[%d] (%s): open_in %q isn't background, herdr or tmux", i, a.ID, a.OpenIn)
		}
		if a.Interactive != nil && (len(a.Interactive) == 0 || a.Interactive[0] == "") {
			return Config{}, warnings, fmt.Errorf("agents[%d] (%s): interactive is empty", i, a.ID)
		}
		if (a.OpenIn == OpenHerdr || a.OpenIn == OpenTmux) && a.Interactive == nil {
			return Config{}, warnings, fmt.Errorf("agents[%d] (%s): open_in %q needs an interactive command", i, a.ID, a.OpenIn)
		}
		if c.Agents[i].Name == "" {
			c.Agents[i].Name = a.ID
		}
	}
	return c, warnings, nil
}

// Defaults for settings that neither the file nor a flag set.
const (
	DefaultPort     = 8777
	DefaultIdleExit = 15.0
	PortTries       = 20 // the default port and the next 19 (8777-8796)
)

// Flags are the command-line values that override the file. A nil field wasn't given.
type Flags struct {
	Port     *int
	IdleExit *float64
	NoOpen   bool
}

// Settings are the effective values after defaults, file and flags.
type Settings struct {
	Name        string  // author name; "" = use the OS user
	Port        int     // the port to try first
	PortStrict  bool    // --port given: that port or exit 2, no fallback
	IdleExit    float64 // minutes; 0 = never
	OpenBrowser bool
}

// Resolve combines defaults, the config file and flags (flags win).
func Resolve(c Config, f Flags) Settings {
	s := Settings{Port: DefaultPort, IdleExit: DefaultIdleExit, OpenBrowser: true}
	if c.Name != nil {
		s.Name = *c.Name
	}
	if c.Port != nil {
		s.Port = *c.Port
	}
	if c.IdleExitMinutes != nil {
		s.IdleExit = *c.IdleExitMinutes
	}
	if c.OpenBrowser != nil {
		s.OpenBrowser = *c.OpenBrowser
	}
	if f.Port != nil {
		s.Port, s.PortStrict = *f.Port, true
	}
	if f.IdleExit != nil {
		s.IdleExit = *f.IdleExit
	}
	if f.NoOpen {
		s.OpenBrowser = false
	}
	return s
}
