package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/samahlstrom/retake/internal/testmedia"
)

// realWhisper returns a WhisperCPP on the CPU with RETAKE_TEST_WHISPER_MODEL (default tiny), or skips the test when
// whisper-cli isn't installed. The model is downloaded on first use into the usual cache folder.
func realWhisper(t *testing.T) *WhisperCPP {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: no real transcription")
	}
	testmedia.Need(t)
	cli, err := FindWhisperCLI("", Env{})
	if err != nil {
		t.Skipf("whisper-cli not installed (scripts/install-whisper.sh): %v", err)
	}
	dir, err := DefaultModelDir(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	model := os.Getenv("RETAKE_TEST_WHISPER_MODEL")
	if model == "" {
		model = "tiny"
	}
	w, err := NewWhisperCPP(WhisperOptions{CLI: cli, Model: model, Device: DeviceCPU, Threads: 4,
		Models: &ModelStore{Dir: dir, Progress: os.Stderr}, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

type phrase struct {
	Text       string
	Start, End float64
}

func loadSpeech(t *testing.T) map[string][]phrase {
	t.Helper()
	b, err := os.ReadFile("testdata/speech.json")
	if err != nil {
		t.Fatal(err)
	}
	var files []struct {
		File    string `json:"file"`
		Phrases []struct {
			Text  string  `json:"text"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"phrases"`
	}
	if err := json.Unmarshal(b, &files); err != nil {
		t.Fatal(err)
	}
	out := map[string][]phrase{}
	for _, f := range files {
		for _, p := range f.Phrases {
			out[f.File] = append(out[f.File], phrase{p.Text, p.Start, p.End})
		}
	}
	return out
}

func bare(w string) string {
	return strings.ToLower(strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }))
}

// keyWords are words even the tiny model gets right in the espeak-ng phrases.
var keyWords = map[string][]string{
	"Throw out everything I said about this clip.": {"throw", "everything", "clip"},
	"Actually, add that back.":                     {"actually", "add", "back"},
	"Cut the intro and zoom in on the map.":        {"intro", "zoom", "map"},
	"Make the caption bigger here.":                {"caption", "bigger"},
}

// checkWords asserts each phrase's key words come out, in order, inside the phrase's time span (with slack for the
// VAD padding), that times are sane, and that no word lands in the silence between phrases.
func checkWords(t *testing.T, file string, words []Word, phrases []phrase) {
	t.Helper()
	const slack = 0.35
	for i, w := range words {
		if !strings.HasPrefix(w.Text, " ") || w.End < w.Start || w.End-w.Start > 2 || w.Prob <= 0 || w.Prob > 1 {
			t.Errorf("%s: odd word %d %+v", file, i, w)
		}
		if i > 0 && w.Start < words[i-1].Start {
			t.Errorf("%s: word %d %+v starts before the one before it", file, i, w)
		}
		inside := false
		for _, p := range phrases {
			inside = inside || w.Start >= p.Start-slack && w.End <= p.End+slack
		}
		if !inside {
			t.Errorf("%s: word %+v is outside every phrase %v", file, w, phrases)
		}
	}
	for _, p := range phrases {
		next := 0 // key words come in order
		for _, k := range keyWords[p.Text] {
			found := false
			for i := next; i < len(words); i++ {
				if bare(words[i].Text) == k {
					w := words[i]
					if w.Start < p.Start-slack || w.End > p.End+slack {
						t.Errorf("%s: %q at %.2f-%.2f, outside its phrase %.2f-%.2f", file, k, w.Start, w.End, p.Start, p.End)
					}
					found, next = true, i+1
					break
				}
			}
			if !found {
				t.Errorf("%s: %q missing from %q", file, k, wordsText(words))
			}
		}
	}
}

func wordsText(words []Word) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(w.Text)
	}
	return b.String()
}

func TestIntegrationWhisperCPP(t *testing.T) {
	w := realWhisper(t)
	speech := loadSpeech(t)
	ctx := context.Background()
	for _, file := range []string{"seg_01.webm", "seg_02.ogg", "gaps.webm"} {
		t.Run(file, func(t *testing.T) {
			began := time.Now()
			words, err := w.Transcribe(ctx, filepath.Join("testdata", file), Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s in %.2f s:%s", w.Name(), time.Since(began).Seconds(), wordsText(words))
			checkWords(t, file, words, speech[file])
		})
	}

	t.Run("silence", func(t *testing.T) {
		silence := filepath.Join(t.TempDir(), "seg_09.webm")
		out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono",
			"-t", "5", "-c:a", "libopus", silence).CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
		words, err := w.Transcribe(ctx, silence, Options{})
		if err != nil || len(words) != 0 {
			t.Errorf("silence gave %q, %v", wordsText(words), err)
		}
	})
}

func TestIntegrationTake(t *testing.T) {
	w := realWhisper(t)
	speech := loadSpeech(t)
	dir := newTake(t)
	path := filepath.Join(dir, TranscriptFile)
	if err := Take(context.Background(), dir, w, TakeOptions{TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seg_01.webm", "seg_02.ogg"} {
		f, ok := tr.File(name)
		if !ok || f.Model != w.Name() || !strings.HasSuffix(f.Model, " (cpu)") {
			t.Fatalf("%s: %+v", name, f)
		}
		checkWords(t, name, f.Words, speech[name])
	}
	before, _ := os.ReadFile(path)
	began := time.Now()
	if err := Take(context.Background(), dir, w, TakeOptions{}); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) || time.Since(began) > 2*time.Second {
		t.Error("second run redid the work")
	}
}
