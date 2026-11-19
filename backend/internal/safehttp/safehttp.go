// Package safehttp is the guarded HTTP client for every URL someone outside
// the server supplies (a verifier's request_uri, a customer's webhook endpoint):
// https only, public addresses only, no redirects, no proxy, bounded time and
// headers. It is the SSRF guard, so every such fetch goes through it.
package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

const (
	// RequestTimeout bounds one round trip, on top of the caller's ctx.
	RequestTimeout = 10 * time.Second
	// maxResponseHeaderBytes is the transport's header cap; a remote party has
	// no business sending more.
	maxResponseHeaderBytes = 64 << 10
)

var (
	ErrNotHTTPS       = errors.New("url is not https")
	ErrNotAbsolute    = errors.New("url is not absolute")
	ErrUserInfo       = errors.New("url carries userinfo")
	ErrPrivateNetwork = errors.New("host resolves to a non-public address")
	ErrRedirect       = errors.New("redirects are not followed")
)

// nonPublicPrefixes are the special-purpose ranges the net.IP predicates do not
// cover. NAT64, 6to4, Teredo and IPv4-compatible IPv6 are refused whole: the
// IPv4 address they embed could be a private one, and no legitimate endpoint is
// reached only through them. IsPublic unmaps ::ffff:a.b.c.d to plain IPv4 before
// matching, so the IPv4-mapped form is checked as the IPv4 address it carries
// and is not caught by ::/96.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),      // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),  // shared address space (CGNAT)
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, includes broadcast
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("2002::/16"),      // 6to4
	netip.MustParsePrefix("2001::/32"),      // Teredo
	netip.MustParsePrefix("::/96"),          // IPv4-compatible IPv6 (deprecated)
}

// Policy is the trust posture for an externally supplied URL. The zero value
// is production: https only, public addresses only. AllowInsecureHTTP is the
// dev-only escape hatch and lifts both, because a local party is plain http on
// a loopback address.
type Policy struct {
	AllowInsecureHTTP bool
}

// CheckURL enforces the policy on a URL before any network I/O.
func (p Policy) CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, ErrNotAbsolute
	}
	if u.User != nil {
		return nil, ErrUserInfo
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowInsecureHTTP {
			return nil, ErrNotHTTPS
		}
	default:
		return nil, ErrNotHTTPS
	}
	return u, nil
}

// NewClient builds the client every guarded round trip goes through: redirects
// disabled (a redirect from a validated URL is an attempt to move the request
// somewhere unvalidated), DNS results vetted against private ranges at dial
// time (so a hostname cannot be a disguise for 169.254.169.254), and the dialed
// address is the vetted one: no second resolution to rebind.
func NewClient(p Policy) *http.Client {
	dialer := &net.Dialer{Timeout: RequestTimeout}
	transport := &http.Transport{
		Proxy:                  nil,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    RequestTimeout,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			lastErr := ErrPrivateNetwork
			for _, ip := range ips {
				if !p.AllowInsecureHTTP && !IsPublic(ip.IP) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return ErrRedirect
		},
	}
}

// IsPublic reports whether ip is a globally routable unicast address: not
// loopback, private, link-local, multicast, unspecified, or one of the
// special-purpose ranges in nonPublicPrefixes (which Go does not classify).
func IsPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
