package organization

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const mandateDepartmentFK = "mandates_department_fkey"

// mandateActive is the real-time activity predicate, evaluated against the
// database clock on every read rather than against a stored status column
// (Annex §12(3)(b): expired authorisations are rejected in real time).
const mandateActive = `revoked_at IS NULL AND valid_from <= now() AND (valid_until IS NULL OR valid_until > now())`

// representationAuthorityJointly is the wallet_representations.authority value
// for a director registered as only able to bind the company together with
// another one.
const representationAuthorityJointly = "jointly"

// claimedBestuurder selects the caller's register-backed representation rows: a
// claimed, unrevoked, in-window `bestuurder`. wallet_representations is written by
// the wallet slice from the KVK registration attestation and is read-only here —
// the same direction as the wallet slice writing memberships during bootstrap.
const claimedBestuurder = `
	FROM wallet_representations r
	WHERE r.organization_id = $1 AND r.claimed_by_user_id = $2
	  AND r.kind = 'bestuurder' AND r.revoked_at IS NULL
	  AND (r.valid_from IS NULL OR r.valid_from <= now())
	  AND (r.valid_until IS NULL OR r.valid_until > now())`

// legalRepresentativeExists is the Axis-A root.
const legalRepresentativeExists = `EXISTS (SELECT 1` + claimedBestuurder + `)`

// jointRepresentativeExists narrows the same root to a director registered
// `jointly`. legalRepresentativeExists ignores the column on purpose, so such a
// director is honoured here as if they could act alone (.ai/features/mandates.md
// §7); this predicate is what lets the register screen say so instead of offering
// a grant flow the layer cannot yet get right.
const jointRepresentativeExists = `EXISTS (SELECT 1` + claimedBestuurder +
	` AND r.authority = '` + representationAuthorityJointly + `')`

// ResolveAuthority reads the caller's basis of authority in one org. Authorize
// runs it on every org-scoped request, so it is one round trip: the
// representation lookup and the three mandate tallies share a single row.
func (s *Store) ResolveAuthority(ctx context.Context, orgID, userID uuid.UUID) (Authority, error) {
	const query = `
		WITH held AS (
			SELECT type, scope, valid_from, valid_until, revoked_at
			FROM mandates WHERE organization_id = $1 AND grantee_user_id = $2
		)
		SELECT ` + legalRepresentativeExists + `,
		       (SELECT count(*) FROM held WHERE valid_from <= now()),
		       (SELECT count(*) FROM held WHERE ` + mandateActive + ` AND scope = '` + MandateScopeOrganization + `'),
		       (SELECT count(*) FROM held WHERE ` + mandateActive + ` AND scope = '` + MandateScopeOrganization + `'
		                                    AND type = '` + MandateFull + `')`

	var a Authority
	var orgWide, full int
	if err := s.db.QueryRow(ctx, query, orgID, userID).
		Scan(&a.LegalRepresentative, &a.Granted, &orgWide, &full); err != nil {
		return Authority{}, fmt.Errorf("organization: resolve authority user %s org %s: %w", userID, orgID, err)
	}
	a.Mandated = orgWide > 0
	a.FullMandate = full > 0
	return a, nil
}

// HasJointRepresentation reports that the caller's register-backed authority is a
// joint one: a `bestuurder` who under Dutch law may only bind the company
// together with another director. ResolveAuthority deliberately ignores the
// column, so the grant path would honour them as a sole representative; the
// register screen reads this to withhold the grant flow rather than record a
// grant no single director could make. Closing the gap properly means co-signing
// (.ai/features/mandates.md §7).
func (s *Store) HasJointRepresentation(ctx context.Context, orgID, userID uuid.UUID) (bool, error) {
	var joint bool
	if err := s.db.QueryRow(ctx, `SELECT `+jointRepresentativeExists, orgID, userID).Scan(&joint); err != nil {
		return false, fmt.Errorf("organization: check joint representation %s org %s: %w", userID, orgID, err)
	}
	return joint, nil
}

const mandateColumns = `
	m.id, m.organization_id, m.type,
	m.grantor_user_id, g.given_names || ' ' || g.last_name,
	m.grantee_user_id, e.given_names || ' ' || e.last_name,
	m.scope, m.scope_department_id, d.name,
	m.parent_mandate_id, m.valid_from, m.valid_until,
	m.revoked_at, m.revoked_by_user_id, m.revocation_reason, m.created_at`

const mandateJoins = `
	FROM mandates m
	JOIN users e ON e.id = m.grantee_user_id
	LEFT JOIN users g ON g.id = m.grantor_user_id
	LEFT JOIN departments d ON d.id = m.scope_department_id`

func scanMandate(row rowScanner, now time.Time) (Mandate, error) {
	var m Mandate
	if err := row.Scan(
		&m.ID, &m.OrganizationID, &m.Type,
		&m.GrantorUserID, &m.GrantorName,
		&m.GranteeUserID, &m.GranteeName,
		&m.Scope, &m.ScopeDepartmentID, &m.ScopeDepartmentName,
		&m.ParentMandateID, &m.ValidFrom, &m.ValidUntil,
		&m.RevokedAt, &m.RevokedByUserID, &m.RevocationReason, &m.CreatedAt,
	); err != nil {
		return Mandate{}, err
	}
	m.Status = mandateStatus(m, now)
	return m, nil
}

// ListMandates returns the organization's whole mandate register — active,
// pending, revoked and expired alike. The register is the audit surface: a
// revoked mandate that vanished from the list would hide the revocation.
func (s *Store) ListMandates(ctx context.Context, orgID uuid.UUID) ([]Mandate, error) {
	const query = `SELECT ` + mandateColumns + mandateJoins + `
		WHERE m.organization_id = $1
		ORDER BY m.created_at DESC`

	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("organization: list mandates org %s: %w", orgID, err)
	}
	defer rows.Close()

	now := time.Now()
	mandates := []Mandate{}
	for rows.Next() {
		m, err := scanMandate(rows, now)
		if err != nil {
			return nil, fmt.Errorf("organization: list mandates scan: %w", err)
		}
		mandates = append(mandates, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization: list mandates rows org %s: %w", orgID, err)
	}
	return mandates, nil
}

// GrantMandate records one grant. The grantor's authority is re-derived here
// rather than taken from the request or from the middleware's context: the
// middleware's check is the cheap gate, this one is the decision of record, and
// it runs inside the same transaction as the insert.
//
// A grantor who is not a legal representative is delegating, so the grant is cut
// from a mandate they hold and clamped to it (Annex §12(3)(b)). When they did not
// name one, their own active org-wide full mandate is used.
func (s *Store) GrantMandate(ctx context.Context, orgID, grantorUserID uuid.UUID, req MandateGrant) (Mandate, error) {
	if err := validateGrant(&req); err != nil {
		return Mandate{}, err
	}

	var granted Mandate
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const memberCheck = `SELECT 1 FROM memberships WHERE organization_id = $1 AND user_id = $2`
		var one int
		switch err := q.QueryRow(ctx, memberCheck, orgID, req.GranteeUserID).Scan(&one); {
		case errors.Is(err, pgx.ErrNoRows):
			return ErrGranteeNotMember
		case err != nil:
			return fmt.Errorf("organization: check grantee membership %s org %s: %w", req.GranteeUserID, orgID, err)
		}

		legalRep, err := isLegalRepresentative(ctx, q, orgID, grantorUserID)
		if err != nil {
			return err
		}

		parentID := req.ParentMandateID
		if parentID == nil && !legalRep {
			// Not a register-backed representative, so they can only pass on what
			// they hold. Find it rather than making the caller name their own
			// mandate on every request.
			parentID, err = ownFullMandate(ctx, q, orgID, grantorUserID)
			if err != nil {
				return err
			}
		}
		if parentID == nil {
			if !legalRep {
				return ErrNotMandateAuthority
			}
		} else {
			parent, err := lockMandate(ctx, q, orgID, *parentID)
			if err != nil {
				return err
			}
			// The grantor must hold the mandate they are cutting from. A legal
			// representative is exempt: their authority is the register's, not a
			// delegation, so they may re-cut anyone's mandate.
			if !legalRep && parent.GranteeUserID != grantorUserID {
				return ErrNotMandateAuthority
			}
			req, err = clampToParent(req, parent, time.Now())
			if err != nil {
				return err
			}
			req.ParentMandateID = parentID
		}

		const insert = `
			INSERT INTO mandates
				(organization_id, type, grantor_user_id, grantee_user_id,
				 scope, scope_department_id, parent_mandate_id, valid_from, valid_until)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, valid_from, created_at,
			          (SELECT given_names || ' ' || last_name FROM users WHERE id = $4),
			          (SELECT name FROM departments WHERE id = $6 AND organization_id = $1)`
		err = q.QueryRow(ctx, insert,
			orgID, req.Type, grantorUserID, req.GranteeUserID,
			req.Scope, req.ScopeDepartmentID, req.ParentMandateID, req.ValidFrom, req.ValidUntil,
		).Scan(&granted.ID, &granted.ValidFrom, &granted.CreatedAt, &granted.GranteeName, &granted.ScopeDepartmentName)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == mandateDepartmentFK {
			return ErrDepartmentNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: grant mandate to %s org %s: %w", req.GranteeUserID, orgID, err)
		}

		granted.OrganizationID = orgID
		granted.Type = req.Type
		granted.GrantorUserID = &grantorUserID
		granted.GranteeUserID = req.GranteeUserID
		granted.Scope = req.Scope
		granted.ScopeDepartmentID = req.ScopeDepartmentID
		granted.ParentMandateID = req.ParentMandateID
		granted.ValidUntil = req.ValidUntil
		granted.Status = mandateStatus(granted, time.Now())

		return s.audit.Record(ctx, q, audit.MandateGranted,
			audit.Target{Type: audit.TargetMandate, ID: granted.ID.String(), OrgID: &orgID},
			audit.Created(mandateMetadata(granted)))
	})
	if err != nil {
		return Mandate{}, err
	}
	return granted, nil
}

// RevokeMandate ends a mandate and everything delegated from it — a delegate
// cannot outlive the authority it was cut from. effectiveAt nil revokes at once;
// a future effectiveAt closes the validity window on that date instead, so the
// mandate stays active until then and expires on its own.
//
// It returns every mandate the revocation touched, the target first, so the caller
// can report how far the cascade reached.
func (s *Store) RevokeMandate(ctx context.Context, orgID, mandateID, revokedBy uuid.UUID, effectiveAt *time.Time, reason string) ([]Mandate, error) {
	if effectiveAt != nil && !effectiveAt.After(time.Now()) {
		return nil, ErrMandateEffectiveInPast
	}

	var touched []Mandate
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		target, err := lockMandate(ctx, q, orgID, mandateID)
		if err != nil {
			return err
		}
		// Only a mandate that still has a life ahead of it can be ended. Revoking
		// one that is already revoked or expired would stamp revoked_at = now() on
		// a window that closed months ago and relabel it in the register this
		// layer exists to keep honest.
		switch mandateStatus(target, time.Now()) {
		case MandateStatusPending, MandateStatusActive:
		default:
			return ErrMandateInactive
		}

		legalRep, err := isLegalRepresentative(ctx, q, orgID, revokedBy)
		if err != nil {
			return err
		}
		// The register-backed representative acts for the owner and may revoke
		// anything (Art 5(1)(j)); anyone else may only take back what they gave.
		if !legalRep && (target.GrantorUserID == nil || *target.GrantorUserID != revokedBy) {
			return ErrNotMandateAuthority
		}

		var reasonArg *string
		if reason != "" {
			reasonArg = &reason
		}

		// The subtree walk is a plain recursive descent: parent_mandate_id always
		// points at a row that already existed, so the chain cannot contain a cycle.
		//
		// A descendant whose window has not opened yet cannot have it trimmed —
		// valid_until would land at or before valid_from and trip
		// mandates_window_check — so it is revoked outright instead. It could never
		// have become effective anyway.
		//
		// A descendant that has already expired is left out, for the same reason the
		// target has to be pending or active: its authority ended on its own date,
		// and stamping revoked_at = now() on it would rewrite how a historical
		// mandate reads.
		const revoke = `
			WITH RECURSIVE subtree AS (
				SELECT id FROM mandates WHERE id = $1 AND organization_id = $2
				UNION ALL
				SELECT m.id FROM mandates m JOIN subtree s ON m.parent_mandate_id = s.id
			)
			UPDATE mandates m SET
				valid_until = CASE WHEN $3::timestamptz IS NULL OR m.valid_from >= $3 THEN m.valid_until
				                   ELSE LEAST(COALESCE(m.valid_until, $3), $3) END,
				revoked_at  = CASE WHEN $3::timestamptz IS NULL OR m.valid_from >= $3 THEN now()
				                   ELSE m.revoked_at END,
				revoked_by_user_id = $4,
				revocation_reason  = $5,
				updated_at = now()
			FROM subtree s
			WHERE m.id = s.id AND m.revoked_at IS NULL
			  AND (m.valid_until IS NULL OR m.valid_until > now())
			RETURNING ` + mandateReturning

		// RETURNING takes no ORDER BY, so the target-first order the caller is
		// promised is imposed here.
		rows, err := q.Query(ctx, revoke, mandateID, orgID, effectiveAt, revokedBy, reasonArg)
		if err != nil {
			return fmt.Errorf("organization: revoke mandate %s org %s: %w", mandateID, orgID, err)
		}
		now := time.Now()
		for rows.Next() {
			m, serr := scanMandate(rows, now)
			if serr != nil {
				rows.Close()
				return fmt.Errorf("organization: revoke mandate scan: %w", serr)
			}
			touched = append(touched, m)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("organization: revoke mandate rows %s: %w", mandateID, err)
		}
		slices.SortStableFunc(touched, func(a, b Mandate) int {
			switch {
			case a.ID == mandateID:
				return -1
			case b.ID == mandateID:
				return 1
			default:
				return a.CreatedAt.Compare(b.CreatedAt)
			}
		})

		basis := mandateBasisGrantor
		if legalRep {
			basis = mandateBasisLegalRepresentative
		}
		for _, m := range touched {
			meta := mandateMetadata(m)
			meta["revokedBy"] = basis
			if m.ID != mandateID {
				meta["cascadedFrom"] = mandateID.String()
			}
			if reasonArg != nil {
				meta["reason"] = reason
			}
			// Branch on what actually happened to this row, not on what was asked:
			// the cascade revokes a descendant outright when its window had not
			// opened by the effective date, so effectiveAt alone does not say which.
			// m comes from UPDATE ... RETURNING and the statement only touches rows
			// whose revoked_at was NULL, so a non-nil RevokedAt here means the
			// now() branch fired for this row.
			envelope := audit.Deleted(meta)
			if effectiveAt != nil && m.RevokedAt == nil {
				// Not gone yet: the window was closed on a date, so the mandate
				// stays active until then and expires on its own.
				meta["effectiveAt"] = effectiveAt.UTC()
				envelope = audit.Updated(nil, meta)
			}
			if err := s.audit.Record(ctx, q, audit.MandateRevoked,
				audit.Target{Type: audit.TargetMandate, ID: m.ID.String(), OrgID: &orgID},
				envelope); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return touched, nil
}

// mandateReturning is mandateColumns against the UPDATE's target row, which has
// to reach the joined names through subqueries — an UPDATE ... RETURNING cannot
// join.
const mandateReturning = `
	m.id, m.organization_id, m.type,
	m.grantor_user_id, (SELECT given_names || ' ' || last_name FROM users WHERE id = m.grantor_user_id),
	m.grantee_user_id, (SELECT given_names || ' ' || last_name FROM users WHERE id = m.grantee_user_id),
	m.scope, m.scope_department_id, (SELECT name FROM departments WHERE id = m.scope_department_id),
	m.parent_mandate_id, m.valid_from, m.valid_until,
	m.revoked_at, m.revoked_by_user_id, m.revocation_reason, m.created_at`

// lockMandate loads one mandate of an org for update, so a concurrent revoke and
// delegate cannot both decide against the same pre-state.
func lockMandate(ctx context.Context, q database.Querier, orgID, mandateID uuid.UUID) (Mandate, error) {
	const query = `SELECT ` + mandateReturning + `
		FROM mandates m WHERE m.id = $1 AND m.organization_id = $2 FOR UPDATE OF m`
	m, err := scanMandate(q.QueryRow(ctx, query, mandateID, orgID), time.Now())
	if errors.Is(err, pgx.ErrNoRows) {
		return Mandate{}, ErrMandateNotFound
	}
	if err != nil {
		return Mandate{}, fmt.Errorf("organization: load mandate %s org %s: %w", mandateID, orgID, err)
	}
	return m, nil
}

func isLegalRepresentative(ctx context.Context, q database.Querier, orgID, userID uuid.UUID) (bool, error) {
	var ok bool
	if err := q.QueryRow(ctx, `SELECT `+legalRepresentativeExists, orgID, userID).Scan(&ok); err != nil {
		return false, fmt.Errorf("organization: check legal representative %s org %s: %w", userID, orgID, err)
	}
	return ok, nil
}

// ownFullMandate returns the caller's active org-wide full mandate, the only
// mandate they may delegate from without naming one. Nil when they hold none.
func ownFullMandate(ctx context.Context, q database.Querier, orgID, userID uuid.UUID) (*uuid.UUID, error) {
	const query = `SELECT id FROM mandates
		WHERE organization_id = $1 AND grantee_user_id = $2
		  AND type = '` + MandateFull + `' AND scope = '` + MandateScopeOrganization + `'
		  AND ` + mandateActive + `
		ORDER BY valid_from LIMIT 1`
	var id uuid.UUID
	switch err := q.QueryRow(ctx, query, orgID, userID).Scan(&id); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("organization: find own full mandate %s org %s: %w", userID, orgID, err)
	}
	return &id, nil
}

// validateGrant rejects a request the mandate model has no shape for, and fills
// the one default a caller may leave out. Everything here is also a database
// CHECK; catching it in Go is what turns it into a 400 with a name on it instead
// of a constraint violation.
func validateGrant(req *MandateGrant) error {
	if req.Type != MandateFull && req.Type != MandateAdministrative {
		return ErrMandateType
	}
	switch req.Scope {
	case MandateScopeOrganization:
		if req.ScopeDepartmentID != nil {
			return ErrMandateScope
		}
	case MandateScopeDepartment:
		if req.ScopeDepartmentID == nil {
			return ErrMandateScope
		}
	default:
		return ErrMandateScope
	}
	if req.ValidFrom.IsZero() {
		req.ValidFrom = time.Now()
	}
	if req.ValidUntil != nil && !req.ValidUntil.After(req.ValidFrom) {
		return ErrMandateWindow
	}
	return nil
}

// mandateMetadata is the audit envelope's payload: readable values, no ids the
// reader would have to resolve, per the auditing convention. `basis` is the
// footing the mandate itself stands on (Annex §12(1)(c)) — a root grant can only
// have come from the register-backed representative, anything with a parent was
// cut from it.
func mandateMetadata(m Mandate) map[string]any {
	basis := mandateBasisLegalRepresentative
	if m.ParentMandateID != nil {
		basis = mandateBasisDelegated
	}
	meta := map[string]any{
		"mandateType": m.Type,
		"grantee":     m.GranteeName,
		"scope":       m.Scope,
		"basis":       basis,
		"validFrom":   m.ValidFrom.UTC(),
	}
	if m.ScopeDepartmentName != nil {
		meta["department"] = *m.ScopeDepartmentName
	}
	if m.ValidUntil != nil {
		meta["validUntil"] = m.ValidUntil.UTC()
	}
	return meta
}

// The basis of authority an acting subject used (Annex §12(1)(c)), so the log
// says on what footing a grant was made and a revocation ordered.
const (
	mandateBasisLegalRepresentative = "legal_representative"
	mandateBasisDelegated           = "delegated"
	mandateBasisGrantor             = "grantor"
)
