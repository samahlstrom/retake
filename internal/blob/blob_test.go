package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestValidKey(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{
		{"a", true},
		{"projects/demo/media/demo_v1.mp4", true},
		{"projects/demo/media/demo_v1.review/2026-10-30_10-00-00/seg_01.webm", true},
		{"a/.hidden/b", true},
		{"a/..b/c", true},
		{"", false},
		{"/a", false},
		{"a/", false},
		{"a//b", false},
		{".", false},
		{"..", false},
		{"a/./b", false},
		{"a/../b", false},
		{"../a", false},
		{`a\b`, false},
		{"a\x00b", false},
	} {
		if got := ValidKey(tc.key); got != tc.want {
			t.Errorf("ValidKey(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}

func TestValidPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		want   bool
	}{
		{"", true}, {"projects/", true}, {"projects/de", true}, {"projects/demo/", true},
		{"/", false}, {"/a", false}, {"../", false}, {"a//", false}, {`a\`, false},
	} {
		if got := validPrefix(tc.prefix); got != tc.want {
			t.Errorf("validPrefix(%q) = %v, want %v", tc.prefix, got, tc.want)
		}
	}
}

func put(t *testing.T, s Store, key, data string) {
	t.Helper()
	if err := s.Put(context.Background(), key, strings.NewReader(data), int64(len(data)), "text/plain"); err != nil {
		t.Fatalf("Put(%s): %v", key, err)
	}
}

func get(t *testing.T, s Store, key string, off, n int64) string {
	t.Helper()
	rc, err := s.Get(context.Background(), key, off, n)
	if err != nil {
		t.Fatalf("Get(%s, %d, %d): %v", key, off, n, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func keys(infos []Info) []string {
	out := make([]string, len(infos))
	for i, x := range infos {
		out[i] = x.Key
	}
	return out
}

func TestLocalPutGetStat(t *testing.T) {
	ctx := context.Background()
	l := NewLocal(t.TempDir())
	if l.Name() != "local" {
		t.Errorf("Name = %q", l.Name())
	}
	put(t, l, "p/demo/media/a.txt", "0123456789")
	b, err := os.ReadFile(filepath.Join(l.Root, "p", "demo", "media", "a.txt"))
	if err != nil || string(b) != "0123456789" {
		t.Fatalf("file = %q, %v", b, err)
	}
	for _, tc := range []struct {
		off, n int64
		want   string
	}{
		{0, -1, "0123456789"},
		{0, 10, "0123456789"},
		{3, 4, "3456"},
		{7, -1, "789"},
		{7, 100, "789"},
		{9, 1, "9"},
		{10, -1, ""}, // at the end
		{50, 5, ""},  // past it
		{2, 0, ""},
	} {
		if got := get(t, l, "p/demo/media/a.txt", tc.off, tc.n); got != tc.want {
			t.Errorf("Get(%d, %d) = %q, want %q", tc.off, tc.n, got, tc.want)
		}
	}
	if _, err := l.Get(ctx, "p/demo/media/a.txt", -1, 2); err == nil {
		t.Error("a negative offset was accepted")
	}
	info, err := l.Stat(ctx, "p/demo/media/a.txt")
	if err != nil || info.Key != "p/demo/media/a.txt" || info.Size != 10 || info.Modified.IsZero() {
		t.Errorf("Stat = %+v, %v", info, err)
	}
	put(t, l, "p/demo/media/a.txt", "new") // replaces
	if got := get(t, l, "p/demo/media/a.txt", 0, -1); got != "new" {
		t.Errorf("after a second Put: %q", got)
	}
	for _, missing := range []string{"p/demo/media/b.txt", "p/demo/media", "nothing/here"} {
		if _, err := l.Stat(ctx, missing); !errors.Is(err, ErrNotFound) {
			t.Errorf("Stat(%s) = %v, want ErrNotFound", missing, err)
		}
		if _, err := l.Get(ctx, missing, 0, -1); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%s) = %v, want ErrNotFound", missing, err)
		}
	}
}

func TestLocalPutSizeAndFailures(t *testing.T) {
	ctx := context.Background()
	l := NewLocal(t.TempDir())
	// Exactly size bytes are stored, even when the reader has more.
	if err := l.Put(ctx, "k/long", strings.NewReader("abcdef"), 3, ""); err != nil {
		t.Fatal(err)
	}
	if got := get(t, l, "k/long", 0, -1); got != "abc" {
		t.Errorf("stored %q, want abc", got)
	}
	// A negative size reads to the end.
	if err := l.Put(ctx, "k/all", strings.NewReader("abcdef"), -1, ""); err != nil {
		t.Fatal(err)
	}
	if got := get(t, l, "k/all", 0, -1); got != "abcdef" {
		t.Errorf("stored %q", got)
	}
	// Too few bytes, or a reader that fails: nothing is stored, and the old object stays.
	put(t, l, "k/old", "keep me")
	if err := l.Put(ctx, "k/old", strings.NewReader("ab"), 5, ""); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("short reader: %v", err)
	}
	broken := io.MultiReader(strings.NewReader("partial"), iotestErr{})
	if err := l.Put(ctx, "k/old", broken, 100, ""); err == nil {
		t.Error("a failing reader was stored")
	}
	if err := l.Put(ctx, "k/new", io.MultiReader(strings.NewReader("x"), iotestErr{}), -1, ""); err == nil {
		t.Error("a failing reader was stored")
	}
	if got := get(t, l, "k/old", 0, -1); got != "keep me" {
		t.Errorf("old object = %q", got)
	}
	if _, err := l.Stat(ctx, "k/new"); !errors.Is(err, ErrNotFound) {
		t.Errorf("k/new after a failed Put: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(l.Root, "k"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"all", "long", "old"}) {
		t.Errorf("files left: %v (no temp files expected)", names)
	}
	// A cancelled context stores nothing.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.Put(cctx, "k/cancelled", strings.NewReader("x"), 1, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Put: %v", err)
	}
}

type iotestErr struct{}

func (iotestErr) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestLocalBadKeys(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l := NewLocal(filepath.Join(root, "store"))
	for _, key := range []string{"", "../escape", "/abs", "a/../../b", `a\b`, "a//b"} {
		if err := l.Put(ctx, key, strings.NewReader("x"), 1, ""); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Put(%q) = %v, want a bad key error", key, err)
		}
		if _, err := l.Get(ctx, key, 0, -1); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v", key, err)
		}
		if _, err := l.Stat(ctx, key); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("Stat(%q) = %v", key, err)
		}
		if err := l.Delete(ctx, key); err == nil {
			t.Errorf("Delete(%q) accepted", key)
		}
		if err := l.PutFile(ctx, key, filepath.Join(root, "x"), ""); err == nil {
			t.Errorf("PutFile(%q) accepted", key)
		}
	}
	if _, err := l.List(ctx, "../"); err == nil {
		t.Error("List(../) accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a bad key wrote outside the root")
	}
}

func TestLocalDeleteAndList(t *testing.T) {
	ctx := context.Background()
	l := NewLocal(t.TempDir())
	for _, k := range []string{"projects/demo/media/demo_v1.mp4", "projects/demo/media/demo_v1.review/t/seg_01.webm",
		"projects/demo2/x", "projects/de.txt", "other/y"} {
		put(t, l, k, k)
	}
	// A leftover temp file of an interrupted Put isn't an object.
	if err := os.WriteFile(filepath.Join(l.Root, "projects", "demo", "media", ".z.123"+tempSuffix), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		prefix string
		want   []string
	}{
		{"", []string{"other/y", "projects/de.txt", "projects/demo/media/demo_v1.mp4",
			"projects/demo/media/demo_v1.review/t/seg_01.webm", "projects/demo2/x"}},
		{"projects/demo/", []string{"projects/demo/media/demo_v1.mp4", "projects/demo/media/demo_v1.review/t/seg_01.webm"}},
		{"projects/demo", []string{"projects/demo/media/demo_v1.mp4", "projects/demo/media/demo_v1.review/t/seg_01.webm",
			"projects/demo2/x"}},
		{"projects/de", []string{"projects/de.txt", "projects/demo/media/demo_v1.mp4",
			"projects/demo/media/demo_v1.review/t/seg_01.webm", "projects/demo2/x"}},
		{"projects/demo/media/demo_v1.mp4", []string{"projects/demo/media/demo_v1.mp4"}},
		{"nothing/", []string{}},
		{"nothing/here/", []string{}},
	} {
		got, err := l.List(ctx, tc.prefix)
		if err != nil {
			t.Fatalf("List(%q): %v", tc.prefix, err)
		}
		if !slices.Equal(keys(got), tc.want) {
			t.Errorf("List(%q) = %v, want %v", tc.prefix, keys(got), tc.want)
		}
	}
	all, err := l.List(ctx, "projects/demo/media/demo_v1.mp4")
	if err != nil || len(all) != 1 || all[0].Size != int64(len("projects/demo/media/demo_v1.mp4")) {
		t.Errorf("List sizes: %+v, %v", all, err)
	}
	if err := l.Delete(ctx, "projects/demo2/x"); err != nil {
		t.Fatal(err)
	}
	if err := l.Delete(ctx, "projects/demo2/x"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
	if err := l.Delete(ctx, "projects/demo"); err != nil {
		t.Errorf("deleting a folder's key: %v", err)
	}
	if _, err := l.Stat(ctx, "projects/demo/media/demo_v1.mp4"); err != nil {
		t.Errorf("a folder delete removed its files: %v", err)
	}
	if _, err := l.Stat(ctx, "projects/demo2/x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete: %v", err)
	}
}

func TestLocalPutFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	l := NewLocal(filepath.Join(root, "store"))
	// The key's own file: nothing to do, and it stays as it is.
	put(t, l, "p/demo/media/v1.mp4", "video")
	own := filepath.Join(l.Root, "p", "demo", "media", "v1.mp4")
	before, err := os.Stat(own)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.PutFile(ctx, "p/demo/media/v1.mp4", own, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.mp4")
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	if err := l.PutFile(ctx, "p/demo/media/v1.mp4", link, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(own)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("PutFile of the key's own file replaced it")
	}
	// Another file is copied in.
	src := filepath.Join(root, "other.mp4")
	if err := os.WriteFile(src, []byte("other video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.PutFile(ctx, "p/demo/media/v2.mp4", src, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, l, "p/demo/media/v2.mp4", 0, -1); got != "other video" {
		t.Errorf("copied %q", got)
	}
	if err := l.PutFile(ctx, "p/demo/media/v1.mp4", src, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	if got := get(t, l, "p/demo/media/v1.mp4", 0, -1); got != "other video" {
		t.Errorf("replaced with %q", got)
	}
	if err := l.PutFile(ctx, "p/x", filepath.Join(root, "missing"), ""); err == nil {
		t.Error("PutFile of a missing file succeeded")
	}
	if err := l.PutFile(ctx, "p/x", root, ""); err == nil {
		t.Error("PutFile of a folder succeeded")
	}
}

type served struct {
	code int
	hdr  http.Header
	body string
}

func serve(t *testing.T, s Store, method, key, rng string) (served, error) {
	t.Helper()
	req := httptest.NewRequest(method, "/video", nil)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	rec := httptest.NewRecorder()
	err := Serve(rec, req, s, key, "video/mp4")
	return served{rec.Code, rec.Header(), rec.Body.String()}, err
}

func TestServeRanges(t *testing.T) {
	l := NewLocal(t.TempDir())
	data := bytes.Repeat([]byte("0123456789"), 100)
	size := len(data)
	put(t, l, "v/clip.mp4", string(data))
	for _, tc := range []struct {
		rng        string
		code       int
		start, end int // inclusive; unused for 416
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
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+" "+tc.rng, func(t *testing.T) {
				r, err := serve(t, l, method, "v/clip.mp4", tc.rng)
				if err != nil {
					t.Fatal(err)
				}
				if r.code != tc.code {
					t.Fatalf("code = %d, want %d", r.code, tc.code)
				}
				if tc.code == 416 {
					want := map[string]string{"Content-Range": fmt.Sprintf("bytes */%d", size),
						"Content-Type": "text/plain", "Content-Length": "0", "Cache-Control": "no-store"}
					for k, v := range want {
						if got := r.hdr.Get(k); got != v {
							t.Errorf("%s = %q, want %q", k, got, v)
						}
					}
					if r.body != "" {
						t.Error("416 with a body")
					}
					return
				}
				n := tc.end - tc.start + 1
				want := map[string]string{"Content-Length": strconv.Itoa(n), "Content-Type": "video/mp4",
					"Accept-Ranges": "bytes", "Cache-Control": "no-cache", "Content-Range": ""}
				if tc.code == 206 {
					want["Content-Range"] = fmt.Sprintf("bytes %d-%d/%d", tc.start, tc.end, size)
				}
				for k, v := range want {
					if got := r.hdr.Get(k); got != v {
						t.Errorf("%s = %q, want %q", k, got, v)
					}
				}
				wantBody := string(data[tc.start : tc.end+1])
				if method == http.MethodHead {
					wantBody = ""
				}
				if r.body != wantBody {
					t.Errorf("body: %d bytes, want %d", len(r.body), len(wantBody))
				}
			})
		}
	}
}

func TestServeEmptyAndMissing(t *testing.T) {
	l := NewLocal(t.TempDir())
	put(t, l, "v/empty.mp4", "")
	r, err := serve(t, l, http.MethodGet, "v/empty.mp4", "")
	if err != nil || r.code != 200 || r.hdr.Get("Content-Length") != "0" || r.body != "" {
		t.Errorf("empty object: %+v, %v", r, err)
	}
	if r, _ := serve(t, l, http.MethodGet, "v/empty.mp4", "bytes=0-"); r.code != 416 {
		t.Errorf("a range of an empty object: %d", r.code)
	}
	r, err = serve(t, l, http.MethodGet, "v/missing.mp4", "bytes=0-9")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if len(r.hdr) != 0 || r.body != "" {
		t.Errorf("a missing object wrote %v %q", r.hdr, r.body)
	}
	if _, err := serve(t, l, http.MethodGet, "../bad", ""); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("a bad key: %v", err)
	}
}

func TestNewS3Config(t *testing.T) {
	ok := S3Config{Endpoint: "minio:9000", Bucket: "retake", AccessKey: "a", SecretKey: "b", Insecure: true}
	for _, tc := range []struct {
		name   string
		mod    func(c *S3Config)
		errHas string
	}{
		{"no endpoint", func(c *S3Config) { c.Endpoint = "" }, "endpoint"},
		{"blank endpoint", func(c *S3Config) { c.Endpoint = "  " }, "endpoint"},
		{"a path", func(c *S3Config) { c.Endpoint = "minio:9000/bucket" }, "without a path"},
		{"no bucket", func(c *S3Config) { c.Bucket = "" }, "bucket"},
		{"bad bucket", func(c *S3Config) { c.Bucket = "Not_A_Bucket" }, "bucket"},
		{"short bucket", func(c *S3Config) { c.Bucket = "ab" }, "bucket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ok
			tc.mod(&c)
			if _, err := NewS3(context.Background(), c); err == nil || !strings.Contains(err.Error(), tc.errHas) {
				t.Errorf("err = %v, want one about %s", err, tc.errHas)
			}
		})
	}
	for _, tc := range []struct {
		endpoint string
		insecure bool
		secure   bool
		host     string
	}{
		{"minio:9000", true, false, "minio:9000"},
		{"s3.example.com", false, true, "s3.example.com"},
		{"http://minio:9000/", false, false, "minio:9000"},
		{"https://s3.example.com", true, true, "s3.example.com"},
	} {
		c := ok
		c.Endpoint, c.Insecure = tc.endpoint, tc.insecure
		s, err := newS3(c)
		if err != nil {
			t.Fatalf("newS3(%s): %v", tc.endpoint, err)
		}
		u := s.client.EndpointURL()
		if u.Host != tc.host || (u.Scheme == "https") != tc.secure {
			t.Errorf("newS3(%s, insecure %v): %s", tc.endpoint, tc.insecure, u)
		}
		if s.Name() != "s3" || s.Bucket() != "retake" || s.region != DefaultRegion {
			t.Errorf("newS3: name %s bucket %s region %s", s.Name(), s.Bucket(), s.region)
		}
	}
}

func TestS3RefusesBadKeysWithoutTheNetwork(t *testing.T) {
	ctx := context.Background()
	// A closed port: any request would fail with a connection error, not a bad key.
	s, err := newS3(S3Config{Endpoint: "127.0.0.1:1", Bucket: "retake", Insecure: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", "../x", "/x", "a//b"} {
		if err := s.Put(ctx, key, strings.NewReader("x"), 1, ""); err == nil || !strings.Contains(err.Error(), "bad key") {
			t.Errorf("Put(%q) = %v", key, err)
		}
		if _, err := s.Get(ctx, key, 0, -1); err == nil || !strings.Contains(err.Error(), "bad key") {
			t.Errorf("Get(%q) = %v", key, err)
		}
		if _, err := s.Stat(ctx, key); err == nil || !strings.Contains(err.Error(), "bad key") {
			t.Errorf("Stat(%q) = %v", key, err)
		}
		if err := s.Delete(ctx, key); err == nil || !strings.Contains(err.Error(), "bad key") {
			t.Errorf("Delete(%q) = %v", key, err)
		}
		if err := s.PutFile(ctx, key, "x", ""); err == nil || !strings.Contains(err.Error(), "bad key") {
			t.Errorf("PutFile(%q) = %v", key, err)
		}
	}
	if _, err := s.List(ctx, "/"); err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Errorf("List(/) = %v", err)
	}
	if _, err := s.Get(ctx, "a", -1, 1); err == nil {
		t.Error("a negative offset was accepted")
	}
}
