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

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/postguard"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/presentation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
)

const (
	pingTimeout     = 5 * time.Second
	shutdownTimeout = 10 * time.Second

	verifierProbeTimeout = 10 * time.Second
	verifierHTTPTimeout  = 15 * time.Second

	qerdsProbeTimeout = 10 * time.Second
	qerdsHTTPTimeout  = 30 * time.Second

	issuerProbeTimeout = 10 * time.Second
	issuerHTTPTimeout  = 15 * time.Second

	// PostGuard uploads can be large; allow a generous client timeout.
	postguardHTTPTimeout = 60 * time.Second

	serverAddr = ":8080"

	// Server-level ingest bounds. ReadHeaderTimeout caps the slowloris header
	// phase and IdleTimeout reclaims idle keep-alives; neither bounds body
	// transfer time, so large attachment uploads/downloads are unaffected (a
	// blanket ReadTimeout/WriteTimeout would truncate those). Per-request body
	// size is bounded in the handlers via http.MaxBytesReader.
	serverReadHeaderTimeout = 10 * time.Second
	serverIdleTimeout       = 120 * time.Second
	serverMaxHeaderBytes    = 1 << 20 // 1 MiB
)

// qerdsProvider is the boot-time provider surface: the readiness probe plus the
// operations the qerds service uses. The concrete provider is chosen by config.
type qerdsProvider interface {
	Ping(context.Context) error
	Send(context.Context, qerdsprovider.OutboundMessage) (qerdsprovider.SendReceipt, error)
	Fetch(context.Context, qerdsprovider.Address) ([]qerdsprovider.InboundMessage, error)
	ResolveAddress(context.Context, string) (qerdsprovider.Address, error)
}

// registryProvider is the boot-time KVK/registry surface: the readiness probe
// plus the request operation the wallet service uses. Chosen by config.
type registryProvider interface {
	Ping(context.Context) error
	Consult(context.Context, string) (registryprovider.RegistrationAttestation, error)
}

func newRegistryProvider(cfg config.Config) (registryProvider, error) {
	switch cfg.WalletRegistryProvider {
	case config.ProviderStub:
		return registryprovider.NewStubRegistry(), nil
	default:
		return nil, fmt.Errorf("wallet registry provider %q is not implemented", cfg.WalletRegistryProvider)
	}
}

// attestationIssuer is the boot-time issuer surface: the readiness probe plus the
// operations the attestation service uses. The concrete issuer is chosen by config.
type attestationIssuer interface {
	Ping(context.Context) error
	CreateOffer(context.Context, openid4vciissuer.OfferRequest) (openid4vciissuer.Offer, error)
	Status(context.Context, string) (string, error)
}

func newAttestationIssuer(cfg config.Config) (attestationIssuer, error) {
	switch cfg.AttestationIssuer {
	case config.IssuerStub:
		return openid4vciissuer.NewStubIssuer(), nil
	case config.IssuerVeramo:
		return openid4vciissuer.NewVeramoIssuer(
			cfg.AttestationIssuerURL,
			cfg.AttestationIssuerInstance,
			openid4vciissuer.NewBearerAuthenticator(cfg.AttestationIssuerToken),
			cfg.AttestationPingCredential,
			&http.Client{Timeout: issuerHTTPTimeout},
		), nil
	default:
		return nil, fmt.Errorf("attestation issuer %q is not implemented", cfg.AttestationIssuer)
	}
}

// qerdsOfferSender adapts the QERDS service to the attestation slice's
// organization-delivery seam: an attestation offered to an organization is sent
// as a QERDS message to its digital address, carrying the claim link.
type qerdsOfferSender struct{ svc *qerds.Service }

func (a qerdsOfferSender) SendCredentialOffer(ctx context.Context, orgID uuid.UUID, toAddress, orgName, credentialName, claimURL string) error {
	subject := fmt.Sprintf("Credential offer: %s", credentialName)
	body := fmt.Sprintf("%s has offered your organization a credential (%s).\n\nAdd it to your business wallet: %s", orgName, credentialName, claimURL)
	_, err := a.svc.Send(ctx, orgID, "", toAddress, subject, body, nil)
	return err
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

	verifier := openid4vpverifier.New(
		cfg.EudiVerifierURL,
		cfg.EudiIssuerChain,
		&http.Client{Timeout: verifierHTTPTimeout},
	)

	// Fatal startup readiness gate: fail the process at boot rather than let a
	// misconfigured verifier silently fail a user's first login (see Ping). The
	// probe confirms the verifier accepts a presentation request of our shape.
	probeCtx, probeCancel := context.WithTimeout(ctx, verifierProbeTimeout)
	defer probeCancel()
	if err := verifier.Ping(probeCtx); err != nil {
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
	presentationStore := presentation.NewStore(pool, cfg.PresentationTTL)
	authService := auth.NewService(verifier, presentationStore, userStore, sessionStore, orgStore)
	authHandler := auth.NewHandler(authService, sessionStore, cookieCfg, platformAdmins)

	startPruner(ctx, "sessions", cfg.SessionPruneEvery, sessionStore.DeleteExpired)
	startPruner(ctx, "presentation_sessions", cfg.SessionPruneEvery, presentationStore.DeleteExpired)

	requireUser := auth.RequireUser(sessionStore)
	orgService := organization.NewService(userStore, orgStore, authService)
	sessionIssuer := auth.NewSessionIssuer(sessionStore, cookieCfg)

	// Per-org e-mail (SMTP) for person-facing notifications (credential offers and
	// member invitations). Built before the org handler so invitations deliver
	// best-effort at invite / resend time.
	emailCipher, err := crypto.NewCipher(cfg.EmailEncryptionKey)
	if err != nil {
		return err
	}
	emailStore := email.NewStore(pool, audit.NewDBRecorder(), emailCipher)
	emailService := email.NewService(emailStore, mailer.New())

	orgHandler := organization.NewHandler(orgStore, orgService, audit.NewReader(pool), sessionIssuer, emailService, cfg.AppBaseURL, requireUser, platformAdmins)

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
	qerdsHandler := qerds.NewHandler(qerdsService, qerdsStore, qerdsStore, qerdsStore, requireUser, orgHandler.Authorize, cfg.QerdsWebhookSecret, cfg.QerdsDefaultAddressDomain)

	registry, err := newRegistryProvider(cfg)
	if err != nil {
		return err
	}
	// Fatal readiness gate, mirroring the QERDS probe: fail at boot if the
	// registry (KVK) provider will not accept our requests.
	registryProbeCtx, registryProbeCancel := context.WithTimeout(ctx, qerdsProbeTimeout)
	defer registryProbeCancel()
	if err := registry.Ping(registryProbeCtx); err != nil {
		return fmt.Errorf("wallet registry ping: %w", err)
	}
	walletStore := wallet.NewStore(pool, audit.NewDBRecorder())
	walletService := wallet.NewService(walletStore, registry, authService, userStore, qerdsStore, cfg.QerdsDefaultAddressDomain)
	walletHandler := wallet.NewHandler(walletService, sessionIssuer, requireUser, orgHandler.Authorize)

	// PostGuard is an optional org capability; unlike the verifier/QERDS/registry
	// it has no fatal boot gate (a send surfaces a clear error if unconfigured).
	// A present-but-malformed key-encryption key is still a real misconfiguration.
	postguardCipher, err := postguard.NewCipher(cfg.PostGuardEncryptionKey)
	if err != nil {
		return err
	}
	postguardStore := postguard.NewStore(pool, audit.NewDBRecorder(), postguardCipher)
	postguardClient := postguard.NewClient(cfg.PostGuardSidecarURL, cfg.PostGuardSharedSecret, &http.Client{Timeout: postguardHTTPTimeout})
	postguardService := postguard.NewService(postguardStore, postguardClient)
	postguardHandler := postguard.NewHandler(postguardService, requireUser, orgHandler.Authorize)

	attIssuer, err := newAttestationIssuer(cfg)
	if err != nil {
		return err
	}
	// Fatal readiness gate, mirroring the verifier/QERDS probes: fail at boot if the
	// issuer will not accept a credential offer of our shape.
	issuerProbeCtx, issuerProbeCancel := context.WithTimeout(ctx, issuerProbeTimeout)
	defer issuerProbeCancel()
	if err := attIssuer.Ping(issuerProbeCtx); err != nil {
		return fmt.Errorf("attestation issuer ping: %w", err)
	}
	emailHandler := email.NewHandler(emailStore, emailService, requireUser, orgHandler.Authorize)

	attestationStore := attestation.NewStore(pool, audit.NewDBRecorder())
	attestationService := attestation.NewService(
		attestationStore, attIssuer, emailService, qerdsOfferSender{qerdsService}, cfg.AppBaseURL,
	)
	attestationHandler := attestation.NewHandler(attestationStore, attestationStore, attestationStore, attestationStore, attestationService, requireUser, orgHandler.Authorize)

	handler := server.New(
		pool,
		authHandler,
		orgHandler,
		qerdsHandler,
		walletHandler,
		postguardHandler,
		emailHandler,
		attestationHandler,
	)

	httpServer := &http.Server{
		Addr:              serverAddr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
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
