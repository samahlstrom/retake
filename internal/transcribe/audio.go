package transcribe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SampleRate is what every engine gets: 16 kHz, mono, 16-bit PCM in a WAV file.
const SampleRate = 16000

// minAudio is the shortest audio worth running a model on, in seconds. A segment shorter than this (R pressed and
// released at once) has no words.
const minAudio = 0.1

// DecodeWAV decodes src (webm, ogg, m4a, or anything else ffmpeg reads) to a 16 kHz mono 16-bit WAV at dst and
// returns its duration in seconds.
func DecodeWAV(ctx context.Context, src, dst string) (float64, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return 0, errors.New("ffmpeg not found on $PATH: it decodes the recorded audio")
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-v", "error", "-y", "-i", src, //nolint:gosec // our own args
		"-vn", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-c:a", "pcm_s16le", "-f", "wav", dst)
	var stderr tail
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("ffmpeg can't decode %s: %w: %s", filepath.Base(src), err, stderr.String())
	}
	info, err := readWAV(dst)
	if err != nil {
		return 0, fmt.Errorf("ffmpeg's output for %s: %w", filepath.Base(src), err)
	}
	return info.duration(), nil
}

// prepareWAV returns a 16 kHz mono WAV of src and its duration: src itself when it already is one, else a decoded
// copy in dir.
func prepareWAV(ctx context.Context, src, dir string) (string, float64, error) {
	if info, err := readWAV(src); err == nil && info.whisperReady() {
		return src, info.duration(), nil
	}
	dst := filepath.Join(dir, "audio.wav")
	d, err := DecodeWAV(ctx, src, dst)
	return dst, d, err
}

// wavInfo is what readWAV learns from a WAV header.
type wavInfo struct {
	format     uint16 // 1 = PCM
	channels   uint16
	rate       uint32
	bits       uint16
	dataBytes  int64
	dataOffset int64
}

func (w wavInfo) whisperReady() bool {
	return w.format == 1 && w.channels == 1 && w.rate == SampleRate && w.bits == 16
}

func (w wavInfo) duration() float64 {
	frame := int64(w.channels) * int64(w.bits/8)
	if frame == 0 || w.rate == 0 {
		return 0
	}
	return float64(w.dataBytes/frame) / float64(w.rate)
}

// readWAV walks a RIFF/WAVE file's chunks up to "data". A data size that runs past the end of the file (a header
// written before the length was known) is clamped to what's there.
func readWAV(path string) (wavInfo, error) {
	var info wavInfo
	f, err := os.Open(path) //nolint:gosec // a path we were asked to read
	if err != nil {
		return info, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return info, err
	}
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[0:4]) != "RIFF" || string(riff[8:12]) != "WAVE" {
		return info, errors.New("not a WAV file")
	}
	pos := int64(12) // the current chunk's header
	haveFmt := false
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return info, errors.New("WAV file has no data chunk")
		}
		id, size, body := string(hdr[0:4]), int64(binary.LittleEndian.Uint32(hdr[4:8])), pos+8
		switch id {
		case "fmt ":
			if size < 16 {
				return info, errors.New("WAV fmt chunk is too short")
			}
			var fm [26]byte
			if _, err := io.ReadFull(f, fm[:min(size, 26)]); err != nil {
				return info, err
			}
			info.format = binary.LittleEndian.Uint16(fm[0:2])
			info.channels = binary.LittleEndian.Uint16(fm[2:4])
			info.rate = binary.LittleEndian.Uint32(fm[4:8])
			info.bits = binary.LittleEndian.Uint16(fm[14:16])
			if info.format == 0xFFFE && size >= 26 { // WAVE_FORMAT_EXTENSIBLE: the real format starts the sub-format GUID
				info.format = binary.LittleEndian.Uint16(fm[24:26])
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return info, errors.New("WAV data chunk comes before fmt")
			}
			info.dataOffset = body
			info.dataBytes = max(0, min(size, st.Size()-body))
			return info, nil
		}
		pos = body + size + size%2 // chunks are word-aligned
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return info, err
		}
	}
}

// tail is an io.Writer that keeps the last 8 KiB written to it, for error messages from a child process.
type tail struct{ b []byte }

const tailMax = 8 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > tailMax {
		t.b = append(t.b[:0], t.b[len(t.b)-tailMax:]...)
	}
	return len(p), nil
}

// String returns the last few lines, trimmed, on one line each joined by " | ".
func (t *tail) String() string {
	lines := strings.Split(strings.TrimSpace(string(t.b)), "\n")
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.Join(lines, " | ")
}
