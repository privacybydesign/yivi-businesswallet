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
// address is the vetted one — no second resolution to rebind.
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

// IsPublic reports whether ip is a globally routable unicast address — not
// loopback, private, link-local, multicast, unspecified, or the shared/CGNAT
// range (which Go does not classify as private).
func IsPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false // 100.64.0.0/10
	}
	return true
}
