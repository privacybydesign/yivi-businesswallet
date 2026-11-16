package proofing

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// reviewLinkTTL is how long a review link opens its review: long enough to
// look at a session and decide, short enough that a link sent on is soon dead.
const reviewLinkTTL = time.Hour

// ReviewLinkActorPrefix starts the audit actor label of a review link, which
// its API key's prefix follows.
const ReviewLinkActorPrefix = "review_link:"

// ErrReviewLinkNotFound is a review link that is unknown, expired, or whose
// API key was revoked.
var ErrReviewLinkNotFound = errors.New("proofing: review link not found")

// ErrNothingToReview is a review link asked for a session that waits for no
// decision.
var ErrNothingToReview = errors.New("proofing: nothing of the session waits for a review")

// ReviewLink is a live link to the hosted review page of one request.
type ReviewLink struct {
	OrganizationID uuid.UUID
	RequestID      uuid.UUID
	Reviewer       string
	KeyPrefix      string
	ExpiresAt      time.Time
}

type reviewLinkStore interface {
	Create(ctx context.Context, req Request, keyID uuid.UUID, reviewer string, tokenHash []byte, expiresAt time.Time) error
	Get(ctx context.Context, tokenHash []byte, now time.Time) (ReviewLink, error)
}

// ReviewLinkStore is the reviewLinkStore on identity_proofing_review_links.
type ReviewLinkStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewReviewLinkStore(db database.DB, recorder audit.Recorder) *ReviewLinkStore {
	return &ReviewLinkStore{db: db, audit: recorder}
}

// Create stores a link to req's review for reviewer, audited
// identity_proofing.review_link_created on the request.
func (s *ReviewLinkStore) Create(ctx context.Context, req Request, keyID uuid.UUID, reviewer string, tokenHash []byte, expiresAt time.Time) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_review_links
				(organization_id, request_id, api_key_id, token_hash, reviewer, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			req.OrganizationID, req.ID, keyID, tokenHash, reviewer, expiresAt); err != nil {
			return fmt.Errorf("proofing: create review link request %s: %w", req.ID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingReviewLinkCreated,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{auditReviewerKey: reviewer, "expiresAt": expiresAt})))
	})
}

// Get is the live link whose token hashes to tokenHash: not expired, its key
// not revoked. Any other is ErrReviewLinkNotFound.
func (s *ReviewLinkStore) Get(ctx context.Context, tokenHash []byte, now time.Time) (ReviewLink, error) {
	var l ReviewLink
	err := s.db.QueryRow(ctx, `SELECT l.organization_id, l.request_id, l.reviewer, k.prefix, l.expires_at
		FROM identity_proofing_review_links l
		JOIN identity_proofing_api_keys k ON k.id = l.api_key_id
		WHERE l.token_hash = $1 AND l.expires_at > $2 AND k.revoked_at IS NULL`, tokenHash, now).
		Scan(&l.OrganizationID, &l.RequestID, &l.Reviewer, &l.KeyPrefix, &l.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReviewLink{}, ErrReviewLinkNotFound
	}
	if err != nil {
		return ReviewLink{}, fmt.Errorf("proofing: read review link: %w", err)
	}
	return l, nil
}

func (s *Service) SetReviewBaseURL(u string) { s.reviewBaseURL = u }

// CreateReviewLink makes a link to the hosted review page of one of the org's
// external identity checks, for reviewer, as caller's API key. Only a session
// with something waiting gets one.
func (s *Service) CreateReviewLink(ctx context.Context, caller APIKeyCaller, id uuid.UUID, reviewer string) (string, time.Time, error) {
	if s.reviewLinks == nil || s.reviewBaseURL == "" {
		return "", time.Time{}, errors.New("proofing: no review links configured")
	}
	name, err := apiReviewer(reviewer)
	if err != nil {
		return "", time.Time{}, err
	}
	req, err := s.ExternalRequest(ctx, caller.Org.ID, id)
	if err != nil {
		return "", time.Time{}, err
	}
	if req.FlowKind.dataRequest() {
		return "", time.Time{}, fmt.Errorf("%w: a review link is for an identity check; decide a data request with POST .../review-items/data_request", ErrInvalidInput)
	}
	if !hasWaitingPart(req) {
		return "", time.Time{}, ErrNothingToReview
	}
	raw, hash, err := newLinkToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().Add(reviewLinkTTL)
	if err := s.reviewLinks.Create(ctx, req, caller.KeyID, name, hash, expires); err != nil {
		return "", time.Time{}, err
	}
	return s.reviewBaseURL + raw, expires, nil
}

// hasWaitingPart reports whether something of req waits for a reviewer.
func hasWaitingPart(req Request) bool {
	for _, p := range reviewParts(req) {
		if p.State == ReviewPending {
			return true
		}
	}
	return false
}

// reviewLinkAccess is a live review link with the context its calls act in:
// audited as the link, for the reviewer it names.
func (s *Service) reviewLinkAccess(ctx context.Context, token string) (context.Context, ReviewLink, error) {
	if s.reviewLinks == nil {
		return ctx, ReviewLink{}, ErrReviewLinkNotFound
	}
	hash := sha256.Sum256([]byte(token))
	link, err := s.reviewLinks.Get(ctx, hash[:], s.now())
	if err != nil {
		return ctx, ReviewLink{}, err
	}
	return audit.ContextWithActor(ctx, audit.Actor{Label: ReviewLinkActorPrefix + link.KeyPrefix}), link, nil
}

// ReviewLinkReview is the ReviewView a review link opens, with its org's
// settings for the page.
func (s *Service) ReviewLinkReview(ctx context.Context, token string) (ReviewView, ReviewLink, Settings, error) {
	ctx, link, err := s.reviewLinkAccess(ctx, token)
	if err != nil {
		return ReviewView{}, ReviewLink{}, Settings{}, err
	}
	req, err := s.ExternalRequest(ctx, link.OrganizationID, link.RequestID)
	if err != nil {
		return ReviewView{}, ReviewLink{}, Settings{}, err
	}
	view, err := s.reviewView(ctx, req)
	if err != nil {
		return ReviewView{}, ReviewLink{}, Settings{}, err
	}
	settings, err := s.orgSettings.Get(ctx, link.OrganizationID)
	return view, link, settings, err
}

// ReviewLinkDecide decides kind of the session a review link opens, as its
// reviewer.
func (s *Service) ReviewLinkDecide(ctx context.Context, token string, kind ReviewKind, in ReviewItemInput) error {
	ctx, link, err := s.reviewLinkAccess(ctx, token)
	if err != nil {
		return err
	}
	_, err = s.DecideExternalReview(ctx, link.OrganizationID, link.RequestID, kind, link.Reviewer, in)
	return err
}

// ReviewLinkCheckSeals checks again the seals of kind of the session a review
// link opens.
func (s *Service) ReviewLinkCheckSeals(ctx context.Context, token string, kind ReviewKind) error {
	ctx, link, err := s.reviewLinkAccess(ctx, token)
	if err != nil {
		return err
	}
	_, err = s.CheckExternalSealsAgain(ctx, link.OrganizationID, link.RequestID, kind)
	return err
}

// ReviewLinkEmbedOrigins are the origins that may frame a review link's page:
// its org's allowed redirect origins, as for a hosted link.
func (s *Service) ReviewLinkEmbedOrigins(ctx context.Context, token string) ([]string, error) {
	_, link, err := s.reviewLinkAccess(ctx, token)
	if err != nil {
		return nil, err
	}
	settings, err := s.orgSettings.Get(ctx, link.OrganizationID)
	if err != nil {
		return nil, err
	}
	return settings.RedirectOrigins, nil
}
