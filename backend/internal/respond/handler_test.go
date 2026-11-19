package respond

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A read the page dropped fails in the handler; that is not an error to log or
// to answer.
func TestHandlerFuncStaysQuietWhenTheClientIsGone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		return fmt.Errorf("reading: %w", context.Canceled)
	}).ServeHTTP(rec, req)

	if rec.Body.Len() != 0 {
		t.Fatalf("wrote %q to a client that is gone", rec.Body.String())
	}
}

// A handler that fails while the client still waits is answered with a 500.
func TestHandlerFuncAnswersAnErrorWhileTheClientWaits(t *testing.T) {
	rec := httptest.NewRecorder()
	HandlerFunc(func(http.ResponseWriter, *http.Request) error {
		return fmt.Errorf("reading: %w", context.Canceled)
	}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
