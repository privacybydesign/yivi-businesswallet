//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

// configureOnboarding creates a natural-person template that binds its required
// "email" attribute to the member.email source, then adds it to the org's
// onboarding auto-issue set. It returns the template id.
func (e *testEnv) configureOnboarding(orgID uuid.UUID) uuid.UUID {
	e.t.Helper()
	store := attestation.NewStore(e.pool, audit.NewDBRecorder())
	ctx := context.Background()

	schema, err := store.CreateSchema(ctx, orgID, attestation.Schema{
		VCT:                "nl.acme.employee",
		DisplayName:        "Employee",
		CredentialConfigID: "EmailCredentialSdJwt",
		SubjectType:        attestation.SubjectNaturalPerson,
		Attributes: []attestation.AttributeDef{
			{Key: "email", Label: "E-mail", Type: "string", Required: true},
		},
	})
	if err != nil {
		e.t.Fatalf("create schema: %v", err)
	}
	tmpl, err := store.CreateTemplate(ctx, orgID, attestation.Template{
		SchemaID:         schema.ID,
		Name:             "Employee",
		AttributeSources: map[string]string{"email": attestation.SourceMemberEmail},
	})
	if err != nil {
		e.t.Fatalf("create template: %v", err)
	}
	if _, err := store.SetOnboardingAttestations(ctx, orgID, []uuid.UUID{tmpl.ID}); err != nil {
		e.t.Fatalf("set onboarding: %v", err)
	}
	return tmpl.ID
}

// issuedForRecipient returns the count of ledger rows and the email attribute of
// the most recent one, for a given recipient in an org.
func (e *testEnv) issuedForRecipient(orgID uuid.UUID, ref string) (int, string) {
	e.t.Helper()
	var (
		n     int
		email string
	)
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*), coalesce(max(attributes->>'email'), '')
		 FROM issued_attestations WHERE organization_id = $1 AND recipient_ref = $2`,
		orgID, ref,
	).Scan(&n, &email); err != nil {
		e.t.Fatalf("issued count: %v", err)
	}
	return n, email
}

// TestAcceptAutoIssuesOnboardingAttestations: accepting an invitation issues the
// org's configured onboarding template to the member, with the email attribute
// resolved from the member's own e-mail.
func TestAcceptAutoIssuesOnboardingAttestations(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	env.configureOnboarding(orgID)

	token := env.createInvitation(orgID, "newbie@example.test", "New", "Comer")
	env.discloses("newbie@example.test", "New", "Comer")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}

	n, email := env.issuedForRecipient(orgID, "newbie@example.test")
	if n != 1 {
		t.Fatalf("issued attestations = %d, want 1", n)
	}
	if email != "newbie@example.test" {
		t.Errorf("resolved email attribute = %q, want the member e-mail", email)
	}
	// The issuance is audited on the ledger like any other.
	if c := env.auditCount(orgID, audit.AttestationIssued); c != 1 {
		t.Errorf("attestation.issued events = %d, want 1", c)
	}
}

// TestAcceptWithNoOnboardingSetIssuesNothing: with no configured set, accepting
// an invitation issues nothing (and still succeeds).
func TestAcceptWithNoOnboardingSetIssuesNothing(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")

	token := env.createInvitation(orgID, "newbie@example.test", "New", "Comer")
	env.discloses("newbie@example.test", "New", "Comer")

	resp := env.acceptInvite(token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("accept = %d, want 200", resp.StatusCode)
	}

	if n, _ := env.issuedForRecipient(orgID, "newbie@example.test"); n != 0 {
		t.Fatalf("issued attestations = %d, want 0", n)
	}
}
