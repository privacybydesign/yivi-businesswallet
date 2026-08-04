//go:build integration

package attestation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

func TestOnboardingSetRoundTripAndReorder(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	first := personTemplate(t, ctx, e.store, e.orgID)

	// A second natural-person template so we can assert ordering.
	schema, err := e.store.CreateSchema(ctx, e.orgID, attestation.Schema{
		VCT:                "nl.caesar.contractor",
		DisplayName:        "Contractor",
		CredentialConfigID: "EmailCredentialSdJwt",
		SubjectType:        attestation.SubjectNaturalPerson,
		Attributes:         []attestation.AttributeDef{{Key: "email", Label: "E-mail", Type: "string", Required: true}},
	})
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}
	second, err := e.store.CreateTemplate(ctx, e.orgID, attestation.Template{SchemaID: schema.ID, Name: "Contractor"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}

	set, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{first, second.ID})
	if err != nil {
		t.Fatalf("SetOnboardingAttestations: %v", err)
	}
	if len(set) != 2 || set[0].TemplateID != first || set[1].TemplateID != second.ID {
		t.Fatalf("unexpected set order: %+v", set)
	}
	if set[0].VCT != "nl.caesar.employee" || set[0].DisplayName != "Employee" {
		t.Errorf("schema identity not joined: %+v", set[0])
	}

	// Reorder: the returned set reflects the new order.
	reordered, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{second.ID, first})
	if err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if reordered[0].TemplateID != second.ID || reordered[1].TemplateID != first {
		t.Fatalf("reorder not applied: %+v", reordered)
	}

	// The accept path reads the ids in the same order.
	ids, err := e.store.OnboardingTemplateIDs(ctx, e.orgID)
	if err != nil {
		t.Fatalf("OnboardingTemplateIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != second.ID || ids[1] != first {
		t.Fatalf("OnboardingTemplateIDs order = %v", ids)
	}
}

func TestOnboardingSetEmptyClears(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	id := personTemplate(t, ctx, e.store, e.orgID)

	if _, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{id}); err != nil {
		t.Fatalf("set: %v", err)
	}
	set, err := e.store.SetOnboardingAttestations(ctx, e.orgID, nil)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(set) != 0 {
		t.Fatalf("expected empty set, got %+v", set)
	}
}

func TestOnboardingSetRejectsOrganizationTemplate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	orgTmpl := orgTemplate(t, ctx, e.store, e.orgID)

	_, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{orgTmpl})
	if !errors.Is(err, attestation.ErrOnboardingSubject) {
		t.Fatalf("expected ErrOnboardingSubject, got %v", err)
	}
}

func TestOnboardingSetRejectsUnknownTemplate(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	_, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{uuid.New()})
	if !errors.Is(err, attestation.ErrTemplateNotFound) {
		t.Fatalf("expected ErrTemplateNotFound, got %v", err)
	}
}
