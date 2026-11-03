// Package testmedia generates tiny test videos with ffmpeg (testsrc2 + a sine tone) for the Go tests. Nothing here
// ships in the binary; only _test files import it.
package testmedia

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Need skips the test when ffmpeg or ffprobe isn't installed.
func Need(t testing.TB) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
}

// NeedShell skips the test on Windows, where its fake programs (POSIX shell scripts run as executables) can't run.
// The behaviour they stand in for is covered there by the tests that don't need them and by CI's Windows smoke test.
func NeedShell(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("its fake programs are POSIX shell scripts, which Windows can't run")
	}
}

func run(t testing.TB, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y", "-threads", "2"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
}

func sources(seconds float64) []string {
	d := strconv.FormatFloat(seconds, 'f', -1, 64)
	return []string{
		"-f", "lavfi", "-i", "testsrc2=s=64x36:r=10:d=" + d,
		"-f", "lavfi", "-i", "sine=f=440:sample_rate=48000:d=" + d,
	}
}

// Video writes a 64x36, 10 fps H.264 + AAC file of the given length to path (creating its folder). The container
// follows the extension (.mp4, .mov, .mkv; .webm gets VP9 + Opus).
func Video(t testing.TB, path string, seconds float64) {
	t.Helper()
	Need(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	codecs := []string{"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac"}
	if filepath.Ext(path) == ".webm" {
		codecs = []string{"-c:v", "libvpx-vp9", "-deadline", "realtime", "-c:a", "libopus"}
	}
	args := append(sources(seconds), codecs...)
	args = append(args, "-shortest", "-movflags", "+faststart", path)
	run(t, args...)
}

// HLS writes a VOD playlist named playlist into dir, with .ts segments seg_000.ts... beside it.
func HLS(t testing.TB, dir, playlist string, seconds float64) {
	t.Helper()
	Need(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	args := append(sources(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac",
		"-shortest", "-f", "hls", "-hls_time", "1", "-hls_playlist_type", "vod",
		"-hls_segment_filename", filepath.Join(dir, "seg_%03d.ts"), filepath.Join(dir, playlist))
	run(t, args...)
}

// Garbage writes a file that has a video name but doesn't probe, like a render still in progress.
func Garbage(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a finished video yet"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Codec arguments for Clip. H264High is veryfast, not ultrafast: ultrafast leaves out what makes a stream High.
var (
	H264High  = []string{"-c:v", "libx264", "-preset", "veryfast", "-profile:v", "high", "-pix_fmt", "yuv420p"}
	BT709     = []string{"-vf", "setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709:range=tv"}
	AACStereo = []string{"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-ac", "2"}
	FastStart = []string{"-movflags", "+faststart"}
)

// Clip writes a 4 s clip to path: testsrc2 at 160x90, 25 fps, and a 440 Hz tone at gainDB (dBFS peak) in both
// channels, encoded with args (codecs, tags, container options).
func Clip(t testing.TB, path string, gainDB float64, args ...string) {
	t.Helper()
	Need(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	a := []string{"-f", "lavfi", "-i", "testsrc2=s=160x90:r=25:d=4", "-f", "lavfi", "-i",
		"sine=f=440:sample_rate=48000:d=4", "-filter_complex",
		"[1:a]volume=" + strconv.FormatFloat(gainDB, 'f', 2, 64) + "dB,pan=stereo|c0=c0|c1=c0[a]", "-map", "0:v", "-map", "[a]"}
	a = append(a, args...)
	run(t, append(a, "-shortest", path)...)
}

// R128 is ffmpeg's EBU R128 meter's reading of a file's first audio stream: integrated loudness (LUFS) and true peak
// (dBTP).
func R128(t testing.TB, path string) (lufs, peak float64) {
	t.Helper()
	out, _ := exec.Command("ffmpeg", "-nostdin", "-nostats", "-i", path, "-map", "0:a:0", "-af", "ebur128=peak=true",
		"-f", "null", "-").CombinedOutput()
	sum := string(out)
	if i := strings.LastIndex(sum, "Summary:"); i >= 0 {
		sum = sum[i:]
	}
	num := func(key string) float64 {
		_, rest, ok := strings.Cut(sum, key)
		if !ok || len(strings.Fields(rest)) == 0 {
			t.Fatalf("no %s in ffmpeg's loudness reading of %s:\n%s", key, path, out)
		}
		v, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	return num("I:"), num("Peak:")
}

// YouTube writes a clip YouTube takes as it is (spec/api.md, "Download"): H.264 High yuv420p tagged BT.709, AAC-LC
// 48 kHz stereo at -14 LUFS, faststart. The tone is measured once and corrected, so it lands on the level.
func YouTube(t testing.TB, path string) {
	t.Helper()
	probe := filepath.Join(filepath.Dir(path), ".level-"+filepath.Base(path))
	Clip(t, probe, -20, append(append([]string{}, H264High...), AACStereo...)...)
	i, _ := R128(t, probe)
	_ = os.Remove(probe)
	args := append(append(append(append([]string{}, H264High...), BT709...), AACStereo...), FastStart...)
	Clip(t, path, -20+(-14-i), args...)
}

type probed struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Profile    string `json:"profile"`
		PixFmt     string `json:"pix_fmt"`
		ColorSpace string `json:"color_space"`
		ColorPrim  string `json:"color_primaries"`
		ColorTrc   string `json:"color_transfer"`
		SampleRate string `json:"sample_rate"`
		Channels   int    `json:"channels"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
	Format struct {
		FormatName string            `json:"format_name"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

func ffprobeJSON(t testing.TB, path string) probed {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", "-show_format", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var p probed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// YouTubeReady checks a Clip's download with ffprobe, its first bytes and the R128 meter (spec/api.md, "Download"):
// MP4, faststart, H.264 High yuv420p BT.709 at 160x90 and 25 fps, AAC-LC 48 kHz stereo, -14 LUFS ±1, at most -1 dBTP.
func YouTubeReady(t testing.TB, path string) {
	t.Helper()
	p := ffprobeJSON(t, path)
	if !strings.Contains(p.Format.FormatName, "mp4") || p.Format.Tags["major_brand"] == "qt  " {
		t.Errorf("%s: format %s brand %q", path, p.Format.FormatName, p.Format.Tags["major_brand"])
	}
	var video, audio bool
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			video = true
			if s.CodecName != "h264" || s.Profile != "High" || s.PixFmt != "yuv420p" || s.ColorPrim != "bt709" ||
				s.ColorTrc != "bt709" || s.ColorSpace != "bt709" {
				t.Errorf("%s: video %+v", path, s)
			}
			if s.Width != 160 || s.Height != 90 || s.RFrameRate != "25/1" {
				t.Errorf("%s: size or rate changed: %dx%d %s", path, s.Width, s.Height, s.RFrameRate)
			}
		case "audio":
			audio = true
			if s.CodecName != "aac" || s.Profile != "LC" || s.SampleRate != "48000" || s.Channels != 2 {
				t.Errorf("%s: audio %+v", path, s)
			}
		}
	}
	if !video || !audio {
		t.Errorf("%s: video %v audio %v", path, video, audio)
	}
	head := make([]byte, 64<<10)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := f.Read(head)
	_ = f.Close()
	moov, mdat := bytes.Index(head[:n], []byte("moov")), bytes.Index(head[:n], []byte("mdat"))
	if moov < 0 || mdat >= 0 && mdat < moov {
		t.Errorf("%s: not faststart (moov at %d, mdat at %d)", path, moov, mdat)
	}
	i, tp := R128(t, path)
	if math.Abs(i+14) > 1 || tp > -1 {
		t.Errorf("%s: %.2f LUFS, %.2f dBTP; want -14 ±1, at most -1", path, i, tp)
	}
}
