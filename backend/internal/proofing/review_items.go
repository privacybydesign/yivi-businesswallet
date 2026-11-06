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

// stepReview is whether kind is an extra step's review, stored as its own
// item beside the session's outcome.
func (k ReviewKind) stepReview() bool {
	return k == ReviewKindAddress || k == ReviewKindEmployment
}

// reviewKind is the kind of req's own review (needs_review): a data
// request's, else the identity's.
func (r Request) reviewKind() ReviewKind {
	if r.FlowKind.dataRequest() {
		return ReviewKindDataRequest
	}
	return ReviewKindIdentity
}

// sessionReviewKindColumn is Request.reviewKind in SQL.
const sessionReviewKindColumn = `CASE WHEN r.flow_kind IN ('data_access', 'data_erasure') THEN 'data_request' ELSE 'identity' END`

// sessionReviewWaits is a request r whose own review waits, as the Reviews
// page lists it: it reads needs_review and is not erased. Those review items
// are the request's status, not rows of identity_proofing_review_items.
const sessionReviewWaits = `r.purged_at IS NULL AND ` + effectiveStatusColumn + ` = 'needs_review'`

// closedReview is a request r the Reviews page lists as closed: a data request
// that ended, or a session a reviewer decided something of and nothing of
// which waits.
const closedReview = `((r.flow_kind IN ('data_access', 'data_erasure') AND r.status IN ('approved', 'rejected'))
	OR (r.flow_kind = 'identity' AND r.purged_at IS NULL AND NOT (` + sessionReviewWaits + `)
		AND NOT EXISTS (SELECT 1 FROM identity_proofing_review_items i WHERE i.request_id = r.id AND i.state = 'pending')
		AND (EXISTS (SELECT 1 FROM identity_proofing_review_items i WHERE i.request_id = r.id)
			OR EXISTS (SELECT 1 ` + identityReviewDecided + `))))`

// closedReviewOrder lists closed reviews by when they closed, the latest first:
// the last decision, else the end of the data request.
const closedReviewOrder = `COALESCE(GREATEST(
		(SELECT max(i.decided_at) FROM identity_proofing_review_items i WHERE i.request_id = r.id),
		(SELECT max(a.occurred_at) ` + identityReviewDecided + `)),
	r.completed_at, r.created_at) DESC, r.created_at DESC`

// reviewItemMatches is a request r with a review item of kind $k in state $s,
// stored or its own review: the filter of RequestFilter.ReviewKind.
func reviewItemMatches(k, s string) string {
	return `(EXISTS (SELECT 1 FROM identity_proofing_review_items i
			WHERE i.request_id = r.id AND i.kind = ` + k + ` AND i.state = ` + s + `)
		OR (` + s + ` = 'pending' AND ` + sessionReviewWaits + ` AND ` + sessionReviewKindColumn + ` = ` + k + `)
		OR (` + k + ` = 'identity' AND r.flow_kind = 'identity' AND r.purged_at IS NULL
			AND r.status = CASE ` + s + ` WHEN 'confirmed' THEN 'approved' WHEN 'rejected' THEN 'rejected' END
			AND EXISTS (SELECT 1 ` + identityReviewDecided + `)))`
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

// latestAddressItem and latestEmploymentItem pick r's latest item of a step.
const (
	latestAddressItem = `FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind = 'address' ORDER BY i.created_at DESC LIMIT 1`
	latestEmploymentItem = `FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind = 'employment' ORDER BY i.created_at DESC LIMIT 1`
)

// stepReviewColumns are the state of r's address and employment items and
// when each was decided, when the first waiting one lapses, and when a
// reviewer decided its identity, for Request's reviews.
const stepReviewColumns = `COALESCE((SELECT i.state ` + latestAddressItem + `), ''),
	(SELECT i.decided_at ` + latestAddressItem + `),
	COALESCE((SELECT i.state ` + latestEmploymentItem + `), ''),
	(SELECT i.decided_at ` + latestEmploymentItem + `),
	(SELECT min(i.due_at) FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind IN ('address', 'employment') AND i.state = 'pending'),
	CASE WHEN r.flow_kind = 'identity' THEN (SELECT max(a.occurred_at) ` + identityReviewDecided + `) END`

// identityReviewDecided is r's identity_proofing.review_decided audit events:
// a reviewer decided its identity, which the request keeps no column of.
const identityReviewDecided = `FROM audit_events a
		WHERE a.organization_id = r.organization_id AND a.target_type = 'identity_proofing_request'
			AND a.target_id = r.id::text AND a.action = 'identity_proofing.review_decided'`

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
// when it was decided: nil, and changes nothing, when none waits. The session
// keeps its outcome.
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
	return s.closeReviewItems(ctx, `state = 'pending' AND due_at <= $4 ORDER BY due_at LIMIT $5`,
		ReviewLapsed, errorReviewLapsed, "", now, limit)
}

// RejectPendingReviewItems rejects every pending item of org with code and
// reason, as nobody may decide them (a paused org), audited with no actor.
// The session keeps its outcome.
func (s *RequestStore) RejectPendingReviewItems(ctx context.Context, orgID uuid.UUID, code, reason string) error {
	_, err := s.closeReviewItems(ctx, `state = 'pending' AND organization_id = $4`,
		ReviewRejected, code, reason, orgID)
	return err
}

// closeReviewItems moves the items where (over $4 on) selects to state with
// code and reason, each audited identity_proofing.review_item_decided, and
// returns how many it moved.
func (s *RequestStore) closeReviewItems(ctx context.Context, where string, state ReviewState, code, reason string, args ...any) (int, error) {
	closed := 0
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		rows, err := q.Query(ctx, `UPDATE identity_proofing_review_items i
			SET state = $1, decided_at = now(), error_code = $2, reason = NULLIF($3, '')
			FROM identity_proofing_requests r
			WHERE r.id = i.request_id AND i.id IN (
				SELECT id FROM identity_proofing_review_items WHERE `+where+` FOR UPDATE SKIP LOCKED)
			RETURNING i.request_id, i.organization_id, i.kind, r.subject_name, r.subject_email`,
			append([]any{string(state), code, reason}, args...)...)
		if err != nil {
			return fmt.Errorf("proofing: close reviews: %w", err)
		}
		type closedItem struct {
			req  Request
			kind string
		}
		items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (closedItem, error) {
			var it closedItem
			err := row.Scan(&it.req.ID, &it.req.OrganizationID, &it.kind, &it.req.SubjectName, &it.req.SubjectEmail)
			return it, err
		})
		if err != nil {
			return fmt.Errorf("proofing: close reviews: %w", err)
		}
		for _, it := range items {
			after := map[string]any{"kind": it.kind, "state": string(state), "errorCode": code}
			if reason != "" {
				after["reason"] = reason
			}
			if err := s.audit.Record(ctx, q, audit.IdentityProofingReviewItemDecided,
				audit.Target{Type: audit.TargetIdentityProofingRequest, ID: it.req.ID.String(), OrgID: &it.req.OrganizationID},
				audit.Updated(it.req.auditFields(map[string]any{"kind": it.kind, "state": string(ReviewPending)}),
					it.req.auditFields(after))); err != nil {
				return err
			}
		}
		closed = len(items)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return closed, nil
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

// ReviewTally is what waits for a reviewer in an org.
type ReviewTally struct {
	// Kinds is how many sessions wait with a review of each kind; every kind
	// is in it, 0 when none waits. A session waiting for its identity and its
	// address counts under both.
	Kinds map[ReviewKind]int
	// Sessions is how many sessions wait, each once.
	Sessions int
	// StepSessions is how many of them wait for a step (an address, a
	// Verzekeringsbericht), each once.
	StepSessions int
}

// ReviewCounts is what waits in org: per kind, in all, and for a step.
func (s *RequestStore) ReviewCounts(ctx context.Context, orgID uuid.UUID) (ReviewTally, error) {
	const stepWaits = `EXISTS (SELECT 1 FROM identity_proofing_review_items i
		WHERE i.request_id = r.id AND i.kind = %s AND i.state = 'pending')`
	address, employment := fmt.Sprintf(stepWaits, "$2"), fmt.Sprintf(stepWaits, "$3")
	var identity, dataRequest, addresses, statements, sessions, steps int
	err := s.db.QueryRow(ctx, `WITH waiting AS (
			SELECT `+sessionReviewWaits+` AS own, `+sessionReviewKindColumn+` AS own_kind,
				`+address+` AS address, `+employment+` AS employment
			FROM identity_proofing_requests r
			WHERE r.organization_id = $1 AND r.purged_at IS NULL
		)
		SELECT count(*) FILTER (WHERE own AND own_kind = $4), count(*) FILTER (WHERE own AND own_kind = $5),
			count(*) FILTER (WHERE address), count(*) FILTER (WHERE employment),
			count(*) FILTER (WHERE own OR address OR employment), count(*) FILTER (WHERE address OR employment)
		FROM waiting`, orgID, string(ReviewKindAddress), string(ReviewKindEmployment),
		string(ReviewKindIdentity), string(ReviewKindDataRequest)).
		Scan(&identity, &dataRequest, &addresses, &statements, &sessions, &steps)
	if err != nil {
		return ReviewTally{}, fmt.Errorf("proofing: count reviews org %s: %w", orgID, err)
	}
	return ReviewTally{
		Kinds: map[ReviewKind]int{
			ReviewKindIdentity: identity, ReviewKindAddress: addresses,
			ReviewKindEmployment: statements, ReviewKindDataRequest: dataRequest,
		},
		Sessions: sessions, StepSessions: steps,
	}, nil
}

// ReviewItemInput is a reviewer's decision on a review item: confirm or
// reject, for Reason, which is required and audited. ErrorCode is only for
// rejecting a session's own review (ReviewInput.ErrorCode), never a step's.
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

// DecideReviewItem decides request id's waiting item of kind: a step's item
// (decideStepReview), or the request's own review of that kind
// (DecideReview), so the identity is still decided by the engine. reviewer is
// the reviewer's name as the engine records it.
func (s *Service) DecideReviewItem(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind, reviewer string, in ReviewItemInput) (Request, error) {
	if kind.stepReview() {
		if in.ErrorCode != "" {
			return Request{}, fmt.Errorf("%w: a step's decision takes no error code", ErrInvalidInput)
		}
		return s.decideStepReview(ctx, orgID, id, kind, StepReviewInput{
			Confirm: in.Confirm, Reason: in.Reason, ReviewerID: in.ReviewerID, Scope: in.Scope, OwnAllowed: in.OwnAllowed,
		})
	}
	return s.DecideReview(ctx, orgID, id, reviewer, ReviewInput{
		Approve: in.Confirm, ErrorCode: in.ErrorCode, Reason: in.Reason, Kind: kind,
		ReviewerID: in.ReviewerID, Scope: in.Scope, OwnAllowed: in.OwnAllowed,
	})
}

// ReviewCounts is what waits in org (RequestStore.ReviewCounts).
func (s *Service) ReviewCounts(ctx context.Context, orgID uuid.UUID) (ReviewTally, error) {
	return s.requests.ReviewCounts(ctx, orgID)
}
