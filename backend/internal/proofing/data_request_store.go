package proofing

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// DataRequestStore persists each flow's kind per org and the matches a data
// request found, reading requests as RequestStore does.
type DataRequestStore struct {
	db       database.DB
	audit    audit.Recorder
	requests *RequestStore
}

func NewDataRequestStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *DataRequestStore {
	return &DataRequestStore{db: db, audit: recorder, requests: NewRequestStore(db, recorder, cipher)}
}

// FlowKind returns the flow's kind; a flow never set is FlowIdentity.
func (s *DataRequestStore) FlowKind(ctx context.Context, orgID uuid.UUID, flowID string) (FlowKind, error) {
	return getFlowKind(ctx, s.db, orgID, flowID)
}

func getFlowKind(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (FlowKind, error) {
	var kind FlowKind
	err := q.QueryRow(ctx, `SELECT kind FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlowIdentity, nil
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read kind flow %s: %w", flowID, err)
	}
	return kind, nil
}

// AllFlowKinds returns every flow of the org that is not an identity check; a
// flow missing is FlowIdentity.
func (s *DataRequestStore) AllFlowKinds(ctx context.Context, orgID uuid.UUID) (map[string]FlowKind, error) {
	rows, err := s.db.Query(ctx, `SELECT flow_id, kind FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND kind <> $2`, orgID, string(FlowIdentity))
	if err != nil {
		return nil, fmt.Errorf("proofing: list flow kinds org %s: %w", orgID, err)
	}
	out := map[string]FlowKind{}
	var flowID string
	var kind FlowKind
	if _, err := pgx.ForEachRow(rows, []any{&flowID, &kind}, func() error {
		out[flowID] = kind
		return nil
	}); err != nil {
		return nil, fmt.Errorf("proofing: list flow kinds org %s: %w", orgID, err)
	}
	return out, nil
}

// SaveFlowKind sets the flow's kind and audits
// identity_proofing.flow_kind_configured with before and after. Saving what it
// already has changes nothing.
func (s *DataRequestStore) SaveFlowKind(ctx context.Context, orgID uuid.UUID, flowID string, kind FlowKind) (FlowKind, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowKind(ctx, q, orgID, flowID)
		if err != nil || before == kind {
			return err
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, kind)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, flow_id) DO UPDATE SET kind = EXCLUDED.kind, updated_at = now()`,
			orgID, flowID, string(kind)); err != nil {
			return fmt.Errorf("proofing: save kind flow %s: %w", flowID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowKindConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(map[string]any{"kind": string(before)}, map[string]any{"kind": string(kind)}))
	})
	if err != nil {
		return "", err
	}
	return kind, nil
}

// Candidates is every session of req's customer that still
// holds personal data and has an outcome: what a data request is matched
// against. The person's earlier data requests are among them, since each
// holds who asked (an erasure covers them too), but only once decided: one
// still in review is the wallet's to decide, and is not purged under it.
func (s *DataRequestStore) Candidates(ctx context.Context, req Request) ([]Request, error) {
	return s.scanRequests(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.organization_id = $1 AND r.customer_id = $2 AND r.id <> $3
			AND r.purged_at IS NULL AND r.ips_session_id IS NOT NULL
			AND (r.status IN ('approved', 'rejected') OR (r.status = 'needs_review' AND r.flow_kind = $4))
		ORDER BY r.created_at`, req.OrganizationID, req.CustomerID, req.ID, string(FlowIdentity))
}

// UnfinishedCandidates is every unfinished session of req's customer
// (pending, in progress, expired or cancelled) that still holds personal data:
// what a data request matches by e-mail address (MatchEmail) or typed name
// (MatchName), there being no proofed identity in them.
func (s *DataRequestStore) UnfinishedCandidates(ctx context.Context, req Request) ([]Request, error) {
	return s.scanRequests(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.organization_id = $1 AND r.customer_id = $2 AND r.id <> $3
			AND r.purged_at IS NULL AND r.status IN ('pending', 'in_progress', 'expired', 'cancelled')
		ORDER BY r.created_at`, req.OrganizationID, req.CustomerID, req.ID)
}

// SaveMatches replaces req's matches.
func (s *DataRequestStore) SaveMatches(ctx context.Context, req Request, matches []NewDataMatch) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_request_matches WHERE request_id = $1`, req.ID); err != nil {
			return fmt.Errorf("proofing: clear matches request %s: %w", req.ID, err)
		}
		for _, m := range matches {
			if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_request_matches (request_id, matched_request_id, level)
				VALUES ($1, $2, $3)`, req.ID, m.RequestID, string(m.Level)); err != nil {
				return fmt.Errorf("proofing: match request %s: %w", req.ID, err)
			}
		}
		return nil
	})
}

// Matches is req's matches, strong ones first, newest first within.
func (s *DataRequestStore) Matches(ctx context.Context, req Request) ([]DataMatch, error) {
	rows, err := s.db.Query(ctx, `SELECT m.matched_request_id, r.flow_name, r.status, COALESCE(r.method, ''),
			COALESCE(r.assurance_level, ''), COALESCE(r.eidas_level, ''), r.created_at, r.completed_at, r.purged_at,
			m.level, m.approved
		FROM identity_proofing_request_matches m
		JOIN identity_proofing_requests r ON r.id = m.matched_request_id
		WHERE m.request_id = $1
		ORDER BY m.level DESC, r.created_at DESC`, req.ID)
	if err != nil {
		return nil, fmt.Errorf("proofing: matches request %s: %w", req.ID, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DataMatch, error) {
		var m DataMatch
		err := row.Scan(&m.RequestID, &m.FlowName, &m.Status, &m.Method, &m.AssuranceLevel, &m.EIDASLevel,
			&m.CreatedAt, &m.CompletedAt, &m.PurgedAt, &m.Level, &m.Approved)
		return m, err
	})
	if err != nil {
		return nil, fmt.Errorf("proofing: matches request %s: %w", req.ID, err)
	}
	return out, nil
}

// MatchedRequests is every session req matched, as requests.
func (s *DataRequestStore) MatchedRequests(ctx context.Context, req Request) ([]Request, error) {
	return s.scanRequests(ctx, `SELECT `+requestColumns+requestFrom+`
		JOIN identity_proofing_request_matches m ON m.matched_request_id = r.id
		WHERE m.request_id = $1 AND r.organization_id = $2
		ORDER BY r.created_at`, req.ID, req.OrganizationID)
}

func (s *DataRequestStore) scanRequests(ctx context.Context, query string, args ...any) ([]Request, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("proofing: data request sessions: %w", err)
	}
	defer rows.Close()
	out := []Request{}
	for rows.Next() {
		req, err := s.requests.scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("proofing: scan data request session: %w", err)
		}
		out = append(out, req)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: data request sessions: %w", err)
	}
	return out, nil
}

// RecordDecision marks each of req's matches approved or not, and sets until
// when an approved "see my data" request's data downloads (nil for none).
func (s *DataRequestStore) RecordDecision(ctx context.Context, req Request, approved []uuid.UUID, exportUntil *time.Time) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return cmp.Or(err, ErrNotUnderReview)
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_request_matches SET approved = (matched_request_id = ANY($2))
			WHERE request_id = $1`, req.ID, approved); err != nil {
			return fmt.Errorf("proofing: decide matches request %s: %w", req.ID, err)
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_requests SET data_export_until = $2, updated_at = now()
			WHERE id = $1`, req.ID, exportUntil); err != nil {
			return fmt.Errorf("proofing: data export request %s: %w", req.ID, err)
		}
		return nil
	})
}

// RecordExported audits identity_proofing.data_exported: who downloaded an
// approved "see my data" request's data, never what it held.
func (s *DataRequestStore) RecordExported(ctx context.Context, req Request, sessions int) error {
	return s.audit.Record(ctx, s.db, audit.IdentityProofingDataExported,
		audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
		audit.Created(map[string]any{"sessions": sessions}))
}
