// Package linkcard reads what a public web page says about itself, its title, site and picture, for a script's link
// cards (spec/api.md, "Link preview"; spec/scripts.md, "Embeds"). It never connects to an address that isn't public:
// the dialer judges every address it connects to, after DNS and on every redirect.
package linkcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Card is what a page says about itself. A field it doesn't say is "".
type Card struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Site        string `json:"site"`
	Image       string `json:"image"`
	Description string `json:"description"`
}

// The limits of a read (spec/api.md, "Link preview").
const (
	MaxURL       = 2048
	MaxBody      = 1 << 20
	Timeout      = 5 * time.Second
	MaxRedirects = 5
	// TTL is how long a card is kept on disk; FailTTL how long a page that couldn't be read is.
	TTL     = 7 * 24 * time.Hour
	FailTTL = 10 * time.Minute
	// Parallel is how many pages are read at once.
	Parallel  = 4
	UserAgent = "Mozilla/5.0 (compatible; Retake link preview)"
)

var (
	// ErrRefused is an address a preview never reads: not a public http(s) address.
	ErrRefused = errors.New("not a public web address")
	// ErrUnreadable is a page that couldn't be read: it didn't answer in time, answered an error, or redirected too
	// often.
	ErrUnreadable = errors.New("can't read that page")
)

// Options make a Reader.
type Options struct {
	// CacheDir keeps the cards on disk, one JSON file per address; "" keeps none.
	CacheDir string
	// Allow says whether the reader may connect to an address; nil is Public. Tests allow their own local server.
	Allow func(netip.AddrPort) bool
	// Timeout is the whole read's, redirects included; 0 is Timeout.
	Timeout time.Duration
	// Now is the clock of the cache; nil is time.Now.
	Now func() time.Time
}

// Reader reads link cards. Make one with New; it is safe for concurrent use.
type Reader struct {
	dir     string
	allow   func(netip.AddrPort) bool
	timeout time.Duration
	now     func() time.Time
	client  *http.Client
	slots   chan struct{}
}

// New makes a Reader.
func New(o Options) *Reader {
	r := &Reader{dir: o.CacheDir, allow: o.Allow, timeout: o.Timeout, now: o.Now, slots: make(chan struct{}, Parallel)}
	if r.allow == nil {
		r.allow = func(ap netip.AddrPort) bool { return Public(ap.Addr()) }
	}
	if r.timeout <= 0 {
		r.timeout = Timeout
	}
	if r.now == nil {
		r.now = time.Now
	}
	dialer := &net.Dialer{Timeout: r.timeout, ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil || !r.allow(ap) {
			return ErrRefused
		}
		return nil
	}}
	r.client = &http.Client{
		Timeout: r.timeout,
		Transport: &http.Transport{
			Proxy:                  nil, // a proxy would connect for us, past the dialer's check
			DialContext:            dialer.DialContext,
			ForceAttemptHTTP2:      true,
			TLSHandshakeTimeout:    r.timeout,
			ResponseHeaderTimeout:  r.timeout,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConns:           8,
			IdleConnTimeout:        30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return ErrUnreadable
			}
			return checkURL(req.URL)
		},
	}
	return r
}

var (
	defaultOnce   sync.Once
	defaultReader *Reader
)

// Default is the Reader of a server that wasn't given one: its cache is retake/link-preview/ in the user's cache
// folder ($XDG_CACHE_HOME, else ~/.cache), or none when there is no such folder.
func Default() *Reader {
	defaultOnce.Do(func() {
		dir := ""
		if c, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(c, "retake", "link-preview")
		}
		defaultReader = New(Options{CacheDir: dir})
	})
	return defaultReader
}

// Card reads the page at raw: from the cache when it was read lately, else from the web. It answers ErrRefused for
// an address that isn't public, ErrUnreadable for a page that couldn't be read.
func (r *Reader) Card(ctx context.Context, raw string) (Card, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > MaxURL {
		return Card{}, ErrRefused
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Card{}, ErrRefused
	}
	u.Fragment, u.RawFragment = "", ""
	if err := checkURL(u); err != nil {
		return Card{}, err
	}
	if ip, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil && !r.allow(netip.AddrPortFrom(ip, port(u))) {
		return Card{}, ErrRefused // an address given as an IP is refused before anything is sent
	}
	key := u.String()
	if c, ok, hit := r.cached(key); hit {
		if !ok {
			return Card{}, ErrUnreadable
		}
		return c, nil
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return Card{}, ErrUnreadable
	}
	c, err := r.read(ctx, u)
	switch {
	case errors.Is(err, ErrRefused):
		return Card{}, ErrRefused
	case err != nil:
		r.keep(key, Card{}, false)
		return Card{}, ErrUnreadable
	}
	r.keep(key, c, true)
	return c, nil
}

func port(u *url.URL) uint16 {
	if p := u.Port(); p != "" {
		var n uint16
		if _, err := fmt.Sscan(p, &n); err == nil {
			return n
		}
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

// read gets the page and reads its head.
func (r *Reader) read(ctx context.Context, u *url.URL) (Card, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Card{}, ErrRefused
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	res, err := r.client.Do(req)
	if err != nil {
		return Card{}, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return Card{}, fmt.Errorf("%w: %s", ErrUnreadable, res.Status)
	}
	final := res.Request.URL
	c := Card{URL: u.String()}
	if mt, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type")); mt == "text/html" || mt == "application/xhtml+xml" {
		c = Parse(io.LimitReader(res.Body, MaxBody), final)
		c.URL = u.String()
	}
	if c.Site == "" {
		c.Site = cut(strings.TrimPrefix(strings.ToLower(u.Hostname()), "www."), maxSite)
	}
	return c, nil
}

// --- the disk cache

type entry struct {
	Card Card  `json:"card"`
	OK   bool  `json:"ok"`
	At   int64 `json:"at"` // Unix seconds
}

func (r *Reader) file(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(r.dir, hex.EncodeToString(sum[:])+".json")
}

// cached is the card kept for key: ok is whether the page could be read, hit whether there is a fresh entry.
func (r *Reader) cached(key string) (c Card, ok, hit bool) {
	if r.dir == "" {
		return Card{}, false, false
	}
	b, err := os.ReadFile(r.file(key))
	if err != nil {
		return Card{}, false, false
	}
	var e entry
	if json.Unmarshal(b, &e) != nil || e.OK && e.Card.URL != key {
		return Card{}, false, false
	}
	ttl := FailTTL
	if e.OK {
		ttl = TTL
	}
	if age := r.now().Sub(time.Unix(e.At, 0)); age < 0 || age >= ttl {
		return Card{}, false, false
	}
	return e.Card, e.OK, true
}

// keep writes key's entry (a temp file and a rename). A cache that can't be written is no cache.
func (r *Reader) keep(key string, c Card, ok bool) {
	if r.dir == "" || os.MkdirAll(r.dir, 0o700) != nil {
		return
	}
	b, _ := json.Marshal(entry{Card: c, OK: ok, At: r.now().Unix()})
	f, err := os.CreateTemp(r.dir, ".card-*")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr != nil || cerr != nil || os.Rename(f.Name(), r.file(key)) != nil {
		_ = os.Remove(f.Name())
	}
}
