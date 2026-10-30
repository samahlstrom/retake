package transcribe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Devices for WhisperOptions.Device (config: [transcribe] device).
const (
	DeviceAuto = "auto" // CUDA when whisper-cli has it and a GPU has room for the model, else the CPU
	DeviceCUDA = "cuda"
	DeviceCPU  = "cpu"
)

// Silero VAD settings, close to the legacy notes.py ones: a lower threshold than Silero's 0.5 keeps quiet speech,
// a long minimum silence keeps a sentence in one piece, and the padding keeps word onsets and endings.
const (
	vadThreshold    = "0.4"
	vadMinSilenceMS = "400"
	vadPadMS        = "200"
)

// WhisperOptions configure a WhisperCPP.
type WhisperOptions struct {
	CLI     string      // whisper-cli's path (required)
	Model   string      // a name from ModelNames or a ggml .bin path; "" = large-v3 on CUDA, small on the CPU
	Device  string      // DeviceAuto (or ""), DeviceCUDA or DeviceCPU
	Threads int         // CPU threads; 0 = the number of CPUs, at most 8
	Models  *ModelStore // where models are kept and downloaded (required)
	TempDir string      // for the 16 kHz WAV and whisper-cli's JSON; "" = os.TempDir()
	Log     io.Writer   // notices such as a fallback from CUDA to the CPU; nil = quiet
}

// WhisperCPP transcribes with whisper.cpp's command-line program, whisper-cli.
//
// It shells out instead of linking libwhisper with cgo: a pure-Go retake binary cross-compiles for every platform,
// whisper.cpp's CUDA/Metal/Vulkan builds stay its own business, a crash in the model can't take the server down, and
// a cancelled job is a killed process. The cost, loading the model once per file, is small next to decoding.
type WhisperCPP struct {
	opts    WhisperOptions
	hasCUDA bool
	gpus    func(context.Context) ([]GPU, error)

	mu   sync.Mutex
	plan *whisperPlan // decided on first use; a CUDA failure in auto mode switches it to the CPU
}

type whisperPlan struct{ device, model string }

// NewWhisperCPP checks the options and returns the engine. The device and model are picked on first use.
func NewWhisperCPP(o WhisperOptions) (*WhisperCPP, error) {
	return newWhisperCPP(o, binaryHasCUDA(o.CLI), nvidiaGPUs)
}

func newWhisperCPP(o WhisperOptions, hasCUDA bool, gpus func(context.Context) ([]GPU, error)) (*WhisperCPP, error) {
	if o.CLI == "" {
		return nil, errors.New("whisper.cpp: no whisper-cli path")
	}
	if o.Models == nil {
		return nil, errors.New("whisper.cpp: no model store")
	}
	switch o.Device {
	case "":
		o.Device = DeviceAuto
	case DeviceAuto, DeviceCPU:
	case DeviceCUDA:
		if !hasCUDA {
			return nil, fmt.Errorf("device = \"cuda\", but %s was built without CUDA: rebuild it with "+
				"scripts/install-whisper.sh --cuda", o.CLI)
		}
	default:
		return nil, fmt.Errorf("unknown device %q (auto, cuda or cpu)", o.Device)
	}
	if o.Model != "" {
		if err := checkModel(o.Model); err != nil {
			return nil, err
		}
	}
	if o.Threads <= 0 {
		o.Threads = min(runtime.NumCPU(), 8)
	}
	return &WhisperCPP{opts: o, hasCUDA: hasCUDA, gpus: gpus}, nil
}

// Name is "whisper.cpp <model> (<device>)", e.g. "whisper.cpp large-v3 (cuda)".
func (w *WhisperCPP) Name() string {
	p := w.resolve()
	return "whisper.cpp " + modelLabel(p.model) + " (" + p.device + ")"
}

func (w *WhisperCPP) resolve() whisperPlan {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.plan == nil {
		p := w.choose()
		w.plan = &p
	}
	return *w.plan
}

func (w *WhisperCPP) setPlan(p whisperPlan) {
	w.mu.Lock()
	w.plan = &p
	w.mu.Unlock()
}

func (w *WhisperCPP) choose() whisperPlan {
	switch w.opts.Device {
	case DeviceCPU:
		return whisperPlan{DeviceCPU, w.cpuModel(false)}
	case DeviceCUDA:
		return whisperPlan{DeviceCUDA, w.gpuModel()}
	}
	if w.hasCUDA {
		model := w.gpuModel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if gpus, err := w.gpus(ctx); err == nil {
			for _, g := range gpus {
				if g.FreeMiB >= gpuNeedMiB(model) {
					return whisperPlan{DeviceCUDA, model}
				}
			}
		}
	}
	return whisperPlan{DeviceCPU, w.cpuModel(true)}
}

func (w *WhisperCPP) gpuModel() string {
	if w.opts.Model == "" {
		return "large-v3"
	}
	return w.opts.Model
}

// cpuModel is the configured model, or small when none is set. With device = auto, a model bigger than small falls
// back to small too: large-v3 on a CPU takes many times the audio's length.
func (w *WhisperCPP) cpuModel(auto bool) string {
	m := w.opts.Model
	if m == "" || auto && modelRank(m) > modelRank("small") {
		return "small"
	}
	return m
}

// Transcribe decodes the file to 16 kHz WAV (unless it already is one) and runs whisper-cli on it. If CUDA fails
// with device = auto, the file is transcribed again on the CPU, and Name reports the CPU model from then on.
func (w *WhisperCPP) Transcribe(ctx context.Context, audioPath string, opts Options) ([]Word, error) {
	tmp, err := os.MkdirTemp(w.opts.TempDir, "retake-whisper-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	wav, dur, err := prepareWAV(ctx, audioPath, tmp)
	if err != nil {
		return nil, err
	}
	if dur < minAudio {
		return []Word{}, nil
	}
	p := w.resolve()
	words, err := w.run(ctx, p, wav, tmp, opts)
	if err != nil && p.device == DeviceCUDA && w.opts.Device == DeviceAuto && ctx.Err() == nil {
		cpu := whisperPlan{DeviceCPU, w.cpuModel(true)}
		if w.opts.Log != nil {
			_, _ = fmt.Fprintf(w.opts.Log, "whisper.cpp: CUDA failed (%v); using %s on the CPU\n", err, cpu.model)
		}
		w.setPlan(cpu)
		words, err = w.run(ctx, cpu, wav, tmp, opts)
	}
	return words, err
}

func (w *WhisperCPP) run(ctx context.Context, p whisperPlan, wav, tmp string, opts Options) ([]Word, error) {
	model, err := w.opts.Models.Model(ctx, p.model)
	if err != nil {
		return nil, err
	}
	vad, err := w.opts.Models.VAD(ctx)
	if err != nil {
		return nil, err
	}
	out := filepath.Join(tmp, "words")
	_ = os.Remove(out + ".json")
	cmd := exec.CommandContext(ctx, w.opts.CLI, whisperArgs(p, model, vad, wav, out, w.opts.Threads, opts)...) //nolint:gosec // the configured whisper-cli
	cmd.Stdout = io.Discard
	var log cliLog
	cmd.Stderr = &log
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("whisper-cli failed on %s: %w: %s", filepath.Base(wav), err, log.String())
	}
	if p.device == DeviceCUDA && log.noGPU {
		return nil, errors.New("whisper-cli found no GPU")
	}
	data, err := os.ReadFile(out + ".json") //nolint:gosec // our temp folder
	if err != nil {
		return nil, fmt.Errorf("whisper-cli wrote no JSON: %w: %s", err, log.String())
	}
	return parseCLIJSON(data, log.vad)
}

// whisperArgs builds whisper-cli's command line: one-word segments with token timestamps in a full JSON file
// (<out>.json), beam search, Silero VAD, non-speech tokens (♪, brackets) suppressed.
func whisperArgs(p whisperPlan, model, vad, wav, out string, threads int, o Options) []string {
	lang := o.Language
	if lang == "" {
		lang = "auto"
	}
	a := []string{
		"-m", model, "-f", wav, "-of", out, "-ojf", "-ml", "1", "-sow",
		"-l", lang, "-bs", "5", "-sns", "-t", strconv.Itoa(threads),
		"--vad", "-vm", vad, "-vt", vadThreshold, "-vsd", vadMinSilenceMS, "-vp", vadPadMS,
		"-vo", strconv.FormatFloat(vadOverlap, 'f', -1, 64),
	}
	if o.Prompt != "" {
		a = append(a, "--prompt", o.Prompt)
	}
	if p.device == DeviceCPU {
		a = append(a, "-ng")
	}
	return a
}
