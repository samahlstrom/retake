package family

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/jsonx"
)

// A streamed version (spec/bridge.md, "Streaming") is an HLS EVENT playlist that renders on demand: pending until
// someone watches it, rendering while someone does, complete once it has #EXT-X-ENDLIST. Its push record says how to
// render it and what to expect; its playlist and render lock say how far it is.

// Stream states.
const (
	StatePending   = "pending"
	StateRendering = "rendering"
	StateComplete  = "complete"
)

// StreamState is a streamed version's state for the page: units published of all, and the seconds playable.
type StreamState struct {
	State   string      `json:"state"`
	Done    int         `json:"done"`
	Total   int         `json:"total"`
	Seconds jsonx.Float `json:"seconds"`
}

// StreamRecord is the "stream" object of a streamed push's record (<stem>.push.json): how to render the version,
// what its plan expects, and how its renders went.
type StreamRecord struct {
	State    string   `json:"state"`
	Playlist string   `json:"playlist"`
	Poster   string   `json:"poster,omitempty"`
	Doc      string   `json:"doc"`              // the timeline snapshot it renders, beside the playlist
	Export   string   `json:"export,omitempty"` // the MP4 the completing render also packages, beside the playlist
	Duration float64  `json:"duration"`
	FPS      float64  `json:"fps"`
	Size     []int    `json:"size,omitempty"`
	Done     int      `json:"done"`
	Total    int      `json:"total"`
	Seconds  float64  `json:"seconds"`
	Renders  int      `json:"renders"`
	FirstS   *float64 `json:"first_s"`
	DoneS    *float64 `json:"done_s"`
	Error    string   `json:"error,omitempty"`
}

// Playlist is what Retake reads from an HLS media playlist.
type Playlist struct {
	Segments int
	Seconds  float64 // the sum of the EXTINF durations
	Units    int     // the last "# unit N" mark of a streamed render: units published
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
		case strings.HasPrefix(line, "# unit "):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "# unit "))); err == nil {
				pl.Units = n
			}
		case line == "" || strings.HasPrefix(line, "#"):
		case pending:
			pl.Segments++
			pending = false
		}
	}
	return pl, sc.Err()
}

// ReadStreamRecord reads the stream object of a playlist's push record; nil when it has none (or no record).
func ReadStreamRecord(playlist string) *StreamRecord {
	b, err := os.ReadFile(Sidecar(playlist, ".push.json"))
	if err != nil {
		return nil
	}
	var rec struct {
		Stream *StreamRecord `json:"stream"`
	}
	if json.Unmarshal(b, &rec) != nil {
		return nil
	}
	return rec.Stream
}

// Sidecar is a file beside a version named after it: X.m3u8 -> X<suffix>.
func Sidecar(video, suffix string) string {
	return filepath.Join(filepath.Dir(video), Stem(filepath.Base(video))+suffix)
}

// RenderLockPath is the lock a render holds on its playlist: .<stem>.lock beside it.
func RenderLockPath(playlist string) string {
	return filepath.Join(filepath.Dir(playlist), "."+Stem(filepath.Base(playlist))+".lock")
}

// State is a streamed version's state: complete when its playlist ended, rendering while a render holds its lock,
// else pending. Done and Seconds come from the playlist, Total from the record.
func State(playlist string, rec *StreamRecord) StreamState {
	pl, _ := ReadPlaylist(playlist)
	st := StreamState{State: StatePending, Done: pl.Units, Total: rec.Total, Seconds: jsonx.Float(round3(pl.Seconds))}
	switch {
	case pl.Ended:
		st.State, st.Done = StateComplete, rec.Total
	case RenderLive(playlist):
		st.State = StateRendering
	}
	return st
}

// streamed reports whether path is a streamed version, with what its record expects and its state. It's listed in
// every state and never probed.
func streamed(path, label string) (Version, bool) {
	if !IsPlaylist(path) {
		return Version{}, false
	}
	rec := ReadStreamRecord(path)
	if rec == nil {
		return Version{}, false
	}
	st := State(path, rec)
	d := rec.Duration
	if d <= 0 {
		d = float64(st.Seconds)
	}
	fps := rec.FPS
	if fps <= 0 {
		fps = 30
	}
	v := Version{Duration: jsonx.FloatPtr(&d), FPS: jsonx.Float(fps), Stream: &st}
	if len(rec.Size) == 2 {
		w, h := rec.Size[0], rec.Size[1]
		v.Width, v.Height = &w, &h
	}
	if rec.Poster != "" {
		v.Poster = "video/" + url.PathEscape(label) + "/" + url.PathEscape(rec.Poster)
	}
	return v, true
}

func round3(x float64) float64 {
	v, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 3, 64), 64)
	return v
}
