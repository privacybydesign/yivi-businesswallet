package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const (
	livePath    = "/livez"
	readyPath   = "/readyz"
	apiV1Prefix = "/api/v1"

	readTimeout = 2 * time.Second
)

type Pinger interface {
	Ping(context.Context) error
}

type Registerer interface {
	Register(*http.ServeMux)
}

func New(db Pinger, features ...Registerer) http.Handler {
	root := http.NewServeMux()

	root.HandleFunc(livePath, live)
	root.HandleFunc(readyPath, ready(db))

	v1 := http.NewServeMux()
	for _, f := range features {
		f.Register(v1)
	}
	root.Handle(apiV1Prefix+"/", http.StripPrefix(apiV1Prefix, v1))

	return defaultMiddleware()(root)
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
