package proofing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// AddressReview is where an address the registered (BRP) address did not
// confirm stands with a reviewer: its 'address' review item. The
// session's own outcome never follows it: only the address is reviewed.
type AddressReview = ReviewState

const (
	// AddressReviewPending waits for a reviewer: no register confirmed where
	// the person lives.
	AddressReviewPending = ReviewPending
	// AddressReviewConfirmed is an address a reviewer confirmed.
	AddressReviewConfirmed = ReviewConfirmed
	// AddressReviewRejected is an address a reviewer rejected.
	AddressReviewRejected = ReviewRejected
	// AddressReviewLapsed is an address nobody decided in time.
	AddressReviewLapsed = ReviewLapsed
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

// StepReviewInput is a reviewer's decision on a step's review item: confirm
// or reject it, for Reason, which is required and audited.
type StepReviewInput struct {
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

// DecideAddressReview confirms or rejects the address of a request that waits
// for a review (decideStepReview).
func (s *Service) DecideAddressReview(ctx context.Context, orgID, id uuid.UUID, in StepReviewInput) (Request, error) {
	return s.decideStepReview(ctx, orgID, id, ReviewKindAddress, in)
}

// decideStepReview confirms or rejects the pending review item of kind, a
// step's, on a request the reviewer's scope reaches. Only the step is
// decided: the session keeps its outcome. A reviewer never decides a request
// they sent or are the subject of, unless in.OwnAllowed. An item that waits
// for none is ErrAddressNotUnderReview for the address, else
// ErrNotUnderReview.
func (s *Service) decideStepReview(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind, in StepReviewInput) (Request, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxReviewReasonLength {
		return Request{}, fmt.Errorf("%w: give a reason of at most %d characters", ErrInvalidInput, maxReviewReasonLength)
	}
	notWaiting := ErrNotUnderReview
	if kind == ReviewKindAddress {
		notWaiting = ErrAddressNotUnderReview
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
	if !in.Scope.reachesStepReview(req) {
		return Request{}, ErrRequestNotFound
	}
	if req.stepReview(kind) != ReviewPending {
		return Request{}, notWaiting
	}
	if ownReview(req, in.ReviewerID) && !in.OwnAllowed {
		return Request{}, ErrOwnReview
	}
	decided := ReviewRejected
	if in.Confirm {
		decided = ReviewConfirmed
	}
	reviewedAt, err := s.requests.DecideStepReview(ctx, req, kind, in.ReviewerID, decided, reason)
	if err != nil {
		return Request{}, err
	}
	if reviewedAt == nil {
		return Request{}, notWaiting
	}
	req.setStepReview(kind, decided, reviewedAt)
	return req, nil
}

// DecideStepReview records a reviewer's decision on req's pending item of
// kind (decideReviewItem), sent as session.address_reviewed for the address
// and session.step_reviewed for another step. It returns when it was decided:
// nil, and changes nothing, when the item no longer waits.
func (s *RequestStore) DecideStepReview(ctx context.Context, req Request, kind ReviewKind, reviewerID uuid.UUID, decided ReviewState, reason string) (*time.Time, error) {
	var reviewedAt *time.Time
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		at, err := s.decideReviewItem(ctx, q, req, kind, reviewerID, decided, reason)
		if err != nil || at == nil {
			return err
		}
		reviewedAt = at
		decidedReq := req
		decidedReq.setStepReview(kind, decided, at)
		data := sessionEventData(decidedReq, req.Status)
		if kind == ReviewKindAddress {
			data[webhookAddressReviewKey] = string(decided)
			return enqueueRequestWebhook(ctx, q, req, EventSessionAddressReviewed, data)
		}
		data[webhookStepKey] = map[string]string{"kind": string(kind), "state": string(decided)}
		return enqueueRequestWebhook(ctx, q, req, EventSessionStepReviewed, data)
	})
	if err != nil {
		return nil, err
	}
	return reviewedAt, nil
}

// stepReview is where r's review item of kind stands; empty for none.
func (r Request) stepReview(kind ReviewKind) ReviewState {
	switch kind {
	case ReviewKindAddress:
		return r.AddressReview
	case ReviewKindEmployment:
		return r.EmploymentReview
	}
	return ""
}

func (r *Request) setStepReview(kind ReviewKind, state ReviewState, at *time.Time) {
	switch kind {
	case ReviewKindAddress:
		r.AddressReview, r.AddressReviewedAt = state, at
	case ReviewKindEmployment:
		r.EmploymentReview, r.EmploymentReviewedAt = state, at
	}
}
