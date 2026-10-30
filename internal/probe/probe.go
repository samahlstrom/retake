// Package probe reads a video's duration, frame rate and size with ffprobe, cached per (path, mtime, size).
package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// DefaultFPS is used when ffprobe reports no usable frame rate.
const DefaultFPS = 30.0

// Timeout bounds one ffprobe run.
const Timeout = 30 * time.Second

// Info is what the player needs to know about a video. Duration is nil while the file isn't a finished video yet
// (still rendering, broken, or not a video at all).
type Info struct {
	Duration *float64
	FPS      float64
	Width    *int
	Height   *int
}

// HasDuration reports whether the file probed with a non-zero duration (Python truthiness, as in the legacy server).
func (i Info) HasDuration() bool {
	return i.Duration != nil && *i.Duration != 0
}

// Runner runs ffprobe on path and returns its JSON output. A non-nil error with output still gets parsed.
type Runner func(ctx context.Context, path string) ([]byte, error)

// Prober probes files and caches the results. The zero value is not usable; call New.
type Prober struct {
	run Runner
	log io.Writer

	mu    sync.Mutex
	cache map[key]Info
}

type key struct {
	path  string
	mtime int64
	size  int64
}

// New returns a Prober that runs the ffprobe binary (looked up on PATH). Failures are logged to log, if not nil.
func New(log io.Writer) *Prober {
	return NewWithRunner(FFprobe("ffprobe"), log)
}

// NewWithRunner returns a Prober using run instead of ffprobe (tests).
func NewWithRunner(run Runner, log io.Writer) *Prober {
	return &Prober{run: run, log: log, cache: map[key]Info{}}
}

// Available reports whether the ffprobe binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// FFprobe returns a Runner that executes the given ffprobe binary.
func FFprobe(bin string) Runner {
	return func(ctx context.Context, path string) ([]byte, error) {
		// The arguments are passed as an argv (no shell); path is a local file the user pointed us at.
		cmd := exec.CommandContext(ctx, bin, "-v", "error", "-select_streams", "v:0", "-show_entries", //nolint:gosec // fixed binary, argv only
			"stream=width,height,avg_frame_rate,r_frame_rate:format=duration", "-of", "json", path)
		return cmd.Output()
	}
}

// Probe returns the file's Info. A file that can't be stat'ed gets the empty Info and isn't cached.
func (p *Prober) Probe(path string) Info {
	empty := Info{FPS: DefaultFPS}
	st, err := os.Stat(path)
	if err != nil {
		return empty
	}
	k := key{path: path, mtime: st.ModTime().UnixNano(), size: st.Size()}
	p.mu.Lock()
	info, ok := p.cache[k]
	p.mu.Unlock()
	if ok {
		return info
	}
	info = p.probe(path)
	p.mu.Lock()
	p.cache[k] = info
	p.mu.Unlock()
	return info
}

func (p *Prober) probe(path string) Info {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	out, runErr := p.run(ctx, path)
	info, err := Parse(out)
	switch {
	case ctx.Err() != nil:
		info = Info{FPS: DefaultFPS}
		p.logf("retake: ffprobe failed on %s (timed out after %s)\n", filepath.Base(path), Timeout)
	case err != nil:
		p.logf("retake: ffprobe failed on %s (%v)\n", filepath.Base(path), err)
	case runErr != nil && !isExit(runErr):
		p.logf("retake: ffprobe failed on %s (%v)\n", filepath.Base(path), runErr)
	}
	return info
}

// isExit reports whether err is ffprobe exiting non-zero (a file it can't read yet), as opposed to not running.
func isExit(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

func (p *Prober) logf(format string, args ...any) {
	if p.log != nil {
		_, _ = fmt.Fprintf(p.log, format, args...)
	}
}

type ffOutput struct {
	Streams []struct {
		Width        *int   `json:"width"`
		Height       *int   `json:"height"`
		AvgFrameRate string `json:"avg_frame_rate"`
		RFrameRate   string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Parse reads ffprobe's JSON output the way the legacy server did: width and height of the first video stream,
// fps from avg_frame_rate then r_frame_rate (rounded to 3 places, default 30), duration from the container
// (rounded to 3 places). It returns what it could read along with any error.
func Parse(out []byte) (Info, error) {
	info := Info{FPS: DefaultFPS}
	if len(strings.TrimSpace(string(out))) == 0 {
		out = []byte("{}")
	}
	var d ffOutput
	if err := json.Unmarshal(out, &d); err != nil {
		return info, err
	}
	if len(d.Streams) > 0 {
		st := d.Streams[0]
		info.Width, info.Height = st.Width, st.Height
		for _, r := range []string{st.AvgFrameRate, st.RFrameRate} {
			fps, ok, err := parseRate(r)
			if err != nil {
				return info, err
			}
			if ok {
				info.FPS = fps
				break
			}
		}
	}
	if d.Format.Duration != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(d.Format.Duration), 64)
		if err != nil {
			return info, fmt.Errorf("duration %q: %w", d.Format.Duration, err)
		}
		f = jsonx.Round3(f)
		info.Duration = &f
	}
	return info, nil
}

// parseRate parses "num/den". ok is false when the rate is missing or zero ("0/0"), which isn't an error.
func parseRate(s string) (float64, bool, error) {
	if s == "" {
		s = "0/0"
	}
	numS, denS, found := strings.Cut(s, "/")
	if !found || denS == "" {
		return 0, false, nil
	}
	den, err := strconv.ParseFloat(strings.TrimSpace(denS), 64)
	if err != nil {
		return 0, false, fmt.Errorf("frame rate %q: %w", s, err)
	}
	if den == 0 {
		return 0, false, nil
	}
	num, err := strconv.ParseFloat(strings.TrimSpace(numS), 64)
	if err != nil {
		return 0, false, fmt.Errorf("frame rate %q: %w", s, err)
	}
	if num == 0 {
		return 0, false, nil
	}
	return jsonx.Round3(num / den), true, nil
}
