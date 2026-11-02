package linkcard

import (
	"net/netip"
	"net/url"
	"strings"
)

// reserved are the address ranges a link preview never connects to besides what netip's own tests catch (loopback,
// private, link-local, multicast, unspecified): shared, benchmarking, documentation and the other special-purpose
// blocks of RFC 6890 and its updates.
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // shared address space (carrier NAT, Tailscale; Alibaba's metadata)
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, and the broadcast address
	netip.MustParsePrefix("100::/64"),        // discard
	netip.MustParsePrefix("2001::/23"),       // IETF protocol assignments (Teredo among them)
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("fec0::/10"),       // site-local (deprecated)
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64: where it puts the IPv4 address is the network's own
}

// The IPv6 ranges that carry an IPv4 address in their last 32 bits (NAT64) or in bits 16–47 (6to4): such an address
// is judged by the IPv4 address it reaches.
var (
	nat64  = netip.MustParsePrefix("64:ff9b::/96")
	sixTo4 = netip.MustParsePrefix("2002::/16")
)

// Public reports whether a link preview may connect to addr: an ordinary public unicast address. Loopback, private
// (10/8, 172.16/12, 192.168/16, fc00::/7), link-local (169.254/16, so the cloud metadata address 169.254.169.254, and
// fe80::/10), multicast, unspecified and the reserved blocks above are not, and neither is an IPv6 address that holds
// one of them as IPv4 (::ffff:127.0.0.1, 64:ff9b::a9fe:a9fe, 2002:7f00:1::).
func Public(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.Is6() {
		b := addr.As16()
		if nat64.Contains(addr) {
			return Public(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if sixTo4.Contains(addr) {
			return Public(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
		if addr.Zone() != "" {
			return false
		}
	}
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// checkURL refuses an address a link preview never reads: not http or https, no host, a user or password in it, or
// longer than MaxURL. It doesn't look the host up: the dialer judges the address it connects to.
func checkURL(u *url.URL) error {
	switch {
	case u == nil || len(u.String()) > MaxURL:
		return ErrRefused
	case u.Scheme != "http" && u.Scheme != "https":
		return ErrRefused
	case u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "":
		return ErrRefused
	case strings.ContainsAny(u.Hostname(), " \t\r\n"):
		return ErrRefused
	}
	return nil
}
