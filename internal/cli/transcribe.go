package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/transcribe"
)

const transcribeUsage = `usage: retake transcribe <take folder | audio file> [--model M] [--device auto|cuda|cpu] [--engine E]
                         [--command CMD] [--whisper-cli PATH] [--prompt TEXT] [--language L] [--force] [--json]

A debugging aid. On a take folder it does what the transcribe job does: it transcribes the audio that transcript.json
doesn't have yet (all of it with --force) and writes transcript.json. On an audio file it prints the words. The flags
override the [transcribe] config; RETAKE_WHISPER_THREADS sets whisper.cpp's CPU threads.
`

type transcribeArgs struct {
	path  string
	set   config.Transcribe // flags over the config file's [transcribe]
	opts  transcribe.Options
	force bool
	json  bool
	help  bool
}

func parseTranscribe(args []string) (transcribeArgs, error) {
	var a transcribeArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !isFlag(arg) {
			if a.path != "" {
				return a, fmt.Errorf("unexpected argument %q", arg)
			}
			a.path = arg
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
		value := func() (*string, error) {
			if !hasVal {
				if i+1 >= len(args) {
					return nil, fmt.Errorf("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			v := val
			return &v, nil
		}
		var err error
		switch name {
		case "-h", "--help":
			a.help = true
		case "--force":
			a.force = true
		case "--json":
			a.json = true
		case "--engine":
			a.set.Engine, err = value()
		case "--model":
			a.set.Model, err = value()
		case "--device":
			a.set.Device, err = value()
		case "--command":
			a.set.Command, err = value()
		case "--whisper-cli":
			a.set.WhisperCLI, err = value()
		case "--prompt", "--language":
			var v *string
			if v, err = value(); err == nil {
				if name == "--prompt" {
					a.opts.Prompt = *v
				} else {
					a.opts.Language = *v
				}
			}
		default:
			return a, fmt.Errorf("unknown option %s", name)
		}
		if err != nil {
			return a, err
		}
	}
	if a.path == "" && !a.help {
		return a, fmt.Errorf("missing the take folder or audio file")
	}
	return a, nil
}

// transcribeCmd is a debugging aid: it runs the configured transcriber on one take (writing its transcript.json, as
// the transcribe job does) or on one audio file (printing the words).
func transcribeCmd(ctx context.Context, args []string, env Env) int {
	a, err := parseTranscribe(args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n\n%s", err, transcribeUsage)
		return ExitUsage
	}
	if a.help {
		_, _ = io.WriteString(env.Stdout, transcribeUsage)
		return ExitOK
	}
	set := loadConfig(env).Transcribe
	for _, kv := range []struct{ flag, conf **string }{{&a.set.Engine, &set.Engine}, {&a.set.Model, &set.Model},
		{&a.set.Device, &set.Device}, {&a.set.Command, &set.Command}, {&a.set.WhisperCLI, &set.WhisperCLI}} {
		if *kv.flag != nil {
			*kv.conf = *kv.flag
		}
	}
	t, err := transcribe.FromSettings(set, transcribe.Env{Getenv: env.Getenv, Log: env.Stderr})
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	path := absPath(a.path, env.Getenv)
	if isDir(path) {
		return transcribeTake(ctx, path, t, a, env)
	}
	if !isFile(path) {
		_, _ = fmt.Fprintf(env.Stderr, "retake: no such file: %s\n", path)
		return ExitUsage
	}
	return transcribeFile(ctx, path, t, a, env)
}

func transcribeTake(ctx context.Context, dir string, t transcribe.Transcriber, a transcribeArgs, env Env) int {
	err := transcribe.Take(ctx, dir, t, transcribe.TakeOptions{Options: a.opts, Force: a.force, Log: env.Stderr})
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	path := filepath.Join(dir, transcribe.TranscriptFile)
	if a.json {
		b, err := os.ReadFile(path) //nolint:gosec // the transcript we just wrote
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
			return ExitError
		}
		_, _ = env.Stdout.Write(b)
		_, _ = fmt.Fprintln(env.Stdout)
		return ExitOK
	}
	tr, err := transcribe.ReadTranscript(path)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	for _, name := range tr.Names() {
		f, _ := tr.File(name)
		_, _ = fmt.Fprintf(env.Stdout, "%s  %.1f s  %s  %s\n", name, f.Duration, f.Model, wordsText(f.Words))
	}
	return ExitOK
}

func transcribeFile(ctx context.Context, path string, t transcribe.Transcriber, a transcribeArgs, env Env) int {
	tmp, err := os.MkdirTemp("", "retake-transcribe-")
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	wav := filepath.Join(tmp, "audio.wav")
	dur, err := transcribe.DecodeWAV(ctx, path, wav)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	began := time.Now()
	words, err := t.Transcribe(ctx, wav, a.opts)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
		return ExitError
	}
	took := time.Since(began).Seconds()
	_, _ = fmt.Fprintf(env.Stderr, "transcribed %.1f s of audio in %.2f s (%.1fx real time) with %s\n", dur, took,
		dur/max(took, 1e-9), t.Name())
	if a.json {
		type word struct {
			W string      `json:"w"`
			S jsonx.Float `json:"s"`
			E jsonx.Float `json:"e"`
			P jsonx.Float `json:"p"`
		}
		out := struct {
			Model    string      `json:"model"`
			Duration jsonx.Float `json:"duration"`
			Seconds  jsonx.Float `json:"seconds"`
			Words    []word      `json:"words"`
		}{t.Name(), jsonx.Float(jsonx.Round3(dur)), jsonx.Float(jsonx.Round3(took)), []word{}}
		for _, w := range words {
			out.Words = append(out.Words, word{w.Text, jsonx.Float(jsonx.Round3(w.Start)), jsonx.Float(jsonx.Round3(w.End)),
				jsonx.Float(jsonx.Round3(w.Prob))})
		}
		b, err := jsonx.Marshal(out)
		if err == nil {
			b, err = indent(b)
		}
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake transcribe: %v\n", err)
			return ExitError
		}
		_, _ = fmt.Fprintf(env.Stdout, "%s\n", b)
		return ExitOK
	}
	for _, w := range words {
		_, _ = fmt.Fprintf(env.Stdout, "%7.2f %7.2f  %.2f %s\n", w.Start, w.End, w.Prob, w.Text)
	}
	return ExitOK
}

func wordsText(words []transcribe.Word) string {
	var b strings.Builder
	for _, w := range words {
		b.WriteString(w.Text)
	}
	return strings.TrimSpace(b.String())
}
