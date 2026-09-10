package openid4vppresenter

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPolicyCheckURL(t *testing.T) {
	strict, insecure := Policy{}, Policy{AllowInsecureHTTP: true}
	cases := []struct {
		name    string
		p       Policy
		url     string
		wantErr error
	}{
		{"https ok", strict, "https://verifier.example.com/r", nil},
		{"http refused", strict, "http://verifier.example.com/r", errNotHTTPS},
		{"http allowed in dev", insecure, "http://localhost:8080/r", nil},
		{"relative", strict, "/r", errNotAbsolute},
		{"userinfo", strict, "https://user@verifier.example.com/r", errUserInfo},
		{"other scheme", insecure, "ftp://verifier.example.com/r", errNotHTTPS},
		{"javascript", insecure, "javascript:alert(1)", errNotAbsolute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.p.checkURL(tc.url)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("checkURL(%q) = %v, want nil", tc.url, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("checkURL(%q) = %v, want %v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestIsPublic(t *testing.T) {
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1"}
	for _, ip := range private {
		if isPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified public", ip)
		}
	}
	for _, ip := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if !isPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified non-public", ip)
		}
	}
}

// The default policy must refuse to dial a loopback verifier even when the URL
// is https; the dev policy reaches it. Uses a TLS test server so the scheme check
// passes and the dial-time guard is what decides.
func TestFetcherBlocksPrivateNetworkUnlessInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("a.b.c"))
	}))
	defer srv.Close()

	strict := NewFetcher(Policy{})
	if _, err := strict.Fetch(context.Background(), srv.URL); !errors.Is(err, ErrRequestURIUnreachable) || !strings.Contains(err.Error(), errPrivateNetwork.Error()) {
		t.Fatalf("strict Fetch to loopback = %v, want private-network refusal", err)
	}

	dev := NewFetcher(Policy{AllowInsecureHTTP: true})
	dev.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	body, err := dev.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("dev Fetch: %v", err)
	}
	if string(body) != "a.b.c" {
		t.Errorf("body = %q", body)
	}
}

func TestFetcherRefusesRedirectsAndOversizedBodies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, bodyLimit+1))
	})
	mux.HandleFunc("/error", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := NewFetcher(Policy{AllowInsecureHTTP: true})

	for _, path := range []string{"/redirect", "/big", "/error"} {
		if _, err := f.Fetch(context.Background(), srv.URL+path); !errors.Is(err, ErrRequestURIUnreachable) {
			t.Errorf("Fetch(%s) = %v, want ErrRequestURIUnreachable", path, err)
		}
	}
}

func TestFetcherSendsJARAccept(t *testing.T) {
	var accept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("x.y.z"))
	}))
	defer srv.Close()
	if _, err := NewFetcher(Policy{AllowInsecureHTTP: true}).Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if accept != requestObjectMediaType {
		t.Errorf("Accept = %q, want %q", accept, requestObjectMediaType)
	}
}

// A transport failure must not carry the verifier's URL into the error (and so
// into a log line): the wrapped *url.Error is reduced to its operation and cause.
func TestDoStripsURLFromTransportErrors(t *testing.T) {
	f := NewFetcher(Policy{AllowInsecureHTTP: true})
	// A port nobody listens on: the dial fails and the error would name the URL.
	_, err := f.Fetch(context.Background(), "http://127.0.0.1:1/request-object-secret")
	if err == nil {
		t.Fatal("Fetch to a closed port succeeded")
	}
	if strings.Contains(err.Error(), "request-object-secret") {
		t.Fatalf("error leaks the URL: %v", err)
	}
}
