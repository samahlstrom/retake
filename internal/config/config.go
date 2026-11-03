// Package config reads $XDG_CONFIG_HOME/retake/config.toml (default ~/.config/retake/config.toml). Every key is
// optional, and command-line flags override the file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

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
	// DefaultAgent is the agent id a project edits with (spec/home.md, "Settings"): the worker offers it first, and the
	// settings page sets it on every project. FallbackAgent is the one to switch to when its usage runs out; unset or
	// "" pauses and asks.
	DefaultAgent  *string `toml:"default_agent"`
	FallbackAgent *string `toml:"fallback_agent"`
	Agents        []Agent `toml:"agents"` // [[agents]]: what retake open and retake worker may run

	Transcribe Transcribe `toml:"transcribe"`
	Share      Share      `toml:"share"`
	Edit       Edit       `toml:"edit"`
	Render     Render     `toml:"render"`
	Server     Server     `toml:"server"`
	Extensions Extensions `toml:"extensions"`
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
	RelayCache     *string  `toml:"relay_cache"`
	// Downloads is who may download a version's MP4 (spec/api.md, "Download"): "everyone" who can watch (default),
	// "reviewers" or "off". retake open and retake share read it too.
	Downloads *string `toml:"downloads"`
	// Extensions are the extensions the server allows, for everyone (spec/extensions.md, "Turning them on").
	Extensions []string `toml:"extensions"`
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

// Render is the [render] table (editor/README.md, "Encoders"): how retake-edit renders on this machine. Retake passes
// it to retake-edit as RETAKE_ENCODER.
type Render struct {
	// Encoder is the H.264 encoder: auto (default: NVENC on NVIDIA, VideoToolbox on macOS, else x264), x264, nvenc,
	// videotoolbox, qsv or vaapi.
	Encoder *string `toml:"encoder"`
}

// DownloadsValues are the values [server] downloads takes.
var DownloadsValues = []string{"everyone", "reviewers", "off"}

// Encoders are the values [render] encoder takes.
var Encoders = []string{"auto", "x264", "nvenc", "videotoolbox", "qsv", "vaapi"}

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
	c.Server.Extensions = ExtensionIDs(c.Server.Extensions)
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
	for _, k := range c.Extensions.unknown {
		warnings = append(warnings, fmt.Sprintf("unknown key %q", "extensions."+k))
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
	if e := c.Render.Encoder; e != nil && !slices.Contains(Encoders, *e) {
		return Config{}, warnings, fmt.Errorf("render.encoder %q isn't auto, x264, nvenc, videotoolbox, qsv or vaapi", *e)
	}
	if d := c.Server.Downloads; d != nil && !slices.Contains(DownloadsValues, *d) {
		return Config{}, warnings, fmt.Errorf("server.downloads %q isn't everyone, reviewers or off", *d)
	}
	if o := c.OpenAgentsIn; o != nil && !ValidOpenIn(*o) {
		return Config{}, warnings, fmt.Errorf("open_agents_in %q isn't background, herdr or tmux", *o)
	}
	for _, k := range []struct {
		name string
		v    *string
	}{{"default_agent", c.DefaultAgent}, {"fallback_agent", c.FallbackAgent}} {
		if k.v != nil && *k.v != "" && !agentID.MatchString(*k.v) {
			return Config{}, warnings, fmt.Errorf("%s %q isn't an agent id (letters, digits, - or _)", k.name, *k.v)
		}
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

// Extensions is the [extensions] table (spec/extensions.md, "Turning them on"): the extensions retake open, share and
// connect turn on, the content hashes of the project extensions among them, and a settings table per extension
// ([extensions.<id>]), which goes to its server and tools, never to the page.
type Extensions struct {
	Enabled  []string
	Pins     map[string]string         // id -> "b3:<hex>"
	Settings map[string]map[string]any // id -> its table
	unknown  []string                  // keys that are neither enabled, pins nor a table
}

// UnmarshalTOML reads [extensions]: enabled, pins and the per-extension tables.
func (e *Extensions) UnmarshalTOML(data any) error {
	m, ok := data.(map[string]any)
	if !ok {
		return errors.New("extensions must be a table")
	}
	for k, v := range m {
		switch k {
		case "enabled":
			list, ok := v.([]any)
			if !ok {
				return errors.New("extensions.enabled must be a list of extension ids")
			}
			for _, x := range list {
				id, ok := x.(string)
				if !ok {
					return errors.New("extensions.enabled must be a list of extension ids")
				}
				e.Enabled = append(e.Enabled, id)
			}
		case "pins":
			t, ok := v.(map[string]any)
			if !ok {
				return errors.New("extensions.pins must be a table of id = \"b3:…\"")
			}
			e.Pins = map[string]string{}
			for id, x := range t {
				pin, ok := x.(string)
				if !ok {
					return fmt.Errorf("extensions.pins.%s must be a string", id)
				}
				e.Pins[id] = pin
			}
		default:
			t, ok := v.(map[string]any)
			if !ok {
				e.unknown = append(e.unknown, k)
				continue
			}
			if e.Settings == nil {
				e.Settings = map[string]map[string]any{}
			}
			e.Settings[k] = t
		}
	}
	// A renamed extension's old id is its new one (spec/extensions.md, "Renamed ids").
	e.Enabled = ExtensionIDs(e.Enabled)
	renameKeys(e.Pins)
	renameKeys(e.Settings)
	return nil
}

var (
	extIDRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pinRE    = regexp.MustCompile(`^b3:[0-9a-f]{64}$`)
	headerRE = regexp.MustCompile(`^\s*\[\[?\s*([^\]]*?)\s*\]\]?\s*(?:#.*)?$`)
)

// SetExtension turns the extension id on or off in the config file at path (`retake ext enable` and `disable`):
// it edits [extensions] enabled, and the id's line in [extensions.pins] (set to pin when on and pin isn't "",
// removed when off). The rest of the file, comments included, stays as it is; the file and its folder are made when
// missing.
func SetExtension(path, id string, on bool, pin string) error {
	if !extIDRE.MatchString(id) {
		return fmt.Errorf("%q isn't an extension id", id)
	}
	id = ExtensionID(id)
	if pin != "" && !pinRE.MatchString(pin) {
		return fmt.Errorf("pin %q isn't b3:<64 hex digits>", pin)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(b)
	c, _, err := Parse(text)
	if err != nil {
		return fmt.Errorf("%s: %w (fix it first)", path, err)
	}
	enabled := slices.Clone(c.Extensions.Enabled)
	switch {
	case !on:
		enabled = slices.DeleteFunc(enabled, func(s string) bool { return s == id })
	case !slices.Contains(enabled, id):
		enabled = append(enabled, id)
	}
	pins := map[string]string{}
	maps.Copy(pins, c.Extensions.Pins)
	switch {
	case !on:
		delete(pins, id)
	case pin != "":
		pins[id] = pin
	}
	if slices.Equal(enabled, c.Extensions.Enabled) && pins[id] == c.Extensions.Pins[id] {
		return nil // already so
	}
	lines := strings.Split(text, "\n")
	if lines, err = setEnabled(lines, enabled); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for old, to := range RenamedExtensions {
		if to != id {
			continue
		}
		if lines, err = setPin(lines, old, ""); err != nil { // the pin is kept under the new id
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if lines, err = setPin(lines, id, pins[id]); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	out := strings.Join(lines, "\n")
	got, _, err := Parse(out)
	if err != nil || !slices.Equal(got.Extensions.Enabled, enabled) || got.Extensions.Pins[id] != pins[id] {
		return fmt.Errorf("%s: can't edit [extensions] here: set enabled = %s by hand", path, tomlList(enabled))
	}
	return writeConfig(path, out)
}

// TopKeys are the top-level keys SetTop writes (the settings page's, spec/home.md "Settings").
var TopKeys = []string{"default_agent", "fallback_agent", "open_agents_in"}

// SetTop sets a top-level string key of the config file at path to value, or removes its line when value is "" (the
// default). Only TopKeys are written. The rest of the file, comments included, stays as it is; the file and its
// folder are made when missing. The result is parsed again, so a value the file can't hold is refused.
func SetTop(path, key, value string) error {
	if !slices.Contains(TopKeys, key) {
		return fmt.Errorf("%q isn't a key the settings write", key)
	}
	b, err := os.ReadFile(path) //nolint:gosec // the user's own config file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(b)
	if _, _, err := Parse(text); err != nil {
		return fmt.Errorf("%s: %w (fix it first)", path, err)
	}
	lines := strings.Split(text, "\n")
	_, end, _ := region(lines, "")
	line := key + " = " + strconv.Quote(value)
	switch i := keyLine(lines, 0, end, key); {
	case i >= 0 && value == "":
		text2 := strings.Join(lines[i:], "\n")
		stop, ok := valueEnd(text2, strings.IndexByte(text2, '=')+1)
		if !ok {
			return fmt.Errorf("%s: %s's value doesn't end", path, key)
		}
		lines = slices.Delete(lines, i, i+strings.Count(text2[:stop], "\n")+1)
	case i >= 0:
		if lines, err = replaceValue(lines, i, line); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	case value == "":
		return nil // already unset
	default:
		// After the last key of the top table, before the first [table] (and the comments above it).
		at := end
		for at > 0 && (strings.TrimSpace(lines[at-1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[at-1]), "#")) {
			at--
		}
		ins := []string{line}
		if at == 0 && end < len(lines) {
			ins = append(ins, "") // a file that starts with a table: a blank line before it
		}
		lines = slices.Insert(lines, at, ins...)
	}
	out := strings.Join(lines, "\n")
	got, _, err := Parse(out)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if v := topValue(got, key); v != value {
		return fmt.Errorf("%s: can't edit %s here: set %s by hand", path, key, line)
	}
	return writeConfig(path, out)
}

// topValue is the value of one of TopKeys ("" when unset).
func topValue(c Config, key string) string {
	var p *string
	switch key {
	case "default_agent":
		p = c.DefaultAgent
	case "fallback_agent":
		p = c.FallbackAgent
	case "open_agents_in":
		p = c.OpenAgentsIn
	}
	if p == nil {
		return ""
	}
	return *p
}

// region is a table's lines: from after its header to before the next header (or the end). The root table's
// header is -1.
func region(lines []string, table string) (header, end int, ok bool) {
	header = -1
	if table != "" {
		header = slices.IndexFunc(lines, func(l string) bool {
			m := headerRE.FindStringSubmatch(l)
			return m != nil && !strings.HasPrefix(strings.TrimSpace(l), "[[") && dotted(m[1]) == table
		})
		if header < 0 {
			return 0, 0, false
		}
	}
	end = len(lines)
	for i := header + 1; i < len(lines); i++ {
		if headerRE.MatchString(lines[i]) {
			end = i
			break
		}
	}
	return header, end, true
}

// dotted is a table name without spaces around its dots or quotes around its keys.
func dotted(name string) string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(p), `"'`)
	}
	return strings.Join(parts, ".")
}

// keyLine finds the line of key in lines[from:to].
func keyLine(lines []string, from, to int, key string) int {
	re := regexp.MustCompile(`^\s*(?:` + regexp.QuoteMeta(key) + `|"` + regexp.QuoteMeta(key) + `")\s*=`)
	for i := from; i < to; i++ {
		if re.MatchString(lines[i]) {
			return i
		}
	}
	return -1
}

// setEnabled writes enabled = [...] in [extensions], or as extensions.enabled at the top, replacing what's there.
func setEnabled(lines []string, enabled []string) ([]string, error) {
	line := "enabled = " + tomlList(enabled)
	if h, end, ok := region(lines, "extensions"); ok {
		if i := keyLine(lines, h+1, end, "enabled"); i >= 0 {
			return replaceValue(lines, i, line)
		}
		return slices.Insert(lines, h+1, line), nil
	}
	_, end, _ := region(lines, "")
	if i := keyLine(lines, 0, end, "extensions.enabled"); i >= 0 {
		return replaceValue(lines, i, "extensions."+line)
	}
	if keyLine(lines, 0, end, "extensions") >= 0 {
		return nil, errors.New("extensions is an inline table")
	}
	// A new [extensions] table: before its first sub-table, else at the end.
	at := slices.IndexFunc(lines, func(l string) bool {
		m := headerRE.FindStringSubmatch(l)
		return m != nil && strings.HasPrefix(dotted(m[1]), "extensions.")
	})
	block := []string{"[extensions]", line}
	if at >= 0 {
		return slices.Insert(lines, at, append(block, "")...), nil
	}
	return appendBlock(lines, block), nil
}

// setPin sets id's line in [extensions.pins] to pin, or removes it when pin is "".
func setPin(lines []string, id, pin string) ([]string, error) {
	h, end, ok := region(lines, "extensions.pins")
	if !ok {
		if pin == "" {
			return lines, nil
		}
		return appendBlock(lines, []string{"[extensions.pins]", id + " = " + strconv.Quote(pin)}), nil
	}
	i := keyLine(lines, h+1, end, id)
	switch {
	case i >= 0 && pin == "":
		return slices.Delete(lines, i, i+1), nil
	case i >= 0:
		return replaceValue(lines, i, id+" = "+strconv.Quote(pin))
	case pin == "":
		return lines, nil
	}
	return slices.Insert(lines, h+1, id+" = "+strconv.Quote(pin)), nil
}

// appendBlock adds a table at the end of the file, after a blank line.
func appendBlock(lines []string, block []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return append(append(lines, block...), "")
}

// replaceValue replaces the key-value that starts on line i (its value may run over several lines, as an array can)
// with line, keeping a comment after the value.
func replaceValue(lines []string, i int, line string) ([]string, error) {
	text := strings.Join(lines[i:], "\n")
	eq := strings.IndexByte(text, '=')
	end, ok := valueEnd(text, eq+1)
	if !ok {
		return nil, errors.New("a value doesn't end")
	}
	last := i + strings.Count(text[:end], "\n")
	rest := strings.TrimSpace(strings.SplitN(text[end:], "\n", 2)[0])
	if strings.HasPrefix(rest, "#") {
		line += "  " + rest
	}
	return slices.Replace(lines, i, last+1, line), nil
}

// valueEnd is where the TOML value that starts at s[i:] ends: after its closing ] or }, its string, or its word.
func valueEnd(s string, i int) (int, bool) {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	depth := 0
	for i < len(s) {
		switch ch := s[i]; {
		case ch == '"' || ch == '\'':
			q := s[i : i+1]
			if strings.HasPrefix(s[i:], q+q+q) {
				q += q + q
			}
			j := i + len(q)
			for j < len(s) && !strings.HasPrefix(s[j:], q) {
				if s[j] == '\\' && q[0] == '"' {
					j++
				}
				j++
			}
			if j >= len(s) {
				return 0, false
			}
			i = j + len(q)
			if depth == 0 {
				return i, true
			}
			continue
		case ch == '#' && depth > 0:
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		case ch == '[' || ch == '{':
			depth++
		case ch == ']' || ch == '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case depth == 0 && (ch == '\n' || ch == '#'):
			return i, true
		}
		i++
	}
	return i, depth == 0
}

// tomlList is a list of ids as a TOML array.
func tomlList(ids []string) string {
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = strconv.Quote(id)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// writeConfig writes the config file through a temp file and a rename, keeping its mode (0600 for a new one).
func writeConfig(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	mode := fs.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(text)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), mode)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
