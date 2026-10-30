package transcribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// remoteFile is a model file we know how to fetch and check.
type remoteFile struct {
	File   string // the name in the models folder, e.g. ggml-small.bin
	URL    string
	Size   int64
	SHA256 string
}

// The official ggml conversions, pinned to one revision of the Hugging Face repos that whisper.cpp's own
// models/download-*.sh scripts use, so the checksums below stay valid.
const (
	ggmlRepo = "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/"
	vadRepo  = "https://huggingface.co/ggml-org/whisper-vad/resolve/9ffd54a1e1ee413ddf265af9913beaf518d1639b/"
)

// modelNames are the whisper.cpp models retake knows, smallest first.
var modelNames = []string{"tiny", "base", "small", "medium", "large-v3-turbo", "large-v3"}

var modelFiles = map[string]remoteFile{
	"tiny":           {"ggml-tiny.bin", ggmlRepo + "ggml-tiny.bin", 77691713, "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21"},
	"base":           {"ggml-base.bin", ggmlRepo + "ggml-base.bin", 147951465, "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe"},
	"small":          {"ggml-small.bin", ggmlRepo + "ggml-small.bin", 487601967, "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b"},
	"medium":         {"ggml-medium.bin", ggmlRepo + "ggml-medium.bin", 1533763059, "6c14d5adee5f86394037b4e4e8b59f1673b6cee10e3cf0b11bbdbee79c156208"},
	"large-v3-turbo": {"ggml-large-v3-turbo.bin", ggmlRepo + "ggml-large-v3-turbo.bin", 1624555275, "1fc70f774d38eb169993ac391eea357ef47c88757ef72ee5943879b7e8e2bc69"},
	"large-v3":       {"ggml-large-v3.bin", ggmlRepo + "ggml-large-v3.bin", 3095033483, "64d182b440b98d5203c4f9bd541544d84c605196c4f7b845dfa11fb23594d1e2"},
}

// vadModel is Silero VAD for whisper.cpp's --vad: it finds the speech, so silence isn't hallucinated into words.
var vadModel = remoteFile{"ggml-silero-v6.2.0.bin", vadRepo + "ggml-silero-v6.2.0.bin", 885098,
	"2aa269b785eeb53a82983a20501ddf7c1d9c48e33ab63a41391ac6c9f7fb6987"}

// ModelNames returns the model names retake can download, smallest first.
func ModelNames() []string { return slices.Clone(modelNames) }

// modelRank orders models by size; a .bin path (a custom model) is -1.
func modelRank(model string) int { return slices.Index(modelNames, model) }

// isModelPath reports whether model names a ggml file rather than one of modelNames.
func isModelPath(model string) bool {
	return strings.HasSuffix(model, ".bin") || strings.ContainsAny(model, `/\`)
}

// modelLabel is how a model appears in Name: its name, or a custom file's name without "ggml-" and ".bin".
func modelLabel(model string) string {
	if !isModelPath(model) {
		return model
	}
	return strings.TrimSuffix(strings.TrimPrefix(filepath.Base(model), "ggml-"), ".bin")
}

// checkModel returns an error unless model is a known name or an existing .bin file.
func checkModel(model string) error {
	if isModelPath(model) {
		if st, err := os.Stat(model); err != nil || !st.Mode().IsRegular() {
			return fmt.Errorf("model file %s not found", model)
		}
		return nil
	}
	if _, ok := modelFiles[model]; !ok {
		return fmt.Errorf("unknown whisper model %q (use one of %s, or the path of a ggml .bin file)", model,
			strings.Join(modelNames, ", "))
	}
	return nil
}

// ModelStore keeps whisper.cpp models in one folder ($XDG_CACHE_HOME/retake/models) and downloads them on first use.
type ModelStore struct {
	Dir      string
	Progress io.Writer    // download progress; nil = quiet
	Client   *http.Client // nil = http.DefaultClient

	mu sync.Mutex // one download at a time
}

// DefaultModelDir returns $XDG_CACHE_HOME/retake/models, or the OS cache folder's retake/models.
func DefaultModelDir(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CACHE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "retake", "models"), nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("no cache folder for whisper models: %w", err)
	}
	return filepath.Join(d, "retake", "models"), nil
}

// Model returns the path of a model, downloading it first if needed. A .bin path is returned as it is.
func (s *ModelStore) Model(ctx context.Context, model string) (string, error) {
	if isModelPath(model) {
		return model, checkModel(model)
	}
	f, ok := modelFiles[model]
	if !ok {
		return "", checkModel(model)
	}
	return s.fetch(ctx, f)
}

// VAD returns the path of the Silero VAD model, downloading it first if needed.
func (s *ModelStore) VAD(ctx context.Context) (string, error) { return s.fetch(ctx, vadModel) }

// fetch returns f's path in Dir. A file of the right size is trusted (it was checked when it was downloaded);
// otherwise it's downloaded to a temporary file, checked against its size and sha256, then renamed into place.
func (s *ModelStore) fetch(ctx context.Context, f remoteFile) (string, error) {
	path := filepath.Join(s.Dir, f.File)
	if st, err := os.Stat(path); err == nil && st.Size() == f.Size {
		return path, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, err := os.Stat(path); err == nil && st.Size() == f.Size { // another call got it meanwhile
		return path, nil
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return "", fmt.Errorf("models folder: %w", err)
	}
	tmp, err := os.CreateTemp(s.Dir, "."+f.File+".*.part")
	if err != nil {
		return "", fmt.Errorf("models folder: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op once renamed
	if err := s.download(ctx, f, tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func (s *ModelStore) download(ctx context.Context, f remoteFile, out *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "retake")
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", f.File, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", f.File, resp.Status)
	}
	h := sha256.New()
	p := newProgress(s.Progress, f.File, f.Size)
	n, err := io.Copy(io.MultiWriter(out, h, p), resp.Body)
	p.finish(err == nil && n == f.Size)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", f.File, err)
	}
	if n != f.Size {
		return fmt.Errorf("downloading %s: got %d bytes, want %d", f.File, n, f.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("downloading %s: checksum mismatch (sha256 %s, want %s)", f.File, got, f.SHA256)
	}
	return nil
}

// progress prints one download's progress: a line rewritten in place on a terminal, else a line every 10%.
type progress struct {
	w           io.Writer
	name        string
	total, done int64
	tty         bool
	shown       int // the last percentage (tty) or tenth (otherwise) printed
	last        time.Time
}

// quietBelow is the size under which a download shows no progress (the 1 MiB VAD model).
const quietBelow = 16 << 20

func newProgress(w io.Writer, name string, total int64) *progress {
	if total < quietBelow {
		w = nil
	}
	p := &progress{w: w, name: name, total: total, shown: -1}
	if f, ok := w.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			p.tty = true
		}
	}
	p.show()
	return p
}

func (p *progress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	p.show()
	return len(b), nil
}

func (p *progress) pct() int {
	if p.total <= 0 {
		return 0
	}
	return int(min(100, p.done*100/p.total))
}

func (p *progress) show() {
	if p.w == nil {
		return
	}
	pct := p.pct()
	if p.tty {
		if pct == p.shown || time.Since(p.last) < 100*time.Millisecond && pct < 100 {
			return
		}
		p.shown, p.last = pct, time.Now()
		_, _ = fmt.Fprintf(p.w, "\rdownloading whisper model %s (%s): %3d%%", p.name, mib(p.total), pct)
		return
	}
	if pct/10 == p.shown {
		return
	}
	p.shown = pct / 10
	_, _ = fmt.Fprintf(p.w, "downloading whisper model %s (%s): %d%%\n", p.name, mib(p.total), pct/10*10)
}

func (p *progress) finish(ok bool) {
	if p.w == nil {
		return
	}
	switch {
	case p.tty && ok:
		_, _ = fmt.Fprintf(p.w, "\rdownloading whisper model %s (%s): done\n", p.name, mib(p.total))
	case p.tty:
		_, _ = fmt.Fprintln(p.w)
	case ok && p.shown < 10:
		_, _ = fmt.Fprintf(p.w, "downloading whisper model %s (%s): 100%%\n", p.name, mib(p.total))
	}
}

func mib(n int64) string { return fmt.Sprintf("%d MiB", (n+(1<<20)-1)>>20) }
