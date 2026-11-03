package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/testmedia"
)

func TestParseTranscribe(t *testing.T) {
	a, err := parseTranscribe([]string{"--model", "tiny", "take", "--device=cpu", "--force", "--json", "--prompt", "Zoom",
		"--language", "en", "--engine", "whisper.cpp", "--whisper-cli", "/w", "--command=fw"})
	if err != nil {
		t.Fatal(err)
	}
	if a.path != "take" || *a.set.Model != "tiny" || *a.set.Device != "cpu" || !a.force || !a.json ||
		a.opts.Prompt != "Zoom" || a.opts.Language != "en" || *a.set.Engine != "whisper.cpp" || *a.set.WhisperCLI != "/w" ||
		*a.set.Command != "fw" {
		t.Errorf("%+v", a)
	}
	for _, args := range [][]string{{}, {"a", "b"}, {"a", "--model"}, {"a", "--nope"}} {
		if _, err := parseTranscribe(args); err == nil {
			t.Errorf("%q: no error", args)
		}
	}
	if a, err := parseTranscribe([]string{"-h"}); err != nil || !a.help {
		t.Errorf("-h: %+v %v", a, err)
	}
}

// fakeEngine writes a command engine that ignores the audio and prints one word.
func fakeEngine(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake engine is a shell script")
	}
	p := filepath.Join(t.TempDir(), "fake-words.sh")
	script := "#!/bin/sh\necho '{\"words\": [{\"w\": \" hello\", \"s\": 0.25, \"e\": 0.5, \"p\": 0.9}]}'\n"
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTranscribeCmd(t *testing.T) {
	testmedia.Need(t)
	w := newWorld(t)
	w.config(t, "[transcribe]\nengine = \"command\"\ncommand = "+tomlPath(fakeEngine(t))+"\n")
	take := filepath.Join(w.root, "v6", "clip_v6.review", "2026-10-30_12-00-00")
	if err := os.MkdirAll(take, 0o755); err != nil {
		t.Fatal(err)
	}
	audio := filepath.Join(take, "seg_01.webm")
	if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=f=300:d=1",
		"-c:a", "libopus", audio).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}

	// One file: the words.
	if code := w.run("transcribe", audio); code != ExitOK {
		t.Fatalf("exit %d: %s", code, w.stderr.String())
	}
	if out := w.stdout.String(); !strings.Contains(out, "0.25    0.50  0.90  hello") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(w.stderr.String(), "with command fake-words.sh") {
		t.Errorf("stderr = %q", w.stderr.String())
	}
	w.stdout = &buffer{}
	if code := w.run("transcribe", "--json", audio); code != ExitOK {
		t.Fatalf("--json exit %d: %s", code, w.stderr.String())
	}
	var got struct {
		Model string
		Words []struct{ W string }
	}
	if err := json.Unmarshal([]byte(w.stdout.String()), &got); err != nil || got.Model != "command fake-words.sh" ||
		len(got.Words) != 1 || got.Words[0].W != " hello" {
		t.Errorf("--json = %s (%v)", w.stdout.String(), err)
	}

	// A take: transcript.json, then a summary.
	w.stdout = &buffer{}
	if code := w.run("transcribe", take); code != ExitOK {
		t.Fatalf("take exit %d: %s", code, w.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(take, "transcript.json")); err != nil {
		t.Error(err)
	}
	if out := w.stdout.String(); !strings.Contains(out, "seg_01.webm  1.0 s  command fake-words.sh  hello") {
		t.Errorf("stdout = %q", out)
	}

	// Flags override the config.
	if code := w.run("transcribe", "--engine", "whisper.cpp", "--whisper-cli", "/nowhere/whisper-cli", audio); code != ExitError ||
		!strings.Contains(w.stderr.String(), `whisper_cli = "/nowhere/whisper-cli"`) {
		t.Errorf("exit %d: %s", code, w.stderr.String())
	}
	if code := w.run("transcribe", filepath.Join(w.root, "missing.webm")); code != ExitUsage {
		t.Errorf("missing file: exit %d", code)
	}
	if code := w.run("transcribe"); code != ExitUsage || !strings.Contains(w.stderr.String(), "usage: retake transcribe") {
		t.Errorf("no args: exit %d", code)
	}
}
