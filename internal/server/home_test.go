package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/thumb"
)

// fakeThumbs writes a JPEG-looking frame named after the video, and counts its runs.
func fakeThumbs(runs *atomic.Int32) *thumb.Maker {
	return &thumb.Maker{Run: func(_ context.Context, video, out string) error {
		runs.Add(1)
		if filepath.Base(video) == "clip_v7.mov" {
			return os.ErrInvalid // no frame ffmpeg can read
		}
		return os.WriteFile(out, append([]byte{0xff, 0xd8, 0xff}, filepath.Base(video)...), 0o600)
	}}
}

func TestPosterRoute(t *testing.T) {
	f := newFixture(t)
	var runs atomic.Int32
	h := start(t, Options{Video: f.video, Thumbs: fakeThumbs(&runs)})

	r := h.get("/api/poster", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "image/jpeg" || r.header.Get("Cache-Control") != "no-cache" ||
		!bytes.HasSuffix(r.body, []byte("clip_v6.mp4")) || r.header.Get("Last-Modified") == "" {
		t.Fatalf("current poster: %d %v %q", r.code, r.header, r.body)
	}
	// The same frame again, from the cache; a revalidation is a 304.
	if r2 := h.get("/api/poster?v=v6", map[string]string{"If-Modified-Since": r.header.Get("Last-Modified")}); r2.code !=
		304 {
		t.Errorf("revalidation: %d", r2.code)
	}
	if r := h.get("/api/poster?v=v5", nil); r.code != 200 || !bytes.HasSuffix(r.body, []byte("clip_v5.mp4")) {
		t.Errorf("v5 poster: %d %q", r.code, r.body)
	}
	if runs.Load() != 2 {
		t.Errorf("frames made %d times, want 2", runs.Load())
	}
	if r := h.get("/api/poster?v=v7", nil); r.code != 404 || r.json(t)["error"] != "no poster" {
		t.Errorf("no frame: %d %s", r.code, r.body)
	}
	if r := h.get("/api/poster?v=v99", nil); r.code != 404 || r.json(t)["error"] != "no such version" {
		t.Errorf("unknown version: %d %s", r.code, r.body)
	}
	// The streaming side's poster beside the file wins.
	if err := os.WriteFile(filepath.Join(f.root, "v5", "clip_v5.poster.jpg"), []byte("\xff\xd8streamed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := h.get("/api/poster?v=v5", nil); r.code != 200 || string(r.body) != "\xff\xd8streamed" {
		t.Errorf("streamed poster: %d %q", r.code, r.body)
	}
	// It's cached in the project root's .retake/thumbs/.
	if entries, err := os.ReadDir(thumb.Dir(f.root)); err != nil || len(entries) != 2 {
		t.Errorf("cache: %v %v", entries, err)
	}
	// Through a share's host, a token is needed, as for every watch route.
	if r := h.do(http.MethodGet, "/api/poster", nil, map[string]string{"X-Forwarded-For": "1.2.3.4"}); r.code != 401 {
		t.Errorf("shared without a token: %d", r.code)
	}
}

func TestUnwatchRoute(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var calls []string
	h := start(t, Options{Video: f.video, Unwatch: func(label, viewer string) {
		mu.Lock()
		calls = append(calls, label+"/"+viewer)
		mu.Unlock()
	}})
	// sendBeacon posts a small text body from the page's own origin.
	r := h.post("/api/versions/v6/unwatch?viewer=abc123", "", h.origin())
	if r.code != 204 || len(r.body) != 0 {
		t.Fatalf("unwatch: %d %q", r.code, r.body)
	}
	if r := h.post("/api/versions/v5/unwatch", "{}", nil); r.code != 204 {
		t.Errorf("no viewer: %d", r.code)
	}
	if r := h.post("/api/versions/v99/unwatch", "", nil); r.code != 404 {
		t.Errorf("unknown version: %d", r.code)
	}
	if r := h.post("/api/versions/v6/unwatch", "", map[string]string{"Origin": "https://evil.test"}); r.code != 403 {
		t.Errorf("another site: %d", r.code)
	}
	mu.Lock()
	if len(calls) != 2 || calls[0] != "v6/abc123" || calls[1] != "v5/" {
		t.Errorf("calls %v", calls)
	}
	mu.Unlock()
	// Without a hook it answers all the same.
	h2 := start(t, Options{Video: f.video})
	if r := h2.post("/api/versions/v6/unwatch", "", nil); r.code != 204 {
		t.Errorf("no hook: %d", r.code)
	}
	// A viewer may leave too: unwatching is part of watching, not recording.
	if got := PermFor(http.MethodPost, "/api/versions/v6/unwatch"); got != team.PermWatch {
		t.Errorf("unwatch needs %s", got)
	}
	if got := PermFor(http.MethodPost, "/api/versions/v6/approve"); got != team.PermApprove {
		t.Errorf("approve needs %s", got)
	}
	if !team.Viewer.Can(PermFor(http.MethodPost, "/api/versions/v6/unwatch")) {
		t.Error("a viewer can't unwatch")
	}
}

func TestInfoJSON(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video, External: true, FollowNewest: true})
	var info struct {
		Label    string `json:"label"`
		Role     string `json:"role"`
		Versions []struct {
			Label string `json:"label"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(h.srv.InfoJSON(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Label != "v7" || info.Role != "owner" || len(info.Versions) != 3 {
		t.Errorf("info %+v", info)
	}
	// The route itself still answers only requests the caller granted.
	if r := h.get("/api/info", nil); r.code != 403 {
		t.Errorf("an ungranted request to an External server: %d", r.code)
	}
}
