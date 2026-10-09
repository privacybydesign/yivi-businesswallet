package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	envDatabaseURL = "DATABASE_URL"
	envLogLevel    = "LOG_LEVEL"
	envLogFormat   = "LOG_FORMAT"
	envLogSource   = "LOG_SOURCE"

	envEudiVerifierURL             = "EUDI_VERIFIER_URL"
	envEudiIssuerChain             = "EUDI_ISSUER_CHAIN"
	envEudiIntendedUseID           = "EUDI_INTENDED_USE_ID"
	envEudiRegistrationCertificate = "EUDI_REGISTRATION_CERTIFICATE"
	envSessionCookieSecure         = "SESSION_COOKIE_SECURE"
	envSessionTTL                  = "SESSION_TTL"
	envSessionPruneEvery           = "SESSION_PRUNE_INTERVAL"
	envPresentationTTL             = "PRESENTATION_SESSION_TTL"

	// Inbound OpenID4VP (the business wallet as holder/presenter toward an
	// external verifier, #188). The transaction TTL bounds the interactive
	// login → org-picker → present flow; the three flags are dev-only escape
	// hatches that default closed. See .ai/features/openid4vp-inbound.md.
	envOpenID4VPTransactionTTL                   = "OPENID4VP_TRANSACTION_TTL"
	envOpenID4VPPresenterAllowInsecureHTTP       = "OPENID4VP_PRESENTER_ALLOW_INSECURE_HTTP"
	envOpenID4VPPresenterAllowUnverifiedRequests = "OPENID4VP_PRESENTER_ALLOW_UNVERIFIED_REQUEST_OBJECTS"
	envOpenID4VPPresenterAutoPresent             = "OPENID4VP_PRESENTER_AUTO_PRESENT"
	// Extra relying-party CA PEM a signed Authorization Request may chain to,
	// added to irmago's pinned Yivi verifier anchors (the verifier analogue of
	// ATTESTATION_HOLDER_TRUST_CHAIN; the value is the PEM, not a path).
	envOpenID4VPVerifierTrustChain = "OPENID4VP_VERIFIER_TRUST_CHAIN"
	// Org-to-org presentation requests over QERDS (#271): how long one stays
	// open on either side, the CA that certifies each requesting organization
	// (PEM content; unset mints an ephemeral one at boot), and the base URL
	// another wallet reaches this backend's request_uri/response_uri on. See
	// .ai/features/oid4vp-over-qerds.md.
	envOpenID4VPOrgRequestTTL      = "OPENID4VP_ORG_REQUEST_TTL"
	envOpenID4VPRequesterCACert    = "OPENID4VP_REQUESTER_CA_CERT"
	envOpenID4VPRequesterCAKey     = "OPENID4VP_REQUESTER_CA_KEY"
	envOpenID4VPRequesterPublicURL = "OPENID4VP_REQUESTER_PUBLIC_URL"

	envPlatformAdminEmails = "PLATFORM_ADMIN_EMAILS"

	envQerdsProvider             = "QERDS_PROVIDER"
	envQerdsProviderURL          = "QERDS_PROVIDER_URL"
	envQerdsAuthToken            = "QERDS_AUTH_TOKEN"
	envQerdsWebhookSecret        = "QERDS_WEBHOOK_SECRET"
	envQerdsDefaultAddressDomain = "QERDS_DEFAULT_ADDRESS_DOMAIN"
	// QerdsInboundPollInterval drives the background inbound poller. Zero
	// disables it, which Load accepts only when the webhook secret keeps push
	// delivery available — otherwise inbound would arrive solely when an org
	// console polls, the silent no-delivery state of issue #105.
	envQerdsInboundPollInterval = "QERDS_INBOUND_POLL_INTERVAL"
	// QerdsTrustedOfferSenders allowlists which QERDS senders may have their
	// credential offers queued for an org admin to accept. Empty trusts every sender.
	envQerdsTrustedOfferSenders = "QERDS_TRUSTED_OFFER_SENDERS"
	// QerdsTrustedOfferParties allowlists which AS4 parties may deliver an
	// acceptable credential offer. Empty trusts every party the PMode admits.
	envQerdsTrustedOfferParties = "QERDS_TRUSTED_OFFER_PARTIES"

	envWalletRegistryProvider = "WALLET_REGISTRY_PROVIDER"

	// VOG screening (#242): validatie.nl real-time document check. The URL
	// defaults to the production GAAV endpoint - there is no staging instance to
	// point at instead, so a deployment that wants the real check opts in
	// explicitly via VogValidatorProvider rather than by an environment default.
	envVogValidatorProvider = "VOG_VALIDATOR_PROVIDER"
	envVogValidatorURL      = "VOG_VALIDATOR_URL"
	// VogReferenceHashKey keys the HMAC that turns a VOG's kenmerk into a
	// reuse-detection hash before it is stored (#242's data-minimisation design).
	// Optional: empty means a screening record is written with no reference hash
	// at all, never an unkeyed one.
	envVogReferenceHashKey = "VOG_REFERENCE_HASH_KEY"

	// Diploma extracts (identity proofing): DUO's PAdES signature on an
	// uploaded extract is checked against the EU Trusted Lists (or the
	// embedded PKIoverheid and certSIGN roots), with the lists cached in
	// DIPLOMA_TRUST_CACHE_DIR when set.
	envDiplomaValidatorProvider = "DIPLOMA_VALIDATOR_PROVIDER"
	envDiplomaTrustSource       = "DIPLOMA_TRUST_SOURCE"
	envDiplomaTrustCacheDir     = "DIPLOMA_TRUST_CACHE_DIR"
	// envDiplomaOCSP ("false" turns it off) checks the signer chain's
	// revocation online; off, only the revocation information embedded in the
	// extract counts. Either way an unchecked certificate fails.
	envDiplomaOCSP = "DIPLOMA_OCSP"

	// Identity proofing: the wallet's own proofing engine
	// (internal/proofingengine) runs document + face verification for an org.
	// Sessions, their evidence and the customer secrets are sealed under
	// IDENTITY_PROOFING_ENCRYPTION_KEY; without it nothing can be sent.
	envIdentityProofingProvider      = "IDENTITY_PROOFING_PROVIDER"
	envIdentityProofingEncryptionKey = "IDENTITY_PROOFING_ENCRYPTION_KEY"
	// envIdentityProofingPublicURL is the origin the Idem app reaches the
	// engine's /api/v1/app routes at (the api= of every vcmrtd deep link);
	// defaults to APP_BASE_URL. Set it when the phone reaches the backend by
	// another address (e.g. the LAN IP in development).
	envIdentityProofingPublicURL = "IDENTITY_PROOFING_PUBLIC_URL"
	// The Regula Face API: the native face step's liveness and match, and the
	// Yivi method's face check. REGULA_FACE_API_URL is what the backend calls,
	// REGULA_FACE_API_PUBLIC_URL what the app runs liveness against (defaults
	// to the former). Unset leaves face verification unavailable.
	envRegulaFaceAPIURL         = "REGULA_FACE_API_URL"
	envRegulaFaceAPIPublicURL   = "REGULA_FACE_API_PUBLIC_URL"
	envRegulaFaceMatchThreshold = "REGULA_FACE_MATCH_THRESHOLD"
	// envIdentityProofingStubOutcome makes every stub session decide (approved,
	// rejected or needs_review), so dev can see outcomes and their webhooks.
	envIdentityProofingStubOutcome = "IDENTITY_PROOFING_STUB_OUTCOME"
	// envDevMode ("true") marks a local developer stack: only then may the
	// stub identity proofing provider, its scripted outcome and the stub
	// diploma validator run, and the first Yivi login on a fresh database
	// become its admin. It refuses to start unless APP_BASE_URL is a localhost
	// URL, so a deployment cannot switch it on by accident.
	envDevMode = "DEV_MODE"

	// Attestation issuance (OpenID4VCI). The hosted Veramo issuer is addressed per
	// instance and authenticated with a Bearer admin token; the ping credential is
	// offered by the boot probe to validate URL + token + a configured credential.
	envAttestationIssuer         = "ATTESTATION_ISSUER"
	envAttestationIssuerURL      = "ATTESTATION_ISSUER_URL"
	envAttestationIssuerToken    = "ATTESTATION_ISSUER_ADMIN_TOKEN"
	envAttestationIssuerInstance = "ATTESTATION_ISSUER_INSTANCE"
	envAttestationPingCredential = "ATTESTATION_ISSUER_PING_CREDENTIAL"

	// Attestation holder (the "store, select" side). The irmago EUDI holder engine
	// is backed by Postgres, one isolated schema per org; the storage dir holds
	// irmago's per-org filesystem material and the master key (hex 32 bytes) seeds
	// per-org key derivation. Both are required only when the irmago engine is
	// selected (the stub needs neither).
	envAttestationHolder           = "ATTESTATION_HOLDER"
	envAttestationHolderStorageDir = "ATTESTATION_HOLDER_STORAGE_DIR"
	envAttestationHolderMasterKey  = "ATTESTATION_HOLDER_MASTER_KEY"
	// Trust posture for the holder's OpenID4VCI receive/redeem path (QERDS).
	envAttestationHolderTrustChain        = "ATTESTATION_HOLDER_TRUST_CHAIN"
	envAttestationHolderStagingAnchors    = "ATTESTATION_HOLDER_STAGING_ANCHORS"
	envAttestationHolderAllowInsecureHTTP = "ATTESTATION_HOLDER_ALLOW_INSECURE_HTTP"
	// Deployment key that seals each org's WSCA activation secret at rest
	// (hex-encoded 32 bytes). Source from a KMS/secret manager. See
	// .ai/features/wsca-holder-binding.md.
	envAttestationHolderWSCAKEK = "ATTESTATION_HOLDER_WSCA_KEK"
	// WSCA (wallet-provider) holder-binding backend. When URL is set, redemption
	// binds holder keys via the WSCA/HSM instead of software keys.
	envAttestationHolderWSCAURL         = "ATTESTATION_HOLDER_WSCA_URL"
	envAttestationHolderWSCAKeystoreDir = "ATTESTATION_HOLDER_WSCA_KEYSTORE_DIR"
	envAttestationHolderWSCAInsecure    = "ATTESTATION_HOLDER_WSCA_INSECURE"

	// APP_BASE_URL is the public base URL of the frontend, used to build links in
	// outbound e-mail / QERDS messages (e.g. the credential claim page). Validated
	// as an absolute http(s) URL at load, because those links are built by
	// concatenation and internal/email refuses to render a relative one.
	envAppBaseURL = "APP_BASE_URL"
	// EMAIL_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org SMTP passwords at rest.
	envEmailEncryptionKey = "EMAIL_ENCRYPTION_KEY"
	// SLACK_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org Slack incoming-webhook
	// URLs at rest. A key of its own, like every other secret at rest here, so it
	// can be rotated without touching stored SMTP passwords; without it an org
	// cannot store a webhook URL at all (internal/slackchannel).
	envSlackEncryptionKey = "SLACK_ENCRYPTION_KEY"
	// TEAMS_ENCRYPTION_KEY (hex 32 bytes) encrypts per-org Microsoft Teams webhook
	// URLs at rest. Its own key rather than the Slack one, for the same reason every
	// other secret at rest here has one: the two are rotated by different decisions,
	// and rotating one must not take out the other channel. Without it an org cannot
	// store a Teams webhook URL at all (internal/teamschannel).
	envTeamsEncryptionKey = "TEAMS_ENCRYPTION_KEY"
	// MAIL_DEFAULT_LOCALE is the language outbound transactional mail falls back to
	// when the recipient's own preference is unknown. Must be a locale the mail
	// catalogue ships (internal/email); cmd/api rejects anything else at boot.
	envMailDefaultLocale = "MAIL_DEFAULT_LOCALE"
	// PROVISIONING_ENCRYPTION_KEY (hex 32 bytes) encrypts the per-org directory
	// client secret at rest. Its own key rather than the e-mail one: the two are
	// rotated by different decisions, and one key per purpose keeps a rotation from
	// taking out a capability nobody meant to touch.
	envProvisioningEncryptionKey = "PROVISIONING_ENCRYPTION_KEY"
	// CSC_ENCRYPTION_KEY (hex 32 bytes) encrypts the per-org CSC signing-provider
	// client secret at rest. Its own key rather than sharing another: the two are
	// rotated by different decisions, and one key per purpose keeps a rotation from
	// taking out a capability nobody meant to touch. Without it an org cannot store
	// a CSC client secret (internal/csc).
	envCSCEncryptionKey = "CSC_ENCRYPTION_KEY"
	// SIGNING_OAUTH_ISSUER_INTERNAL overrides the OAuth issuer base the backend uses
	// for its own server-side token exchange during the signing ceremony, when that
	// host differs from the browser-facing issuer. It exists for local Docker (the
	// authorization server is localhost:8084 to the browser but qtsp-authz:8084 to
	// the backend container). Empty in production, where the two are the same URL.
	envSigningOAuthIssuerInternal = "SIGNING_OAUTH_ISSUER_INTERNAL"
	// SIGNING_REDIRECT_URI overrides the OAuth callback the browser is sent back to
	// after the signing ceremony (and which the QTSP registers for the SCA client).
	// Empty means the built-in localhost default, which is correct for local Docker;
	// a hosted deployment sets it to its own public /api/v1/signing/callback so the
	// redirect_uri the backend sends matches what the QTSP has registered.
	envSigningRedirectURI = "SIGNING_REDIRECT_URI"
	// STATIC_DIR points at the built frontend; when set the API also serves it as
	// an SPA on "/". Unset in dev (Vite serves the frontend).
	envStaticDir = "STATIC_DIR"

	defaultAppBaseURL = "http://localhost:5173"
	defaultMailLocale = "en"

	// PostGuard: the internal sidecar that performs encrypt-and-upload, the shared
	// secret the backend presents to it, and the deployment master key that wraps
	// each org's own (owner-configured) encryption key at rest (envelope
	// encryption); the per-org key in turn encrypts that org's API key.
	envPostGuardSidecarURL    = "POSTGUARD_SIDECAR_URL"
	envPostGuardSharedSecret  = "POSTGUARD_SHARED_SECRET"
	envPostGuardEncryptionKey = "POSTGUARD_KEY_ENCRYPTION_KEY"
	// The three endpoints that together aim a deployment at one PostGuard
	// environment: the key service and the storage the sidecar uploads through,
	// and the public website the recipient download link points at (used only for
	// the "own SMTP" notification path). The backend consumes only the website;
	// it reads the other two to refuse a combination that mixes environments.
	envPostGuardPkgURL          = "POSTGUARD_PKG_URL"
	envPostGuardCryptifyURL     = "POSTGUARD_CRYPTIFY_URL"
	envPostGuardWebsiteURL      = "POSTGUARD_WEBSITE_URL"
	defaultPostGuardPkgURL      = "https://pkg.postguard.eu"
	defaultPostGuardCryptifyURL = "https://storage.postguard.eu"
	defaultPostGuardWebsiteURL  = "https://postguard.eu"

	// Host labels naming a PostGuard service within its environment, stripped to
	// compare the three URLs: pkg.staging.postguard.eu -> staging.postguard.eu.
	postGuardPkgLabel      = "pkg."
	postGuardCryptifyLabel = "storage."

	// Domibus WS-plugin ebMS3 addressing. Defaults match the parties in the
	// Domibus sample PMode so a blue -> red self-send works out of the box.
	envQerdsDomibusFromParty   = "QERDS_DOMIBUS_FROM_PARTY"
	envQerdsDomibusToParty     = "QERDS_DOMIBUS_TO_PARTY"
	envQerdsDomibusPartyType   = "QERDS_DOMIBUS_PARTY_ID_TYPE"
	envQerdsDomibusService     = "QERDS_DOMIBUS_SERVICE"
	envQerdsDomibusServiceType = "QERDS_DOMIBUS_SERVICE_TYPE"
	envQerdsDomibusAction      = "QERDS_DOMIBUS_ACTION"

	defaultLogLevel  = "info"
	defaultLogFormat = "text"
	defaultLogSource = "true"

	// The hosted EUDI reference Verifier Endpoint (Yivi staging). Overridable so a
	// deployment can point at its own verifier.
	defaultEudiVerifierURL     = "https://verifierapi.openid4vc.staging.yivi.app"
	defaultEudiIntendedUseID   = "1"
	defaultSessionCookieSecure = "false"
	defaultSessionTTL          = "24h"
	defaultSessionPruneEvery   = "1h"
	// A login/disclosure flow (scan QR, present in the wallet, claim) completes in
	// minutes; the presentation-session mapping only needs to outlive that window.
	defaultPresentationTTL = "15m"
	// Shorter than the outbound default: an inbound transaction spans an
	// interactive multi-step flow (login, org picker) but must not outlive a
	// plausible browser session, and the verifier's own request is short-lived.
	defaultOpenID4VPTransactionTTL = "5m"
	// An org-to-org request waits for an admin on the receiving side, who may
	// not look for days; a week covers a weekend and a busy week.
	defaultOpenID4VPOrgRequestTTL = "168h"

	// ProviderStub selects the in-process StubProvider (local dev / CI).
	ProviderStub = "stub"
	// ProviderDomibus selects the Domibus AS4 access-point driver. Requires
	// QERDS_PROVIDER_URL (the WS-plugin endpoint).
	ProviderDomibus = "domibus"
	// IssuerStub selects the in-process StubIssuer (local dev / CI); IssuerVeramo
	// selects the hosted Veramo OpenID4VCI issuer.
	IssuerStub   = "stub"
	IssuerVeramo = "veramo"

	defaultAttestationIssuer = IssuerStub

	// HolderStub selects the in-process StubHolder (local dev / CI); HolderIrmago
	// selects the irmago EUDI holder engine backed by Postgres.
	HolderStub   = "stub"
	HolderIrmago = "irmago"

	defaultAttestationHolder = HolderStub

	defaultQerdsProvider             = ProviderStub
	defaultQerdsDefaultAddressDomain = "qerds.localhost"
	// Frequent enough that a pre-authorized code has not expired by the time an
	// offer is redeemed, cheap enough to leave on: one listPendingMessages call
	// per provisioned address.
	defaultQerdsInboundPollInterval = "30s"

	// The wallet-bootstrap registry (KVK) provider. Reuses ProviderStub ("stub").
	defaultWalletRegistryProvider = ProviderStub

	// ProviderValidatieNL selects the real validatie.nl HTTP client for VOG
	// screening; ProviderStub (shared with every other provider) is the
	// dev/CI default.
	ProviderValidatieNL         = "validatie_nl"
	defaultVogValidatorProvider = ProviderStub
	defaultVogValidatorURL      = "https://validatie.nl/api/valideer/"

	// ProviderDUO checks a diploma extract's DUO signature for real, and is the
	// default, as in go-diploma-issuer; ProviderStub accepts every extract's
	// signature and runs only when set explicitly (the dev stack sets it:
	// nobody holds a DUO-signed extract of a test person). DiplomaTrustEUTL and
	// DiplomaTrustPinned are where its trust anchors come from.
	ProviderDUO                     = "duo"
	defaultDiplomaValidatorProvider = ProviderDUO
	DiplomaTrustEUTL                = "eutl"
	DiplomaTrustPinned              = "pinned"
	defaultDiplomaTrustSource       = DiplomaTrustEUTL

	// ProviderEngine runs identity proofing in the wallet's own engine (the
	// default); ProviderStub is an in-memory stand-in whose sessions no phone
	// can reach.
	ProviderEngine                  = "engine"
	defaultIdentityProofingProvider = ProviderEngine

	defaultQerdsDomibusFromParty   = "domibus-blue"
	defaultQerdsDomibusToParty     = "domibus-red"
	defaultQerdsDomibusPartyType   = "urn:oasis:names:tc:ebcore:partyid-type:unregistered"
	defaultQerdsDomibusService     = "bdx:noprocess"
	defaultQerdsDomibusServiceType = "tc1"
	defaultQerdsDomibusAction      = "TC1Leg1"
)

type Config struct {
	DatabaseDSN string
	LogLevel    string
	LogFormat   string
	LogSource   bool

	EudiVerifierURL             string
	EudiIssuerChain             string
	EudiIntendedUseID           string
	EudiRegistrationCertificate string
	SessionCookieSecure         bool
	SessionTTL                  time.Duration
	SessionPruneEvery           time.Duration
	PresentationTTL             time.Duration
	// OpenID4VPTransactionTTL bounds an inbound presentation transaction from
	// the verifier's invocation to the org's response.
	OpenID4VPTransactionTTL time.Duration
	// OpenID4VPPresenterAllowInsecureHTTP permits http:// and private-network
	// request_uri / response_uri targets on the inbound presenter (local dev
	// only; the holder analogue is AttestationHolderAllowInsecureHTTP).
	OpenID4VPPresenterAllowInsecureHTTP bool
	// OpenID4VPPresenterAllowUnverifiedRequests accepts Request Objects whose
	// signature and client_id binding are only structurally checked, not
	// cryptographically verified (the x509_san_dns chain validation is #112's).
	// Off, an inbound request is refused before anything is persisted; a
	// deployment must opt in explicitly rather than inherit unverified trust.
	OpenID4VPPresenterAllowUnverifiedRequests bool
	// OpenID4VPVerifierTrustChain is extra relying-party CA PEM an inbound
	// Authorization Request's x5c chain may end in, merged onto irmago's pinned
	// Yivi verifier anchors (staging ones too when
	// AttestationHolderStagingAnchors is set — one Yivi PKI switch per
	// environment). Empty trusts the pinned anchors alone.
	OpenID4VPVerifierTrustChain string
	// OpenID4VPPresenterAutoPresent completes a presentation immediately after
	// organization selection. It stands in for the consent/approval layer (#113)
	// in dev / CI only; off, a selected transaction waits for that layer.
	OpenID4VPPresenterAutoPresent bool
	// OpenID4VPOrgRequestTTL bounds an org-to-org presentation request: on the
	// sending side the request, its certificate and the response window; on the
	// receiving side how long a QERDS-delivered request waits for approval.
	OpenID4VPOrgRequestTTL time.Duration
	// OpenID4VPRequesterCACert / Key are the PEM CA that certifies this
	// deployment's organizations as relying parties. Both empty mints an
	// ephemeral CA at boot, which only this deployment trusts.
	OpenID4VPRequesterCACert string
	OpenID4VPRequesterCAKey  string
	// OpenID4VPRequesterPublicURL is the base URL (no /api/v1) a receiving
	// wallet fetches request_uri from and posts the response to. Defaults to
	// AppBaseURL.
	OpenID4VPRequesterPublicURL string

	QerdsProvider             string
	QerdsProviderURL          string
	QerdsAuthToken            string
	QerdsWebhookSecret        string
	QerdsDefaultAddressDomain string
	// QerdsInboundPollInterval is how often the background poller drains inbound
	// messages for every provisioned address. Zero disables it.
	QerdsInboundPollInterval time.Duration
	// QerdsTrustedOfferSenders allowlists senders whose inbound credential offers
	// are queued for an org admin to accept ("addr@domain", "*@domain" or "*").
	// Empty trusts every sender, which is safe only while every sender is an org
	// on this deployment
	// — a deployment peering with an external AS4 party must set it.
	//
	// It is matched against the originalSender message property, which the
	// SENDING side populates. It refines the decision; it cannot bound it.
	QerdsTrustedOfferSenders []string
	// QerdsTrustedOfferParties allowlists the AS4 parties (ebMS3 From PartyId,
	// e.g. "verid-qerds") that may deliver an acceptable credential offer, or "*" for
	// any. Unlike QerdsTrustedOfferSenders this is the identity the receiving
	// gateway verified against its PMode and the party's signing certificate, so
	// it is the allowlist a remote sender cannot claim its way past. Empty trusts
	// every party the PMode admits.
	QerdsTrustedOfferParties []string

	QerdsDomibusFromParty   string
	QerdsDomibusToParty     string
	QerdsDomibusPartyType   string
	QerdsDomibusService     string
	QerdsDomibusServiceType string
	QerdsDomibusAction      string

	WalletRegistryProvider string

	VogValidatorProvider string
	VogValidatorURL      string
	VogReferenceHashKey  string

	DiplomaValidatorProvider string
	DiplomaTrustSource       string
	DiplomaTrustCacheDir     string
	// DiplomaOCSP enables the online revocation check of DUO's signing
	// certificate (envDiplomaOCSP).
	DiplomaOCSP bool

	IdentityProofingProvider string
	// IdentityProofingPublicURL is the origin in every vcmrtd deep link.
	IdentityProofingPublicURL string
	// IdentityProofingStubOutcome is what every stub session decides; empty
	// leaves them undecided until they expire.
	IdentityProofingStubOutcome string
	// IdentityProofingEncryptionKey seals the engine's sessions and the
	// customer secrets at rest. Empty means nothing can be sent.
	IdentityProofingEncryptionKey string
	// RegulaFaceAPIURL/-PublicURL are the Regula Face API as the backend and
	// as the app reach it; RegulaFaceMatchThreshold the similarity a match
	// needs (0 is the engine's default). An empty URL disables Regula.
	RegulaFaceAPIURL         string
	RegulaFaceAPIPublicURL   string
	RegulaFaceMatchThreshold float64

	AttestationIssuer         string
	AttestationIssuerURL      string
	AttestationIssuerToken    string
	AttestationIssuerInstance string
	AttestationPingCredential string

	AttestationHolder           string
	AttestationHolderStorageDir string
	AttestationHolderMasterKey  string
	// AttestationHolderTrustChain is extra trusted-issuer CA PEM the holder
	// verifies received credentials against (holder analogue of EudiIssuerChain).
	// It is *added* to irmago's built-in trust model, not a replacement for it, so
	// setting it keeps every issuer that already verified working; concatenate to
	// trust several partners. Empty uses the built-in trust model alone.
	AttestationHolderTrustChain string
	// AttestationHolderStagingAnchors adds irmago's staging trust anchors (for the
	// Yivi staging Veramo issuer in dev/staging).
	AttestationHolderStagingAnchors bool
	// AttestationHolderAllowInsecureHTTP permits http:// issuer endpoints on the
	// receive path (local dev only).
	AttestationHolderAllowInsecureHTTP bool
	// AttestationHolderWSCAKEK is the hex-encoded 32-byte deployment key that
	// seals each org's WSCA activation secret at rest. Empty = WSCA not configured.
	AttestationHolderWSCAKEK string
	// AttestationHolderWSCAURL is the wallet-provider (WSCA) base URL. When set
	// (with the irmago holder), redemption binds holder keys via the WSCA.
	AttestationHolderWSCAURL string
	// AttestationHolderWSCAKeystoreDir is the parent dir for per-org walletmobile
	// keystores (a persistent volume).
	AttestationHolderWSCAKeystoreDir string
	// AttestationHolderWSCAInsecure trusts the wallet-provider's dev TLS cert.
	AttestationHolderWSCAInsecure bool

	AppBaseURL         string
	EmailEncryptionKey string
	// SlackEncryptionKey encrypts per-org Slack webhook URLs at rest. Empty means
	// the deployment cannot store one.
	SlackEncryptionKey string
	// TeamsEncryptionKey encrypts per-org Microsoft Teams webhook URLs at rest.
	// Empty means the deployment cannot store one.
	TeamsEncryptionKey string
	// ProvisioningEncryptionKey encrypts the per-org directory client secret at
	// rest. Empty means no organisation can store one, so directory provisioning
	// stays unavailable.
	ProvisioningEncryptionKey string
	// CSCEncryptionKey encrypts the per-org CSC signing-provider client secret at
	// rest. Empty means no organisation can store one.
	CSCEncryptionKey string
	// SigningOAuthIssuerInternal overrides the OAuth issuer base for the backend's
	// server-side token exchange during signing (local Docker only; empty in prod).
	SigningOAuthIssuerInternal string
	// SigningRedirectURI overrides the OAuth callback sent to the QTSP during
	// signing. Empty means the built-in localhost default; a hosted deployment sets
	// its own public /api/v1/signing/callback.
	SigningRedirectURI string
	// MailDefaultLocale is the fallback language for outbound transactional mail.
	MailDefaultLocale string

	// StaticDir is the directory holding the built frontend (index.html + assets).
	// When set, the API server also serves it as an SPA on "/"; empty disables
	// static serving (dev serves the frontend via Vite).
	StaticDir string

	PostGuardSidecarURL    string
	PostGuardSharedSecret  string
	PostGuardEncryptionKey string
	// PostGuardPkgURL and PostGuardCryptifyURL are the key service and storage the
	// sidecar uploads through. The backend does not call them; it holds them so a
	// deployment that mixes PostGuard environments fails at startup.
	PostGuardPkgURL      string
	PostGuardCryptifyURL string
	// PostGuardWebsiteURL is the public base URL of the PostGuard website the
	// recipient download link points at (e.g. https://postguard.eu/download?uuid=…).
	// Used only for the "own SMTP" notification path, where the backend composes
	// the notification itself instead of letting PostGuard's service send it.
	PostGuardWebsiteURL string

	PlatformAdminEmails []string
	// DevMode is a local developer stack (envDevMode).
	DevMode bool
}

func Load() (Config, error) {
	dsn := os.Getenv(envDatabaseURL)
	if dsn == "" {
		return Config{}, fmt.Errorf("%s is required", envDatabaseURL)
	}

	cookieSecure := strings.EqualFold(envOrDefault(envSessionCookieSecure, defaultSessionCookieSecure), "true")

	verifierURL := envOrDefault(envEudiVerifierURL, defaultEudiVerifierURL)
	if cookieSecure && os.Getenv(envEudiVerifierURL) == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is true", envEudiVerifierURL, envSessionCookieSecure)
	}

	intendedUseID := os.Getenv(envEudiIntendedUseID)
	registrationCertificate := os.Getenv(envEudiRegistrationCertificate)
	if intendedUseID != "" && registrationCertificate != "" {
		return Config{}, fmt.Errorf("config: %s and %s are mutually exclusive", envEudiIntendedUseID, envEudiRegistrationCertificate)
	}
	if intendedUseID == "" && registrationCertificate == "" && verifierURL == defaultEudiVerifierURL {
		intendedUseID = defaultEudiIntendedUseID
	}

	sessionTTL, err := parseDuration(envSessionTTL, defaultSessionTTL)
	if err != nil {
		return Config{}, err
	}

	sessionPruneEvery, err := parseDuration(envSessionPruneEvery, defaultSessionPruneEvery)
	if err != nil {
		return Config{}, err
	}

	presentationTTL, err := parseDuration(envPresentationTTL, defaultPresentationTTL)
	if err != nil {
		return Config{}, err
	}

	openid4vpTransactionTTL, err := parseDuration(envOpenID4VPTransactionTTL, defaultOpenID4VPTransactionTTL)
	if err != nil {
		return Config{}, err
	}
	openid4vpOrgRequestTTL, err := parseDuration(envOpenID4VPOrgRequestTTL, defaultOpenID4VPOrgRequestTTL)
	if err != nil {
		return Config{}, err
	}
	requesterCACert, requesterCAKey := os.Getenv(envOpenID4VPRequesterCACert), os.Getenv(envOpenID4VPRequesterCAKey)
	if (requesterCACert == "") != (requesterCAKey == "") {
		return Config{}, fmt.Errorf("config: %s and %s must be set together", envOpenID4VPRequesterCACert, envOpenID4VPRequesterCAKey)
	}

	// "0" (or "0s") disables the background inbound poller. With the webhook
	// also unconfigured no mechanism delivers inbound messages automatically,
	// so that combination fails the boot rather than degrading to manual-only
	// console polls.
	qerdsInboundPollInterval, err := parseDuration(envQerdsInboundPollInterval, defaultQerdsInboundPollInterval)
	if err != nil {
		return Config{}, err
	}
	qerdsWebhookSecret := os.Getenv(envQerdsWebhookSecret)
	if qerdsInboundPollInterval <= 0 && qerdsWebhookSecret == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s disables the background poller, or no inbound QERDS message is delivered automatically", envQerdsWebhookSecret, envQerdsInboundPollInterval)
	}

	qerdsProvider := envOrDefault(envQerdsProvider, defaultQerdsProvider)
	qerdsProviderURL := os.Getenv(envQerdsProviderURL)
	if qerdsProvider != ProviderStub && qerdsProviderURL == "" {
		return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envQerdsProviderURL, envQerdsProvider, ProviderStub)
	}

	vogValidatorProvider := envOrDefault(envVogValidatorProvider, defaultVogValidatorProvider)
	if vogValidatorProvider != ProviderStub && vogValidatorProvider != ProviderValidatieNL {
		return Config{}, fmt.Errorf("config: %s must be %q or %q", envVogValidatorProvider, ProviderStub, ProviderValidatieNL)
	}
	vogValidatorURL := envOrDefault(envVogValidatorURL, defaultVogValidatorURL)

	diplomaValidatorProvider := envOrDefault(envDiplomaValidatorProvider, defaultDiplomaValidatorProvider)
	if diplomaValidatorProvider != ProviderStub && diplomaValidatorProvider != ProviderDUO {
		return Config{}, fmt.Errorf("config: %s must be %q or %q", envDiplomaValidatorProvider, ProviderStub, ProviderDUO)
	}
	diplomaTrustSource := envOrDefault(envDiplomaTrustSource, defaultDiplomaTrustSource)
	if diplomaTrustSource != DiplomaTrustEUTL && diplomaTrustSource != DiplomaTrustPinned {
		return Config{}, fmt.Errorf("config: %s must be %q or %q", envDiplomaTrustSource, DiplomaTrustEUTL, DiplomaTrustPinned)
	}

	identityProofingProvider := envOrDefault(envIdentityProofingProvider, defaultIdentityProofingProvider)
	if identityProofingProvider != ProviderEngine && identityProofingProvider != ProviderStub {
		return Config{}, fmt.Errorf("config: %s must be %q or %q", envIdentityProofingProvider, ProviderEngine, ProviderStub)
	}
	regulaFaceAPIURL := os.Getenv(envRegulaFaceAPIURL)
	regulaFaceAPIPublicURL := envOrDefault(envRegulaFaceAPIPublicURL, regulaFaceAPIURL)
	for key, raw := range map[string]string{envRegulaFaceAPIURL: regulaFaceAPIURL, envRegulaFaceAPIPublicURL: regulaFaceAPIPublicURL} {
		if raw == "" {
			continue
		}
		if err := requireAbsoluteHTTPURL(key, raw); err != nil {
			return Config{}, err
		}
	}
	var regulaFaceMatchThreshold float64
	if raw := os.Getenv(envRegulaFaceMatchThreshold); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 || v > 1 {
			return Config{}, fmt.Errorf("config: %s must be a number in (0, 1]", envRegulaFaceMatchThreshold)
		}
		regulaFaceMatchThreshold = v
	}
	identityProofingStubOutcome := os.Getenv(envIdentityProofingStubOutcome)
	switch identityProofingStubOutcome {
	case "", "approved", "rejected", "needs_review":
	default:
		return Config{}, fmt.Errorf("config: %s must be approved, rejected or needs_review", envIdentityProofingStubOutcome)
	}

	attestationIssuer := envOrDefault(envAttestationIssuer, defaultAttestationIssuer)
	attestationIssuerURL := os.Getenv(envAttestationIssuerURL)
	attestationIssuerInstance := os.Getenv(envAttestationIssuerInstance)
	if attestationIssuer != IssuerStub {
		if attestationIssuerURL == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationIssuerURL, envAttestationIssuer, IssuerStub)
		}
		if attestationIssuerInstance == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationIssuerInstance, envAttestationIssuer, IssuerStub)
		}
	}

	postguardURLs, err := loadPostGuardURLs()
	if err != nil {
		return Config{}, err
	}

	attestationHolder := envOrDefault(envAttestationHolder, defaultAttestationHolder)
	attestationHolderStorageDir := os.Getenv(envAttestationHolderStorageDir)
	attestationHolderMasterKey := os.Getenv(envAttestationHolderMasterKey)
	if attestationHolder != HolderStub {
		if attestationHolderStorageDir == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationHolderStorageDir, envAttestationHolder, HolderStub)
		}
		if attestationHolderMasterKey == "" {
			return Config{}, fmt.Errorf("config: %s must be set when %s is not %q", envAttestationHolderMasterKey, envAttestationHolder, HolderStub)
		}
	}

	// Outbound mail and QERDS messages build their links by concatenating onto
	// APP_BASE_URL, and internal/email refuses to render a link that is not an
	// absolute http(s) URL. Without this check a scheme-less value boots clean and
	// then fails every credential offer and invitation at send time.
	appBaseURL := envOrDefault(envAppBaseURL, defaultAppBaseURL)
	if err := requireAbsoluteHTTPURL(envAppBaseURL, appBaseURL); err != nil {
		return Config{}, err
	}
	requesterPublicURL := envOrDefault(envOpenID4VPRequesterPublicURL, appBaseURL)
	if err := requireAbsoluteHTTPURL(envOpenID4VPRequesterPublicURL, requesterPublicURL); err != nil {
		return Config{}, err
	}
	identityProofingPublicURL := envOrDefault(envIdentityProofingPublicURL, appBaseURL)
	if err := requireAbsoluteHTTPURL(envIdentityProofingPublicURL, identityProofingPublicURL); err != nil {
		return Config{}, err
	}
	devMode := strings.EqualFold(os.Getenv(envDevMode), "true")
	if devMode && !localhostURL(appBaseURL) {
		return Config{}, fmt.Errorf("config: %s is only for a local stack: %s must be a localhost URL, not %q",
			envDevMode, envAppBaseURL, appBaseURL)
	}
	if !devMode {
		for key, stubbed := range map[string]bool{
			envIdentityProofingProvider:    identityProofingProvider == ProviderStub,
			envIdentityProofingStubOutcome: identityProofingStubOutcome != "",
			envDiplomaValidatorProvider:    diplomaValidatorProvider == ProviderStub,
		} {
			if stubbed {
				return Config{}, fmt.Errorf("config: %s stubs out a real check and needs %s=true (a local stack)", key, envDevMode)
			}
		}
	}

	// SIGNING_REDIRECT_URI is optional (empty keeps the built-in localhost default),
	// but a set value is concatenated into the QTSP authorize URL and the token
	// exchange's redirect_uri, so a scheme-less, spaced or relative value must fail
	// at boot rather than deep inside the signing ceremony at the QTSP.
	signingRedirectURI := os.Getenv(envSigningRedirectURI)
	if signingRedirectURI != "" {
		if err := requireAbsoluteHTTPURL(envSigningRedirectURI, signingRedirectURI); err != nil {
			return Config{}, err
		}
	}

	return Config{
		DatabaseDSN: dsn,
		LogLevel:    envOrDefault(envLogLevel, defaultLogLevel),
		LogFormat:   envOrDefault(envLogFormat, defaultLogFormat),
		LogSource:   strings.EqualFold(envOrDefault(envLogSource, defaultLogSource), "true"),

		EudiVerifierURL:             verifierURL,
		EudiIssuerChain:             os.Getenv(envEudiIssuerChain),
		EudiIntendedUseID:           intendedUseID,
		EudiRegistrationCertificate: registrationCertificate,
		SessionCookieSecure:         cookieSecure,
		SessionTTL:                  sessionTTL,
		SessionPruneEvery:           sessionPruneEvery,
		PresentationTTL:             presentationTTL,
		OpenID4VPTransactionTTL:     openid4vpTransactionTTL,
		OpenID4VPPresenterAllowInsecureHTTP: strings.EqualFold(
			os.Getenv(envOpenID4VPPresenterAllowInsecureHTTP), "true"),
		OpenID4VPPresenterAllowUnverifiedRequests: strings.EqualFold(
			os.Getenv(envOpenID4VPPresenterAllowUnverifiedRequests), "true"),
		OpenID4VPPresenterAutoPresent: strings.EqualFold(
			os.Getenv(envOpenID4VPPresenterAutoPresent), "true"),
		OpenID4VPVerifierTrustChain: os.Getenv(envOpenID4VPVerifierTrustChain),
		OpenID4VPOrgRequestTTL:      openid4vpOrgRequestTTL,
		OpenID4VPRequesterCACert:    requesterCACert,
		OpenID4VPRequesterCAKey:     requesterCAKey,
		OpenID4VPRequesterPublicURL: requesterPublicURL,

		QerdsProvider:             qerdsProvider,
		QerdsProviderURL:          qerdsProviderURL,
		QerdsAuthToken:            os.Getenv(envQerdsAuthToken),
		QerdsWebhookSecret:        qerdsWebhookSecret,
		QerdsDefaultAddressDomain: envOrDefault(envQerdsDefaultAddressDomain, defaultQerdsDefaultAddressDomain),
		QerdsInboundPollInterval:  qerdsInboundPollInterval,
		QerdsTrustedOfferSenders:  parseList(os.Getenv(envQerdsTrustedOfferSenders)),
		QerdsTrustedOfferParties:  parseList(os.Getenv(envQerdsTrustedOfferParties)),

		QerdsDomibusFromParty:   envOrDefault(envQerdsDomibusFromParty, defaultQerdsDomibusFromParty),
		QerdsDomibusToParty:     envOrDefault(envQerdsDomibusToParty, defaultQerdsDomibusToParty),
		QerdsDomibusPartyType:   envOrDefault(envQerdsDomibusPartyType, defaultQerdsDomibusPartyType),
		QerdsDomibusService:     envOrDefault(envQerdsDomibusService, defaultQerdsDomibusService),
		QerdsDomibusServiceType: envOrDefault(envQerdsDomibusServiceType, defaultQerdsDomibusServiceType),
		QerdsDomibusAction:      envOrDefault(envQerdsDomibusAction, defaultQerdsDomibusAction),

		WalletRegistryProvider: envOrDefault(envWalletRegistryProvider, defaultWalletRegistryProvider),

		VogValidatorProvider: vogValidatorProvider,
		VogValidatorURL:      vogValidatorURL,
		VogReferenceHashKey:  os.Getenv(envVogReferenceHashKey),

		DiplomaValidatorProvider: diplomaValidatorProvider,
		DiplomaTrustSource:       diplomaTrustSource,
		DiplomaOCSP:              !strings.EqualFold(os.Getenv(envDiplomaOCSP), "false"),
		DiplomaTrustCacheDir:     os.Getenv(envDiplomaTrustCacheDir),

		IdentityProofingProvider:      identityProofingProvider,
		IdentityProofingStubOutcome:   identityProofingStubOutcome,
		IdentityProofingPublicURL:     identityProofingPublicURL,
		DevMode:                       devMode,
		IdentityProofingEncryptionKey: os.Getenv(envIdentityProofingEncryptionKey),
		RegulaFaceAPIURL:              regulaFaceAPIURL,
		RegulaFaceAPIPublicURL:        regulaFaceAPIPublicURL,
		RegulaFaceMatchThreshold:      regulaFaceMatchThreshold,

		AttestationIssuer:         attestationIssuer,
		AttestationIssuerURL:      attestationIssuerURL,
		AttestationIssuerToken:    os.Getenv(envAttestationIssuerToken),
		AttestationIssuerInstance: attestationIssuerInstance,
		AttestationPingCredential: os.Getenv(envAttestationPingCredential),

		AttestationHolder:           attestationHolder,
		AttestationHolderStorageDir: attestationHolderStorageDir,
		AttestationHolderMasterKey:  attestationHolderMasterKey,
		AttestationHolderTrustChain: os.Getenv(envAttestationHolderTrustChain),
		AttestationHolderStagingAnchors: strings.EqualFold(
			os.Getenv(envAttestationHolderStagingAnchors), "true"),
		AttestationHolderAllowInsecureHTTP: strings.EqualFold(
			os.Getenv(envAttestationHolderAllowInsecureHTTP), "true"),
		AttestationHolderWSCAKEK:         os.Getenv(envAttestationHolderWSCAKEK),
		AttestationHolderWSCAURL:         os.Getenv(envAttestationHolderWSCAURL),
		AttestationHolderWSCAKeystoreDir: os.Getenv(envAttestationHolderWSCAKeystoreDir),
		AttestationHolderWSCAInsecure: strings.EqualFold(
			os.Getenv(envAttestationHolderWSCAInsecure), "true"),

		AppBaseURL:                 appBaseURL,
		EmailEncryptionKey:         os.Getenv(envEmailEncryptionKey),
		SlackEncryptionKey:         os.Getenv(envSlackEncryptionKey),
		TeamsEncryptionKey:         os.Getenv(envTeamsEncryptionKey),
		ProvisioningEncryptionKey:  os.Getenv(envProvisioningEncryptionKey),
		CSCEncryptionKey:           os.Getenv(envCSCEncryptionKey),
		SigningOAuthIssuerInternal: os.Getenv(envSigningOAuthIssuerInternal),
		SigningRedirectURI:         signingRedirectURI,
		MailDefaultLocale:          envOrDefault(envMailDefaultLocale, defaultMailLocale),
		StaticDir:                  os.Getenv(envStaticDir),

		PostGuardSidecarURL:    os.Getenv(envPostGuardSidecarURL),
		PostGuardSharedSecret:  os.Getenv(envPostGuardSharedSecret),
		PostGuardEncryptionKey: os.Getenv(envPostGuardEncryptionKey),
		PostGuardPkgURL:        postguardURLs.pkg,
		PostGuardCryptifyURL:   postguardURLs.cryptify,
		PostGuardWebsiteURL:    postguardURLs.website,

		PlatformAdminEmails: parseList(os.Getenv(envPlatformAdminEmails)),
	}, nil
}

// postGuardURLs holds the three endpoints that aim a deployment at one PostGuard
// environment.
type postGuardURLs struct {
	pkg      string
	cryptify string
	website  string
}

// loadPostGuardURLs reads the three PostGuard endpoints and refuses a combination
// that mixes environments. "Upload to staging, link to production" is otherwise
// silent: the notification is delivered and only the recipient finds out the
// download link is dead.
func loadPostGuardURLs() (postGuardURLs, error) {
	urls := postGuardURLs{
		pkg:      envOrDefault(envPostGuardPkgURL, defaultPostGuardPkgURL),
		cryptify: envOrDefault(envPostGuardCryptifyURL, defaultPostGuardCryptifyURL),
		website:  envOrDefault(envPostGuardWebsiteURL, defaultPostGuardWebsiteURL),
	}

	pkgEnv, err := postGuardEnvironment(envPostGuardPkgURL, urls.pkg, postGuardPkgLabel)
	if err != nil {
		return postGuardURLs{}, err
	}
	cryptifyEnv, err := postGuardEnvironment(envPostGuardCryptifyURL, urls.cryptify, postGuardCryptifyLabel)
	if err != nil {
		return postGuardURLs{}, err
	}
	websiteEnv, err := postGuardEnvironment(envPostGuardWebsiteURL, urls.website, "")
	if err != nil {
		return postGuardURLs{}, err
	}

	if pkgEnv != websiteEnv || cryptifyEnv != websiteEnv {
		return postGuardURLs{}, fmt.Errorf(
			"config: PostGuard URLs name different environments (%s=%q, %s=%q, %s=%q); switch all three together",
			envPostGuardPkgURL, urls.pkg,
			envPostGuardCryptifyURL, urls.cryptify,
			envPostGuardWebsiteURL, urls.website)
	}
	return urls, nil
}

// postGuardEnvironment validates one PostGuard URL as an absolute http(s) URL and
// reduces it to the environment host the three are compared on. serviceLabel is
// the host label naming the service within its environment and is stripped when
// present; the website carries no such label, so it passes an empty one.
func postGuardEnvironment(key, raw, serviceLabel string) (string, error) {
	if err := requireAbsoluteHTTPURL(key, raw); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	host := strings.ToLower(u.Hostname())
	if serviceLabel != "" {
		host = strings.TrimPrefix(host, serviceLabel)
	}
	return host, nil
}

// requireAbsoluteHTTPURL rejects a configured URL that cannot be used as a link
// base: a relative path, a missing or non-http(s) scheme, or a missing host. Shared
// so every URL variable reports the same requirement in the same words.
func requireAbsoluteHTTPURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: %s %q must be an absolute http(s) URL", key, raw)
	}
	return nil
}

func parseList(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func parseDuration(key, fallback string) (time.Duration, error) {
	raw := envOrDefault(key, fallback)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s %q: %w", key, raw, err)
	}
	return d, nil
}

// localhostURL reports whether raw is an absolute URL on this machine:
// localhost or a loopback address.
func localhostURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
