package transcribe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/config"
)

func ptr(s string) *string { return &s }

// testEnv has ffmpeg on $PATH, plus the names in path; XDG folders point into a temp dir.
func testEnv(t *testing.T, path map[string]string, vars map[string]string) (Env, string) {
	t.Helper()
	root := t.TempDir()
	m := map[string]string{"XDG_CACHE_HOME": filepath.Join(root, "cache"), "XDG_DATA_HOME": filepath.Join(root, "data"),
		"HOME": filepath.Join(root, "home")}
	for k, v := range vars {
		m[k] = v
	}
	return Env{
		Getenv: func(k string) string { return m[k] },
		LookPath: func(name string) (string, error) {
			if name == "ffmpeg" {
				return "/usr/bin/ffmpeg", nil
			}
			if p, ok := path[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Log: &strings.Builder{},
	}, root
}

// cpuCLI writes a stand-in whisper-cli that has no CUDA in it.
func cpuCLI(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, "#!/bin/sh\n")
	return path
}

func TestFromSettings(t *testing.T) {
	cli := cpuCLI(t, filepath.Join(t.TempDir(), "bin", "whisper-cli"))
	for _, tc := range []struct {
		name string
		set  config.Transcribe
		path map[string]string
		vars map[string]string
		want string // Name(), or the error's text
		err  bool
	}{
		{name: "defaults: whisper-cli on $PATH, small on the CPU", path: map[string]string{"whisper-cli": cli},
			want: "whisper.cpp small (cpu)"},
		{name: "configured path and model", set: config.Transcribe{WhisperCLI: &cli, Model: ptr("tiny"), Device: ptr("cpu")},
			want: "whisper.cpp tiny (cpu)"},
		{name: "explicit whisper.cpp engine", set: config.Transcribe{Engine: ptr("whisper.cpp"), Command: ptr("ignored")},
			path: map[string]string{"whisper-cli": cli}, want: "whisper.cpp small (cpu)"},
		{name: "command engine", set: config.Transcribe{Engine: ptr("command"), Command: ptr("/opt/fw/words.py --model x")},
			want: "command words.py --model x"},
		{name: "auto picks a configured command", set: config.Transcribe{Command: ptr("fw")}, want: "command fw"},
		{name: "command engine without a command", set: config.Transcribe{Engine: ptr("command")}, err: true,
			want: "[transcribe] command"},
		{name: "not installed", err: true, want: "whisper.cpp not installed: run scripts/install-whisper.sh"},
		{name: "configured path missing", set: config.Transcribe{WhisperCLI: ptr("/nowhere/whisper-cli")}, err: true,
			want: `whisper_cli = "/nowhere/whisper-cli": no such file`},
		{name: "configured name not on $PATH", set: config.Transcribe{WhisperCLI: ptr("whisper-cpp")}, err: true,
			want: "not found on $PATH"},
		{name: "unknown model", set: config.Transcribe{Model: ptr("large-v9")}, path: map[string]string{"whisper-cli": cli},
			err: true, want: `unknown whisper model "large-v9"`},
		{name: "cuda forced on a CPU build", set: config.Transcribe{Device: ptr("cuda")},
			path: map[string]string{"whisper-cli": cli}, err: true, want: "built without CUDA"},
		{name: "bad engine", set: config.Transcribe{Engine: ptr("vosk")}, err: true, want: `unknown transcribe engine "vosk"`},
		{name: "threads", path: map[string]string{"whisper-cli": cli}, vars: map[string]string{"RETAKE_WHISPER_THREADS": "3"},
			want: "whisper.cpp small (cpu)"},
		{name: "bad threads", path: map[string]string{"whisper-cli": cli}, vars: map[string]string{"RETAKE_WHISPER_THREADS": "lots"},
			err: true, want: "RETAKE_WHISPER_THREADS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, root := testEnv(t, tc.path, tc.vars)
			tr, err := FromSettings(tc.set, env)
			if tc.err {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("err = %v, want %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tr.Name() != tc.want {
				t.Errorf("Name = %q, want %q", tr.Name(), tc.want)
			}
			if w, ok := tr.(*WhisperCPP); ok {
				if w.opts.Models.Dir != filepath.Join(root, "cache", "retake", "models") {
					t.Errorf("models in %s", w.opts.Models.Dir)
				}
				if tc.vars["RETAKE_WHISPER_THREADS"] == "3" && w.opts.Threads != 3 {
					t.Errorf("threads = %d", w.opts.Threads)
				}
			}
		})
	}
}

func TestFromSettingsNeedsFFmpeg(t *testing.T) {
	env := Env{Getenv: func(string) string { return "" }, LookPath: func(string) (string, error) { return "", errors.New("no") }}
	if _, err := FromSettings(config.Transcribe{Command: ptr("fw")}, env); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err = %v", err)
	}
}

func TestFindWhisperCLI(t *testing.T) {
	env, root := testEnv(t, nil, nil)
	if _, err := FindWhisperCLI("", env); !errors.Is(err, ErrNoWhisper) {
		t.Errorf("nothing installed: %v", err)
	}
	// install-whisper.sh's folder is the last resort.
	installed := cpuCLI(t, filepath.Join(root, "data", "retake", "whisper.cpp", "bin", "whisper-cli"))
	if p, err := FindWhisperCLI("", env); err != nil || p != installed {
		t.Errorf("install dir: %q %v", p, err)
	}
	// $PATH comes before it, and the config before both.
	onPath := cpuCLI(t, filepath.Join(root, "usr", "bin", "whisper-cli"))
	env2, _ := testEnv(t, map[string]string{"whisper-cli": onPath, "my-whisper": "/opt/bin/my-whisper"}, nil)
	env2.Getenv = env.Getenv
	if p, err := FindWhisperCLI("", env2); err != nil || p != onPath {
		t.Errorf("$PATH: %q %v", p, err)
	}
	if p, err := FindWhisperCLI(installed, env2); err != nil || p != installed {
		t.Errorf("configured: %q %v", p, err)
	}
	if p, err := FindWhisperCLI("my-whisper", env2); err != nil || p != "/opt/bin/my-whisper" {
		t.Errorf("configured name: %q %v", p, err)
	}
	if _, err := FindWhisperCLI(filepath.Dir(installed), env2); err == nil {
		t.Error("a folder accepted as whisper-cli")
	}
}

func TestInstallDir(t *testing.T) {
	if d := InstallDir(env(map[string]string{"XDG_DATA_HOME": "/x/data", "HOME": "/h"})); d != filepath.Join("/x/data", "retake", "whisper.cpp") {
		t.Errorf("XDG: %q", d)
	}
	if d := InstallDir(env(map[string]string{"XDG_DATA_HOME": "rel", "HOME": "/h"})); d != filepath.Join("/h", ".local", "share", "retake", "whisper.cpp") {
		t.Errorf("HOME: %q", d)
	}
}
