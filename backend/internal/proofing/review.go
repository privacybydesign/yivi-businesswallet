package proofing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// maxReviewReasonLength bounds a reviewer's reason, as IPS does.
const maxReviewReasonLength = 500

// reviewErrorCodePattern is a rejection code a reviewer may name: IPS's shape.
var reviewErrorCodePattern = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

// ErrNotUnderReview is a decision on a request that is not in needs_review, or
// whose session IPS already ended.
var ErrNotUnderReview = errors.New("proofing: the request is not under review")

// ReviewInput is an admin's decision on a request under review: approve, or
// reject with an optional ErrorCode. Reason is required and audited.
type ReviewInput struct {
	Approve   bool
	ErrorCode string
	Reason    string
}

func (s *Service) DecideReview(ctx context.Context, orgID, id uuid.UUID, reviewer string, in ReviewInput) (Request, error) {
	in.Reason, in.ErrorCode = strings.TrimSpace(in.Reason), strings.TrimSpace(in.ErrorCode)
	switch {
	case in.Reason == "" || len(in.Reason) > maxReviewReasonLength:
		return Request{}, fmt.Errorf("%w: give a reason of at most %d characters", ErrInvalidInput, maxReviewReasonLength)
	case in.Approve && in.ErrorCode != "":
		return Request{}, fmt.Errorf("%w: an error code is only for a rejection", ErrInvalidInput)
	case in.ErrorCode != "" && !reviewErrorCodePattern.MatchString(in.ErrorCode):
		return Request{}, fmt.Errorf("%w: an error code is 1-64 of A-Z, 0-9 and _", ErrInvalidInput)
	}
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if req.Status != StatusNeedsReview || req.session == nil || req.session.EndedAt != nil {
		return Request{}, ErrNotUnderReview
	}
	tenant := requestTenant(req)
	decision := proofingprovider.ReviewDecision{
		Approve: in.Approve, ErrorCode: in.ErrorCode, Reason: in.Reason, Reviewer: reviewer,
	}
	if err := s.ips.DecideReview(ctx, tenant, req.session.ID, req.session.Token, decision); err != nil {
		var rejected *proofingprovider.RejectedError
		if errors.As(err, &rejected) {
			// IPS no longer holds it under review: another decision won, or it ended.
			return Request{}, ErrNotUnderReview
		}
		return Request{}, fmt.Errorf("proofing: decide review request %s: %w", req.ID, err)
	}
	decided := StatusRejected
	if in.Approve {
		decided = StatusApproved
	}
	if err := s.requests.RecordReviewDecision(ctx, req, decided, in.Reason, in.ErrorCode); err != nil {
		return Request{}, err
	}
	// A failed read is logged; IPS's push or the next read records the outcome.
	req, _ = s.tryReconcile(ctx, tenant, req)
	return req, nil
}
