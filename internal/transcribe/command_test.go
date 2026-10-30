package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/testmedia"
)

func TestSplitCommand(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
		err  bool
	}{
		{"python3 fw.py --model small", []string{"python3", "fw.py", "--model", "small"}, false},
		{`  "/opt/my tools/fw" --x  `, []string{"/opt/my tools/fw", "--x"}, false},
		{`fw --prompt 'it''s' --q "a \"b\" \\ c"`, []string{"fw", "--prompt", "its", "--q", `a "b" \ c`}, false},
		{`C:\tools\fw.exe --in {audio}`, []string{`C:\tools\fw.exe`, "--in", "{audio}"}, false},
		{`my\ prog arg\"q`, []string{"my prog", `arg"q`}, false},
		{`fw ""`, []string{"fw", ""}, false},
		{"", nil, false},
		{`fw "open`, nil, true},
		{`fw 'open`, nil, true},
	} {
		got, err := splitCommand(tc.in)
		if (err != nil) != tc.err || !slices.Equal(got, tc.want) {
			t.Errorf("splitCommand(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestParseWordsJSON(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []Word
		err      string
	}{
		{"legacy keys", `[{"w": " Hi", "s": 0.1, "e": 0.4, "p": 0.9}, {"w": " there.", "s": 0.5, "e": 0.9, "p": 0.8}]`,
			[]Word{{" Hi", 0.1, 0.4, 0.9}, {" there.", 0.5, 0.9, 0.8}}, ""},
		{"object with long keys", `{"model": "x", "words": [{"word": " Hi", "start": 1, "end": 2, "probability": 0.5}]}`,
			[]Word{{" Hi", 1, 2, 0.5}}, ""},
		{"text and prob", `{"words": [{"text": " a", "start": 1, "end": 2, "prob": 0.25}]}`, []Word{{" a", 1, 2, 0.25}}, ""},
		{"no probability is 1", `[{"w": " a", "s": 0, "e": 1}]`, []Word{{" a", 0, 1, 1}}, ""},
		{"no leading spaces get one", `[{"w": "add", "s": 0, "e": 1}, {"w": "back", "s": 1, "e": 2}]`,
			[]Word{{" add", 0, 1, 1}, {" back", 1, 2, 1}}, ""},
		{"spaced words keep their punctuation token", `[{"w": " it", "s": 0, "e": 1}, {"w": "'s", "s": 1, "e": 2}]`,
			[]Word{{" it", 0, 1, 1}, {"'s", 1, 2, 1}}, ""},
		{"end before start, p out of range", `[{"w": " a", "s": 2, "e": 1, "p": 1.5}]`, []Word{{" a", 2, 2, 1}}, ""},
		{"empty words dropped", `[{"w": "", "s": 0, "e": 1}]`, []Word{}, ""},
		{"empty list", `[]`, []Word{}, ""},
		{"no words key", `{"text": "hi"}`, nil, `no "words"`},
		{"missing time", `[{"w": " a", "s": 0}]`, nil, "needs w, s and e"},
		{"not an object", `[1]`, nil, "isn't an object"},
		{"not JSON", `Traceback (most recent call last):`, nil, "want a list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseWordsJSON([]byte(tc.in))
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Errorf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !slices.Equal(got, tc.want) || got == nil {
				t.Errorf("got %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func fakeCommandLine(t *testing.T, extra string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RETAKE_FAKE", "command")
	return `"` + strings.ReplaceAll(exe, `\`, `\\`) + `" ` + extra
}

func TestCommandTranscribe(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "seg_01.wav")
	writeWAV(t, wav, 1)
	t.Setenv("RETAKE_FAKE_OUT", `{"words": [{"w": " $PROMPT", "s": 0.25, "e": 0.5, "p": 0.75}]}`)
	for _, extra := range []string{"--model small", "--audio {audio} --model small"} {
		c, err := NewCommand(fakeCommandLine(t, extra), t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(c.Name(), "command transcribe.test") || !strings.HasSuffix(c.Name(), " --model small") {
			t.Errorf("Name = %q", c.Name())
		}
		words, err := c.Transcribe(context.Background(), wav, Options{Prompt: "hello"})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(words, []Word{{" hello", 0.25, 0.5, 0.75}}) {
			t.Errorf("%s: words = %+v", extra, words)
		}
	}

	t.Setenv("RETAKE_FAKE_EXIT", "1")
	c, err := NewCommand(fakeCommandLine(t, ""), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Transcribe(context.Background(), wav, Options{}); err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("failing command: err = %v", err)
	}
}

func TestCommandDecodesFirst(t *testing.T) {
	testmedia.Need(t)
	t.Setenv("RETAKE_FAKE_OUT", `[]`)
	c, err := NewCommand(fakeCommandLine(t, ""), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The fake fails unless it's given a 16 kHz mono WAV.
	if words, err := c.Transcribe(context.Background(), "testdata/seg_02.ogg", Options{}); err != nil || len(words) != 0 {
		t.Errorf("words %v, err %v", words, err)
	}
}

func TestNewCommandErrors(t *testing.T) {
	if _, err := NewCommand("  ", ""); err == nil || !strings.Contains(err.Error(), "[transcribe] command") {
		t.Errorf("empty: %v", err)
	}
	if _, err := NewCommand(`fw "x`, ""); err == nil || !strings.Contains(err.Error(), "unclosed") {
		t.Errorf("unclosed: %v", err)
	}
}
