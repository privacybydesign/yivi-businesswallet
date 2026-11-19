package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/identity"
)

const reverifyTokenBytes = 32

func newReverifyToken() (string, [sha256.Size]byte, error) {
	b := make([]byte, reverifyTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("organization: reverify token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(b)
	return raw, sha256.Sum256([]byte(raw)), nil
}

func hashReverifyToken(raw string) [sha256.Size]byte {
	return sha256.Sum256([]byte(raw))
}

// EnsureReverifyToken mints or rotates the one live re-identification token for
// a membership. There is exactly one mechanism regardless of entry point: an
// admin's "request identification", the scheduler's reminder mail and the
// member's own in-app banner all call this and each rotates the link, so an
// older copy of the link (e.g. from an earlier reminder) stops working the
// moment a fresher one is issued — the same posture as invitation resend.
func (s *Store) EnsureReverifyToken(ctx context.Context, orgID, userID uuid.UUID) (string, time.Time, error) {
	var raw string
	var expiresAt time.Time
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		var err error
		raw, expiresAt, err = ensureReverifyTokenTx(ctx, q, orgID, userID)
		return err
	})
	return raw, expiresAt, err
}

func ensureReverifyTokenTx(ctx context.Context, q database.Querier, orgID, userID uuid.UUID) (string, time.Time, error) {
	rawToken, tokenHash, err := newReverifyToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().Add(DefaultReidentifyTokenTTL)
	const upsert = `
		INSERT INTO identity_reverify_tokens (user_id, organization_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, organization_id) DO UPDATE SET
			token_hash = EXCLUDED.token_hash, expires_at = EXCLUDED.expires_at, created_at = now()`
	if _, err := q.Exec(ctx, upsert, userID, orgID, tokenHash[:], expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("organization: ensure reverify token user %s org %s: %w", userID, orgID, err)
	}
	return rawToken, expiresAt, nil
}

// ReverifyContext is what a re-identification token resolves to.
type ReverifyContext struct {
	OrganizationID   uuid.UUID
	OrganizationName string
	OrganizationSlug string
	UserID           uuid.UUID
	Email            string
	// StoredName is the member's current verified name, matched against the
	// re-identification disclosure the same way invite-accept matches a
	// disclosure against the invitation (identity.Reconcile).
	StoredName identity.Name
	MemberType string
}

// ReverifyTokenLookup resolves a raw re-identification token to its member
// context, or ErrReverifyTokenNotFound for an unknown or expired token.
func (s *Store) ReverifyTokenLookup(ctx context.Context, rawToken string) (ReverifyContext, error) {
	hash := hashReverifyToken(rawToken)
	const q = `
		SELECT t.organization_id, o.name, o.slug, t.user_id, u.email, u.given_names, u.last_name, m.member_type
		FROM identity_reverify_tokens t
		JOIN organizations o ON o.id = t.organization_id
		JOIN users u ON u.id = t.user_id
		JOIN memberships m ON m.user_id = t.user_id AND m.organization_id = t.organization_id
		WHERE t.token_hash = $1 AND t.expires_at > now()`
	var rc ReverifyContext
	var givenNames, lastName string
	err := s.db.QueryRow(ctx, q, hash[:]).Scan(
		&rc.OrganizationID, &rc.OrganizationName, &rc.OrganizationSlug, &rc.UserID, &rc.Email,
		&givenNames, &lastName, &rc.MemberType)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReverifyContext{}, ErrReverifyTokenNotFound
	}
	if err != nil {
		return ReverifyContext{}, fmt.Errorf("organization: reverify token lookup: %w", err)
	}
	rc.StoredName = identity.Name{GivenNames: givenNames, LastName: lastName}
	return rc, nil
}

// RecordReverifyRejected logs a re-identification attempt that did not pass
// (email/name mismatch, or a disclosed credential older than the org's
// freshness policy) so the rejection leaves a trail, mirroring
// RecordRejectedAccept. Not a state change, so it writes outside a transaction.
func (s *Store) RecordReverifyRejected(ctx context.Context, orgID, userID uuid.UUID, email, reason string) error {
	return s.audit.Record(ctx, s.db, audit.MembershipIdentityReverifyRejected,
		audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
		audit.Created(map[string]any{"email": email, "reason": reason}))
}

// CompleteReverification records a successful re-identification: sets
// identity_verified_at to now, refreshes date of birth and phone (best-effort),
// recomputes identity_due_at under the org's current policy, clears any
// outstanding admin request and the reminder cadence counters, retires the
// token that was used, and audits membership.identity_reverified — all in one
// transaction.
func (s *Store) CompleteReverification(ctx context.Context, orgID, userID uuid.UUID, disclosed identity.Name, phone, dateOfBirth string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		settings, err := identitySettingsTx(ctx, q, orgID)
		if err != nil {
			return err
		}

		var memberType string
		var oldVerifiedAt *time.Time
		err = q.QueryRow(ctx,
			`SELECT member_type, identity_verified_at FROM memberships WHERE organization_id = $1 AND user_id = $2 FOR UPDATE`,
			orgID, userID).Scan(&memberType, &oldVerifiedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotMember
		}
		if err != nil {
			return fmt.Errorf("organization: read membership for reverify user %s org %s: %w", userID, orgID, err)
		}

		verifiedNow := time.Now()
		const update = `
			UPDATE memberships SET
				identity_verified_at = $3,
				date_of_birth = COALESCE($4, date_of_birth),
				phone = COALESCE($5, phone),
				identity_due_at = $6,
				identity_requested_at = NULL,
				identity_requested_by = NULL,
				identity_last_reminder_at = NULL,
				identity_reminder_count = 0
			WHERE organization_id = $1 AND user_id = $2`
		if _, err := q.Exec(ctx, update, orgID, userID, verifiedNow, parseDateOfBirth(dateOfBirth), nullIfEmpty(phone),
			dueAtFor(&verifiedNow, memberType, settings)); err != nil {
			return fmt.Errorf("organization: complete reverification user %s org %s: %w", userID, orgID, err)
		}

		if _, err := q.Exec(ctx, `DELETE FROM identity_reverify_tokens WHERE organization_id = $1 AND user_id = $2`, orgID, userID); err != nil {
			return fmt.Errorf("organization: retire reverify token user %s org %s: %w", userID, orgID, err)
		}

		return s.audit.Record(ctx, q, audit.MembershipIdentityReverified,
			audit.Target{Type: audit.TargetMembership, ID: userID.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"identityVerifiedAt": oldVerifiedAt},
				map[string]any{"identityVerifiedAt": verifiedNow, "givenNames": disclosed.GivenNames, "lastName": disclosed.LastName}))
	})
}
