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

// Request statuses.
const (
	StatusAwaitingAuthorization = "awaiting_authorization"
	StatusCompleted             = "completed"
	StatusFailed                = "failed"
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
}

// LinkedCredential is a user's cached signing credential (fetched once, so the
// signing ceremony is a single authorize — the cert must be known before the
// document hash is computed, because the CMS SignedAttributes hash the cert).
type LinkedCredential struct {
	CredentialID string
	KeyAlgo      string
	UpdatedAt    time.Time
}

// Request is the non-document view of a signing request.
type Request struct {
	ID           uuid.UUID  `json:"id"`
	Status       string     `json:"status"`
	Filename     string     `json:"filename"`
	CredentialID string     `json:"credentialId"`
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

// Start is what StartLink/StartSign return to the frontend: the authorization URL
// to send the browser to, plus (for signing) the created request id.
type Start struct {
	RequestID    *uuid.UUID `json:"requestId,omitempty"`
	AuthorizeURL string     `json:"authorizeUrl"`
}
