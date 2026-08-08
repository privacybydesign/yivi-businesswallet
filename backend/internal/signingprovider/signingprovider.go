// Package signingprovider is the leaf client seam for a remote QTSP driven over
// the Cloud Signature Consortium (CSC) API v2, plus the OAuth2/OID4VP
// authorization it fronts. The business wallet is the RP-centric Signature
// Creation Application (SCA): it does not implement the QTSP, it drives one.
//
// This package imports no other internal/* package (leaf level, like
// qerdsprovider). It exports value types, a concrete net/http Client, and an
// in-process StubProvider so the whole ceremony is testable without a network,
// an authorization server, or a wallet. The domain orchestration lives in
// internal/signing, behind a consumer-defined interface there.
//
// Redaction: a CSC/OAuth request carries a bearer token or a client secret, and
// net/http names the URL in transport errors. Following the slackchannel /
// teamschannel / mailoauth discipline, no error or log line from this package
// repeats the base URL, the token, the client secret, or any byte the far side
// wrote — a failed call is reported as a status code (or "unreachable") only.
package signingprovider

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
)

// Signature-algorithm OIDs. The reference QTSP (and CSC in general for the EC
// credentials we use) accepts a SHA-256 digest signed with ECDSA.
const (
	// HashAlgoSHA256OID is the OID of SHA-256 (the digest we hand to signHash).
	HashAlgoSHA256OID = "2.16.840.1.101.3.4.2.1"
	// SignAlgoECDSASHA256OID is ecdsa-with-SHA256 (how the QTSP signs the digest).
	SignAlgoECDSASHA256OID = "1.2.840.10045.4.3.2"
)

// OAuth2 scopes the authorization server understands. ScopeService yields a
// token that can list/inspect credentials; ScopeCredential is the SCAL2 leg that
// binds the document hashes into the access token (the SAD).
const (
	ScopeService    = "service"
	ScopeCredential = "credential"
)

// Info is the QTSP's unauthenticated /csc/v2/info answer, reduced to what the SCA
// needs: the OAuth2 issuer to authorize against and the two identifying fields.
type Info struct {
	Name   string
	Specs  string
	OAuth2 string // the authorization server issuer URL (CSC info `oauth2`)
}

// AuthorizeParams are the inputs to the /oauth2/authorize URL. For the service
// leg leave CredentialID/Hashes empty and Scope = ScopeService; for the signing
// leg set Scope = ScopeCredential and provide the bound hashes + credential.
type AuthorizeParams struct {
	ClientID         string
	RedirectURI      string
	State            string
	CodeChallenge    string // PKCE S256 challenge (mandatory at the AS)
	Scope            string
	CredentialID     string
	NumSignatures    int
	Hashes           []string // base64 raw digests, credential scope only
	HashAlgorithmOID string
}

// Token is the OAuth2 token response. The access token is a JWT that (for the
// credential scope) carries the bound hashes — it is the SAD signHash validates.
type Token struct {
	AccessToken string
	TokenType   string
	ExpiresIn   int
}

// Credential is a signing credential resolved from credentials/info: the id, the
// signing certificate + chain (leaf first), and the key algorithm OIDs.
type Credential struct {
	ID          string
	Certificate *x509.Certificate
	Chain       []*x509.Certificate // includes the leaf at index 0
	KeyAlgo     []string
}

// ErrNoCredential means the subject has no signing credential the QTSP would
// list — the SCA cannot start a signing ceremony without one.
var ErrNoCredential = errors.New("signingprovider: no signing credential available")

// RequestError is a redaction-safe failure of a QTSP/OAuth call. Reason names a
// status code or "unreachable" — never the URL, the token, or the far side's body.
type RequestError struct {
	Reason string
}

func (e *RequestError) Error() string { return "signingprovider: " + e.Reason }

// PKCE holds a generated PKCE pair: the verifier is kept by the SCA for the token
// exchange, the challenge goes in the authorize URL.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a PKCE verifier and its S256 challenge (RFC 7636).
func NewPKCE() (PKCE, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return PKCE{}, fmt.Errorf("signingprovider: generate pkce verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// NewState generates an opaque, URL-safe OAuth state value.
func NewState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("signingprovider: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
