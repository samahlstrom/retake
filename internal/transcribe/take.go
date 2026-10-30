package transcribe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// TakeOptions tune Take.
type TakeOptions struct {
	Options           // the prompt and language, for every file
	Force   bool      // transcribe every file again, even ones transcript.json already has
	TempDir string    // for the decoded audio; "" = os.TempDir()
	Log     io.Writer // one line per file; nil = quiet
}

var (
	segAudioRE      = regexp.MustCompile(`^seg_\d+\.(webm|ogg|m4a|opus|wav|mp3|flac|aac|mp4)$`)
	narrationRE     = regexp.MustCompile(`^narration\.(webm|ogg|m4a|wav)$`) // format 2: one file for the whole take
	errBadAudioName = errors.New("not an audio file name")
)

// AudioFiles returns the names of a take's audio files, in order: the files segments.json lists, then any other
// seg_* audio in the folder. A take without segments.json (legacy format 2) has narration.<ext> instead. Listed files
// may be missing; only plain names inside the folder are returned.
func AudioFiles(takeDir string) ([]string, error) {
	entries, err := os.ReadDir(takeDir)
	if err != nil {
		return nil, err
	}
	var names []string
	add := func(n string) {
		if checkAudioName(n) == nil && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	segs, err := os.ReadFile(filepath.Join(takeDir, "segments.json")) //nolint:gosec // inside the take
	haveSegs := err == nil
	if haveSegs {
		var list []struct {
			File string `json:"file"`
		}
		if json.Unmarshal(segs, &list) == nil {
			for _, s := range list {
				add(s.File)
			}
		}
	}
	for _, e := range entries { // ReadDir sorts by name
		n := e.Name()
		if e.Type().IsRegular() && (segAudioRE.MatchString(n) || !haveSegs && narrationRE.MatchString(n)) {
			add(n)
		}
	}
	return names, nil
}

func checkAudioName(n string) error {
	if n == "" || n == "." || n == ".." || filepath.Base(n) != n || n == TranscriptFile {
		return errBadAudioName
	}
	return nil
}

// Take transcribes every audio file of a take (AudioFiles) that transcript.json doesn't have yet, and writes
// transcript.json after each one, atomically. A file is skipped when its entry has the same size, the transcriber's
// Name() and the same prompt (FileTranscript.Current); an empty file is skipped until it has audio. A file that
// fails doesn't stop the others: their errors are joined. transcript.json is created even for a take with no audio.
func Take(ctx context.Context, takeDir string, t Transcriber, opts TakeOptions) error {
	names, err := AudioFiles(takeDir)
	if err != nil {
		return err
	}
	path := filepath.Join(takeDir, TranscriptFile)
	tr, err := ReadTranscript(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := tr.Write(path); err != nil {
			return err
		}
	}
	prompt := PromptHash(opts.Prompt)
	take := filepath.Base(takeDir)
	var errs []error
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		st, err := os.Stat(filepath.Join(takeDir, name))
		if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
			continue
		}
		if f, ok := tr.File(name); ok && !opts.Force && f.Current(st.Size(), t.Name(), prompt) {
			logf(opts.Log, "transcribe: %s/%s: already transcribed (%s)\n", take, name, f.Model)
			continue
		}
		began := time.Now()
		words, dur, err := transcribeFile(ctx, filepath.Join(takeDir, name), t, opts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			errs = append(errs, fmt.Errorf("%s/%s: %w", take, name, err))
			continue
		}
		model := t.Name() // after the run: an engine that fell back to another device reports what it used
		if err := tr.Set(name, FileTranscript{Size: st.Size(), Model: model, Prompt: prompt, Duration: dur,
			Words: words}); err != nil {
			return err
		}
		if err := tr.Write(path); err != nil {
			return err
		}
		logf(opts.Log, "transcribe: %s/%s: %.1f s of audio, %d words, %s, in %.1f s\n", take, name, dur, len(words),
			model, time.Since(began).Seconds())
	}
	return errors.Join(errs...)
}

// transcribeFile decodes one file to 16 kHz WAV (which gives its duration) and transcribes that.
func transcribeFile(ctx context.Context, src string, t Transcriber, opts TakeOptions) ([]Word, float64, error) {
	tmp, err := os.MkdirTemp(opts.TempDir, "retake-take-")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	wav, dur, err := prepareWAV(ctx, src, tmp)
	if err != nil {
		return nil, 0, err
	}
	if dur < minAudio {
		return []Word{}, dur, nil
	}
	words, err := t.Transcribe(ctx, wav, opts.Options)
	if words == nil {
		words = []Word{}
	}
	return words, dur, err
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}
