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

	irma "github.com/privacybydesign/irmago/irma"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/irmarequestor"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	pingTimeout     = 5 * time.Second
	shutdownTimeout = 10 * time.Second

	irmaProbeTimeout = 10 * time.Second
	irmaHTTPTimeout  = 15 * time.Second

	serverAddr = ":8080"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", slog.String("error", err.Error()))
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

	logging.Setup(cfg.LogLevel, cfg.LogFormat, cfg.LogSource)

	startupCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	pool, err := database.New(startupCtx, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	emailAttr := irma.NewAttributeTypeIdentifier(cfg.IrmaEmailAttribute)
	identityAttrs := auth.IdentityAttributes{
		GivenNames: irma.NewAttributeTypeIdentifier(cfg.IrmaGivenNamesAttribute),
		FamilyName: irma.NewAttributeTypeIdentifier(cfg.IrmaFamilyNameAttribute),
	}
	requestor := irmarequestor.New(
		cfg.IrmaRequestorURL,
		irmarequestor.NewTokenAuthenticator(cfg.IrmaRequestorToken),
		&http.Client{Timeout: irmaHTTPTimeout},
	)

	// Fatal startup readiness gate: fail the process at boot rather than let a
	// misconfigured daemon silently fail a user's first login (see Ping).
	probeCtx, probeCancel := context.WithTimeout(ctx, irmaProbeTimeout)
	defer probeCancel()
	if err := requestor.Ping(probeCtx, emailAttr); err != nil {
		return err
	}

	userStore := user.NewStore(pool)
	sessionStore := session.NewStore(pool, cfg.SessionTTL)
	cookieCfg := auth.CookieConfig{
		Secure: cfg.SessionCookieSecure,
		MaxAge: int(cfg.SessionTTL.Seconds()),
	}
	platformAdmins := auth.NewPlatformAdmins(cfg.PlatformAdminEmails)
	authService := auth.NewService(requestor, userStore, sessionStore, emailAttr, identityAttrs)
	authHandler := auth.NewHandler(authService, sessionStore, cookieCfg, platformAdmins)

	startSessionPruner(ctx, sessionStore, cfg.SessionPruneEvery)

	requireUser := auth.RequireUser(sessionStore)
	orgStore := organization.NewStore(pool, audit.NewDBRecorder())
	orgService := organization.NewService(userStore, orgStore)
	orgHandler := organization.NewHandler(orgStore, orgService, audit.NewReader(pool), requireUser, platformAdmins)

	handler := server.New(
		pool,
		authHandler,
		orgHandler,
	)

	httpServer := &http.Server{
		Addr:    serverAddr,
		Handler: handler,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		slog.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		shutdownErr <- httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("starting server", slog.String("addr", httpServer.Addr))
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	if err := <-shutdownErr; err != nil {
		slog.Error("shutdown error", slog.String("error", err.Error()))
		return err
	}

	slog.Info("server stopped")
	return nil
}
