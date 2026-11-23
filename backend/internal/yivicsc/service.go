package yivicsc

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

const (
	// randomBytes is the entropy behind an authorization id, a code and an
	// access token: each is the whole key to what it names.
	randomBytes = 32
	// maxAuthorizations bounds the authorizations in flight. Each one is a
	// presentation at the verifier and a slot in memory; past this a new one
	// is refused rather than let an unauthenticated endpoint grow without end.
	maxAuthorizations = 10_000
	// maxDescriptionLength bounds, in characters, the transaction description a
	// client sends for the authorization page to show.
	maxDescriptionLength = 200
	// maxHashes bounds the hashes one authorization may bind.
	maxHashes = 100

	// ecPublicKeyOID is id-ecPublicKey, the key algorithm of every credential.
	ecPublicKeyOID = "1.2.840.10045.2.1"
	// p256OID is the curve every credential's key is on.
	p256OID = "1.2.840.10045.3.1.7"
	// keyBits is the size of a credential's key.
	keyBits = 256

	responseTypeCode   = "code"
	challengeMethodS   = "S256"
	grantTypeAuthCode  = "authorization_code"
	tokenTypeBearer    = "Bearer"
	paramCode          = "code"
	paramState         = "state"
	paramError         = "error"
	paramErrorDescribe = "error_description"
)

// Authorization statuses the authorization page reads.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

// Authorization purposes: what the signer is asked to approve.
const (
	// PurposeLink issues (or finds) the signer's certificate.
	PurposeLink = "link"
	// PurposeSign authorises signatures with it.
	PurposeSign = "sign"
)

// Reasons an authorization was denied, for the page to explain.
const (
	// ReasonCancelled is the signer choosing not to go on.
	ReasonCancelled = "cancelled"
	// ReasonIdentityMismatch is a disclosure naming someone other than the
	// certificate's subject.
	ReasonIdentityMismatch = "identity_mismatch"
	// ReasonIdentityIncomplete is a disclosure without a name or birth date.
	ReasonIdentityIncomplete = "identity_incomplete"
)

// OAuth2 and CSC error codes this service answers with.
const (
	errInvalidRequest     = "invalid_request"
	errInvalidClient      = "invalid_client"
	errInvalidGrant       = "invalid_grant"
	errInvalidScope       = "invalid_scope"
	errUnsupportedGrant   = "unsupported_grant_type"
	errUnsupportedReponse = "unsupported_response_type"
	errAccessDenied       = "access_denied"
	errServerError        = "server_error"
	errInvalidToken       = "invalid_token"
)

// Error is a failure the far side is told about: an OAuth2 or CSC error code,
// the HTTP status it travels with, and a description written here (it never
// repeats a secret, a token or a verifier's bytes).
type Error struct {
	Status      int
	Code        string
	Description string
}

func (e *Error) Error() string { return "yivicsc: " + e.Code + ": " + e.Description }

func badRequest(code, description string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Description: description}
}

func unauthorized(description string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: errInvalidToken, Description: description}
}

// Client is an OAuth2 client of the service: a signing application that may
// start authorizations and exchange their codes.
type Client struct {
	ID     string
	Secret string
	// RedirectURI is the one callback the client's codes are sent to; an
	// authorization naming any other is refused before anything is shown.
	RedirectURI string
}

// Config is where the service lives and who may use it.
type Config struct {
	// PublicURL is the API's public URL including its version prefix
	// (https://wallet.example/api/v1); the service is served under it.
	PublicURL string
	// AppBaseURL is where the SPA is served, for the authorization page.
	AppBaseURL string
	Clients    []Client
}

// verifier is the part of the hosted OpenID4VP verifier the service uses.
type verifier interface {
	StartSigning(ctx context.Context, req openid4vpverifier.SigningRequest) (openid4vpverifier.Session, error)
	Result(ctx context.Context, id string) (openid4vpverifier.Presentation, error)
}

// credentialStore is where the authority and the credentials are kept.
type credentialStore interface {
	authority(ctx context.Context, now time.Time) (*authority, error)
	credentialFor(ctx context.Context, subject []byte, source string, now time.Time,
		issue func() (*ecdsa.PrivateKey, *x509.Certificate, error)) (credential, error)
	credentialByID(ctx context.Context, id uuid.UUID) (credential, error)
}

// Service is the authorization server and the CSC provider behind it.
type Service struct {
	store         credentialStore
	verifier      verifier
	subjectSecret []byte
	baseURL       string
	appBaseURL    string
	clients       map[string]Client
	now           func() time.Time

	mu             sync.Mutex
	authorizations map[string]*authorization
	codes          map[string]*grant
	tokens         map[string]*grant
}

// authorization is one signer's visit to the authorization page.
type authorization struct {
	id            string
	client        Client
	state         string
	codeChallenge string
	scope         string
	description   string
	// The signing leg only: the credential and the digests it may sign.
	credential    *credential
	hashes        [][]byte
	numSignatures int

	transactionID string
	walletLink    string
	expiresAt     time.Time

	status      string
	reason      string
	redirectURL string
	// checking is set while one request asks the verifier, so two polls that
	// arrive together do not both settle the authorization.
	checking bool
}

// grant is what a code, and then the access token it is exchanged for, lets
// its holder do: act for one subject, and on the signing leg sign the bound
// digests with one credential.
type grant struct {
	clientID      string
	codeChallenge string
	scope         string
	subject       []byte
	identity      Identity
	credentialID  uuid.UUID
	hashes        [][]byte
	remaining     int
	expiresAt     time.Time
}

// DeriveSecrets returns the two secrets the service needs from the
// deployment's CSC key: the key subject keys are made with, and the built-in
// client's secret. Both are the same on every replica and neither is the key.
func DeriveSecrets(cipher *crypto.Cipher) (subjectSecret []byte, clientSecret string, err error) {
	if cipher == nil {
		return nil, "", ErrUnavailable
	}
	subjectSecret, err = cipher.DeriveSecret(subjectKeyPurpose)
	if err != nil {
		return nil, "", err
	}
	client, err := cipher.DeriveSecret(clientSecretPurpose)
	if err != nil {
		return nil, "", err
	}
	return subjectSecret, hex.EncodeToString(client), nil
}

// NewService builds the service. subjectSecret comes from DeriveSecrets.
func NewService(store *Store, v verifier, subjectSecret []byte, cfg Config) *Service {
	return newService(store, v, subjectSecret, cfg, time.Now)
}

func newService(store credentialStore, v verifier, subjectSecret []byte, cfg Config, now func() time.Time) *Service {
	clients := make(map[string]Client, len(cfg.Clients))
	for _, c := range cfg.Clients {
		clients[c.ID] = c
	}
	return &Service{
		store:          store,
		verifier:       v,
		subjectSecret:  subjectSecret,
		baseURL:        BaseURL(cfg.PublicURL),
		appBaseURL:     strings.TrimRight(cfg.AppBaseURL, "/"),
		clients:        clients,
		now:            now,
		authorizations: make(map[string]*authorization),
		codes:          make(map[string]*grant),
		tokens:         make(map[string]*grant),
	}
}

// BaseURL is the service's CSC base URL and OAuth2 issuer for an API served at
// publicURL (which includes the version prefix).
func BaseURL(publicURL string) string {
	return strings.TrimRight(publicURL, "/") + BasePath
}

// BaseURL returns this service's CSC base URL, which is also its OAuth2 issuer.
func (s *Service) BaseURL() string { return s.baseURL }

// Ping makes sure the certificate authority exists and its key opens, so a
// wrong CSC key fails the boot rather than the first signature.
func (s *Service) Ping(ctx context.Context) error {
	_, err := s.store.authority(ctx, s.now())
	return err
}

// Info is the /csc/v2/info answer.
type Info struct {
	Specs       string   `json:"specs"`
	Name        string   `json:"name"`
	Logo        string   `json:"logo"`
	Region      string   `json:"region"`
	Lang        string   `json:"lang"`
	Description string   `json:"description"`
	AuthType    []string `json:"authType"`
	OAuth2      string   `json:"oauth2"`
	Methods     []string `json:"methods"`
}

// Info describes the service.
func (s *Service) Info() Info {
	return Info{
		Specs:       Specs,
		Name:        Name,
		Logo:        s.appBaseURL + "/favicon.svg",
		Region:      "NL",
		Lang:        "en-US",
		Description: "Signs documents for a person who identified with the Yivi app.",
		AuthType:    []string{"oauth2code"},
		OAuth2:      s.baseURL,
		Methods:     []string{"oauth2/authorize", "oauth2/token", "credentials/list", "credentials/info", "signatures/signHash"},
	}
}

// AuthorizeRequest is the query of /oauth2/authorize, as sent.
type AuthorizeRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	CredentialID        string
	NumSignatures       string
	Hashes              string
	HashAlgorithmOID    string
	Description         string
}

// Authorize starts an authorization and returns where to send the browser: the
// authorization page, or the client's callback carrying an error. It returns an
// *Error instead, to be shown as is, only when the client or its callback is
// not one it knows: nothing may be redirected to an address nobody registered.
func (s *Service) Authorize(ctx context.Context, req AuthorizeRequest) (string, error) {
	client, ok := s.clients[req.ClientID]
	if !ok {
		return "", badRequest(errInvalidClient, "unknown client")
	}
	if req.RedirectURI != client.RedirectURI {
		return "", badRequest(errInvalidRequest, "the redirect URI is not registered for this client")
	}

	a, err := s.parseAuthorize(ctx, client, req)
	if oauthErr, ok := errors.AsType[*Error](err); ok {
		return callbackURL(client.RedirectURI, req.State, oauthErr.Code, oauthErr.Description), nil
	}
	if err != nil {
		return "", err
	}

	// Linking offers every source; signing only the family the certificate was
	// issued on, which is the one a matching disclosure can come from.
	family := ""
	if a.credential != nil {
		family = a.credential.source
	}
	session, err := s.verifier.StartSigning(ctx, openid4vpverifier.SigningRequest{Sources: sourcesOf(family)})
	if err != nil {
		slog.ErrorContext(ctx, "yivicsc: start presentation", slog.Any("error", err))
		return callbackURL(client.RedirectURI, req.State, errServerError, "the Yivi disclosure could not be started"), nil
	}
	a.transactionID, a.walletLink = session.TransactionID, session.WalletLink

	id, err := randomToken()
	if err != nil {
		return "", err
	}
	a.id = id
	a.expiresAt = s.now().Add(AuthorizationTTL)

	s.mu.Lock()
	s.pruneLocked()
	if len(s.authorizations) >= maxAuthorizations {
		s.mu.Unlock()
		return callbackURL(client.RedirectURI, req.State, errServerError, "too many authorizations in progress"), nil
	}
	s.authorizations[id] = a
	s.mu.Unlock()

	return s.appBaseURL + AuthorizePagePath + url.PathEscape(id), nil
}

// pageAuthorizationID is the authorization id in an authorization page's URL,
// or empty for any other location Authorize returns (the client's callback).
func (s *Service) pageAuthorizationID(location string) string {
	escaped, ok := strings.CutPrefix(location, s.appBaseURL+AuthorizePagePath)
	if !ok {
		return ""
	}
	id, err := url.PathUnescape(escaped)
	if err != nil {
		return ""
	}
	return id
}

// parseAuthorize checks an authorize request and returns the authorization it
// asks for, not yet started. A request the client got wrong is an *Error.
func (s *Service) parseAuthorize(ctx context.Context, client Client, req AuthorizeRequest) (*authorization, error) {
	if req.ResponseType != responseTypeCode {
		return nil, badRequest(errUnsupportedReponse, "response_type must be code")
	}
	if req.CodeChallenge == "" || req.CodeChallengeMethod != challengeMethodS {
		return nil, badRequest(errInvalidRequest, "a PKCE S256 code challenge is required")
	}
	// A long description (a long file name) is cut, not refused: it is a label.
	description := []rune(clean(req.Description))
	if len(description) > maxDescriptionLength {
		description = description[:maxDescriptionLength]
	}
	a := &authorization{
		client: client, state: req.State, codeChallenge: req.CodeChallenge,
		scope: req.Scope, description: string(description), status: StatusPending,
	}
	switch req.Scope {
	case signingprovider.ScopeService:
		return a, nil
	case signingprovider.ScopeCredential:
	default:
		return nil, badRequest(errInvalidScope, "scope must be service or credential")
	}

	if req.HashAlgorithmOID != signingprovider.HashAlgoSHA256OID {
		return nil, badRequest(errInvalidRequest, "hashAlgorithmOID must be SHA-256")
	}
	hashes, err := parseHashes(req.Hashes)
	if err != nil {
		return nil, err
	}
	numSignatures, err := strconv.Atoi(req.NumSignatures)
	if err != nil || numSignatures < len(hashes) || numSignatures > maxHashes {
		return nil, badRequest(errInvalidRequest, "numSignatures must cover the hashes")
	}
	credentialID, err := uuid.Parse(req.CredentialID)
	if err != nil {
		return nil, badRequest(errInvalidRequest, "unknown credentialID")
	}
	cred, err := s.store.credentialByID(ctx, credentialID)
	if errors.Is(err, ErrCredentialNotFound) {
		return nil, badRequest(errInvalidRequest, "unknown credentialID")
	}
	if err != nil {
		return nil, err
	}
	if !cred.cert.NotAfter.After(s.now()) {
		return nil, badRequest(errInvalidRequest, "the credential's certificate has expired")
	}
	a.credential, a.hashes, a.numSignatures = &cred, hashes, numSignatures
	return a, nil
}

// parseHashes reads the comma-separated digests of an authorize request. A
// client sends them base64url; the standard alphabet is taken too, since the
// digest is the same either way.
func parseHashes(raw string) ([][]byte, error) {
	parts := strings.Split(raw, ",")
	if raw == "" || len(parts) > maxHashes {
		return nil, badRequest(errInvalidRequest, "hashes is required")
	}
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		digest, err := decodeDigest(p)
		if err != nil {
			return nil, badRequest(errInvalidRequest, "a hash is not a base64 SHA-256 digest")
		}
		out = append(out, digest)
	}
	return out, nil
}

// decodeDigest reads one SHA-256 digest in either base64 alphabet, padded or
// not.
func decodeDigest(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if digest, err := enc.DecodeString(s); err == nil && len(digest) == sha256.Size {
			return digest, nil
		}
	}
	return nil, errors.New("yivicsc: not a SHA-256 digest")
}

// AuthorizationView is what the authorization page shows.
type AuthorizationView struct {
	Status  string `json:"status"`
	Purpose string `json:"purpose"`
	// Description is the client's own name for what is being signed.
	Description string `json:"description,omitempty"`
	// Source is the family the signer must disclose from on the signing leg;
	// empty when any will do.
	Source string `json:"source,omitempty"`
	// WalletLink is the OpenID4VP request for the Yivi app, while pending.
	WalletLink string    `json:"walletLink,omitempty"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// Reason says why a denied authorization was denied.
	Reason string `json:"reason,omitempty"`
	// RedirectURL is where the browser goes once the authorization settled.
	RedirectURL string `json:"redirectUrl,omitempty"`
}

// Authorization returns an authorization's state, asking the verifier whether
// the signer has disclosed yet while it is pending.
func (s *Service) Authorization(ctx context.Context, id string) (AuthorizationView, error) {
	s.mu.Lock()
	s.pruneLocked()
	a, ok := s.authorizations[id]
	if !ok {
		s.mu.Unlock()
		return AuthorizationView{}, ErrAuthorizationNotFound
	}
	check := a.status == StatusPending && !a.checking
	if check {
		a.checking = true
	}
	transactionID := a.transactionID
	s.mu.Unlock()

	if check {
		presentation, err := s.verifier.Result(ctx, transactionID)
		s.settle(ctx, a, presentation, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return a.view(), nil
}

// settle records what the verifier answered for a pending authorization.
func (s *Service) settle(ctx context.Context, a *authorization, p openid4vpverifier.Presentation, resultErr error) {
	var approved *grant
	reason := ""
	switch {
	case errors.Is(resultErr, openid4vpverifier.ErrPending):
	case resultErr != nil:
		// A verifier that cannot answer now may answer the next poll.
		slog.WarnContext(ctx, "yivicsc: read presentation", slog.Any("error", resultErr))
	default:
		approved, reason = s.evaluate(a, p)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	a.checking = false
	if a.status != StatusPending {
		return
	}
	switch {
	case approved != nil:
		code, err := randomToken()
		if err != nil {
			slog.ErrorContext(ctx, "yivicsc: authorization code", slog.Any("error", err))
			return
		}
		approved.expiresAt = s.now().Add(codeTTL)
		s.codes[code] = approved
		a.status = StatusApproved
		a.redirectURL = callbackURL(a.client.RedirectURI, a.state, "", code)
	case reason != "":
		s.denyLocked(a, reason)
	}
}

// evaluate decides a disclosure: the grant it earns, or why it earns none.
func (s *Service) evaluate(a *authorization, p openid4vpverifier.Presentation) (*grant, string) {
	identity, err := identityOf(p)
	if err != nil {
		return nil, ReasonIdentityIncomplete
	}
	g := &grant{
		clientID: a.client.ID, codeChallenge: a.codeChallenge, scope: a.scope,
		subject: subjectKey(s.subjectSecret, identity), identity: identity,
	}
	if a.credential == nil {
		return g, ""
	}
	// Sole control: only the person the certificate names may sign with it.
	if !bytes.Equal(g.subject, a.credential.subjectKey) {
		return nil, ReasonIdentityMismatch
	}
	g.credentialID, g.hashes, g.remaining = a.credential.id, a.hashes, a.numSignatures
	return g, ""
}

// Cancel denies a pending authorization at the signer's own request.
func (s *Service) Cancel(id string) (AuthorizationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	a, ok := s.authorizations[id]
	if !ok {
		return AuthorizationView{}, ErrAuthorizationNotFound
	}
	if a.status == StatusPending {
		s.denyLocked(a, ReasonCancelled)
	}
	return a.view(), nil
}

func (s *Service) denyLocked(a *authorization, reason string) {
	a.status, a.reason = StatusDenied, reason
	a.redirectURL = callbackURL(a.client.RedirectURI, a.state, errAccessDenied, reason)
}

func (a *authorization) view() AuthorizationView {
	v := AuthorizationView{
		Status: a.status, Purpose: PurposeLink, Description: a.description,
		ExpiresAt: a.expiresAt, Reason: a.reason, RedirectURL: a.redirectURL,
	}
	if a.credential != nil {
		v.Purpose, v.Source = PurposeSign, a.credential.source
	}
	if a.status == StatusPending {
		v.WalletLink = a.walletLink
	}
	return v
}

// pruneLocked drops everything past its expiry. The caller holds s.mu.
func (s *Service) pruneLocked() {
	now := s.now()
	for id, a := range s.authorizations {
		if !a.expiresAt.After(now) {
			delete(s.authorizations, id)
		}
	}
	for code, g := range s.codes {
		if !g.expiresAt.After(now) {
			delete(s.codes, code)
		}
	}
	for token, g := range s.tokens {
		if !g.expiresAt.After(now) {
			delete(s.tokens, token)
		}
	}
}

// TokenRequest is the form of /oauth2/token plus the client's credentials,
// from the Authorization header or the form.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	CodeVerifier string
	ClientID     string
	ClientSecret string
}

// TokenResponse is the /oauth2/token answer.
type TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Token exchanges an authorization code for an access token. A code is good
// once: it is spent by the first exchange that names it, right or wrong.
func (s *Service) Token(req TokenRequest) (TokenResponse, error) {
	client, ok := s.clients[req.ClientID]
	if !ok || subtle.ConstantTimeCompare([]byte(client.Secret), []byte(req.ClientSecret)) != 1 {
		return TokenResponse{}, &Error{Status: http.StatusUnauthorized, Code: errInvalidClient, Description: "client authentication failed"}
	}
	if req.GrantType != grantTypeAuthCode {
		return TokenResponse{}, badRequest(errUnsupportedGrant, "grant_type must be authorization_code")
	}

	s.mu.Lock()
	s.pruneLocked()
	g, ok := s.codes[req.Code]
	delete(s.codes, req.Code)
	s.mu.Unlock()

	challenge := sha256.Sum256([]byte(req.CodeVerifier))
	switch {
	case !ok,
		g.clientID != client.ID,
		req.RedirectURI != client.RedirectURI,
		subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(challenge[:])), []byte(g.codeChallenge)) != 1:
		return TokenResponse{}, badRequest(errInvalidGrant, "the authorization code is not valid")
	}

	token, err := randomToken()
	if err != nil {
		return TokenResponse{}, err
	}
	g.expiresAt = s.now().Add(tokenTTL)
	s.mu.Lock()
	s.tokens[token] = g
	s.mu.Unlock()
	return TokenResponse{AccessToken: token, TokenType: tokenTypeBearer, ExpiresIn: int(tokenTTL.Seconds())}, nil
}

// grantOf returns a copy of the grant an access token stands for.
func (s *Service) grantOf(accessToken string) (grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	g, ok := s.tokens[accessToken]
	if !ok || accessToken == "" {
		return grant{}, unauthorized("the access token is not valid")
	}
	return *g, nil
}

// ListCredentials returns the credentials the token's subject may use. On the
// service leg that is their one credential, issued now if they have none; on
// the signing leg it is the credential the token was bound to.
func (s *Service) ListCredentials(ctx context.Context, accessToken string) ([]string, error) {
	g, err := s.grantOf(accessToken)
	if err != nil {
		return nil, err
	}
	if g.scope == signingprovider.ScopeCredential {
		return []string{g.credentialID.String()}, nil
	}
	now := s.now()
	cred, err := s.store.credentialFor(ctx, g.subject, g.identity.Source, now, func() (*ecdsa.PrivateKey, *x509.Certificate, error) {
		ca, err := s.store.authority(ctx, now)
		if err != nil {
			return nil, nil, err
		}
		return ca.issue(g.identity, now)
	})
	if err != nil {
		return nil, err
	}
	return []string{cred.id.String()}, nil
}

// CredentialInfo is the /csc/v2/credentials/info answer.
type CredentialInfo struct {
	Description string         `json:"description"`
	Key         CredentialKey  `json:"key"`
	Cert        CredentialCert `json:"cert"`
	AuthMode    string         `json:"authMode"`
	SCAL        string         `json:"SCAL"`
	Multisign   int            `json:"multisign"`
	Lang        string         `json:"lang"`
}

// CredentialKey describes a credential's signing key.
type CredentialKey struct {
	Status string   `json:"status"`
	Algo   []string `json:"algo"`
	Len    int      `json:"len"`
	Curve  string   `json:"curve"`
}

// CredentialCert describes a credential's certificate. Certificates is the
// chain, leaf first, each base64 DER.
type CredentialCert struct {
	Status       string   `json:"status"`
	Certificates []string `json:"certificates,omitempty"`
	IssuerDN     string   `json:"issuerDN"`
	SerialNumber string   `json:"serialNumber"`
	SubjectDN    string   `json:"subjectDN"`
	ValidFrom    string   `json:"validFrom"`
	ValidTo      string   `json:"validTo"`
}

// generalizedTime is how CSC writes a certificate's dates.
const generalizedTime = "20060102150405Z"

// Certificate chain selectors of credentials/info.
const (
	certificatesNone   = "none"
	certificatesSingle = "single"
)

// CredentialInfo describes one of the token subject's credentials. certificates
// is the request's selector: none, single, or (the default here) the chain.
func (s *Service) CredentialInfo(ctx context.Context, accessToken, credentialID, certificates string) (CredentialInfo, error) {
	g, err := s.grantOf(accessToken)
	if err != nil {
		return CredentialInfo{}, err
	}
	cred, err := s.ownCredential(ctx, g, credentialID)
	if err != nil {
		return CredentialInfo{}, err
	}
	status := "valid"
	if !cred.cert.NotAfter.After(s.now()) {
		status = "expired"
	}
	info := CredentialInfo{
		Description: "Signing certificate for " + cred.cert.Subject.CommonName,
		Key: CredentialKey{
			Status: "enabled",
			Algo:   []string{ecPublicKeyOID, signingprovider.SignAlgoECDSASHA256OID},
			Len:    keyBits,
			Curve:  p256OID,
		},
		Cert: CredentialCert{
			Status:       status,
			IssuerDN:     cred.cert.Issuer.String(),
			SerialNumber: cred.cert.SerialNumber.Text(16),
			SubjectDN:    cred.cert.Subject.String(),
			ValidFrom:    cred.cert.NotBefore.UTC().Format(generalizedTime),
			ValidTo:      cred.cert.NotAfter.UTC().Format(generalizedTime),
		},
		AuthMode:  "oauth2code",
		SCAL:      "2",
		Multisign: 1,
		Lang:      "en-US",
	}
	switch certificates {
	case certificatesNone:
	case certificatesSingle:
		info.Cert.Certificates = []string{base64.StdEncoding.EncodeToString(cred.cert.Raw)}
	default:
		ca, err := s.store.authority(ctx, s.now())
		if err != nil {
			return CredentialInfo{}, err
		}
		info.Cert.Certificates = []string{
			base64.StdEncoding.EncodeToString(cred.cert.Raw),
			base64.StdEncoding.EncodeToString(ca.cert.Raw),
		}
	}
	return info, nil
}

// ownCredential returns the credential a grant may read or sign with: one that
// belongs to the grant's subject. Another person's reads as unknown.
func (s *Service) ownCredential(ctx context.Context, g grant, credentialID string) (credential, error) {
	unknown := badRequest(errInvalidRequest, "unknown credentialID")
	id, err := uuid.Parse(credentialID)
	if err != nil {
		return credential{}, unknown
	}
	cred, err := s.store.credentialByID(ctx, id)
	if errors.Is(err, ErrCredentialNotFound) {
		return credential{}, unknown
	}
	if err != nil {
		return credential{}, err
	}
	if !bytes.Equal(cred.subjectKey, g.subject) {
		return credential{}, unknown
	}
	return cred, nil
}

// SignHashRequest is the body of /csc/v2/signatures/signHash.
type SignHashRequest struct {
	CredentialID     string   `json:"credentialID"`
	Hashes           []string `json:"hashes"`
	HashAlgorithmOID string   `json:"hashAlgorithmOID"`
	SignAlgo         string   `json:"signAlgo"`
}

// SignHash signs digests with the credential an access token was bound to, and
// only the digests it was bound to: the token is the signature activation
// data. Each digest the token covers is signed once.
func (s *Service) SignHash(ctx context.Context, accessToken string, req SignHashRequest) ([]string, error) {
	g, err := s.grantOf(accessToken)
	if err != nil {
		return nil, err
	}
	if g.scope != signingprovider.ScopeCredential || req.CredentialID != g.credentialID.String() {
		return nil, badRequest(errInvalidRequest, "the access token does not authorise this credential")
	}
	if req.HashAlgorithmOID != signingprovider.HashAlgoSHA256OID {
		return nil, badRequest(errInvalidRequest, "hashAlgorithmOID must be SHA-256")
	}
	if req.SignAlgo != signingprovider.SignAlgoECDSASHA256OID && req.SignAlgo != ecPublicKeyOID {
		return nil, badRequest(errInvalidRequest, "signAlgo must be ECDSA")
	}
	if len(req.Hashes) == 0 {
		return nil, badRequest(errInvalidRequest, "hashes is required")
	}
	digests := make([][]byte, 0, len(req.Hashes))
	for _, h := range req.Hashes {
		digest, err := decodeDigest(h)
		if err != nil {
			return nil, badRequest(errInvalidRequest, "a hash is not a base64 SHA-256 digest")
		}
		digests = append(digests, digest)
	}
	if err := s.spend(accessToken, digests); err != nil {
		return nil, err
	}

	cred, err := s.ownCredential(ctx, g, req.CredentialID)
	if err != nil {
		return nil, err
	}
	if !cred.cert.NotAfter.After(s.now()) {
		return nil, badRequest(errInvalidRequest, "the credential's certificate has expired")
	}
	signatures := make([]string, 0, len(digests))
	for _, digest := range digests {
		sig, err := ecdsa.SignASN1(rand.Reader, cred.key, digest)
		if err != nil {
			return nil, fmt.Errorf("yivicsc: sign: %w", err)
		}
		signatures = append(signatures, base64.StdEncoding.EncodeToString(sig))
	}
	return signatures, nil
}

// spend takes digests out of what an access token still authorises, all of
// them or none: a digest it was not bound to, or more signatures than it has
// left, spends nothing.
func (s *Service) spend(accessToken string, digests [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.tokens[accessToken]
	if !ok {
		return unauthorized("the access token is not valid")
	}
	if len(digests) > g.remaining {
		return badRequest(errInvalidRequest, "the access token authorises no more signatures")
	}
	left := append([][]byte{}, g.hashes...)
	for _, digest := range digests {
		i := indexOf(left, digest)
		if i < 0 {
			return badRequest(errInvalidRequest, "a hash was not authorised")
		}
		left = append(left[:i], left[i+1:]...)
	}
	g.hashes, g.remaining = left, g.remaining-len(digests)
	if g.remaining == 0 || len(g.hashes) == 0 {
		delete(s.tokens, accessToken)
	}
	return nil
}

func indexOf(haystack [][]byte, needle []byte) int {
	for i, h := range haystack {
		if bytes.Equal(h, needle) {
			return i
		}
	}
	return -1
}

// callbackURL is the client's callback with an OAuth2 answer on it: a code, or
// (errCode set) an error with value as its description.
func callbackURL(redirectURI, state, errCode, value string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	if errCode == "" {
		q.Set(paramCode, value)
	} else {
		q.Set(paramError, errCode)
		q.Set(paramErrorDescribe, value)
	}
	if state != "" {
		q.Set(paramState, state)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func randomToken() (string, error) {
	b := make([]byte, randomBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("yivicsc: random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
