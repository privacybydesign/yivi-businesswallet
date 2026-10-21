package proofing

import (
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
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

// Candidates is every external session of req's org that still
// holds personal data and has an outcome: what a data request is matched
// against. The person's earlier data requests are among them, since each
// holds who asked (an erasure covers them too), but only once decided: one
// still in review is the wallet's to decide, and is not purged under it. A
// session rejected with errorIdentityMismatch is not: whoever proofed it is
// known not to be the person it was sent to. Only the sessions whose identity
// match key is the requester's are candidates: another family name or date of
// birth is never read from the engine.
func (s *DataRequestStore) Candidates(ctx context.Context, req Request, requester SubjectIdentity) ([]Request, error) {
	key, err := s.identityMatchKey(req, requester)
	if err != nil || key == nil {
		return []Request{}, err
	}
	return s.scanRequests(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.organization_id = $1 AND r.subject_kind = 'external' AND r.id <> $2 AND r.identity_match_key = $5
			AND r.purged_at IS NULL AND r.ips_session_id IS NOT NULL
			AND (r.status IN ('approved', 'rejected') OR (r.status = 'needs_review' AND r.flow_kind = $3))
			AND r.error_code IS DISTINCT FROM $4
		ORDER BY r.created_at`, req.OrganizationID, req.ID, string(FlowIdentity), errorIdentityMismatch, key)
}

// UnfinishedCandidates is every unfinished external session of req's org
// (pending, in progress, expired or cancelled) that still holds personal data:
// what a data request matches by e-mail address (MatchEmail) or typed name
// (MatchName), there being no proofed identity in them.
func (s *DataRequestStore) UnfinishedCandidates(ctx context.Context, req Request) ([]Request, error) {
	return s.scanRequests(ctx, `SELECT `+requestColumns+requestFrom+`
		WHERE r.organization_id = $1 AND r.subject_kind = 'external' AND r.id <> $2
			AND r.purged_at IS NULL AND r.status IN ('pending', 'in_progress', 'expired', 'cancelled')
		ORDER BY r.created_at`, req.OrganizationID, req.ID)
}

// matchingLockKey namespaces TryLockMatching's advisory lock.
const matchingLockKey = "identity_proofing_matching:"

// TryLockMatching takes the lock on finding data request id's matches, across
// replicas, until release is called; ok is false while another holds it.
func (s *DataRequestStore) TryLockMatching(ctx context.Context, id uuid.UUID) (release func(), ok bool, err error) {
	return advisoryLock(ctx, s.db, matchingLockKey, id, false)
}

// MatchChecks is what req found for each candidate whose identity it read
// from the engine: the level it matched at, "" for none.
func (s *DataRequestStore) MatchChecks(ctx context.Context, req Request) (map[uuid.UUID]MatchLevel, error) {
	rows, err := s.db.Query(ctx, `SELECT candidate_request_id, COALESCE(level, '')
		FROM identity_proofing_request_match_checks WHERE request_id = $1 AND organization_id = $2`,
		req.ID, req.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("proofing: match checks request %s: %w", req.ID, err)
	}
	out := map[uuid.UUID]MatchLevel{}
	var id uuid.UUID
	var level MatchLevel
	if _, err := pgx.ForEachRow(rows, []any{&id, &level}, func() error {
		out[id] = level
		return nil
	}); err != nil {
		return nil, fmt.Errorf("proofing: match checks request %s: %w", req.ID, err)
	}
	return out, nil
}

// identityKeyPurpose names the secret identity match keys are hashed with.
const identityKeyPurpose = "identity_proofing_identity_match_key"

// identityMatchKey is the keyed hash of req's org, a family name, compared as
// diploma.Normalize does, and a date of birth: equal for the same person's
// external sessions at one org, without the name or date being stored, and
// never equal across orgs. nil for a member's request, or when the name or
// date is missing or unreadable.
func (s *DataRequestStore) identityMatchKey(req Request, id SubjectIdentity) ([]byte, error) {
	family := strings.Join(strings.Fields(diploma.Normalize(id.FamilyName)), " ")
	birthDate, err := diploma.ParseDate(id.BirthDate)
	if !req.External() || family == "" || err != nil {
		return nil, nil
	}
	if s.requests.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	secret, err := s.requests.cipher.DeriveSecret(identityKeyPurpose)
	if err != nil {
		return nil, fmt.Errorf("proofing: identity match key: %w", err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(req.OrganizationID.String() + "\x00" + family + "\x00" + birthDate.Format(time.DateOnly)))
	return mac.Sum(nil), nil
}

// RecordIdentityKey keeps the identity match key of who req's session
// proofed; nothing when the session read no name or date of birth.
func (s *DataRequestStore) RecordIdentityKey(ctx context.Context, req Request, id SubjectIdentity) error {
	key, err := s.identityMatchKey(req, id)
	if err != nil || key == nil {
		return err
	}
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_requests SET identity_match_key = $3
		WHERE id = $1 AND organization_id = $2 AND purged_at IS NULL`,
		req.ID, req.OrganizationID, key); err != nil {
		return fmt.Errorf("proofing: identity match key request %s: %w", req.ID, err)
	}
	return nil
}

// RecordMatchCheck keeps what req found for one candidate ("" for no match),
// so it is never read from the engine again.
func (s *DataRequestStore) RecordMatchCheck(ctx context.Context, req Request, candidateID uuid.UUID, level MatchLevel) error {
	if _, err := s.db.Exec(ctx, `INSERT INTO identity_proofing_request_match_checks
			(organization_id, request_id, candidate_request_id, level)
		VALUES ($1, $2, $3, NULLIF($4, ''))
		ON CONFLICT (request_id, candidate_request_id) DO NOTHING`,
		req.OrganizationID, req.ID, candidateID, string(level)); err != nil {
		return fmt.Errorf("proofing: match check request %s candidate %s: %w", req.ID, candidateID, err)
	}
	return nil
}

// SaveMatches replaces req's matches.
func (s *DataRequestStore) SaveMatches(ctx context.Context, req Request, matches []NewDataMatch) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_request_matches WHERE request_id = $1`, req.ID); err != nil {
			return fmt.Errorf("proofing: clear matches request %s: %w", req.ID, err)
		}
		for _, m := range matches {
			if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_request_matches
					(organization_id, request_id, matched_request_id, level)
				VALUES ($1, $2, $3, $4)`, req.OrganizationID, req.ID, m.RequestID, string(m.Level)); err != nil {
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
			m.level, m.approved, `+legalHeldUntil+`
		FROM identity_proofing_request_matches m
		JOIN identity_proofing_requests r ON r.id = m.matched_request_id AND r.organization_id = m.organization_id
		LEFT JOIN identity_proofing_settings st ON st.organization_id = r.organization_id
		WHERE m.request_id = $1 AND m.organization_id = $2
		ORDER BY m.level DESC, r.created_at DESC`, req.ID, req.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("proofing: matches request %s: %w", req.ID, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (DataMatch, error) {
		var m DataMatch
		err := row.Scan(&m.RequestID, &m.FlowName, &m.Status, &m.Method, &m.AssuranceLevel, &m.EIDASLevel,
			&m.CreatedAt, &m.CompletedAt, &m.PurgedAt, &m.Level, &m.Approved, &m.HeldUntil)
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
// when an approved "see my data" request's data downloads (nil for none) and
// how many matches an approved erasure kept (nil for any other decision).
func (s *DataRequestStore) RecordDecision(ctx context.Context, req Request, approved []uuid.UUID, exportUntil *time.Time, kept *int) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return cmp.Or(err, ErrNotUnderReview)
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_request_matches SET approved = (matched_request_id = ANY($2))
			WHERE request_id = $1`, req.ID, approved); err != nil {
			return fmt.Errorf("proofing: decide matches request %s: %w", req.ID, err)
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_requests SET data_export_until = $2, data_matches_kept = $3,
				updated_at = now()
			WHERE id = $1`, req.ID, exportUntil, kept); err != nil {
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
