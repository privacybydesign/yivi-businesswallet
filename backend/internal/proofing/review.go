package proofing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
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

// ErrOwnReview is a reviewer deciding an identity check they sent or are the
// subject of: another reviewer decides it.
var ErrOwnReview = errors.New("proofing: a reviewer does not decide a check they sent or are the subject of")

// The codes a reviewer's rejection names, for what they found wrong; none is
// ErrorReviewRejected. Each is the reviewer's own and no code the wallet or the
// engine sets for another reason: a data request still finds the session
// (DataRequestStore.Candidates leaves out only errorIdentityMismatch), and the
// person is told what the reviewer found, not a failed check.
const (
	// errorReviewNotHolder: what was sent is someone else's.
	errorReviewNotHolder = "REVIEW_NOT_HOLDER"
	// errorReviewAltered: what was sent looks altered.
	errorReviewAltered = "REVIEW_ALTERED"
	// errorEvidenceNotAsRequested: what was sent is not what was asked for.
	errorEvidenceNotAsRequested = "EVIDENCE_NOT_AS_REQUESTED"
)

// reviewerRejectCodes are the codes a reviewer may name; ErrorReviewRejected is
// "something else", and also what a rejection naming none records.
var reviewerRejectCodes = []string{errorReviewNotHolder, errorReviewAltered, errorEvidenceNotAsRequested, ErrorReviewRejected}

// ReviewScope is which of the org's requests a caller reaches on the review
// routes: the result read, data-matches, data-export and the decision.
type ReviewScope int

const (
	// ReviewScopeAll is an admin's: every request of the org.
	ReviewScopeAll ReviewScope = iota
	// ReviewScopeReviews is a member's with the reviewer right: only what the
	// Reviews page shows, a request waiting for review and a data request in
	// any status. An identity check that is not waiting holds a person's
	// identity and photos the right was never given for.
	ReviewScopeReviews
)

// reaches reports whether the scope reaches req. One it does not is
// ErrRequestNotFound, so the caller learns nothing of it.
func (sc ReviewScope) reaches(req Request) bool {
	return sc == ReviewScopeAll || req.Status == StatusNeedsReview || req.FlowKind.dataRequest()
}

// reachesAddress reports whether the scope reaches req's request and address:
// what reaches does, and a self-declared address waiting for a reviewer. Never
// the result of a check it holds.
func (sc ReviewScope) reachesAddress(req Request) bool {
	return sc.reaches(req) || (req.AddressReview == AddressReviewPending && req.PurgedAt == nil)
}

// ReviewInput is a decision on a request under review: approve, or reject with
// an optional ErrorCode. Reason is required and audited.
type ReviewInput struct {
	Approve bool
	// ErrorCode names why a rejection is one: for a reviewer one of
	// reviewerRejectCodes; the wallet deciding on its own names its own
	// (ErrorOrgPaused).
	ErrorCode string
	Reason    string
	// RequestIDs are the matched sessions approving a data request takes; nil
	// takes those approvedMatches takes without a tick. Only for a data request.
	RequestIDs []uuid.UUID
	// ReviewerID is the member deciding; uuid.Nil when the wallet decides on
	// its own (a paused org's open reviews).
	ReviewerID uuid.UUID
	// Scope is the requests the deciding member reaches.
	Scope ReviewScope
}

// ownReview reports whether reviewer is the one who sent req or its subject:
// for an identity check they would vouch for their own request or their own
// identity. A data request is the person's own ask, which whoever sent its
// link has no stake in, so its sender may decide it: in an org with one admin
// nobody else could, and it would lapse unanswered.
func ownReview(req Request, reviewer uuid.UUID) bool {
	if reviewer == uuid.Nil || req.FlowKind.dataRequest() {
		return false
	}
	return (req.RequestedBy != nil && *req.RequestedBy == reviewer) ||
		(req.SubjectUserID != nil && *req.SubjectUserID == reviewer)
}

func (s *Service) DecideReview(ctx context.Context, orgID, id uuid.UUID, reviewer string, in ReviewInput) (Request, error) {
	in.Reason, in.ErrorCode = strings.TrimSpace(in.Reason), strings.TrimSpace(in.ErrorCode)
	switch {
	case in.Reason == "" || utf8.RuneCountInString(in.Reason) > maxReviewReasonLength:
		return Request{}, fmt.Errorf("%w: give a reason of at most %d characters", ErrInvalidInput, maxReviewReasonLength)
	case in.Approve && in.ErrorCode != "":
		return Request{}, fmt.Errorf("%w: an error code is only for a rejection", ErrInvalidInput)
	case in.ErrorCode != "" && !reviewErrorCodePattern.MatchString(in.ErrorCode):
		return Request{}, fmt.Errorf("%w: an error code is 1-64 of A-Z, 0-9 and _", ErrInvalidInput)
	case in.ReviewerID != uuid.Nil && in.ErrorCode != "" && !slices.Contains(reviewerRejectCodes, in.ErrorCode):
		return Request{}, fmt.Errorf("%w: a reviewer's error code is one of %s", ErrInvalidInput, strings.Join(reviewerRejectCodes, ", "))
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
	if !in.Scope.reaches(req) {
		return Request{}, ErrRequestNotFound
	}
	if req.Status != StatusNeedsReview || req.session == nil || req.PurgedAt != nil {
		return Request{}, ErrNotUnderReview
	}
	if ownReview(req, in.ReviewerID) {
		return Request{}, ErrOwnReview
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
