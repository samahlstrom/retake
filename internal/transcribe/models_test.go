package transcribe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func serveFile(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/ggml-test.bin" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestModelStoreDownloadsAndChecks(t *testing.T) {
	body := bytes.Repeat([]byte("ggml"), 6<<20) // 24 MiB: big enough to show progress
	sum := sha256.Sum256(body)
	srv, hits := serveFile(t, body)
	good := remoteFile{File: "ggml-test.bin", URL: srv.URL + "/ggml-test.bin", Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
	var progress bytes.Buffer
	s := &ModelStore{Dir: filepath.Join(t.TempDir(), "models"), Progress: &progress}

	path, err := s.fetch(context.Background(), good)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("downloaded file differs: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(progress.String()), "\n")
	if len(lines) != 11 || !strings.HasSuffix(lines[0], "(24 MiB): 0%") || !strings.HasSuffix(lines[10], ": 100%") {
		t.Errorf("progress = %q", progress.String())
	}
	if _, err := s.fetch(context.Background(), good); err != nil || hits.Load() != 1 {
		t.Errorf("second fetch: err %v, %d downloads", err, hits.Load())
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 1 {
		t.Errorf("models folder holds %d files, want just the model", len(entries))
	}

	for name, f := range map[string]remoteFile{
		"checksum": {File: "bad-sum.bin", URL: good.URL, Size: good.Size, SHA256: strings.Repeat("0", 64)},
		"size":     {File: "bad-size.bin", URL: good.URL, Size: good.Size + 1, SHA256: good.SHA256},
		"404":      {File: "missing.bin", URL: srv.URL + "/missing.bin", Size: 10, SHA256: good.SHA256},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.fetch(context.Background(), f); err == nil {
				t.Fatal("no error")
			}
			if _, err := os.Stat(filepath.Join(s.Dir, f.File)); !os.IsNotExist(err) {
				t.Errorf("a bad download was kept: %v", err)
			}
		})
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 1 {
		t.Errorf("failed downloads left %d files", len(entries))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.fetch(ctx, remoteFile{File: "other.bin", URL: good.URL, Size: good.Size, SHA256: good.SHA256}); err == nil {
		t.Error("cancelled fetch succeeded")
	}
}

func TestModelStoreSmallFileIsQuiet(t *testing.T) {
	body := []byte("silero")
	sum := sha256.Sum256(body)
	srv, _ := serveFile(t, body)
	var progress bytes.Buffer
	s := &ModelStore{Dir: t.TempDir(), Progress: &progress}
	if _, err := s.fetch(context.Background(), remoteFile{"ggml-test.bin", srv.URL + "/ggml-test.bin", int64(len(body)),
		hex.EncodeToString(sum[:])}); err != nil {
		t.Fatal(err)
	}
	if progress.Len() != 0 {
		t.Errorf("progress = %q", progress.String())
	}
}

func TestModelNames(t *testing.T) {
	names := ModelNames()
	if strings.Join(names, " ") != "tiny base small medium large-v3-turbo large-v3" {
		t.Errorf("names = %v", names)
	}
	for _, n := range names {
		f, ok := modelFiles[n]
		if !ok || f.File != "ggml-"+n+".bin" || !strings.HasPrefix(f.URL, "https://huggingface.co/") ||
			!strings.HasSuffix(f.URL, "/"+f.File) || len(f.SHA256) != 64 || f.Size < 70<<20 {
			t.Errorf("%s: %+v", n, f)
		}
	}
	if modelRank("small") != 2 || modelRank("/x/ggml-mine.bin") != -1 {
		t.Error("modelRank")
	}
	for in, want := range map[string]string{"large-v3": "large-v3", "/m/ggml-large-v3-q5_0.bin": "large-v3-q5_0", "my.bin": "my"} {
		if got := modelLabel(in); got != want {
			t.Errorf("modelLabel(%q) = %q, want %q", in, got, want)
		}
	}
	s := &ModelStore{Dir: t.TempDir()}
	if _, err := s.Model(context.Background(), "huge"); err == nil || !strings.Contains(err.Error(), "tiny, base") {
		t.Errorf("unknown model: %v", err)
	}
	custom := filepath.Join(t.TempDir(), "ggml-custom.bin")
	if err := os.WriteFile(custom, []byte("w"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Model(context.Background(), custom); err != nil || p != custom {
		t.Errorf("custom model: %q %v", p, err)
	}
}

func TestDefaultModelDir(t *testing.T) {
	xdg := filepath.Join(t.TempDir(), "cache")
	if d, err := DefaultModelDir(env(map[string]string{"XDG_CACHE_HOME": xdg})); err != nil || d != filepath.Join(xdg, "retake", "models") {
		t.Errorf("XDG: %q %v", d, err)
	}
	d, err := DefaultModelDir(env(map[string]string{"XDG_CACHE_HOME": "relative"}))
	if err != nil || !strings.HasSuffix(d, filepath.Join("retake", "models")) || !filepath.IsAbs(d) {
		t.Errorf("fallback: %q %v", d, err)
	}
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
