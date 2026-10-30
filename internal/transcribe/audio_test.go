package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/testmedia"
)

func TestReadWAV(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.wav")
	writeWAV(t, plain, 1.5)
	info, err := readWAV(plain)
	if err != nil || !info.whisperReady() || info.duration() != 1.5 {
		t.Errorf("plain: %+v %v", info, err)
	}

	// ffmpeg puts a LIST chunk (odd-sized here) before data, and some writers use WAVE_FORMAT_EXTENSIBLE.
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	le(uint32(0))
	b.WriteString("WAVEfmt ")
	le(uint32(40))
	for _, v := range []any{uint16(0xFFFE), uint16(1), uint32(SampleRate), uint32(2 * SampleRate), uint16(2),
		uint16(16), uint16(22), uint16(16), uint32(4), uint16(1)} {
		le(v)
	}
	b.Write(make([]byte, 14)) // the rest of the sub-format GUID
	b.WriteString("LIST")
	le(uint32(5))
	b.WriteString("INFOx\x00") // 5 bytes + a pad byte
	b.WriteString("data")
	le(uint32(0xFFFFFFFF)) // streamed: the length wasn't known
	b.Write(make([]byte, 3200))
	ext := filepath.Join(dir, "ext.wav")
	if err := os.WriteFile(ext, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err = readWAV(ext)
	if err != nil || !info.whisperReady() || info.dataBytes != 3200 || math.Abs(info.duration()-0.1) > 1e-9 {
		t.Errorf("extensible: %+v %v", info, err)
	}

	for name, data := range map[string]string{"empty": "", "text": "hello world, not a wav", "riff only": "RIFF\x00\x00\x00\x00WAVE"} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"))
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readWAV(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestDecodeWAV(t *testing.T) {
	testmedia.Need(t)
	dir := t.TempDir()
	m4a := filepath.Join(dir, "seg_03.m4a")
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=f=300:sample_rate=48000:d=1.5",
		"-c:a", "aac", "-ac", "2", m4a)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	for _, tc := range []struct {
		src  string
		want float64
	}{{"testdata/seg_01.webm", 4.771}, {"testdata/seg_02.ogg", 3.554}, {m4a, 1.5}} {
		t.Run(filepath.Ext(tc.src), func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "out.wav")
			d, err := DecodeWAV(context.Background(), tc.src, dst)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(d-tc.want) > 0.05 {
				t.Errorf("duration %.3f, want %.3f", d, tc.want)
			}
			info, err := readWAV(dst)
			if err != nil || !info.whisperReady() {
				t.Errorf("output %+v %v", info, err)
			}
		})
	}

	bad := filepath.Join(dir, "seg_04.webm")
	if err := os.WriteFile(bad, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWAV(context.Background(), bad, filepath.Join(dir, "x.wav")); err == nil ||
		!strings.Contains(err.Error(), "can't decode seg_04.webm") {
		t.Errorf("garbage: err = %v", err)
	}
}

func TestPrepareWAVUsesReadyWAV(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "ready.wav")
	writeWAV(t, wav, 2)
	// No ffmpeg needed: a 16 kHz mono WAV is used as it is.
	got, d, err := prepareWAV(context.Background(), wav, t.TempDir())
	if err != nil || got != wav || d != 2 {
		t.Errorf("prepareWAV = %q %v %v", got, d, err)
	}
}

func TestTail(t *testing.T) {
	var tl tail
	for i := range 2000 {
		_, _ = tl.Write([]byte(strings.Repeat("x", i%50) + "\n"))
	}
	_, _ = tl.Write([]byte("  error: the last line  \n"))
	if len(tl.b) > tailMax {
		t.Errorf("kept %d bytes", len(tl.b))
	}
	if s := tl.String(); !strings.HasSuffix(s, "| error: the last line") || strings.Count(s, "|") != 5 {
		t.Errorf("String = %q", s)
	}
}
