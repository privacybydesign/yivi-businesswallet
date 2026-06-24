package server

import (
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
)

// Header and log-attribute constants.
const (
	headerRequestID = "X-Request-Id"
	attrMethod      = "method"
	attrPath        = "path"
	attrStatus      = "status"
	attrDurationMS  = "duration_ms"
	attrPanic       = "panic"
	attrStack       = "stack"

	// requestIDMaxLen caps the length of a client-supplied X-Request-Id to
	// prevent unbounded strings in logs and response headers.
	requestIDMaxLen = 64
)

// requestIDPattern restricts accepted X-Request-Id values to a safe charset:
// alphanumeric, hyphens, and underscores.
var requestIDPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`)

// ShouldLog is a predicate that controls whether a completed request is
// logged by requestLogger. It provides the sampling seam: today it always
// returns true; replace with a rate-limiting or sampling predicate when
// throughput warrants it.
type ShouldLog func(status int, duration time.Duration) bool

// AlwaysLog is the default ShouldLog predicate — every non-skipped request is
// logged.
func AlwaysLog(_ int, _ time.Duration) bool { return true }

// skipPaths contains paths excluded from request logging (health probes).
var skipPaths = map[string]struct{}{
	livePath:  {},
	readyPath: {},
}

// requestID is the outermost middleware. It reads an incoming X-Request-Id (if
// valid), or generates a fresh UUID, stores it in the request context via
// logging.ContextWithRequestID, and echoes it on the response header.
//
// This middleware is intentionally NOT wrapped by recoverer because it has no
// panic surface (UUID generation + header reads). Placing it outermost ensures
// every downstream middleware (including recoverer) has the ID in context.
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

// sanitizeRequestID validates a client-supplied request ID. It returns the ID
// if it is non-empty, within the length limit, and matches the safe charset;
// otherwise it returns an empty string, causing the caller to generate a fresh
// UUID.
func sanitizeRequestID(raw string) string {
	if raw == "" || len(raw) > requestIDMaxLen || !requestIDPattern.MatchString(raw) {
		return ""
	}
	return raw
}

// recoverer catches panics from downstream handlers, logs the panic value and
// stack trace with the request ID (available in context because requestID runs
// outermost), and returns a 500 response instead of dropping the connection.
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
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestLogger logs every completed request (except health-probe paths) with
// method, path, status, and duration. The request_id is injected
// automatically by the context-aware slog handler from the context set by
// requestID.
//
// The shouldLog predicate is the sampling seam: pass AlwaysLog for
// unconditional logging, or a custom predicate for rate-limited/sampled
// logging when throughput warrants it.
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

// statusRecorder wraps http.ResponseWriter to capture the response status code
// for logging. It records only the first call to WriteHeader.
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

// Unwrap exposes the underlying ResponseWriter so that callers using
// http.ResponseController or interface assertions (e.g. http.Flusher) still
// work through the wrapper.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// chain applies middleware in order: the first argument is outermost.
//
//	chain(a, b, c)(handler) == a(b(c(handler)))
func chain(mws ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			h = mws[i](h)
		}
		return h
	}
}

// defaultMiddleware returns the standard middleware chain in the correct order:
//
//	requestID → recoverer → requestLogger
//
// requestID is outermost so the ID is in context before recoverer and
// requestLogger run. recoverer catches panics from requestLogger and the
// handler, and its panic log carries the request_id. requestLogger is
// innermost so it measures only handler time.
func defaultMiddleware() func(http.Handler) http.Handler {
	return chain(
		requestID,
		recoverer,
		requestLogger(AlwaysLog),
	)
}
