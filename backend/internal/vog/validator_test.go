package vog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *HTTPClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewHTTPClient(srv.URL, &http.Client{Timeout: 5 * time.Second})
}

func writeCode(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"response_code": code})
}

func TestValidateAuthentic(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeCode(w, 0)
	})
	code, err := c.Validate(context.Background(), []byte("%PDF-fake"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if code != ResponseAuthentic {
		t.Errorf("code = %d, want %d", code, ResponseAuthentic)
	}
}

func TestValidateFinalRejectionDoesNotRetry(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeCode(w, 2)
	})
	code, err := c.Validate(context.Background(), []byte("doc"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (a final rejection must not retry)", got)
	}
}

func TestValidateRetryableThenSuccess(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			writeCode(w, 3) // retryable
			return
		}
		writeCode(w, 0)
	})
	start := time.Now()
	code, err := c.Validate(context.Background(), []byte("doc"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if code != ResponseAuthentic {
		t.Errorf("code = %d, want authentic", code)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
	if elapsed := time.Since(start); elapsed < retryBackoff[0] {
		t.Errorf("elapsed = %v, want at least the first backoff (%v)", elapsed, retryBackoff[0])
	}
}

func TestValidateExhaustsRetries(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		writeCode(w, 5) // always retryable
	})
	_, err := c.Validate(context.Background(), []byte("doc"))
	if err == nil {
		t.Fatal("Validate: want an error once every attempt is retryable")
	}
	if got := atomic.LoadInt32(&calls); int(got) != maxAttempts {
		t.Errorf("calls = %d, want %d", got, maxAttempts)
	}
}

func TestValidateServerErrorIsRetryable(t *testing.T) {
	var calls int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeCode(w, 0)
	})
	code, err := c.Validate(context.Background(), []byte("doc"))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if code != ResponseAuthentic {
		t.Errorf("code = %d, want authentic", code)
	}
}

func TestValidateSendsApplicationPDFContentType(t *testing.T) {
	var gotContentType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		file := r.MultipartForm.File["file"]
		if len(file) != 1 {
			t.Fatalf("want exactly one file part, got %d", len(file))
		}
		gotContentType = file[0].Header.Get("Content-Type")
		writeCode(w, 0)
	})
	if _, err := c.Validate(context.Background(), []byte("doc")); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if gotContentType != "application/pdf" {
		t.Errorf("part Content-Type = %q, want application/pdf (Go's octet-stream default makes GAAV answer code 2 for a genuine VOG)", gotContentType)
	}
}

func TestPingAcceptsMethodNotAllowed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v, want nil (405 on GET is the expected healthy answer)", err)
	}
}

func TestPingFailsOnServerError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := c.Ping(context.Background()); err == nil {
		t.Error("Ping: want an error on a 500")
	}
}

func TestStubValidator(t *testing.T) {
	s := StubValidator{Code: 2}
	code, err := s.Validate(context.Background(), nil)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if code != 2 {
		t.Errorf("code = %d, want 2", code)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
