package openid4vprequester

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vppresenter"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/relyingparty"
)

const (
	maxCredentials     = 10
	maxClaims          = 50
	maxVCTLength       = 255
	maxClaimNameLength = 128
	queryIDPrefix      = "credential_"

	// messageSubject is the QERDS subject line of the invocation; the body is the
	// machine-readable envelope, which carries its own human-readable fallback.
	messageSubject = "Credential request"

	// apiPrefix is where the public endpoints live under the public URL; the
	// handler registers them on the /api/v1 sub-mux.
	apiPrefix = "/api/v1/openid4vp/outbound/"
)

// Consumer-defined seams, listing only what the flow uses.
type (
	requestStore interface {
		Create(ctx context.Context, in NewRequest) (Request, error)
		SetMessage(ctx context.Context, id, messageID uuid.UUID) error
		Get(ctx context.Context, id uuid.UUID) (Request, error)
		GetForOrg(ctx context.Context, orgID, id uuid.UUID) (Request, error)
		ListForOrg(ctx context.Context, orgID uuid.UUID) ([]Request, error)
		FetchRequestObject(ctx context.Context, id uuid.UUID) (string, error)
		Complete(ctx context.Context, id uuid.UUID, disclosed []DisclosedCredential) error
		Fail(ctx context.Context, id uuid.UUID, reason string) error
	}
	messenger interface {
		ResolveSender(ctx context.Context, orgID uuid.UUID, from string) (qerds.Address, error)
		Send(ctx context.Context, orgID uuid.UUID, from, recipient, subject, body string, attachments []qerdsprovider.Attachment) (qerds.Message, error)
	}
	identityIssuer interface {
		IssueOrganization(orgName, address string, lifetime time.Duration) (relyingparty.Signer, error)
	}
	tokenVerifier interface {
		Verify(token map[string][]string, nonce, clientID string) []relyingparty.Presented
	}
)

// Service runs the outbound flow: sign and send a request, serve its Request
// Object, and accept or refuse the answer.
type Service struct {
	store     requestStore
	qerds     messenger
	ca        identityIssuer
	verifier  tokenVerifier
	publicURL string
	ttl       time.Duration
	now       func() time.Time
}

// NewService builds the flow. publicURL is the base (no /api/v1) another
// wallet reaches this backend on; ttl bounds each request.
func NewService(store requestStore, qerds messenger, ca identityIssuer, verifier tokenVerifier, publicURL string, ttl time.Duration) *Service {
	return &Service{
		store:     store,
		qerds:     qerds,
		ca:        ca,
		verifier:  verifier,
		publicURL: strings.TrimRight(publicURL, "/"),
		ttl:       ttl,
		now:       time.Now,
	}
}

// SendInput is what an admin asks for: which organization (its QERDS address),
// from which of this organization's addresses (empty: the default), and which
// credentials.
type SendInput struct {
	From        string
	Recipient   string
	Credentials []CredentialRequest
}

// Send certifies org as relying party for this one request, signs the
// Authorization Request, stores it and sends the invocation over QERDS. The
// certificate names the sending address, which is what the receiving wallet
// binds the request to (openid4vppresenter.Service.ReceiveFromQERDS).
func (s *Service) Send(ctx context.Context, org organization.Organization, createdBy uuid.UUID, in SendInput) (Request, error) {
	recipient := strings.TrimSpace(in.Recipient)
	if recipient == "" {
		return Request{}, fmt.Errorf("%w: recipient is required", ErrInvalidInput)
	}
	creds, err := normalizeCredentials(in.Credentials)
	if err != nil {
		return Request{}, err
	}
	sender, err := s.qerds.ResolveSender(ctx, org.ID, strings.TrimSpace(in.From))
	if err != nil {
		return Request{}, err
	}

	signer, err := s.ca.IssueOrganization(org.Name, sender.Address, s.ttl)
	if err != nil {
		return Request{}, err
	}
	nonce, err := relyingparty.RandomToken()
	if err != nil {
		return Request{}, err
	}
	state, err := relyingparty.RandomToken()
	if err != nil {
		return Request{}, err
	}
	queries := make([]relyingparty.CredentialQuery, 0, len(creds))
	for _, c := range creds {
		queries = append(queries, relyingparty.CredentialQuery{ID: c.ID, VCT: c.VCT, Claims: c.Claims})
	}
	dcql, err := relyingparty.BuildDCQL(queries)
	if err != nil {
		return Request{}, err
	}
	id := uuid.New()
	jar, err := relyingparty.SignRequestObject(signer, relyingparty.Request{
		Nonce:        nonce,
		State:        state,
		ResponseURI:  s.endpoint(id, "response"),
		ResponseMode: string(openid4vp.ResponseMode_DirectPost),
		DCQLQuery:    dcql,
		Lifetime:     s.ttl,
	})
	if err != nil {
		return Request{}, err
	}

	r, err := s.store.Create(ctx, NewRequest{
		ID:               id,
		OrganizationID:   org.ID,
		CreatedBy:        &createdBy,
		SenderAddress:    sender.Address,
		RecipientAddress: recipient,
		ClientID:         signer.ClientID,
		Nonce:            nonce,
		State:            state,
		Credentials:      creds,
		RequestObject:    jar,
		ExpiresAt:        s.now().Add(s.ttl),
	})
	if err != nil {
		return Request{}, err
	}

	body, err := openid4vppresenter.MarshalPresentationRequestEnvelope(
		org.Name, signer.ClientID, s.endpoint(id, "request-object"), string(openid4vp.RequestUriMethod_Get))
	if err != nil {
		return Request{}, s.failDelivery(ctx, r, err)
	}
	msg, err := s.qerds.Send(ctx, org.ID, sender.Address, recipient, messageSubject, body, nil)
	if err != nil {
		return Request{}, s.failDelivery(ctx, r, err)
	}
	if err := s.store.SetMessage(ctx, r.ID, msg.ID); err != nil {
		return Request{}, err
	}
	r.QerdsMessageID = &msg.ID
	return r, nil
}

// failDelivery consumes a request whose invocation never left, so it does not
// sit in the list as sent, and reports the delivery failure.
func (s *Service) failDelivery(ctx context.Context, r Request, cause error) error {
	slog.ErrorContext(ctx, "openid4vprequester: presentation request not delivered",
		slog.String("requestId", r.ID.String()),
		slog.String("error", cause.Error()))
	if err := s.store.Fail(ctx, r.ID, ReasonDeliveryFailed); err != nil {
		return err
	}
	return fmt.Errorf("%w: %w", ErrDeliveryFailed, cause)
}

func (s *Service) endpoint(id uuid.UUID, leaf string) string {
	return s.publicURL + apiPrefix + id.String() + "/" + leaf
}

// normalizeCredentials trims and checks what an admin asked for and assigns the
// DCQL query ids, so nothing a client sent becomes an identifier by itself.
func normalizeCredentials(in []CredentialRequest) ([]CredentialRequest, error) {
	if len(in) == 0 || len(in) > maxCredentials {
		return nil, fmt.Errorf("%w: ask for 1 to %d credentials", ErrInvalidInput, maxCredentials)
	}
	out := make([]CredentialRequest, 0, len(in))
	for i, c := range in {
		vct := strings.TrimSpace(c.VCT)
		if !validToken(vct, maxVCTLength) {
			return nil, fmt.Errorf("%w: credential %d: invalid credential type", ErrInvalidInput, i+1)
		}
		if len(c.Claims) > maxClaims {
			return nil, fmt.Errorf("%w: credential %d: at most %d claims", ErrInvalidInput, i+1, maxClaims)
		}
		claims := make([]string, 0, len(c.Claims))
		seen := map[string]bool{}
		for _, name := range c.Claims {
			name = strings.TrimSpace(name)
			if !validToken(name, maxClaimNameLength) {
				return nil, fmt.Errorf("%w: credential %d: invalid claim name", ErrInvalidInput, i+1)
			}
			if !seen[name] {
				seen[name] = true
				claims = append(claims, name)
			}
		}
		out = append(out, CredentialRequest{ID: queryIDPrefix + strconv.Itoa(i+1), VCT: vct, Claims: claims})
	}
	return out, nil
}

// validToken accepts a non-empty identifier of printable, non-space characters.
func validToken(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// RequestObject serves the signed Request Object for the receiving wallet's
// request_uri fetch, once.
func (s *Service) RequestObject(ctx context.Context, id uuid.UUID) (string, error) {
	return s.store.FetchRequestObject(ctx, id)
}

// Respond accepts the receiving wallet's direct_post answer. A post without the
// request's state is refused without touching the request: the id travels in
// the QERDS message, the state only inside the signed Request Object, so a
// third party cannot burn a request by posting to its URL. A post with the
// right state is the holder's one answer: it completes the request only if
// every credential asked for came back verified — issuer signature, key
// binding over this nonce and client_id, the requested type — and otherwise
// consumes it as failed.
func (s *Service) Respond(ctx context.Context, id uuid.UUID, form url.Values) error {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if r.EffectiveStatus(s.now()) != StatusSent {
		return ErrNotPending
	}
	if form.Get("state") != r.State {
		return fmt.Errorf("%w: state does not match", ErrInvalidResponse)
	}
	raw := form.Get("vp_token")
	if raw == "" {
		return fmt.Errorf("%w: vp_token is required", ErrInvalidResponse)
	}
	token, err := relyingparty.ParseVPToken(raw)
	if err != nil {
		return s.refuse(ctx, r, ReasonVerificationFailed, err)
	}
	disclosed, reason, err := s.check(r, token)
	if err != nil {
		return s.refuse(ctx, r, reason, err)
	}
	return s.store.Complete(ctx, r.ID, disclosed)
}

// check verifies token against what r asked for and returns the disclosure.
func (s *Service) check(r Request, token map[string][]string) ([]DisclosedCredential, string, error) {
	asked := make(map[string]CredentialRequest, len(r.Credentials))
	for _, c := range r.Credentials {
		asked[c.ID] = c
	}
	for queryID, presentations := range token {
		if _, ok := asked[queryID]; !ok || len(presentations) != 1 {
			return nil, ReasonIncompleteResponse, fmt.Errorf("unexpected presentations for %q", queryID)
		}
	}
	if len(token) != len(asked) {
		return nil, ReasonIncompleteResponse, errors.New("not every requested credential was presented")
	}
	var out []DisclosedCredential
	for _, p := range s.verifier.Verify(token, r.Nonce, r.ClientID) {
		if !p.Verified {
			return nil, ReasonVerificationFailed, fmt.Errorf("presentation %q: %s", p.QueryID, p.Error)
		}
		if want := asked[p.QueryID].VCT; p.VCT != want {
			return nil, ReasonVerificationFailed, fmt.Errorf("presentation %q is a %q, want %q", p.QueryID, p.VCT, want)
		}
		out = append(out, DisclosedCredential{QueryID: p.QueryID, VCT: p.VCT, Issuer: p.Issuer, Claims: p.Claims})
	}
	if len(out) != len(asked) {
		return nil, ReasonIncompleteResponse, errors.New("not every requested credential was verified")
	}
	return out, "", nil
}

func (s *Service) refuse(ctx context.Context, r Request, reason string, cause error) error {
	slog.WarnContext(ctx, "openid4vprequester: presentation response refused",
		slog.String("requestId", r.ID.String()),
		slog.String("reason", reason),
		slog.String("error", cause.Error()))
	if err := s.store.Fail(ctx, r.ID, reason); err != nil {
		return err
	}
	return fmt.Errorf("%w: %s", ErrInvalidResponse, reason)
}

// List returns the organization's recent outbound requests.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]Request, error) {
	return s.store.ListForOrg(ctx, orgID)
}

// Get returns one of the organization's outbound requests.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (Request, error) {
	return s.store.GetForOrg(ctx, orgID, id)
}

// Now is the clock EffectiveStatus is applied with, shared with the handler.
func (s *Service) Now() time.Time { return s.now() }
