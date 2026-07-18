package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/apidocs"
)

const (
	livePath    = "/livez"
	readyPath   = "/readyz"
	apiV1Prefix = "/api/v1"
	rootPath    = "/"
	spaIndex    = "index.html"

	readTimeout = 2 * time.Second
)

type Pinger interface {
	Ping(context.Context) error
}

type Registerer interface {
	Register(*http.ServeMux)
}

// New builds the root handler. When staticDir is non-empty the built frontend
// is served from it as a single-page application on "/", so one container can
// serve both the API and the SPA; when empty (e.g. dev, where Vite serves the
// frontend) no static handler is mounted and unmatched paths 404.
func New(db Pinger, staticDir string, features ...Registerer) http.Handler {
	root := http.NewServeMux()

	root.HandleFunc(livePath, live)
	root.HandleFunc(readyPath, ready(db))

	// API documentation: a ReDoc page and the raw OpenAPI spec. Mounted on the
	// root mux under /api/docs (not /api/v1) so they are not mistaken for API
	// resources; the more specific patterns win over the SPA fallback.
	apidocs.Register(root)

	v1 := http.NewServeMux()
	for _, f := range features {
		f.Register(v1)
	}

	// defaultMiddleware wraps outside StripPrefix so the request logger sees the
	// full /api/v1/... path, while the feature handlers receive stripped paths.
	apiHandler := defaultMiddleware()(http.StripPrefix(apiV1Prefix, v1))
	root.Handle(apiV1Prefix+"/", apiHandler)

	// The SPA catches every path not matched by a more specific pattern above.
	// ServeMux precedence guarantees /api/v1/, /livez and /readyz still win.
	if staticDir != "" {
		root.Handle(rootPath, spaHandler{staticPath: staticDir, indexPath: spaIndex})
	}

	return root
}

func live(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func ready(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			slog.ErrorContext(r.Context(), "readiness probe failed",
				slog.String("error", err.Error()),
			)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}
