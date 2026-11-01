package relay

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayCorrelatesResponsesAndDropsCancelledRequests(t *testing.T) {
	var h Hub
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		v, _ := h.Fetch(ctx, "sam", Request{Render: "revision", File: "index.m3u8"})
		b, _ := io.ReadAll(v.Body)
		v.Close()
		v.Close() // twice is fine
		done <- string(b)
	}()
	r, e := h.Next(ctx, "sam")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := h.Reply("clark", r.ID, Response{Status: 200}); ok {
		t.Fatal("another worker answered")
	}
	read, ok := h.Reply("sam", r.ID, Response{Status: 200, Body: strings.NewReader("playlist")})
	if !ok {
		t.Fatal("response not delivered")
	}
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("the reader never said it was done")
	}
	if v := <-done; v != "playlist" {
		t.Fatal(v)
	}
	if _, ok := h.Reply("sam", r.ID, Response{Status: 200}); ok {
		t.Fatal("reply after request finished")
	}
	cancelled, c := context.WithCancel(context.Background())
	c()
	if _, e = h.Fetch(cancelled, "sam", Request{}); e == nil {
		t.Fatal("cancellation ignored")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) != 0 {
		t.Fatal("queue slots leaked")
	}
}

func TestMediaNamesCannotReadSourceOrEscape(t *testing.T) {
	for _, name := range []string{"../file.mp4", "a/b.mp4", "a\\b.mp4", "source.json", "http://elsewhere/a.mp4", ".env", "index.m3u8?secret"} {
		if ValidFile(name) {
			t.Errorf("accepted %q", name)
		}
	}
	for _, name := range []string{"index.m3u8", "init_00000.mp4", "seg_00000.m4s"} {
		if !ValidFile(name) {
			t.Errorf("rejected %q", name)
		}
	}
}

func TestPlanCapsEveryAnswerAtOneChunk(t *testing.T) {
	const size, limit = 100, 10
	for _, c := range []struct {
		header     string
		start, end int64
		status     int
	}{
		{"", 0, 9, 206},            // no range on a big file: its first chunk, as a range of it
		{"bytes=0-", 0, 9, 206},    // open-ended: capped
		{"bytes=95-", 95, 99, 206}, // the tail
		{"bytes=10-14", 10, 14, 206},
		{"bytes=20-200", 20, 29, 206},
		{"bytes=-5", 95, 99, 206}, // a suffix
		{"bytes=-500", 0, 9, 206}, // a suffix longer than the file
		{"bytes=100-", 0, 0, 416}, // past the end
		{"bytes=-0", 0, 0, 416},
		{"bytes=5-1", 0, 9, 206},     // invalid: the whole file, capped
		{"bytes=0-1,5-6", 0, 9, 206}, // several: the whole file, capped
		{"items=0-5", 0, 9, 206},
	} {
		start, end, status := Plan(c.header, size, limit)
		if start != c.start || end != c.end || status != c.status {
			t.Errorf("%q: %d-%d %d, want %d-%d %d", c.header, start, end, status, c.start, c.end, c.status)
		}
	}
	if start, end, status := Plan("", 8, limit); start != 0 || end != 7 || status != 200 {
		t.Errorf("a small file whole: %d-%d %d", start, end, status)
	}
	if _, end, status := Plan("bytes=0-", 8, limit); end != 7 || status != 206 {
		t.Errorf("a small file's range: %d %d", end, status)
	}
	if _, end, status := Plan("", 0, limit); end != -1 || status != 200 {
		t.Errorf("an empty file: %d %d", end, status)
	}
	if _, _, status := Plan("bytes=0-", 0, limit); status != 416 {
		t.Errorf("a range of an empty file: %d", status)
	}
	if got := ContentRange(0, 9, 100); got != "bytes 0-9/100" {
		t.Fatal(got)
	}
	if got := ChunkRange(2); got != "bytes=16777216-25165823" {
		t.Fatal(got)
	}
	if a, b, n, ok := ParseContentRange("bytes 16777216-20000000/20000001"); !ok || a != 16777216 || b != 20000000 || n != 20000001 {
		t.Fatal(a, b, n, ok)
	}
	for _, bad := range []string{"bytes */100", "bytes 5-1/10", "bytes 0-10/10", "bytes 0-1", ""} {
		if _, _, _, ok := ParseContentRange(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func fill(t *testing.T, c *Cache, scope, key, body string) {
	t.Helper()
	f, wait := c.Begin(scope, key)
	if f == nil || wait != nil {
		t.Fatalf("%s: not the one to fetch it", key)
	}
	f.Open(Meta{Type: "video/mp4", Start: 0, Length: int64(len(body)), Total: int64(len(body))})
	_, _ = io.Copy(f, strings.NewReader(body))
	f.Commit()
}

func read(t *testing.T, c *Cache, scope, key string) (string, bool) {
	t.Helper()
	h, ok := c.Get(scope, key)
	if !ok {
		return "", false
	}
	defer h.Close()
	b, err := io.ReadAll(h.Body())
	if err != nil {
		t.Fatal(err)
	}
	return string(b), true
}

func TestCacheKeepsChunksAndForgetsTheLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCache(dir, 200) // two chunks of 30 bytes and their headers fit, not three
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("demo", "a"); ok {
		t.Fatal("a hit in an empty cache")
	}
	fill(t, c, "demo", "a", strings.Repeat("a", 30))
	if got, ok := read(t, c, "demo", "a"); !ok || got != strings.Repeat("a", 30) {
		t.Fatal(got, ok)
	}
	h, _ := c.Get("demo", "a")
	if h.Type != "video/mp4" || h.Total != 30 || h.Length != 30 {
		t.Fatalf("meta %+v", h.Meta)
	}
	h.Close()
	fill(t, c, "demo", "b", strings.Repeat("b", 30))
	read(t, c, "demo", "a") // a is used more recently than b now
	fill(t, c, "other", "c", strings.Repeat("c", 30))
	if _, ok := read(t, c, "demo", "b"); ok {
		t.Fatal("the least recently used chunk was kept past the size")
	}
	if _, ok := read(t, c, "demo", "a"); !ok {
		t.Fatal("a recently used chunk went")
	}
	if c.Used() > 200 {
		t.Fatal("over its size", c.Used())
	}

	// Another process finds what this one kept.
	again, err := OpenCache(dir, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := read(t, again, "other", "c"); !ok || got != strings.Repeat("c", 30) {
		t.Fatal("not kept across a restart", got)
	}
	// A deleted project's chunks go.
	if err := again.Drop("demo"); err != nil {
		t.Fatal(err)
	}
	if _, ok := read(t, again, "demo", "a"); ok {
		t.Fatal("a dropped project's chunk")
	}
	if _, err := os.Stat(filepath.Join(dir, "demo")); !os.IsNotExist(err) {
		t.Fatal("its folder stayed")
	}
	if _, ok := read(t, again, "other", "c"); !ok {
		t.Fatal("dropping one project dropped another's")
	}
	for _, bad := range []string{"", "..", "a/b", ".hidden"} {
		if f, _ := again.Begin(bad, "k"); f != nil {
			t.Errorf("scope %q", bad)
		}
	}
}

func TestCacheFetchesAChunkOnceForConcurrentMisses(t *testing.T) {
	c, err := OpenCache(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	f, wait := c.Begin("demo", "k")
	if f == nil || wait != nil {
		t.Fatal("the first miss doesn't fetch")
	}
	f2, wait2 := c.Begin("demo", "k")
	if f2 != nil || wait2 == nil {
		t.Fatal("a second miss fetches too")
	}
	select {
	case <-wait2:
		t.Fatal("done before the fetch")
	default:
	}
	f.Open(Meta{Length: 5, Total: 5})
	_, _ = f.Write([]byte("hello"))
	f.Commit()
	<-wait2
	if got, ok := read(t, c, "demo", "k"); !ok || got != "hello" {
		t.Fatal(got, ok)
	}
	if f3, wait3 := c.Begin("demo", "k"); f3 != nil || wait3 == nil {
		t.Fatal("a cached chunk fetched again")
	}

	// A fetch that ends short isn't kept, and the next miss fetches again.
	short, _ := c.Begin("demo", "short")
	short.Open(Meta{Length: 10, Total: 10})
	_, _ = short.Write([]byte("only5"))
	short.Commit()
	if _, ok := c.Get("demo", "short"); ok {
		t.Fatal("a short chunk was kept")
	}
	aborted, _ := c.Begin("demo", "short")
	if aborted == nil {
		t.Fatal("no new fetch after a failed one")
	}
	aborted.Abort()
	if _, ok := c.Get("demo", "short"); ok {
		t.Fatal("an aborted chunk was kept")
	}
}
