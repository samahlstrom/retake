package transcribe

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTranscriptRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), TranscriptFile)
	tr := NewTranscript()
	f1 := FileTranscript{Size: 12551, Model: "whisper.cpp tiny (cpu)", Duration: 4.3125,
		Words: []Word{{" Throw", 0.6, 0.91234, 0.5}, {" out.", 1, 1.2, 1}}}
	f2 := FileTranscript{Size: 10, Model: "whisper.cpp tiny (cpu)", Prompt: PromptHash("Zoom"), Duration: 0, Words: []Word{}}
	if err := tr.Set("seg_02.ogg", f2); err != nil {
		t.Fatal(err)
	}
	if err := tr.Set("seg_01.webm", f1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Write(path); err != nil {
		t.Fatal(err)
	}
	// Python's json.dumps(indent=1) layout, floats with a ".0", rounded to 3 decimals, file order kept.
	want := `{
 "files": {
  "seg_02.ogg": {
   "size": 10,
   "model": "whisper.cpp tiny (cpu)",
   "prompt": "` + *PromptHash("Zoom") + `",
   "duration": 0.0,
   "words": []
  },
  "seg_01.webm": {
   "size": 12551,
   "model": "whisper.cpp tiny (cpu)",
   "prompt": null,
   "duration": 4.312,
   "words": [
    {
     "w": " Throw",
     "s": 0.6,
     "e": 0.912,
     "p": 0.5
    },
    {
     "w": " out.",
     "s": 1.0,
     "e": 1.2,
     "p": 1.0
    }
   ]
  }
 }
}`
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("transcript.json =\n%s\nwant\n%s", b, want)
	}

	back, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Names(), []string{"seg_02.ogg", "seg_01.webm"}) {
		t.Errorf("names = %v", back.Names())
	}
	g1, ok := back.File("seg_01.webm")
	if !ok || g1.Size != f1.Size || g1.Model != f1.Model || g1.Prompt != nil || g1.Duration != 4.312 ||
		!slices.Equal(g1.Words, []Word{{" Throw", 0.6, 0.912, 0.5}, {" out.", 1, 1.2, 1}}) {
		t.Errorf("seg_01 = %+v", g1)
	}
	g2, ok := back.File("seg_02.ogg")
	if !ok || g2.Prompt == nil || *g2.Prompt != *f2.Prompt || len(g2.Words) != 0 {
		t.Errorf("seg_02 = %+v", g2)
	}
}

// A transcript written by legacy notes.py (faster-whisper) reads the same way, and what retake doesn't change is
// written back byte for byte.
func TestTranscriptKeepsWhatItDoesntChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), TranscriptFile)
	legacy := `{"files": {"seg_01.webm": {"size": 52113, "model": "whisper large-v3 (cuda)", "prompt": null, ` +
		`"duration": 6.48, "words": [{"w": " Zoom", "s": 0.84, "e": 1.1, "p": 0.982}, {"w": " here.", "s": 1.1, "e": 1.5}],` +
		` "extra": [1, 2.50]}}, "version": 7}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := tr.File("seg_01.webm")
	if !ok || f.Model != "whisper large-v3 (cuda)" || f.Size != 52113 || f.Duration != 6.48 ||
		!slices.Equal(f.Words, []Word{{" Zoom", 0.84, 1.1, 0.982}, {" here.", 1.1, 1.5, 1}}) {
		t.Errorf("legacy entry = %+v", f)
	}
	if err := tr.Set("seg_02.webm", FileTranscript{Size: 1, Model: "m"}); err != nil {
		t.Fatal(err)
	}
	b, err := tr.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"extra": [1, 2.50]}`) || !strings.HasSuffix(s, `,"version":7}`) ||
		!strings.Contains(s, `"seg_02.webm":{"size":1,"model":"m","prompt":null,"duration":0.0,"words":[]}`) {
		t.Errorf("marshal = %s", s)
	}
}

func TestReadTranscriptOddFiles(t *testing.T) {
	dir := t.TempDir()
	if tr, err := ReadTranscript(filepath.Join(dir, "missing.json")); err != nil || len(tr.Names()) != 0 {
		t.Errorf("missing: %v %v", tr, err)
	}
	for name, body := range map[string]string{"files-list.json": `{"files": []}`, "no-files.json": `{}`} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if tr, err := ReadTranscript(p); err != nil || len(tr.Names()) != 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`[1, 2]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTranscript(bad); err == nil {
		t.Error("a JSON list read as a transcript")
	}
	// An entry that doesn't parse counts as missing, so it's transcribed again.
	odd := filepath.Join(dir, "odd.json")
	if err := os.WriteFile(odd, []byte(`{"files": {"a.webm": {"size": "big"}, "b.webm": {"model": "m"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := ReadTranscript(odd)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.webm", "b.webm", "c.webm"} {
		if _, ok := tr.File(n); ok {
			t.Errorf("%s reads as transcribed", n)
		}
	}
}

func TestSkipRule(t *testing.T) {
	p1, p2 := PromptHash("Zoom, B-roll"), PromptHash("Zoom, B-roll")
	f := FileTranscript{Size: 100, Model: "whisper.cpp small (cpu)", Prompt: p1}
	for _, tc := range []struct {
		name   string
		size   int64
		model  string
		prompt *string
		want   bool
	}{
		{"same", 100, "whisper.cpp small (cpu)", p2, true},
		{"file grew", 120, "whisper.cpp small (cpu)", p2, false},
		{"other model", 100, "whisper.cpp large-v3 (cuda)", p2, false},
		{"legacy model name", 100, "whisper small (cpu)", p2, false},
		{"other prompt", 100, "whisper.cpp small (cpu)", PromptHash("other"), false},
		{"no prompt now", 100, "whisper.cpp small (cpu)", nil, false},
	} {
		if got := f.Current(tc.size, tc.model, tc.prompt); got != tc.want {
			t.Errorf("%s: Current = %v", tc.name, got)
		}
	}
	if !(FileTranscript{Size: 1, Model: "m"}).Current(1, "m", nil) {
		t.Error("no prompt either side should match")
	}
	if PromptHash("") != nil || *PromptHash("a") == *PromptHash("b") || !strings.HasPrefix(*PromptHash("a"), "sha256:") {
		t.Error("PromptHash")
	}
}
