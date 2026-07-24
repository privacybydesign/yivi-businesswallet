package attestation

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ListOnboardingAttestations returns an organization's onboarding auto-issue set
// — the templates issued to a new member on accept — enriched with the schema
// identity for the invite-screen chips, ordered as the admin arranged them.
func (s *Store) ListOnboardingAttestations(ctx context.Context, orgID uuid.UUID) ([]OnboardingAttestation, error) {
	const query = `SELECT o.template_id, t.name, s.vct, s.display_name, s.subject_type, o.position
		FROM org_onboarding_attestations o
		JOIN attestation_templates t ON t.id = o.template_id
		JOIN attestation_schemas s ON s.id = t.schema_id
		WHERE o.organization_id = $1
		ORDER BY o.position, o.created_at`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: list onboarding attestations org %s: %w", orgID, err)
	}
	defer rows.Close()

	out := []OnboardingAttestation{}
	for rows.Next() {
		var a OnboardingAttestation
		if err := rows.Scan(&a.TemplateID, &a.Name, &a.VCT, &a.DisplayName, &a.SubjectType, &a.Position); err != nil {
			return nil, fmt.Errorf("attestation: list onboarding attestations scan: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: list onboarding attestations rows: %w", err)
	}
	return out, nil
}

// OnboardingTemplateIDs returns just the template ids in the onboarding set, in
// order. It backs the accept path, which issues each configured template to the
// new member.
func (s *Store) OnboardingTemplateIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	const query = `SELECT template_id FROM org_onboarding_attestations
		WHERE organization_id = $1 ORDER BY position, created_at`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("attestation: onboarding template ids org %s: %w", orgID, err)
	}
	defer rows.Close()

	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("attestation: onboarding template ids scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("attestation: onboarding template ids rows: %w", err)
	}
	return ids, nil
}

// SetOnboardingAttestations replaces an organization's onboarding auto-issue set
// with the given templates (in the given order) and audits, in one transaction.
// Every template must belong to the organization and target a natural person
// (onboarding issues to the accepting member); otherwise the whole change is
// rejected. Passing an empty list clears the set. Duplicate ids collapse to a
// single entry keyed by their first position.
func (s *Store) SetOnboardingAttestations(ctx context.Context, orgID uuid.UUID, templateIDs []uuid.UUID) ([]OnboardingAttestation, error) {
	ordered := dedupeUUIDs(templateIDs)
	if err := s.checkOnboardingTemplates(ctx, orgID, ordered); err != nil {
		return nil, err
	}

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `DELETE FROM org_onboarding_attestations WHERE organization_id = $1`, orgID); err != nil {
			return fmt.Errorf("attestation: clear onboarding set org %s: %w", orgID, err)
		}
		for position, id := range ordered {
			const insert = `INSERT INTO org_onboarding_attestations (organization_id, template_id, position)
				VALUES ($1, $2, $3)`
			if _, err := q.Exec(ctx, insert, orgID, id, position); err != nil {
				return fmt.Errorf("attestation: insert onboarding template %s org %s: %w", id, orgID, err)
			}
		}
		templateIDStrings := make([]string, len(ordered))
		for i, id := range ordered {
			templateIDStrings[i] = id.String()
		}
		return s.audit.Record(ctx, q, audit.OnboardingSettingsUpdated,
			audit.Target{Type: audit.TargetOnboardingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"templateIds": templateIDStrings}))
	})
	if err != nil {
		return nil, err
	}
	return s.ListOnboardingAttestations(ctx, orgID)
}

// checkOnboardingTemplates verifies every id belongs to the organization and
// targets a natural person. It runs before the transaction so a bad request is
// rejected without touching the set.
func (s *Store) checkOnboardingTemplates(ctx context.Context, orgID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	const query = `SELECT t.id, s.subject_type
		FROM attestation_templates t
		JOIN attestation_schemas s ON s.id = t.schema_id
		WHERE t.organization_id = $1 AND t.id = ANY($2)`
	rows, err := s.db.Query(ctx, query, orgID, ids)
	if err != nil {
		return fmt.Errorf("attestation: check onboarding templates org %s: %w", orgID, err)
	}
	defer rows.Close()

	subjects := make(map[uuid.UUID]string, len(ids))
	for rows.Next() {
		var (
			id      uuid.UUID
			subject string
		)
		if err := rows.Scan(&id, &subject); err != nil {
			return fmt.Errorf("attestation: check onboarding templates scan: %w", err)
		}
		subjects[id] = subject
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("attestation: check onboarding templates rows: %w", err)
	}

	for _, id := range ids {
		subject, ok := subjects[id]
		if !ok {
			return ErrTemplateNotFound
		}
		if subject != SubjectNaturalPerson {
			return ErrOnboardingSubject
		}
	}
	return nil
}

// dedupeUUIDs returns the ids with duplicates removed, preserving first-seen order.
func dedupeUUIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
