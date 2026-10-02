package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/csc"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/emailchannel"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/issuersettings"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/logging"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/mailoauth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vppresenter"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/postguard"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/presentation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	proofingflow "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regulasweep"
	proofingsession "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioning"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/relyingparty"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/server"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/slackchannel"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/teamschannel"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wscawallet"
)

const (
	pingTimeout     = 5 * time.Second
	shutdownTimeout = 10 * time.Second

	verifierProbeTimeout = 10 * time.Second
	verifierHTTPTimeout  = 15 * time.Second

	qerdsProbeTimeout = 10 * time.Second
	qerdsHTTPTimeout  = 30 * time.Second

	vogProbeTimeout = 10 * time.Second
	// validatie.nl retries internally up to three times with backoff (#242); the
	// client timeout has to outlast that whole sequence, not one attempt.
	vogHTTPTimeout = 2 * time.Minute

	// heldStatusRecheckEvery is how often held credentials' status lists are
	// re-read: issuers publish revocations on the scale of hours, not seconds.
	heldStatusRecheckEvery = 6 * time.Hour

	issuerProbeTimeout = 10 * time.Second
	issuerHTTPTimeout  = 15 * time.Second

	holderProbeTimeout = 10 * time.Second

	// PostGuard uploads can be large; allow a generous client timeout.
	postguardHTTPTimeout = 60 * time.Second

	// A directory read is a handful of paged calls to somebody else's API; the
	// per-organisation deadline in provisioning.Scheduler bounds the whole sync.
	provisioningHTTPTimeout = 30 * time.Second

	// One token request to the identity platform, on the path of a send.
	mailOAuthHTTPTimeout = 15 * time.Second

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
	Consult(context.Context, registryprovider.ConsultRequest) (registryprovider.RegistrationAttestation, error)
}

func newRegistryProvider(cfg config.Config, db database.DB, recorder audit.Recorder) (registryProvider, error) {
	switch cfg.WalletRegistryProvider {
	case config.ProviderStub:
		return registryprovider.NewSeededRegistry(db, recorder), nil
	default:
		return nil, fmt.Errorf("wallet registry provider %q is not implemented", cfg.WalletRegistryProvider)
	}
}

// vogValidatorProvider is the boot-time VOG-validator surface: the readiness
// probe plus the operation the screening service uses. Chosen by config.
type vogValidatorProvider interface {
	Ping(context.Context) error
	Validate(ctx context.Context, pdf []byte) (vog.ResponseCode, error)
}

// newDiplomaValidator checks a diploma extract's DUO signature: for real
// (ProviderDUO, loading the trust anchors now, which with the EU lists takes
// a few seconds), or the stub that accepts every signature.
func newDiplomaValidator(ctx context.Context, cfg config.Config) (diploma.Validator, error) {
	switch cfg.DiplomaValidatorProvider {
	case config.ProviderStub:
		return diploma.StubValidator{}, nil
	case config.ProviderDUO:
		store, err := diploma.NewTrustStore(ctx, diploma.TrustConfig{
			Source: cfg.DiplomaTrustSource, CacheDir: cfg.DiplomaTrustCacheDir, FallbackToPinned: true,
		})
		if err != nil {
			return nil, fmt.Errorf("diploma trust anchors: %w", err)
		}
		return diploma.NewPadesValidator(store, false), nil
	default:
		return nil, fmt.Errorf("diploma validator provider %q is not implemented", cfg.DiplomaValidatorProvider)
	}
}

func newVogValidatorProvider(cfg config.Config) (vogValidatorProvider, error) {
	switch cfg.VogValidatorProvider {
	case config.ProviderStub:
		return vog.StubValidator{Code: vog.ResponseAuthentic}, nil
	case config.ProviderValidatieNL:
		return vog.NewHTTPClient(cfg.VogValidatorURL, &http.Client{Timeout: vogHTTPTimeout}), nil
	default:
		return nil, fmt.Errorf("vog validator provider %q is not implemented", cfg.VogValidatorProvider)
	}
}

// newProofingProvider builds what the proofing service drives: the wallet's
// own engine (on the pool, sealing under cipher), or the in-memory stub. The
// engine is also returned on its own so its app routes and jobs get wired.
func newProofingProvider(cfg config.Config, pool *pgxpool.Pool, cipher *crypto.Cipher, orgStore *organization.Store) (proofing.Provider, *proofingengine.Engine, error) {
	switch cfg.IdentityProofingProvider {
	case config.ProviderStub:
		stub := proofingprovider.NewStub()
		stub.Outcome = proofingprovider.Status(cfg.IdentityProofingStubOutcome)
		return stub, nil, nil
	case config.ProviderEngine:
		engineCfg := proofingengine.DefaultConfig()
		engineCfg.PublicBaseURL = cfg.IdentityProofingPublicURL
		if cfg.RegulaFaceAPIURL != "" {
			regulaClient := regula.New(cfg.RegulaFaceAPIURL)
			engineCfg.Regula = regulaClient
			engineCfg.RegulaFaceAPIPublicURL = cfg.RegulaFaceAPIPublicURL
			engineCfg.RegulaSweeps = regulasweep.NewDedup(regulasweep.NewStore(pool))
		}
		if cfg.RegulaFaceMatchThreshold > 0 {
			engineCfg.RegulaFaceMatchThreshold = cfg.RegulaFaceMatchThreshold
		}
		// Without a cipher the session store refuses every session
		// (proofingprovider.ErrNoEncryptionKey); flows still work.
		sessions := proofingsession.NewPostgresStore(pool, cipher)
		engine := proofingengine.New(engineCfg, sessions, proofingflow.NewPostgresStore(pool), orgNames{store: orgStore}, nil)
		return engine, engine, nil
	default:
		return nil, nil, fmt.Errorf("identity proofing provider %q is not implemented", cfg.IdentityProofingProvider)
	}
}

// attestationIssuer is the boot-time issuer surface: the readiness probe plus the
// operations the attestation service uses. The concrete issuer is chosen by config.
type attestationIssuer interface {
	Ping(context.Context) error
	CreateOffer(context.Context, openid4vciissuer.OfferRequest) (openid4vciissuer.Offer, error)
	Status(context.Context, string, string) (openid4vciissuer.IssuanceStatus, error)
	RevokeCredential(context.Context, string, string) error
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

// newAttestationHolder builds the holder-wallet engine chosen by config: the
// in-process stub (dev / CI) or the irmago EUDI engine backed by Postgres.
func newAttestationHolder(cfg config.Config, wscaStore *wsca.Store) (eudiholder.Holder, error) {
	switch cfg.AttestationHolder {
	case config.HolderStub:
		return eudiholder.NewStubHolder(), nil
	case config.HolderIrmago:
		key, err := eudiholder.ParseMasterKey(cfg.AttestationHolderMasterKey)
		if err != nil {
			return nil, err
		}
		engine := eudiholder.NewEngine(cfg.DatabaseDSN, cfg.AttestationHolderStorageDir, key, eudiholder.RedeemConfig{
			TrustChainPEM:       []byte(cfg.AttestationHolderTrustChain),
			StagingTrustAnchors: cfg.AttestationHolderStagingAnchors,
			AllowInsecureHTTP:   cfg.AttestationHolderAllowInsecureHTTP,
		})
		// WSCA-backed holder binding is opt-in: only when a wallet-provider URL is
		// configured. It requires the sealed-secret store (a WSCA KEK) and a binary
		// built with the `wsca` tag (the walletmobile client is a private module).
		if cfg.AttestationHolderWSCAURL != "" {
			if !wscaCompiledIn {
				return nil, fmt.Errorf("%s is set but this binary was built without -tags wsca", "ATTESTATION_HOLDER_WSCA_URL")
			}
			if !wscaStore.Configured() {
				return nil, fmt.Errorf("%s is set but %s is not", "ATTESTATION_HOLDER_WSCA_URL", "ATTESTATION_HOLDER_WSCA_KEK")
			}
			engine.SetWSCA(&eudiholder.WSCAConfig{
				BaseURL:     cfg.AttestationHolderWSCAURL,
				KeystoreDir: cfg.AttestationHolderWSCAKeystoreDir,
				Insecure:    cfg.AttestationHolderWSCAInsecure,
				Secret:      wscaStore.Secret,
			})
		}
		return engine, nil
	default:
		return nil, fmt.Errorf("attestation holder %q is not implemented", cfg.AttestationHolder)
	}
}

// attestationIssuerURL is the hosted issuer instance base URL ({url}/{instance}),
// emitted into generated VCT documents (attestation schema issuer-config). Empty
// when the issuer is stubbed / unconfigured, in which case the generated config's
// issuer field is left for the operator to fill in.
func attestationIssuerURL(cfg config.Config) string {
	if cfg.AttestationIssuerURL == "" || cfg.AttestationIssuerInstance == "" {
		return ""
	}
	return strings.TrimRight(cfg.AttestationIssuerURL, "/") + "/" + cfg.AttestationIssuerInstance
}

// qerdsOfferSender adapts the QERDS service to the attestation slice's
// organization-delivery seam: an attestation offered to an organization is sent
// as a QERDS message to its digital address, carrying a structured OpenID4VCI
// credential offer in the body (the recipient's wallet redeems it), not a claim
// link.
type qerdsOfferSender struct{ svc *qerds.Service }

func (a qerdsOfferSender) SendCredentialOffer(ctx context.Context, orgID uuid.UUID, toAddress, orgName, credentialName, offerURI string) error {
	body, err := attestation.MarshalCredentialOfferEnvelope(orgName, credentialName, offerURI)
	if err != nil {
		return err
	}
	subject := fmt.Sprintf("Credential offer: %s", credentialName)
	_, err = a.svc.Send(ctx, orgID, "", toAddress, subject, body, nil)
	return err
}

// qerdsDefaultAddress adapts qerds.Store to the organization slice's
// defaultAddressResolver seam: the dashboard's Wallet card shows the org's
// live default QERDS address rather than the registration-time snapshot on
// the organization row (#260).
type qerdsDefaultAddress struct{ store *qerds.Store }

func (a qerdsDefaultAddress) DefaultDigitalAddress(ctx context.Context, orgID uuid.UUID) (string, bool, error) {
	addr, err := a.store.DefaultAddress(ctx, orgID)
	if errors.Is(err, qerds.ErrNoSenderAddress) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return addr.Address, true, nil
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

// newOpenID4VPPresenter wires the inbound-presentation slice. The Request Object
// validator verifies the JAR's signature and x509_san_dns chain against irmago's
// pinned Yivi relying-party anchors plus OPENID4VP_VERIFIER_TRUST_CHAIN;
// UnverifiedDecoder (structural checks only) is the explicit dev / CI opt-out. The
// service is returned alongside the handler because it is also the QERDS receive
// path's collaborator (openid4vppresenter.Receiver, wired into qerdsService below).
// requesterRoot is this deployment's own requester CA (newRequesterCA): its
// organizations' requests to each other verify without configuration.
func newOpenID4VPPresenter(cfg config.Config, pool *pgxpool.Pool, recorder audit.Recorder, orgStore *organization.Store, holder eudiholder.Holder, requesterRoot []byte, requireUser, authorize func(http.Handler) http.Handler) (*openid4vppresenter.Handler, *openid4vppresenter.Service, error) {
	policy := openid4vppresenter.Policy{AllowInsecureHTTP: cfg.OpenID4VPPresenterAllowInsecureHTTP}
	var validator openid4vppresenter.Validator
	if cfg.OpenID4VPPresenterAllowUnverifiedRequests {
		slog.Warn("OpenID4VP request objects are accepted WITHOUT signature verification (dev only); " +
			"requests arriving over QERDS are refused, since only a verified certificate can certify their sender")
		validator = openid4vppresenter.NewUnverifiedDecoder(policy)
	} else {
		trustPEM := append([]byte(cfg.OpenID4VPVerifierTrustChain+"\n"), requesterRoot...)
		trust, err := eudiholder.NewVerifierTrust(trustPEM, cfg.AttestationHolderStagingAnchors)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", "OPENID4VP_VERIFIER_TRUST_CHAIN", err)
		}
		validator = openid4vppresenter.NewVerifyingValidator(trust, policy)
	}
	if cfg.OpenID4VPPresenterAutoPresent {
		slog.Warn("OpenID4VP presentations complete immediately after organization selection (dev only; no consent layer)")
	}
	store := openid4vppresenter.NewStore(pool, recorder, cfg.OpenID4VPTransactionTTL, cfg.OpenID4VPOrgRequestTTL)
	svc := openid4vppresenter.NewService(
		store, orgStore, holder,
		openid4vppresenter.NewFetcher(policy), validator, openid4vppresenter.NewResponder(policy),
		cfg.OpenID4VPPresenterAutoPresent,
	)
	metadata, err := openid4vppresenter.NewMetadataHandler(
		openid4vppresenter.NewMetadata(cfg.AppBaseURL, eudiholder.Formats(), validator))
	if err != nil {
		return nil, nil, err
	}
	return openid4vppresenter.NewHandler(svc, metadata, requireUser, authorize), svc, nil
}

// newRequesterCA loads the CA that certifies this deployment's organizations as
// OpenID4VP relying parties, or mints an ephemeral one. An ephemeral root
// changes on every start: only this deployment trusts it, and a request signed
// before a restart no longer verifies after it.
func newRequesterCA(cfg config.Config) (*relyingparty.CA, error) {
	if cfg.OpenID4VPRequesterCACert != "" {
		ca, err := relyingparty.LoadCA([]byte(cfg.OpenID4VPRequesterCACert), []byte(cfg.OpenID4VPRequesterCAKey))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", "OPENID4VP_REQUESTER_CA_CERT", err)
		}
		return ca, nil
	}
	slog.Warn("OPENID4VP_REQUESTER_CA_CERT not set; organizations request credentials under an ephemeral CA " +
		"that no other deployment trusts and that changes on restart")
	return relyingparty.NewCA("Yivi Business Wallet ephemeral requester CA")
}

// chainedInboundConsumer notifies each QERDS inbound consumer in turn. Every
// consumer already ignores an envelope type it does not recognise (see
// attestation.OfferReceiver, openid4vppresenter.Receiver), so trying them in
// sequence is enough — qerds.Service holds only one InboundConsumer, and a
// deployment now queues two unrelated kinds of inbound envelope from it.
type chainedInboundConsumer []qerds.InboundConsumer

func (c chainedInboundConsumer) OnInboundMessage(ctx context.Context, in qerds.Inbound) error {
	for _, consumer := range c {
		if err := consumer.OnInboundMessage(ctx, in); err != nil {
			return err
		}
	}
	return nil
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
		cfg.EudiIntendedUseID,
		cfg.EudiRegistrationCertificate,
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

	// Every store records audit events through this recorder: it writes the event
	// like a plain audit.DBRecorder and, for an event an org can subscribe to,
	// queues it for notification in the same transaction. A store handed a bare
	// audit.NewDBRecorder() instead is invisible to notifications, so only three get
	// one: the seeder (seeding pages nobody), the notification store itself, which
	// cannot be handed a recorder that needs it, and the organization store the
	// provisioning sync writes through (a bulk directory import would otherwise page
	// every admin once per person — see below).
	// The consequence of that second exception is that a notification.* action
	// would never notify — which is fine as long as none is in the catalog, and a
	// reason to think twice before putting one there.
	notificationStore := notifications.NewStore(pool, audit.NewDBRecorder())
	recorder := notifications.NewRecorder(audit.NewDBRecorder(), notificationStore)

	userStore := user.NewStore(pool)
	sessionStore := session.NewStore(pool, cfg.SessionTTL)
	cookieCfg := auth.CookieConfig{
		Secure: cfg.SessionCookieSecure,
		MaxAge: int(cfg.SessionTTL.Seconds()),
	}
	platformAdmins := auth.NewPlatformAdmins(cfg.PlatformAdminEmails)
	orgStore := organization.NewStore(pool, recorder)
	presentationStore := presentation.NewStore(pool, cfg.PresentationTTL)
	authService := auth.NewService(verifier, presentationStore, userStore, sessionStore, orgStore)
	authHandler := auth.NewHandler(authService, sessionStore, userStore, cookieCfg, platformAdmins)

	startPruner(ctx, "sessions", cfg.SessionPruneEvery, sessionStore.DeleteExpired)
	startPruner(ctx, "presentation_sessions", cfg.SessionPruneEvery, presentationStore.DeleteExpired)
	startPruner(ctx, "openid4vp_transactions", cfg.SessionPruneEvery, openid4vppresenter.NewStore(pool, recorder, cfg.OpenID4VPTransactionTTL, cfg.OpenID4VPOrgRequestTTL).Prune)

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
	emailStore := email.NewStore(pool, recorder, emailCipher)
	mailLocale, ok := email.ParseLocale(cfg.MailDefaultLocale)
	if !ok {
		return fmt.Errorf("MAIL_DEFAULT_LOCALE: unsupported locale %q (supported: %v)", cfg.MailDefaultLocale, email.Locales())
	}
	// Mail reuses the org's app palette (themesettings), so a tenant configures its
	// branding once and outbound mail follows.
	themeSettingsStore := themesettings.NewStore(pool, recorder)
	// An org on Microsoft 365 authenticates with an OAuth2 bearer token rather
	// than a password (Microsoft has turned off Basic Authentication for SMTP
	// AUTH), so the mail service needs a token source. It holds no deployment
	// configuration of its own: the app registration is per-org, on the same
	// settings row as the host and port.
	emailService := email.NewService(emailStore, mailer.New(),
		mailoauth.NewMicrosoft(&http.Client{Timeout: mailOAuthHTTPTimeout}),
		mailBranding{theme: themeSettingsStore}, mailLocale)

	// Member screening / VOG (#242): validatie.nl real-time PDF check.
	vogValidator, err := newVogValidatorProvider(cfg)
	if err != nil {
		return err
	}
	// Fatal readiness gate, mirroring every other provider: fail at boot if the
	// configured VOG validator will not accept our requests.
	vogProbeCtx, vogProbeCancel := context.WithTimeout(ctx, vogProbeTimeout)
	defer vogProbeCancel()
	if err := vogValidator.Ping(vogProbeCtx); err != nil {
		return fmt.Errorf("vog validator ping: %w", err)
	}
	var vogReferenceHashKey []byte
	if cfg.VogReferenceHashKey != "" {
		vogReferenceHashKey, err = hex.DecodeString(cfg.VogReferenceHashKey)
		if err != nil {
			return fmt.Errorf("VOG_REFERENCE_HASH_KEY must be hex-encoded: %w", err)
		}
	}
	// The VOG PDF parser is a PDFium WebAssembly pool; compiling the module
	// takes seconds, so it is built once here rather than per upload.
	vogParser, err := vog.NewPDFiumParser()
	if err != nil {
		return fmt.Errorf("vog parser: %w", err)
	}
	defer func() {
		if err := vogParser.Close(); err != nil {
			slog.Error("close vog parser", slog.String("error", err.Error()))
		}
	}()
	screeningService := organization.NewScreeningService(orgStore, vogValidator, vogParser, authService, orgService, vogReferenceHashKey)

	// Built ahead of qerdsService/qerdsHandler below so the org handler can read
	// an org's live default address for its Wallet card (#260) without this
	// slice importing qerds, which already imports organization for org-scoped
	// auth and would cycle.
	qerdsStore := qerds.NewStore(pool, recorder)
	orgHandler := organization.NewHandler(orgStore, orgService, screeningService, audit.NewReader(pool), sessionIssuer, emailService, cfg.AppBaseURL, requireUser, platformAdmins, qerdsDefaultAddress{qerdsStore})

	// Daily re-identification reminder sweep (#240 §6): mails members whose
	// identity is due soon or overdue, per each org's own policy.
	organization.NewIdentityScheduler(orgStore, emailService, cfg.AppBaseURL).Start(ctx, organization.DefaultIdentityScheduleInterval)

	// Daily VOG reminder sweep (#242 §6): mails members whose VOG is expiring
	// soon or has expired, per each org's own screening policy.
	organization.NewScreeningScheduler(orgStore, emailService, cfg.AppBaseURL).Start(ctx, organization.DefaultScreeningScheduleInterval)

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
	qerdsService := qerds.NewService(qerdsStore, qerdsStore, qerdsProv)
	qerdsHandler := qerds.NewHandler(qerdsService, qerdsStore, qerdsStore, qerdsStore, requireUser, orgHandler.Authorize, cfg.QerdsWebhookSecret, cfg.QerdsDefaultAddressDomain)

	registry, err := newRegistryProvider(cfg, pool, recorder)
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
	walletStore := wallet.NewStore(pool, recorder)
	walletService := wallet.NewService(walletStore, registry, authService, userStore, qerdsStore, cfg.QerdsDefaultAddressDomain)
	walletHandler := wallet.NewHandler(walletService, sessionIssuer, requireUser, orgHandler.Authorize)

	// PostGuard is an optional org capability; unlike the verifier/QERDS/registry
	// it has no fatal boot gate (a send surfaces a clear error if unconfigured).
	// A present-but-malformed key-encryption key is still a real misconfiguration.
	postguardCipher, err := postguard.NewCipher(cfg.PostGuardEncryptionKey)
	if err != nil {
		return err
	}
	postguardStore := postguard.NewStore(pool, recorder, postguardCipher)
	postguardClient := postguard.NewClient(cfg.PostGuardSidecarURL, cfg.PostGuardSharedSecret, &http.Client{Timeout: postguardHTTPTimeout})
	postguardService := postguard.NewService(postguardStore, postguardClient, postguardNotifier{email: emailService}, cfg.PostGuardWebsiteURL)
	postguardHandler := postguard.NewHandler(postguardService, requireUser, orgHandler.Authorize)
	slog.Info("postguard environment",
		slog.String("websiteUrl", cfg.PostGuardWebsiteURL),
		slog.String("pkgUrl", cfg.PostGuardPkgURL),
		slog.String("cryptifyUrl", cfg.PostGuardCryptifyURL))

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

	issuerSettingsStore := issuersettings.NewStore(pool, recorder)
	issuerSettingsHandler := issuersettings.NewHandler(issuerSettingsStore, requireUser, orgHandler.Authorize)

	wscaCipher, err := crypto.NewCipher(cfg.AttestationHolderWSCAKEK)
	if err != nil {
		return err
	}
	wscaStore := wsca.NewStore(pool, wscaCipher)

	themeSettingsHandler := themesettings.NewHandler(themeSettingsStore, requireUser, orgHandler.Authorize)

	attHolder, err := newAttestationHolder(cfg, wscaStore)
	if err != nil {
		return err
	}
	// Fatal readiness gate, mirroring the issuer probe: fail at boot if the holder
	// engine cannot open + migrate its per-org schema (irmago) against Postgres.
	holderProbeCtx, holderProbeCancel := context.WithTimeout(ctx, holderProbeTimeout)
	defer holderProbeCancel()
	if err := attHolder.Ping(holderProbeCtx); err != nil {
		return fmt.Errorf("attestation holder ping: %w", err)
	}
	defer func() {
		if err := attHolder.Close(); err != nil {
			slog.Error("closing attestation holder", slog.String("error", err.Error()))
		}
	}()

	// Inbound OpenID4VP: an external verifier invoking the business wallet as
	// the holder (#188), and — via the QERDS receiver wired into qerdsService
	// below — another org's wallet doing the same over QERDS (#271). Request
	// Objects are refused until a deployment opts into the structural
	// (unverified) decoder; the signed-request cryptography is #112's, and
	// completing a presentation right after organization selection is a dev-only
	// stand-in for the consent layer (#113). Built ahead of the QERDS wiring
	// below because presenterService is its Receiver's collaborator. See
	// .ai/features/openid4vp-inbound.md and .ai/features/oid4vp-over-qerds.md.
	requesterCA, err := newRequesterCA(cfg)
	if err != nil {
		return err
	}
	presenterHandler, presenterService, err := newOpenID4VPPresenter(cfg, pool, recorder, orgStore, attHolder, requesterCA.RootPEM(), requireUser, orgHandler.Authorize)
	if err != nil {
		return err
	}
	// The other direction (#271): an organization asks another for credentials
	// under a certificate from requesterCA, sends the invocation over QERDS and
	// verifies the answer against the same issuers its own holder trusts.
	requesterIssuerTrust, err := eudiholder.NewIssuerTrust([]byte(cfg.AttestationHolderTrustChain), cfg.AttestationHolderStagingAnchors)
	if err != nil {
		return fmt.Errorf("%s: %w", "ATTESTATION_HOLDER_TRUST_CHAIN", err)
	}
	requesterHandler := openid4vprequester.NewHandler(
		openid4vprequester.NewService(
			openid4vprequester.NewStore(pool, recorder), qerdsService, requesterCA,
			relyingparty.NewTokenVerifier(requesterIssuerTrust),
			cfg.OpenID4VPRequesterPublicURL, cfg.OpenID4VPOrgRequestTTL),
		requireUser, orgHandler.Authorize)

	attestationStore := attestation.NewStore(pool, recorder)
	// The QERDS message screen renders a credential-offer body as a parsed
	// attestation summary instead of raw envelope JSON; wired via a setter (like
	// the inbound consumer below) because qerds.Handler is constructed before
	// the attestation store exists.
	qerdsHandler.SetOfferLookup(attestation.NewOfferAnnotator(attestationStore))
	// An inbound QERDS message carrying an OpenID4VCI credential offer is queued
	// for the receiving org to accept or decline; accepting redeems it into the
	// org's holder engine and indexes it (source=qerds).
	// Two allowlists decide whose offers are queued at all: the AS4 party that
	// delivered the message (verified by the gateway) and the originalSender
	// address it claims (written by the sender). Unset trusts everyone, which is
	// only safe while every sender is an org on this deployment — warn loudly,
	// because peering with an external AS4 party (see partners/verid/) makes that
	// assumption false.
	trustedSenders := attestation.NewTrustedOfferSenders(cfg.QerdsTrustedOfferSenders, cfg.QerdsTrustedOfferParties)
	if trustedSenders.PartiesConfigured() {
		slog.InfoContext(ctx, "qerds credential-offer party allowlist active",
			slog.Any("trustedParties", trustedSenders.Parties()))
	} else {
		slog.WarnContext(ctx, "qerds credential-offer AS4 party allowlist NOT configured; "+
			"any party the PMode admits can put an offer in front of an org admin, whatever "+
			"originalSender it claims. Set QERDS_TRUSTED_OFFER_PARTIES before peering "+
			"with an external AS4 party.")
	}
	if trustedSenders.Configured() {
		slog.InfoContext(ctx, "qerds credential-offer sender allowlist active",
			slog.Any("trustedSenders", trustedSenders.Patterns()))
	} else {
		slog.WarnContext(ctx, "qerds credential-offer sender allowlist NOT configured; "+
			"offers from ANY sender address will be queued for acceptance. Set "+
			"QERDS_TRUSTED_OFFER_SENDERS before peering with an external AS4 party.")
	}
	qerdsService.SetInboundConsumer(chainedInboundConsumer{
		attestation.NewOfferReceiver(attestationStore, trustedSenders),
		openid4vppresenter.NewReceiver(presenterService),
	})

	// The other inbound path is the push webhook, which serves only when a
	// secret is configured (a secretless deployment 404s it). Say so at boot:
	// a disabled push path must be a visible choice, not a silent 404 (#105).
	if cfg.QerdsWebhookSecret == "" {
		slog.WarnContext(ctx, "qerds inbound webhook push disabled; POST /api/v1/qerds/webhook returns 404 "+
			"and inbound delivery relies on the background poller alone. Set "+
			"QERDS_WEBHOOK_SECRET when the provider should push.")
	} else {
		slog.InfoContext(ctx, "qerds inbound webhook push enabled")
	}

	// Drain inbound for every provisioned address on a ticker, so a remote party's
	// credential offer is received without an operator being logged in.
	startQerdsInboundPoller(ctx, qerdsService, cfg.QerdsInboundPollInterval)
	attestationService := attestation.NewService(
		attestationStore, attIssuer, issuerSettingsStore, emailService, qerdsOfferSender{qerdsService}, attestationStore, attestationStore, attHolder, cfg.AppBaseURL,
	)
	// Re-read the issuer status list of every held credential, so a credential
	// revoked after it was received stops reading as valid.
	startPruner(ctx, "attestation_held_status", heldStatusRecheckEvery, attestationService.RecheckAllHeld)
	// Auto-issue an org's configured onboarding attestations when a member accepts
	// an invitation. Wired via a setter (like the inbound QERDS consumer) because
	// the org service is constructed before the attestation service.
	orgService.SetOnboardingIssuer(attestation.NewOnboardingIssuer(attestationStore, attestationService))
	attestationHandler := attestation.NewHandler(attestationStore, attestationStore, attestationStore, attestationStore, attestationService, issuerSettingsStore, attestationStore, orgStore, attestationIssuerURL(cfg), requireUser, orgHandler.Authorize)

	// Org-admin WSCA holder-wallet lifecycle (activate / rotate). It shares the
	// sealed-secret store + keystore layout with the holder redeem path so a wallet
	// activated here is the one the redeem path signs with. Enabled (Configured())
	// only when a wallet-provider URL is set.
	wscaActivator := wscawallet.NewActivator(
		cfg.AttestationHolderWSCAURL != "",
		func(orgID uuid.UUID) (wscawallet.WalletClient, error) {
			return newWSCAWalletClient(cfg, orgID)
		},
		wscaStore,
	)
	wscaWalletHandler := wscawallet.NewHandler(wscaActivator, requireUser, orgHandler.Authorize)

	notificationsHandler := notifications.NewHandler(notificationStore, requireUser, orgHandler.Authorize)

	// Per-org Slack incoming webhook, the notification layer's second channel. The
	// webhook URL is encrypted at rest under its own deployment key; without that key
	// an org cannot save one (slackchannel.ErrNoEncryptionKey).
	slackCipher, err := crypto.NewCipher(cfg.SlackEncryptionKey)
	if err != nil {
		return err
	}
	slackStore := slackchannel.NewStore(pool, recorder, slackCipher)
	slackChannel := slackchannel.New(slackStore, orgStore, cfg.AppBaseURL, mailLocale)
	slackHandler := slackchannel.NewHandler(slackStore, slackChannel, requireUser, orgHandler.Authorize)

	// Per-org Microsoft Teams webhook, the notification layer's third channel. Same
	// shape as Slack, on its own deployment key: without it an org cannot save one
	// (teamschannel.ErrNoEncryptionKey).
	teamsCipher, err := crypto.NewCipher(cfg.TeamsEncryptionKey)
	if err != nil {
		return err
	}
	teamsStore := teamschannel.NewStore(pool, recorder, teamsCipher)
	teamsChannel := teamschannel.New(teamsStore, orgStore, cfg.AppBaseURL, mailLocale)
	teamsHandler := teamschannel.NewHandler(teamsStore, teamsChannel, requireUser, orgHandler.Authorize)

	// Drain the notification outbox out of band, into the channels registered here.
	// A channel a deployment leaves out keeps the orgs' saved preference and delivers
	// nothing until it is registered (see notifications.Dispatcher).
	dispatcher := notifications.NewDispatcher(notificationStore, notificationStore)
	dispatcher.Register(emailchannel.New(emailService, orgStore, cfg.AppBaseURL))
	dispatcher.Register(slackChannel)
	dispatcher.Register(teamsChannel)
	dispatcher.Start(ctx, notifications.DefaultPollInterval)

	// Directory provisioning. Unlike the verifier/QERDS/registry there is no boot
	// gate: the source is configured per organisation, not per deployment, so
	// there is nothing to probe at startup and a tenant with an expired secret
	// must not fail everyone else's deploy. A run's outcome lands on the org's
	// settings row and in its audit log instead.
	provisioningCipher, err := crypto.NewCipher(cfg.ProvisioningEncryptionKey)
	if err != nil {
		return err
	}
	provisioningStore := provisioning.NewStore(pool, recorder, provisioningCipher)
	// The sync writes memberships through its own organization store, built on the
	// plain audit recorder rather than the notifications one. Its changes are
	// audited exactly like a hand-made invite or off-boarding; they just do not page
	// anybody. membership.invited, membership.revoked and membership.role_changed
	// are all in the notifications catalogue, so a first run against a directory of
	// five hundred people would mail every admin five hundred times for one act of
	// configuration — and every leaver sweep after that in bursts. Same exception,
	// and the same reason, as internal/seed. Pass `recorder` here to reverse it.
	provisioningOrgStore := organization.NewStore(pool, audit.NewDBRecorder())
	provisioningService := provisioning.NewService(provisioningStore, provisioningOrgStore, provisioningOrgStore, emailService, cfg.AppBaseURL)
	provisioningService.Register(provisioner.NewEntra(&http.Client{Timeout: provisioningHTTPTimeout}))
	provisioningHandler := provisioning.NewHandler(provisioningStore, provisioningService, requireUser, orgHandler.Authorize)
	provisioning.NewScheduler(provisioningStore, provisioningService).Start(ctx, provisioning.DefaultSyncInterval)

	// Per-org CSC signing-provider settings (a remote QTSP driven over the CSC API
	// v2). Settings + a connection test only for now; the signing ceremony is a
	// separate seam. Its own deployment key encrypts the client secret at rest;
	// without it an org cannot save one (csc.ErrNoEncryptionKey).
	cscCipher, err := crypto.NewCipher(cfg.CSCEncryptionKey)
	if err != nil {
		return err
	}
	cscStore := csc.NewStore(pool, recorder, cscCipher)
	cscHandler := csc.NewHandler(cscStore, csc.NewClient(), requireUser, orgHandler.Authorize)

	// Qualified document signing: the business wallet as the RP-centric SCA driving
	// the per-org CSC provider (base URL + OAuth client from cscStore). A request is
	// co-signed by its selected signers — org members and external signees alike, each
	// with their own linked credential — and, once fully signed, delivered to a
	// recipient over email or QERDS.
	signingStore := signing.NewStore(pool, recorder)
	signingDelivery := signingDeliverer{email: emailService, qerds: qerdsService, orgs: orgStore}
	signingNotify := signingNotifier{email: emailService, orgs: orgStore, appBaseURL: cfg.AppBaseURL}
	// SigningRedirectURI is the QTSP-registered OAuth callback; empty falls back to
	// the localhost default inside NewService (see signing.DefaultRedirectURI). A
	// hosted deploy sets SIGNING_REDIRECT_URI to its own public callback.
	signingHandler := signing.NewHandler(
		signing.NewService(signingStore, signingprovider.NewClient(), cscStore, signingMembers{store: orgStore}, signingOrgs{store: orgStore}, signingDelivery, signingNotify, cfg.SigningRedirectURI, cfg.AppBaseURL, cfg.SigningOAuthIssuerInternal),
		requireUser, orgHandler.Authorize)

	// Identity proofing: the wallet's own engine runs every org's sessions, the
	// org being its tenant. The deployment key seals the sessions' evidence and
	// the customer secrets; without it nothing can be sent
	// (proofing.ErrNoEncryptionKey).
	proofingCipher, err := crypto.NewCipher(cfg.IdentityProofingEncryptionKey)
	if err != nil {
		return err
	}
	ips, proofingEngine, err := newProofingProvider(cfg, pool, proofingCipher, orgStore)
	if err != nil {
		return err
	}
	proofingRequests := proofing.NewRequestStore(pool, recorder, proofingCipher)
	proofingWebhooks := proofing.NewWebhookStore(pool, recorder, proofingCipher)
	proofingWebhooks.SetDefaultEndpoint(cfg.IdentityProofingDefaultWebhookURL)
	proofingService := proofing.NewService(proofing.Stores{
		Settings:     proofing.NewSettingsStore(pool, recorder),
		Requests:     proofingRequests,
		Customers:    proofing.NewCustomerStore(pool, recorder),
		APIKeys:      proofing.NewAPIKeyStore(pool, recorder),
		Webhooks:     proofingWebhooks,
		Events:       audit.NewReader(pool),
		Pauses:       proofing.NewPauseStore(pool, recorder),
		FlowHosted:   proofing.NewFlowHostedStore(pool, recorder),
		FlowDiplomas: proofing.NewFlowDiplomaStore(pool, recorder),
		Diplomas:     proofing.NewDiplomaStore(pool, recorder),
	}, ips, verifier, emailService)
	// A diploma extract is parsed on the VOG parser's PDFium pool.
	diplomaValidator, err := newDiplomaValidator(ctx, cfg)
	if err != nil {
		return err
	}
	diplomaChecker := diploma.NewChecker(diplomaValidator, diploma.NewPDFiumParser(vogParser.Pool()))
	if err := diplomaChecker.Ping(ctx); err != nil {
		return fmt.Errorf("diploma validator ping: %w", err)
	}
	proofingService.SetDiplomaChecker(diplomaChecker)
	// A customer's session keeps its personal data for its customer's data retention.
	startPruner(ctx, "identity_proofing_purge", cfg.SessionPruneEvery, proofingService.PurgeDue)
	// The engine tells the service about every session change; a session
	// nobody finishes is reconciled at its cap. Neither polls.
	proofingService.SetHostedBaseURL(strings.TrimSuffix(cfg.AppBaseURL, "/") + "/p/")
	if stub, ok := ips.(interface{ OnSessionChange(func(string)) }); ok {
		stub.OnSessionChange(func(sessionID string) { proofingService.SessionChanged(ctx, sessionID) })
	}
	var proofingApp server.Registerer = noRoutes{}
	if proofingEngine != nil {
		proofingEngine.SetNotifier(proofingService.SessionChanged)
		go proofingEngine.Run(ctx)
		proofingApp = proofingEngine
		startPruner(ctx, "identity_proofing_engine_sessions", cfg.SessionPruneEvery, proofingEngine.Purge)
		startPruner(ctx, "identity_proofing_regula_sweep", cfg.SessionPruneEvery, proofingEngine.SweepRegula)
	}
	database.RunOnNotify(ctx, pool, proofing.SessionChannel, "identity_proofing_deadlines", proofingService.ReconcileDue)
	// Customer webhooks go out as their change commits, retries at their due
	// time: to the customer's endpoint (https, public addresses only), or to the
	// wallet's own default endpoint.
	database.RunOnNotify(ctx, pool, proofing.WebhookChannel, "identity_proofing_webhooks",
		proofing.NewDeliverer(proofingWebhooks, safehttp.Policy{}).Run)
	proofingHandler := proofing.NewHandler(proofingService, requireUser, orgHandler.Authorize)
	proofingIdempotency := proofing.NewIdempotencyStore(pool)
	proofingHandler.SetIdempotencyStore(proofingIdempotency)
	proofingHandler.SetPlatformAdmins(platformAdmins)
	startPruner(ctx, "identity_proofing_idempotency_keys", cfg.SessionPruneEvery, proofingIdempotency.Prune)

	handler := server.New(
		pool,
		cfg.StaticDir,
		presenterHandler,
		requesterHandler,
		authHandler,
		orgHandler,
		qerdsHandler,
		walletHandler,
		postguardHandler,
		emailHandler,
		issuerSettingsHandler,
		themeSettingsHandler,
		attestationHandler,
		wscaWalletHandler,
		notificationsHandler,
		slackHandler,
		teamsHandler,
		provisioningHandler,
		cscHandler,
		signingHandler,
		proofingHandler,
		proofingApp,
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

// mailBranding adapts the theme-settings store to the mail service's branding
// seam. Outbound mail is branded from the same org_theme_settings row as the app
// shell, so there is exactly one place a tenant configures its look; the mail
// service keeps its own Seeds type so internal/email does not depend on the
// theming slice.
type mailBranding struct {
	theme *themesettings.Store
}

func (b mailBranding) MailBrandSeeds(ctx context.Context, orgID uuid.UUID) (email.Seeds, error) {
	settings, err := b.theme.GetSettings(ctx, orgID)
	if err != nil {
		return email.Seeds{}, err
	}
	return email.Seeds{
		PrimaryColor: settings.PrimaryColor,
		TextColor:    settings.TextColor,
		SurfaceColor: settings.SurfaceColor,
		BorderColor:  settings.BorderColor,
		LinkColor:    settings.LinkColor,
		FontFamily:   settings.FontFamily,
	}, nil
}

// MailLogo resolves the org's uploaded theme logo for embedding in mail. No logo
// set is not an error: it returns an empty Logo and the logo block renders the org
// name as a wordmark instead.
func (b mailBranding) MailLogo(ctx context.Context, orgID uuid.UUID) (email.Logo, error) {
	logo, err := b.theme.GetLogo(ctx, orgID)
	if errors.Is(err, themesettings.ErrNoLogo) {
		return email.Logo{}, nil
	}
	if err != nil {
		return email.Logo{}, err
	}
	return email.Logo{Bytes: logo.Bytes, ContentType: logo.ContentType}, nil
}

// postguardNotifier adapts the e-mail service to the PostGuard "own SMTP"
// notification seam, mapping the e-mail package's not-configured sentinel onto
// PostGuard's so the handler reports a clear "configure your SMTP" error.
type postguardNotifier struct {
	email *email.Service
}

func (n postguardNotifier) SendPostguardNotification(ctx context.Context, orgID uuid.UUID, recipients []string, orgName, message, downloadURL string) error {
	err := n.email.SendPostguardNotification(ctx, orgID, recipients, orgName, message, downloadURL)
	if errors.Is(err, email.ErrNotConfigured) {
		return postguard.ErrSMTPNotConfigured
	}
	return err
}
