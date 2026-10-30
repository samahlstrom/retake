package family

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// A streamed render (spec/bridge.md, "Streaming") is an HLS EVENT playlist that grows while its push renders it.
// It is in the family as soon as it lists a segment, with rendering set, as long as the push is alive.

// Progress is how far a streamed render is: units published of all, and the seconds playable so far.
type Progress struct {
	Done    int         `json:"done"`
	Total   int         `json:"total"`
	Seconds jsonx.Float `json:"seconds"`
}

// Playlist is what Retake reads from an HLS media playlist.
type Playlist struct {
	Segments int
	Seconds  float64 // the sum of the EXTINF durations
	Ended    bool    // #EXT-X-ENDLIST
}

// ReadPlaylist reads an HLS media playlist. A missing or unreadable file gives an error.
func ReadPlaylist(path string) (Playlist, error) {
	f, err := os.Open(path) //nolint:gosec // a playlist of the family
	if err != nil {
		return Playlist{}, err
	}
	defer func() { _ = f.Close() }()
	var pl Playlist
	sc := bufio.NewScanner(f)
	pending := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			d := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(d, ','); i >= 0 {
				d = d[:i]
			}
			if v, err := strconv.ParseFloat(strings.TrimSpace(d), 64); err == nil {
				pl.Seconds += v
			}
			pending = true
		case line == "#EXT-X-ENDLIST":
			pl.Ended = true
		case line == "" || strings.HasPrefix(line, "#"):
		case pending:
			pl.Segments++
			pending = false
		}
	}
	return pl, sc.Err()
}

// RenderingState is the "rendering" object of a streamed push's record (<stem>.push.json): the expected
// duration, frame rate and size from the render plan, and the units published so far.
type RenderingState struct {
	Playlist string  `json:"playlist,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	FPS      float64 `json:"fps,omitempty"`
	Size     []int   `json:"size,omitempty"`
	Done     int     `json:"done"`
	Total    int     `json:"total"`
	Started  string  `json:"started,omitempty"`
}

// renderingOf reads the rendering state from a playlist's push record; nil when it has none (or no record).
func renderingOf(playlist string) *RenderingState {
	b, err := os.ReadFile(sidecar(playlist, ".push.json"))
	if err != nil {
		return nil
	}
	var rec struct {
		Rendering *RenderingState `json:"rendering"`
	}
	if json.Unmarshal(b, &rec) != nil {
		return nil
	}
	return rec.Rendering
}

func sidecar(video, suffix string) string {
	return filepath.Join(filepath.Dir(video), Stem(filepath.Base(video))+suffix)
}

// RenderLockPath is the lock a streamed push holds on its playlist while it renders: .<stem>.lock beside it.
func RenderLockPath(playlist string) string {
	return filepath.Join(filepath.Dir(playlist), "."+Stem(filepath.Base(playlist))+".lock")
}

// streaming reports whether path is a streamed render in progress that can be listed: a playlist without
// #EXT-X-ENDLIST that has a segment, a push record with rendering, and a live push holding its lock. It returns the
// version's expected info and progress.
func streaming(path string) (info streamInfo, ok bool) {
	if !IsPlaylist(path) {
		return info, false
	}
	pl, err := ReadPlaylist(path)
	if err != nil || pl.Ended || pl.Segments == 0 {
		return info, false
	}
	st := renderingOf(path)
	if st == nil || !RenderLive(path) {
		return info, false
	}
	info.duration = st.Duration
	if info.duration <= 0 {
		info.duration = pl.Seconds
	}
	info.fps = st.FPS
	if info.fps <= 0 {
		info.fps = 30
	}
	if len(st.Size) == 2 {
		w, h := st.Size[0], st.Size[1]
		info.width, info.height = &w, &h
	}
	info.progress = Progress{Done: st.Done, Total: st.Total, Seconds: jsonx.Float(round3(pl.Seconds))}
	return info, true
}

type streamInfo struct {
	duration      float64
	fps           float64
	width, height *int
	progress      Progress
}

func round3(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 3, 64), 64)
	return v
}
