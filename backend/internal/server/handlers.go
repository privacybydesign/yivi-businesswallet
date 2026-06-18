package server

import (
	"log"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

func health(db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := db.Ping(r.Context()); err != nil {
			log.Printf("health: database ping: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte(`{"status": "unavailable"}`)); err != nil {
				log.Printf("health: write response: %v", err)
			}
			return
		}
		if _, err := w.Write([]byte(`{"status": "ok"}`)); err != nil {
			log.Printf("health: write response: %v", err)
		}
	}
}

func ping(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"message": "pong"}`)); err != nil {
		log.Printf("ping: write response: %v", err)
	}
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}
