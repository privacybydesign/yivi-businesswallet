// Package signing is the org-scoped qualified-document-signing slice. The
// business wallet is the RP-centric Signature Creation Application (SCA): it
// prepares a PDF, drives the QTSP's OAuth2/OID4VP authorization (the wallet
// ceremony), asks the QTSP to sign the document hash, and assembles the signed
// PAdES itself — the document never leaves the wallet.
//
// Layering (handler -> service -> store / provider): the provider seam is
// internal/signingprovider (a CSC/OAuth client or its stub); the per-org
// connection (base URL, client id, client secret) comes from internal/csc.
//
// Scope: PAdES-B/B-T (a detached CMS signature over the ByteRange). B-LT/B-LTA
// (validation data, archival timestamp) are out of scope and belong to a
// separate DSS augmentation seam.
package signing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// DefaultRedirectURI is the QTSP-registered OAuth callback the browser is sent
// back to after the wallet ceremony. It must match a redirect-uri in the
// authorization server's client registration (see the --profile signer demo's
// config/application-secret.yml). This dev default targets the local backend;
// a real deployment behind a different host would need this made configurable.
const DefaultRedirectURI = "http://localhost:8080/api/v1/signing/callback"

// SessionTTL bounds how long a signing/link authorization may stay in flight
// before the in-memory session is abandoned and the request fails. It is the
// window a person has to complete the wallet ceremony.
const SessionTTL = 5 * time.Minute

// Request statuses. A request is awaiting_signatures until every selected signer
// has signed; then completed (delivery, if any, is tracked separately on
// DeliveryStatus). failed means a signer's ceremony errored out.
const (
	StatusAwaitingSignatures = "awaiting_signatures"
	StatusCompleted          = "completed"
	StatusFailed             = "failed"
)

// Signing modes: parallel signers may sign in any order (serialized by an
// in-flight lock, since incremental PAdES cannot merge concurrent signatures);
// sequential signers must sign in ascending sign_order.
const (
	ModeParallel   = "parallel"
	ModeSequential = "sequential"
)

// Recipient channels the finished document may be delivered over.
const (
	ChannelNone  = "none"
	ChannelQERDS = "qerds"
	ChannelEmail = "email"
)

// Per-request delivery lifecycle (independent of the signing status).
const (
	DeliveryNotRequested = "not_requested"
	DeliveryPending      = "pending"
	DeliveryDelivered    = "delivered"
	DeliveryFailed       = "failed"
)

// Per-signer statuses.
const (
	SignerPending = "pending"
	SignerSigned  = "signed"
	SignerFailed  = "failed"
)

var (
	// ErrNotConfigured means the org has no usable CSC provider configured.
	ErrNotConfigured = errors.New("signing: no CSC signing provider configured for organization")
	// ErrNoCredential means the acting user has not linked a signing credential.
	ErrNoCredential = errors.New("signing: no signing credential linked; link one first")
	// ErrNotFound means a signing request does not exist (or is not the caller's).
	ErrNotFound = errors.New("signing: signing request not found")
	// ErrNotCompleted means the signed document is not ready yet.
	ErrNotCompleted = errors.New("signing: signing request is not completed")
	// ErrSessionExpired means the authorization was not completed within SessionTTL.
	ErrSessionExpired = errors.New("signing: authorization session expired")
	// ErrInvalidPDF means the uploaded bytes are not a usable PDF.
	ErrInvalidPDF = errors.New("signing: the uploaded file is not a valid PDF")
	// ErrNotSigner means the acting user is not a signer of the request.
	ErrNotSigner = errors.New("signing: you are not a signer of this request")
	// ErrAlreadySigned means the acting user has already signed the request.
	ErrAlreadySigned = errors.New("signing: you have already signed this request")
	// ErrNotYourTurn means an earlier signer must sign first (sequential mode).
	ErrNotYourTurn = errors.New("signing: it is not your turn to sign yet")
	// ErrSignInProgress means another signer's ceremony is in flight for this request.
	ErrSignInProgress = errors.New("signing: another signature is in progress for this document")
	// ErrInvalidRequest means the request parameters (signers, mode, recipient) are invalid.
	ErrInvalidRequest = errors.New("signing: invalid signing request")
)

// provider is the consumer-defined view of internal/signingprovider that this
// package depends on (accept interfaces, return structs). Both the concrete
// Client and the StubProvider satisfy it.
type provider interface {
	Discover(ctx context.Context, baseURL string) (signingprovider.Info, error)
	AuthorizeURL(issuer string, p signingprovider.AuthorizeParams) string
	ExchangeToken(ctx context.Context, issuer, clientID, clientSecret, code, codeVerifier, redirectURI string) (signingprovider.Token, error)
	ListCredentials(ctx context.Context, baseURL, accessToken string) ([]string, error)
	CredentialInfo(ctx context.Context, baseURL, accessToken, credentialID string) (signingprovider.Credential, error)
	SignHash(ctx context.Context, baseURL, accessToken, credentialID string, hashesB64 []string, signAlgoOID, hashAlgoOID string) ([]string, error)
}

// connectionResolver resolves an org's CSC connection (base URL + OAuth client
// credentials). internal/csc.Store implements it; the client secret is decrypted
// inside that store and only handed here for the in-flight token exchange.
type connectionResolver interface {
	ResolveConnection(ctx context.Context, orgID uuid.UUID) (baseURL, clientID, clientSecret string, err error)
	// Available reports whether the org has a signing provider configured and
	// enabled. It is member-safe (no secret), so the signing feature can be gated
	// for members who cannot read the admin-only provider settings.
	Available(ctx context.Context, orgID uuid.UUID) (bool, error)
}

// OrgMember is the minimal view of an org member this package needs to validate
// selected signers and label them in the UI. The adapter in cmd/api maps
// organization.Member onto it, keeping this slice free of that import.
type OrgMember struct {
	UserID uuid.UUID
	Name   string
	Email  string
}

// memberDirectory lists an org's active members, so a create-request call can
// validate the chosen signer ids and enrich signers with a name/email for display.
type memberDirectory interface {
	ListMembers(ctx context.Context, orgID uuid.UUID) ([]OrgMember, error)
}

// documentDeliverer delivers the finished signed PDF to an external natural
// person over the recipient's chosen channel. The adapters in cmd/api wrap
// email.Service (attachment) and qerds.Service (registered delivery). Delivery is
// best-effort: a failure is recorded on the request (delivery_status=failed), not
// fatal to the completed signature.
type documentDeliverer interface {
	DeliverEmail(ctx context.Context, orgID uuid.UUID, to, recipientName, coverMessage, filename string, pdf []byte) error
	DeliverQERDS(ctx context.Context, orgID uuid.UUID, to, recipientName, subject, coverMessage, filename string, pdf []byte) error
}

// signerNotifier tells a selected member that a document is waiting for their
// signature. The adapter in cmd/api wraps email.Service and builds the signing-page
// link from the org slug. Notification is best-effort: a failure is logged, never
// fatal to creating (or advancing) the request.
type signerNotifier interface {
	NotifySignatureRequested(ctx context.Context, orgID uuid.UUID, signerEmail, documentName, slug string) error
}

// LinkedCredential is a user's cached signing credential (fetched once, so the
// signing ceremony is a single authorize — the cert must be known before the
// document hash is computed, because the CMS SignedAttributes hash the cert).
type LinkedCredential struct {
	CredentialID string    `json:"credentialId"`
	KeyAlgo      string    `json:"keyAlgo"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Signer is one member who must sign a request, with their per-signer state.
type Signer struct {
	UserID   uuid.UUID  `json:"userId"`
	Name     string     `json:"name"`
	Email    string     `json:"email"`
	Order    int        `json:"order"`
	Status   string     `json:"status"`
	SignedAt *time.Time `json:"signedAt,omitempty"`
}

// SignerInput is one selected signer at create time (order is 1-based; ignored in
// parallel mode).
type SignerInput struct {
	UserID uuid.UUID
	Order  int
}

// RecipientInput is where the finished document is delivered, chosen at create time.
type RecipientInput struct {
	Channel string // ChannelNone | ChannelQERDS | ChannelEmail
	Address string // email address or QERDS address; empty for ChannelNone
	Name    string
	Message string // cover message shown to the recipient
}

// Request is the non-document view of a signing request.
type Request struct {
	ID               uuid.UUID  `json:"id"`
	Status           string     `json:"status"`
	Filename         string     `json:"filename"`
	Mode             string     `json:"mode"`
	CreatedBy        uuid.UUID  `json:"createdBy"`
	CreatedByName    string     `json:"createdByName"`
	RecipientChannel string     `json:"recipientChannel"`
	RecipientName    string     `json:"recipientName,omitempty"`
	RecipientAddress string     `json:"recipientAddress,omitempty"`
	Message          string     `json:"message,omitempty"`
	DeliveryStatus   string     `json:"deliveryStatus"`
	DeliveryError    string     `json:"deliveryError,omitempty"`
	Error            string     `json:"error,omitempty"`
	Signers          []Signer   `json:"signers"`
	CreatedAt        time.Time  `json:"createdAt"`
	CompletedAt      *time.Time `json:"completedAt,omitempty"`
}

// Start is what StartLink/StartSign return to the frontend: the authorization URL
// to send the browser to, plus (for signing) the request id being signed.
type Start struct {
	RequestID    *uuid.UUID `json:"requestId,omitempty"`
	AuthorizeURL string     `json:"authorizeUrl"`
}
