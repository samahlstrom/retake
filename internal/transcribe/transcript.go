package transcribe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/store"
)

// TranscriptFile is the name of a take's transcript, beside its audio.
const TranscriptFile = "transcript.json"

// FileTranscript is one audio file's entry in transcript.json (the legacy format): {size, model, prompt, duration,
// words: [{w, s, e, p}]}.
type FileTranscript struct {
	Size     int64   // the audio file's size in bytes when it was transcribed
	Model    string  // the Transcriber's Name()
	Prompt   *string // PromptHash of Options.Prompt; nil = no prompt
	Duration float64 // seconds of audio
	Words    []Word  // times in seconds from the file's start
}

// Current is the skip rule: the entry is reused when the file still has the same size and was transcribed with the
// same model and prompt.
func (f FileTranscript) Current(size int64, model string, prompt *string) bool {
	samePrompt := f.Prompt == nil && prompt == nil || f.Prompt != nil && prompt != nil && *f.Prompt == *prompt
	return f.Size == size && f.Model == model && samePrompt
}

// PromptHash identifies a prompt in transcript.json without storing it. An empty prompt is nil.
func PromptHash(prompt string) *string {
	if prompt == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(prompt))
	h := "sha256:" + hex.EncodeToString(sum[:8])
	return &h
}

// Transcript is a take's transcript.json: {"files": {"seg_01.webm": {...}, ...}}. Keys and entries it doesn't
// change are written back exactly as they were read, in their order.
type Transcript struct {
	doc   *jsonx.Object
	files *jsonx.Object
}

// NewTranscript returns an empty transcript.
func NewTranscript() *Transcript {
	return &Transcript{doc: jsonx.NewObject(), files: jsonx.NewObject()}
}

// ReadTranscript reads transcript.json. A missing file is an empty transcript; so is a "files" that isn't an object
// (as legacy notes.py treats it). A file that isn't a JSON object is an error.
func ReadTranscript(path string) (*Transcript, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a take's own file
	if errors.Is(err, fs.ErrNotExist) {
		return NewTranscript(), nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := jsonx.ParseObject(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	t := &Transcript{doc: doc, files: jsonx.NewObject()}
	if raw, ok := doc.Get("files"); ok && jsonx.Kind(raw) == "object" {
		if files, err := jsonx.ParseObject(raw); err == nil {
			t.files = files
		}
	}
	return t, nil
}

// Names returns the audio file names in the transcript, in file order.
func (t *Transcript) Names() []string { return t.files.Keys() }

// File returns one file's entry. An entry that doesn't parse is reported missing, so it gets transcribed again.
func (t *Transcript) File(name string) (FileTranscript, bool) {
	raw, ok := t.files.Get(name)
	if !ok {
		return FileTranscript{}, false
	}
	var in struct {
		Size     *float64 `json:"size"`
		Model    *string  `json:"model"`
		Prompt   *string  `json:"prompt"`
		Duration float64  `json:"duration"`
		Words    []struct {
			W string   `json:"w"`
			S float64  `json:"s"`
			E float64  `json:"e"`
			P *float64 `json:"p"`
		} `json:"words"`
	}
	if err := json.Unmarshal(raw, &in); err != nil || in.Size == nil || in.Model == nil {
		return FileTranscript{}, false
	}
	f := FileTranscript{Size: int64(*in.Size), Model: *in.Model, Prompt: in.Prompt, Duration: in.Duration,
		Words: make([]Word, 0, len(in.Words))}
	for _, w := range in.Words {
		p := 1.0
		if w.P != nil {
			p = *w.P
		}
		f.Words = append(f.Words, Word{Text: w.W, Start: w.S, End: w.E, Prob: p})
	}
	return f, true
}

type wireWord struct {
	W string      `json:"w"`
	S jsonx.Float `json:"s"`
	E jsonx.Float `json:"e"`
	P jsonx.Float `json:"p"`
}

type wireFile struct {
	Size     int64       `json:"size"`
	Model    string      `json:"model"`
	Prompt   *string     `json:"prompt"`
	Duration jsonx.Float `json:"duration"`
	Words    []wireWord  `json:"words"`
}

// Set adds or replaces one file's entry. Times, durations and probabilities are rounded to 3 decimals, like legacy.
func (t *Transcript) Set(name string, f FileTranscript) error {
	w := wireFile{Size: f.Size, Model: f.Model, Prompt: f.Prompt, Duration: jsonx.Float(jsonx.Round3(f.Duration)),
		Words: make([]wireWord, 0, len(f.Words))}
	for _, x := range f.Words {
		w.Words = append(w.Words, wireWord{W: x.Text, S: jsonx.Float(jsonx.Round3(x.Start)),
			E: jsonx.Float(jsonx.Round3(x.End)), P: jsonx.Float(jsonx.Round3(x.Prob))})
	}
	return t.files.SetValue(name, w)
}

// Marshal encodes the transcript compactly; Write indents it like Python's json.dumps(indent=1).
func (t *Transcript) Marshal() ([]byte, error) {
	b, err := t.files.MarshalJSON()
	if err != nil {
		return nil, err
	}
	t.doc.Set("files", b)
	return t.doc.MarshalJSON()
}

// Write writes the transcript to path atomically.
func (t *Transcript) Write(path string) error {
	b, err := t.Marshal()
	if err != nil {
		return err
	}
	return store.WriteJSON(path, b)
}
