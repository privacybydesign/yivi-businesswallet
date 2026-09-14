package organization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/vog"
)

// keyedHash is a keyed (HMAC-SHA256) hash of a VOG's kenmerk: enough to detect
// the same document being re-uploaded without keeping the reference number
// itself (#242's data-minimisation design).
func keyedHash(key []byte, reference string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(reference))
	return mac.Sum(nil)
}

// ScreeningMatchContext is what a screening decision matches a document
// against: the member's own verified identity, resolved once per attempt so
// neither the PDF path nor the credential path re-derives it differently.
type ScreeningMatchContext struct {
	Name        identity.Name
	DateOfBirth *time.Time
	MemberType  string
	Email       string
}

// ScreeningMatchContext resolves a member's identity for a screening decision.
// ErrNotMember when userID is not a member of orgID.
func (s *Store) ScreeningMatchContext(ctx context.Context, orgID, userID uuid.UUID) (ScreeningMatchContext, error) {
	const q = `
		SELECT u.given_names, u.last_name, m.date_of_birth, m.member_type, u.email
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.user_id = $2`
	var mc ScreeningMatchContext
	var givenNames, lastName string
	err := s.db.QueryRow(ctx, q, orgID, userID).Scan(&givenNames, &lastName, &mc.DateOfBirth, &mc.MemberType, &mc.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScreeningMatchContext{}, ErrNotMember
	}
	if err != nil {
		return ScreeningMatchContext{}, fmt.Errorf("organization: screening match context user %s org %s: %w", userID, orgID, err)
	}
	mc.Name = identity.Name{GivenNames: givenNames, LastName: lastName}
	return mc, nil
}

// ScreeningInput is one completed screening attempt, ready to record - the
// ScreeningService's output and RecordScreening's input.
type ScreeningInput struct {
	Method           vog.Method
	Result           vog.Result
	CheckedBy        string
	CheckedByUserID  *uuid.UUID
	VogIssueDate     *time.Time
	Reference        string
	CoveredCodes     []string
	MissingCodes     []string
	GAAVResponseCode *int
}

// RecordScreening writes one member_screenings row, updates the membership's
// denormalised vog_last_result / vog_valid_until / vog_covered_codes (see the
// migration's comment for why), clears any outstanding admin request and the
// reminder cadence, and audits the outcome - all in one transaction. valid_until
// is computed from in.VogIssueDate or now (per settings.RecheckAnchor) and is
// only ever non-NULL when in.Result is vog.ResultValid.
func (s *Store) RecordScreening(ctx context.Context, orgID, userID uuid.UUID, memberType string, settings ScreeningSettings, in ScreeningInput, hashKey []byte) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		checkedAt := time.Now()

		var validUntil *time.Time
		if in.Result == vog.ResultValid {
			anchor := checkedAt
			if settings.RecheckAnchor == RecheckAnchorIssueDate && in.VogIssueDate != nil {
				anchor = *in.VogIssueDate
			}
			validUntil = vogDueAtFor(anchor, memberType, settings)
		}

		var referenceHash []byte
		if len(hashKey) > 0 && in.Reference != "" {
			referenceHash = keyedHash(hashKey, in.Reference)
		}

		coveredCodes, missingCodes := in.CoveredCodes, in.MissingCodes
		if coveredCodes == nil {
			coveredCodes = []string{}
		}
		if missingCodes == nil {
			missingCodes = []string{}
		}

		const insert = `INSERT INTO member_screenings
			(organization_id, user_id, method, result, checked_at, vog_issue_date, valid_until,
			 covered_codes, missing_codes, reference_hash, checked_by, checked_by_user_id, gaav_response_code)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`
		if _, err := q.Exec(ctx, insert, orgID, userID, string(in.Method), string(in.Result), checkedAt, in.VogIssueDate, validUntil,
			coveredCodes, missingCodes, referenceHash, in.CheckedBy, in.CheckedByUserID, in.GAAVResponseCode); err != nil {
			return fmt.Errorf("organization: record screening user %s org %s: %w", userID, orgID, err)
		}

		screeningCovered := coveredCodes
		if in.Result != vog.ResultValid {
			screeningCovered = []string{}
		}
		const update = `UPDATE memberships SET
				vog_last_result = $3,
				vog_valid_until = $4,
				vog_covered_codes = $5,
				vog_requested_at = NULL,
				vog_requested_by = NULL,
				vog_last_reminder_at = NULL,
				vog_reminder_count = 0
			WHERE organization_id = $1 AND user_id = $2`
		if _, err := q.Exec(ctx, update, orgID, userID, string(in.Result), validUntil, screeningCovered); err != nil {
			return fmt.Errorf("organization: update membership screening state user %s org %s: %w", userID, orgID, err)
		}

		return s.audit.Record(ctx, q, screeningAuditAction(in.Result),
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"method": in.Method, "result": in.Result, "checkedBy": in.CheckedBy,
				"missingCodes": missingCodes, "gaavResponseCode": in.GAAVResponseCode,
			}))
	})
}

func screeningAuditAction(result vog.Result) string {
	switch result {
	case vog.ResultValid:
		return audit.MembershipVogChecked
	case vog.ResultMismatch:
		return audit.MembershipVogMismatch
	case vog.ResultInsufficientScope:
		return audit.MembershipVogInsufficientScope
	default:
		return audit.MembershipVogRejected
	}
}

// ListScreeningHistory returns a member's screening attempts, most recent
// first.
func (s *Store) ListScreeningHistory(ctx context.Context, orgID, userID uuid.UUID) ([]ScreeningRecord, error) {
	const q = `
		SELECT id, method, result, checked_at, vog_issue_date, valid_until, covered_codes, missing_codes,
		       checked_by, checked_by_user_id
		FROM member_screenings
		WHERE organization_id = $1 AND user_id = $2
		ORDER BY checked_at DESC`
	rows, err := s.db.Query(ctx, q, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("organization: list screening history user %s org %s: %w", userID, orgID, err)
	}
	defer rows.Close()

	history := []ScreeningRecord{}
	for rows.Next() {
		var r ScreeningRecord
		if err := rows.Scan(&r.ID, &r.Method, &r.Result, &r.CheckedAt, &r.VogIssueDate, &r.ValidUntil,
			&r.CoveredCodes, &r.MissingCodes, &r.CheckedBy, &r.CheckedByUserID); err != nil {
			return nil, fmt.Errorf("organization: list screening history scan: %w", err)
		}
		history = append(history, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list screening history rows: %w", err)
	}
	return history, nil
}

// RequestedVogMember is one member an admin's "request VOG" reached.
type RequestedVogMember struct {
	UserID uuid.UUID
	Email  string
}

// RequestVog sets vog_requested_at/by for one or more members (single or bulk,
// mirroring RequestIdentification) and audits one event per member, all in one
// transaction. A user id that is not a member of orgID is silently skipped, the
// same guard RequestIdentification uses.
func (s *Store) RequestVog(ctx context.Context, orgID uuid.UUID, userIDs []uuid.UUID, requestedBy uuid.UUID, reason string) ([]RequestedVogMember, error) {
	var out []RequestedVogMember
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		now := time.Now()
		for _, userID := range userIDs {
			const update = `
				UPDATE memberships SET vog_requested_at = $3, vog_requested_by = $4
				WHERE organization_id = $1 AND user_id = $2
				RETURNING (SELECT email FROM users WHERE id = $2)`
			var email string
			err := q.QueryRow(ctx, update, orgID, userID, now, requestedBy).Scan(&email)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return fmt.Errorf("organization: request vog user %s org %s: %w", userID, orgID, err)
			}

			if err := s.audit.Record(ctx, q, audit.MembershipVogRequested,
				audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
				audit.Created(map[string]any{"email": email, "reason": nullIfEmpty(reason)})); err != nil {
				return err
			}
			out = append(out, RequestedVogMember{UserID: userID, Email: email})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
