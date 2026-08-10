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

// Service orchestrates the RP-centric co-signing ceremony: it resolves the org's
// CSC connection, drives each signer's QTSP OAuth2 authorization (whose OID4VP
// wallet step the browser performs), asks the QTSP to sign the current document
// hash, and assembles the accumulating PAdES. In-flight authorizations are held in
// memory keyed by the OAuth state (see padesSession for the single-instance /
// no-restart caveat); a per-request in-flight lock serialises the signers of one
// document, because incremental PAdES cannot merge concurrent signatures.
type Service struct {
	store       *Store
	provider    provider
	settings    connectionResolver
	members     memberDirectory
	deliverer   documentDeliverer
	notifier    signerNotifier
	redirectURI string
	appBaseURL  string
	// issuerInternal, when set, is the OAuth issuer base the backend uses for its
	// own server-side token exchange, instead of the issuer discovered from the
	// provider's /info (which is the browser-facing one used to build the authorize
	// URL). It exists only because in local Docker the authorization server is
	// reachable at a different host from the browser (localhost:8084) than from the
	// backend container (qtsp-authz:8084); in production the two are the same URL and
	// this stays empty. Only the host differs — the token/claims are unaffected.
	issuerInternal string

	mu       sync.Mutex
	sessions map[string]*ceremony
	// active guards one in-flight signing ceremony per request id, so two signers of
	// a parallel request cannot both prepare against the same base document and clobber
	// each other's incremental signature. It maps to the holding ceremony so the same
	// signer re-initiating can reclaim their own stale slot (see reserveSign).
	// Protected by mu.
	active map[uuid.UUID]*ceremony
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

	// state is this ceremony's own OAuth-state key in sessions, so it can be
	// discarded (removed from sessions) when the same signer reclaims the slot.
	state string
	timer *time.Timer
}

const (
	flowLink = "link"
	flowSign = "sign"
)

// NewService builds the signing service. redirectURI is the QTSP-registered OAuth
// callback (must match the authorization server's client registration);
// appBaseURL is where the browser is sent after the callback resolves. members
// lists the org's members (to validate/label selected signers); deliverer delivers
// a completed document to its recipient (may be nil, in which case a request with a
// recipient records a delivery failure). notifier tells a selected member a document
// is waiting for their signature (may be nil to disable signer notifications).
// issuerInternal is an optional override for the backend's server-side token
// exchange host (see the Service field); empty in production.
func NewService(store *Store, p provider, settings connectionResolver, members memberDirectory, deliverer documentDeliverer, notifier signerNotifier, redirectURI, appBaseURL, issuerInternal string) *Service {
	return &Service{
		store:          store,
		provider:       p,
		settings:       settings,
		members:        members,
		deliverer:      deliverer,
		notifier:       notifier,
		redirectURI:    redirectURI,
		appBaseURL:     appBaseURL,
		issuerInternal: issuerInternal,
		sessions:       make(map[string]*ceremony),
		active:         make(map[uuid.UUID]*ceremony),
	}
}

// tokenIssuer returns the OAuth issuer base to use for the backend's own
// server-side token exchange: the internal override when configured (local Docker,
// where the browser-facing issuer host is not reachable from the backend), else
// the issuer discovered from the provider (the production case).
func (s *Service) tokenIssuer(discovered string) string {
	if s.issuerInternal != "" {
		return s.issuerInternal
	}
	return discovered
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

// CreateRequest validates the selected signers, mode and recipient, stores the
// uploaded PDF as a new co-signing request awaiting signatures, and returns its id.
// It does not itself sign — each selected signer signs later via StartSign. The
// creator need not be a signer. signerIDs order is the signing order for sequential
// mode (1-based); it is ignored in parallel mode. slug is used to build the signing
// link in the notifications sent to the signers.
func (s *Service) CreateRequest(ctx context.Context, orgID, createdBy uuid.UUID, slug, filename string, pdf []byte, signerIDs []uuid.UUID, mode string, rec RecipientInput) (uuid.UUID, error) {
	if _, err := s.connection(ctx, orgID); err != nil {
		return uuid.Nil, err
	}
	if mode != ModeParallel && mode != ModeSequential {
		return uuid.Nil, ErrInvalidRequest
	}
	if err := validateRecipient(rec); err != nil {
		return uuid.Nil, err
	}
	if len(signerIDs) == 0 {
		return uuid.Nil, ErrInvalidRequest
	}
	if err := validatePDF(pdf); err != nil {
		return uuid.Nil, err
	}
	members, err := s.members.ListMembers(ctx, orgID)
	if err != nil {
		return uuid.Nil, err
	}
	valid := make(map[uuid.UUID]bool, len(members))
	for _, m := range members {
		valid[m.UserID] = true
	}
	seen := make(map[uuid.UUID]bool, len(signerIDs))
	signers := make([]SignerInput, 0, len(signerIDs))
	for i, id := range signerIDs {
		if !valid[id] || seen[id] {
			return uuid.Nil, ErrInvalidRequest
		}
		seen[id] = true
		signers = append(signers, SignerInput{UserID: id, Order: i + 1})
	}
	requestID, err := s.store.CreateRequest(ctx, orgID, createdBy, filename, pdf, mode, signers, rec)
	if err != nil {
		return uuid.Nil, err
	}
	// Notify the signers it is their turn: everyone in parallel mode, only the first
	// (lowest order) in sequential mode — later signers are notified as their turn
	// comes (see finishSign). Best-effort; a mail failure must not fail the create.
	notify := signers
	if mode == ModeSequential {
		notify = signers[:1]
	}
	for _, sg := range notify {
		s.notifySigner(ctx, orgID, slug, filename, sg.UserID, members)
	}
	return requestID, nil
}

// StartSign begins the acting user's signing ceremony for a request: it checks the
// user is a pending signer whose turn it is, takes the per-request in-flight lock,
// prepares the current document (pass 1) to obtain the hash, binds that hash into a
// credential-scope authorization, and returns the authorize URL. The callback
// finishes the signature.
func (s *Service) StartSign(ctx context.Context, orgID, userID uuid.UUID, slug string, requestID uuid.UUID) (Start, error) {
	conn, err := s.connection(ctx, orgID)
	if err != nil {
		return Start{}, err
	}
	req, err := s.store.GetRequest(ctx, orgID, requestID)
	if err != nil {
		return Start{}, err
	}
	if req.Status != StatusAwaitingSignatures {
		return Start{}, ErrInvalidRequest
	}
	if err := checkTurn(req, userID); err != nil {
		return Start{}, err
	}
	if !s.reserveSign(requestID, userID) {
		return Start{}, ErrSignInProgress
	}
	cred, err := s.store.GetCredential(ctx, orgID, userID)
	if err != nil {
		s.release(requestID)
		return Start{}, err
	}
	doc, _, err := s.store.GetLatestDocument(ctx, orgID, requestID)
	if err != nil {
		s.release(requestID)
		return Start{}, err
	}
	pades, digest, err := startPAdES(doc, cred)
	if err != nil {
		s.release(requestID)
		return Start{}, err
	}
	info, err := s.provider.Discover(ctx, conn.baseURL)
	if err != nil {
		pades.abandon(err)
		s.release(requestID)
		return Start{}, err
	}
	pkce, state, err := newAuthArtifacts()
	if err != nil {
		pades.abandon(err)
		s.release(requestID)
		return Start{}, err
	}
	digestB64 := base64.StdEncoding.EncodeToString(digest)
	authURL := s.provider.AuthorizeURL(info.OAuth2, signingprovider.AuthorizeParams{
		ClientID:      conn.clientID,
		RedirectURI:   s.redirectURI,
		State:         state,
		CodeChallenge: pkce.Challenge,
		Scope:         signingprovider.ScopeCredential,
		CredentialID:  cred.ID,
		NumSignatures: 1,
		// The authorize `hashes` param is base64url (the QTSP decodes it with
		// getUrlDecoder); the token claim it becomes — and therefore the signHash
		// request below — is standard base64 (digestB64). Same digest, two encodings.
		Hashes:           []string{base64.RawURLEncoding.EncodeToString(digest)},
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

	token, err := s.provider.ExchangeToken(ctx, s.tokenIssuer(c.issuer), c.clientID, c.clientSecret, code, c.pkceVerifier, s.redirectURI)
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
	allSigned, err := s.store.RecordSignature(ctx, c.orgID, c.requestID, c.userID, signed)
	if err != nil {
		return s.failSign(ctx, c, "the signature could not be stored", err)
	}
	s.release(c.requestID)
	if allSigned {
		if err := s.store.CompleteRequest(ctx, c.orgID, c.requestID); err != nil {
			slog.ErrorContext(ctx, "signing: complete request", slog.String("error", err.Error()))
		}
		// Deliver off the request goroutine: it is best-effort (outcome polled from
		// delivery_status) and would otherwise make the last signer's browser wait on
		// the SMTP/QERDS round trip carrying the full PDF before the redirect. Its own
		// background context also survives the redirect that cancels the request's ctx.
		s.deliverAsync(c.orgID, c.requestID)
	} else {
		// Sequential mode: it is now the next signer's turn — let them know.
		s.notifyNextSequential(ctx, c.orgID, c.slug, c.requestID)
	}
	return s.resultURL(c, "request="+c.requestID.String())
}

// DeliverTimeout bounds a background delivery (SMTP conversation or QERDS round
// trip) so a stalled recipient server cannot leak the goroutine indefinitely.
const DeliverTimeout = 2 * time.Minute

// deliverAsync runs deliver on its own goroutine with a detached, bounded context,
// so a completed request's callback returns to the browser immediately and the
// delivery outcome is still recorded after the request's own context is cancelled.
// It recovers panics: a panic on a spawned goroutine would otherwise kill the process.
func (s *Service) deliverAsync(orgID, requestID uuid.UUID) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("signing: deliver panicked", slog.Any("recover", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), DeliverTimeout)
		defer cancel()
		s.deliver(ctx, orgID, requestID)
	}()
}

// deliver dispatches a completed request's signed document to its recipient over
// the chosen channel and records the outcome. It is best-effort: a failure leaves
// the request completed with delivery_status=failed, surfaced in the history.
func (s *Service) deliver(ctx context.Context, orgID, requestID uuid.UUID) {
	req, err := s.store.GetRequest(ctx, orgID, requestID)
	if err != nil {
		slog.ErrorContext(ctx, "signing: load request for delivery", slog.String("error", err.Error()))
		return
	}
	if req.RecipientChannel == ChannelNone {
		return
	}
	doc, filename, err := s.store.GetSignedDocument(ctx, orgID, requestID)
	if err != nil {
		s.setDeliveryFailed(ctx, orgID, requestID, "the signed document was not available")
		return
	}
	if s.deliverer == nil {
		s.setDeliveryFailed(ctx, orgID, requestID, "no delivery channel is configured on this deployment")
		return
	}
	switch req.RecipientChannel {
	case ChannelEmail:
		err = s.deliverer.DeliverEmail(ctx, orgID, req.RecipientAddress, req.RecipientName, req.Message, filename, doc)
	case ChannelQERDS:
		err = s.deliverer.DeliverQERDS(ctx, orgID, req.RecipientAddress, req.RecipientName, filename, req.Message, filename, doc)
	default:
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "signing: deliver signed document",
			slog.String("channel", req.RecipientChannel), slog.String("error", err.Error()))
		s.setDeliveryFailed(ctx, orgID, requestID, "the signed document could not be delivered")
		return
	}
	if err := s.store.SetDelivery(ctx, orgID, requestID, DeliveryDelivered, ""); err != nil {
		slog.ErrorContext(ctx, "signing: record delivery", slog.String("error", err.Error()))
	}
}

func (s *Service) setDeliveryFailed(ctx context.Context, orgID, requestID uuid.UUID, reason string) {
	if err := s.store.SetDelivery(ctx, orgID, requestID, DeliveryFailed, reason); err != nil {
		slog.ErrorContext(ctx, "signing: record delivery failure", slog.String("error", err.Error()))
	}
}

// notifySigner e-mails one signer that a document awaits their signature, resolving
// their address from the already-fetched member list. Best-effort: a nil notifier,
// an unknown address, or a send failure is logged, never fatal.
func (s *Service) notifySigner(ctx context.Context, orgID uuid.UUID, slug, documentName string, userID uuid.UUID, members []OrgMember) {
	if s.notifier == nil {
		return
	}
	var email string
	for _, m := range members {
		if m.UserID == userID {
			email = m.Email
			break
		}
	}
	if email == "" {
		return
	}
	if err := s.notifier.NotifySignatureRequested(ctx, orgID, email, documentName, slug); err != nil {
		slog.WarnContext(ctx, "signing: notify signer", slog.String("error", err.Error()))
	}
}

// notifyNextSequential notifies the signer whose turn it now is after a signature
// completes in a sequential request (the lowest-order still-pending signer). It is a
// no-op for a parallel request, where everyone was notified at create time.
func (s *Service) notifyNextSequential(ctx context.Context, orgID uuid.UUID, slug string, requestID uuid.UUID) {
	if s.notifier == nil {
		return
	}
	req, err := s.store.GetRequest(ctx, orgID, requestID)
	if err != nil {
		slog.WarnContext(ctx, "signing: load request to notify next signer", slog.String("error", err.Error()))
		return
	}
	if req.Mode != ModeSequential {
		return
	}
	var next *Signer
	for i := range req.Signers {
		if req.Signers[i].Status == SignerPending {
			next = &req.Signers[i] // signers come ordered by sign_order, so this is the next turn
			break
		}
	}
	if next == nil {
		return
	}
	members, err := s.members.ListMembers(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "signing: list members to notify next signer", slog.String("error", err.Error()))
		return
	}
	s.notifySigner(ctx, orgID, slug, req.Filename, next.UserID, members)
}

// failSign abandons the parked PAdES pass, releases the in-flight lock and marks
// only the acting signer's attempt failed — never the whole request. The request
// stays awaiting_signatures so a transient error (a QTSP reject, a token-exchange
// blip) leaves it retryable and does not strand co-signers' already-applied
// signatures. The failed signer is offered the document again (ListPendingForUser).
func (s *Service) failSign(ctx context.Context, c *ceremony, reason string, cause error) string {
	if c.pades != nil {
		c.pades.abandon(cause)
	}
	s.release(c.requestID)
	if err := s.store.MarkSignerFailed(ctx, c.orgID, c.requestID, c.userID, reason); err != nil {
		slog.ErrorContext(ctx, "signing: mark signer failed", slog.String("error", err.Error()))
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

// GetRequest returns a request with signer names, enforcing that a non-admin caller
// is the creator or a signer (else ErrNotFound, so existence is not leaked).
func (s *Service) GetRequest(ctx context.Context, orgID, userID, id uuid.UUID, isAdmin bool) (Request, error) {
	req, err := s.store.GetRequest(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if !isAdmin && !involves(req, userID) {
		return Request{}, ErrNotFound
	}
	s.enrich(ctx, orgID, []*Request{&req})
	return req, nil
}

// GetSignedDocument returns the final signed PDF, enforcing the same access rule as
// GetRequest for a non-admin caller.
func (s *Service) GetSignedDocument(ctx context.Context, orgID, userID, id uuid.UUID, isAdmin bool) ([]byte, string, error) {
	if !isAdmin {
		req, err := s.store.GetRequest(ctx, orgID, id)
		if err != nil {
			return nil, "", err
		}
		if !involves(req, userID) {
			return nil, "", ErrNotFound
		}
	}
	return s.store.GetSignedDocument(ctx, orgID, id)
}

// ListPending returns the requests awaiting the acting user's signature (their turn).
func (s *Service) ListPending(ctx context.Context, orgID, userID uuid.UUID) ([]Request, error) {
	reqs, err := s.store.ListPendingForUser(ctx, orgID, userID)
	if err != nil {
		return nil, err
	}
	s.enrichSlice(ctx, orgID, reqs)
	return reqs, nil
}

// ListRequests returns the org's signing requests, newest first, cursor-paginated.
func (s *Service) ListRequests(ctx context.Context, orgID uuid.UUID, cursor string, limit int) ([]Request, string, error) {
	reqs, next, err := s.store.ListRequests(ctx, orgID, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	s.enrichSlice(ctx, orgID, reqs)
	return reqs, next, nil
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

// enrich fills CreatedByName and per-signer name/email from the member directory. A
// directory failure is cosmetic (names stay blank) and never blocks the response.
func (s *Service) enrich(ctx context.Context, orgID uuid.UUID, reqs []*Request) {
	if s.members == nil || len(reqs) == 0 {
		return
	}
	members, err := s.members.ListMembers(ctx, orgID)
	if err != nil {
		slog.WarnContext(ctx, "signing: enrich signer names", slog.String("error", err.Error()))
		return
	}
	byID := make(map[uuid.UUID]OrgMember, len(members))
	for _, m := range members {
		byID[m.UserID] = m
	}
	for _, r := range reqs {
		if m, ok := byID[r.CreatedBy]; ok {
			r.CreatedByName = m.Name
		}
		for i := range r.Signers {
			if m, ok := byID[r.Signers[i].UserID]; ok {
				r.Signers[i].Name = m.Name
				r.Signers[i].Email = m.Email
			}
		}
	}
}

func (s *Service) enrichSlice(ctx context.Context, orgID uuid.UUID, reqs []Request) {
	ptrs := make([]*Request, len(reqs))
	for i := range reqs {
		ptrs[i] = &reqs[i]
	}
	s.enrich(ctx, orgID, ptrs)
}

// reserveSign takes the per-request in-flight slot for userID's new signing
// ceremony. If another user's ceremony holds it, it returns false and the caller
// answers ErrSignInProgress. If this same user already holds one — a reload, or an
// attempt they walked away from — that stale ceremony is discarded (its parked
// pdfsign pass abandoned, its timer stopped, its session dropped) so the user can
// start over without waiting out SessionTTL. A reservation marker holds the slot
// through the build; put replaces it with the real ceremony.
func (s *Service) reserveSign(requestID, userID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.active[requestID]; existing != nil {
		if existing.userID != userID {
			return false
		}
		s.discardLocked(existing)
	}
	s.active[requestID] = &ceremony{flow: flowSign, requestID: requestID, userID: userID}
	return true
}

// discardLocked tears down a ceremony being replaced or abandoned. The caller
// holds s.mu. It is safe on a reservation marker (nil pades, empty state).
func (s *Service) discardLocked(c *ceremony) {
	if c.timer != nil {
		c.timer.Stop()
	}
	if c.state != "" {
		delete(s.sessions, c.state)
	}
	if c.pades != nil {
		c.pades.abandon(ErrSessionExpired)
	}
	delete(s.active, c.requestID)
}

func (s *Service) release(requestID uuid.UUID) {
	s.mu.Lock()
	delete(s.active, requestID)
	s.mu.Unlock()
}

// put stores an in-flight ceremony and arms its TTL.
func (s *Service) put(state string, c *ceremony) {
	c.state = state
	c.timer = time.AfterFunc(SessionTTL, func() { s.expire(state) })
	s.mu.Lock()
	s.sessions[state] = c
	// Replace the reservation marker reserveSign put in active with the real
	// ceremony, so a same-user re-take can discard this parked pass.
	if c.flow == flowSign {
		s.active[c.requestID] = c
	}
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
		// A ceremony that outlived SessionTTL means the signer never finished the
		// wallet step. Do NOT fail the whole multi-party request over one abandoned
		// attempt: the signer is still `pending` and the request still
		// `awaiting_signatures`, so just abandon the parked pass and free the
		// in-flight lock, leaving it retryable by that signer or the next.
		c.pades.abandon(ErrSessionExpired)
		s.release(c.requestID)
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

// checkTurn reports whether userID may start a signature now: they must be a
// pending signer, and in sequential mode every lower-order signer must already have
// signed.
func checkTurn(req Request, userID uuid.UUID) error {
	var me *Signer
	for i := range req.Signers {
		if req.Signers[i].UserID == userID {
			me = &req.Signers[i]
			break
		}
	}
	if me == nil {
		return ErrNotSigner
	}
	if me.Status == SignerSigned {
		return ErrAlreadySigned
	}
	if req.Mode == ModeSequential {
		for _, sg := range req.Signers {
			if sg.Order < me.Order && sg.Status != SignerSigned {
				return ErrNotYourTurn
			}
		}
	}
	return nil
}

// involves reports whether userID is the creator or a signer of the request.
func involves(req Request, userID uuid.UUID) bool {
	if req.CreatedBy == userID {
		return true
	}
	for _, sg := range req.Signers {
		if sg.UserID == userID {
			return true
		}
	}
	return false
}

func validateRecipient(rec RecipientInput) error {
	switch rec.Channel {
	case ChannelNone:
		return nil
	case ChannelQERDS, ChannelEmail:
		if strings.TrimSpace(rec.Address) == "" {
			return ErrInvalidRequest
		}
		return nil
	default:
		return ErrInvalidRequest
	}
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
