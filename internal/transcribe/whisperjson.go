package transcribe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// cliLog is whisper-cli's stderr as it runs: the VAD table, whether the model got a GPU, and the tail for errors.
type cliLog struct {
	tail
	line  []byte
	vad   []vadSegment
	noGPU bool // "whisper_backend_init_gpu: no GPU found": the model, or the VAD model (which never uses the GPU)
	gpu   bool // "whisper_backend_init_gpu: using CUDA0 backend": the model is on a GPU
}

// onCPU reports whether whisper-cli said it found no GPU for the model: only when no context got one, since the VAD
// context always says so.
func (c *cliLog) onCPU() bool { return c.noGPU && !c.gpu }

func (c *cliLog) Write(p []byte) (int, error) {
	_, _ = c.tail.Write(p)
	c.line = append(c.line, p...)
	for {
		i := bytes.IndexByte(c.line, '\n')
		if i < 0 {
			break
		}
		l := c.line[:i]
		if s, ok := parseVADLine(l); ok {
			c.vad = append(c.vad, s)
		}
		if i := bytes.Index(l, []byte("whisper_backend_init_gpu: ")); i >= 0 {
			msg := l[i+len("whisper_backend_init_gpu: "):]
			c.noGPU = c.noGPU || bytes.HasPrefix(msg, []byte("no GPU found"))
			c.gpu = c.gpu || bytes.HasPrefix(msg, []byte("using "))
		}
		c.line = c.line[i+1:]
	}
	if len(c.line) > tailMax { // a very long line: we only want short ones
		c.line = c.line[:0]
	}
	return len(p), nil
}

// cliOutput is the part of whisper-cli's --output-json-full file that we read. Offsets are milliseconds.
type cliOutput struct {
	Transcription []cliSegment `json:"transcription"`
}

type cliOffsets struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type cliSegment struct {
	Offsets cliOffsets `json:"offsets"`
	Text    string     `json:"text"`
	Tokens  []cliToken `json:"tokens"`
}

type cliToken struct {
	Text    string      `json:"text"`
	Offsets *cliOffsets `json:"offsets"` // only with token timestamps
	P       float64     `json:"p"`
}

// vadSegment is one speech region that whisper.cpp's VAD kept: where it is in the file, and where it is in the
// trimmed audio whisper decoded (the regions back to back, with 0.1 s of overlap and 0.1 s of silence between them).
// Seconds.
type vadSegment struct{ origStart, origEnd, vadStart, vadEnd float64 }

var vadInfoRE = regexp.MustCompile(`vad_segment_info: orig_start: ([0-9.]+), orig_end: ([0-9.]+), vad_start: ([0-9.]+), vad_end: ([0-9.]+)`)

// parseVADLine reads one of whisper.cpp's "vad_segment_info:" log lines.
func parseVADLine(line []byte) (vadSegment, bool) {
	m := vadInfoRE.FindSubmatch(line)
	if m == nil {
		return vadSegment{}, false
	}
	var f [4]float64
	for i := range f {
		v, err := strconv.ParseFloat(string(m[i+1]), 64)
		if err != nil {
			return vadSegment{}, false
		}
		f[i] = v
	}
	return vadSegment{f[0], f[1], f[2], f[3]}, true
}

// parseCLIJSON turns whisper-cli's --output-json-full output into words. vad is the VAD table from its log; nil if
// there was none.
//
// retake runs whisper-cli with --max-len 1 --split-on-word, so each segment is one word (with any punctuation that
// follows it), and the tokens carry times on the trimmed clock. With the VAD table, a word is placed in the speech
// region its tokens overlap most, at its token times shifted to the file's clock. That matters for the first word
// after a pause: its segment starts at whisper's timestamp token, which often sits in the previous region's tail,
// and whisper.cpp maps that back to before the pause, seconds early.
//
// Without the table (a whisper-cli that logs differently), segment offsets, which whisper.cpp maps back itself, place
// each word, and its tokens' span gives its length: a word ends at its start plus that span, instead of at the next
// word's start (which, after a pause, is seconds later). A segment that holds several words (an older whisper-cli,
// or no --max-len) is split on the tokens that start with a space. A word's probability is the mean of its tokens',
// as in faster-whisper.
func parseCLIJSON(data []byte, vad []vadSegment) ([]Word, error) {
	var out cliOutput
	if err := json.Unmarshal(sanitizeCLIJSON(data), &out); err != nil {
		return nil, fmt.Errorf("whisper-cli's JSON output: %w", err)
	}
	words := []Word{}
	for _, seg := range out.Transcription {
		words = append(words, segmentWords(seg, vad)...)
	}
	return words, nil
}

// place maps a word's span on the trimmed clock (seconds) to the file's clock, inside the speech region that the
// span overlaps most (or the nearest one). A span that starts before its region is moved into it whole.
func place(vad []vadSegment, r0, r1 float64) (float64, float64) {
	best, bestOv := 0, math.Inf(-1)
	for i, s := range vad {
		if ov := min(r1, s.vadEnd) - max(r0, s.vadStart); ov > bestOv {
			best, bestOv = i, ov
		}
	}
	s := vad[best]
	start, end := s.origStart+(r0-s.vadStart), s.origStart+(r1-s.vadStart)
	if start < s.origStart {
		end += s.origStart - start
		start = s.origStart
	}
	limit := s.origEnd + vadOverlap
	return min(start, limit), clamp(end, min(start, limit), limit)
}

// vadOverlap is how far whisper.cpp lets a region run past its end (its default --vad-samples-overlap).
const vadOverlap = 0.1

type tokenGroup struct {
	text       string
	ps         []float64
	raw0, raw1 int64 // token offsets (ms, on the VAD-trimmed clock)
	timed      bool
}

func segmentWords(seg cliSegment, vad []vadSegment) []Word {
	var groups []tokenGroup
	base := int64(-1) // the first text token's start on the trimmed clock: it lines up with the segment's start
	for _, tok := range seg.Tokens {
		if isSpecialToken(tok.Text) {
			continue
		}
		if len(groups) == 0 || strings.HasPrefix(tok.Text, " ") && strings.TrimSpace(groups[len(groups)-1].text) != "" {
			groups = append(groups, tokenGroup{})
		}
		g := &groups[len(groups)-1]
		g.text += tok.Text
		g.ps = append(g.ps, tok.P)
		if tok.Offsets != nil {
			if !g.timed {
				g.raw0, g.timed = tok.Offsets.From, true
			}
			g.raw1 = tok.Offsets.To
			if base < 0 {
				base = tok.Offsets.From
			}
		}
	}
	if len(groups) == 0 && strings.TrimSpace(seg.Text) != "" { // no tokens in the output: the segment is the word
		groups = append(groups, tokenGroup{text: seg.Text})
	}
	s0, s1 := float64(seg.Offsets.From)/1000, float64(seg.Offsets.To)/1000
	s1 = max(s1, s0)
	words := make([]Word, 0, len(groups))
	for _, g := range groups {
		if strings.TrimSpace(g.text) == "" {
			continue
		}
		w := Word{Text: g.text, Start: s0, End: s1, Prob: 1}
		switch {
		case g.timed && len(vad) > 0:
			w.Start, w.End = place(vad, float64(g.raw0)/1000, float64(max(g.raw1, g.raw0))/1000)
		case g.timed && base >= 0:
			w.Start = clamp(s0+float64(g.raw0-base)/1000, s0, s1)
			w.End = clamp(s0+float64(g.raw1-base)/1000, w.Start, s1)
		}
		if len(g.ps) > 0 {
			sum := 0.0
			for _, p := range g.ps {
				sum += p
			}
			w.Prob = clamp(sum/float64(len(g.ps)), 0, 1)
		}
		words = append(words, w)
	}
	return words
}

// isSpecialToken reports whisper.cpp's names for non-text tokens: [_BEG_], [_TT_123], [_SOT_], [_EOT_], ….
func isSpecialToken(text string) bool {
	return strings.HasPrefix(text, "[_") && strings.HasSuffix(text, "]") || strings.HasPrefix(text, "<|") && strings.HasSuffix(text, "|>")
}

func clamp(v, lo, hi float64) float64 { return max(lo, min(v, hi)) }

// sanitizeCLIJSON makes whisper-cli's hand-written JSON parseable: it escapes only quotes and backslashes in
// strings, so a control character in a token comes out raw, and C++ prints a NaN probability as nan.
func sanitizeCLIJSON(b []byte) []byte {
	out := make([]byte, 0, len(b)+16)
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			case c < 0x20:
				out = fmt.Appendf(out, `\u%04x`, c)
				continue
			}
			out = append(out, c)
			continue
		}
		if c == '"' {
			inStr = true
		} else if n := nonFinite(b[i:]); n > 0 {
			out = append(out, "null"...)
			i += n - 1
			continue
		}
		out = append(out, c)
	}
	return out
}

func nonFinite(b []byte) int {
	for _, w := range []string{"-nan", "nan", "-inf", "inf"} {
		if len(b) >= len(w) && strings.EqualFold(string(b[:len(w)]), w) {
			return len(w)
		}
	}
	return 0
}
