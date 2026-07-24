package consent

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

// Store owns the approval queue and the policies. It writes each state change
// and its audit event in one transaction through the shared audit.Recorder seam,
// exactly as the organization slice does — no decision without its audit row.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

type rowScanner interface {
	Scan(dest ...any) error
}

const requestColumns = `id, organization_id, kind, counterparty, requested, status, mode,
	decided_subset, decided_by, decided_at, dual_decided_by, dual_decided_at, policy_id, expires_at, created_at`

func scanRequest(row rowScanner) (ApprovalRequest, error) {
	var r ApprovalRequest
	var kind, status, mode string
	err := row.Scan(&r.ID, &r.OrganizationID, &kind, &r.Counterparty, &r.Requested,
		&status, &mode, &r.DecidedSubset, &r.DecidedBy, &r.DecidedAt,
		&r.DualDecidedBy, &r.DualDecidedAt, &r.PolicyID, &r.ExpiresAt, &r.CreatedAt)
	r.Kind, r.Status, r.Mode = Kind(kind), Status(status), Mode(mode)
	return r, err
}

const policyColumns = `id, organization_id, kind, counterparty_pattern, required_attributes, effect,
	approve_subset, four_eyes, priority, created_by, valid_from, valid_until, revoked_at, created_at, updated_at`

func scanPolicy(row rowScanner) (Policy, error) {
	var p Policy
	var kind, effect string
	err := row.Scan(&p.ID, &p.OrganizationID, &kind, &p.CounterpartyPattern, &p.RequiredAttributes,
		&effect, &p.ApproveSubset, &p.FourEyes, &p.Priority, &p.CreatedBy,
		&p.ValidFrom, &p.ValidUntil, &p.RevokedAt, &p.CreatedAt, &p.UpdatedAt)
	p.Kind, p.Effect = Kind(kind), Effect(effect)
	return p, err
}

// EnqueueParams is an inbound request handed to the queue by a flow (#112/#32).
type EnqueueParams struct {
	Kind         Kind
	Counterparty string
	Requested    []string
	ExpiresAt    time.Time
	// ForceDualControl marks the item four-eyes from a resource threshold the
	// calling flow evaluates (the design's other dual-control source besides a
	// policy). It beats a policy's auto_approve, never a caller's concern to waive.
	ForceDualControl bool
}

// Enqueue records an inbound request, evaluating the org's policies for its kind
// first (first-match-wins). A matching auto_decline/auto_approve policy decides
// the item immediately (mode policy_auto); a four-eyes marker (from the policy or
// the caller) always beats auto_approve and leaves the item pending for two
// humans; otherwise the item is pending for one human (mode human_approved). Every
// path writes exactly one audit event.
func (s *Store) Enqueue(ctx context.Context, orgID uuid.UUID, p EnqueueParams) (ApprovalRequest, error) {
	if !p.Kind.valid() {
		return ApprovalRequest{}, ErrInvalidKind
	}
	if len(p.Requested) == 0 {
		return ApprovalRequest{}, ErrNoAttributes
	}

	var out ApprovalRequest
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		policies, err := listActivePoliciesTx(ctx, q, orgID, p.Kind)
		if err != nil {
			return err
		}
		matched := Match(policies, PendingItem{Kind: p.Kind, Counterparty: p.Counterparty, Requested: p.Requested})

		status := StatusPending
		mode := ModeHumanApproved
		subset := []string{}
		var policyID *uuid.UUID
		action := audit.ApprovalRequested

		dual := p.ForceDualControl || (matched != nil && matched.FourEyes)
		switch {
		case matched != nil && matched.Effect == EffectAutoDecline:
			// A decline is safe to automate even under a four-eyes marker: the floor
			// protects against approving without dual control, not declining.
			status, mode, policyID, action = StatusDeclined, ModePolicyAuto, &matched.ID, audit.ApprovalAutoDeclined
		case matched != nil && matched.Effect == EffectAutoApprove && !dual:
			status, mode, policyID, action = StatusApproved, ModePolicyAuto, &matched.ID, audit.ApprovalAutoApproved
			subset = resolveApprovedSubset(*matched, p.Requested)
		case dual:
			mode = ModeFourEyes
		}

		const insert = `INSERT INTO approval_requests
			(organization_id, kind, counterparty, requested, status, mode, decided_subset, policy_id, expires_at, decided_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, CASE WHEN $10::boolean THEN now() ELSE NULL END)
			RETURNING ` + requestColumns
		req, err := scanRequest(q.QueryRow(ctx, insert,
			orgID, string(p.Kind), p.Counterparty, p.Requested, string(status), string(mode),
			subset, policyID, p.ExpiresAt, status != StatusPending))
		if err != nil {
			return fmt.Errorf("consent: enqueue: %w", err)
		}
		out = req
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetApprovalRequest, ID: req.ID.String(), OrgID: &orgID},
			audit.Created(requestMetadata(req)))
	})
	return out, err
}

// Decide resolves a pending item as the first (or only) approver. For a
// human_approved item it approves the given attribute subset or declines. For a
// four-eyes item it records the first approval (the item stays pending for a
// distinct second approver via DecideDual) or, on decline, resolves it outright —
// a decline never needs two subjects. An approval's subset must be a non-empty
// subset of the requested attributes.
func (s *Store) Decide(ctx context.Context, orgID, requestID, approverID uuid.UUID, approve bool, subset []string) (ApprovalRequest, error) {
	var out ApprovalRequest
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		req, err := lockRequest(ctx, q, orgID, requestID)
		if err != nil {
			return err
		}
		if req.Status != StatusPending {
			return ErrRequestResolved
		}
		if req.Mode != ModeHumanApproved && req.Mode != ModeFourEyes {
			return ErrRequestResolved
		}

		if !approve {
			return s.resolve(ctx, q, &out, req, StatusDeclined, approverID, nil, req.DecidedSubset, audit.ApprovalDeclined)
		}
		if err := validateSubset(subset, req.Requested); err != nil {
			return err
		}
		if req.Mode == ModeFourEyes {
			if req.DecidedBy != nil {
				return ErrAwaitingSecondApproval
			}
			// First approval: record it and the proposed subset; item stays pending.
			// No audit here — the decision is not complete until the second approver.
			const upd = `UPDATE approval_requests
				SET decided_by = $2, decided_at = now(), decided_subset = $3
				WHERE id = $1 RETURNING ` + requestColumns
			updated, err := scanRequest(q.QueryRow(ctx, upd, requestID, approverID, subset))
			if err != nil {
				return fmt.Errorf("consent: record first approval %s: %w", requestID, err)
			}
			out = updated
			return nil
		}
		return s.resolve(ctx, q, &out, req, StatusApproved, approverID, nil, subset, audit.ApprovalApproved)
	})
	return out, err
}

// DecideDual completes a four-eyes item as the distinct second approver. The
// first approval must already be recorded (via Decide), and the second approver
// must be a different subject; the first approver's subset stands.
func (s *Store) DecideDual(ctx context.Context, orgID, requestID, approverID uuid.UUID, approve bool) (ApprovalRequest, error) {
	var out ApprovalRequest
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		req, err := lockRequest(ctx, q, orgID, requestID)
		if err != nil {
			return err
		}
		if req.Status != StatusPending {
			return ErrRequestResolved
		}
		if req.Mode != ModeFourEyes {
			return ErrNotDualControl
		}
		if req.DecidedBy == nil {
			return ErrAwaitingFirstApproval
		}
		if *req.DecidedBy == approverID {
			return ErrSameApprover
		}

		status := StatusApproved
		action := audit.ApprovalApproved
		if !approve {
			status, action = StatusDeclined, audit.ApprovalDeclined
		}
		return s.resolve(ctx, q, &out, req, status, *req.DecidedBy, &approverID, req.DecidedSubset, action)
	})
	return out, err
}

// resolve writes the terminal status, the deciders and the subset, then audits
// the change as a {before, after} update on the same transaction.
func (s *Store) resolve(ctx context.Context, q database.Querier, out *ApprovalRequest, req ApprovalRequest,
	status Status, decidedBy uuid.UUID, dualBy *uuid.UUID, subset []string, action string,
) error {
	if subset == nil {
		subset = []string{}
	}
	const upd = `UPDATE approval_requests SET
			status = $2,
			decided_by = COALESCE(decided_by, $3),
			decided_at = COALESCE(decided_at, now()),
			decided_subset = $4,
			dual_decided_by = $5,
			dual_decided_at = CASE WHEN $5::uuid IS NULL THEN NULL ELSE now() END
		WHERE id = $1 RETURNING ` + requestColumns
	updated, err := scanRequest(q.QueryRow(ctx, upd, req.ID, string(status), decidedBy, subset, dualBy))
	if err != nil {
		return fmt.Errorf("consent: resolve %s: %w", req.ID, err)
	}
	*out = updated
	orgID := req.OrganizationID
	return s.audit.Record(ctx, q, action,
		audit.Target{Type: audit.TargetApprovalRequest, ID: req.ID.String(), OrgID: &orgID},
		audit.Updated(
			map[string]any{"status": string(StatusPending)},
			decisionMetadata(updated)))
}

// ListPending returns the org's pending items, oldest first.
func (s *Store) ListPending(ctx context.Context, orgID uuid.UUID) ([]ApprovalRequest, error) {
	rows, err := s.db.Query(ctx, `SELECT `+requestColumns+`
		FROM approval_requests WHERE organization_id = $1 AND status = 'pending' ORDER BY created_at`, orgID)
	if err != nil {
		return nil, fmt.Errorf("consent: list pending: %w", err)
	}
	defer rows.Close()

	items := []ApprovalRequest{}
	for rows.Next() {
		req, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("consent: list pending scan: %w", err)
		}
		items = append(items, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consent: list pending rows: %w", err)
	}
	return items, nil
}

// SweepExpired marks every pending item whose expiry has passed as expired and
// audits each. #27 owns the schedule that calls it (the invitation-expiry
// precedent); the store provides the mechanism and its audit trail. It returns
// the number of items expired.
func (s *Store) SweepExpired(ctx context.Context, orgID uuid.UUID) (int, error) {
	var expired int
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Collect the whole set before auditing: a single tx connection can't run
		// audit writes while the RETURNING rows are still being iterated.
		rows, err := q.Query(ctx, `UPDATE approval_requests SET status = 'expired'
			WHERE organization_id = $1 AND status = 'pending' AND expires_at <= now()
			RETURNING id, counterparty, kind`, orgID)
		if err != nil {
			return fmt.Errorf("consent: sweep expired: %w", err)
		}
		type expiredItem struct {
			id           uuid.UUID
			counterparty string
			kind         string
		}
		var items []expiredItem
		for rows.Next() {
			var it expiredItem
			if err := rows.Scan(&it.id, &it.counterparty, &it.kind); err != nil {
				rows.Close()
				return fmt.Errorf("consent: sweep expired scan: %w", err)
			}
			items = append(items, it)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("consent: sweep expired rows: %w", err)
		}

		for _, it := range items {
			if err := s.audit.Record(ctx, q, audit.ApprovalExpired,
				audit.Target{Type: audit.TargetApprovalRequest, ID: it.id.String(), OrgID: &orgID},
				audit.Updated(
					map[string]any{"status": string(StatusPending)},
					map[string]any{"status": string(StatusExpired), "kind": it.kind, "counterparty": it.counterparty})); err != nil {
				return err
			}
		}
		expired = len(items)
		return nil
	})
	return expired, err
}

// CreatePolicy authors a new auto-decide rule under the given admin.
func (s *Store) CreatePolicy(ctx context.Context, orgID, authorID uuid.UUID, spec PolicySpec) (Policy, error) {
	if err := spec.validate(); err != nil {
		return Policy{}, err
	}
	spec = spec.normalized()

	var out Policy
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO policies
			(organization_id, kind, counterparty_pattern, required_attributes, effect, approve_subset, four_eyes, priority, created_by, valid_from, valid_until)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING ` + policyColumns
		p, err := scanPolicy(q.QueryRow(ctx, insert,
			orgID, string(spec.Kind), spec.CounterpartyPattern, spec.RequiredAttributes, string(spec.Effect),
			spec.ApproveSubset, spec.FourEyes, spec.Priority, authorID, spec.ValidFrom, spec.ValidUntil))
		if err != nil {
			return fmt.Errorf("consent: create policy: %w", err)
		}
		out = p
		return s.audit.Record(ctx, q, audit.PolicyCreated,
			audit.Target{Type: audit.TargetPolicy, ID: p.ID.String(), OrgID: &orgID},
			audit.Created(policyMetadata(p)))
	})
	return out, err
}

// UpdatePolicy edits an active policy. A revoked policy cannot be edited.
func (s *Store) UpdatePolicy(ctx context.Context, orgID, policyID uuid.UUID, spec PolicySpec) (Policy, error) {
	if err := spec.validate(); err != nil {
		return Policy{}, err
	}
	spec = spec.normalized()

	var out Policy
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := lockPolicy(ctx, q, orgID, policyID)
		if err != nil {
			return err
		}
		const upd = `UPDATE policies SET
				kind = $2, counterparty_pattern = $3, required_attributes = $4, effect = $5,
				approve_subset = $6, four_eyes = $7, priority = $8, valid_from = $9, valid_until = $10,
				updated_at = now()
			WHERE id = $1 RETURNING ` + policyColumns
		after, err := scanPolicy(q.QueryRow(ctx, upd,
			policyID, string(spec.Kind), spec.CounterpartyPattern, spec.RequiredAttributes, string(spec.Effect),
			spec.ApproveSubset, spec.FourEyes, spec.Priority, spec.ValidFrom, spec.ValidUntil))
		if err != nil {
			return fmt.Errorf("consent: update policy %s: %w", policyID, err)
		}
		out = after
		return s.audit.Record(ctx, q, audit.PolicyUpdated,
			audit.Target{Type: audit.TargetPolicy, ID: policyID.String(), OrgID: &orgID},
			audit.Updated(policyMetadata(before), policyMetadata(after)))
	})
	return out, err
}

// RevokePolicy revokes a policy immediately; the matcher stops seeing it at once.
func (s *Store) RevokePolicy(ctx context.Context, orgID, policyID uuid.UUID) (Policy, error) {
	var out Policy
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := lockPolicy(ctx, q, orgID, policyID)
		if err != nil {
			return err
		}
		after, err := scanPolicy(q.QueryRow(ctx,
			`UPDATE policies SET revoked_at = now(), updated_at = now() WHERE id = $1 RETURNING `+policyColumns, policyID))
		if err != nil {
			return fmt.Errorf("consent: revoke policy %s: %w", policyID, err)
		}
		out = after
		return s.audit.Record(ctx, q, audit.PolicyRevoked,
			audit.Target{Type: audit.TargetPolicy, ID: policyID.String(), OrgID: &orgID},
			audit.Deleted(policyMetadata(before)))
	})
	return out, err
}

// ListPolicies returns the org's active (non-revoked) policies in evaluation
// order, across both kinds — the management view.
func (s *Store) ListPolicies(ctx context.Context, orgID uuid.UUID) ([]Policy, error) {
	rows, err := s.db.Query(ctx, `SELECT `+policyColumns+`
		FROM policies WHERE organization_id = $1 AND revoked_at IS NULL ORDER BY priority, created_at`, orgID)
	if err != nil {
		return nil, fmt.Errorf("consent: list policies: %w", err)
	}
	defer rows.Close()
	return collectPolicies(rows)
}

func listActivePoliciesTx(ctx context.Context, q database.Querier, orgID uuid.UUID, kind Kind) ([]Policy, error) {
	rows, err := q.Query(ctx, `SELECT `+policyColumns+`
		FROM policies WHERE organization_id = $1 AND kind = $2 AND revoked_at IS NULL ORDER BY priority, created_at`,
		orgID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("consent: list active policies: %w", err)
	}
	defer rows.Close()
	return collectPolicies(rows)
}

func collectPolicies(rows pgx.Rows) ([]Policy, error) {
	policies := []Policy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("consent: scan policy: %w", err)
		}
		policies = append(policies, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("consent: policy rows: %w", err)
	}
	return policies, nil
}

func lockRequest(ctx context.Context, q database.Querier, orgID, requestID uuid.UUID) (ApprovalRequest, error) {
	req, err := scanRequest(q.QueryRow(ctx,
		`SELECT `+requestColumns+` FROM approval_requests WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		requestID, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovalRequest{}, ErrRequestNotFound
	}
	if err != nil {
		return ApprovalRequest{}, fmt.Errorf("consent: lock request %s: %w", requestID, err)
	}
	return req, nil
}

func lockPolicy(ctx context.Context, q database.Querier, orgID, policyID uuid.UUID) (Policy, error) {
	p, err := scanPolicy(q.QueryRow(ctx,
		`SELECT `+policyColumns+` FROM policies WHERE id = $1 AND organization_id = $2 FOR UPDATE`,
		policyID, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, ErrPolicyNotFound
	}
	if err != nil {
		return Policy{}, fmt.Errorf("consent: lock policy %s: %w", policyID, err)
	}
	if p.RevokedAt != nil {
		return Policy{}, ErrPolicyRevoked
	}
	return p, nil
}

func validateSubset(subset, requested []string) error {
	if len(subset) == 0 {
		return ErrEmptySubset
	}
	if !isSubset(subset, requested) {
		return ErrSubsetNotSubset
	}
	return nil
}

// normalized fills nil slices with empties (the columns are NOT NULL) and clears
// an approve subset on an auto_decline policy, where it is meaningless.
func (s PolicySpec) normalized() PolicySpec {
	s.RequiredAttributes = nonNil(s.RequiredAttributes)
	s.ApproveSubset = nonNil(s.ApproveSubset)
	if s.Effect == EffectAutoDecline {
		s.ApproveSubset = []string{}
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func requestMetadata(r ApprovalRequest) map[string]any {
	m := map[string]any{
		"kind":         string(r.Kind),
		"counterparty": r.Counterparty,
		"requested":    r.Requested,
		"mode":         string(r.Mode),
		"status":       string(r.Status),
	}
	if len(r.DecidedSubset) > 0 {
		m["decidedSubset"] = r.DecidedSubset
	}
	if r.PolicyID != nil {
		m["policyId"] = r.PolicyID.String()
	}
	return m
}

func decisionMetadata(r ApprovalRequest) map[string]any {
	m := map[string]any{
		"status":       string(r.Status),
		"kind":         string(r.Kind),
		"counterparty": r.Counterparty,
	}
	if r.DecidedBy != nil {
		m["decidedBy"] = r.DecidedBy.String()
	}
	if r.DualDecidedBy != nil {
		m["dualDecidedBy"] = r.DualDecidedBy.String()
	}
	if len(r.DecidedSubset) > 0 {
		m["decidedSubset"] = r.DecidedSubset
	}
	return m
}

func policyMetadata(p Policy) map[string]any {
	m := map[string]any{
		"kind":                string(p.Kind),
		"counterpartyPattern": p.CounterpartyPattern,
		"effect":              string(p.Effect),
		"fourEyes":            p.FourEyes,
		"priority":            p.Priority,
	}
	if len(p.RequiredAttributes) > 0 {
		m["requiredAttributes"] = p.RequiredAttributes
	}
	if len(p.ApproveSubset) > 0 {
		m["approveSubset"] = p.ApproveSubset
	}
	return m
}
