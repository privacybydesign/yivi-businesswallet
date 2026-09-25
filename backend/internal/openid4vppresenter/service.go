package openid4vppresenter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/eudi/openid4vp"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// Consumer-defined seams, listing only what the flow uses.
type (
	transactionStore interface {
		Create(ctx context.Context, in NewTransaction) (string, error)
		Get(ctx context.Context, rawID string) (Transaction, error)
		BindUser(ctx context.Context, id, userID uuid.UUID) error
		SelectOrganization(ctx context.Context, id, orgID uuid.UUID, orgName string) (Transaction, error)
		Complete(ctx context.Context, id uuid.UUID) error
		Deny(ctx context.Context, id uuid.UUID, reason string) error
		CreateForOrganization(ctx context.Context, orgID, sourceMessageID uuid.UUID, in NewTransaction) (Transaction, bool, error)
	}
	organizationLister interface {
		ListForUser(ctx context.Context, userID uuid.UUID) ([]organization.Organization, error)
	}
	presenter interface {
		Present(ctx context.Context, orgID uuid.UUID, dcqlQuery []byte, nonce, audience string) (eudiholder.Presentation, error)
	}
	requestFetcher interface {
		Fetch(ctx context.Context, requestURI string) ([]byte, error)
	}
	responseSender interface {
		Send(ctx context.Context, t Transaction, token openid4vp.VpToken) (string, error)
	}
)

// Service orchestrates an inbound presentation: validate and persist the
// request, bind the authenticated user, list the organizations they may act
// for, record the selection, and — only under the dev-only auto-present flag —
// build and deliver the response. The organization is always the one resolved
// by the tenant seam from the caller's membership; a client-supplied hint is
// never authorization.
type Service struct {
	store     transactionStore
	orgs      organizationLister
	holder    presenter
	fetcher   requestFetcher
	validator Validator
	responder responseSender
	// autoPresent completes a presentation right after organization selection.
	// It stands in for the governance layer (#113) in dev / CI only.
	autoPresent bool
	now         func() time.Time
}

func NewService(store transactionStore, orgs organizationLister, holder presenter, fetcher requestFetcher, validator Validator, responder responseSender, autoPresent bool) *Service {
	return &Service{
		store:       store,
		orgs:        orgs,
		holder:      holder,
		fetcher:     fetcher,
		validator:   validator,
		responder:   responder,
		autoPresent: autoPresent,
		now:         time.Now,
	}
}

// StartRequest is the invocation as the browser received it from the verifier:
// the query parameters of GET /openid4vp, forwarded verbatim.
type StartRequest struct {
	ClientID         string
	RequestURI       string
	RequestURIMethod string
	// Request is the by-value JAR parameter. Supplying it is rejected: this slice
	// supports pass-by-reference only, and never guesses which of the two wins.
	Request string
}

// Start validates the invocation, fetches and validates the Request Object, and
// persists the transaction. It returns the opaque id the browser carries from
// here on — through login, if needed.
func (s *Service) Start(ctx context.Context, req StartRequest) (string, error) {
	method, ro, err := s.validate(ctx, req)
	if err != nil {
		return "", err
	}
	return s.store.Create(ctx, NewTransaction{
		ClientID:         req.ClientID,
		RequestURI:       req.RequestURI,
		RequestURIMethod: method,
		Request:          ro,
	})
}

// ReceiveFromQERDS validates an Authorization Request that arrived over QERDS
// (Receiver), already addressed to orgID by the receiving digital address, and
// queues it at StatusOrgSelected for that organization to decide on — skipping
// the browser's pending_auth/org-picker steps, which do not apply here. From
// there it is exactly the browser flow's post-selection state, decided by
// whatever the governance layer (#113) provides for any org_selected
// transaction, regardless of origin. Idempotent on sourceMessageID: a
// re-delivered message resolves to the row already queued (recorded=false).
func (s *Service) ReceiveFromQERDS(ctx context.Context, orgID, sourceMessageID uuid.UUID, req StartRequest) (Transaction, bool, error) {
	method, ro, err := s.validate(ctx, req)
	if err != nil {
		return Transaction{}, false, err
	}
	return s.store.CreateForOrganization(ctx, orgID, sourceMessageID, NewTransaction{
		ClientID:         req.ClientID,
		RequestURI:       req.RequestURI,
		RequestURIMethod: method,
		Request:          ro,
	})
}

// validate is the invocation-independent half of Start/ReceiveFromQERDS: reject
// the unsupported/ambiguous forms, normalize request_uri_method, then fetch and
// validate the Request Object. Neither caller persists anything this did not
// already validate.
func (s *Service) validate(ctx context.Context, req StartRequest) (string, RequestObject, error) {
	if req.ClientID == "" {
		return "", RequestObject{}, fmt.Errorf("%w: client_id is required", ErrInvalidRequest)
	}
	switch {
	case req.Request != "" && req.RequestURI != "":
		return "", RequestObject{}, fmt.Errorf("%w: request and request_uri are mutually exclusive", ErrInvalidRequest)
	case req.Request != "":
		return "", RequestObject{}, fmt.Errorf("%w: by-value request objects are not supported; use request_uri", ErrInvalidRequest)
	case req.RequestURI == "":
		return "", RequestObject{}, fmt.Errorf("%w: request_uri is required", ErrInvalidRequest)
	}
	method, err := normalizeRequestURIMethod(req.RequestURIMethod)
	if err != nil {
		return "", RequestObject{}, err
	}

	raw, err := s.fetcher.Fetch(ctx, req.RequestURI)
	if err != nil {
		return "", RequestObject{}, err
	}
	ro, err := s.validator.Validate(ctx, req.ClientID, raw)
	if err != nil {
		return "", RequestObject{}, err
	}
	return method, ro, nil
}

// normalizeRequestURIMethod validates request_uri_method: absent defaults to get
// (RFC 9101 behaviour), post is accepted and served by GET per OpenID4VP 1.0
// §5.10 for a wallet without post support, anything else is the standard error.
func normalizeRequestURIMethod(method string) (string, error) {
	switch openid4vp.RequestUriMethod(method) {
	case "", openid4vp.RequestUriMethod_Get:
		return string(openid4vp.RequestUriMethod_Get), nil
	case openid4vp.RequestUriMethod_Post:
		return string(openid4vp.RequestUriMethod_Post), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRequestURIMethod, method)
	}
}

// View is what the browser may learn about a transaction: enough to draw the
// right step, never the query or where the response goes.
type View struct {
	Status   string
	Verifier string
}

// Status reports the transaction's effective state. It is public (the id is the
// bearer, as with the outbound session status) so the pre-login screen can name
// the verifier and an expired link is explained instead of failing.
func (s *Service) Status(ctx context.Context, rawID string) (View, error) {
	t, err := s.store.Get(ctx, rawID)
	if err != nil {
		return View{}, err
	}
	return View{Status: t.EffectiveStatus(s.now()), Verifier: t.VerifierIdentity}, nil
}

// Organizations binds the authenticated user to a pending transaction and lists
// the organizations they are a member of — the same "my orgs" query GET /orgs
// uses, no new authorization logic.
func (s *Service) Organizations(ctx context.Context, rawID string, userID uuid.UUID) ([]organization.Organization, error) {
	if _, err := s.pending(ctx, rawID, userID); err != nil {
		return nil, err
	}
	return s.orgs.ListForUser(ctx, userID)
}

// pending loads the transaction, requires it to be pending, and binds or checks
// the user it belongs to.
func (s *Service) pending(ctx context.Context, rawID string, userID uuid.UUID) (Transaction, error) {
	t, err := s.store.Get(ctx, rawID)
	if err != nil {
		return Transaction{}, err
	}
	if t.EffectiveStatus(s.now()) != StatusPendingAuth {
		return Transaction{}, ErrNotPending
	}
	switch {
	case t.UserID == nil:
		if err := s.store.BindUser(ctx, t.ID, userID); err != nil {
			return Transaction{}, err
		}
	case *t.UserID != userID:
		return Transaction{}, ErrForbidden
	}
	return t, nil
}

// SelectResult is the outcome of an organization selection.
type SelectResult struct {
	Status string
	// RedirectURI is where the verifier asked to send the browser after a
	// completed presentation; empty when it gave none or nothing was sent yet.
	RedirectURI string
}

// Select records the caller's choice of organization. org has already been
// resolved by the tenant seam from the caller's membership. Without the
// auto-present flag the transaction rests at org_selected for the governance
// layer; with it, the presentation is built and delivered now.
func (s *Service) Select(ctx context.Context, rawID string, userID uuid.UUID, org organization.Organization) (SelectResult, error) {
	t, err := s.pending(ctx, rawID, userID)
	if err != nil {
		return SelectResult{}, err
	}
	t, err = s.store.SelectOrganization(ctx, t.ID, org.ID, org.Name)
	if err != nil {
		return SelectResult{}, err
	}
	if !s.autoPresent {
		return SelectResult{Status: StatusOrgSelected}, nil
	}
	redirect, err := s.present(ctx, t, org.ID)
	if err != nil {
		return SelectResult{}, err
	}
	return SelectResult{Status: StatusCompleted, RedirectURI: redirect}, nil
}

// present builds the vp_token for orgID and delivers it. Any failure consumes the
// transaction as denied — one-time use means no second attempt on the same
// nonce — and is logged without the query or response material. An organization
// that holds nothing matching is a denial with its own reason, not a failure.
func (s *Service) present(ctx context.Context, t Transaction, orgID uuid.UUID) (string, error) {
	p, err := s.holder.Present(ctx, orgID, t.DCQLQuery, t.Nonce, t.ClientID)
	if errors.Is(err, eudiholder.ErrNoMatchingCredential) {
		if err := s.store.Deny(ctx, t.ID, ErrNoMatchingCredential.Error()); err != nil && !errors.Is(err, ErrNotPending) {
			return "", err
		}
		return "", ErrNoMatchingCredential
	}
	if err != nil {
		return "", s.fail(ctx, t.ID, "holder_present", err)
	}
	redirect, err := s.responder.Send(ctx, t, p.VPToken)
	if err != nil {
		return "", s.fail(ctx, t.ID, "direct_post", err)
	}
	if err := s.store.Complete(ctx, t.ID); err != nil {
		return "", err
	}
	return redirect, nil
}

func (s *Service) fail(ctx context.Context, id uuid.UUID, step string, cause error) error {
	slog.ErrorContext(ctx, "openid4vp presentation failed",
		slog.String("transaction", id.String()),
		slog.String("step", step),
		slog.String("error", cause.Error()),
	)
	if err := s.store.Deny(ctx, id, step); err != nil && !errors.Is(err, ErrNotPending) {
		return err
	}
	return fmt.Errorf("%w: %s", ErrPresentationFailed, step)
}
