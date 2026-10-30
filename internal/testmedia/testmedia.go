// Package testmedia generates tiny test videos with ffmpeg (testsrc2 + a sine tone) for the Go tests. Nothing here
// ships in the binary; only _test files import it.
package testmedia

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
