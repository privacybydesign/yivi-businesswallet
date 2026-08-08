package csc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientInfoParsesProviderInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != infoPath {
			t.Errorf("path = %q, want %q", r.URL.Path, infoPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"specs":"2.2.0.0","name":"Test QTSP","region":"EU"}`))
	}))
	defer server.Close()

	info, err := NewClient().Info(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != "Test QTSP" || info.Specs != "2.2.0.0" {
		t.Errorf("Info = %+v, want name=Test QTSP specs=2.2.0.0", info)
	}
}

// A 2xx that decodes as a JSON object but lacks the required CSC v2 /info fields
// (name, specs) is a failed test, not a blank success — otherwise any endpoint the
// probe happens to reach would read as "connected".
func TestClientInfoRejectsEmptyInfo(t *testing.T) {
	for _, body := range []string{`{"ok":true}`, `{"name":"","specs":""}`, `{"specs":"2.2.0.0"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		_, err := NewClient().Info(context.Background(), server.URL)
		server.Close()
		var testErr *TestError
		if !errors.As(err, &testErr) {
			t.Fatalf("Info(%s) error = %v, want *TestError", body, err)
		}
	}
}

// A non-2xx answer becomes a TestError carrying the status code only — never the
// far side's error document, and never the URL that would have been reachable.
func TestClientInfoRedactsNon2xx(t *testing.T) {
	const secretPath = "/super-secret-path"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"leaky internal detail"}`))
	}))
	defer server.Close()

	_, err := NewClient().Info(context.Background(), server.URL+secretPath)
	var testErr *TestError
	if !errors.As(err, &testErr) {
		t.Fatalf("Info error = %v, want *TestError", err)
	}
	if !strings.Contains(testErr.Reason, "500") {
		t.Errorf("reason %q should name the status code", testErr.Reason)
	}
	for _, leak := range []string{"leaky internal detail", secretPath, server.URL} {
		if strings.Contains(testErr.Reason, leak) {
			t.Errorf("reason %q leaked %q", testErr.Reason, leak)
		}
	}
}

// An unreachable endpoint becomes a TestError that names neither the URL (which
// net/http would put in its transport error) nor any far-side bytes.
func TestClientInfoRedactsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close() // now unreachable

	_, err := NewClient().Info(context.Background(), url)
	var testErr *TestError
	if !errors.As(err, &testErr) {
		t.Fatalf("Info error = %v, want *TestError", err)
	}
	if strings.Contains(testErr.Reason, url) {
		t.Errorf("reason %q leaked the URL", testErr.Reason)
	}
}
