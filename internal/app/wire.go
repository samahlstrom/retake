// Package app connects the engines to the parts that run them. The notes engine (internal/notes) and the
// transcriber (internal/transcribe) plug into the jobs, `retake notes build`, `retake open` and `retake mcp`
// through Engines, and nowhere else.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/notes"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/transcribe"
)

// ErrNoTranscriber is the transcribe jobs' error while no transcriber is connected in Engines.
var ErrNoTranscriber = errors.New("this retake build has no transcriber")

// Engines returns the notes engine and the transcriber, configured by cfg. The two adapters below are the
// connection points: notesEngine.Build calls the notes engine for one version and converts its result into a
// project.Run (one project.Comment per comment, keyed by take, segment and index), and transcriber returns the
// configured transcriber wrapped as a jobs.TakeTranscriber.
func Engines(cfg config.Config) jobs.Engines {
	return jobs.Engines{
		Notes:       notesEngine{},
		Transcriber: func() (jobs.TakeTranscriber, error) { return transcriber(cfg) },
	}
}

// notesEngine runs the notes engine for one version.
type notesEngine struct{}

func (notesEngine) Build(ctx context.Context, v family.Version, fam []family.Version) (project.Run, error) {
	res, err := notes.Build(ctx, v, fam, notes.Options{})
	if err != nil {
		return project.Run{}, err
	}
	run := runFromResult(v.Label, res)
	attachTargets(&run, res, manifests(v, fam))
	return run, nil
}

// runFromResult converts a notes engine result into the store's run: one project.Comment per comment, with its
// strokes (bbox [x0, y0, x1, y1] becomes box [x, y, w, h]) and the snapshot of its moment.
func runFromResult(label string, res *notes.Result) project.Run {
	snaps := map[*notes.Comment]string{}
	if res.Doc != nil {
		for _, m := range res.Doc.Moments {
			if m.Snapshot == nil {
				continue
			}
			p := filepath.Join(res.Dir, filepath.FromSlash(*m.Snapshot))
			for _, c := range m.Comments {
				snaps[c] = p
			}
		}
	}
	run := project.Run{Version: label}
	for _, c := range res.Comments {
		pc := project.Comment{
			Key: c.Key, Take: c.TakeFolder(), Seg: c.SegID(),
			T0: float64(c.VideoT), T1: float64(c.VideoTEnd),
			Text: c.Text, Author: c.Author, Retracted: c.Retracted,
		}
		if c.Version != nil {
			pc.Version = *c.Version
		}
		if len(c.AnswerTo) > 0 {
			var id int64
			if json.Unmarshal(c.AnswerTo, &id) == nil {
				pc.AnswerTo = id
			}
		}
		for _, st := range c.Strokes {
			x0, y0, x1, y1 := float64(st.BBox[0]), float64(st.BBox[1]), float64(st.BBox[2]), float64(st.BBox[3])
			pc.Detail.Strokes = append(pc.Detail.Strokes, project.Stroke{
				Shape: st.Shape, Color: st.Color, Box: [4]float64{x0, y0, x1 - x0, y1 - y0},
			})
		}
		pc.Detail.Snapshot = snaps[c]
		run.Comments = append(run.Comments, pc)
	}
	return run
}

// transcriber returns the transcriber cfg selects, or why there is none. whisper.cpp not being installed is fatal
// (retrying can't fix it) and carries the install hint the sidebar shows.
func transcriber(cfg config.Config) (jobs.TakeTranscriber, error) {
	t, err := transcribe.FromConfig(cfg)
	if errors.Is(err, transcribe.ErrNoWhisper) {
		return nil, jobs.Fatal(jobs.Hinted(err, "Run scripts/install-whisper.sh, or set [transcribe] command in config.toml."))
	}
	if err != nil {
		return nil, err
	}
	return takeTranscriber{t}, nil
}

// takeTranscriber transcribes every audio file of a take that transcript.json doesn't have yet.
type takeTranscriber struct{ t transcribe.Transcriber }

func (a takeTranscriber) Take(ctx context.Context, takeDir string) error {
	return transcribe.Take(ctx, takeDir, a.t, transcribe.TakeOptions{})
}
