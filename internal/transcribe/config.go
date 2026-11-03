package transcribe

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/userdir"
)

// Engines for [transcribe] engine.
const (
	EngineAuto    = "auto" // the command when one is configured, else whisper.cpp
	EngineWhisper = "whisper.cpp"
	EngineCommand = "command"
)

// ErrNoWhisper is returned when whisper-cli can't be found.
var ErrNoWhisper = errors.New("whisper.cpp not installed: run scripts/install-whisper.sh")

// Env is what FromSettings reads from the system. Zero fields get the real thing.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Log      io.Writer // model download progress and fallback notices; nil = os.Stderr
}

func (e Env) withDefaults() Env {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.LookPath == nil {
		e.LookPath = exec.LookPath
	}
	if e.Log == nil {
		e.Log = os.Stderr
	}
	return e
}

// FromConfig returns the Transcriber that the config's [transcribe] table asks for (spec/notes.md, "Config
// additions"). It checks that the engine can run (ffmpeg, whisper-cli, the model name) but downloads nothing: models
// are fetched on first use.
func FromConfig(cfg config.Config) (Transcriber, error) {
	return FromSettings(cfg.Transcribe, Env{})
}

// FromSettings is FromConfig for one [transcribe] table, with the system access in env.
func FromSettings(c config.Transcribe, env Env) (Transcriber, error) {
	env = env.withDefaults()
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	engine, command := str(c.Engine), str(c.Command)
	if engine == "" {
		engine = EngineAuto
	}
	if _, err := env.LookPath("ffmpeg"); err != nil {
		return nil, errors.New("ffmpeg not found on $PATH: transcription needs it to decode the recorded audio")
	}
	switch engine {
	case EngineCommand:
		return NewCommand(command, "")
	case EngineAuto:
		if command != "" {
			return NewCommand(command, "")
		}
	case EngineWhisper:
	default:
		return nil, fmt.Errorf("unknown transcribe engine %q (auto, whisper.cpp or command)", engine)
	}
	cli, err := FindWhisperCLI(str(c.WhisperCLI), env)
	if err != nil {
		return nil, err
	}
	dir, err := DefaultModelDir(env.Getenv)
	if err != nil {
		return nil, err
	}
	threads := 0
	if s := env.Getenv("RETAKE_WHISPER_THREADS"); s != "" {
		if threads, err = strconv.Atoi(s); err != nil || threads < 1 {
			return nil, fmt.Errorf("RETAKE_WHISPER_THREADS=%q isn't a positive number", s)
		}
	}
	return NewWhisperCPP(WhisperOptions{CLI: cli, Model: str(c.Model), Device: str(c.Device), Threads: threads,
		Models: &ModelStore{Dir: dir, Progress: env.Log}, Log: env.Log})
}

// InstallDir is where scripts/install-whisper.sh puts whisper.cpp: $XDG_DATA_HOME/retake/whisper.cpp, by default
// ~/.local/share/retake/whisper.cpp (%LOCALAPPDATA%\retake\whisper.cpp on Windows).
func InstallDir(getenv func(string) string) string {
	if x := getenv("XDG_DATA_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "retake", "whisper.cpp")
	}
	if d := userdir.Local(getenv); d != "" {
		return filepath.Join(d, "retake", "whisper.cpp")
	}
	home := userdir.Home(getenv)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".local", "share", "retake", "whisper.cpp")
}

// FindWhisperCLI finds whisper-cli: the configured path (a bare name is looked up on $PATH), else whisper-cli on
// $PATH, else install-whisper.sh's install folder.
func FindWhisperCLI(configured string, env Env) (string, error) {
	env = env.withDefaults()
	if configured != "" {
		if !strings.ContainsAny(configured, `/\`) {
			p, err := env.LookPath(configured)
			if err != nil {
				return "", fmt.Errorf("whisper_cli = %q: not found on $PATH", configured)
			}
			return p, nil
		}
		if st, err := os.Stat(configured); err != nil || st.IsDir() {
			return "", fmt.Errorf("whisper_cli = %q: no such file", configured)
		}
		return configured, nil
	}
	if p, err := env.LookPath("whisper-cli"); err == nil {
		return p, nil
	}
	name := "whisper-cli"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(InstallDir(env.Getenv), "bin", name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, nil
	}
	return "", fmt.Errorf("%w (or set whisper_cli in [transcribe])", ErrNoWhisper)
}
