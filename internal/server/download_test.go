package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/download"
	"github.com/samahlstrom/retake/internal/relay"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// x264 is a download Manager that encodes with x264 (tests never touch a GPU).
func x264() *download.Manager {
	return &download.Manager{Getenv: func(k string) string {
		if k == "RETAKE_ENCODER" {
			return "x264"
		}
		return ""
	}}
}

// waitReady asks GET /api/download until the version's download is ready (or fails), and returns its answer.
func waitReady(t *testing.T, h *harness, label string, hdr map[string]string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		m := h.get("/api/download?v="+label, hdr).json(t)
		switch m["state"] {
		case download.StateReady, download.StateFailed, download.StateUnavailable:
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never got ready: %v", label, m)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A plain project: v1 is an MP4 YouTube takes as it is, served as it is; v2 is an MKV with a loud tone, made once
// into .retake/downloads/ and reused.
func TestDownloadServesAsItIsOrMadeOnce(t *testing.T) {
	root := t.TempDir()
	v1 := filepath.Join(root, "v1", "clip_v1.mp4")
	v2 := filepath.Join(root, "v2", "clip_v2.mkv")
	testmedia.YouTube(t, v1)
	testmedia.Clip(t, v2, -4, append(append([]string{}, testmedia.H264High...), testmedia.AACStereo...)...)
	h := start(t, Options{Video: v2, Root: root, Downloads: x264()})
	t.Cleanup(func() { _ = h.srv.Close() })

	if info := h.get("/api/info", nil).json(t); info["downloads"] != true {
		t.Fatalf("info: downloads %v", info["downloads"])
	}
	if m := h.get("/api/download?v=v1", nil).json(t); m["state"] != download.StateNone || m["name"] != "clip_v1.mp4" ||
		m["label"] != "v1" || m["license"] != "" {
		t.Fatalf("before: %v", m)
	}
	if r := h.post("/api/download?v=v1", "", nil); r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	m := waitReady(t, h, "v1", nil)
	whole, _ := os.ReadFile(v1)
	if m["state"] != download.StateReady || m["as_is"] != true || m["size"] != float64(len(whole)) {
		t.Fatalf("v1: %v", m)
	}
	r := h.get("/video/v1/download", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "video/mp4" ||
		r.header.Get("Content-Disposition") != `attachment; filename="clip_v1.mp4"; filename*=UTF-8''clip_v1.mp4` ||
		r.header.Get("Content-Length") != strconv.Itoa(len(whole)) || r.header.Get("Accept-Ranges") != "bytes" ||
		!bytes.Equal(r.body, whole) {
		t.Fatalf("v1 file: %d %v %d bytes", r.code, r.header, len(r.body))
	}
	if r = h.get("/video/v1/download", map[string]string{"Range": "bytes=10-19"}); r.code != 206 ||
		r.header.Get("Content-Range") != fmt.Sprintf("bytes 10-19/%d", len(whole)) || !bytes.Equal(r.body, whole[10:20]) {
		t.Fatalf("range: %d %v", r.code, r.header)
	}
	if r = h.do(http.MethodHead, "/video/v1/download", nil, nil); r.code != 200 || r.header.Get("Content-Length") != strconv.Itoa(len(whole)) {
		t.Fatalf("HEAD: %d %v", r.code, r.header)
	}

	// v2 isn't ready: the file route starts it and says so.
	r = h.get("/video/v2/download", nil)
	if r.code != http.StatusConflict || r.header.Get("Retry-After") != "5" || r.json(t)["error"] != "not ready" {
		t.Fatalf("v2 before: %d %v %s", r.code, r.header, r.body)
	}
	m = waitReady(t, h, "v2", nil)
	if m["state"] != download.StateReady || m["as_is"] != false || m["name"] != "clip_v2.mp4" {
		t.Fatalf("v2: %v", m)
	}
	r = h.get("/video/v2/download", nil)
	if r.code != 200 || !strings.Contains(r.header.Get("Content-Disposition"), `filename="clip_v2.mp4"`) ||
		r.header.Get("Content-Length") != strconv.Itoa(len(r.body)) {
		t.Fatalf("v2 file: %d %v", r.code, r.header)
	}
	got := filepath.Join(t.TempDir(), "clip_v2.mp4")
	if err := os.WriteFile(got, r.body, 0o600); err != nil {
		t.Fatal(err)
	}
	testmedia.YouTubeReady(t, got)
	made, _ := filepath.Glob(filepath.Join(download.Dir(root), "*.mp4"))
	if len(made) != 1 {
		t.Fatalf("made: %v", made)
	}
	before, _ := os.Stat(made[0])
	if r2 := h.get("/video/v2/download", nil); r2.code != 200 || !bytes.Equal(r2.body, r.body) {
		t.Fatal("again", r2.code)
	}
	after, _ := os.Stat(made[0])
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("made again")
	}
	// The version files are never touched.
	if _, err := os.Stat(filepath.Join(root, "v2", "clip_v2.mp4")); err == nil {
		t.Fatal("wrote beside the version")
	}
	if r = h.get("/video/v9/download", nil); r.code != 404 {
		t.Fatal("unknown version", r.code)
	}
	if r = h.get("/api/download?v=v9", nil); r.code != 404 {
		t.Fatal("unknown version", r.code)
	}
}

// Who may download: everyone who can watch, else as [server] downloads says.
func TestDownloadRoles(t *testing.T) {
	for _, c := range []struct {
		who  string
		want map[team.Role]bool
	}{
		{"", map[team.Role]bool{team.Viewer: true, team.Reviewer: true, team.Approver: true, team.Owner: true}},
		{DownloadsReviewers, map[team.Role]bool{team.Viewer: false, team.Reviewer: true, team.Approver: true, team.Owner: true}},
		{DownloadsOff, map[team.Role]bool{team.Viewer: false, team.Reviewer: false, team.Approver: false, team.Owner: false}},
	} {
		t.Run("downloads="+c.who, func(t *testing.T) {
			h := startTeam(t, Options{WhoDownloads: c.who, Downloads: x264()})
			for role, may := range c.want {
				hdr := map[string]string{}
				host := "share.test"
				if role == team.Owner {
					host = ""
				} else {
					hdr["Cookie"] = ShareCookie + "=" + makeToken(t, h, string(role), "", time.Hour)
				}
				info := h.req("GET", "/api/info", host, hdr)
				if info.code != 200 || info.json(t)["downloads"] != may {
					t.Errorf("%s: info %d downloads %v, want %v", role, info.code, info.json(t)["downloads"], may)
				}
				for _, rt := range [][2]string{{"GET", "/api/download?v=v5"}, {"GET", "/video/v5/download"}} {
					res := h.req(rt[0], rt[1], host, hdr)
					if refused := res.code == http.StatusForbidden; refused == may {
						t.Errorf("%s %s %s: %d %s", role, rt[0], rt[1], res.code, res.body)
					}
				}
			}
			if c.who == DownloadsOff {
				if res := h.get("/api/download?v=v5", nil); res.json(t)["error"] != "downloads are off" {
					t.Errorf("off: %s", res.body)
				}
			}
		})
	}
}

func TestALiveRevisionDownloadsUnderItsVersion(t *testing.T) {
	for label, want := range map[string]string{"v5": "v5", "v5-draft2": "v5", "V12-draft30": "V12"} {
		if got := versionOfLabel(label); got != want {
			t.Errorf("%s: %s, want %s", label, got, want)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	for name, want := range map[string]string{
		"clip_v5.mp4":      `attachment; filename="clip_v5.mp4"; filename*=UTF-8''clip_v5.mp4`,
		`Ünï "cut"_v2.mp4`: `attachment; filename="_n_ _cut__v2.mp4"; filename*=UTF-8''%C3%9Cn%C3%AF%20%22cut%22_v2.mp4`,
	} {
		if got := contentDisposition(name); got != want {
			t.Errorf("%q: %s, want %s", name, got, want)
		}
	}
}

// answerDownload is a fake worker's answer to the relay's download request: none until asked to start, then ready
// with its "download" file's size.
func (w *fakeWorker) answerDownload(ctx context.Context, r relay.Request) {
	key := r.Version + r.Render
	data, ok := w.files[key+"/download"]
	status := http.StatusNotFound
	var body []byte
	if ok {
		w.mu.Lock()
		if r.Start {
			w.ready[key] = true
		}
		ready := w.ready[key]
		w.mu.Unlock()
		st := download.Status{State: download.StateNone, License: "Has 2 assets marked review only"}
		if ready {
			st.State, st.Size, st.Modified = download.StateReady, int64(len(data)), 1700000000000
		}
		body, _ = json.Marshal(st)
		status = http.StatusOK
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, w.h.url+"/api/worker/media/"+r.ID, bytes.NewReader(body))
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Media-Status", strconv.Itoa(status))
	req.Header.Set("X-Media-Length", strconv.Itoa(len(body)))
	req.Header.Set("Content-Type", "application/json")
	if res, err := w.h.ts.Client().Do(req); err == nil {
		_ = res.Body.Close()
	}
}

// A connected computer's version downloads through the relay: got ready there, then its bytes in 8 MiB chunks, cached
// on the server. The license line comes from the computer.
func TestRelayedDownload(t *testing.T) {
	h, w, _ := relayFixture(t, 1<<30)
	big := pattern(2*relay.Chunk + 12345)
	w.mu.Lock()
	w.files["v1/download"] = big
	w.ready = map[string]bool{}
	w.mu.Unlock()

	m := h.get("/api/download?v=v1", nil).json(t)
	if m["state"] != download.StateNone || m["name"] != "clip_v1.mp4" || m["license"] != "Has 2 assets marked review only" {
		t.Fatalf("before: %v", m)
	}
	// The file route gets it ready (this computer is quick), then streams it.
	asked := w.count()
	r := h.get("/video/v1/download", nil)
	if r.code != 200 || r.header.Get("Content-Length") != strconv.Itoa(len(big)) || !bytes.Equal(r.body, big) ||
		!strings.Contains(r.header.Get("Content-Disposition"), `filename="clip_v1.mp4"`) ||
		r.header.Get("Content-Type") != "video/mp4" {
		t.Fatalf("whole: %d %v %d bytes", r.code, r.header, len(r.body))
	}
	chunks, started := 0, false
	w.mu.Lock()
	for _, q := range w.asked[asked:] {
		started = started || q.Op == "download" && q.Start && q.Version == "v1"
		if q.Download {
			chunks++
			if q.Version != "v1" || q.Op != "" || q.Range == "" {
				t.Errorf("chunk request: %+v", q)
			}
		}
	}
	w.mu.Unlock()
	if chunks != 3 || !started {
		t.Fatalf("%d chunk requests, want 3; started %v", chunks, started)
	}
	if m = h.get("/api/download?v=v1", nil).json(t); m["state"] != download.StateReady || m["size"] != float64(len(big)) {
		t.Fatalf("ready: %v", m)
	}
	// Again, and a range: the status is asked, the bytes come from the server's cache.
	asked = w.count()
	if r = h.get("/video/v1/download", map[string]string{"Range": fmt.Sprintf("bytes=%d-", relay.Chunk+5)}); r.code != 206 ||
		!bytes.Equal(r.body, big[relay.Chunk+5:2*relay.Chunk]) {
		t.Fatalf("range: %d %v", r.code, r.header)
	}
	if r = h.get("/video/v1/download", nil); r.code != 200 || !bytes.Equal(r.body, big) {
		t.Fatal("again", r.code)
	}
	w.mu.Lock()
	for _, q := range w.asked[asked:] {
		if q.Download {
			t.Errorf("a cached chunk was asked again: %+v", q)
		}
	}
	w.mu.Unlock()
	// A version the computer has no download for.
	if m = h.get("/api/download?v=v2", nil).json(t); m["state"] != download.StateUnavailable {
		t.Fatalf("v2: %v", m)
	}
}
