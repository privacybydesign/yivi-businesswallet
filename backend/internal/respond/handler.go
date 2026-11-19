package respond

import (
	"errors"
	"log/slog"
	"net/http"
)

const (
	internalErrorCode = "internal_error"
	internalErrorMsg  = "internal server error"
)

type HandlerFunc func(http.ResponseWriter, *http.Request) error

func (h HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := h(w, r)
	if err == nil {
		return
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		Error(w, r, apiErr.Status, apiErr.Code, apiErr.Message)
		return
	}

	if ClientGone(r) {
		return
	}

	slog.ErrorContext(r.Context(), "unhandled handler error",
		slog.String("error", err.Error()),
	)
	Error(w, r, http.StatusInternalServerError, internalErrorCode, internalErrorMsg)
}

// ClientGone reports that the page dropped the request (it navigated on, or a
// newer read replaced this one), which fails whatever the handler was doing.
// That is routine, not an error: nobody is left to answer, and logging it at
// Error would pollute the severity tier you page on.
func ClientGone(r *http.Request) bool {
	if r.Context().Err() == nil {
		return false
	}
	slog.DebugContext(r.Context(), "request dropped by the client",
		slog.String("method", r.Method), slog.String("path", r.URL.Path))
	return true
}
