package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
)

func installTestLogger(buf *bytes.Buffer) func() {
	prev := slog.Default()
	inner := slog.NewJSONHandler(buf, nil)
	slog.SetDefault(slog.New(logging.NewContextHandler(inner)))
	return func() { slog.SetDefault(prev) }
}

func parseLogs(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("failed to parse log line: %v\nraw: %s", err, line)
		}
		records = append(records, m)
	}
	return records
}

func TestRequestID_Generated(t *testing.T) {
	handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	id := rec.Header().Get(headerRequestID)
	if id == "" {
		t.Fatal("expected X-Request-Id in response, got empty")
	}
	// UUID format: 8-4-4-4-12, 36 chars
	if len(id) != 36 {
		t.Fatalf("expected UUID-length ID, got %q (len %d)", id, len(id))
	}
}

func TestRequestID_Propagated(t *testing.T) {
	handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(headerRequestID, "client-abc-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(headerRequestID)
	if got != "client-abc-123" {
		t.Fatalf("expected propagated ID %q, got %q", "client-abc-123", got)
	}
}

func TestRequestID_Sanitized(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"too long", strings.Repeat("a", requestIDMaxLen+1)},
		{"bad chars", "id with spaces!"},
		{"newline injection", "id\nInjected-Header: bad"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := requestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set(headerRequestID, tc.value)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			got := rec.Header().Get(headerRequestID)
			if got == tc.value {
				t.Fatalf("expected sanitized ID, but got original %q", tc.value)
			}
			if got == "" {
				t.Fatal("expected a generated ID, got empty")
			}
		})
	}
}

func TestRequestLogger_LogsRequestWithID(t *testing.T) {
	var buf bytes.Buffer
	restore := installTestLogger(&buf)
	defer restore()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := requestID(recoverer(requestLogger(AlwaysLog)(inner)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	records := parseLogs(t, &buf)
	if len(records) == 0 {
		t.Fatal("expected at least one log record")
	}

	found := false
	for _, r := range records {
		if r["msg"] == "request" {
			found = true
			if r["request_id"] == nil || r["request_id"] == "" {
				t.Error("request log missing request_id")
			}
			if r[attrMethod] != "GET" {
				t.Errorf("expected method=GET, got %v", r[attrMethod])
			}
			if r[attrPath] != "/api/v1/test" {
				t.Errorf("expected path=/api/v1/test, got %v", r[attrPath])
			}
		}
	}
	if !found {
		t.Fatal("did not find a log record with msg=\"request\"")
	}
}

func TestRequestLogger_SkipsHealthProbes(t *testing.T) {
	var buf bytes.Buffer
	restore := installTestLogger(&buf)
	defer restore()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := requestLogger(AlwaysLog)(inner)

	for _, path := range []string{livePath, readyPath} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if buf.Len() > 0 {
			t.Errorf("expected no logs for %s, got: %s", path, buf.String())
		}
	}
}

// Validates middleware order: a panic must produce a log line that carries the
// request_id (requestID outermost, recoverer inside it).
func TestRecoverer_PanicCarriesRequestID(t *testing.T) {
	var buf bytes.Buffer
	restore := installTestLogger(&buf)
	defer restore()

	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	})

	handler := requestID(recoverer(panicking))

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}

	records := parseLogs(t, &buf)
	if len(records) == 0 {
		t.Fatal("expected a panic log record")
	}

	panicRecord := records[0]
	if panicRecord["msg"] != "panic recovered" {
		t.Fatalf("expected msg=panic recovered, got %v", panicRecord["msg"])
	}
	if panicRecord["request_id"] == nil || panicRecord["request_id"] == "" {
		t.Fatal("CRITICAL: panic log is missing request_id — middleware order is wrong")
	}
	if panicRecord[attrPanic] != "test panic" {
		t.Errorf("expected panic=test panic, got %v", panicRecord[attrPanic])
	}
}

func TestRecoverer_ReturnsInternalServerError(t *testing.T) {
	panicking := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})

	handler := recoverer(panicking)
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", rec.Code)
	}
}
