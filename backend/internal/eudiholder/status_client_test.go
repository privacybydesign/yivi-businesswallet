package eudiholder

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
// public address, and a redirect is never followed.
func TestStatusClientPolicy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/list", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/list", http.StatusFound)
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
	if err := statusGet(t, dev, srv.URL+"/redirect"); !errors.Is(err, safehttp.ErrRedirect) {
		t.Errorf("redirect fetch = %v, want ErrRedirect", err)
	}
}
