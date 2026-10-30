package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// Command runs an external program (config: [transcribe] engine = "command") for each file.
//
// The command line is split like a shell would (quotes group, a backslash escapes a quote, a space or another
// backslash) but run without one. The program gets the path of a 16 kHz mono WAV: in place of a {audio} argument, or
// as the last argument. RETAKE_PROMPT and RETAKE_LANGUAGE carry the Options. It prints the words on stdout as JSON,
// either a list or {"words": [...]}, each {"w", "s", "e", "p"} (or word/text, start, end, probability/prob), with
// times in seconds from the start of the file. scripts/faster-whisper-words.py is an example.
type Command struct {
	argv    []string
	tempDir string
}

// NewCommand parses the command line. tempDir holds the decoded WAV ("" = os.TempDir()).
func NewCommand(cmdline, tempDir string) (*Command, error) {
	argv, err := splitCommand(cmdline)
	if err != nil {
		return nil, fmt.Errorf("transcribe command: %w", err)
	}
	if len(argv) == 0 {
		return nil, errors.New(`engine = "command" needs [transcribe] command, a program that prints words as JSON`)
	}
	return &Command{argv: argv, tempDir: tempDir}, nil
}

// Name is "command " plus the command line, with the program's folder left out.
func (c *Command) Name() string {
	return strings.Join(append([]string{"command", filepath.Base(c.argv[0])}, c.argv[1:]...), " ")
}

// commandOutputMax caps what we keep of the program's stdout.
const commandOutputMax = 64 << 20

// Transcribe runs the program on a 16 kHz WAV of audioPath and parses the words it prints.
func (c *Command) Transcribe(ctx context.Context, audioPath string, opts Options) ([]Word, error) {
	tmp, err := os.MkdirTemp(c.tempDir, "retake-command-")
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
	args, placed := make([]string, 0, len(c.argv)), false
	for _, a := range c.argv[1:] {
		if strings.Contains(a, "{audio}") {
			a, placed = strings.ReplaceAll(a, "{audio}", wav), true
		}
		args = append(args, a)
	}
	if !placed {
		args = append(args, wav)
	}
	cmd := exec.CommandContext(ctx, c.argv[0], args...) //nolint:gosec // the configured command
	cmd.Env = append(os.Environ(), "RETAKE_PROMPT="+opts.Prompt, "RETAKE_LANGUAGE="+opts.Language)
	var stdout limitedBuffer
	var stderr tail
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed on %s: %w: %s", filepath.Base(c.argv[0]), filepath.Base(audioPath), err, stderr.String())
	}
	if stdout.over {
		return nil, fmt.Errorf("%s printed more than %d MiB", filepath.Base(c.argv[0]), commandOutputMax>>20)
	}
	words, err := ParseWordsJSON(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(c.argv[0]), err)
	}
	return words, nil
}

// limitedBuffer keeps up to commandOutputMax bytes and remembers whether there was more.
type limitedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := commandOutputMax - b.Len(); len(p) > room {
		b.over = true
		p = p[:max(room, 0)]
	}
	_, _ = b.Buffer.Write(p)
	return len(p), nil
}

// ParseWordsJSON reads words printed by a command engine: a JSON list of words, or an object whose "words" is one.
// A word is {"w", "s", "e", "p"} or {"word"|"text", "start", "end", "probability"|"prob"}; a missing probability is 1.
// Words should keep their leading space like whisper's (" word"); if none has one, a space is added to each.
func ParseWordsJSON(data []byte) ([]Word, error) {
	data = bytes.TrimSpace(data)
	var list []json.RawMessage
	if len(data) > 0 && data[0] == '{' {
		var obj struct {
			Words []json.RawMessage `json:"words"`
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return nil, fmt.Errorf("words JSON: %w", err)
		}
		if obj.Words == nil {
			return nil, errors.New(`words JSON: no "words" list`)
		}
		list = obj.Words
	} else if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("words JSON (want a list or {\"words\": [...]}): %w", err)
	}
	words := make([]Word, 0, len(list))
	spaced := false
	for i, raw := range list {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("words JSON: word %d isn't an object", i)
		}
		text, ok1 := firstString(m, "w", "word", "text")
		start, ok2 := firstNumber(m, "s", "start")
		end, ok3 := firstNumber(m, "e", "end")
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("words JSON: word %d needs w, s and e (or word, start and end)", i)
		}
		prob, ok := firstNumber(m, "p", "probability", "prob")
		if !ok {
			prob = 1
		}
		if text == "" {
			continue
		}
		if unicode.IsSpace([]rune(text)[0]) {
			spaced = true
		}
		words = append(words, Word{Text: text, Start: start, End: max(end, start), Prob: clamp(prob, 0, 1)})
	}
	if !spaced {
		for i := range words {
			words[i].Text = " " + words[i].Text
		}
	}
	return words, nil
}

func firstString(m map[string]json.RawMessage, keys ...string) (string, bool) {
	for _, k := range keys {
		var s string
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &s) == nil {
			return s, true
		}
	}
	return "", false
}

func firstNumber(m map[string]json.RawMessage, keys ...string) (float64, bool) {
	for _, k := range keys {
		var f float64
		if raw, ok := m[k]; ok && json.Unmarshal(raw, &f) == nil {
			return f, true
		}
	}
	return 0, false
}

// splitCommand splits a command line into arguments: whitespace separates them, '…' is literal, "…" groups with
// \" and \\ as escapes, and outside quotes a backslash escapes only a quote, a space or a backslash (so a Windows
// path like C:\tools\fw.exe stays as it is).
func splitCommand(s string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inArg := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\'':
			inArg = true
			j := i + 1
			for j < len(rs) && rs[j] != '\'' {
				j++
			}
			if j == len(rs) {
				return nil, errors.New("unclosed ' quote")
			}
			cur.WriteString(string(rs[i+1 : j]))
			i = j
		case r == '"':
			inArg = true
			j := i + 1
			for ; j < len(rs) && rs[j] != '"'; j++ {
				if rs[j] == '\\' && j+1 < len(rs) && (rs[j+1] == '"' || rs[j+1] == '\\') {
					j++
				}
				cur.WriteRune(rs[j])
			}
			if j == len(rs) {
				return nil, errors.New(`unclosed " quote`)
			}
			i = j
		case r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\'' || rs[i+1] == '\\' || unicode.IsSpace(rs[i+1])):
			inArg = true
			cur.WriteRune(rs[i+1])
			i++
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			inArg = true
			cur.WriteRune(r)
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, nil
}
