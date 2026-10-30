package hub

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Requests: the public URL, trusted proxies, the client's address and the CSRF check (spec/server.md, "Requests"
// and "CSRF").

// Proxies is a list of trusted proxy ranges.
type Proxies []netip.Prefix

// ParseProxies parses trusted proxy addresses and ranges: 10.0.0.0/8, 172.17.0.1, ::1.
func ParseProxies(list []string) (Proxies, error) {
	var out Proxies
	for _, raw := range list {
		for s := range strings.SplitSeq(raw, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if p, err := netip.ParsePrefix(s); err == nil {
				out = append(out, p.Masked())
				continue
			}
			a, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: give an address or a range like 10.0.0.0/8", s)
			}
			out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	return out, nil
}

// Has reports whether addr is a trusted proxy.
func (ps Proxies) Has(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range ps {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// PublicURL is the parsed public URL: its origin, scheme and host.
type PublicURL struct {
	Origin string // https://review.example.com[:port]
	Scheme string
	Host   string // review.example.com[:port], lower case
}

// ParsePublicURL checks a public URL: http or https, a host, and no path beyond "/".
func ParsePublicURL(s string) (PublicURL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return PublicURL{}, nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return PublicURL{}, fmt.Errorf("public URL %q: give the origin people use, like https://review.example.com", s)
	}
	host := strings.ToLower(u.Host)
	return PublicURL{Origin: u.Scheme + "://" + host, Scheme: u.Scheme, Host: host}, nil
}

// Loopback reports whether the public URL's host is localhost or a loopback address.
func (p PublicURL) Loopback() bool {
	h := p.Host
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && a.IsLoopback()
}

// reqInfo is what the server makes of a request's network side.
type reqInfo struct {
	client string // the client's address, for rate limits
	proto  string // http or https
	host   string // lower case
	origin string // our origin for this request: the public URL's, else proto://host
}

// inspect reads the client's address, protocol and host, believing forwarding headers only from trusted proxies.
func (h *Hub) inspect(r *http.Request) reqInfo {
	ri := reqInfo{proto: "http", host: strings.ToLower(r.Host)}
	if r.TLS != nil {
		ri.proto = "https"
	}
	peerText := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		peerText = host
	}
	ri.client = peerText
	peer, err := netip.ParseAddr(peerText)
	if err == nil && h.proxies.Has(peer) {
		if c := forwardedClient(r, h.proxies); c != "" {
			ri.client = c
		}
		if p := strings.ToLower(strings.TrimSpace(firstValue(r.Header.Get("X-Forwarded-Proto")))); p == "http" ||
			p == "https" {
			ri.proto = p
		}
		if fh := strings.ToLower(strings.TrimSpace(firstValue(r.Header.Get("X-Forwarded-Host")))); fh != "" {
			ri.host = fh
		}
	}
	ri.origin = ri.proto + "://" + ri.host
	if h.public.Origin != "" {
		ri.origin = h.public.Origin
	}
	return ri
}

func firstValue(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return first
}

// forwardedClient walks X-Forwarded-For from the right and returns the first address that isn't a trusted proxy
// (or the left-most one when they all are). "" when there is no usable header.
func forwardedClient(r *http.Request, proxies Proxies) string {
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for s := range strings.SplitSeq(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				hops = append(hops, s)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err != nil {
			return "" // a garbled chain: count the proxy itself
		}
		if !proxies.Has(a) || i == 0 {
			return a.Unmap().String()
		}
	}
	return ""
}

// hostOK reports whether the request's host is ours: the public URL's host, or any host without a public URL.
func (h *Hub) hostOK(ri reqInfo) bool { return h.public.Host == "" || ri.host == h.public.Host }

// stateChanging reports whether a method changes state, so it needs the CSRF check.
func stateChanging(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// sameOrigin is the CSRF check of a state-changing request that carries no Bearer token: its Origin must be ours;
// without one, Sec-Fetch-Site must say same-origin (or none: the person typed it); without either, it's refused.
func sameOrigin(r *http.Request, ri reqInfo) bool {
	if vals := r.Header.Values("Origin"); len(vals) > 0 {
		return len(vals) == 1 && strings.EqualFold(vals[0], ri.origin)
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	}
	return false
}

// bearer returns a request's Bearer token, or "".
func bearer(r *http.Request) string {
	scheme, v, _ := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(v)
	}
	return ""
}
