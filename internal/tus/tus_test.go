package tus

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const base = "/p/demo/api/uploads"

// clock is a settable time for the expiry tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t    *testing.T
	h    *Handler
	ts   *httptest.Server
	clk  *clock
	busy *atomic.Int64 // requests the server is still handling
}

// settle waits for the requests the server is still handling: a client that gave up on a cut PATCH returns before the
// server has written what reached it.
func (h *harness) settle() {
	for h.busy.Load() > 0 {
		time.Sleep(5 * time.Millisecond)
	}
}

// newHarness serves a Handler at base. The X-Owner header picks the owner (default "demo"), standing in for the
// hub's project routing.
func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	h := &Handler{Dir: filepath.Join(t.TempDir(), "uploads"), Now: clk.now}
	busy := &atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		busy.Add(1)
		defer busy.Add(-1)
		rest, ok := strings.CutPrefix(r.URL.Path, base)
		if !ok || rest != "" && !strings.HasPrefix(rest, "/") {
			http.NotFound(w, r)
			return
		}
		owner := r.Header.Get("X-Owner")
		if owner == "" {
			owner = "demo"
		}
		h.Serve(w, r, base, owner, strings.TrimPrefix(rest, "/"))
	}))
	t.Cleanup(ts.Close)
	return &harness{t: t, h: h, ts: ts, clk: clk, busy: busy}
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func payload(n int) []byte {
	b := make([]byte, n)
	r := rand.New(rand.NewPCG(1, uint64(n)))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	return b
}

type res struct {
	code int
	hdr  http.Header
	body string
}

func (r res) json(t *testing.T) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal([]byte(r.body), &m); err != nil {
		t.Fatalf("not JSON (%d): %q", r.code, r.body)
	}
	return m
}

// do sends one request; hdr values of "" are left out.
func (h *harness) do(method, path string, body io.Reader, hdr map[string]string) res {
	h.t.Helper()
	req, err := http.NewRequest(method, h.ts.URL+path, body)
	if err != nil {
		h.t.Fatal(err)
	}
	for k, v := range hdr {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	r, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	return res{r.StatusCode, r.Header, string(b)}
}

func tusHdr(extra map[string]string) map[string]string {
	m := map[string]string{"Tus-Resumable": Version}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// create makes an upload for data named name and returns its path (base + "/" + id).
func (h *harness) create(name string, data []byte) string {
	h.t.Helper()
	r := h.do(http.MethodPost, base, nil, tusHdr(map[string]string{
		"Upload-Length":   strconv.Itoa(len(data)),
		"Upload-Metadata": EncodeMetadata(map[string]string{"filename": name, "sha256": sum(data)}),
	}))
	if r.code != http.StatusCreated {
		h.t.Fatalf("POST: %d %s", r.code, r.body)
	}
	loc := r.hdr.Get("Location")
	if !strings.HasPrefix(loc, base+"/up_") || !ValidID(strings.TrimPrefix(loc, base+"/")) {
		h.t.Fatalf("Location %q", loc)
	}
	if r.hdr.Get("Tus-Resumable") != Version || r.hdr.Get("Upload-Expires") == "" {
		h.t.Fatalf("headers %v", r.hdr)
	}
	return loc
}

func (h *harness) patch(path string, offset int, body []byte) res {
	h.t.Helper()
	return h.do(http.MethodPatch, path, bytes.NewReader(body), tusHdr(map[string]string{
		"Content-Type": OffsetType, "Upload-Offset": strconv.Itoa(offset)}))
}

func (h *harness) offset(path string) int64 {
	h.t.Helper()
	r := h.do(http.MethodHead, path, nil, tusHdr(nil))
	if r.code != http.StatusOK {
		h.t.Fatalf("HEAD %s: %d", path, r.code)
	}
	n, err := strconv.ParseInt(r.hdr.Get("Upload-Offset"), 10, 64)
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

func idOf(path string) string { return strings.TrimPrefix(path, base+"/") }

func TestOptions(t *testing.T) {
	h := newHarness(t)
	h.h.MaxSize = 1234
	for _, p := range []string{base, base + "/up_0123456789abcdefghijkl"} {
		r := h.do(http.MethodOptions, p, nil, nil)
		if r.code != http.StatusNoContent || r.hdr.Get("Tus-Version") != "1.0.0" ||
			r.hdr.Get("Tus-Extension") != "creation,termination,expiration" || r.hdr.Get("Tus-Max-Size") != "1234" ||
			r.hdr.Get("Tus-Resumable") != "1.0.0" {
			t.Fatalf("OPTIONS %s: %d %v", p, r.code, r.hdr)
		}
	}
}

func TestNeedsTusResumable(t *testing.T) {
	h := newHarness(t)
	for _, v := range []string{"", "0.2.2"} {
		r := h.do(http.MethodPost, base, nil, map[string]string{"Tus-Resumable": v, "Upload-Length": "1"})
		if r.code != http.StatusPreconditionFailed || r.hdr.Get("Tus-Version") != Version ||
			r.hdr.Get("Tus-Resumable") != Version {
			t.Fatalf("Tus-Resumable %q: %d %v", v, r.code, r.hdr)
		}
	}
	if r := h.do(http.MethodGet, base, nil, tusHdr(nil)); r.code != http.StatusMethodNotAllowed ||
		r.hdr.Get("Allow") != "OPTIONS, POST" {
		t.Fatalf("GET collection: %d %v", r.code, r.hdr)
	}
	if r := h.do(http.MethodPost, base+"/up_0123456789abcdefghijkl", nil, tusHdr(nil)); r.code != http.StatusMethodNotAllowed ||
		r.hdr.Get("Allow") != "OPTIONS, HEAD, PATCH, DELETE" {
		t.Fatalf("POST upload: %d %v", r.code, r.hdr)
	}
}

func TestFullFlow(t *testing.T) {
	h := newHarness(t)
	data := payload(300_000)
	p := h.create("Clip_V3.MP4", data)
	r := h.do(http.MethodHead, p, nil, tusHdr(nil))
	meta, err := ParseMetadata(r.hdr.Get("Upload-Metadata"))
	if err != nil || r.code != http.StatusOK || r.hdr.Get("Upload-Offset") != "0" ||
		r.hdr.Get("Upload-Length") != "300000" || r.hdr.Get("Cache-Control") != "no-store" ||
		meta["filename"] != "Clip_V3.MP4" || meta["sha256"] != sum(data) || r.hdr.Get("Upload-Expires") == "" {
		t.Fatalf("HEAD: %d %v %v", r.code, r.hdr, err)
	}
	u, err := h.h.Lookup("demo", idOf(p))
	if err != nil || u.Complete || u.Offset != 0 || u.Size != 300000 || u.Filename != "Clip_V3.MP4" {
		t.Fatalf("Lookup before: %+v %v", u, err)
	}
	for _, part := range [][2]int{{0, 100_000}, {100_000, 250_000}, {250_000, 300_000}} {
		r := h.patch(p, part[0], data[part[0]:part[1]])
		if r.code != http.StatusNoContent || r.hdr.Get("Upload-Offset") != strconv.Itoa(part[1]) {
			t.Fatalf("PATCH %v: %d %v %s", part, r.code, r.hdr, r.body)
		}
	}
	u, err = h.h.Lookup("demo", idOf(p))
	if err != nil || !u.Complete || u.Offset != 300000 || u.SHA256 != sum(data) {
		t.Fatalf("Lookup after: %+v %v", u, err)
	}
	if filepath.Base(h.h.DataPath(u)) != u.ID+".mp4" {
		t.Fatalf("data path %s", h.h.DataPath(u))
	}
	got, err := os.ReadFile(h.h.DataPath(u))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("data differs: %v", err)
	}
	// A PATCH at the end of a complete upload is a no-op; one elsewhere is a conflict.
	if r := h.patch(p, 300000, nil); r.code != http.StatusNoContent || r.hdr.Get("Upload-Offset") != "300000" {
		t.Fatalf("PATCH complete: %d", r.code)
	}
	if r := h.patch(p, 5, []byte("x")); r.code != http.StatusConflict {
		t.Fatalf("PATCH complete elsewhere: %d", r.code)
	}
}

// rawPatch sends a PATCH announcing want bytes over a raw connection, sends only body, then closes it: a connection
// that breaks mid-upload.
func rawPatch(t *testing.T, h *harness, path string, offset, want int, body []byte) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(h.ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(conn)
	_, _ = fmt.Fprintf(w, "PATCH %s HTTP/1.1\r\nHost: x\r\nTus-Resumable: 1.0.0\r\nContent-Type: %s\r\n"+
		"Upload-Offset: %d\r\nContent-Length: %d\r\n\r\n", path, OffsetType, offset, want)
	_, _ = w.Write(body)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	_ = conn.Close()
}

func TestResumeAfterABrokenConnection(t *testing.T) {
	h := newHarness(t)
	data := payload(200_000)
	p := h.create("a.mov", data)
	rawPatch(t, h, p, 0, len(data), data[:70_000])
	// The server keeps what arrived. HEAD waits for the broken PATCH to finish (one request per upload at a time).
	deadline := time.Now().Add(5 * time.Second)
	off := h.offset(p)
	for off != 70_000 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		off = h.offset(p)
	}
	if off != 70_000 {
		t.Fatalf("offset after the break: %d", off)
	}
	if r := h.patch(p, 0, data); r.code != http.StatusConflict || r.hdr.Get("Upload-Offset") != "70000" {
		t.Fatalf("PATCH at a stale offset: %d %v", r.code, r.hdr)
	}
	if r := h.patch(p, 70_000, data[70_000:]); r.code != http.StatusNoContent || r.hdr.Get("Upload-Offset") != "200000" {
		t.Fatalf("resumed PATCH: %d %s", r.code, r.body)
	}
	u, err := h.h.Lookup("demo", idOf(p))
	got, _ := os.ReadFile(h.h.DataPath(u))
	if err != nil || !u.Complete || !bytes.Equal(got, data) {
		t.Fatalf("after resume: %+v %v", u, err)
	}
}

func TestSHAMismatch(t *testing.T) {
	h := newHarness(t)
	data := payload(50_000)
	r := h.do(http.MethodPost, base, nil, tusHdr(map[string]string{"Upload-Length": "50000",
		"Upload-Metadata": EncodeMetadata(map[string]string{"filename": "a.mp4", "sha256": sum([]byte("other"))})}))
	if r.code != http.StatusCreated {
		t.Fatal(r.code)
	}
	p := r.hdr.Get("Location")
	if r := h.patch(p, 0, data[:20_000]); r.code != http.StatusNoContent {
		t.Fatal(r.code)
	}
	r = h.patch(p, 20_000, data[20_000:])
	if r.code != StatusChecksumMismatch || r.hdr.Get("Upload-Offset") != "0" ||
		!strings.HasPrefix(r.json(t)["error"], "sha256 mismatch: got "+sum(data)) {
		t.Fatalf("mismatch: %d %v %s", r.code, r.hdr, r.body)
	}
	if off := h.offset(p); off != 0 {
		t.Fatalf("offset after a mismatch: %d", off)
	}
	u, err := h.h.Lookup("demo", idOf(p))
	if err != nil || u.Complete || u.Offset != 0 {
		t.Fatalf("Lookup: %+v %v", u, err)
	}
	if st, err := os.Stat(h.h.DataPath(u)); err != nil || st.Size() != 0 {
		t.Fatalf("data after a mismatch: %v %v", st, err)
	}
}

func TestPatchRefusals(t *testing.T) {
	h := newHarness(t)
	data := payload(1000)
	p := h.create("a.mkv", data)
	r := h.do(http.MethodPatch, p, bytes.NewReader(data), tusHdr(map[string]string{
		"Content-Type": "application/octet-stream", "Upload-Offset": "0"}))
	if r.code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type: %d", r.code)
	}
	if r := h.patch(p, 10, data[10:]); r.code != http.StatusConflict || r.hdr.Get("Upload-Offset") != "0" {
		t.Fatalf("offset: %d", r.code)
	}
	r = h.do(http.MethodPatch, p, bytes.NewReader(data), tusHdr(map[string]string{"Content-Type": OffsetType}))
	if r.code != http.StatusBadRequest {
		t.Fatalf("no Upload-Offset: %d", r.code)
	}
	// Too long: the bytes up to the length are kept (here the whole file, so it completes), nothing past it.
	r = h.patch(p, 0, append(append([]byte{}, data...), "past the end"...))
	if r.code != http.StatusRequestEntityTooLarge || r.hdr.Get("Upload-Offset") != "1000" {
		t.Fatalf("too long: %d %v", r.code, r.hdr)
	}
	u, err := h.h.Lookup("demo", idOf(p))
	if err != nil || !u.Complete {
		t.Fatalf("after too long: %+v %v", u, err)
	}
	if st, _ := os.Stat(h.h.DataPath(u)); st.Size() != 1000 {
		t.Fatalf("data size %d", st.Size())
	}
}

func TestCreateRefusals(t *testing.T) {
	h := newHarness(t)
	h.h.MaxSize = 100
	good := sum([]byte("x"))
	meta := func(m map[string]string) string { return EncodeMetadata(m) }
	cases := []struct {
		name string
		hdr  map[string]string
		code int
	}{
		{"too big", map[string]string{"Upload-Length": "101", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": good})}, 413},
		{"no length", map[string]string{"Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": good})}, 400},
		{"negative", map[string]string{"Upload-Length": "-1", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": good})}, 400},
		{"deferred", map[string]string{"Upload-Defer-Length": "1", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": good})}, 400},
		{"no filename", map[string]string{"Upload-Length": "1", "Upload-Metadata": meta(map[string]string{"sha256": good})}, 400},
		{"not a video", map[string]string{"Upload-Length": "1", "Upload-Metadata": meta(map[string]string{"filename": "a.txt", "sha256": good})}, 400},
		{"a folder", map[string]string{"Upload-Length": "1", "Upload-Metadata": meta(map[string]string{"filename": "../a.mp4", "sha256": good})}, 400},
		{"no sha", map[string]string{"Upload-Length": "1", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4"})}, 400},
		{"bad sha", map[string]string{"Upload-Length": "1", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": "abc"})}, 400},
		{"bad base64", map[string]string{"Upload-Length": "1", "Upload-Metadata": "filename !!!,sha256 " + good}, 400},
		{"empty, wrong sha", map[string]string{"Upload-Length": "0", "Upload-Metadata": meta(map[string]string{"filename": "a.mp4", "sha256": good})}, StatusChecksumMismatch},
	}
	for _, c := range cases {
		r := h.do(http.MethodPost, base, nil, tusHdr(c.hdr))
		if r.code != c.code || r.json(t)["error"] == "" {
			t.Errorf("%s: %d %s, want %d", c.name, r.code, r.body, c.code)
		}
	}
	if entries, _ := os.ReadDir(h.h.Dir); len(entries) != 0 {
		t.Fatalf("refused creations left %d files", len(entries))
	}
	// An empty file with the empty hash is complete at once; an upper-case hash is fine.
	r := h.do(http.MethodPost, base, nil, tusHdr(map[string]string{"Upload-Length": "0",
		"Upload-Metadata": meta(map[string]string{"filename": "e.webm", "sha256": strings.ToUpper(sum(nil))})}))
	if r.code != http.StatusCreated {
		t.Fatalf("empty: %d %s", r.code, r.body)
	}
	if u, err := h.h.Lookup("demo", idOf(r.hdr.Get("Location"))); err != nil || !u.Complete || u.SHA256 != sum(nil) {
		t.Fatalf("empty upload: %+v %v", u, err)
	}
}

func TestNotFound(t *testing.T) {
	h := newHarness(t)
	p := h.create("a.mp4", payload(10))
	other := map[string]string{"X-Owner": "other"}
	for _, m := range []string{http.MethodHead, http.MethodPatch, http.MethodDelete} {
		hdr := tusHdr(other)
		hdr["Content-Type"], hdr["Upload-Offset"] = OffsetType, "0"
		if r := h.do(m, p, nil, hdr); r.code != http.StatusNotFound {
			t.Errorf("%s by another owner: %d", m, r.code)
		}
		for _, bad := range []string{base + "/up_unknownunknownunknown1", base + "/..%2F..%2Fetc", base + "/up_x"} {
			if r := h.do(m, bad, nil, tusHdr(map[string]string{"Content-Type": OffsetType, "Upload-Offset": "0"})); r.code != http.StatusNotFound {
				t.Errorf("%s %s: %d", m, bad, r.code)
			}
		}
	}
	if _, err := h.h.Lookup("other", idOf(p)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup by another owner: %v", err)
	}
	if err := h.h.Remove("other", idOf(p)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Remove by another owner: %v", err)
	}
	if h.offset(p) != 0 {
		t.Fatal("the upload should still be there")
	}
}

func TestExpiryAndSweep(t *testing.T) {
	h := newHarness(t)
	data := payload(100)
	p := h.create("a.mp4", data)
	h.clk.add(23 * time.Hour)
	if r := h.patch(p, 0, data[:50]); r.code != http.StatusNoContent { // renews the expiry
		t.Fatal(r.code)
	}
	h.clk.add(23 * time.Hour)
	if h.offset(p) != 50 {
		t.Fatal("renewed upload gone")
	}
	if n, err := h.h.Sweep(); n != 0 || err != nil {
		t.Fatalf("sweep of live uploads: %d %v", n, err)
	}
	h.clk.add(2 * time.Hour)
	if r := h.do(http.MethodHead, p, nil, tusHdr(nil)); r.code != http.StatusNotFound {
		t.Fatalf("expired HEAD: %d", r.code)
	}
	if _, err := h.h.Lookup("demo", idOf(p)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired Lookup: %v", err)
	}
	// A complete upload whose data a push moved away is swept too; a fresh one stays.
	q := h.create("b.mp4", data)
	if r := h.patch(q, 0, data); r.code != http.StatusNoContent {
		t.Fatal(r.code)
	}
	u, _ := h.h.Lookup("demo", idOf(q))
	if err := os.Rename(h.h.DataPath(u), filepath.Join(t.TempDir(), "pushed.mp4")); err != nil {
		t.Fatal(err)
	}
	keep := h.create("c.mp4", data)
	n, err := h.h.Sweep()
	if err != nil || n != 2 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	entries, _ := os.ReadDir(h.h.Dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 || !strings.HasPrefix(names[0], idOf(keep)) || !strings.HasPrefix(names[1], idOf(keep)) {
		t.Fatalf("left after the sweep: %v", names)
	}
}

func TestDelete(t *testing.T) {
	h := newHarness(t)
	p := h.create("a.mp4", payload(10))
	if r := h.do(http.MethodDelete, p, nil, tusHdr(nil)); r.code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", r.code)
	}
	if r := h.do(http.MethodHead, p, nil, tusHdr(nil)); r.code != http.StatusNotFound {
		t.Fatalf("HEAD after DELETE: %d", r.code)
	}
	if entries, _ := os.ReadDir(h.h.Dir); len(entries) != 0 {
		t.Fatalf("DELETE left %d files", len(entries))
	}
	q := h.create("b.mp4", payload(10))
	if err := h.h.Remove("demo", idOf(q)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(h.h.Dir); len(entries) != 0 {
		t.Fatalf("Remove left %d files", len(entries))
	}
}

func TestUncheckedCompleteUploadIsCheckedOnLookup(t *testing.T) {
	h := newHarness(t)
	data := payload(64)
	p := h.create("a.mp4", data)
	u, _ := h.h.Lookup("demo", idOf(p))
	// The server stopped after the last byte was written but before the check.
	if err := os.WriteFile(h.h.DataPath(u), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if u, err := h.h.Lookup("demo", idOf(p)); err != nil || !u.Complete || u.Offset != 64 {
		t.Fatalf("Lookup: %+v %v", u, err)
	}
}

// --- the client

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "clip_v4.mp4")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// cutter is a RoundTripper that records requests and cuts chosen PATCH bodies short.
type cutter struct {
	next    http.RoundTripper
	mu      sync.Mutex
	posts   int
	offsets []int64           // Upload-Offset of every PATCH sent
	cut     func(i int) int64 // for the i-th PATCH: how many body bytes to send before failing; < 0 = all
}

func (c *cutter) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	if req.Method == http.MethodPost {
		c.posts++
	}
	i := -1
	if req.Method == http.MethodPatch {
		off, _ := strconv.ParseInt(req.Header.Get("Upload-Offset"), 10, 64)
		c.offsets = append(c.offsets, off)
		i = len(c.offsets) - 1
	}
	c.mu.Unlock()
	if i >= 0 && c.cut != nil {
		if n := c.cut(i); n >= 0 {
			req.Body = &failAfter{r: req.Body, left: n}
		}
	}
	return c.next.RoundTrip(req)
}

type failAfter struct {
	r    io.ReadCloser
	left int64
}

var errCut = errors.New("connection cut")

func (f *failAfter) Read(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errCut
	}
	if int64(len(p)) > f.left {
		p = p[:f.left]
	}
	n, err := f.r.Read(p)
	f.left -= int64(n)
	return n, err
}

func (f *failAfter) Close() error { return f.r.Close() }

func newClient(h *harness, c *cutter) *Client {
	c.next = h.ts.Client().Transport
	return &Client{HTTP: &http.Client{Transport: c}, Token: "rt_test", Backoff: func(int) time.Duration { return time.Millisecond }}
}

func TestClientUploads(t *testing.T) {
	h := newHarness(t)
	data := payload(2_500_000)
	path := writeFile(t, data)
	var created string
	var last atomic.Int64
	ct := &cutter{}
	c := newClient(h, ct)
	c.ChunkSize = 1 << 20
	c.Created = func(u string) { created = u }
	c.Progress = func(sent, total int64) {
		if total != int64(len(data)) {
			t.Errorf("total %d", total)
		}
		last.Store(sent)
	}
	got, err := c.Upload(context.Background(), h.ts.URL+base, path, map[string]string{"filename": "clip_v4.mp4",
		"sha256": sum(data)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != created || !strings.HasPrefix(got, h.ts.URL+base+"/up_") || last.Load() != int64(len(data)) {
		t.Fatalf("url %s, created %s, progress %d", got, created, last.Load())
	}
	if fmt.Sprint(ct.offsets) != "[0 1048576 2097152]" || ct.posts != 1 {
		t.Fatalf("PATCHes %v, POSTs %d", ct.offsets, ct.posts)
	}
	u, err := h.h.Lookup("demo", idOf(strings.TrimPrefix(got, h.ts.URL)))
	if err != nil || !u.Complete {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestClientResumesAfterACutConnection(t *testing.T) {
	h := newHarness(t)
	data := payload(1_000_000)
	path := writeFile(t, data)
	ct := &cutter{cut: func(i int) int64 {
		if i == 0 {
			return 300_000
		}
		return -1
	}}
	c := newClient(h, ct)
	got, err := c.Upload(context.Background(), h.ts.URL+base, path, map[string]string{"filename": "clip_v4.mp4",
		"sha256": sum(data)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ct.posts != 1 || len(ct.offsets) != 2 || ct.offsets[0] != 0 || ct.offsets[1] <= 0 || ct.offsets[1] > 300_000 {
		t.Fatalf("POSTs %d, PATCH offsets %v: want one POST and a resumed second PATCH", ct.posts, ct.offsets)
	}
	u, err := h.h.Lookup("demo", idOf(strings.TrimPrefix(got, h.ts.URL)))
	b, _ := os.ReadFile(h.h.DataPath(u))
	if err != nil || !u.Complete || !bytes.Equal(b, data) {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestClientResumesAcrossRuns(t *testing.T) {
	h := newHarness(t)
	data := payload(1_000_000)
	path := writeFile(t, data)
	meta := map[string]string{"filename": "clip_v4.mp4", "sha256": sum(data)}
	// The first run: every PATCH is cut, and it gives up after one retry, having saved the upload's URL.
	ct := &cutter{cut: func(int) int64 { return 200_000 }}
	c := newClient(h, ct)
	c.Retries = 1
	saved := ""
	c.Created = func(u string) { saved = u }
	if _, err := c.Upload(context.Background(), h.ts.URL+base, path, meta, ""); err == nil || saved == "" {
		t.Fatalf("first run: %v, saved %q", err, saved)
	}
	h.settle()
	have := h.offset(strings.TrimPrefix(saved, h.ts.URL))
	if have <= 0 {
		t.Fatalf("the server has %d bytes after the first run", have)
	}
	// The second run picks the upload up where the server is.
	ct2 := &cutter{}
	c2 := newClient(h, ct2)
	c2.Created = func(string) { t.Error("the second run made a new upload") }
	got, err := c2.Upload(context.Background(), h.ts.URL+base, path, meta, saved)
	if err != nil || got != saved {
		t.Fatalf("second run: %s %v", got, err)
	}
	if ct2.posts != 0 || len(ct2.offsets) == 0 || ct2.offsets[0] != have {
		t.Fatalf("second run: POSTs %d, PATCH offsets %v, want a first PATCH at %d", ct2.posts, ct2.offsets, have)
	}
	// A third run of a complete upload sends nothing.
	ct3 := &cutter{}
	if got, err := newClient(h, ct3).Upload(context.Background(), h.ts.URL+base, path, meta, saved); err != nil ||
		got != saved || ct3.posts != 0 || len(ct3.offsets) != 0 {
		t.Fatalf("third run: %s %v %d %v", got, err, ct3.posts, ct3.offsets)
	}
	// A resume URL that's gone (or of another file) starts a new upload.
	ct4 := &cutter{}
	other := payload(10)
	if _, err := newClient(h, ct4).Upload(context.Background(), h.ts.URL+base, writeFile(t, other),
		map[string]string{"filename": "x.mp4", "sha256": sum(other)}, saved); err != nil || ct4.posts != 1 {
		t.Fatalf("a different file: %v, POSTs %d", err, ct4.posts)
	}
	ct5 := &cutter{}
	if _, err := newClient(h, ct5).Upload(context.Background(), h.ts.URL+base, path, meta,
		h.ts.URL+base+"/up_unknownunknownunknown1"); err != nil || ct5.posts != 1 {
		t.Fatalf("a gone upload: %v, POSTs %d", err, ct5.posts)
	}
}

func TestClientSHAMismatch(t *testing.T) {
	h := newHarness(t)
	data := payload(5000)
	ct := &cutter{}
	_, err := newClient(h, ct).Upload(context.Background(), h.ts.URL+base, writeFile(t, data),
		map[string]string{"filename": "a.mp4", "sha256": sum([]byte("not it"))}, "")
	if err == nil || !strings.Contains(err.Error(), "changed while it was uploading") {
		t.Fatalf("err %v", err)
	}
	if fmt.Sprint(ct.offsets) != "[0 0]" {
		t.Fatalf("PATCHes %v: want the whole file sent twice", ct.offsets)
	}
}

func TestClientRefused(t *testing.T) {
	h := newHarness(t)
	data := payload(10)
	_, err := newClient(h, &cutter{}).Upload(context.Background(), h.ts.URL+base, writeFile(t, data),
		map[string]string{"filename": "a.txt", "sha256": sum(data)}, "")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadRequest || !strings.Contains(se.Message, "video file name") {
		t.Fatalf("err %v", err)
	}
	if _, err := (&Client{}).Upload(context.Background(), "/relative", writeFile(t, data), nil, ""); err == nil {
		t.Fatal("a relative endpoint was accepted")
	}
}

func TestMetadata(t *testing.T) {
	m, err := ParseMetadata("filename Y2xpcC5tcDQ=,flag, sha256 YWJj")
	if err != nil || m["filename"] != "clip.mp4" || m["sha256"] != "abc" || m["flag"] != "" || len(m) != 3 {
		t.Fatalf("%v %v", m, err)
	}
	if _, err := ParseMetadata("a YQ==,a YQ=="); err == nil {
		t.Fatal("a duplicate key was accepted")
	}
	if got := EncodeMetadata(map[string]string{"sha256": "abc", "filename": "clip.mp4"}); got != "filename Y2xpcC5tcDQ=,sha256 YWJj" {
		t.Fatal(got)
	}
	if s, err := FileSHA256(writeFile(t, []byte("abc"))); err != nil || s != sum([]byte("abc")) {
		t.Fatal(s, err)
	}
}
