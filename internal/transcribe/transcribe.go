// Package transcribe turns recorded review audio into timed words (spec/notes.md, "Go interfaces").
package transcribe

import "context"

// Word is one transcribed word, with times in seconds from the start of its audio file.
type Word struct {
	Text  string  // the word as the model wrote it, including its leading space
	Start float64 // seconds
	End   float64 // seconds
	Prob  float64 // 0..1
}

// Options tune one transcription.
type Options struct {
	Prompt   string // rare words that should come out right (e.g. a VO script's names)
	Language string // "" = detect
}

// Transcriber transcribes one audio file.
type Transcriber interface {
	// Transcribe returns the words in the audio file, with times in seconds from the file's start.
	Transcribe(ctx context.Context, audioPath string, opts Options) ([]Word, error)
	// Name is stored as "model" in transcript.json, e.g. "whisper.cpp large-v3 (cuda)".
	Name() string
}
