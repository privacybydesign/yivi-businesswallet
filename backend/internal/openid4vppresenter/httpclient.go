package openid4vppresenter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	// bodyLimit caps what is read from a verifier — the Request Object or the
	// direct_post acknowledgement. The outbound client's 1 MiB cap is the
	// precedent; a JAR is a few KiB.
	bodyLimit = 1 << 20
	// requestTimeout bounds one verifier round trip, on top of the caller's ctx.
	requestTimeout = 10 * time.Second
	// maxResponseHeaderBytes is the transport's header cap; a verifier has no
	// business sending more.
	maxResponseHeaderBytes = 64 << 10
)

var (
	errNotHTTPS        = errors.New("url is not https")
	errNotAbsolute     = errors.New("url is not absolute")
	errUserInfo        = errors.New("url carries userinfo")
	errPrivateNetwork  = errors.New("host resolves to a non-public address")
	errRedirect        = errors.New("redirects are not followed")
	errBodyTooLarge    = errors.New("response body exceeds limit")
	errUnexpectedState = errors.New("unexpected status")
)

// Policy is the trust posture for every URL a verifier supplies (request_uri,
// response_uri). The zero value is production: https only, public addresses
// only. AllowInsecureHTTP is the dev-only escape hatch — the analogue of
// ATTESTATION_HOLDER_ALLOW_INSECURE_HTTP — and lifts both, because a local
// verifier is plain http on a loopback address.
type Policy struct {
	AllowInsecureHTTP bool
}

// checkURL enforces the policy on a verifier-supplied URL before any network
// I/O. It is the first SSRF guard: nothing in the repo fetched an externally
// supplied URL server-side before this slice.
func (p Policy) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if !u.IsAbs() || u.Host == "" {
		return nil, errNotAbsolute
	}
	if u.User != nil {
		return nil, errUserInfo
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !p.AllowInsecureHTTP {
			return nil, errNotHTTPS
		}
	default:
		return nil, errNotHTTPS
	}
	return u, nil
}

// newClient builds the http.Client every verifier round trip goes through:
// redirects disabled (a redirect from a validated URL is an attempt to move the
// request somewhere unvalidated), DNS results vetted against private ranges at
// dial time (so a hostname cannot be a disguise for 169.254.169.254), and the
// dialed address is the vetted one — no second resolution to rebind.
func newClient(p Policy) *http.Client {
	dialer := &net.Dialer{Timeout: requestTimeout}
	transport := &http.Transport{
		Proxy:                  nil,
		MaxResponseHeaderBytes: maxResponseHeaderBytes,
		TLSHandshakeTimeout:    requestTimeout,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			lastErr := errPrivateNetwork
			for _, ip := range ips {
				if !p.AllowInsecureHTTP && !isPublic(ip.IP) {
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
		Timeout:   requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirect
		},
	}
}

// isPublic reports whether ip is a globally routable unicast address — not
// loopback, private, link-local, multicast, unspecified, or the shared/CGNAT
// range (which Go does not classify as private).
func isPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return false // 100.64.0.0/10
	}
	return true
}

// readBody reads at most bodyLimit bytes and fails if the body is larger, rather
// than silently truncating a Request Object into something that still parses.
func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, bodyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(b) > bodyLimit {
		return nil, errBodyTooLarge
	}
	return b, nil
}

// do performs one guarded request and returns the response body of a 2xx. A
// transport error is stripped of its URL: net/http's *url.Error carries the full
// target, and the request_uri / response_uri must never reach a log line.
func do(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w %d", errUnexpectedState, resp.StatusCode)
	}
	return readBody(resp.Body)
}
