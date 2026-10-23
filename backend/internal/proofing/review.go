package proofing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// maxReviewReasonLength bounds a reviewer's reason, as the engine does.
const maxReviewReasonLength = 500

// reviewErrorCodePattern is a rejection code a reviewer may name: the engine's shape.
var reviewErrorCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

// ErrNotUnderReview is a decision on a request that is not in needs_review, or
// whose session the engine already ended.
var ErrNotUnderReview = errors.New("proofing: the request is not under review")

// ReviewInput is an admin's decision on a request under review: approve, or
// reject with an optional ErrorCode. Reason is required and audited.
type ReviewInput struct {
	Approve   bool
	ErrorCode string
	Reason    string
	// RequestIDs are the matched sessions approving a data request takes; nil
	// takes those approvedMatches takes without a tick. Only for a data request.
	RequestIDs []uuid.UUID
}

// validate checks a trimmed in: a reason within maxReviewReasonLength, and an
// error code only on a rejection, of reviewErrorCodePattern.
func (in ReviewInput) validate() error {
	switch {
	case in.Reason == "" || utf8.RuneCountInString(in.Reason) > maxReviewReasonLength:
		return fmt.Errorf("%w: give a reason of at most %d characters", ErrInvalidInput, maxReviewReasonLength)
	case in.Approve && in.ErrorCode != "":
		return fmt.Errorf("%w: an error code is only for a rejection", ErrInvalidInput)
	case in.ErrorCode != "" && !reviewErrorCodePattern.MatchString(in.ErrorCode):
		return fmt.Errorf("%w: an error code is 1-64 of A-Z, 0-9 and _", ErrInvalidInput)
	}
	return nil
}

func (s *Service) DecideReview(ctx context.Context, orgID, id uuid.UUID, reviewer string, in ReviewInput) (Request, error) {
	in.Reason, in.ErrorCode = strings.TrimSpace(in.Reason), strings.TrimSpace(in.ErrorCode)
	if err := in.validate(); err != nil {
		return Request{}, err
	}
	// Two reviewers deciding at once: the second waits, then reads it decided.
	release, err := s.requests.LockReview(ctx, id)
	if err != nil {
		return Request{}, err
	}
	defer release()
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if req.Status != StatusNeedsReview || req.session == nil || req.PurgedAt != nil {
		return Request{}, ErrNotUnderReview
	}
	switch {
	case req.ReviewReason != "":
		return s.decideWalletReview(ctx, req, in)
	case req.session.EndedAt != nil:
		return Request{}, ErrNotUnderReview
	case req.FlowKind.dataRequest() && s.dataRequests != nil:
		return s.decideDataRequest(ctx, req, reviewer, in)
	case in.RequestIDs != nil:
		return Request{}, fmt.Errorf("%w: sessions are chosen only on a data request", ErrInvalidInput)
	}
	return s.decideEngineReview(ctx, req, reviewer, in)
}

// decideEngineReview sends in to the engine, which holds req's review, and
// records who decided and why.
func (s *Service) decideEngineReview(ctx context.Context, req Request, reviewer string, in ReviewInput) (Request, error) {
	tenant := requestTenant(req)
	decision := proofingprovider.ReviewDecision{
		Approve: in.Approve, ErrorCode: in.ErrorCode, Reason: in.Reason, Reviewer: reviewer,
	}
	if err := s.ips.DecideReview(ctx, tenant, req.session.ID, req.session.Token, decision); err != nil {
		var rejected *proofingprovider.RejectedError
		if errors.As(err, &rejected) {
			// The engine no longer holds it under review: another decision won,
			// or it ended. Read what it holds, so the request does not wait in
			// review for a notice that may have been missed.
			_, _ = s.tryReconcile(ctx, tenant, req)
			return Request{}, ErrNotUnderReview
		}
		return Request{}, fmt.Errorf("proofing: decide review request %s: %w", req.ID, err)
	}
	decided := StatusRejected
	if in.Approve {
		decided = StatusApproved
	}
	// The engine has decided: who decided and why is recorded even if the
	// caller has gone, or the outcome would land as the system's.
	ctx = context.WithoutCancel(ctx)
	if err := s.requests.RecordReviewDecision(ctx, req, decided, in.Reason, in.ErrorCode); err != nil {
		return Request{}, err
	}
	// A failed read is logged; the engine's notice or the next read records the outcome.
	req, _ = s.tryReconcile(ctx, tenant, req)
	return req, nil
}

// decideWalletReview decides a review the wallet opened (Request.ReviewReason)
// in the wallet alone: the engine approved the session before, and holds no
// review to decide. A rejection without a code is ErrorReviewRejected.
func (s *Service) decideWalletReview(ctx context.Context, req Request, in ReviewInput) (Request, error) {
	if in.RequestIDs != nil {
		return Request{}, fmt.Errorf("%w: sessions are chosen only on a data request", ErrInvalidInput)
	}
	decided, code := StatusApproved, ""
	if !in.Approve {
		decided, code = StatusRejected, in.ErrorCode
		if code == "" {
			code = ErrorReviewRejected
		}
	}
	// Who decided and why is recorded even if the caller has gone.
	ctx = context.WithoutCancel(ctx)
	if err := s.requests.RecordReviewDecision(ctx, req, decided, in.Reason, code); err != nil {
		return Request{}, err
	}
	if err := s.requests.RecordWalletOutcome(ctx, req, req.session.ID, decided, proofingprovider.Result{
		Method: req.Method, AssuranceLevel: req.AssuranceLevel, EIDASLevel: req.EIDASLevel, ErrorCode: code,
		Name: req.ProofedName,
	}); err != nil {
		return Request{}, err
	}
	return s.requests.Get(ctx, req.OrganizationID, req.ID)
}
