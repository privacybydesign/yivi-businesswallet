package server

import (
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

const (
	headerRequestID = "X-Request-Id"
	attrMethod      = "method"
	attrPath        = "path"
	attrStatus      = "status"
	attrDurationMS  = "duration_ms"
	attrPanic       = "panic"
	attrStack       = "stack"

	// Caps length to prevent unbounded strings in logs and response headers.
	requestIDMaxLen = 64
)

// Restricts accepted values to a safe charset to avoid header/log injection.
var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

// ShouldLog is the sampling seam: replace with a rate-limiting predicate when
// throughput warrants it.
type ShouldLog func(status int, duration time.Duration) bool

func AlwaysLog(_ int, _ time.Duration) bool { return true }

var skipPaths = map[string]struct{}{
	livePath:  {},
	readyPath: {},
}

// Outermost middleware — NOT wrapped by recoverer because it has no panic
// surface. Placing it outermost ensures every downstream middleware has the
// request ID in context.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(headerRequestID))
		if id == "" {
			id = uuid.New().String()
		}

		ctx := logging.ContextWithRequestID(r.Context(), id)
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sanitizeRequestID(raw string) string {
	if raw == "" || len(raw) > requestIDMaxLen || !requestIDPattern.MatchString(raw) {
		return ""
	}
	return raw
}

// Logs panic with request_id (available because requestID runs outermost)
// and returns 500 instead of dropping the connection.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				stack := string(debug.Stack())
				slog.ErrorContext(r.Context(), "panic recovered",
					slog.Any(attrPanic, v),
					slog.String(attrStack, stack),
					slog.String(attrMethod, r.Method),
					slog.String(attrPath, r.URL.Path),
				)
				respond.Error(w, r, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// shouldLog is the sampling seam: pass AlwaysLog for unconditional logging, or
// a custom predicate for rate-limited logging when throughput warrants it.
func requestLogger(shouldLog ShouldLog) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip health probes to avoid flooding logs.
			if _, skip := skipPaths[r.URL.Path]; skip {
				next.ServeHTTP(w, r)
				return
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()

			next.ServeHTTP(rec, r)

			duration := time.Since(start)
			if shouldLog(rec.status, duration) {
				slog.InfoContext(r.Context(), "request",
					slog.String(attrMethod, r.Method),
					slog.String(attrPath, r.URL.Path),
					slog.Int(attrStatus, rec.status),
					slog.Float64(attrDurationMS, float64(duration.Microseconds())/1000.0),
				)
			}
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.wroteHeader = true
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap is required so http.ResponseController and interface assertions
// (e.g. http.Flusher) work through the wrapper.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// chain(a, b, c)(handler) == a(b(c(handler)))
func chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

// Order matters: requestID outermost so the ID is in context before recoverer
// and requestLogger run. recoverer wraps requestLogger so its panic log
// carries request_id. requestLogger is innermost so it measures only handler
// time.
func defaultMiddleware() func(http.Handler) http.Handler {
	return chain(
		requestID,
		recoverer,
		requestLogger(AlwaysLog),
	)
}
