package linkcard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The address guard (spec/api.md, "Link preview"): only ordinary public unicast addresses.
func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34": true, "8.8.8.8": true, "1.1.1.1": true, "2606:4700:4700::1111": true, "2a00:1450:4001::200e": true,
		"127.0.0.1": false, "127.255.255.254": false, "::1": false, // loopback
		"10.1.2.3": false, "172.16.0.1": false, "172.31.255.255": false, "192.168.1.1": false, "fd00::1": false, // private
		"169.254.169.254": false, "169.254.0.1": false, "fe80::1": false, // link-local: the cloud metadata address
		"fd00:ec2::254": false,                           // AWS's IPv6 metadata address
		"100.64.0.1":    false, "100.100.100.200": false, // shared (carrier NAT, Alibaba's metadata)
		"0.0.0.0": false, "::": false, "0.1.2.3": false, // unspecified, "this network"
		"224.0.0.1": false, "239.255.255.250": false, "ff02::1": false, // multicast
		"255.255.255.255": false, "240.0.0.1": false, // broadcast, reserved
		"192.0.2.1": false, "198.51.100.7": false, "203.0.113.9": false, "2001:db8::1": false, // documentation
		"198.18.0.1": false, "192.0.0.8": false, // benchmarking, protocol assignments
		"::ffff:127.0.0.1": false, "::ffff:169.254.169.254": false, "::ffff:8.8.8.8": true, // IPv4 in IPv6
		"64:ff9b::a9fe:a9fe": false, "64:ff9b::808:808": true, "64:ff9b:1::808:808": false, // NAT64
		"2002:7f00:1::": false, "2002:a9fe:a9fe::1": false, "2002:808:808::1": true, // 6to4
		"fe80::1%eth0": false,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
	if Public(netip.Addr{}) {
		t.Error("the zero address is public")
	}
}

func TestCheckURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://example.com/a?b=c": true, "http://example.com:8080/": true,
		"ftp://example.com/": false, "javascript:alert(1)": false, "file:///etc/passwd": false, "data:text/html,x": false,
		"https://user:pass@example.com/": false, "https://user@example.com/": false, "https:///path": false,
		"//example.com/": false, "example.com": false, "mailto:a@example.com": false,
		"https://example.com/" + strings.Repeat("a", MaxURL): false,
	} {
		u, err := url.Parse(raw)
		if err == nil {
			err = checkURL(u)
		}
		if (err == nil) != ok {
			t.Errorf("checkURL(%q) = %v", raw, err)
		}
	}
}

func TestParse(t *testing.T) {
	base, _ := url.Parse("https://example.com/posts/1")
	page := `<!doctype html><html><head>
<meta charset="utf-8">
<title>  The   &amp; title
</title>
<meta property="og:title" content="Jump souls &mdash; the trick">
<meta property="og:site_name" content="Example">
<meta property="og:image" content="/img/card.png">
<meta name="description" content="A plain   description.">
<meta property="og:title" content="A second og:title is ignored">
</head><body><meta property="og:description" content="in the body: ignored"></body></html>`
	c := Parse(strings.NewReader(page), base)
	want := Card{Title: "Jump souls — the trick", Site: "Example", Image: "https://example.com/img/card.png",
		Description: "A plain description."}
	if c != want {
		t.Errorf("og: %+v\nwant %+v", c, want)
	}
	// No og: the <title>, twitter: tags, an http picture dropped.
	c = Parse(strings.NewReader(`<html><head><title>Only a &lt;title&gt;</title>
<meta name="twitter:image" content="http://example.com/x.png"></head>`), base)
	if c.Title != "Only a <title>" || c.Image != "" || c.Site != "" {
		t.Errorf("title only: %+v", c)
	}
	c = Parse(strings.NewReader(`<meta name="twitter:title" content="Tw"><meta name="twitter:image" content="//cdn.example.com/p.jpg">`), base)
	if c.Title != "Tw" || c.Image != "https://cdn.example.com/p.jpg" {
		t.Errorf("twitter: %+v", c)
	}
	// Long text is cut; broken UTF-8 is dropped.
	c = Parse(strings.NewReader(`<title>`+strings.Repeat("é", 400)+"\xff</title>"), base)
	if n := len([]rune(c.Title)); n != maxTitle || !strings.HasSuffix(c.Title, "…") {
		t.Errorf("cut: %d %q", n, c.Title[len(c.Title)-8:])
	}
	if c := Parse(strings.NewReader(""), base); c != (Card{}) {
		t.Errorf("empty: %+v", c)
	}
}

// loopback is a reader that may reach the test's own servers (on 127.0.0.1) and nothing else that isn't public.
func loopback(o Options, ports ...string) *Reader {
	o.Allow = func(ap netip.AddrPort) bool {
		for _, p := range ports {
			if ap.Addr().IsLoopback() && fmt.Sprint(ap.Port()) == p {
				return true
			}
		}
		return Public(ap.Addr())
	}
	return New(o)
}

func portOf(ts *httptest.Server) string {
	u, _ := url.Parse(ts.URL)
	return u.Port()
}

func TestCard(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("User-Agent") != UserAgent {
			http.Error(w, "who", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, `<html><head><title>A page</title><meta property="og:image" content="https://img.example.com/a.png"></head></html>`)
		case "/moved":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 <title>not read</title>"))
		case "/big":
			// The title comes after the first MiB: it isn't read.
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<html><head><!--"+strings.Repeat("x", MaxBody)+"--><title>Too far</title></head>")
		case "/slow":
			time.Sleep(600 * time.Millisecond)
			_, _ = fmt.Fprint(w, "<title>Late</title>")
		case "/gone":
			http.NotFound(w, r)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer ts.Close()
	now := time.Unix(1_800_000_000, 0)
	rd := loopback(Options{CacheDir: t.TempDir(), Timeout: 300 * time.Millisecond, Now: func() time.Time { return now }}, portOf(ts))
	ctx := context.Background()

	c, err := rd.Card(ctx, ts.URL+"/page#top")
	if err != nil || c.Title != "A page" || c.Image != "https://img.example.com/a.png" || c.Site != "127.0.0.1" ||
		c.URL != ts.URL+"/page" {
		t.Fatalf("page: %+v %v", c, err)
	}
	// From the cache: the server isn't asked again, until the card is a week old.
	n := hits.Load()
	if c2, err := rd.Card(ctx, ts.URL+"/page"); err != nil || c2 != c || hits.Load() != n {
		t.Errorf("cached: %+v %v, %d requests", c2, err, hits.Load()-n)
	}
	now = now.Add(TTL)
	if _, err := rd.Card(ctx, ts.URL+"/page"); err != nil || hits.Load() != n+1 {
		t.Errorf("a week later: %v, %d requests", err, hits.Load()-n)
	}
	if c, err := rd.Card(ctx, ts.URL+"/moved"); err != nil || c.Title != "A page" || c.URL != ts.URL+"/moved" {
		t.Errorf("redirected: %+v %v", c, err)
	}
	if c, err := rd.Card(ctx, ts.URL+"/pdf"); err != nil || c.Title != "" || c.Site != "127.0.0.1" {
		t.Errorf("not HTML: %+v %v", c, err)
	}
	if c, err := rd.Card(ctx, ts.URL+"/big"); err != nil || c.Title != "" {
		t.Errorf("past 1 MiB: %+v %v", c, err)
	}
	for _, p := range []string{"/slow", "/gone", "/loop"} {
		if _, err := rd.Card(ctx, ts.URL+p); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: %v", p, err)
		}
	}
	// A failure is kept for 10 minutes.
	n = hits.Load()
	if _, err := rd.Card(ctx, ts.URL+"/gone"); !errors.Is(err, ErrUnreadable) || hits.Load() != n {
		t.Errorf("a failure again: %v, %d requests", err, hits.Load()-n)
	}
	now = now.Add(FailTTL)
	if _, err := rd.Card(ctx, ts.URL+"/gone"); !errors.Is(err, ErrUnreadable) || hits.Load() != n+1 {
		t.Errorf("a failure later: %v, %d requests", err, hits.Load()-n)
	}
}

// The guard (spec/api.md, "Link preview"): a loopback server, a name that resolves to loopback, a metadata address,
// and a redirect from an allowed server to one that isn't are all refused, and nothing reaches the refused server.
func TestCardRefuses(t *testing.T) {
	var reached atomic.Int32
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		_, _ = fmt.Fprint(w, "<title>Inside</title>")
	}))
	defer inside.Close()
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-inside":
			http.Redirect(w, r, inside.URL+"/", http.StatusFound)
		case "/to-name":
			http.Redirect(w, r, "http://localhost:"+portOf(inside)+"/", http.StatusFound)
		case "/to-metadata":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		case "/to-file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}
	}))
	defer outside.Close()
	ctx := context.Background()
	// The default guard: nothing local at all.
	plain := New(Options{Timeout: time.Second})
	for _, raw := range []string{inside.URL + "/", "http://localhost:" + portOf(inside) + "/", "http://169.254.169.254/",
		"http://[::1]:" + portOf(inside) + "/", "http://0.0.0.0:" + portOf(inside) + "/", "http://[::ffff:127.0.0.1]:" + portOf(inside) + "/",
		"ftp://example.com/", "https://user:pw@example.com/", "not a url"} {
		if _, err := plain.Card(ctx, raw); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	// Only the outside server is allowed: its redirects to the inside one are refused at the dial.
	rd := loopback(Options{CacheDir: t.TempDir(), Timeout: time.Second}, portOf(outside))
	for _, p := range []string{"/to-inside", "/to-name", "/to-metadata", "/to-file"} {
		if _, err := rd.Card(ctx, outside.URL+p); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", p, err)
		}
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the inside server was reached %d times", n)
	}
}

// The default reader caches under the user's cache folder.
func TestDefault(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if d := Default(); d == nil || d != Default() {
		t.Error("Default")
	}
}
