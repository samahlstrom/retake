package download

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/bridge"
)

// Encoders a download's video can be encoded with (the [render] encoder values; qsv and vaapi encode with x264 here).
const (
	EncoderX264         = "x264"
	EncoderNVENC        = "nvenc"
	EncoderVideoToolbox = "videotoolbox"
)

// encoderArgs are the video encoder's options: x264 at medium, CRF 18 (as renders), or a hardware encoder tuned close
// to it. All H.264 High, yuv420p, two B-frames.
func encoderArgs(kind string) []string {
	switch kind {
	case EncoderNVENC:
		return []string{"-c:v", "h264_nvenc", "-preset", "p7", "-tune", "hq", "-rc", "vbr", "-cq", "19", "-b:v", "0",
			"-profile:v", "high", "-bf", "2"}
	case EncoderVideoToolbox:
		return []string{"-c:v", "h264_videotoolbox", "-q:v", "65", "-profile:v", "high"}
	}
	return []string{"-c:v", "libx264", "-preset", "medium", "-crf", "18", "-profile:v", "high", "-bf", "2"}
}

// encoder is the video encoder to use: RETAKE_ENCODER from env (the editor's) or the process, else auto. auto takes
// what retake-edit last picked on this computer (its ffmpeg-caps.json), else x264.
func (m *Manager) encoder(env []string) string {
	choice := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "RETAKE_ENCODER="); ok {
			choice = v
		}
	}
	if choice == "" {
		choice = m.getenv("RETAKE_ENCODER")
	}
	switch choice {
	case EncoderX264, EncoderNVENC, EncoderVideoToolbox:
		return choice
	case "", "auto":
		return m.autoEncoder()
	}
	return EncoderX264
}

// autoEncoder reads retake-edit's pick for auto from its per-machine cache.
func (m *Manager) autoEncoder() string {
	file := m.getenv("RETAKE_CAPS_FILE")
	if file == "" {
		dir := m.getenv("XDG_CACHE_HOME")
		if dir == "" || !filepath.IsAbs(dir) {
			home := m.getenv("HOME")
			if home == "" {
				return EncoderX264
			}
			dir = filepath.Join(home, ".cache")
		}
		file = filepath.Join(dir, "retake", "ffmpeg-caps.json")
	}
	b, err := os.ReadFile(file) //nolint:gosec // retake-edit's own cache file
	if err != nil {
		return EncoderX264
	}
	var caps map[string]struct {
		Auto *struct {
			Value string `json:"value"`
		} `json:"auto"`
	}
	if json.Unmarshal(b, &caps) != nil {
		return EncoderX264
	}
	if e, ok := caps[m.ffmpeg()]; ok && e.Auto != nil {
		switch e.Auto.Value {
		case EncoderNVENC, EncoderVideoToolbox:
			return e.Auto.Value
		}
	}
	return EncoderX264
}

// args is the ffmpeg command line that makes src's download at out, by plan.
func args(src, out string, c Check, encoder string) []string {
	p := c.Plan
	a := []string{"-nostdin", "-hide_banner", "-v", "error", "-nostats", "-y", "-i", src, "-map", "0:v:0"}
	if c.Audio != nil {
		a = append(a, "-map", "0:a:0")
	}
	tags := []string{"-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv"}
	switch {
	case p.CopyVideo && p.TagVideo:
		a = append(a, "-c:v", "copy", "-bsf:v",
			"h264_metadata=colour_primaries=1:transfer_characteristics=1:matrix_coefficients=1:video_full_range_flag=0")
		a = append(a, tags...)
	case p.CopyVideo:
		a = append(a, "-c:v", "copy")
	default:
		// The frames carry the tags (ffmpeg's encoders take them from the frames, not from -color_* options).
		a = append(a, "-vf", "scale=out_color_matrix=bt709:out_range=tv:flags=bicubic+accurate_rnd+full_chroma_int,"+
			"format=yuv420p,setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv")
		a = append(a, encoderArgs(encoder)...)
		a = append(a, "-pix_fmt", "yuv420p")
		a = append(a, tags...)
	}
	if c.Audio != nil {
		switch {
		case p.CopyAudio:
			a = append(a, "-c:a", "copy")
		default:
			if l := c.Loudness; p.Level && l != nil {
				// The second pass of loudnorm, linear (one gain) when it can be: the measured range fits.
				lra := math.Min(math.Max(11, math.Ceil(l.LRA+1)), 50)
				a = append(a, "-af", fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%.2f:measured_TP=%.2f:"+
					"measured_LRA=%.2f:measured_thresh=%.2f:offset=%.2f:linear=true,aresample=%d", LUFS, MakeTruePeak, lra,
					l.I, l.TP, l.LRA, l.Thresh, l.Offset, SampleRate))
			}
			a = append(a, "-c:a", "aac", "-profile:a", "aac_low", "-b:a", AudioBitrate, "-ar", strconv.Itoa(SampleRate),
				"-ac", strconv.Itoa(Channels))
		}
	}
	return append(a, "-movflags", "+faststart", "-f", "mp4", "-progress", "pipe:1", out)
}

// ffmpegMake makes src's download at out (written whole: a temporary file renamed), reporting progress from 0 to 1.
// A hardware encoder that fails is tried once more with x264.
func (m *Manager) ffmpegMake(ctx context.Context, src, out string, c Check, encoder string, progress func(float64)) error {
	err := m.runFFmpeg(ctx, src, out, c, encoder, progress)
	if err != nil && encoder != EncoderX264 && !c.Plan.CopyVideo && ctx.Err() == nil {
		m.logf("retake: download of %s: %s failed (%v), encoding with x264\n", filepath.Base(src), encoder, err)
		err = m.runFFmpeg(ctx, src, out, c, EncoderX264, progress)
	}
	return err
}

func (m *Manager) runFFmpeg(ctx context.Context, src, out string, c Check, encoder string, progress func(float64)) error {
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".part")
	defer func() { _ = os.Remove(tmp) }()
	cmd := exec.CommandContext(ctx, m.ffmpeg(), args(src, tmp, c, encoder)...) //nolint:gosec // our ffmpeg, our arguments
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("ffmpeg isn't installed")
		}
		return err
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		v, ok := strings.CutPrefix(sc.Text(), "out_time_us=")
		if !ok || c.Duration <= 0 {
			continue
		}
		if us, err := strconv.ParseFloat(v, 64); err == nil && us >= 0 {
			progress(math.Min(us/1e6/c.Duration, 0.99))
		}
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg: %s", lastLine(stderr.Bytes()))
	}
	return os.Rename(tmp, out)
}

// renderExport has the renderer package a rendered version's export at r.Export (spec/bridge.md, "Download"): into a
// temporary folder beside it, then moved in with its manifest.
func (m *Manager) renderExport(ctx context.Context, r *Render) error {
	bin := r.Bin
	if bin == "" {
		var err error
		if bin, err = r.Editor.Find(); err != nil {
			return err
		}
	}
	dir := filepath.Dir(r.Export)
	tmp, err := os.MkdirTemp(dir, ".download-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	out := filepath.Join(tmp, filepath.Base(r.Export))
	if _, err := r.Editor.Export(ctx, bin, bridge.ExportArgs{Timeline: r.Timeline, Doc: r.Doc, Out: out,
		Draft: r.Draft, Loudness: LUFS}); err != nil {
		return err
	}
	man := bridge.ManifestPath(out)
	if _, err := os.Stat(man); err == nil {
		if err := os.Rename(man, bridge.ManifestPath(r.Export)); err != nil {
			return err
		}
	}
	return os.Rename(out, r.Export)
}
