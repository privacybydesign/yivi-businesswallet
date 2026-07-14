package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

const (
	pingTimeout     = 5 * time.Second
	shutdownTimeout = 10 * time.Second

	irmaProbeTimeout = 10 * time.Second
	irmaHTTPTimeout  = 15 * time.Second

	qerdsProbeTimeout = 10 * time.Second
	qerdsHTTPTimeout  = 30 * time.Second

	serverAddr = ":8080"
)

// qerdsProvider is the boot-time provider surface: the readiness probe plus the
// operations the qerds service uses. The concrete provider is chosen by config.
type qerdsProvider interface {
	Ping(context.Context) error
	Send(context.Context, qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error)
	Fetch(context.Context, qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error)
	ResolveAddress(context.Context, string) (qerdsprovider.Address, error)
}

func newQerdsProvider(cfg config.Config) (qerdsProvider, error) {
	switch cfg.QerdsProvider {
	case config.ProviderStub:
		return qerdsprovider.NewStubProvider(), nil
	case config.ProviderDomibus:
		return qerdsprovider.NewDomibusProvider(
			cfg.QerdsProviderURL,
			qerdsprovider.NewTokenAuthenticator(cfg.QerdsAuthToken),
			qerdsprovider.DomibusConfig{
				FromParty:   cfg.QerdsDomibusFromParty,
				ToParty:     cfg.QerdsDomibusToParty,
				PartyType:   cfg.QerdsDomibusPartyType,
				Service:     cfg.QerdsDomibusService,
				ServiceType: cfg.QerdsDomibusServiceType,
				Action:      cfg.QerdsDomibusAction,
			},
			&http.Client{Timeout: qerdsHTTPTimeout},
		), nil
	default:
		return nil, fmt.Errorf("qerds provider %q is not implemented", cfg.QerdsProvider)
	}
}

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
	// misconfigured daemon silently fail a user's first login or accept (see Ping).
	// Probe both shapes the app discloses: email-only login and identity accept.
	probeCtx, probeCancel := context.WithTimeout(ctx, irmaProbeTimeout)
	defer probeCancel()
	loginProbe := irma.NewDisclosureRequest(emailAttr)
	acceptProbe := irma.NewDisclosureRequest(identityAttrs.GivenNames, identityAttrs.FamilyName, emailAttr)
	if err := requestor.Ping(probeCtx, loginProbe, acceptProbe); err != nil {
		return err
	}

	userStore := user.NewStore(pool)
	sessionStore := session.NewStore(pool, cfg.SessionTTL)
	cookieCfg := auth.CookieConfig{
		Secure: cfg.SessionCookieSecure,
		MaxAge: int(cfg.SessionTTL.Seconds()),
	}
	platformAdmins := auth.NewPlatformAdmins(cfg.PlatformAdminEmails)
	orgStore := organization.NewStore(pool, audit.NewDBRecorder())
	authService := auth.NewService(requestor, userStore, sessionStore, emailAttr, identityAttrs, orgStore)
	authHandler := auth.NewHandler(authService, sessionStore, cookieCfg, platformAdmins)

	startSessionPruner(ctx, sessionStore, cfg.SessionPruneEvery)

	requireUser := auth.RequireUser(sessionStore)
	orgService := organization.NewService(userStore, orgStore, authService)
	sessionIssuer := auth.NewSessionIssuer(sessionStore, cookieCfg)
	orgHandler := organization.NewHandler(orgStore, orgService, audit.NewReader(pool), sessionIssuer, requireUser, platformAdmins)

	qerdsProv, err := newQerdsProvider(cfg)
	if err != nil {
		return err
	}
	// Fatal readiness gate, mirroring the IRMA probe: fail at boot if the QERDS
	// provider will not accept our requests.
	qerdsProbeCtx, qerdsProbeCancel := context.WithTimeout(ctx, qerdsProbeTimeout)
	defer qerdsProbeCancel()
	if err := qerdsProv.Ping(qerdsProbeCtx); err != nil {
		return fmt.Errorf("qerds provider ping: %w", err)
	}
	qerdsStore := qerds.NewStore(pool, audit.NewDBRecorder())
	qerdsService := qerds.NewService(qerdsStore, qerdsStore, qerdsProv)
	qerdsHandler := qerds.NewHandler(qerdsService, qerdsStore, qerdsStore, requireUser, orgHandler.Authorize, cfg.QerdsWebhookSecret, cfg.QerdsDefaultAddressDomain)

	handler := server.New(
		pool,
		authHandler,
		orgHandler,
		qerdsHandler,
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
