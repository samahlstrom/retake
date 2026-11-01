package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/internal/theme"
)

// The user's theme (spec/theme.md) through retake open's server: the page gets it, its files are served with their
// own types and a CSP, scripts never are, and every answer says the API version.
func TestThemeServed(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"theme.css": ":root{--accent:#2f6fed}", "theme.toml": `name = "Acme"`, "fonts/a.woff2": "w2",
		"evil.js": "alert(1)", "logo.svg": "<svg/>",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	page := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html><head><title>Review</title></head></html>")}}
	h := start(t, Options{Video: f.video, UI: theme.Page(page, dir)})

	r := h.get("/", nil)
	body := string(r.body)
	if r.code != 200 || !strings.Contains(body, `<link rel="stylesheet" href="theme/theme.css">`) ||
		!strings.Contains(body, "<title>Review · Acme</title>") || !strings.Contains(body, `data-theme="dark"`) {
		t.Errorf("page = %d %s", r.code, body)
	}
	for path, ct := range map[string]string{"/theme/theme.css": "text/css; charset=utf-8",
		"/theme/fonts/a.woff2": "font/woff2", "/theme/logo.svg": "image/svg+xml"} {
		r := h.get(path, nil)
		if r.code != 200 || r.header.Get("Content-Type") != ct || r.header.Get("X-Content-Type-Options") != "nosniff" ||
			r.header.Get("Content-Security-Policy") != theme.CSP || r.header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s = %d %v", path, r.code, r.header)
		}
	}
	for _, path := range []string{"/theme/evil.js", "/theme/missing.css", "/theme/", "/theme/../index.html"} {
		if r := h.get(path, nil); r.code != 404 {
			t.Errorf("%s = %d %s", path, r.code, r.body)
		}
	}
	for _, path := range []string{"/", "/api/ping", "/api/info", "/theme/theme.css", "/nope"} {
		if v := h.get(path, nil).header.Get(APIHeader); v != APIVersion {
			t.Errorf("%s: %s = %q", path, APIHeader, v)
		}
	}
}
