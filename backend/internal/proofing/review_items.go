package proofing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ReviewKind is the part of a session a reviewer decides on: the identity as
// a whole, a data request, or an extra step.
type ReviewKind string

const (
	ReviewKindIdentity    ReviewKind = "identity"
	ReviewKindAddress     ReviewKind = "address"
	ReviewKindEmployment  ReviewKind = "employment"
	ReviewKindDataRequest ReviewKind = "data_request"
)

// ReviewKinds are every kind, in the order a count lists them.
var ReviewKinds = []ReviewKind{ReviewKindIdentity, ReviewKindAddress, ReviewKindEmployment, ReviewKindDataRequest}

func (k ReviewKind) known() bool {
	switch k {
	case ReviewKindIdentity, ReviewKindAddress, ReviewKindEmployment, ReviewKindDataRequest:
		return true
	}
	return false
}

// ReviewState is where a review item stands.
type ReviewState string

const (
	ReviewPending   ReviewState = "pending"
	ReviewConfirmed ReviewState = "confirmed"
	ReviewRejected  ReviewState = "rejected"
	// ReviewLapsed is an item nobody decided before it was due.
	ReviewLapsed ReviewState = "lapsed"
)

func (s ReviewState) known() bool {
	switch s {
	case ReviewPending, ReviewConfirmed, ReviewRejected, ReviewLapsed:
		return true
	}
	return false
}

// reviewKind is the kind of req's own review (needs_review): the
// Verzekeringsbericht's when the wallet opened it, a data request's, else the
// identity's.
func (r Request) reviewKind() ReviewKind {
	switch {
	case r.ReviewReason != "":
		return ReviewKindEmployment
	case r.FlowKind.dataRequest():
		return ReviewKindDataRequest
	}
	return ReviewKindIdentity
}

// sessionReviewKindColumn is Request.reviewKind in SQL.
const sessionReviewKindColumn = `CASE WHEN r.review_reason IS NOT NULL THEN 'employment'
		WHEN r.flow_kind IN ('data_access', 'data_erasure') THEN 'data_request' ELSE 'identity' END`

// sessionReviewWaits is a request r whose own review waits, as the Reviews
// page lists it: it reads needs_review and is not erased. Those review items
// are the request's status, not rows of identity_proofing_review_items.
const sessionReviewWaits = `r.purged_at IS NULL AND ` + effectiveStatusColumn + ` = 'needs_review'`

// reviewItemMatches is a request r with a review item of kind $k in state $s,
// stored or its own review: the filter of RequestFilter.ReviewKind.
func reviewItemMatches(k, s string) string {
	return `(EXISTS (SELECT 1 FROM identity_proofing_review_items i
			WHERE i.request_id = r.id AND i.kind = ` + k + ` AND i.state = ` + s + `)
		OR (` + s + ` = 'pending' AND ` + sessionReviewWaits + ` AND ` + sessionReviewKindColumn + ` = ` + k + `))`
}

// reviewItemOrder orders a list narrowed to review items of kind $k: waiting
// ones the longest waiting first, decided ones the latest decision first.
func reviewItemOrder(state ReviewState, k string) string {
	if state == ReviewPending {
		return `COALESCE((SELECT i.created_at FROM identity_proofing_review_items i
				WHERE i.request_id = r.id AND i.kind = ` + k + ` AND i.state = 'pending'),
			r.completed_at, r.created_at), r.created_at`
	}
	return `(SELECT max(i.decided_at) FROM identity_proofing_review_items i
			WHERE i.request_id = r.id AND i.kind = ` + k + `) DESC NULLS LAST, r.created_at DESC`
}

// addressReviewColumns are the state of r's address item and when it was
// decided, for Request.AddressReview and AddressReviewedAt.
const addressReviewColumns = `COALESCE((SELECT i.state FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind = 'address' ORDER BY i.created_at DESC LIMIT 1), ''),
	(SELECT i.decided_at FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind = 'address' ORDER BY i.created_at DESC LIMIT 1)`

// openReviewItem opens a pending item of kind on req, due its retention
// (Request.NameRetention) from now: by then nothing of the session would be
// kept to decide on. One already pending is kept.
func openReviewItem(ctx context.Context, q database.Querier, req Request, kind ReviewKind) error {
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_review_items
			(request_id, organization_id, kind, state, due_at)
		VALUES ($1, $2, $3, 'pending', now() + make_interval(secs => $4))
		ON CONFLICT (request_id, kind) WHERE state = 'pending' DO NOTHING`,
		req.ID, req.OrganizationID, string(kind), req.NameRetention.Seconds()); err != nil {
		return fmt.Errorf("proofing: open %s review request %s: %w", kind, req.ID, err)
	}
	return nil
}

// decideReviewItem records a reviewer's decision on req's pending item of
// kind, audited identity_proofing.review_item_decided with reason. It returns
// when it was decided: nil, and changes nothing, when none waits.
func (s *RequestStore) decideReviewItem(ctx context.Context, q database.Querier, req Request, kind ReviewKind,
	reviewerID uuid.UUID, decided ReviewState, reason string,
) (*time.Time, error) {
	var at time.Time
	err := q.QueryRow(ctx, `UPDATE identity_proofing_review_items i
		SET state = $3, decided_by = $4, decided_at = now(), reason = $5
		FROM identity_proofing_requests r
		WHERE r.id = i.request_id AND i.request_id = $1 AND i.kind = $2 AND i.state = 'pending' AND r.purged_at IS NULL
		RETURNING i.decided_at`,
		req.ID, string(kind), string(decided), reviewerID, reason).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("proofing: decide %s review request %s: %w", kind, req.ID, err)
	}
	if err := s.audit.Record(ctx, q, audit.IdentityProofingReviewItemDecided,
		audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
		audit.Updated(req.auditFields(map[string]any{"kind": string(kind), "state": string(ReviewPending)}),
			req.auditFields(map[string]any{"kind": string(kind), "state": string(decided), "reason": reason}))); err != nil {
		return nil, err
	}
	return &at, nil
}

// clearReviewItems drops req's waiting items at its purge, and the reasons of
// its decided ones, which may name the person.
func clearReviewItems(ctx context.Context, q database.Querier, id uuid.UUID) error {
	if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_review_items
		WHERE request_id = $1 AND state = 'pending'`, id); err != nil {
		return fmt.Errorf("proofing: purge waiting reviews request %s: %w", id, err)
	}
	if _, err := q.Exec(ctx, `UPDATE identity_proofing_review_items SET reason = NULL
		WHERE request_id = $1 AND reason IS NOT NULL`, id); err != nil {
		return fmt.Errorf("proofing: purge review reasons request %s: %w", id, err)
	}
	return nil
}

// LapseReviewItems lapses up to limit pending items past their due time,
// audited identity_proofing.review_item_decided with errorReviewLapsed and no
// actor. The session keeps its outcome. It returns how many it lapsed.
func (s *RequestStore) LapseReviewItems(ctx context.Context, now time.Time, limit int) (int, error) {
	lapsed := 0
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		rows, err := q.Query(ctx, `UPDATE identity_proofing_review_items i
			SET state = 'lapsed', decided_at = now(), error_code = $3
			FROM identity_proofing_requests r
			WHERE r.id = i.request_id AND i.id IN (
				SELECT id FROM identity_proofing_review_items
				WHERE state = 'pending' AND due_at <= $1
				ORDER BY due_at LIMIT $2 FOR UPDATE SKIP LOCKED)
			RETURNING i.request_id, i.organization_id, i.kind, r.subject_name, r.subject_email`,
			now, limit, errorReviewLapsed)
		if err != nil {
			return fmt.Errorf("proofing: lapse reviews: %w", err)
		}
		type lapsedItem struct {
			req  Request
			kind string
		}
		var items []lapsedItem
		for rows.Next() {
			var it lapsedItem
			if err := rows.Scan(&it.req.ID, &it.req.OrganizationID, &it.kind, &it.req.SubjectName, &it.req.SubjectEmail); err != nil {
				rows.Close()
				return fmt.Errorf("proofing: scan lapsed review: %w", err)
			}
			items = append(items, it)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("proofing: lapse reviews: %w", err)
		}
		for _, it := range items {
			if err := s.audit.Record(ctx, q, audit.IdentityProofingReviewItemDecided,
				audit.Target{Type: audit.TargetIdentityProofingRequest, ID: it.req.ID.String(), OrgID: &it.req.OrganizationID},
				audit.Updated(it.req.auditFields(map[string]any{"kind": it.kind, "state": string(ReviewPending)}),
					it.req.auditFields(map[string]any{"kind": it.kind, "state": string(ReviewLapsed), "errorCode": errorReviewLapsed}))); err != nil {
				return err
			}
		}
		lapsed = len(items)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return lapsed, nil
}

// NextReviewItemDue is when the next pending item is due for
// LapseReviewItems; zero for none.
func (s *RequestStore) NextReviewItemDue(ctx context.Context) (time.Time, error) {
	var due *time.Time
	if err := s.db.QueryRow(ctx, `SELECT min(due_at) FROM identity_proofing_review_items
		WHERE state = 'pending'`).Scan(&due); err != nil {
		return time.Time{}, fmt.Errorf("proofing: next review due: %w", err)
	}
	if due == nil {
		return time.Time{}, nil
	}
	return *due, nil
}

// ReviewCounts is how many items of each kind wait in org: stored ones and
// the requests whose own review waits. Every kind is in it, 0 when none waits.
func (s *RequestStore) ReviewCounts(ctx context.Context, orgID uuid.UUID) (map[ReviewKind]int, error) {
	rows, err := s.db.Query(ctx, `SELECT kind, count(*) FROM (
			SELECT i.kind FROM identity_proofing_review_items i
			JOIN identity_proofing_requests r ON r.id = i.request_id
			WHERE i.organization_id = $1 AND i.state = 'pending' AND r.purged_at IS NULL
			UNION ALL
			SELECT `+sessionReviewKindColumn+` FROM identity_proofing_requests r
			WHERE r.organization_id = $1 AND `+sessionReviewWaits+`
		) waiting GROUP BY kind`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: count reviews org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := make(map[ReviewKind]int, len(ReviewKinds))
	for _, k := range ReviewKinds {
		out[k] = 0
	}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, fmt.Errorf("proofing: count reviews org %s: %w", orgID, err)
		}
		out[ReviewKind(kind)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: count reviews org %s: %w", orgID, err)
	}
	return out, nil
}

// ReviewItemInput is a reviewer's decision on a review item: confirm or
// reject, for Reason, which is required and audited. ErrorCode is only for
// rejecting a session's own review (ReviewInput.ErrorCode).
type ReviewItemInput struct {
	Confirm   bool
	Reason    string
	ErrorCode string
	// ReviewerID is the member deciding.
	ReviewerID uuid.UUID
	// Scope is the requests the deciding member reaches.
	Scope ReviewScope
	// OwnAllowed lets the reviewer decide a check they sent or are the subject
	// of (auth.SelfReview: a platform admin on a dev stack only).
	OwnAllowed bool
}

// DecideReviewItem decides request id's waiting item of kind: an address
// item (DecideAddressReview), or the request's own review of that kind
// (DecideReview), so the identity is still decided by the engine. reviewer is
// the reviewer's name as the engine records it.
func (s *Service) DecideReviewItem(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind, reviewer string, in ReviewItemInput) (Request, error) {
	if kind == ReviewKindAddress {
		if in.ErrorCode != "" {
			return Request{}, fmt.Errorf("%w: an address decision takes no error code", ErrInvalidInput)
		}
		return s.DecideAddressReview(ctx, orgID, id, AddressReviewInput{
			Confirm: in.Confirm, Reason: in.Reason, ReviewerID: in.ReviewerID, Scope: in.Scope, OwnAllowed: in.OwnAllowed,
		})
	}
	return s.DecideReview(ctx, orgID, id, reviewer, ReviewInput{
		Approve: in.Confirm, ErrorCode: in.ErrorCode, Reason: in.Reason, Kind: kind,
		ReviewerID: in.ReviewerID, Scope: in.Scope, OwnAllowed: in.OwnAllowed,
	})
}

// ReviewCounts is how many items of each kind wait in org (RequestStore.ReviewCounts).
func (s *Service) ReviewCounts(ctx context.Context, orgID uuid.UUID) (map[ReviewKind]int, error) {
	return s.requests.ReviewCounts(ctx, orgID)
}
