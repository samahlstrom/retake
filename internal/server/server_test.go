package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/samahlstrom/retake/internal/probe"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// fakeProber gives every existing file a 10 s duration, except files whose name contains "rendering".
type fakeProber struct{}

func (fakeProber) Probe(p string) probe.Info {
	if _, err := os.Stat(p); err != nil || strings.Contains(filepath.Base(p), "rendering") {
		return probe.Info{FPS: probe.DefaultFPS}
	}
	d, w, h := 10.0, 1920, 1080
	return probe.Info{Duration: &d, FPS: 30, Width: &w, Height: &h}
}

const take = "2026-10-29_20-48-01"

// fixture is a version family on disk: v5/clip_v5.mp4, v6/clip_v6.mp4 (opened), v7/clip_v7.mov, plus
// clip_v8_rendering.mp4 which doesn't probe. File contents are bytes, not real video (the prober is fake).
type fixture struct {
	root  string
	video string
	files map[string][]byte
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: t.TempDir(), files: map[string][]byte{}}
	add := func(rel string, size int) {
		b := make([]byte, size)
		for i := range b {
			b[i] = byte(i % 251)
		}
		p := filepath.Join(f.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		f.files[rel] = b
	}
	add("v5/clip_v5.mp4", 1000)
	add("v6/clip_v6.mp4", 1000)
	add("v7/clip_v7.mov", 300)
	add("v8/clip_v8_rendering.mp4", 50)
	f.video = filepath.Join(f.root, "v6", "clip_v6.mp4")
	return f
}

type harness struct {
	t   *testing.T
	srv *Server
	ts  *httptest.Server
	url string
}

func start(t *testing.T, opts Options) *harness {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	port := ts.Listener.Addr().(*net.TCPAddr).Port
	opts.Port = port
	if opts.Prober == nil {
		opts.Prober = fakeProber{}
	}
	srv := New(opts)
	ts.Config.Handler = srv
	ts.Start()
	t.Cleanup(ts.Close)
	return &harness{t: t, srv: srv, ts: ts, url: ts.URL}
}

type resp struct {
	code   int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("not JSON (%d): %q", r.code, r.body)
	}
	return m
}

func (h *harness) do(method, path string, body io.Reader, hdr map[string]string) resp {
	h.t.Helper()
	req, err := http.NewRequest(method, h.url+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp{res.StatusCode, res.Header, b}
}

func (h *harness) get(path string, hdr map[string]string) resp {
	return h.do(http.MethodGet, path, nil, hdr)
}

func (h *harness) post(path, body string, hdr map[string]string) resp {
	return h.do(http.MethodPost, path, strings.NewReader(body), hdr)
}

func TestRoutes(t *testing.T) {
	f := newFixture(t)
	page := fstest.MapFS{
		"index.html":       {Data: []byte("<!doctype html><title>page</title>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"assets/app-1.css": {Data: []byte("body{}")},
		".secret":          {Data: []byte("no")},
		"assets/.hidden":   {Data: []byte("no")},
	}
	h := start(t, Options{Video: f.video, UI: page})
	for _, tc := range []struct {
		method, path string
		code         int
		ctype        string
		cache        string
		body         string // substring
	}{
		{"GET", "/", 200, "text/html; charset=utf-8", "no-store", "<title>page</title>"},
		{"GET", "/index.html", 200, "text/html; charset=utf-8", "no-store", "<title>page</title>"},
		{"HEAD", "/", 200, "text/html; charset=utf-8", "no-store", ""},
		{"GET", "/assets/app-1.js", 200, "text/javascript; charset=utf-8", "no-cache", "console.log"},
		{"GET", "/assets/app-1.css", 200, "text/css; charset=utf-8", "no-cache", "body{}"},
		{"GET", "/assets/", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/.secret", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/assets/.hidden", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/assets/../index.html", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/assets/%2e%2e/index.html", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/api/ping", 200, "application/json", "no-store", `{"ok":true}`},
		{"HEAD", "/api/ping", 200, "application/json", "no-store", ""},
		{"GET", "/favicon.ico", 204, "", "no-store", ""},
		{"GET", "/api/nope", 404, "application/json", "no-store", `{"error":"not found"}`},
		{"GET", "/nope.txt", 404, "application/json", "no-store", `{"error":"not found"}`},
		{"GET", "/video/v6/extra/x", 404, "application/json", "no-store", `{"error":"not found"}`},
		{"GET", "/video/v99", 404, "application/json", "no-store", `{"error":"no such version"}`},
		{"GET", "/video/v8", 404, "application/json", "no-store", `{"error":"no such version"}`}, // still rendering
		{"PUT", "/api/ping", 404, "application/json", "no-store", `{"error":"not found"}`},
		{"DELETE", "/video", 404, "application/json", "no-store", `{"error":"not found"}`},
		{"OPTIONS", "/api/session/" + take + "/data", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/api/session/" + take + "/data", 404, "application/json", "no-store", `"not found"`},
		{"GET", "/api/history?v=v99", 404, "application/json", "no-store", `{"error":"no such version"}`},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			r := h.do(tc.method, tc.path, nil, nil)
			if r.code != tc.code {
				t.Fatalf("code = %d, want %d (%s)", r.code, tc.code, r.body)
			}
			if got := r.header.Get("Content-Type"); tc.ctype != "" && got != tc.ctype {
				t.Errorf("Content-Type = %q, want %q", got, tc.ctype)
			}
			if got := r.header.Get("Cache-Control"); got != tc.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tc.cache)
			}
			if !bytes.Contains(r.body, []byte(tc.body)) {
				t.Errorf("body = %q, want it to contain %q", r.body, tc.body)
			}
			if tc.method == "HEAD" && len(r.body) != 0 {
				t.Errorf("HEAD sent a body")
			}
		})
	}
}

func TestPageNotBuilt(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video})
	r := h.get("/", nil)
	if r.code != 200 || !bytes.Contains(r.body, []byte("make build")) {
		t.Errorf("not-built page: %d %q", r.code, r.body)
	}
	if r := h.get("/assets/app.js", nil); r.code != 404 {
		t.Errorf("static without a page: %d", r.code)
	}
}

func TestVideoFiles(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video})
	for _, tc := range []struct {
		path, rel, ctype string
	}{
		{"/video", "v6/clip_v6.mp4", "video/mp4"},
		{"/video/", "v6/clip_v6.mp4", "video/mp4"},
		{"//video//v6/", "v6/clip_v6.mp4", "video/mp4"},
		{"/video/v5", "v5/clip_v5.mp4", "video/mp4"},
		{"/video/v7", "v7/clip_v7.mov", "video/quicktime"},
	} {
		r := h.get(tc.path, nil)
		if r.code != 200 || !bytes.Equal(r.body, f.files[tc.rel]) {
			t.Errorf("%s: %d, %d bytes", tc.path, r.code, len(r.body))
			continue
		}
		for k, want := range map[string]string{
			"Content-Type": tc.ctype, "Accept-Ranges": "bytes", "Cache-Control": "no-cache",
			"Content-Length": strconv.Itoa(len(f.files[tc.rel])),
		} {
			if got := r.header.Get(k); got != want {
				t.Errorf("%s: %s = %q, want %q", tc.path, k, got, want)
			}
		}
	}
	// A version file that vanished after the scan.
	if err := os.Remove(filepath.Join(f.root, "v5", "clip_v5.mp4")); err != nil {
		t.Fatal(err)
	}
	if r := h.get("/video/v5", nil); r.code != 404 || !bytes.Contains(r.body, []byte(`"missing"`)) {
		t.Errorf("vanished file: %d %s", r.code, r.body)
	}
}

func TestRanges(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video})
	data := f.files["v6/clip_v6.mp4"]
	size := len(data)
	for _, tc := range []struct {
		rng        string
		code       int
		start, end int // inclusive; for 416 unused
	}{
		{"", 200, 0, size - 1},
		{"bytes=0-99", 206, 0, 99},
		{"bytes=100-", 206, 100, size - 1},
		{"bytes=-100", 206, size - 100, size - 1},
		{"bytes=0-0", 206, 0, 0},
		{" bytes=10-19 ", 206, 10, 19},
		{"bytes=990-5000", 206, 990, size - 1}, // end clamped
		{"bytes=-5000", 206, 0, size - 1},      // suffix longer than the file
		{"bytes=999-", 206, 999, 999},          // last byte
		{"bytes=1000-", 416, 0, 0},             // starts at the end
		{"bytes=5-2", 416, 0, 0},               // reversed
		{"bytes=-0", 416, 0, 0},                // empty suffix
		{"bytes=99999999999999999999-", 416, 0, 0},
		{"bytes=0-99999999999999999999", 206, 0, size - 1},
		{"bytes=0-1,5-6", 200, 0, size - 1}, // multiple ranges: ignored
		{"bytes=-", 200, 0, size - 1},       // malformed: ignored
		{"items=0-1", 200, 0, size - 1},
		{"bytes=a-b", 200, 0, size - 1},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			t.Run(method+" "+tc.rng, func(t *testing.T) {
				var hdr map[string]string
				if tc.rng != "" {
					hdr = map[string]string{"Range": tc.rng}
				}
				r := h.do(method, "/video/v6", nil, hdr)
				if r.code != tc.code {
					t.Fatalf("code = %d, want %d", r.code, tc.code)
				}
				if tc.code == 416 {
					if got := r.header.Get("Content-Range"); got != fmt.Sprintf("bytes */%d", size) {
						t.Errorf("Content-Range = %q", got)
					}
					if len(r.body) != 0 {
						t.Errorf("416 with a body")
					}
					return
				}
				n := tc.end - tc.start + 1
				if got := r.header.Get("Content-Length"); got != strconv.Itoa(n) {
					t.Errorf("Content-Length = %s, want %d", got, n)
				}
				wantCR := ""
				if tc.code == 206 {
					wantCR = fmt.Sprintf("bytes %d-%d/%d", tc.start, tc.end, size)
				}
				if got := r.header.Get("Content-Range"); got != wantCR {
					t.Errorf("Content-Range = %q, want %q", got, wantCR)
				}
				if r.header.Get("Accept-Ranges") != "bytes" {
					t.Errorf("no Accept-Ranges")
				}
				if method == "HEAD" {
					if len(r.body) != 0 {
						t.Errorf("HEAD sent %d bytes", len(r.body))
					}
				} else if !bytes.Equal(r.body, data[tc.start:tc.end+1]) {
					t.Errorf("body is %d bytes, not bytes %d-%d", len(r.body), tc.start, tc.end)
				}
			})
		}
	}
}

func TestEmptyFileRange(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "empty.mp4")
	if err := os.WriteFile(video, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Video: video})
	if r := h.get("/video", nil); r.code != 200 || r.header.Get("Content-Length") != "0" {
		t.Errorf("empty file: %d %v", r.code, r.header)
	}
	if r := h.get("/video", map[string]string{"Range": "bytes=0-"}); r.code != 416 {
		t.Errorf("range on an empty file: %d", r.code)
	}
}

func TestPlaylistFiles(t *testing.T) {
	testmedia.Need(t)
	root := t.TempDir()
	testmedia.Video(t, filepath.Join(root, "v6", "clip_v6.mp4"), 1)
	hlsDir := filepath.Join(root, "v9")
	testmedia.HLS(t, hlsDir, "clip_v9.m3u8", 2)
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(hlsDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hlsDir, ".hidden"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := start(t, Options{Video: filepath.Join(root, "v6", "clip_v6.mp4"), Prober: probe.New(nil)})

	r := h.get("/video/v9/clip_v9.m3u8", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "application/vnd.apple.mpegurl" || !bytes.Contains(r.body, []byte("seg_000.ts")) {
		t.Fatalf("playlist: %d %q %q", r.code, r.header.Get("Content-Type"), r.body)
	}
	r = h.get("/video/v9/seg_000.ts", map[string]string{"Range": "bytes=0-187"})
	if r.code != 206 || r.header.Get("Content-Type") != "video/mp2t" || len(r.body) != 188 || r.body[0] != 0x47 {
		t.Errorf("segment: %d %q %d bytes", r.code, r.header.Get("Content-Type"), len(r.body))
	}
	if r := h.get("/video/v9", nil); r.code != 200 || r.header.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Errorf("/video/v9: %d %q", r.code, r.header.Get("Content-Type"))
	}
	for _, p := range []string{
		"/video/v9/..%2Fsecret.txt", "/video/v9/%2E%2E", "/video/v9/../secret.txt", "/video/v9/sub",
		"/video/v9/sub/x.ts", "/video/v9/.hidden", "/video/v9/..%5Csecret.txt", "/video/v9/C:secret.txt",
		"/video/v9/nope.ts", "/video/v6/clip_v6.mp4", "/video/v9/%00",
	} {
		if r := h.get(p, nil); r.code != 404 {
			t.Errorf("%s: %d %q", p, r.code, r.body)
		}
	}
	if r := h.get("/video/v1/clip_v9.m3u8", nil); r.code != 404 || !bytes.Contains(r.body, []byte("no such version")) {
		t.Errorf("unknown label: %d %s", r.code, r.body)
	}
}

func TestInfoAndHistory(t *testing.T) {
	f := newFixture(t)
	writeTake(t, filepath.Join(f.root, "v6", "clip_v6.review", take), `[{"video_t":2,"color":"red","points":[[0.1,0.2]]}]`)
	h := start(t, Options{Video: f.video})
	r := h.get("/api/info", nil)
	if r.code != 200 || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("info: %d", r.code)
	}
	// Key order matches the legacy server: the current version's fields, then the rest; then role (spec/team.md).
	wantKeys := []string{"label", "n", "path", "name", "current", "duration", "fps", "width", "height",
		"versions", "stem", "review_dir", "sessions", "history", "role"}
	if got := keyOrder(t, r.body); strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("keys = %v\nwant %v", got, wantKeys)
	}
	m := r.json(t)
	if m["label"] != "v6" || m["current"] != true || m["stem"] != "clip_v6" || m["role"] != "owner" ||
		m["review_dir"] != filepath.Join(f.root, "v6", "clip_v6.review") || m["duration"] != 10.0 {
		t.Errorf("info = %v", m)
	}
	if !bytes.Contains(r.body, []byte(`"duration":10.0,"fps":30.0`)) {
		t.Errorf("numbers not formatted like Python: %s", r.body)
	}
	var labels []string
	for _, v := range m["versions"].([]any) {
		labels = append(labels, v.(map[string]any)["label"].(string))
	}
	if strings.Join(labels, " ") != "v5 v6 v7" {
		t.Errorf("versions = %v", labels)
	}
	if s := m["sessions"].([]any); len(s) != 1 || s[0].(map[string]any)["strokes"] != 1.0 {
		t.Errorf("sessions = %v", s)
	}
	if hs := m["history"].([]any); len(hs) != 1 {
		t.Errorf("history = %v", hs)
	}

	// /api/info re-scans: a finished render shows up, without restarting.
	if err := os.WriteFile(filepath.Join(f.root, "v8", "clip_v8.mp4"), []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := h.get("/video/v8", nil); r.code != 404 {
		t.Errorf("v8 served before a rescan: %d", r.code)
	}
	if got := len(h.get("/api/info", nil).json(t)["versions"].([]any)); got != 4 {
		t.Errorf("after the render finished: %d versions", got)
	}
	if r := h.get("/video/v8", nil); r.code != 200 {
		t.Errorf("v8 after the rescan: %d", r.code)
	}

	for _, q := range []string{"?v=v6", "", "?v=", "?v=v5&v=v6"} {
		hr := h.get("/api/history"+q, nil).json(t)
		if hr["label"] != "v6" || len(hr["history"].([]any)) != 1 {
			t.Errorf("history%s = %v", q, hr)
		}
	}
	if hr := h.get("/api/history?v=v5", nil).json(t); hr["label"] != "v5" || len(hr["history"].([]any)) != 0 {
		t.Errorf("history v5 = %v", hr)
	}
}

func writeTake(t *testing.T, dir, strokes string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "strokes.json"), []byte(strokes), 0o600); err != nil {
		t.Fatal(err)
	}
}

// keyOrder returns the top-level keys of a JSON object in document order.
func keyOrder(t *testing.T, b []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestPostRules(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video, MaxBody: 64, Author: "Tester"})
	origin := fmt.Sprintf("http://127.0.0.1:%d", h.srv.opts.Port)
	local := fmt.Sprintf("http://localhost:%d", h.srv.opts.Port)
	data := "/api/session/" + take + "/data?v=v6"
	audio := "/api/session/" + take + "/audio?v=v6&name=seg_01.webm&offset=0"
	const bad = "2026-10-29_20-48-09" // a take only the failing requests use: its folder must never appear
	for _, tc := range []struct {
		name, path, body string
		hdr              map[string]string
		code             int
		want             string
	}{
		{"no origin", data, `{}`, nil, 200, `"ok":true`},
		{"127.0.0.1 origin", data, `{}`, map[string]string{"Origin": origin}, 200, `"ok":true`},
		{"localhost origin", data, `{}`, map[string]string{"Origin": local}, 200, `"ok":true`},
		{"other port", data, `{}`, map[string]string{"Origin": "http://127.0.0.1:1"}, 403, `{"error":"bad origin"}`},
		{"https", data, `{}`, map[string]string{"Origin": "https://127.0.0.1:" + strconv.Itoa(h.srv.opts.Port)}, 403, "bad origin"},
		{"null origin", data, `{}`, map[string]string{"Origin": "null"}, 403, "bad origin"},
		{"empty origin", data, `{}`, map[string]string{"Origin": ""}, 403, "bad origin"},
		{"evil", data, `{}`, map[string]string{"Origin": "http://evil.example"}, 403, "bad origin"},
		{"trailing slash", data, `{}`, map[string]string{"Origin": origin + "/"}, 403, "bad origin"},
		{"origin before path", "/nope", `{}`, map[string]string{"Origin": "http://evil.example"}, 403, "bad origin"},
		{"body at the limit", audio, strings.Repeat("a", 64), nil, 200, `{"size":64}`},
		{"body over the limit", audio, strings.Repeat("a", 65), nil, 413, `{"error":"body too large"}`},
		{"over the limit, wrong path", "/nope", strings.Repeat("a", 65), nil, 413, "body too large"},
		{"bad take id", "/api/session/2026-10-29/data?v=v6", `{}`, nil, 404, `{"error":"not found"}`},
		{"take id with traversal", "/api/session/..%2F" + take + "/data?v=v6", `{}`, nil, 404, "not found"},
		{"short path", "/api/session/" + take, `{}`, nil, 404, "not found"},
		{"wrong prefix", "/api/sessions/" + take + "/data?v=v6", `{}`, nil, 404, "not found"},
		{"unknown kind", "/api/session/" + bad + "/notes?v=v6", `{}`, nil, 404, "not found"},
		{"unknown version", "/api/session/" + bad + "/data?v=v99", `{}`, nil, 400, `{"error":"bad version"}`},
		{"rendering version", "/api/session/" + bad + "/data?v=v8", `{}`, nil, 400, "bad version"},
		{"bad version before bad kind", "/api/session/" + bad + "/x?v=v99", `{}`, nil, 400, "bad version"},
		{"bad json", "/api/session/" + bad + "/data?v=v6", `{"session":`, nil, 400, `{"error":"bad json"}`},
		{"json not an object", "/api/session/" + bad + "/data?v=v6", `[1,2]`, nil, 400, "bad json"},
		{"bad name", "/api/session/" + bad + "/audio?v=v6&name=../x.webm&offset=0", "x", nil, 400, `{"error":"bad name"}`},
		{"no name", "/api/session/" + bad + "/audio?v=v6&offset=0", "x", nil, 400, "bad name"},
		{"bad offset", "/api/session/" + bad + "/audio?v=v6&name=seg_02.webm&offset=abc", "x", nil, 400, `{"error":"bad offset"}`},
		{"float offset", "/api/session/" + bad + "/audio?v=v6&name=seg_02.webm&offset=1.5", "x", nil, 400, "bad offset"},
		{"past end on a new file", "/api/session/" + bad + "/audio?v=v6&name=seg_02.webm&offset=3", "x", nil, 409, `{"size":0,"error":"offset past end"}`},
		{"blank offset is 0", "/api/session/" + take + "/audio?v=v6&name=seg_03.webm&offset=", "xyz", nil, 200, `{"size":3}`},
		{"no offset is 0", "/api/session/" + take + "/audio?v=v6&name=seg_04.webm", "xy", nil, 200, `{"size":2}`},
		{"huge offset", "/api/session/" + take + "/audio?v=v6&name=seg_04.webm&offset=99999999999999999999", "x", nil, 409, `{"size":2,"error":"offset past end"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := h.post(tc.path, tc.body, tc.hdr)
			if r.code != tc.code || !bytes.Contains(r.body, []byte(tc.want)) {
				t.Errorf("%d %s, want %d containing %s", r.code, r.body, tc.code, tc.want)
			}
			if r.header.Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q", r.header.Get("Cache-Control"))
			}
		})
	}
	// A chunked body (no Content-Length) over the limit is cut off with 413 too.
	req, err := http.NewRequest(http.MethodPost, h.url+audio, io.MultiReader(strings.NewReader(strings.Repeat("b", 100))))
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = -1
	res, err := h.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked over the limit: %d", res.StatusCode)
	}
	// Rejected requests (409 included) leave no folders behind; the legacy server created the take folder before
	// some of its checks.
	if _, err := os.Stat(filepath.Join(f.root, "v6", "clip_v6.review", bad)); !os.IsNotExist(err) {
		t.Errorf("rejected requests created their take folder (%v)", err)
	}
	for _, v := range []string{"v5", "v7", "v8"} {
		if matches, _ := filepath.Glob(filepath.Join(f.root, v, "*.review")); len(matches) != 0 {
			t.Errorf("%s got a review folder: %v", v, matches)
		}
	}
}

func TestPostWritesTakes(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video, Author: "Tester"})
	dir := filepath.Join(f.root, "v6", "clip_v6.review", take)

	body := `{"session":{"format":3,"version":"v6","saved":"x"},"segments":[{"id":"S1"}],"events":[],"strokes":[],"voice":{"bin":0.25,"bins":[]}}`
	r := h.post("/api/session/"+take+"/data?v=v6", body, nil)
	if r.code != 200 {
		t.Fatalf("data: %d %s", r.code, r.body)
	}
	if got := r.json(t); got["ok"] != true || got["dir"] != dir {
		t.Errorf("data response = %v", got)
	}
	var meta map[string]any
	b, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	video := meta["video"].(map[string]any)
	if meta["version"] != "v6" || meta["author"].(map[string]any)["name"] != "Tester" || video["name"] != "clip_v6.mp4" ||
		video["path"] != f.video || video["fps"] != 30.0 || len(meta["versions"].([]any)) != 3 {
		t.Errorf("session.json = %s", b)
	}

	// A take on another version goes next to that version's file.
	if r := h.post("/api/session/"+take+"/data?v=v5", `{}`, nil); r.code != 200 {
		t.Fatalf("v5 data: %d", r.code)
	}
	if _, err := os.Stat(filepath.Join(f.root, "v5", "clip_v5.review", take, "session.json")); err != nil {
		t.Error(err)
	}
	// No ?v= means the current version, as in the legacy server.
	if r := h.post("/api/session/2026-10-29_20-48-02/data", `{}`, nil); r.code != 200 || !bytes.Contains(r.body, []byte("clip_v6.review")) {
		t.Errorf("no v: %d %s", r.code, r.body)
	}

	// Audio: append, resume after a 409, rewrite a tail.
	audio := func(offset, chunk string) resp {
		return h.post("/api/session/"+take+"/audio?v=v6&name=seg_01.webm&offset="+offset, chunk, nil)
	}
	for i, s := range []struct {
		offset, chunk string
		code          int
		want          string
	}{
		{"0", "hello", 200, `{"size":5}`},
		{"5", " world", 200, `{"size":11}`},
		{"20", "!", 409, `{"size":11,"error":"offset past end"}`},
		{"-1", "!", 409, `{"size":11,"error":"offset past end"}`},
		{"11", "!", 200, `{"size":12}`},
		{"5", "!", 200, `{"size":6}`},
	} {
		r := audio(s.offset, s.chunk)
		if r.code != s.code || string(r.body) != s.want {
			t.Errorf("step %d: %d %s, want %d %s", i, r.code, r.body, s.code, s.want)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "seg_01.webm")); string(got) != "hello!" {
		t.Errorf("seg_01.webm = %q", got)
	}
}

func TestConcurrentWritesAreSerialized(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video})
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"segments":[{"id":"S%d","pad":%q}]}`, i, strings.Repeat("x", 2000))
			req, _ := http.NewRequest(http.MethodPost, h.url+"/api/session/"+take+"/data?v=v6", strings.NewReader(body))
			res, err := h.ts.Client().Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusOK {
				t.Errorf("save %d: %d", i, res.StatusCode)
			}
		}()
	}
	wg.Wait()
	b, err := os.ReadFile(filepath.Join(f.root, "v6", "clip_v6.review", take, "segments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var segs []map[string]any
	if err := json.Unmarshal(b, &segs); err != nil || len(segs) != 1 {
		t.Errorf("segments.json after concurrent saves: %v %s", err, b[:min(80, len(b))])
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestIdleExit(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name     string
		idle     time.Duration
		contact  bool
		advance  time.Duration
		wantExit bool
	}{
		{"no page yet: never", time.Minute, false, time.Hour, false},
		{"page gone long enough", time.Minute, true, 61 * time.Second, true},
		{"page still recent", time.Minute, true, 59 * time.Second, false},
		{"idle exit off", 0, true, 24 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			srv := New(Options{Video: f.video, Prober: fakeProber{}, Port: ln.Addr().(*net.TCPAddr).Port,
				IdleExit: tc.idle, CheckEvery: 2 * time.Millisecond, Clock: clock})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type result struct {
				idle bool
				err  error
			}
			done := make(chan result, 1)
			go func() {
				idle, err := srv.Serve(ctx, ln)
				done <- result{idle, err}
			}()
			if tc.contact {
				res, err := http.Get("http://" + ln.Addr().String() + "/api/ping")
				if err != nil {
					t.Fatal(err)
				}
				_ = res.Body.Close()
			}
			clock.Add(tc.advance)
			select {
			case r := <-done:
				if !tc.wantExit || !r.idle || r.err != nil {
					t.Fatalf("exited: idle=%v err=%v, want exit %v", r.idle, r.err, tc.wantExit)
				}
			case <-time.After(150 * time.Millisecond):
				if tc.wantExit {
					t.Fatal("did not exit")
				}
				cancel()
				if r := <-done; r.idle || r.err != nil {
					t.Fatalf("after cancel: idle=%v err=%v", r.idle, r.err)
				}
			}
		})
	}
}

func TestVerboseLog(t *testing.T) {
	f := newFixture(t)
	var log bytes.Buffer
	var mu sync.Mutex
	h := start(t, Options{Video: f.video, Verbose: true, Log: lockedWriter{&mu, &log}})
	h.get("/api/ping", nil)
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(log.String(), `retake: "GET /api/ping HTTP/1.1" 200 -`) {
		t.Errorf("log = %q", log.String())
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestParseOffset(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
		ok   bool
	}{
		{"", 0, true}, {"0", 0, true}, {"42", 42, true}, {" 42 ", 42, true}, {"+7", 7, true}, {"-3", -3, true},
		{"1.5", 0, false}, {"x", 0, false}, {"99999999999999999999", 1<<63 - 1, true},
		{"-99999999999999999999", -1, true},
	} {
		got, ok := parseOffset(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseOffset(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// A DNS-rebinding page reaches 127.0.0.1 under its own hostname; every route must refuse it, before counting as
// contact for the idle timer.
func TestHostHeaderRebinding(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video})
	port := h.ts.Listener.Addr().(*net.TCPAddr).Port
	for _, tc := range []struct {
		host string
		want int
	}{
		{fmt.Sprintf("127.0.0.1:%d", port), http.StatusOK},
		{fmt.Sprintf("localhost:%d", port), http.StatusOK},
		{fmt.Sprintf("LOCALHOST:%d", port), http.StatusOK},
		{fmt.Sprintf("evil.example:%d", port), http.StatusForbidden},
		{"127.0.0.1", http.StatusForbidden},
		{fmt.Sprintf("127.0.0.1:%d", port+1), http.StatusForbidden},
	} {
		for _, path := range []string{"/api/ping", "/api/info", "/video"} {
			req, err := http.NewRequest(http.MethodGet, h.url+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tc.host
			res, err := h.ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != tc.want {
				t.Errorf("Host %q %s: got %d, want %d", tc.host, path, res.StatusCode, tc.want)
			}
		}
	}
}

// --allow-host: a tunnel's hostname is answered on every route (with a token, spec/team.md), and https://<host> may
// write; other hosts still can't.
func TestAllowHostForTunnels(t *testing.T) {
	f := newFixture(t)
	h := start(t, Options{Video: f.video, AllowHosts: []string{"Abc.TryCloudflare.com"}})
	tok := makeToken(t, h, "reviewer", "", 0)
	do := func(method, path, host, origin string) int {
		req, err := http.NewRequest(method, h.url+path, strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+tok)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := h.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if got := do(http.MethodGet, "/api/info", "abc.trycloudflare.com", ""); got != http.StatusOK {
		t.Errorf("tunnel host GET: %d", got)
	}
	// 404 (bad take id) means the origin check passed without creating anything.
	if got := do(http.MethodPost, "/api/session/nope/data", "abc.trycloudflare.com", "https://abc.trycloudflare.com"); got != http.StatusNotFound {
		t.Errorf("tunnel origin POST: %d, want 404", got)
	}
	if got := do(http.MethodPost, "/api/session/nope/data", "abc.trycloudflare.com", "http://abc.trycloudflare.com"); got != http.StatusForbidden {
		t.Errorf("plain-http tunnel origin POST: %d, want 403", got)
	}
	if got := do(http.MethodGet, "/api/info", "evil.trycloudflare.com", ""); got != http.StatusForbidden {
		t.Errorf("other host: %d, want 403", got)
	}
}
