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

	slog.ErrorContext(r.Context(), "unhandled handler error",
		slog.String("error", err.Error()),
	)
	Error(w, r, http.StatusInternalServerError, internalErrorCode, internalErrorMsg)
}
