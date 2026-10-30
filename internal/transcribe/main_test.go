package transcribe

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// The test binary doubles as a fake whisper-cli and a fake command engine: with RETAKE_FAKE set, TestMain acts the
// part instead of running the tests. Its behaviour comes from more RETAKE_FAKE_* variables.
func TestMain(m *testing.M) {
	switch os.Getenv("RETAKE_FAKE") {
	case "whisper-cli":
		os.Exit(fakeWhisperCLI(os.Args[1:]))
	case "command":
		os.Exit(fakeCommand(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeWhisperCLI records its arguments (RETAKE_FAKE_ARGS, appended, one run per line), prints RETAKE_FAKE_LOG's
// contents to stderr and writes RETAKE_FAKE_JSON's to <-of>.json. Without -ng it fails (RETAKE_FAKE_GPU=fail) or says
// it found no GPU (=none).
func fakeWhisperCLI(args []string) int {
	if p := os.Getenv("RETAKE_FAKE_ARGS"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return 9
		}
		_, _ = fmt.Fprintln(f, strings.Join(args, "\x1f"))
		_ = f.Close()
	}
	if log, err := os.ReadFile(os.Getenv("RETAKE_FAKE_LOG")); err == nil {
		_, _ = os.Stderr.Write(log)
	}
	if !slices.Contains(args, "-ng") {
		switch os.Getenv("RETAKE_FAKE_GPU") {
		case "fail":
			_, _ = fmt.Fprintln(os.Stderr, "ggml_cuda_init: CUDA error: out of memory")
			return 1
		case "none":
			_, _ = fmt.Fprintln(os.Stderr, "whisper_backend_init_gpu: no GPU found")
		default: // as a real CUDA build logs it: the model on the GPU, then the VAD model, which never is
			_, _ = fmt.Fprintln(os.Stderr, "whisper_backend_init_gpu: using CUDA0 backend")
			_, _ = fmt.Fprintln(os.Stderr, "whisper_backend_init_gpu: no GPU found")
		}
	}
	i := slices.Index(args, "-of")
	if i < 0 || i+1 >= len(args) {
		_, _ = fmt.Fprintln(os.Stderr, "no -of")
		return 2
	}
	data, err := os.ReadFile(os.Getenv("RETAKE_FAKE_JSON"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 3
	}
	if err := os.WriteFile(args[i+1]+".json", data, 0o600); err != nil {
		return 4
	}
	return 0
}

// fakeCommand checks that its audio argument (the last one, or the one after --audio) is a 16 kHz mono WAV and
// prints RETAKE_FAKE_OUT, with $PROMPT replaced by RETAKE_PROMPT. RETAKE_FAKE_EXIT makes it fail.
func fakeCommand(args []string) int {
	if os.Getenv("RETAKE_FAKE_EXIT") != "" {
		_, _ = fmt.Fprintln(os.Stderr, "model not found")
		return 1
	}
	audio := args[len(args)-1]
	if i := slices.Index(args, "--audio"); i >= 0 {
		audio = args[i+1]
	}
	if info, err := readWAV(audio); err != nil || !info.whisperReady() {
		_, _ = fmt.Fprintf(os.Stderr, "not a 16 kHz WAV: %s %v\n", audio, err)
		return 5
	}
	_, _ = fmt.Print(strings.ReplaceAll(os.Getenv("RETAKE_FAKE_OUT"), "$PROMPT", os.Getenv("RETAKE_PROMPT")))
	return 0
}
