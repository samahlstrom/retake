package assets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Kinds.
const (
	KindVideo = "video"
	KindAudio = "audio"
	KindImage = "image"
	KindFont  = "font"
	KindOther = "other"
)

var extKinds = map[string]string{}

func init() {
	for kind, exts := range map[string]string{
		KindVideo: ".mp4 .mov .mkv .webm .avi .m4v .ts .mts .m2ts .flv .wmv .mpg .mpeg .3gp .ogv",
		KindAudio: ".wav .mp3 .m4a .aac .flac .ogg .oga .opus .aif .aiff .wma .caf",
		KindImage: ".png .jpg .jpeg .gif .webp .bmp .tif .tiff .avif .heic .heif",
		KindFont:  ".ttf .otf .ttc .otc .woff .woff2",
	} {
		for _, e := range strings.Fields(exts) {
			extKinds[e] = kind
		}
	}
}

// kindOf guesses a file's kind from its extension: "" when unknown.
func kindOf(path string) string { return extKinds[strings.ToLower(filepath.Ext(path))] }

// AudioStream is one audio stream of a media file.
type AudioStream struct {
	Stream     int    `json:"stream"` // the stream index in the file
	Codec      string `json:"codec,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
	Role       string `json:"role,omitempty"` // from the stream's title or handler name ("mic", "game")
}

// Media is what the probe found: video and audio streams, image size, or a font's names.
type Media struct {
	Duration *float64      `json:"duration,omitempty"`
	Size     *[2]int       `json:"size,omitempty"`
	FPS      *float64      `json:"fps,omitempty"`
	VFR      *bool         `json:"vfr,omitempty"`
	Codec    string        `json:"codec,omitempty"`
	Audio    []AudioStream `json:"audio,omitempty"`
	Family   string        `json:"family,omitempty"` // fonts
	Style    string        `json:"style,omitempty"`
	Glyphs   int           `json:"glyphs,omitempty"`
}

func (m Media) duration() float64 {
	if m.Duration == nil {
		return 0
	}
	return *m.Duration
}

func (m Media) hasVideo() bool { return m.Size != nil && m.Codec != "" }

// tools runs ffmpeg and ffprobe.
type tools struct {
	ffmpeg, ffprobe string
}

func (t tools) available() bool {
	_, e1 := exec.LookPath(t.ffmpeg)
	_, e2 := exec.LookPath(t.ffprobe)
	return e1 == nil && e2 == nil
}

// ffmpegRun runs ffmpeg with args (quiet, no stdin, 2 threads) and returns its stdout. stderr is in the error.
func (t tools) ffmpegRun(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"-nostdin", "-hide_banner", "-v", "error", "-y"}, args...)
	cmd := exec.CommandContext(ctx, t.ffmpeg, full...) //nolint:gosec // a fixed binary, argv only
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, msg)
	}
	return out.Bytes(), nil
}

type ffStream struct {
	Index        int               `json:"index"`
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	AvgFrameRate string            `json:"avg_frame_rate"`
	RFrameRate   string            `json:"r_frame_rate"`
	Channels     int               `json:"channels"`
	SampleRate   string            `json:"sample_rate"`
	Disposition  map[string]int    `json:"disposition"`
	Tags         map[string]string `json:"tags"`
}

type ffProbe struct {
	Streams []ffStream `json:"streams"`
	Format  struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// probe reads a media file's streams with ffprobe.
func (t tools) probe(ctx context.Context, path string) (Media, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, t.ffprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path) //nolint:gosec // argv only
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return Media{}, fmt.Errorf("ffprobe can't read %s: %s", filepath.Base(path), strings.TrimSpace(string(ee.Stderr)))
		}
		return Media{}, fmt.Errorf("ffprobe: %w", err)
	}
	return parseProbe(out)
}

func parseProbe(out []byte) (Media, error) {
	var p ffProbe
	if err := json.Unmarshal(out, &p); err != nil {
		return Media{}, fmt.Errorf("ffprobe output: %w", err)
	}
	var m Media
	if d, err := strconv.ParseFloat(strings.TrimSpace(p.Format.Duration), 64); err == nil && d > 0 {
		d = math.Round(d*1000) / 1000
		m.Duration = &d
	}
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if m.Size != nil || s.Disposition["attached_pic"] == 1 {
				continue // the first real video stream only; cover art isn't video
			}
			if s.Width > 0 && s.Height > 0 {
				m.Size = &[2]int{s.Width, s.Height}
			}
			m.Codec = s.CodecName
			avg, okA := rate(s.AvgFrameRate)
			nom, okN := rate(s.RFrameRate)
			switch {
			case okA:
				m.FPS = &avg
			case okN:
				m.FPS = &nom
			}
			if okA && okN {
				vfr := math.Abs(avg-nom) > 0.01*nom
				m.VFR = &vfr
			}
		case "audio":
			a := AudioStream{Stream: s.Index, Codec: s.CodecName, Channels: s.Channels}
			a.SampleRate, _ = strconv.Atoi(s.SampleRate)
			for _, k := range []string{"title", "handler_name"} {
				if v := strings.TrimSpace(s.Tags[k]); v != "" && !genericHandler(v) {
					a.Role = v
					break
				}
			}
			m.Audio = append(m.Audio, a)
		}
	}
	return m, nil
}

// genericHandler reports a muxer's default handler name, which says nothing about the stream.
func genericHandler(v string) bool {
	switch strings.ToLower(v) {
	case "soundhandler", "sound handler", "videohandler", "core media audio", "isomedia file produced by google":
		return true
	}
	return false
}

func rate(s string) (float64, bool) {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return 0, false
	}
	n, e1 := strconv.ParseFloat(num, 64)
	d, e2 := strconv.ParseFloat(den, 64)
	if e1 != nil || e2 != nil || n <= 0 || d <= 0 {
		return 0, false
	}
	return math.Round(n/d*1000) / 1000, true
}

// refineKind settles a media file's kind from its probe: a container with no video stream is audio, a video
// container holding a single still frame stays video.
func refineKind(kind string, m Media) string {
	if kind == KindVideo && !m.hasVideo() && len(m.Audio) > 0 {
		return KindAudio
	}
	return kind
}
