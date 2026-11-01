package hub

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/samahlstrom/retake/internal/server"
	"github.com/samahlstrom/retake/internal/theme"
)

const themedHome = "<!doctype html><html lang=\"en\"><head><title>Projects · Retake</title></head><body></body></html>"

func themeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"theme.css": ":root{--accent:red}", "theme.toml": `name = "Acme"`,
		"run.js": "alert(1)"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func themedUI() fstest.MapFS {
	return fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html><head><title>Review</title></head></html>")},
		"home.html": {Data: []byte(themedHome)}}
}

// retake open <folder>: the home and every project's page get the theme, at / and at /p/<name>/.
func TestFolderTheme(t *testing.T) {
	dir := themeDir(t)
	x := newFT(t, func(o *FolderOptions) { o.UI = theme.Page(themedUI(), dir) })
	home := x.do("GET", "/", "")
	if home.code != 200 || !strings.Contains(home.body, `href="theme/theme.css"`) ||
		!strings.Contains(home.body, "<title>Projects · Acme</title>") {
		t.Errorf("home = %d %s", home.code, home.body)
	}
	for _, path := range []string{"/theme/theme.css", "/p/alpha/theme/theme.css"} {
		r := x.do("GET", path, "")
		if r.code != 200 || r.body != ":root{--accent:red}" || r.hdr.Get("Content-Security-Policy") != theme.CSP ||
			r.hdr.Get("Content-Type") != "text/css; charset=utf-8" {
			t.Errorf("%s = %d %q %v", path, r.code, r.body, r.hdr)
		}
	}
	if page := x.do("GET", "/p/alpha/", ""); !strings.Contains(page.body, `href="theme/theme.css"`) {
		t.Errorf("project page = %s", page.body)
	}
	for _, path := range []string{"/theme/run.js", "/p/alpha/theme/run.js", "/theme/theme.toml"} {
		if r := x.do("GET", path, ""); r.code != http.StatusNotFound {
			t.Errorf("%s = %d", path, r.code)
		}
	}
	if v := x.do("GET", "/api/ping", "").hdr.Get(server.APIHeader); v != server.APIVersion {
		t.Errorf("api version header = %q", v)
	}
}

// retake serve: the theme's files are served at /theme/ like the page's own assets (no session needed).
func TestHubTheme(t *testing.T) {
	dir := themeDir(t)
	x := newTH(t, Config{}, func(o *Options) { o.UI, o.Theme = theme.Page(themedUI(), dir), dir })
	r := x.do("GET", "/theme/theme.css", "")
	if r.code != 200 || r.hdr.Get("Content-Security-Policy") != theme.CSP || r.hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("theme.css = %d %v", r.code, r.hdr)
	}
	if r := x.do("GET", "/theme/run.js", ""); r.code != http.StatusNotFound {
		t.Errorf("run.js = %d", r.code)
	}
	if v := x.do("GET", "/api/ping", "").hdr.Get(server.APIHeader); v != server.APIVersion {
		t.Errorf("api version header = %q", v)
	}
	// The server's own pages wear the theme too: its name, its stylesheet, and a CSP that lets them load.
	r = x.do("GET", "/signin", "")
	if !strings.Contains(r.body, "<title>Sign in</title>") || !strings.Contains(r.body, "<h1>Sign in to Acme</h1>") ||
		!strings.Contains(r.body, `<link rel="stylesheet" href="/theme/theme.css">`) ||
		!strings.Contains(r.hdr.Get("Content-Security-Policy"), "style-src 'self' 'unsafe-inline'") ||
		strings.Contains(r.hdr.Get("Content-Security-Policy"), "script-src") {
		t.Errorf("signin = %d %s %v", r.code, r.body, r.hdr)
	}
	// Without a theme they are as they were.
	y := newTH(t, Config{}, nil)
	if r := y.do("GET", "/signin", ""); !strings.Contains(r.body, `<h1>Sign in to Retake<span class="wordmark-dot">.</span></h1>`) ||
		!strings.Contains(r.body, "<title>Sign in</title>") ||
		strings.Contains(r.body, "theme.css") || strings.Contains(r.hdr.Get("Content-Security-Policy"), "style-src 'self'") {
		t.Errorf("unthemed signin = %s %v", r.body, r.hdr)
	}
}
