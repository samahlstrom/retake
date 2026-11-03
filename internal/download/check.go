package download

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// What YouTube takes as it is (spec/api.md, "Download").
const (
	// LUFS is the integrated loudness of a download's audio, and Tolerance how far from it a file may be (LU).
	LUFS      = -14.0
	Tolerance = 1.0
	// TruePeak is the highest true peak a download's audio may have (dBTP). Audio this package levels aims at
	// MakeTruePeak, under it, so the AAC encode's overshoot stays below TruePeak.
	TruePeak     = -1.0
	MakeTruePeak = -1.5
	// SampleRate and Channels are the audio's.
	SampleRate = 48000
	Channels   = 2
	// AudioBitrate is what levelled audio is encoded at (YouTube's recommendation for stereo).
	AudioBitrate = "384k"
	// silence is the loudness under which audio isn't levelled (a silent track has none to bring up).
	silence = -70.0
)

// Check is what a file is, as far as a download cares: its container, streams, box order and loudness.
type Check struct {
	Format    string    `json:"format"`    // ffprobe's format_name
	Brand     string    `json:"brand"`     // the MP4's major brand ("qt  " is a QuickTime movie)
	Duration  float64   `json:"duration"`  // seconds
	FastStart bool      `json:"faststart"` // moov before mdat
	Video     *Video    `json:"video,omitempty"`
	Audio     *Audio    `json:"audio,omitempty"`
	Loudness  *Loudness `json:"loudness,omitempty"` // nil when it wasn't measured
	Ext       string    `json:"ext"`
	Problems  []string  `json:"problems,omitempty"` // why it isn't a download as it is
	Plan      *MakePlan `json:"plan,omitempty"`     // what making one does
}

// Video is the first video stream.
type Video struct {
	Codec     string `json:"codec"`
	Profile   string `json:"profile"`
	PixFmt    string `json:"pix_fmt"`
	Primaries string `json:"primaries"`
	Transfer  string `json:"transfer"`
	Matrix    string `json:"matrix"`
	Range     string `json:"range"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	FPS       string `json:"fps"`
}

// Audio is the first audio stream.
type Audio struct {
	Codec      string `json:"codec"`
	Profile    string `json:"profile"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

// Loudness is ffmpeg's loudnorm measure of the audio: integrated (LUFS), true peak (dBTP), loudness range (LU), the
// gate's threshold, and the offset its second pass needs.
type Loudness struct {
	I      float64 `json:"i"`
	TP     float64 `json:"tp"`
	LRA    float64 `json:"lra"`
	Thresh float64 `json:"thresh"`
	Offset float64 `json:"offset"`
}

// MakePlan is what making the download does with each stream.
type MakePlan struct {
	CopyVideo bool `json:"copy_video"` // H.264 High yuv420p: copied
	TagVideo  bool `json:"tag_video"`  // copied, with BT.709 colour tags written in
	CopyAudio bool `json:"copy_audio"` // AAC-LC 48 kHz stereo at the level: copied
	Level     bool `json:"level"`      // levelled with loudnorm (else only encoded)
}

type rawStream struct {
	CodecType     string `json:"codec_type"`
	CodecName     string `json:"codec_name"`
	Profile       string `json:"profile"`
	PixFmt        string `json:"pix_fmt"`
	ColorRange    string `json:"color_range"`
	ColorSpace    string `json:"color_space"`
	ColorTransfer string `json:"color_transfer"`
	ColorPrim     string `json:"color_primaries"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	RFrameRate    string `json:"r_frame_rate"`
	AvgFrameRate  string `json:"avg_frame_rate"`
	SampleRate    string `json:"sample_rate"`
	Channels      int    `json:"channels"`
}

type rawFormat struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	Tags       map[string]string `json:"tags"`
}

// probe reads a file's streams with ffprobe.
func (m *Manager) probe(ctx context.Context, path string) (Check, error) {
	cmd := exec.CommandContext(ctx, m.ffprobe(), "-v", "error", "-print_format", "json", "-show_format", //nolint:gosec // our ffprobe
		"-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return Check{}, fmt.Errorf("ffprobe can't read it: %s", firstLine(ee.Stderr))
		}
		if errors.Is(err, exec.ErrNotFound) {
			return Check{}, errors.New("ffprobe isn't installed")
		}
		return Check{}, fmt.Errorf("ffprobe can't read it: %w", err)
	}
	var raw struct {
		Streams []rawStream `json:"streams"`
		Format  rawFormat   `json:"format"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return Check{}, fmt.Errorf("ffprobe's answer: %w", err)
	}
	c := Check{Format: raw.Format.FormatName, Ext: strings.ToLower(filepath.Ext(path))}
	c.Brand = raw.Format.Tags["major_brand"]
	c.Duration, _ = strconv.ParseFloat(raw.Format.Duration, 64)
	for _, s := range raw.Streams {
		switch {
		case s.CodecType == "video" && c.Video == nil && s.CodecName != "mjpeg" && s.CodecName != "png":
			fps := s.AvgFrameRate
			if fps == "" || fps == "0/0" {
				fps = s.RFrameRate
			}
			c.Video = &Video{Codec: s.CodecName, Profile: s.Profile, PixFmt: s.PixFmt, Primaries: s.ColorPrim,
				Transfer: s.ColorTransfer, Matrix: s.ColorSpace, Range: s.ColorRange, Width: s.Width, Height: s.Height,
				FPS: fps}
		case s.CodecType == "audio" && c.Audio == nil:
			sr, _ := strconv.Atoi(s.SampleRate)
			c.Audio = &Audio{Codec: s.CodecName, Profile: s.Profile, SampleRate: sr, Channels: s.Channels}
		}
	}
	if c.Video == nil {
		return c, errors.New("it has no video")
	}
	return c, nil
}

// fastStart reports whether an MP4's moov box comes before its mdat box (its top-level boxes, read in place).
func fastStart(path string) bool {
	f, err := os.Open(path) //nolint:gosec // a version file, read only
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return false
	}
	var pos int64
	head := make([]byte, 16)
	for pos+8 <= st.Size() {
		if _, err := f.ReadAt(head[:8], pos); err != nil {
			return false
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		kind := string(head[4:8])
		switch size {
		case 1:
			if _, err := f.ReadAt(head[8:16], pos+8); err != nil {
				return false
			}
			size = int64(binary.BigEndian.Uint64(head[8:16])) //nolint:gosec // a box size; checked below
		case 0:
			size = st.Size() - pos
		}
		switch kind {
		case "moov":
			return true
		case "mdat":
			return false
		}
		if size < 8 {
			return false
		}
		pos += size
	}
	return false
}

// measure runs loudnorm's first pass over the first audio stream.
func (m *Manager) measure(ctx context.Context, path string) (*Loudness, error) {
	filter := fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=11:print_format=json", LUFS, MakeTruePeak)
	cmd := exec.CommandContext(ctx, m.ffmpeg(), "-nostdin", "-hide_banner", "-nostats", "-i", path, //nolint:gosec // our ffmpeg
		"-map", "0:a:0", "-af", filter, "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("ffmpeg isn't installed")
		}
		return nil, fmt.Errorf("measuring its loudness: %s", lastLine(stderr.Bytes()))
	}
	return parseLoudnorm(stderr.Bytes())
}

// parseLoudnorm reads the JSON block loudnorm prints at the end of its first pass.
func parseLoudnorm(out []byte) (*Loudness, error) {
	end := bytes.LastIndexByte(out, '}')
	start := -1
	if end >= 0 {
		start = bytes.LastIndexByte(out[:end], '{')
	}
	if start < 0 {
		return nil, errors.New("ffmpeg's loudness measure printed nothing")
	}
	var raw map[string]string
	if err := json.Unmarshal(out[start:end+1], &raw); err != nil {
		return nil, fmt.Errorf("ffmpeg's loudness measure: %w", err)
	}
	num := func(k string) float64 {
		v, err := strconv.ParseFloat(strings.TrimSpace(raw[k]), 64)
		if err != nil {
			return math.Inf(-1) // "-inf": silence
		}
		return v
	}
	return &Loudness{I: num("input_i"), TP: num("input_tp"), LRA: max(num("input_lra"), 0),
		Thresh: num("input_thresh"), Offset: num("target_offset")}, nil
}

// judge fills in Problems and Plan: what keeps the file from being a download as it is, and what making one does.
// keep says the audio keeps its own level (a timeline's master sets one).
func (c *Check) judge(keep bool) {
	c.Problems = nil
	p := &MakePlan{}
	add := func(s string) { c.Problems = append(c.Problems, s) }
	if c.Ext != ".mp4" || !strings.Contains(c.Format, "mp4") || c.Brand == "qt  " {
		add("not an MP4")
	} else if !c.FastStart {
		add("its index is at the end (not faststart)")
	}
	v := c.Video
	switch {
	case v == nil:
		add("no video")
	case v.Codec != "h264" || v.Profile != "High" || v.PixFmt != "yuv420p" || v.Range == "pc":
		add(fmt.Sprintf("video is %s %s %s, not H.264 High yuv420p", v.Codec, v.Profile, v.PixFmt))
	default:
		p.CopyVideo = true
		if !bt709(v) {
			if untagged(v) {
				p.TagVideo = true
				add("video isn't tagged BT.709")
			} else {
				p.CopyVideo = false
				add(fmt.Sprintf("video colours are %s/%s/%s, not BT.709", v.Primaries, v.Transfer, v.Matrix))
			}
		}
	}
	if a := c.Audio; a != nil {
		format := a.Codec == "aac" && a.Profile == "LC" && a.SampleRate == SampleRate && a.Channels == Channels
		if !format {
			add(fmt.Sprintf("audio is %s %s %d Hz %d ch, not AAC-LC 48 kHz stereo", a.Codec, a.Profile, a.SampleRate,
				a.Channels))
		}
		level := true
		if !keep && c.Loudness != nil && c.Loudness.I > silence {
			l := c.Loudness
			if math.Abs(l.I-LUFS) > Tolerance || l.TP > TruePeak {
				level = false
				p.Level = true
				add(fmt.Sprintf("audio is %.1f LUFS, %.1f dBTP, not %g LUFS at most %g dBTP", l.I, l.TP, LUFS, TruePeak))
			}
		}
		p.CopyAudio = format && level
	}
	c.Plan = p
}

func bt709(v *Video) bool {
	return v.Primaries == "bt709" && v.Transfer == "bt709" && v.Matrix == "bt709"
}

func untagged(v *Video) bool {
	for _, t := range []string{v.Primaries, v.Transfer, v.Matrix} {
		if t != "" && t != "unknown" && t != "bt709" {
			return false
		}
	}
	return true
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return "ffmpeg failed"
	}
	return s
}
