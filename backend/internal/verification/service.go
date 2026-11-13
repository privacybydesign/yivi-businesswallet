package verification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vpverifier"
)

// inboundPath is the SPA entry an external verifier redirects a holder to (see
// .ai/features/openid4vp-inbound.md §7); browserLink targets it.
const inboundPath = "/openid4vp"

// Consumer-defined seams, listing only what the flow uses.
type (
	templateReader interface {
		GetTemplate(ctx context.Context, orgID, id uuid.UUID) (Template, error)
	}
	sessionStore interface {
		CreateSession(ctx context.Context, in NewSession) (Session, error)
		GetSession(ctx context.Context, orgID, id uuid.UUID) (Session, error)
		ListSessions(ctx context.Context, orgID uuid.UUID) ([]Session, error)
		CompleteSession(ctx context.Context, orgID, id uuid.UUID, res Result) (Session, error)
	}
	// verifier is the slice of openid4vpverifier.Client the flow needs; the id
	// handed to Result is the verifier's transaction_id, never the session id.
	verifier interface {
		StartQuery(ctx context.Context, q openid4vpverifier.Query) (openid4vpverifier.Session, error)
		Result(ctx context.Context, transactionID string) (openid4vpverifier.Presentation, error)
	}
	// ledger resolves a disclosed credential back to the organisation's own
	// issuance ledger. Implemented by attestation.Store.
	ledger interface {
		FindIssuedByClaims(ctx context.Context, orgID uuid.UUID, vct string, claims map[string]string) (attestation.Issued, error)
	}
)

// Service runs a verification: template -> presentation request at the hosted
// verifier -> session row -> poll -> grade against the ledger. The requester is
// the issuer in this scenario, so revocation is a ledger lookup rather than a
// status list.
type Service struct {
	templates  templateReader
	sessions   sessionStore
	verifier   verifier
	ledger     ledger
	appBaseURL string
	ttl        time.Duration
	now        func() time.Time
}

// NewService wires the flow. ttl bounds how long a started session waits for the
// holder; appBaseURL is this deployment's public origin, used for BrowserLink.
func NewService(templates templateReader, sessions sessionStore, verifier verifier, ledger ledger, appBaseURL string, ttl time.Duration) *Service {
	return &Service{
		templates:  templates,
		sessions:   sessions,
		verifier:   verifier,
		ledger:     ledger,
		appBaseURL: appBaseURL,
		ttl:        ttl,
		now:        time.Now,
	}
}

// View is a session as the browser sees it: the row with its effective status,
// plus, while pending, the two forms of the invocation to hand the holder.
type View struct {
	Session
	// WalletLink is the verifier's openid4vp:// deeplink, for a wallet with its
	// own scanner. BrowserLink is the same invocation as an https URL into this
	// deployment's /openid4vp entry, so a phone camera opens the holder's
	// business wallet in the browser instead of handing the custom scheme to a
	// personal wallet app. Both are empty once the session is no longer pending.
	WalletLink  string `json:"walletLink,omitempty"`
	BrowserLink string `json:"browserLink,omitempty"`
}

// Start creates a presentation request for the template at the verifier and
// records the session. startedBy is the member running the check.
func (s *Service) Start(ctx context.Context, orgID, templateID, startedBy uuid.UUID) (View, error) {
	tpl, err := s.templates.GetTemplate(ctx, orgID, templateID)
	if err != nil {
		return View{}, err
	}
	sess, err := s.verifier.StartQuery(ctx, openid4vpverifier.Query{VCT: tpl.VCT, Claims: tpl.Claims})
	if err != nil {
		return View{}, fmt.Errorf("%w: %w", ErrVerifierUnavailable, err)
	}
	row, err := s.sessions.CreateSession(ctx, NewSession{
		Template:      tpl,
		TransactionID: sess.TransactionID,
		WalletLink:    sess.WalletLink,
		StartedBy:     startedBy,
		ExpiresAt:     s.now().Add(s.ttl),
	})
	if err != nil {
		return View{}, err
	}
	return s.view(row), nil
}

// Get reads a session, polling the verifier while it is pending. The first read
// that finds the disclosure grades it and stores the result; the hosted verifier
// keeps a completed presentation, so a lost race between two polls is harmless.
func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (View, error) {
	row, err := s.sessions.GetSession(ctx, orgID, id)
	if err != nil {
		return View{}, err
	}
	if row.EffectiveStatus(s.now()) != StatusPending {
		return s.view(row), nil
	}

	res, err := s.verifier.Result(ctx, row.TransactionID)
	if errors.Is(err, openid4vpverifier.ErrPending) {
		return s.view(row), nil
	}
	if err != nil {
		return View{}, fmt.Errorf("verification: fetch result: %w", err)
	}
	result, err := s.grade(ctx, row, res)
	if err != nil {
		return View{}, err
	}
	row, err = s.sessions.CompleteSession(ctx, orgID, id, result)
	if err != nil {
		return View{}, err
	}
	return s.view(row), nil
}

// List is the organisation's check history, newest first.
func (s *Service) List(ctx context.Context, orgID uuid.UUID) ([]View, error) {
	rows, err := s.sessions.ListSessions(ctx, orgID)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(rows))
	for _, row := range rows {
		views = append(views, s.view(row))
	}
	return views, nil
}

func (s *Service) view(row Session) View {
	row.Status = row.EffectiveStatus(s.now())
	v := View{Session: row}
	if row.Status == StatusPending {
		v.WalletLink = row.WalletLink
		v.BrowserLink = browserLink(s.appBaseURL, row.WalletLink)
	}
	return v
}

// grade turns the disclosure into the checks a result card shows. The verifier
// already did the cryptography; what is graded here is whether the credential
// is one this organisation issued and still stands behind.
func (s *Service) grade(ctx context.Context, sess Session, p openid4vpverifier.Presentation) (Result, error) {
	claims := p.QueryClaims()
	if len(claims) == 0 {
		return Result{Claims: claims, Checks: []Check{{Name: CheckVerified, Passed: false}}}, nil
	}
	checks := []Check{{Name: CheckVerified, Passed: true}}

	entry, err := s.ledger.FindIssuedByClaims(ctx, sess.OrganizationID, sess.VCT, claims)
	issuedHere := err == nil
	if err != nil && !errors.Is(err, attestation.ErrIssuedNotFound) {
		return Result{}, fmt.Errorf("verification: ledger lookup: %w", err)
	}

	expiresAt := p.QueryExpiresAt()
	if expiresAt.IsZero() && issuedHere && entry.ExpiresAt != nil {
		expiresAt = *entry.ExpiresAt
	}
	checks = append(checks, notExpired(expiresAt, s.now()), Check{Name: CheckIssuedHere, Passed: issuedHere})

	switch {
	case !issuedHere:
		checks = append(checks, Check{Name: CheckNotRevoked, Passed: false, Detail: "no_ledger_entry"})
	case entry.Status != attestation.StatusClaimed:
		checks = append(checks, Check{Name: CheckNotRevoked, Passed: false, Detail: entry.Status})
	default:
		checks = append(checks, Check{Name: CheckNotRevoked, Passed: true})
	}

	valid := true
	for _, c := range checks {
		valid = valid && c.Passed
	}
	return Result{Claims: claims, Checks: checks, Valid: valid}, nil
}

// notExpired passes when the credential has no known expiry or one in the future.
func notExpired(expiresAt, now time.Time) Check {
	if expiresAt.IsZero() || now.Before(expiresAt) {
		return Check{Name: CheckNotExpired, Passed: true}
	}
	return Check{Name: CheckNotExpired, Passed: false, Detail: expiresAt.UTC().Format(time.RFC3339)}
}

// browserLink rewrites the openid4vp:// deeplink to the https address of this
// deployment's inbound entry; the query (client_id, request_uri) travels as is.
func browserLink(appBaseURL, walletLink string) string {
	query := ""
	if i := strings.IndexByte(walletLink, '?'); i >= 0 {
		query = walletLink[i+1:]
	}
	return strings.TrimRight(appBaseURL, "/") + inboundPath + "?" + query
}
