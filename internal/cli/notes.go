package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/notes"
)

// notesCmd dispatches `retake notes <subcommand>`.
func notesCmd(ctx context.Context, args []string, env Env) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	switch args[0] {
	case "build":
		return notesBuildCmd(ctx, args[1:], env)
	}
	_, _ = fmt.Fprintf(env.Stderr, "retake notes: unknown subcommand %q\n\n%s", args[0], usage)
	return ExitUsage
}

type notesBuildArgs struct {
	target          string
	window, maxSpan float64
	videoTranscript string
	asJSON, help    bool
}

func parseNotesBuild(args []string) (notesBuildArgs, error) {
	a := notesBuildArgs{window: notes.DefaultWindow, maxSpan: notes.DefaultMaxSpan}
	var pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !isFlag(arg) {
			pos = append(pos, arg)
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
		value := func() (string, error) {
			if hasVal {
				return val, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		seconds := func() (float64, error) {
			v, err := value()
			if err != nil {
				return 0, err
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 {
				return 0, fmt.Errorf("%s: %q is not a positive number of seconds", name, v)
			}
			return f, nil
		}
		var err error
		switch name {
		case "-h", "--help":
			a.help = true
		case "--json":
			a.asJSON = true
		case "--window":
			a.window, err = seconds()
		case "--max-span":
			a.maxSpan, err = seconds()
		case "--video-transcript":
			a.videoTranscript, err = value()
		default:
			err = fmt.Errorf("unknown option %s", name)
		}
		if err != nil {
			return a, err
		}
	}
	if a.help {
		return a, nil
	}
	switch len(pos) {
	case 0:
		return a, errors.New("missing the video, its .review folder or a take folder")
	case 1:
		a.target = pos[0]
	default:
		return a, fmt.Errorf("unexpected argument %q", pos[1])
	}
	return a, nil
}

// notesBuildSummary is the --json output of `retake notes build`.
type notesBuildSummary struct {
	NotesMD       string                `json:"notes_md"`
	NotesJSON     string                `json:"notes_json"`
	Version       string                `json:"version"`
	Moments       int                   `json:"moments"`
	Comments      int                   `json:"comments"`
	Segments      int                   `json:"segments"`
	Flags         int                   `json:"flags"`
	Takes         int                   `json:"takes"`
	Untranscribed []notes.Untranscribed `json:"untranscribed"`
}

func notesBuildCmd(ctx context.Context, args []string, env Env) int {
	a, err := parseNotesBuild(args)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake notes build: %v\n\n%s", err, usage)
		return ExitUsage
	}
	if a.help {
		_, _ = io.WriteString(env.Stdout, usage)
		return ExitOK
	}
	target := absPath(a.target, env.Getenv)
	if !isFile(target) && !isDir(target) {
		_, _ = fmt.Fprintf(env.Stderr, "retake: no such file: %s\n", target)
		return ExitUsage
	}
	opts := notes.Options{Window: a.window, MaxSpan: a.maxSpan, Log: env.Stderr, Prober: env.Prober}
	if a.videoTranscript != "" {
		opts.VideoTranscript = absPath(a.videoTranscript, env.Getenv)
	}
	res, err := notes.Run(ctx, target, opts)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		if errors.Is(err, notes.ErrNoTakes) {
			return ExitUsage
		}
		return ExitError
	}
	for _, u := range res.Untranscribed {
		_, _ = fmt.Fprintf(env.Stderr, "retake: take %s %s: %s has no transcript yet; its words are missing\n", u.Take,
			u.Seg, u.File)
	}
	if a.asJSON {
		s := notesBuildSummary{NotesMD: res.MDPath, NotesJSON: res.JSONPath, Version: res.Doc.Version,
			Moments: len(res.Doc.Moments), Comments: len(res.Comments), Segments: len(res.Doc.Segments),
			Flags: len(res.Doc.Flags), Takes: len(res.Doc.Takes), Untranscribed: res.Untranscribed}
		if s.Untranscribed == nil {
			s.Untranscribed = []notes.Untranscribed{}
		}
		b, err := jsonx.Marshal(s)
		if err == nil {
			b, err = indent(b)
		}
		if err != nil {
			_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
			return ExitError
		}
		_, _ = fmt.Fprintf(env.Stdout, "%s\n", b)
		return ExitOK
	}
	_, _ = fmt.Fprintln(env.Stdout, res.Summary())
	return ExitOK
}
