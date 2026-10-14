package openid4vppresenter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

// The default policy must refuse to dial a loopback verifier even when the URL
// is https; the dev policy reaches it. Uses a TLS test server so the scheme check
// passes and the dial-time guard is what decides.
func TestFetcherBlocksPrivateNetworkUnlessInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("a.b.c"))
	}))
	defer srv.Close()

	strict := NewFetcher(Policy{})
	if _, err := strict.Fetch(context.Background(), srv.URL); !errors.Is(err, ErrRequestURIUnreachable) || !strings.Contains(err.Error(), safehttp.ErrPrivateNetwork.Error()) {
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
