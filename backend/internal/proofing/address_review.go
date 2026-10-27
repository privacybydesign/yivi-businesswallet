package proofing

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// AddressReview is where a self-declared address stands with a reviewer. The
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

// selfDeclared reports an address nothing confirmed as where the person lives:
// anything but the registered (BRP) address, matched or not.
func selfDeclared(v AddressVerification) bool {
	return v != AddressRegistered && v != AddressRegisteredMismatch
}

// addressReviewOf is the review a newly entered address starts with: pending
// when it is self-declared, none otherwise.
func addressReviewOf(addr Address) *AddressReview {
	if !selfDeclared(addr.Verification) {
		return nil
	}
	pending := AddressReviewPending
	return &pending
}

// DecideAddressReview confirms or rejects the self-declared address of a
// request the reviewer's scope reaches. The session keeps its outcome. A
// reviewer never decides a request they sent or are the subject of, unless
// they are a platform admin (platformAdmin).
func (s *Service) DecideAddressReview(ctx context.Context, orgID, id, reviewerID uuid.UUID, scope ReviewScope, confirm, platformAdmin bool) (Request, error) {
	release, err := s.requests.LockReview(ctx, id)
	if err != nil {
		return Request{}, err
	}
	defer release()
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if !scope.reachesAddress(req) {
		return Request{}, ErrRequestNotFound
	}
	if req.AddressReview != AddressReviewPending || req.PurgedAt != nil {
		return Request{}, ErrAddressNotUnderReview
	}
	if !platformAdmin && ownReview(req, reviewerID) {
		return Request{}, ErrOwnReview
	}
	decided := AddressReviewRejected
	if confirm {
		decided = AddressReviewConfirmed
	}
	ok, err := s.requests.DecideAddressReview(ctx, req, reviewerID, decided)
	if err != nil {
		return Request{}, err
	}
	if !ok {
		return Request{}, ErrAddressNotUnderReview
	}
	req.AddressReview = decided
	return req, nil
}

// DecideAddressReview records a reviewer's decision on req's pending address,
// audited identity_proofing.address_reviewed and sent as
// session.address_reviewed. It is false, and changes nothing, when the
// address no longer waits for one.
func (s *RequestStore) DecideAddressReview(ctx context.Context, req Request, reviewerID uuid.UUID, decided AddressReview) (bool, error) {
	done := false
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
			SET address_review = $2, address_reviewed_by = $3, address_reviewed_at = now(), updated_at = now()
			WHERE id = $1 AND address_review = $4 AND purged_at IS NULL`,
			req.ID, string(decided), reviewerID, string(AddressReviewPending))
		if err != nil {
			return fmt.Errorf("proofing: decide address review request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		done = true
		if err := s.audit.Record(ctx, q, audit.IdentityProofingAddressReviewed,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Updated(req.auditFields(map[string]any{"addressReview": string(AddressReviewPending)}),
				req.auditFields(map[string]any{"addressReview": string(decided)}))); err != nil {
			return err
		}
		data := sessionEventData(req, req.Status)
		data[webhookAddressReviewKey] = string(decided)
		return enqueueRequestWebhook(ctx, q, req, EventSessionAddressReviewed, data)
	})
	if err != nil {
		return false, err
	}
	return done, nil
}
