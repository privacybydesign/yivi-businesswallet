package proofing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// AddressReview is where an address the registered (BRP) address did not
// confirm stands with a reviewer. The
// session's own outcome never follows it: only the address is reviewed.
type AddressReview string

const (
	// AddressReviewPending waits for a reviewer: no register confirmed where
	// the person lives.
	AddressReviewPending AddressReview = "pending"
	// AddressReviewConfirmed is an address a reviewer confirmed.
	AddressReviewConfirmed AddressReview = "confirmed"
	// AddressReviewRejected is an address a reviewer rejected.
	AddressReviewRejected AddressReview = "rejected"
)

// ErrAddressNotUnderReview is an address decision on a request whose address
// waits for none: none entered, confirmed by the BRP, decided, or erased.
var ErrAddressNotUnderReview = errors.New("proofing: the address does not wait for a review")

// addressReviewOf is the review a newly entered address starts with: pending
// unless it is the registered (BRP) address. One that differs from it is
// reviewed too: the person may have moved since.
func addressReviewOf(addr Address) *AddressReview {
	if addr.Verification == AddressRegistered {
		return nil
	}
	pending := AddressReviewPending
	return &pending
}

// AddressReviewInput is a reviewer's decision on an address waiting for
// review: confirm or reject it, for Reason, which is required and audited.
type AddressReviewInput struct {
	Confirm bool
	Reason  string
	// ReviewerID is the member deciding.
	ReviewerID uuid.UUID
	// Scope is the requests the deciding member reaches.
	Scope ReviewScope
	// OwnAllowed lets the reviewer decide a check they sent or are the subject
	// of (auth.SelfReview: a platform admin on a dev stack only).
	OwnAllowed bool
}

// DecideAddressReview confirms or rejects the address of a request the
// reviewer's scope reaches that waits for a review. The session keeps its
// outcome. A reviewer never decides a request they sent or are the subject
// of, unless in.OwnAllowed.
func (s *Service) DecideAddressReview(ctx context.Context, orgID, id uuid.UUID, in AddressReviewInput) (Request, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxReviewReasonLength {
		return Request{}, fmt.Errorf("%w: give a reason of at most %d characters", ErrInvalidInput, maxReviewReasonLength)
	}
	release, err := s.requests.LockReview(ctx, id)
	if err != nil {
		return Request{}, err
	}
	defer release()
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if !in.Scope.reachesAddress(req) {
		return Request{}, ErrRequestNotFound
	}
	if req.AddressReview != AddressReviewPending {
		return Request{}, ErrAddressNotUnderReview
	}
	if ownReview(req, in.ReviewerID) && !in.OwnAllowed {
		return Request{}, ErrOwnReview
	}
	decided := AddressReviewRejected
	if in.Confirm {
		decided = AddressReviewConfirmed
	}
	reviewedAt, err := s.requests.DecideAddressReview(ctx, req, in.ReviewerID, decided, reason)
	if err != nil {
		return Request{}, err
	}
	if reviewedAt == nil {
		return Request{}, ErrAddressNotUnderReview
	}
	req.AddressReview, req.AddressReviewedAt = decided, reviewedAt
	return req, nil
}

// DecideAddressReview records a reviewer's decision on req's pending address,
// audited identity_proofing.address_reviewed with reason and sent as
// session.address_reviewed. It returns when it was decided: nil, and changes
// nothing, when the address no longer waits for one.
func (s *RequestStore) DecideAddressReview(ctx context.Context, req Request, reviewerID uuid.UUID, decided AddressReview, reason string) (*time.Time, error) {
	var reviewedAt *time.Time
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var at time.Time
		err := q.QueryRow(ctx, `UPDATE identity_proofing_requests
			SET address_review = $2, address_reviewed_by = $3, address_reviewed_at = now(), updated_at = now()
			WHERE id = $1 AND address_review = $4 AND purged_at IS NULL
			RETURNING address_reviewed_at`,
			req.ID, string(decided), reviewerID, string(AddressReviewPending)).Scan(&at)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("proofing: decide address review request %s: %w", req.ID, err)
		}
		reviewedAt = &at
		if err := s.audit.Record(ctx, q, audit.IdentityProofingAddressReviewed,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(req.auditFields(map[string]any{"addressReview": string(AddressReviewPending)}),
				req.auditFields(map[string]any{"addressReview": string(decided), "reason": reason}))); err != nil {
			return err
		}
		data := sessionEventData(req, req.Status)
		data[webhookAddressReviewKey] = string(decided)
		return enqueueRequestWebhook(ctx, q, req, EventSessionAddressReviewed, data)
	})
	if err != nil {
		return nil, err
	}
	return reviewedAt, nil
}
