package signing

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/signingprovider"
)

// Service orchestrates the RP-centric signing ceremony: it resolves the org's
// CSC connection, drives the QTSP's OAuth2 authorization (whose OID4VP wallet
// step the browser performs), asks the QTSP to sign the document hash, and
// assembles the PAdES. In-flight authorizations are held in memory keyed by the
// OAuth state (see padesSession for the single-instance / no-restart caveat).
type Service struct {
	store       *Store
	provider    provider
	settings    connectionResolver
	redirectURI string
	appBaseURL  string

	mu       sync.Mutex
	sessions map[string]*ceremony
}

// ceremony is one in-flight authorization awaiting its callback.
type ceremony struct {
	flow         string // "link" | "sign"
	orgID        uuid.UUID
	userID       uuid.UUID
	slug         string
	baseURL      string
	clientID     string
	clientSecret string
	issuer       string
	pkceVerifier string

	// sign flow only:
	requestID    uuid.UUID
	credentialID string
	digestB64    string
	pades        *padesSession

	timer *time.Timer
}

const (
	flowLink = "link"
	flowSign = "sign"
)

// NewService builds the signing service. redirectURI is the QTSP-registered OAuth
// callback (must match the authorization server's client registration);
// appBaseURL is where the browser is sent after the callback resolves.
func NewService(store *Store, p provider, settings connectionResolver, redirectURI, appBaseURL string) *Service {
	return &Service{
		store:       store,
		provider:    p,
		settings:    settings,
		redirectURI: redirectURI,
		appBaseURL:  appBaseURL,
		sessions:    make(map[string]*ceremony),
	}
}

// StartLink begins linking a signing credential for the acting user: a
// service-scope authorization whose callback lists + caches the credential.
func (s *Service) StartLink(ctx context.Context, orgID, userID uuid.UUID, slug string) (Start, error) {
	conn, err := s.connection(ctx, orgID)
	if err != nil {
		return Start{}, err
	}
	info, err := s.provider.Discover(ctx, conn.baseURL)
	if err != nil {
		return Start{}, err
	}
	pkce, state, err := newAuthArtifacts()
	if err != nil {
		return Start{}, err
	}
	authURL := s.provider.AuthorizeURL(info.OAuth2, signingprovider.AuthorizeParams{
		ClientID:      conn.clientID,
		RedirectURI:   s.redirectURI,
		State:         state,
		CodeChallenge: pkce.Challenge,
		Scope:         signingprovider.ScopeService,
	})
	s.put(state, &ceremony{
		flow: flowLink, orgID: orgID, userID: userID, slug: slug,
		baseURL: conn.baseURL, clientID: conn.clientID, clientSecret: conn.clientSecret,
		issuer: info.OAuth2, pkceVerifier: pkce.Verifier,
	})
	return Start{AuthorizeURL: authURL}, nil
}

// StartSign begins signing a PDF for the acting user. It prepares the document
// (pass 1) to obtain the hash, binds that hash into a credential-scope
// authorization, and records a pending request; the callback finishes the sign.
func (s *Service) StartSign(ctx context.Context, orgID, userID uuid.UUID, slug, filename string, pdf []byte) (Start, error) {
	conn, err := s.connection(ctx, orgID)
	if err != nil {
		return Start{}, err
	}
	cred, err := s.store.GetCredential(ctx, orgID, userID)
	if err != nil {
		return Start{}, err
	}
	pades, digest, err := startPAdES(pdf, cred)
	if err != nil {
		return Start{}, err
	}
	info, err := s.provider.Discover(ctx, conn.baseURL)
	if err != nil {
		pades.abandon(err)
		return Start{}, err
	}
	pkce, state, err := newAuthArtifacts()
	if err != nil {
		pades.abandon(err)
		return Start{}, err
	}
	digestB64 := base64.StdEncoding.EncodeToString(digest)
	requestID, err := s.store.CreateRequest(ctx, orgID, userID, cred.ID, filename)
	if err != nil {
		pades.abandon(err)
		return Start{}, err
	}
	authURL := s.provider.AuthorizeURL(info.OAuth2, signingprovider.AuthorizeParams{
		ClientID:         conn.clientID,
		RedirectURI:      s.redirectURI,
		State:            state,
		CodeChallenge:    pkce.Challenge,
		Scope:            signingprovider.ScopeCredential,
		CredentialID:     cred.ID,
		NumSignatures:    1,
		Hashes:           []string{digestB64},
		HashAlgorithmOID: signingprovider.HashAlgoSHA256OID,
	})
	s.put(state, &ceremony{
		flow: flowSign, orgID: orgID, userID: userID, slug: slug,
		baseURL: conn.baseURL, clientID: conn.clientID, clientSecret: conn.clientSecret,
		issuer: info.OAuth2, pkceVerifier: pkce.Verifier,
		requestID: requestID, credentialID: cred.ID, digestB64: digestB64, pades: pades,
	})
	return Start{RequestID: &requestID, AuthorizeURL: authURL}, nil
}

// HandleCallback resolves the OAuth callback (correlated by state), completes the
// link or sign flow, and returns the frontend URL to redirect the browser to.
func (s *Service) HandleCallback(ctx context.Context, code, state string) string {
	c := s.take(state)
	if c == nil {
		// Unknown/expired state: nowhere org-specific to send them.
		return s.appBaseURL
	}
	dest := s.resultURL(c, "")

	token, err := s.provider.ExchangeToken(ctx, c.issuer, c.clientID, c.clientSecret, code, c.pkceVerifier, s.redirectURI)
	if err != nil {
		return s.failCeremony(ctx, c, "authorization could not be completed", err)
	}

	switch c.flow {
	case flowLink:
		return s.finishLink(ctx, c, token.AccessToken)
	case flowSign:
		return s.finishSign(ctx, c, token.AccessToken)
	default:
		return dest
	}
}

func (s *Service) finishLink(ctx context.Context, c *ceremony, accessToken string) string {
	ids, err := s.provider.ListCredentials(ctx, c.baseURL, accessToken)
	if err != nil {
		return s.failCeremony(ctx, c, "could not list credentials", err)
	}
	if len(ids) == 0 {
		return s.failCeremony(ctx, c, "no signing credential available", signingprovider.ErrNoCredential)
	}
	cred, err := s.provider.CredentialInfo(ctx, c.baseURL, accessToken, ids[0])
	if err != nil {
		return s.failCeremony(ctx, c, "could not read the credential", err)
	}
	if err := s.store.UpsertCredential(ctx, c.orgID, c.userID, cred); err != nil {
		return s.failCeremony(ctx, c, "could not store the credential", err)
	}
	return s.resultURL(c, "link=ok")
}

func (s *Service) finishSign(ctx context.Context, c *ceremony, accessToken string) string {
	sigs, err := s.provider.SignHash(ctx, c.baseURL, accessToken, c.credentialID,
		[]string{c.digestB64}, signingprovider.SignAlgoECDSASHA256OID, signingprovider.HashAlgoSHA256OID)
	if err != nil {
		return s.failSign(ctx, c, "the signing provider rejected the signature request", err)
	}
	sig, err := base64.StdEncoding.DecodeString(sigs[0])
	if err != nil {
		return s.failSign(ctx, c, "the signature was not valid base64", err)
	}
	signed, err := c.pades.finish(sig)
	if err != nil {
		return s.failSign(ctx, c, "the signed document could not be assembled", err)
	}
	if err := s.store.CompleteRequest(ctx, c.orgID, c.requestID, signed); err != nil {
		return s.failSign(ctx, c, "the signed document could not be stored", err)
	}
	return s.resultURL(c, "request="+c.requestID.String())
}

// failSign abandons the parked PAdES pass and marks the request failed.
func (s *Service) failSign(ctx context.Context, c *ceremony, reason string, cause error) string {
	if c.pades != nil {
		c.pades.abandon(cause)
	}
	if err := s.store.FailRequest(ctx, c.orgID, c.requestID, reason); err != nil {
		slog.ErrorContext(ctx, "signing: mark request failed", slog.String("error", err.Error()))
	}
	slog.ErrorContext(ctx, "signing: sign ceremony failed", slog.String("reason", reason), slog.String("error", cause.Error()))
	return s.resultURL(c, "request="+c.requestID.String())
}

// failCeremony handles a link-flow (or pre-sign) failure with no request row.
func (s *Service) failCeremony(ctx context.Context, c *ceremony, reason string, cause error) string {
	if c.flow == flowSign {
		return s.failSign(ctx, c, reason, cause)
	}
	slog.ErrorContext(ctx, "signing: link ceremony failed", slog.String("reason", reason), slog.String("error", cause.Error()))
	return s.resultURL(c, "link=failed")
}

// StartSign/StartLink public read helpers delegate to the store.
func (s *Service) GetRequest(ctx context.Context, orgID, userID, id uuid.UUID) (Request, error) {
	return s.store.GetRequest(ctx, orgID, userID, id)
}

func (s *Service) GetSignedDocument(ctx context.Context, orgID, userID, id uuid.UUID) ([]byte, string, error) {
	return s.store.GetSignedDocument(ctx, orgID, userID, id)
}

// GetCredential reports the acting user's linked credential (for the UI to show
// whether linking is needed). Returns ErrNoCredential if none.
func (s *Service) GetCredential(ctx context.Context, orgID, userID uuid.UUID) (LinkedCredential, error) {
	cred, err := s.store.GetCredential(ctx, orgID, userID)
	if err != nil {
		return LinkedCredential{}, err
	}
	return LinkedCredential{CredentialID: cred.ID, KeyAlgo: joinAlgo(cred.KeyAlgo)}, nil
}

type connection struct{ baseURL, clientID, clientSecret string }

// Available reports whether the org has a signing provider configured and
// enabled. It is member-safe, so the signing feature can be gated for members who
// cannot read the admin-only provider settings.
func (s *Service) Available(ctx context.Context, orgID uuid.UUID) (bool, error) {
	return s.settings.Available(ctx, orgID)
}

func (s *Service) connection(ctx context.Context, orgID uuid.UUID) (connection, error) {
	baseURL, clientID, clientSecret, err := s.settings.ResolveConnection(ctx, orgID)
	if err != nil {
		return connection{}, err
	}
	if baseURL == "" || clientID == "" {
		return connection{}, ErrNotConfigured
	}
	return connection{baseURL: baseURL, clientID: clientID, clientSecret: clientSecret}, nil
}

// put stores an in-flight ceremony and arms its TTL.
func (s *Service) put(state string, c *ceremony) {
	c.timer = time.AfterFunc(SessionTTL, func() { s.expire(state) })
	s.mu.Lock()
	s.sessions[state] = c
	s.mu.Unlock()
}

// take removes and returns a ceremony, stopping its timer.
func (s *Service) take(state string) *ceremony {
	s.mu.Lock()
	c := s.sessions[state]
	delete(s.sessions, state)
	s.mu.Unlock()
	if c != nil && c.timer != nil {
		c.timer.Stop()
	}
	return c
}

// expire abandons a ceremony that outlived SessionTTL.
func (s *Service) expire(state string) {
	c := s.take(state)
	if c == nil {
		return
	}
	if c.flow == flowSign {
		c.pades.abandon(ErrSessionExpired)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.store.FailRequest(ctx, c.orgID, c.requestID, "authorization was not completed in time"); err != nil {
			slog.ErrorContext(ctx, "signing: expire request", slog.String("error", err.Error()))
		}
	}
}

func (s *Service) resultURL(c *ceremony, query string) string {
	// The frontend serves org pages at /{slug}/... (no /orgs prefix — that is the
	// API path, not the SPA route), so the post-ceremony redirect targets that.
	u := fmt.Sprintf("%s/%s/signing", s.appBaseURL, url.PathEscape(c.slug))
	if query != "" {
		u += "?" + query
	}
	return u
}

func newAuthArtifacts() (signingprovider.PKCE, string, error) {
	pkce, err := signingprovider.NewPKCE()
	if err != nil {
		return signingprovider.PKCE{}, "", err
	}
	state, err := signingprovider.NewState()
	if err != nil {
		return signingprovider.PKCE{}, "", err
	}
	return pkce, state, nil
}

func joinAlgo(a []string) string {
	return strings.Join(a, ",")
}
