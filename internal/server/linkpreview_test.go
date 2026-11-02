package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/linkcard"
)

// GET /api/link-preview (spec/api.md, "Link preview"): a page's title, site and picture for a script's link card,
// read by the server that serves the page. The page is a local test server, which only this test's reader may reach;
// nothing here reaches the internet.
func TestLinkPreview(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // the default reader's cache
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<head><meta property="og:title" content="A reference"><meta property="og:site_name" content="Refs">`+
			`<meta property="og:image" content="https://img.example/r.png"></head>`)
	}))
	defer site.Close()
	su, _ := url.Parse(site.URL)
	reader := linkcard.New(linkcard.Options{CacheDir: t.TempDir(), Timeout: 2 * time.Second, Allow: func(ap netip.AddrPort) bool {
		return ap.Addr().IsLoopback() && fmt.Sprint(ap.Port()) == su.Port() || linkcard.Public(ap.Addr())
	}})
	h := startTeam(t, Options{Links: reader})
	q := "/api/link-preview?url=" + url.QueryEscape(site.URL+"/ref")

	r := h.get(q, nil)
	if c := r.json(t); r.code != 200 || c["title"] != "A reference" || c["site"] != "Refs" ||
		c["image"] != "https://img.example/r.png" || c["url"] != site.URL+"/ref" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("local: %d %s %v", r.code, r.body, r.header)
	}
	// A viewer's share link reads it too; without a token it's refused, like every read.
	tok := makeToken(t, h, "viewer", "", 0)
	if r := h.req("GET", q, "share.test", map[string]string{"Cookie": ShareCookie + "=" + tok}); r.code != 200 ||
		!strings.Contains(string(r.body), "A reference") {
		t.Errorf("viewer: %d %s", r.code, r.body)
	}
	if r := h.req("GET", q, "share.test", nil); r.code != 401 {
		t.Errorf("no token: %d %s", r.code, r.body)
	}
	// Not a public address, not http(s), nothing: 400. The server's own default reader refuses every local address.
	for _, raw := range []string{"http://127.0.0.1:1/", "http://169.254.169.254/latest/meta-data/", "file:///etc/passwd", ""} {
		if r := h.get("/api/link-preview?url="+url.QueryEscape(raw), nil); r.code != 400 || r.json(t)["error"] != "not a public web address" {
			t.Errorf("%q: %d %s", raw, r.code, r.body)
		}
	}
	plain := startTeam(t, Options{})
	if r := plain.get(q, nil); r.code != 400 {
		t.Errorf("the default reader read a loopback page: %d %s", r.code, r.body)
	}
	// A page that can't be read: 502. A POST isn't a route.
	if r := h.get("/api/link-preview?url="+url.QueryEscape(site.URL+"/gone"), nil); r.code != 502 ||
		r.json(t)["error"] != "can't read that page" {
		t.Errorf("unreadable: %d %s", r.code, r.body)
	}
	if r := h.post(q, "", nil); r.code != 404 {
		t.Errorf("POST: %d", r.code)
	}
}
