package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

const (
	serverAddr = ":8080"
	healthPath = "/healthz"
	apiPrefix  = "/api/v1"

	readTimeout     = 5 * time.Second
	writeTimeout    = 10 * time.Second
	idleTimeout     = 60 * time.Second
	shutdownTimeout = 10 * time.Second
)

func New(db *database.DB) *http.Server {
	router := mux.NewRouter()
	router.Use(logging)
	router.HandleFunc(healthPath, health(db)).Methods(http.MethodGet)

	api := router.PathPrefix(apiPrefix).Subrouter()
	api.HandleFunc("/ping", ping).Methods(http.MethodGet)
	organization.NewHandler(organization.NewStore(db.Gorm())).RegisterRoutes(api)

	return &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
}

func Run(srv *http.Server) error {
	go func() {
		log.Printf("Listening on port %s\n", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown server: %w", err)
	}
	return nil
}
