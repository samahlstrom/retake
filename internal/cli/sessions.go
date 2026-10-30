package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/jsonx"
	"github.com/samahlstrom/retake/internal/store"
)

func sessionsCmd(args []string, env Env) int {
	var video string
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case a == "-h" || a == "--help":
			_, _ = io.WriteString(env.Stdout, usage)
			return ExitOK
		case isFlag(a):
			_, _ = fmt.Fprintf(env.Stderr, "retake sessions: unknown option %s\n\n%s", a, usage)
			return ExitUsage
		case video != "":
			_, _ = fmt.Fprintf(env.Stderr, "retake sessions: unexpected argument %q\n\n%s", a, usage)
			return ExitUsage
		default:
			video = a
		}
	}
	if video == "" {
		_, _ = fmt.Fprintf(env.Stderr, "retake sessions: missing the video file\n\n%s", usage)
		return ExitUsage
	}
	video = absPath(video, env.Getenv)
	rd := store.ReviewDir(video)
	// The takes outlive the video: list them as long as either is there.
	if !isFile(video) && !isDir(rd) {
		_, _ = fmt.Fprintf(env.Stderr, "retake: no such file: %s\n", video)
		return ExitUsage
	}
	rows := store.Sessions(video)
	if asJSON {
		b, err := jsonx.Marshal(rows)
		if err == nil {
			var out []byte
			if out, err = indent(b); err == nil {
				_, _ = fmt.Fprintf(env.Stdout, "%s\n", out)
				return ExitOK
			}
		}
		_, _ = fmt.Fprintf(env.Stderr, "retake: %v\n", err)
		return ExitError
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintf(env.Stdout, "no takes in %s\n", rd)
		return ExitOK
	}
	for _, r := range rows {
		var narration float64
		ok := json.Unmarshal(r.Narration, &narration) == nil && jsonx.Kind(r.Narration) == "number"
		notes := "no notes yet"
		if r.Notes {
			notes = "notes.md"
		}
		_, _ = fmt.Fprintf(env.Stdout, "%s  recorded %s  %s  %s  %s\n", filepath.Join(rd, r.ID), fmtDur(narration, ok),
			plural(r.Segments, "segment"), plural(r.Strokes, "stroke"), notes)
	}
	return ExitOK
}

func indent(b []byte) ([]byte, error) {
	var v json.RawMessage = b
	return json.MarshalIndent(v, "", "  ")
}
