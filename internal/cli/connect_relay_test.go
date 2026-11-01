package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// A folder of finished videos, no timeline: retake connect puts it on the server, and its videos play there through
// the relay, in ranges, from the computer (and its chunks are kept in the server's cache). Nothing is uploaded.
func TestConnectRelaysAFolderOfPlainVideos(t *testing.T) {
	testmedia.Need(t)
	scratch, err := filepath.Abs("../../.retake-work")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(scratch, "connect-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	// clip_v1.mp4 is small; clip_v2.mp4 is a few relay chunks long (lossless 720p is big, and quick to make).
	folder := filepath.Join(dir, "Videos", "Launch Promo")
	testmedia.Video(t, filepath.Join(folder, "clip_v1.mp4"), 2)
	v2 := filepath.Join(folder, "clip_v2.mp4")
	ff := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-threads", "2", "-f", "lavfi", "-i",
		"testsrc2=s=1280x720:r=25:d=12", "-f", "lavfi", "-i", "sine=f=440:sample_rate=48000:d=12", "-c:v", "libx264",
		"-preset", "ultrafast", "-qp", "0", "-pix_fmt", "yuv420p", "-threads", "2", "-c:a", "aac", "-shortest",
		"-movflags", "+faststart", v2)
	if out, err := ff.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	// And a project whose version is an HLS playlist with its segments.
	teaser := filepath.Join(dir, "Videos", "Teaser", "v1")
	testmedia.HLS(t, teaser, "teaser_v1.m3u8", 3)
	video, err := os.ReadFile(v2)
	if err != nil {
		t.Fatal(err)
	}
	size := len(video)
	if size <= 2*relay.Chunk {
		t.Fatalf("clip_v2.mp4 is %d bytes, not more than two chunks", size)
	}

	w := newWorld(t)
	data := filepath.Join(dir, "data")
	base, session, line, _ := hosted(t, w, data)
	profiles := filepath.Join(w.root, "profiles.json")
	writeFile(t, profiles, `[{"id":"fake","name":"Fake agent","command":["sh","-c","exit 0"]}]`)
	env := w.environment(nil)
	env.ConnectEvery = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan int, 1)
	go func() {
		exited <- Main(ctx, []string{"connect", line[1], "--token", line[2], "--folder", filepath.Dir(folder),
			"--name", "test-pc", "--profiles", profiles}, env)
	}()
	disconnect := func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			t.Error("connect didn't stop")
		}
	}
	defer func() {
		if ctx.Err() == nil {
			disconnect()
		}
	}()

	get := func(path string, hdr map[string]string) (int, http.Header, []byte) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.AddCookie(&http.Cookie{Name: "retake_session", Value: session})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, b
	}
	type version struct {
		Label    string  `json:"label"`
		Name     string  `json:"name"`
		Relay    bool    `json:"relay"`
		Duration float64 `json:"duration"`
		Width    int     `json:"width"`
	}
	var info struct {
		Versions []version `json:"versions"`
	}
	deadline := time.Now().Add(30 * time.Second)
	for len(info.Versions) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the versions never reached the server: %+v\n%s%s", info, w.stdout, w.stderr)
		}
		time.Sleep(100 * time.Millisecond)
		if code, _, b := get("/p/launch-promo/api/info", nil); code == http.StatusOK {
			_ = json.Unmarshal(b, &info)
		}
	}
	if v := info.Versions[1]; v.Label != "v2" || v.Name != "clip_v2.mp4" || !v.Relay || v.Duration < 11.9 || v.Width != 1280 {
		t.Fatalf("v2: %+v", v)
	}

	// Ranges through the server, from the computer: an open range is one chunk; a seek gets the rest of its chunk.
	code, h, b := get("/p/launch-promo/video/v2", map[string]string{"Range": "bytes=0-"})
	if code != http.StatusPartialContent || h.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", relay.Chunk-1, size) ||
		h.Get("Accept-Ranges") != "bytes" || h.Get("Content-Type") != "video/mp4" || !bytes.Equal(b, video[:relay.Chunk]) {
		t.Fatalf("bytes=0-: %d %v (%d bytes)", code, h, len(b))
	}
	seek := relay.Chunk + 4321
	code, h, b = get("/p/launch-promo/video/v2", map[string]string{"Range": "bytes=" + strconv.Itoa(seek) + "-"})
	if code != http.StatusPartialContent || !bytes.Equal(b, video[seek:2*relay.Chunk]) ||
		h.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", seek, 2*relay.Chunk-1, size) {
		t.Fatalf("seek: %d %s (%d bytes)", code, h.Get("Content-Range"), len(b))
	}
	if code, _, b = get("/p/launch-promo/video/v2", nil); code != http.StatusOK || !bytes.Equal(b, video) {
		t.Fatalf("the whole file: %d (%d of %d bytes)", code, len(b), size)
	}
	if code, _, b = get("/p/launch-promo/video/v1", map[string]string{"Range": "bytes=10-19"}); code != http.StatusPartialContent || len(b) != 10 {
		t.Fatalf("v1: %d %d", code, len(b))
	}
	// Only the versions' own files.
	for _, path := range []string{"/p/launch-promo/video/v2/..%2Fclip_v1.mp4", "/p/launch-promo/video/v1/clip_v2.mp4",
		"/p/launch-promo/video/v3"} {
		if code, _, _ = get(path, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d", path, code)
		}
	}

	// The playlist version: its playlist and the segments beside it, by name, and nothing else.
	deadline = time.Now().Add(30 * time.Second)
	for code, _, _ = get("/p/teaser/api/info", nil); code != http.StatusOK; code, _, _ = get("/p/teaser/api/info", nil) {
		if time.Now().After(deadline) {
			t.Fatalf("the teaser never reached the server: %d", code)
		}
		time.Sleep(100 * time.Millisecond)
	}
	code, h, b = get("/p/teaser/video/v1/teaser_v1.m3u8", nil)
	if code != http.StatusOK || h.Get("Content-Type") != "application/vnd.apple.mpegurl" || !bytes.Contains(b, []byte("seg_000.ts")) {
		t.Fatalf("playlist: %d %s", code, b)
	}
	segment, err := os.ReadFile(filepath.Join(teaser, "seg_000.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if code, _, b = get("/p/teaser/video/v1/seg_000.ts", nil); code != http.StatusOK || !bytes.Equal(b, segment) {
		t.Fatalf("segment: %d (%d bytes)", code, len(b))
	}
	for _, path := range []string{"/p/teaser/video/v1/..%2F..%2FLaunch%20Promo%2Fclip_v1.mp4", "/p/teaser/video/v1/notes.txt"} {
		if code, _, _ = get(path, nil); code != http.StatusNotFound {
			t.Errorf("%s: %d", path, code)
		}
	}

	// What was watched is in the server's cache now (later viewers get it from there): v2's three chunks and v1's one.
	chunks, _ := os.ReadDir(filepath.Join(data, "relay-cache", "launch-promo"))
	if len(chunks) != 4 {
		t.Fatalf("%d chunks cached", len(chunks))
	}
	disconnect()

	// No video reached the server's projects: only the cache holds what was watched.
	err = filepath.WalkDir(filepath.Join(data, "projects"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".mp4" {
			t.Errorf("uploaded: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
