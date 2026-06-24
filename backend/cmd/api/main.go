package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
)

const (
	pingTimeout     = 5 * time.Second
	shutdownTimeout = 10 * time.Second

	serverAddr = ":8080"
)

func main() {
	if err := run(); err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	startupCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	pool, err := database.New(startupCtx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler := server.New(pool, organization.NewHandler(organization.NewStore(pool)))

	httpServer := &http.Server{
		Addr:    serverAddr,
		Handler: handler,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr <- httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("starting server", "addr", httpServer.Addr)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return <-shutdownErr
}
