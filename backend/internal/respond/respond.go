package respond

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

const contentTypeJSON = "application/json"

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func JSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	buf, err := json.Marshal(v)
	if err != nil {
		slog.ErrorContext(r.Context(), "respond: marshal failed",
			slog.String("error", err.Error()),
		)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if _, err := w.Write(buf); err != nil {
		logWriteError(r.Context(), err)
	}
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	JSON(w, r, status, errorBody{Error: msg, Code: code})
}

// logWriteError logs at Warn for client disconnects — logging those at Error
// would pollute the severity tier you page on.
func logWriteError(ctx context.Context, err error) {
	if errors.Is(err, context.Canceled) {
		slog.WarnContext(ctx, "respond: client disconnected",
			slog.String("error", err.Error()),
		)
		return
	}

	slog.ErrorContext(ctx, "respond: write failed",
		slog.String("error", err.Error()),
	)
}
