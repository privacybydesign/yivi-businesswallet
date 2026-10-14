package clientip

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func mustTrusted(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	prefixes, err := ParseTrusted(entries)
	if err != nil {
		t.Fatalf("ParseTrusted(%v): %v", entries, err)
	}
	return prefixes
}

func request(remoteAddr string, forwardedFor ...string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	for _, v := range forwardedFor {
		r.Header.Add(headerForwardedFor, v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	ingress := []string{"10.0.0.0/8", "fd00::/8"}
	for _, tc := range []struct {
		name         string
		trusted      []string
		remoteAddr   string
		forwardedFor []string
		want         string
	}{
		{
			name:       "no trusted proxies: the peer, X-Forwarded-For ignored",
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"203.0.113.7"},
			want: "10.1.2.3",
		},
		{
			name: "untrusted peer: its own address, X-Forwarded-For ignored", trusted: ingress,
			remoteAddr: "198.51.100.9:4567", forwardedFor: []string{"203.0.113.7"},
			want: "198.51.100.9",
		},
		{
			name: "trusted peer, no header: the peer", trusted: ingress,
			remoteAddr: "10.1.2.3:4567",
			want:       "10.1.2.3",
		},
		{
			name: "trusted peer: the address it reports", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name: "spoofed leftmost entries are never used", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"1.2.3.4, 5.6.7.8, 203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name: "several trusted hops are skipped", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"1.2.3.4, 203.0.113.7, 10.9.9.9, 10.8.8.8"},
			want: "203.0.113.7",
		},
		{
			name: "entries across repeated headers", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"1.2.3.4", "203.0.113.7, 10.9.9.9"},
			want: "203.0.113.7",
		},
		{
			name: "every hop trusted: the leftmost of them", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"10.7.7.7, 10.9.9.9"},
			want: "10.7.7.7",
		},
		{
			name: "garbage left of a trusted hop: the nearest valid hop", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"not-an-ip, 10.9.9.9"},
			want: "10.9.9.9",
		},
		{
			name: "garbage left of the client is never reached", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"not-an-ip, 203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name: "IPv6 peer and client", trusted: ingress,
			remoteAddr: "[fd00::1]:4567", forwardedFor: []string{"2001:db8::1, fd00::2"},
			want: "2001:db8::1",
		},
		{
			name: "IPv6 entry with brackets and port", trusted: ingress,
			remoteAddr: "[fd00::1]:4567", forwardedFor: []string{"[2001:db8::1]:443"},
			want: "2001:db8::1",
		},
		{
			name: "IPv4 entry with a port", trusted: ingress,
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"203.0.113.7:51000"},
			want: "203.0.113.7",
		},
		{
			name: "IPv4-mapped IPv6 peer matches an IPv4 prefix", trusted: ingress,
			remoteAddr: "[::ffff:10.1.2.3]:4567", forwardedFor: []string{"203.0.113.7"},
			want: "203.0.113.7",
		},
		{
			name: "single trusted address", trusted: []string{"10.1.2.3"},
			remoteAddr: "10.1.2.3:4567", forwardedFor: []string{"203.0.113.7, 10.1.2.4"},
			want: "10.1.2.4",
		},
		{
			name:       "peer without a port",
			remoteAddr: "203.0.113.7",
			want:       "203.0.113.7",
		},
		{
			name:       "peer that is no IP: as given",
			remoteAddr: "pipe",
			want:       "pipe",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := NewResolver(mustTrusted(t, tc.trusted...))
			if got := res.ClientIP(request(tc.remoteAddr, tc.forwardedFor...)); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseTrusted(t *testing.T) {
	got, err := ParseTrusted([]string{"10.0.0.0/8", "192.168.1.7", "fd00::/8", "10.1.2.3/8"})
	if err != nil {
		t.Fatalf("ParseTrusted: %v", err)
	}
	want := []string{"10.0.0.0/8", "192.168.1.7/32", "fd00::/8", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("ParseTrusted = %v, want %v", got, want)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("prefix %d = %s, want %s", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "ingress", ""} {
		if _, err := ParseTrusted([]string{bad}); err == nil {
			t.Errorf("ParseTrusted(%q) accepted it", bad)
		}
	}
}

func TestMiddlewareAndFromRequest(t *testing.T) {
	var got string
	h := Middleware(NewResolver(mustTrusted(t, "10.0.0.0/8")))(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = FromRequest(r)
	}))
	h.ServeHTTP(httptest.NewRecorder(), request("10.1.2.3:4567", "1.2.3.4, 203.0.113.7"))
	if got != "203.0.113.7" {
		t.Errorf("behind Middleware: FromRequest = %q, want the client", got)
	}

	if got := FromRequest(request("10.1.2.3:4567", "203.0.113.7")); got != "10.1.2.3" {
		t.Errorf("without Middleware: FromRequest = %q, want the peer", got)
	}
}
