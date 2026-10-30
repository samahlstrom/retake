package transcribe

import (
	"bytes"
	"math"
	"os"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// realOutput reads real whisper-cli output (tiny; retake's flags) for testdata/gaps.webm: the JSON, with its
// machine-specific systeminfo and model path replaced, and the VAD table lines of its log, read through cliLog.
func realOutput(t *testing.T) ([]byte, *cliLog) {
	t.Helper()
	data, err := os.ReadFile("testdata/whisper-cli-gaps.json")
	if err != nil {
		t.Fatal(err)
	}
	logText, err := os.ReadFile("testdata/whisper-cli-gaps.log")
	if err != nil {
		t.Fatal(err)
	}
	var log cliLog
	for _, chunk := range bytes.SplitAfter(logText, []byte(" ")) { // in pieces, as a pipe delivers it
		_, _ = log.Write(chunk)
	}
	return data, &log
}

func checkOrderedWords(t *testing.T, words []Word) string {
	t.Helper()
	var text strings.Builder
	for i, w := range words {
		text.WriteString(w.Text)
		if !strings.HasPrefix(w.Text, " ") {
			t.Errorf("word %d %q has no leading space", i, w.Text)
		}
		if w.End < w.Start || w.Prob < 0 || w.Prob > 1 {
			t.Errorf("word %d: %+v", i, w)
		}
		if i > 0 && w.Start < words[i-1].Start {
			t.Errorf("word %d starts before word %d: %+v", i, i-1, w)
		}
	}
	return strings.TrimSpace(text.String())
}

// gaps.webm says "Cut the intro and zoom in on the map." from 1.0 to 4.2 s, then "Make the caption bigger here."
// from 7.2 to 9.7 s (testdata/speech.json).
func TestParseCLIJSONRealOutput(t *testing.T) {
	data, log := realOutput(t)
	if len(log.vad) != 2 || log.vad[1] != (vadSegment{7.03, 9.51, 3.35, 5.83}) {
		t.Fatalf("VAD table = %+v", log.vad)
	}
	words, err := parseCLIJSON(data, log.vad)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkOrderedWords(t, words); got != "cut the intro and zoom in on the map. Make the caption bigger here." {
		t.Errorf("text = %q", got)
	}
	for _, w := range words {
		if w.Start > 4.3 && w.Start < 7.0 || w.End > 4.3 && w.End < 7.0 {
			t.Errorf("a word in the pause: %+v", w)
		}
		if w.Start < 0.8 || w.End > 9.8 {
			t.Errorf("a word outside the speech: %+v", w)
		}
	}
	// "Make" opens the second region: placed by its tokens, not by whisper's segment start in the first one's tail.
	if w := words[9]; w.Text != " Make" || w.Start < 7.0 || w.End-w.Start < 0.2 {
		t.Errorf("Make = %+v", w)
	}

	// Without the table, segment offsets place the words; ends still stop at each word's own tokens.
	words, err = parseCLIJSON(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkOrderedWords(t, words); got != "cut the intro and zoom in on the map. Make the caption bigger here." {
		t.Errorf("no table: text = %q", got)
	}
	if w := words[8]; w.Text != " map." || w.End > 4.3 {
		t.Errorf("no table: map. = %+v (its end runs into the pause)", w)
	}
}

func TestPlace(t *testing.T) {
	vad := []vadSegment{{0.89, 4.04, 0, 3.15}, {7.03, 9.51, 3.35, 5.83}}
	for _, tc := range []struct{ r0, r1, s, e float64 }{
		{0.5, 0.9, 1.39, 1.79},   // inside the first region
		{3.16, 3.5, 7.03, 7.37},  // mostly in the second region, starting early: moved in whole
		{2.9, 3.2, 3.79, 4.09},   // mostly in the first, running into the overlap
		{3.0, 3.4, 3.89, 4.14},   // ends past the region and its overlap: clamped
		{3.27, 3.30, 7.03, 7.06}, // in the silence between: the nearer region
		{5.0, 5.4, 8.68, 9.08},   // second region
		{6.0, 6.2, 9.61, 9.61},   // past the last region: clamped to its end
		{-0.1, 0.1, 0.89, 1.09},  // before the first
	} {
		s, e := place(vad, tc.r0, tc.r1)
		if math.Abs(s-tc.s) > 1e-9 || math.Abs(e-tc.e) > 1e-9 {
			t.Errorf("place(%.2f, %.2f) = %.2f, %.2f; want %.2f, %.2f", tc.r0, tc.r1, s, e, tc.s, tc.e)
		}
	}
	if s, e := place(vad, 4.0, 4.0); s != e || math.Abs(s-7.68) > 1e-9 {
		t.Errorf("a point: %v %v", s, e)
	}
}

func TestCLILog(t *testing.T) {
	var log cliLog
	for _, s := range []string{"whisper_backend_init_gpu: no G", "PU found\nwhisper_vad: vad_segment_info: orig_start: 1.5",
		"0, orig_end: 2.00, vad_start: 0.00, vad_end: 0.50\nvad_segment_info: orig_start: x\n", strings.Repeat("y", 3*tailMax),
		"\nwhisper_vad: vad_segment_info: orig_start: 3.00, orig_end: 4.00, vad_start: 0.70, vad_end: 1.70\n"} {
		if n, err := log.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatal(n, err)
		}
	}
	if !log.noGPU || len(log.vad) != 2 || log.vad[0] != (vadSegment{1.5, 2, 0, 0.5}) || log.vad[1].origStart != 3 {
		t.Errorf("log = %+v %v", log.vad, log.noGPU)
	}
	if len(log.b) > tailMax || !strings.Contains(log.String(), "vad_end: 1.70") {
		t.Errorf("tail = %q", log.String())
	}
}

func TestParseCLIJSONSegments(t *testing.T) {
	const doc = `{"transcription": [
	{"offsets": {"from": 70, "to": 170}, "text": "", "tokens": [
		{"text": "[_BEG_]", "offsets": {"from": 0, "to": 0}, "id": 50364, "p": 0.98, "t_dtw": -1}]},
	{"offsets": {"from": 100, "to": 410}, "text": " throw", "tokens": [
		{"text": " throw", "offsets": {"from": 30, "to": 340}, "id": 3507, "p": 0.4, "t_dtw": -1}]},
	{"offsets": {"from": 2280, "to": 6620}, "text": " clip.", "tokens": [
		{"text": " clip", "offsets": {"from": 2210, "to": 2420}, "id": 7353, "p": 0.6, "t_dtw": -1},
		{"text": ".", "offsets": {"from": 2490, "to": 2520}, "id": 13, "p": 0.4, "t_dtw": -1},
		{"text": "[_TT_128]", "offsets": {"from": 2560, "to": 2560}, "id": 50492, "p": 0.05, "t_dtw": -1}]}
	]}`
	words, err := parseCLIJSON([]byte(doc), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Word{{" throw", 0.1, 0.41, 0.4}, {" clip.", 2.28, 2.59, 0.5}}
	if len(words) != len(want) {
		t.Fatalf("words = %+v", words)
	}
	for i := range want {
		g, w := words[i], want[i]
		if g.Text != w.Text || !near(g.Start, w.Start) || !near(g.End, w.End) || !near(g.Prob, w.Prob) {
			t.Errorf("word %d = %+v, want %+v", i, g, w)
		}
	}
}

// Without --max-len 1 a segment holds a sentence: its words are split on the tokens that start with a space and
// placed by their token times relative to the segment's start.
func TestParseCLIJSONMultiWordSegment(t *testing.T) {
	const doc = `{"transcription": [{"offsets": {"from": 1000, "to": 2000}, "text": " Add it back.", "tokens": [
		{"text": "[_BEG_]", "offsets": {"from": 0, "to": 0}, "p": 0.9},
		{"text": " Add", "offsets": {"from": 500, "to": 800}, "p": 0.9},
		{"text": " it", "offsets": {"from": 800, "to": 950}, "p": 0.7},
		{"text": " back", "offsets": {"from": 1000, "to": 1300}, "p": 1.0},
		{"text": ".", "offsets": {"from": 1300, "to": 1350}, "p": 0.8}]}]}`
	words, err := parseCLIJSON([]byte(doc), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Word{{" Add", 1.0, 1.3, 0.9}, {" it", 1.3, 1.45, 0.7}, {" back.", 1.5, 1.85, 0.9}}
	if len(words) != len(want) {
		t.Fatalf("words = %+v", words)
	}
	for i := range want {
		g, w := words[i], want[i]
		if g.Text != w.Text || !near(g.Start, w.Start) || !near(g.End, w.End) || !near(g.Prob, w.Prob) {
			t.Errorf("word %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestParseCLIJSONOddOutput(t *testing.T) {
	t.Run("no token timestamps", func(t *testing.T) {
		words, err := parseCLIJSON([]byte(`{"transcription": [{"offsets": {"from": 500, "to": 900}, "text": " ok",
			"tokens": [{"text": " ok", "p": 0.5}]}]}`), nil)
		if err != nil || len(words) != 1 || words[0] != (Word{" ok", 0.5, 0.9, 0.5}) {
			t.Errorf("%+v %v", words, err)
		}
	})
	t.Run("no tokens", func(t *testing.T) {
		words, err := parseCLIJSON([]byte(`{"transcription": [{"offsets": {"from": 500, "to": 900}, "text": " ok"}]}`), nil)
		if err != nil || len(words) != 1 || words[0] != (Word{" ok", 0.5, 0.9, 1}) {
			t.Errorf("%+v %v", words, err)
		}
	})
	t.Run("raw control character and nan", func(t *testing.T) {
		words, err := parseCLIJSON([]byte("{\"transcription\": [{\"offsets\": {\"from\": 0, \"to\": 100}, \"text\": \" a\tb\","+
			"\"tokens\": [{\"text\": \" a\tb\", \"offsets\": {\"from\": 0, \"to\": 100}, \"p\": -nan, \"t_dtw\": -1}]}]}"), nil)
		if err != nil || len(words) != 1 || words[0].Text != " a\tb" || words[0].Prob != 0 {
			t.Errorf("%+v %v", words, err)
		}
	})
	t.Run("nothing said", func(t *testing.T) {
		words, err := parseCLIJSON([]byte(`{"transcription": []}`), nil)
		if err != nil || words == nil || len(words) != 0 {
			t.Errorf("%#v %v", words, err)
		}
	})
	t.Run("not JSON", func(t *testing.T) {
		if _, err := parseCLIJSON([]byte(`whisper_init: failed`), nil); err == nil {
			t.Error("no error")
		}
	})
}

func TestSanitizeCLIJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"p": nan, "q": -inf, "r": inf}`: `{"p": null, "q": null, "r": null}`,
		`{"t": "nan inf"}`:                `{"t": "nan inf"}`,
		"{\"t\": \"a\nb\\\"c\"}":          `{"t": "a\u000ab\"c"}`,
		`{"t": "\\", "u": nan}`:           `{"t": "\\", "u": null}`,
		`{"ok": true, "n": null}`:         `{"ok": true, "n": null}`,
	} {
		if got := string(sanitizeCLIJSON([]byte(in))); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
