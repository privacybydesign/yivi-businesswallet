package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

const (
	serverAddr = ":8080"
	healthPath = "/healthz"

	healthcheckURL     = "http://localhost" + serverAddr + healthPath
	healthcheckTimeout = 2 * time.Second
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit (used by container HEALTHCHECK)")
	flag.Parse()
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	router := mux.NewRouter()
	router.Use(logging)

	router.HandleFunc(healthPath, health).Methods(http.MethodGet)

	api := router.PathPrefix("/api/v1").Subrouter()
	api.HandleFunc("/ping", ping).Methods(http.MethodGet)

	server := &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("Listening on port %s\n", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}

// runHealthcheck probes the local health endpoint and returns a process exit
// code: 0 when the server reports healthy, 1 otherwise. It lets the compiled
// binary act as its own container HEALTHCHECK in images without a shell or
// HTTP client (e.g. distroless).
func runHealthcheck() int {
	client := &http.Client{Timeout: healthcheckTimeout}
	resp, err := client.Get(healthcheckURL)
	if err != nil {
		log.Printf("healthcheck: get %s: %v", healthcheckURL, err)
		return 1
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("healthcheck: close response body: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		log.Printf("healthcheck: unexpected status %d", resp.StatusCode)
		return 1
	}
	return 0
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"status": "ok"}`)); err != nil {
		log.Printf("health: write response: %v", err)
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
