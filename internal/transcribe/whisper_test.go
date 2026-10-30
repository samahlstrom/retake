package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeWAV writes a 16 kHz mono 16-bit WAV of a 220 Hz tone, so tests that don't decode need no ffmpeg.
func writeWAV(t *testing.T, path string, seconds float64) {
	t.Helper()
	n := int(seconds * SampleRate)
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+2*n))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(SampleRate), uint32(2 * SampleRate), uint16(2),
		uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(2*n))
	for i := range n {
		_ = binary.Write(&b, binary.LittleEndian, int16(8000*math.Sin(2*math.Pi*220*float64(i)/SampleRate)))
	}
	if err := os.WriteFile(path, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeStore is a ModelStore whose VAD model is already there (a sparse file of the right size), plus a custom model
// file to use instead of a download.
func fakeStore(t *testing.T) (*ModelStore, string) {
	t.Helper()
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, vadModel.File))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(vadModel.Size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	model := filepath.Join(dir, "ggml-test.bin")
	if err := os.WriteFile(model, []byte("weights"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &ModelStore{Dir: dir}, model
}

func gpusWith(freeMiB ...int64) func(context.Context) ([]GPU, error) {
	return func(context.Context) ([]GPU, error) {
		var g []GPU
		for _, f := range freeMiB {
			g = append(g, GPU{Name: "Test GPU", FreeMiB: f, TotalMiB: 24564})
		}
		return g, nil
	}
}

func TestWhisperChoosesDeviceAndModel(t *testing.T) {
	store, custom := fakeStore(t)
	noSMI := func(context.Context) ([]GPU, error) { return nil, errors.New("nvidia-smi not found") }
	for _, tc := range []struct {
		name, device, model string
		cuda                bool
		gpus                func(context.Context) ([]GPU, error)
		want                string
	}{
		{"cpu, no model: small", DeviceCPU, "", false, nil, "whisper.cpp small (cpu)"},
		{"cpu, explicit large stays", DeviceCPU, "large-v3", true, nil, "whisper.cpp large-v3 (cpu)"},
		{"auto without CUDA: small", DeviceAuto, "", false, nil, "whisper.cpp small (cpu)"},
		{"auto without CUDA: large falls back", "", "large-v3", false, nil, "whisper.cpp small (cpu)"},
		{"auto without CUDA: medium falls back", DeviceAuto, "medium", false, nil, "whisper.cpp small (cpu)"},
		{"auto without CUDA: tiny stays", DeviceAuto, "tiny", false, nil, "whisper.cpp tiny (cpu)"},
		{"auto with a free GPU", DeviceAuto, "", true, gpusWith(22000), "whisper.cpp large-v3 (cuda)"},
		{"auto, second GPU free", DeviceAuto, "", true, gpusWith(500, 22000), "whisper.cpp large-v3 (cuda)"},
		{"auto, GPU too full for large", DeviceAuto, "", true, gpusWith(2000), "whisper.cpp small (cpu)"},
		{"auto, GPU has room for base", DeviceAuto, "base", true, gpusWith(2000), "whisper.cpp base (cuda)"},
		{"auto, no nvidia-smi", DeviceAuto, "", true, noSMI, "whisper.cpp small (cpu)"},
		{"cuda forced, no probe", DeviceCUDA, "", true, noSMI, "whisper.cpp large-v3 (cuda)"},
		{"custom model file", DeviceAuto, custom, false, nil, "whisper.cpp test (cpu)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gpus := tc.gpus
			if gpus == nil {
				gpus = func(context.Context) ([]GPU, error) { t.Error("GPU probed"); return nil, nil }
			}
			w, err := newWhisperCPP(WhisperOptions{CLI: "whisper-cli", Model: tc.model, Device: tc.device, Models: store},
				tc.cuda, gpus)
			if err != nil {
				t.Fatal(err)
			}
			if got := w.Name(); got != tc.want {
				t.Errorf("Name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewWhisperCPPErrors(t *testing.T) {
	store, _ := fakeStore(t)
	for _, tc := range []struct {
		name string
		o    WhisperOptions
		want string
	}{
		{"cuda without CUDA", WhisperOptions{CLI: "w", Device: DeviceCUDA, Models: store}, "install-whisper.sh --cuda"},
		{"bad device", WhisperOptions{CLI: "w", Device: "gpu", Models: store}, `unknown device "gpu"`},
		{"bad model", WhisperOptions{CLI: "w", Model: "huge", Models: store}, "large-v3-turbo"},
		{"missing model file", WhisperOptions{CLI: "w", Model: "/nowhere/ggml-x.bin", Models: store}, "not found"},
		{"no cli", WhisperOptions{Models: store}, "no whisper-cli"},
		{"no store", WhisperOptions{CLI: "w"}, "no model store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newWhisperCPP(tc.o, false, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWhisperArgs(t *testing.T) {
	a := whisperArgs(whisperPlan{DeviceCPU, "small"}, "/m/ggml-small.bin", "/m/vad.bin", "/t/a.wav", "/t/words", 6,
		Options{Prompt: "Zoom, B-roll.", Language: "en"})
	joined := " " + strings.Join(a, " ") + " "
	for _, want := range []string{" -m /m/ggml-small.bin ", " -f /t/a.wav ", " -of /t/words ", " -ojf ", " -ml 1 ",
		" -sow ", " -l en ", " -t 6 ", " --vad ", " -vm /m/vad.bin ", " -sns ", " -ng "} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %v", want, a)
		}
	}
	if i := slices.Index(a, "--prompt"); i < 0 || a[i+1] != "Zoom, B-roll." {
		t.Errorf("prompt: %v", a)
	}
	a = whisperArgs(whisperPlan{DeviceCUDA, "large-v3"}, "m", "v", "w", "o", 4, Options{})
	if slices.Contains(a, "-ng") || slices.Contains(a, "--prompt") || a[slices.Index(a, "-l")+1] != "auto" {
		t.Errorf("cuda args: %v", a)
	}
}

// fakeCLI sets up the test binary as whisper-cli. It returns the file its arguments are recorded in.
func fakeCLI(t *testing.T, gpu string) (cli, argsFile string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argsFile = filepath.Join(t.TempDir(), "args")
	t.Setenv("RETAKE_FAKE", "whisper-cli")
	t.Setenv("RETAKE_FAKE_JSON", filepath.Join(mustAbs(t, "testdata"), "whisper-cli-gaps.json"))
	t.Setenv("RETAKE_FAKE_LOG", filepath.Join(mustAbs(t, "testdata"), "whisper-cli-gaps.log"))
	t.Setenv("RETAKE_FAKE_ARGS", argsFile)
	t.Setenv("RETAKE_FAKE_GPU", gpu)
	return exe, argsFile
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func runs(t *testing.T, argsFile string) [][]string {
	t.Helper()
	b, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		out = append(out, strings.Split(l, "\x1f"))
	}
	return out
}

func TestWhisperTranscribeRunsCLI(t *testing.T) {
	cli, argsFile := fakeCLI(t, "")
	store, custom := fakeStore(t)
	w, err := newWhisperCPP(WhisperOptions{CLI: cli, Model: custom, Device: DeviceCPU, Threads: 3, Models: store,
		TempDir: t.TempDir()}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(t.TempDir(), "in.wav")
	writeWAV(t, wav, 1)
	words, err := w.Transcribe(context.Background(), wav, Options{Prompt: "Deadlock", Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	// The VAD table in the log places "Make" in the second speech region (TestParseCLIJSONRealOutput).
	if len(words) != 14 || words[0].Text != " cut" || words[9].Text != " Make" || words[9].Start < 7 {
		t.Errorf("words = %+v", words)
	}
	r := runs(t, argsFile)
	if len(r) != 1 {
		t.Fatalf("runs = %v", r)
	}
	a := r[0]
	if a[slices.Index(a, "-m")+1] != custom || a[slices.Index(a, "-vm")+1] != filepath.Join(store.Dir, vadModel.File) ||
		a[slices.Index(a, "-f")+1] != wav || a[slices.Index(a, "-t")+1] != "3" || !slices.Contains(a, "-ng") ||
		a[slices.Index(a, "--prompt")+1] != "Deadlock" {
		t.Errorf("args = %v", a)
	}
}

func TestWhisperFallsBackToCPU(t *testing.T) {
	for _, gpu := range []string{"fail", "none"} {
		t.Run(gpu, func(t *testing.T) {
			cli, argsFile := fakeCLI(t, gpu)
			store, custom := fakeStore(t)
			var log bytes.Buffer
			w, err := newWhisperCPP(WhisperOptions{CLI: cli, Model: custom, Models: store, Log: &log}, true, gpusWith(24000))
			if err != nil {
				t.Fatal(err)
			}
			if w.Name() != "whisper.cpp test (cuda)" {
				t.Fatalf("Name = %q", w.Name())
			}
			wav := filepath.Join(t.TempDir(), "in.wav")
			writeWAV(t, wav, 1)
			words, err := w.Transcribe(context.Background(), wav, Options{})
			if err != nil || len(words) == 0 {
				t.Fatalf("words %v, err %v", words, err)
			}
			if w.Name() != "whisper.cpp test (cpu)" {
				t.Errorf("after the fallback Name = %q", w.Name())
			}
			r := runs(t, argsFile)
			if len(r) != 2 || slices.Contains(r[0], "-ng") || !slices.Contains(r[1], "-ng") {
				t.Errorf("runs = %v", r)
			}
			if !strings.Contains(log.String(), "on the CPU") {
				t.Errorf("log = %q", log.String())
			}
		})
	}
}

func TestWhisperForcedCUDAFails(t *testing.T) {
	cli, argsFile := fakeCLI(t, "fail")
	store, custom := fakeStore(t)
	w, err := newWhisperCPP(WhisperOptions{CLI: cli, Model: custom, Device: DeviceCUDA, Models: store}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(t.TempDir(), "in.wav")
	writeWAV(t, wav, 1)
	_, err = w.Transcribe(context.Background(), wav, Options{})
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("err = %v", err)
	}
	if r := runs(t, argsFile); len(r) != 1 {
		t.Errorf("runs = %v", r)
	}
}

func TestWhisperSkipsTinyAudio(t *testing.T) {
	store, custom := fakeStore(t)
	w, err := newWhisperCPP(WhisperOptions{CLI: filepath.Join(t.TempDir(), "never-run"), Model: custom, Device: DeviceCPU,
		Models: store}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(t.TempDir(), "in.wav")
	writeWAV(t, wav, 0.05)
	words, err := w.Transcribe(context.Background(), wav, Options{})
	if err != nil || words == nil || len(words) != 0 {
		t.Errorf("words %#v, err %v", words, err)
	}
}

func TestWhisperCLIFailure(t *testing.T) {
	store, custom := fakeStore(t)
	w, err := newWhisperCPP(WhisperOptions{CLI: filepath.Join(t.TempDir(), "missing-cli"), Model: custom, Device: DeviceCPU,
		Models: store}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(t.TempDir(), "in.wav")
	writeWAV(t, wav, 1)
	if _, err := w.Transcribe(context.Background(), wav, Options{}); err == nil || !strings.Contains(err.Error(), "whisper-cli failed") {
		t.Errorf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Transcribe(ctx, wav, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: err = %v", err)
	}
}

func TestBinaryHasCUDA(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(bin, "whisper-cli")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(cli, "ELF ... plain CPU build")
	if binaryHasCUDA(cli) {
		t.Error("CPU build reported CUDA")
	}
	// A static CUDA build carries the backend's strings; put one across the 1 MiB read boundary.
	big := bytes.Repeat([]byte{'x'}, 1<<20-5)
	write(cli, string(big)+"ggml_cuda_init: found 1 CUDA devices")
	if !binaryHasCUDA(cli) {
		t.Error("static CUDA build not detected")
	}
	write(cli, "plain")
	write(filepath.Join(root, "BUILD"), "whisper.cpp v1.9.5\nbackend cuda\n")
	if !binaryHasCUDA(cli) {
		t.Error("BUILD backend cuda not detected")
	}
	write(filepath.Join(root, "BUILD"), "whisper.cpp v1.9.5\nbackend cpu\n")
	write(cli, "ggml_cuda_init")
	if binaryHasCUDA(cli) {
		t.Error("BUILD backend cpu should win")
	}
	if err := os.Remove(filepath.Join(root, "BUILD")); err != nil {
		t.Fatal(err)
	}
	write(cli, "plain")
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "lib", "libggml-cuda.so"), "")
	if !binaryHasCUDA(cli) {
		t.Error("shared libggml-cuda not detected")
	}
	if binaryHasCUDA(filepath.Join(t.TempDir(), "missing")) {
		t.Error("missing binary reported CUDA")
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	got := parseNvidiaSMI([]byte("NVIDIA GeForce RTX 4090, 22897, 24564\nweird line\nTesla T4, [N/A], 15360\n" +
		"NVIDIA A100-SXM4-80GB, 81000, 81920\n"))
	want := []GPU{{"NVIDIA GeForce RTX 4090", 22897, 24564}, {"NVIDIA A100-SXM4-80GB", 81000, 81920}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v", got)
	}
	if n := gpuNeedMiB("large-v3"); n < 4000 || n > 6000 {
		t.Errorf("large-v3 needs %d MiB?", n)
	}
	if n := gpuNeedMiB("tiny"); n > 1200 {
		t.Errorf("tiny needs %d MiB?", n)
	}
}
