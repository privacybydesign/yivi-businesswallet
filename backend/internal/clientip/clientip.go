// Package clientip resolves the address a request came from behind trusted
// reverse proxies. A request carries it in context once Middleware has run;
// FromRequest falls back to the connection's peer when it has not.
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// headerForwardedFor is the header each proxy appends the address it received
// the request from to.
const headerForwardedFor = "X-Forwarded-For"

// Trusted is the parsed list of trusted proxies.
type Trusted = []netip.Prefix

// Resolver finds a request's client address. Its zero value trusts no proxy:
// every request resolves to its connection's peer.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver trusts the proxies in trusted to report, in X-Forwarded-For,
// the address they received a request from.
func NewResolver(trusted []netip.Prefix) Resolver {
	return Resolver{trusted: trusted}
}

// ClientIP is the address r came from: its peer when that is not a trusted
// proxy, otherwise the rightmost X-Forwarded-For entry that is not one. The
// entries left of it are the client's own word and never consulted. When
// every hop is trusted it is the leftmost of them; when the peer is not an
// IP at all, the peer as given.
func (res Resolver) ClientIP(r *http.Request) string {
	client, ok := peerAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !res.isTrusted(client) {
		return client.String()
	}
	hops := forwardedHops(r.Header)
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseHop(hops[i])
		if !ok {
			break
		}
		client = hop
		if !res.isTrusted(hop) {
			break
		}
	}
	return client.String()
}

// ParseTrusted reads trusted proxies as CIDR prefixes or single addresses.
func ParseTrusted(entries []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		if addr, err := netip.ParseAddr(e); err == nil {
			addr = addr.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(e)
		if err != nil {
			return nil, fmt.Errorf("clientip: trusted proxy %q is neither an address nor a CIDR prefix: %w", e, err)
		}
		prefixes = append(prefixes, p.Masked())
	}
	return prefixes, nil
}

func (res Resolver) isTrusted(addr netip.Addr) bool {
	for _, p := range res.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// forwardedHops is every X-Forwarded-For entry in order, across repeated
// headers.
func forwardedHops(h http.Header) []string {
	var hops []string
	for _, v := range h.Values(headerForwardedFor) {
		hops = append(hops, strings.Split(v, ",")...)
	}
	return hops
}

// parseHop reads one X-Forwarded-For entry: an address, optionally with a
// port or IPv6 brackets.
func parseHop(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if ap, err := netip.ParseAddrPort(raw); err == nil {
		return ap.Addr().Unmap(), true
	}
	addr, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

type contextKey struct{}

// Middleware stores each request's client address, as res resolves it, in
// its context.
func Middleware(res Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), contextKey{}, res.ClientIP(r))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromRequest is r's client address as Middleware stored it, or its peer
// when no Middleware ran: a server that trusts no proxy.
func FromRequest(r *http.Request) string {
	if ip, ok := r.Context().Value(contextKey{}).(string); ok {
		return ip
	}
	return Resolver{}.ClientIP(r)
}
