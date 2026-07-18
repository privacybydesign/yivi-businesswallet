// Package apidocs serves the OpenAPI specification for the API together with a
// browsable ReDoc page. The spec and the HTML shell are embedded into the
// binary so a single container serves the docs alongside the API and the SPA.
//
// The routes are registered on the root mux (see server.New), deliberately
// outside the versioned /api/v1 prefix so they are not mistaken for API
// resources and do not collide with the SPA fallback: ServeMux always prefers
// the more specific /api/docs patterns over "/".
package apidocs

import (
	_ "embed"
	"log/slog"
	"net/http"
)

// Route patterns registered by Register. Kept as constants so tests and the
// router assembly refer to the same strings.
const (
	UIPath   = "GET /api/docs"
	SpecPath = "GET /api/docs/openapi.yaml"
)

//go:embed openapi.yaml
var spec []byte

//go:embed redoc.html
var redoc []byte

// Register mounts the docs routes on the given mux. Both are read-only and
// unauthenticated: the spec is not sensitive and the page is a static shell.
func Register(mux *http.ServeMux) {
	mux.Handle(UIPath, uiHandler())
	mux.Handle(SpecPath, specHandler())
}

// Spec returns the raw embedded OpenAPI document. Exposed for tests.
func Spec() []byte { return spec }

func uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(redoc); err != nil {
			slog.ErrorContext(r.Context(), "apidocs: write redoc html failed",
				slog.String("error", err.Error()),
			)
		}
	})
}

func specHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		if _, err := w.Write(spec); err != nil {
			slog.ErrorContext(r.Context(), "apidocs: write openapi spec failed",
				slog.String("error", err.Error()),
			)
		}
	})
}
