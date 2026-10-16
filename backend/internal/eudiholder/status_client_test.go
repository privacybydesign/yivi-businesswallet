package eudiholder

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

func statusGet(t *testing.T, client *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

// A status list URI is issuer-supplied: outside dev it must be https on a
// public address. A redirect is followed, up to maxStatusListRedirects.
func TestStatusClientPolicy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/list", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/list", http.StatusFound)
	})
	// /hops/n redirects n more times before it reaches the list.
	mux.HandleFunc("/hops/{n}", func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.Atoi(r.PathValue("n"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if n == 0 {
			http.Redirect(w, r, "/list", http.StatusFound)
			return
		}
		http.Redirect(w, r, "/hops/"+strconv.Itoa(n-1), http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := statusGet(t, newStatusClient(safehttp.Policy{}), srv.URL+"/list"); !errors.Is(err, safehttp.ErrNotHTTPS) {
		t.Errorf("strict http fetch = %v, want ErrNotHTTPS", err)
	}

	dev := newStatusClient(safehttp.Policy{AllowInsecureHTTP: true})
	if err := statusGet(t, dev, srv.URL+"/list"); err != nil {
		t.Errorf("dev fetch = %v, want nil", err)
	}
	if err := statusGet(t, dev, srv.URL+"/redirect"); err != nil {
		t.Errorf("redirect fetch = %v, want it followed", err)
	}
	// /hops/n is n+1 redirects in all.
	if err := statusGet(t, dev, srv.URL+"/hops/"+strconv.Itoa(maxStatusListRedirects-1)); err != nil {
		t.Errorf("fetch over %d redirects = %v, want it followed", maxStatusListRedirects, err)
	}
	if err := statusGet(t, dev, srv.URL+"/hops/"+strconv.Itoa(maxStatusListRedirects)); !errors.Is(err, errStatusListRedirects) {
		t.Errorf("fetch over %d redirects = %v, want errStatusListRedirects", maxStatusListRedirects+1, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A redirect is checked like the first request: outside dev, an https list
// that redirects to plain http is refused before that hop is sent.
func TestStatusClientChecksEachRedirect(t *testing.T) {
	var sent []string
	issuer := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent = append(sent, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"http://issuer.example/list"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	})
	client := &http.Client{
		Transport:     policyTransport{policy: safehttp.Policy{}, next: issuer},
		CheckRedirect: followStatusRedirect,
	}
	if err := statusGet(t, client, "https://issuer.example/list"); !errors.Is(err, safehttp.ErrNotHTTPS) {
		t.Errorf("downgrading redirect = %v, want ErrNotHTTPS", err)
	}
	if len(sent) != 1 {
		t.Errorf("sent %v, want only the https request", sent)
	}
}
