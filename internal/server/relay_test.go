package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/relay"
)

// fakeWorker answers the server's relay requests from files in memory, as a worker does (relay.Plan), and counts
// what it was asked.
type fakeWorker struct {
	h       *harness
	headers map[string]string
	files   map[string][]byte // by "<version or render>/<file>"
	mu      sync.Mutex
	asked   []relay.Request
	hold    chan struct{} // when set, every answer waits for it
	served  atomic.Int64
}

func (w *fakeWorker) run(ctx context.Context) {
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, w.h.url+"/api/worker/media", nil)
		for k, v := range w.headers {
			req.Header.Set(k, v)
		}
		res, err := w.h.ts.Client().Do(req)
		if err != nil {
			return
		}
		var r relay.Request
		if res.StatusCode == http.StatusOK {
			_ = json.NewDecoder(res.Body).Decode(&r)
		}
		_ = res.Body.Close()
		if r.ID == "" {
			continue
		}
		go w.answer(ctx, r)
	}
}

func (w *fakeWorker) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.asked)
}

func (w *fakeWorker) answer(ctx context.Context, r relay.Request) {
	w.mu.Lock()
	w.asked = append(w.asked, r)
	hold := w.hold
	w.mu.Unlock()
	if hold != nil {
		<-hold
	}
	key := r.Version + r.Render + "/" + r.File
	data, ok := w.files[key]
	status, length, rng := http.StatusNotFound, int64(0), ""
	var body []byte
	_, ranged := relay.ParseRange(r.Range)
	switch {
	case ok && r.Head && !ranged: // a HEAD of the whole file: its size
		status, length = http.StatusOK, int64(len(data))
	case ok:
		start, end, st := relay.Plan(r.Range, int64(len(data)), relay.Chunk)
		status = st
		switch st {
		case http.StatusRequestedRangeNotSatisfiable:
			rng = "bytes */" + strconv.Itoa(len(data))
		case http.StatusPartialContent:
			rng = relay.ContentRange(start, end, int64(len(data)))
			fallthrough
		default:
			body, length = data[start:end+1], end-start+1
			if r.Head {
				body = nil
			}
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, w.h.url+"/api/worker/media/"+r.ID, bytes.NewReader(body))
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Media-Status", strconv.Itoa(status))
	req.Header.Set("X-Media-Length", strconv.FormatInt(length, 10))
	req.Header.Set("X-Media-Modified", "1700000000000")
	req.Header.Set("Content-Range", rng)
	if res, err := w.h.ts.Client().Do(req); err == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		w.served.Add(length)
	}
}

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

// relayFixture is a server with a relay cache and a connected fake worker that has a plain project's version files:
// v1 (a 2.4-chunk MP4) and v2 (an HLS playlist with a segment).
func relayFixture(t *testing.T, cacheSize int64, opts ...func(*Options)) (*harness, *fakeWorker, map[string]string) {
	t.Helper()
	cache, err := relay.OpenCache(t.TempDir(), cacheSize)
	if err != nil {
		t.Fatal(err)
	}
	o := Options{Root: t.TempDir(), MediaCache: cache, CacheScope: "demo"}
	for _, f := range opts {
		f(&o)
	}
	h := start(t, o)
	t.Cleanup(func() { _ = h.srv.Close() })
	token := makeToken(t, h, "agent", "worker", 0)
	headers := map[string]string{"Authorization": "Bearer " + token, "X-Retake-Worker": "machine", "X-Retake-Session": "session"}
	if got := h.post("/api/worker/poll", `{"worker":{"id":"machine","session":"session","name":"Sam's computer","agents":[{"id":"codex"}]}}`, headers); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	big := pattern(2*relay.Chunk + 3*relay.Chunk/7)
	w := &fakeWorker{h: h, headers: headers, files: map[string][]byte{
		"v1/clip_v1.mp4":   big,
		"v2/clip_v2.m3u8":  []byte("#EXTM3U\n#EXTINF:1,\nseg_000.m4s\n#EXT-X-ENDLIST\n"),
		"v2/seg_000.m4s":   pattern(5000),
		"v2/outside.m4s":   []byte("never asked for"),
		"v1/../secret.mp4": []byte("never asked for"),
	}}
	files := fmt.Sprintf(`{"files":[{"label":"v1","n":1,"name":"clip_v1.mp4","size":%d,"modified":1700000000000,"duration":40,"fps":30,"width":1280,"height":720},`+
		`{"label":"v2","n":2,"name":"clip_v2.m3u8","size":60,"modified":1700000000000,"duration":1,"fps":30}]}`, len(big))
	if got := h.post("/api/worker/files", files, headers); got.code != 200 {
		t.Fatal(got.code, string(got.body))
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for range 4 {
		go w.run(ctx)
	}
	return h, w, headers
}

func TestRelayedVersionFilesPlayInAlignedChunksFromTheCache(t *testing.T) {
	h, w, headers := relayFixture(t, 1<<30)
	big := w.files["v1/clip_v1.mp4"]
	size := len(big)

	// The family lists the computer's versions, marked as relayed.
	info := h.get("/api/info", nil).json(t)
	vs, _ := info["versions"].([]any)
	if len(vs) != 2 {
		t.Fatalf("versions: %v", info["versions"])
	}
	v1 := vs[0].(map[string]any)
	if v1["label"] != "v1" || v1["name"] != "clip_v1.mp4" || v1["relay"] != true || v1["duration"] != 40.0 {
		t.Fatalf("v1: %v", v1)
	}

	// An open-ended range gets its first chunk, capped: 206 with the file's size.
	r := h.get("/video/v1", map[string]string{"Range": "bytes=0-"})
	if r.code != 206 || r.header.Get("Content-Range") != fmt.Sprintf("bytes 0-%d/%d", relay.Chunk-1, size) ||
		r.header.Get("Content-Length") != strconv.Itoa(relay.Chunk) || r.header.Get("Accept-Ranges") != "bytes" ||
		r.header.Get("Content-Type") != "video/mp4" || !bytes.Equal(r.body, big[:relay.Chunk]) {
		t.Fatalf("bytes=0-: %d %v %d bytes", r.code, r.header, len(r.body))
	}
	if n := w.count(); n != 1 {
		t.Fatalf("worker asked %d times", n)
	}
	// The same chunk again, and a range inside it: from the server's cache, the worker isn't asked.
	if r = h.get("/video/v1", map[string]string{"Range": "bytes=0-"}); r.code != 206 || !bytes.Equal(r.body, big[:relay.Chunk]) {
		t.Fatal("again", r.code)
	}
	if r = h.get("/video/v1", map[string]string{"Range": "bytes=100-199"}); r.code != 206 ||
		r.header.Get("Content-Range") != fmt.Sprintf("bytes 100-199/%d", size) || !bytes.Equal(r.body, big[100:200]) {
		t.Fatal("inside", r.code, r.header.Get("Content-Range"))
	}
	if n := w.count(); n != 1 {
		t.Fatalf("a cached chunk asked the worker again (%d)", n)
	}
	// A seek into the middle: the rest of that chunk, asked for as the whole aligned chunk.
	mid := relay.Chunk + 12345
	r = h.get("/video/v1", map[string]string{"Range": fmt.Sprintf("bytes=%d-", mid)})
	if r.code != 206 || r.header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", mid, 2*relay.Chunk-1, size) ||
		!bytes.Equal(r.body, big[mid:2*relay.Chunk]) {
		t.Fatal("seek", r.code, r.header.Get("Content-Range"))
	}
	w.mu.Lock()
	last := w.asked[len(w.asked)-1]
	w.mu.Unlock()
	if last.Version != "v1" || last.File != "clip_v1.mp4" || last.Range != relay.ChunkRange(1) {
		t.Fatalf("asked %+v", last)
	}
	// A suffix range, and the end of the file.
	if r = h.get("/video/v1", map[string]string{"Range": "bytes=-10"}); r.code != 206 || !bytes.Equal(r.body, big[size-10:]) ||
		r.header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", size-10, size-1, size) {
		t.Fatal("suffix", r.code, r.header.Get("Content-Range"))
	}
	// Past the end: 416 with the size.
	if r = h.get("/video/v1", map[string]string{"Range": fmt.Sprintf("bytes=%d-", size)}); r.code != 416 ||
		r.header.Get("Content-Range") != fmt.Sprintf("bytes */%d", size) {
		t.Fatal("past the end", r.code, r.header)
	}
	// No range: the whole file, chunk after chunk, as one 200.
	if r = h.get("/video/v1", nil); r.code != 200 || r.header.Get("Content-Length") != strconv.Itoa(size) || !bytes.Equal(r.body, big) {
		t.Fatal("whole", r.code, len(r.body))
	}
	// A HEAD asks the worker for the size, and sends no body.
	if r = h.do(http.MethodHead, "/video/v1", nil, nil); r.code != 200 || r.header.Get("Content-Length") != strconv.Itoa(size) {
		t.Fatal("HEAD", r.code, r.header)
	}

	// A playlist version: the playlist and its segments by name; segments are cached, the playlist never.
	before := w.count()
	for range 2 {
		if r = h.get("/video/v2/clip_v2.m3u8", nil); r.code != 200 || !strings.Contains(string(r.body), "seg_000.m4s") ||
			r.header.Get("Content-Type") != "application/vnd.apple.mpegurl" {
			t.Fatal("playlist", r.code, string(r.body))
		}
		if r = h.get("/video/v2/seg_000.m4s", nil); r.code != 200 || !bytes.Equal(r.body, w.files["v2/seg_000.m4s"]) {
			t.Fatal("segment", r.code)
		}
	}
	if n := w.count() - before; n != 3 {
		t.Fatalf("the playlist twice and the segment once, not %d requests", n)
	}

	// Only the registered versions' own files: nothing else reaches the worker.
	before = w.count()
	for _, path := range []string{"/video/v3", "/video/v1/secret.mp4", "/video/v1/..%2Fsecret.mp4", "/video/v2/..%2F..%2Fetc%2Fpasswd",
		"/video/v2/notes.txt", "/video/v2/sub%2Fseg.m4s", "/video/..%2Fv1", "/video/v1/clip_v2.mp4"} {
		if r = h.get(path, nil); r.code != 404 {
			t.Errorf("%s: %d", path, r.code)
		}
	}
	if n := w.count() - before; n != 0 {
		t.Fatalf("unregistered files reached the worker: %d", n)
	}

	// Offline: what's cached still plays, the rest says the computer is offline.
	st, _ := h.srv.store(false)
	_ = st.UpdateAutomation(func(a *project.AutomationState) (bool, error) { a.Worker("machine").Seen = 1; return true, nil })
	if r = h.get("/video/v1", map[string]string{"Range": "bytes=0-"}); r.code != 206 {
		t.Fatal("a cached chunk while offline", r.code)
	}
	r = h.get("/video/v2/clip_v2.m3u8", nil) // a playlist is never cached
	if r.code != 503 || r.header.Get("Retry-After") == "" || !strings.Contains(string(r.body), "offline") {
		t.Fatal("offline", r.code, len(r.body))
	}
	if r = h.do(http.MethodHead, "/video/v1", nil, nil); r.code != 503 {
		t.Fatal("HEAD offline", r.code)
	}
	_ = headers
}

func TestConcurrentMissesOfAChunkAskTheWorkerOnce(t *testing.T) {
	h, w, _ := relayFixture(t, 1<<30)
	w.mu.Lock()
	w.hold = make(chan struct{})
	w.mu.Unlock()
	big := w.files["v1/clip_v1.mp4"]
	var wg sync.WaitGroup
	results := make(chan resp, 5)
	for i := range 5 {
		wg.Go(func() {
			results <- h.get("/video/v1", map[string]string{"Range": fmt.Sprintf("bytes=%d-", i*1000)})
		})
	}
	waitFor(t, "the first request", func() bool { return w.count() == 1 })
	time.Sleep(100 * time.Millisecond) // the other four are waiting on it by now
	close(w.hold)
	wg.Wait()
	close(results)
	for r := range results {
		if r.code != 206 || len(r.body) == 0 || !bytes.Equal(r.body, big[relay.Chunk-len(r.body):relay.Chunk]) {
			t.Fatal(r.code, len(r.body))
		}
	}
	if n := w.count(); n != 1 {
		t.Fatalf("5 viewers asked the worker %d times", n)
	}
}

func TestAGrowingRenderPlaylistIsNeverCached(t *testing.T) {
	h, w, headers := relayFixture(t, 1<<30)
	source := strings.Repeat("a", 64)
	render := `{"id":"rev1","worker":"machine","source":"` + source + `","duration":2,"fps":30,"width":64,"height":36,"done":1,"total":2}`
	r := h.post("/api/worker/render", render, headers)
	if r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	var v project.LiveRender
	_ = json.Unmarshal(r.body, &v)
	w.files["rev1/index.m3u8"] = []byte("#EXTM3U\nseg_00000.m4s\n")
	w.files["rev1/seg_00000.m4s"] = pattern(3000)
	before := w.count()
	for range 3 {
		if got := h.get("/video/"+v.Label, nil); got.code != 200 || !strings.Contains(string(got.body), "seg_00000") {
			t.Fatal(got.code, string(got.body))
		}
		if got := h.get("/video/"+v.Label+"/seg_00000.m4s", nil); got.code != 200 || len(got.body) != 3000 {
			t.Fatal(got.code)
		}
	}
	if n := w.count() - before; n != 4 {
		t.Fatalf("the growing playlist 3 times and the finished segment once, not %d", n)
	}
	// Once the render is complete its playlist is final, and cached.
	r = h.post("/api/worker/render", strings.Replace(render, `"done":1`, `"done":2,"complete":true`, 1), headers)
	if r.code != 200 {
		t.Fatal(r.code, string(r.body))
	}
	before = w.count()
	for range 2 {
		if got := h.get("/video/"+v.Label, nil); got.code != 200 {
			t.Fatal(got.code)
		}
	}
	if n := w.count() - before; n != 1 {
		t.Fatalf("a complete playlist asked %d times", n)
	}
}

func TestTheCacheKeepsItsSizeAndAWorkerOnlyChangesItsOwnFiles(t *testing.T) {
	h, w, headers := relayFixture(t, relay.Chunk+relay.Chunk/2) // one chunk fits, not two
	for _, rng := range []string{"bytes=0-", fmt.Sprintf("bytes=%d-", relay.Chunk), "bytes=0-"} {
		if r := h.get("/video/v1", map[string]string{"Range": rng}); r.code != 206 {
			t.Fatal(r.code)
		}
	}
	if n := w.count(); n != 3 {
		t.Fatalf("the first chunk was evicted by the second: 3 requests, not %d", n)
	}
	if used := h.srv.opts.MediaCache.Used(); used > relay.Chunk+relay.Chunk/2 {
		t.Fatal("over its size", used)
	}
	// Bad lists are refused whole; a worker's list replaces only its own.
	for _, bad := range []string{`{"files":[{"label":"x1","n":1,"name":"a_v1.mp4"}]}`, `{"files":[{"label":"v1","n":2,"name":"a_v1.mp4"}]}`,
		`{"files":[{"label":"v1","n":1,"name":"../a_v1.mp4"}]}`, `{"files":[{"label":"v1","n":1,"name":"notes.txt"}]}`,
		`{"files":[{"label":"v1","n":1,"name":"a.mp4"},{"label":"v1","n":1,"name":"b.mp4"}]}`} {
		if r := h.post("/api/worker/files", bad, headers); r.code != 400 {
			t.Errorf("%s: %d", bad, r.code)
		}
	}
	if r := h.post("/api/worker/files", `{"files":[{"label":"v1","n":1,"name":"clip_v1.mp4"}]}`, headers); r.code != 200 {
		t.Fatal(r.code)
	}
	if vs := h.get("/api/info", nil).json(t)["versions"].([]any); len(vs) != 1 {
		t.Fatalf("v2 is gone from the computer, and from the family: %v", vs)
	}
	stranger := map[string]string{"Authorization": headers["Authorization"], "X-Retake-Worker": "other", "X-Retake-Session": "s"}
	if r := h.post("/api/worker/files", `{"files":[]}`, stranger); r.code != 409 {
		t.Fatal("an unregistered worker sent files", r.code)
	}
}

func TestTakesOnAVersionFileAreKeptOnTheServer(t *testing.T) {
	h, _, _ := relayFixture(t, 1<<30)
	h.saveTake(take, "v1")
	r := h.get("/api/history?v=v1", nil)
	if r.code != 200 || !strings.Contains(string(r.body), take) {
		t.Fatalf("history: %d %s", r.code, r.body)
	}
	v, ok := h.srv.version("v1")
	if !ok || !strings.Contains(v.Path, ".retake/remote/v1/") {
		t.Fatalf("v1's takes go under .retake/remote: %+v", v)
	}
}
