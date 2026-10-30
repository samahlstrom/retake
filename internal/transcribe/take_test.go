package transcribe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeTranscriber checks it's given a 16 kHz WAV and answers with one word: the audio's duration.
type fakeTranscriber struct {
	mu    sync.Mutex
	name  string
	calls int
	opts  []Options
	err   error
}

func (f *fakeTranscriber) Transcribe(_ context.Context, audio string, o Options) ([]Word, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.opts = append(f.opts, o)
	if f.err != nil {
		return nil, f.err
	}
	info, err := readWAV(audio)
	if err != nil || !info.whisperReady() {
		return nil, fmt.Errorf("not a 16 kHz WAV: %w", err)
	}
	return []Word{{Text: fmt.Sprintf(" %.1f", info.duration()), Start: 0.5, End: 1, Prob: 0.9}}, nil
}

func (f *fakeTranscriber) Name() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.name
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, string(b))
}

func TestAudioFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "segments.json"), `[{"id": "S1", "file": "seg_02.ogg"}, {"id": "S2", "file": "seg_01.webm"},
		{"file": "../outside.webm"}, {"file": ""}, {"file": "seg_09.webm"}, {"file": "seg_01.webm"}, {"id": "S9"}]`)
	for _, n := range []string{"seg_01.webm", "seg_02.ogg", "seg_03.m4a", "seg_1.wav", "narration.webm", "notes.md",
		"session.json", "seg_04.webm.part", TranscriptFile} {
		writeFile(t, filepath.Join(dir, n), "x")
	}
	if err := os.Mkdir(filepath.Join(dir, "seg_05.webm"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := AudioFiles(dir)
	want := []string{"seg_02.ogg", "seg_01.webm", "seg_09.webm", "seg_03.m4a", "seg_1.wav"}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("AudioFiles = %v, %v; want %v", got, err, want)
	}

	// Legacy format 2: no segments.json, one narration file.
	old := t.TempDir()
	writeFile(t, filepath.Join(old, "narration.webm"), "x")
	if got, err := AudioFiles(old); err != nil || !slices.Equal(got, []string{"narration.webm"}) {
		t.Errorf("format 2: %v %v", got, err)
	}
	if _, err := AudioFiles(filepath.Join(old, "missing")); err == nil {
		t.Error("missing take: no error")
	}
}

// newTake makes a take folder with the two test phrases, an empty segment still being recorded, and segments.json.
func newTake(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "2026-10-30_12-00-00")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, "testdata/seg_01.webm", filepath.Join(dir, "seg_01.webm"))
	copyFile(t, "testdata/seg_02.ogg", filepath.Join(dir, "seg_02.ogg"))
	writeFile(t, filepath.Join(dir, "seg_03.webm"), "")
	writeFile(t, filepath.Join(dir, "segments.json"), `[{"id": "S1", "file": "seg_01.webm"}, {"id": "S2", "file": "seg_02.ogg"},
		{"id": "S3", "file": "seg_03.webm", "recording": true}]`)
	return dir
}

func TestTake(t *testing.T) {
	testmedia.Need(t)
	dir := newTake(t)
	path := filepath.Join(dir, TranscriptFile)
	f := &fakeTranscriber{name: "fake v1"}
	var log bytes.Buffer
	ctx := context.Background()

	if err := Take(ctx, dir, f, TakeOptions{Log: &log, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if f.calls != 2 {
		t.Fatalf("calls = %d, want 2", f.calls)
	}
	tr, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tr.Names(), []string{"seg_01.webm", "seg_02.ogg"}) {
		t.Errorf("names = %v (the empty seg_03 waits for audio)", tr.Names())
	}
	e1, _ := tr.File("seg_01.webm")
	st, _ := os.Stat(filepath.Join(dir, "seg_01.webm"))
	if e1.Size != st.Size() || e1.Model != "fake v1" || e1.Prompt != nil || math.Abs(e1.Duration-4.771) > 0.05 ||
		len(e1.Words) != 1 || e1.Words[0].Text != " 4.8" {
		t.Errorf("seg_01 = %+v", e1)
	}
	if !strings.Contains(log.String(), "2026-10-30_12-00-00/seg_02.ogg: 3.6 s of audio, 1 words, fake v1") {
		t.Errorf("log = %q", log.String())
	}

	// The skip rule: nothing changed, nothing runs, and transcript.json isn't rewritten.
	before, _ := os.ReadFile(path)
	log.Reset()
	if err := Take(ctx, dir, f, TakeOptions{Log: &log}); err != nil || f.calls != 2 {
		t.Fatalf("second run: err %v, calls %d", err, f.calls)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("transcript.json rewritten with nothing new")
	}
	if strings.Count(log.String(), "already transcribed (fake v1)") != 2 {
		t.Errorf("log = %q", log.String())
	}

	// A file that grew is done again; the other is kept.
	af, err := os.OpenFile(filepath.Join(dir, "seg_02.ogg"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = af.Write([]byte{0})
	_ = af.Close()
	if err := Take(ctx, dir, f, TakeOptions{}); err != nil || f.calls != 3 {
		t.Fatalf("grown file: err %v, calls %d", err, f.calls)
	}

	// Another model, a prompt, or --force: everything again.
	f.name = "fake v2"
	if err := Take(ctx, dir, f, TakeOptions{}); err != nil || f.calls != 5 {
		t.Fatalf("new model: err %v, calls %d", err, f.calls)
	}
	if err := Take(ctx, dir, f, TakeOptions{Options: Options{Prompt: "Zoom", Language: "en"}}); err != nil || f.calls != 7 {
		t.Fatalf("prompt: err %v, calls %d", err, f.calls)
	}
	if o := f.opts[len(f.opts)-1]; o.Prompt != "Zoom" || o.Language != "en" {
		t.Errorf("options passed = %+v", o)
	}
	tr, _ = ReadTranscript(path)
	if e, _ := tr.File("seg_01.webm"); e.Model != "fake v2" || e.Prompt == nil || *e.Prompt != *PromptHash("Zoom") {
		t.Errorf("after the prompt run: %+v", e)
	}
	if err := Take(ctx, dir, f, TakeOptions{Options: Options{Prompt: "Zoom"}, Force: true}); err != nil || f.calls != 9 {
		t.Fatalf("force: err %v, calls %d", err, f.calls)
	}
}

func TestTakeErrors(t *testing.T) {
	testmedia.Need(t)
	ctx := context.Background()

	// A file ffmpeg can't decode doesn't stop the others.
	dir := newTake(t)
	writeFile(t, filepath.Join(dir, "seg_03.webm"), "garbage, not audio")
	f := &fakeTranscriber{name: "fake"}
	err := Take(ctx, dir, f, TakeOptions{})
	if err == nil || !strings.Contains(err.Error(), "seg_03.webm") || f.calls != 2 {
		t.Errorf("err %v, calls %d", err, f.calls)
	}
	tr, _ := ReadTranscript(filepath.Join(dir, TranscriptFile))
	if len(tr.Names()) != 2 {
		t.Errorf("names = %v", tr.Names())
	}

	// A transcriber error: no entry is written, and both files' errors come back.
	dir = newTake(t)
	f = &fakeTranscriber{name: "fake", err: errors.New("model exploded")}
	err = Take(ctx, dir, f, TakeOptions{})
	if err == nil || strings.Count(err.Error(), "model exploded") != 2 {
		t.Errorf("err = %v", err)
	}
	if tr, _ := ReadTranscript(filepath.Join(dir, TranscriptFile)); len(tr.Names()) != 0 {
		t.Errorf("names = %v", tr.Names())
	}

	// Cancelled: stops before transcribing.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	f = &fakeTranscriber{name: "fake"}
	if err := Take(cctx, newTake(t), f, TakeOptions{}); !errors.Is(err, context.Canceled) || f.calls != 0 {
		t.Errorf("cancelled: err %v, calls %d", err, f.calls)
	}

	// A corrupt transcript.json is reported, not overwritten.
	dir = newTake(t)
	writeFile(t, filepath.Join(dir, TranscriptFile), "{not json")
	if err := Take(ctx, dir, f, TakeOptions{}); err == nil {
		t.Error("corrupt transcript: no error")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, TranscriptFile)); string(b) != "{not json" {
		t.Error("corrupt transcript overwritten")
	}
}

func TestTakeWithoutAudio(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "segments.json"), "[]")
	if err := Take(context.Background(), dir, &fakeTranscriber{name: "fake"}, TakeOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, TranscriptFile)); err != nil || string(b) != "{\n \"files\": {}\n}" {
		t.Errorf("transcript.json = %q, %v", b, err)
	}
}
