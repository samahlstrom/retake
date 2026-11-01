package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // decodes ffmpeg's frame
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/still"
)

// StillWidth is the widest a still the worker makes of a note gets, in pixels.
const StillWidth = 1280

// noteFiles saves each assigned note's still and attached images into the task folder for its agent
// (spec/automation.md, "A note's stills and images"). Nothing here stops the task: a still the server can't give is
// made here from the version's video with the note's pen lines drawn on it, and whatever is still missing is said in
// the note's context (Missing).
func (r *runner) noteFiles(ctx context.Context, a *project.WorkAssignment, dir string) {
	for i := range a.Notes {
		n := &a.Notes[i]
		if a.Task == nil || !slices.Contains(a.Task.Notes, n.ID) {
			continue
		}
		if n.Detail.Snapshot != "" || len(n.Detail.Strokes) > 0 {
			name := fmt.Sprintf("note-%d.png", n.ID)
			why, fetched := "the server has no saved still of it", false
			if n.Detail.Snapshot != "" {
				err := r.fetchArtifact(ctx, fmt.Sprintf("/api/worker/notes/%d/snapshot", n.ID), filepath.Join(dir, name))
				if err == nil {
					fetched, n.Detail.Snapshot = true, name
				} else {
					why = "its saved still couldn't be fetched (" + err.Error() + ")"
				}
			}
			if !fetched {
				at, err := r.noteStill(ctx, n, filepath.Join(dir, name))
				if err == nil {
					n.Detail.Snapshot = name
					n.Missing = append(n.Missing, fmt.Sprintf("%s: %s is a still this computer made of %s at %.2f s, "+
						"with the note's pen lines drawn on it.", upper(why), name, n.Version, at))
				} else {
					n.Detail.Snapshot = ""
					_ = os.Remove(filepath.Join(dir, name)) // an earlier attempt's, if any: not this note's still now
					n.Missing = append(n.Missing, fmt.Sprintf("No still of this note: %s, and none could be made here "+
						"(%v). Look at %s at %.2f s yourself.", why, err, n.Version, n.T0))
				}
				r.log("note #%d: %s", n.ID, n.Missing[len(n.Missing)-1])
			}
		}
		for _, im := range n.Images {
			if !project.ValidImageName(im.File) {
				n.Missing = append(n.Missing, "An attached image has an invalid file name and was left out.")
				continue
			}
			name := fmt.Sprintf("note-%d-%s", n.ID, im.File)
			if err := r.fetchArtifact(ctx, fmt.Sprintf("/api/worker/notes/%d/images/%s", n.ID, im.File), filepath.Join(dir, name)); err != nil {
				n.Missing = append(n.Missing, fmt.Sprintf("The attached image %s couldn't be fetched (%v): only its box "+
					"and time are here.", im.File, err))
				r.log("note #%d: %s", n.ID, n.Missing[len(n.Missing)-1])
			}
		}
	}
}

func upper(s string) string {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return s
	}
	return string(s[0]-'a'+'A') + s[1:]
}

// noteStill makes the still of a note as a PNG at out: the frame of its version at its moment, from this computer's
// file of that version, with its pen lines drawn and numbered as the notes engine draws them. The moment is the
// middle one of its lines' times, else the note's start. It returns that moment.
func (r *runner) noteStill(ctx context.Context, n *project.WorkNote, out string) (float64, error) {
	at := stillTime(n.Note, n.Detail.Strokes)
	if r.opts.NoStill {
		return at, errors.New("stills are turned off on this computer")
	}
	path := r.videoOf(n.Version)
	if path == "" {
		return at, fmt.Errorf("this computer has no file of %s", n.Version)
	}
	frame, err := ffmpegFrame(ctx, path, at, StillWidth, 16<<20)
	if err != nil {
		return at, fmt.Errorf("no frame of %s at %.2f s: %w", n.Version, at, err)
	}
	src, _, err := image.Decode(bytes.NewReader(frame))
	if err != nil {
		return at, err
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	if err := still.Draw(img, linesOf(n.Detail.Strokes)); err != nil {
		return at, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return at, err
	}
	return at, os.WriteFile(out, buf.Bytes(), 0o600)
}

// stillTime is the moment a note's still shows: the middle one of its lines' times (strokes stored with their line),
// else its start.
func stillTime(n project.Note, strokes []project.Stroke) float64 {
	var ts []float64
	for _, s := range strokes {
		if len(s.Points) > 0 {
			ts = append(ts, s.T)
		}
	}
	if len(ts) == 0 {
		return n.T0
	}
	sort.Float64s(ts)
	return ts[(len(ts)-1)/2]
}

// linesOf is the pen lines of a note's strokes, numbered from 1. A stroke stored without its line (an older note) is
// drawn as an outline of its box: an ellipse for a circle, else a rectangle.
func linesOf(strokes []project.Stroke) []still.Line {
	out := make([]still.Line, 0, len(strokes))
	for i, s := range strokes {
		pts := s.Points
		if len(pts) == 0 {
			pts = outline(s.Shape, s.Box)
		}
		out = append(out, still.Line{Points: pts, Color: s.Color, N: i + 1})
	}
	return out
}

func outline(shape string, box [4]float64) [][2]float64 {
	x, y, w, h := box[0], box[1], box[2], box[3]
	if shape == "circle" {
		const steps = 48
		pts := make([][2]float64, 0, steps+1)
		for i := 0; i <= steps; i++ {
			a := 2 * math.Pi * float64(i) / steps
			pts = append(pts, [2]float64{x + w/2 + w/2*math.Cos(a), y + h/2 + h/2*math.Sin(a)})
		}
		return pts
	}
	return [][2]float64{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}, {x, y}}
}
