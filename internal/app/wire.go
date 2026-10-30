// Package app connects the engines to the parts that run them. The notes engine (internal/notes) and the
// transcriber (internal/transcribe) plug into the jobs, `retake notes build`, `retake open` and `retake mcp`
// through Engines, and nowhere else.
package app

import (
	"context"
	"errors"

	"github.com/samahlstrom/retake/internal/config"
	"github.com/samahlstrom/retake/internal/family"
	"github.com/samahlstrom/retake/internal/jobs"
	"github.com/samahlstrom/retake/internal/project"
)

// ErrNoNotesEngine is the notes jobs' error while the notes engine isn't connected in Engines.
var ErrNoNotesEngine = errors.New("this retake build has no notes engine")

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

func (notesEngine) Build(_ context.Context, _ family.Version, _ []family.Version) (project.Run, error) {
	return project.Run{}, jobs.Fatal(ErrNoNotesEngine)
}

// transcriber returns the transcriber cfg selects, or why there is none.
func transcriber(config.Config) (jobs.TakeTranscriber, error) {
	return nil, ErrNoTranscriber
}
