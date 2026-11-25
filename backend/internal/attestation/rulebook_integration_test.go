//go:build integration

package attestation_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation/rulebook"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func rulebookTemplate(t *testing.T, ctx context.Context, e env, id string) (attestation.Schema, attestation.Template) {
	t.Helper()
	in, err := attestation.SchemaFromRulebook(id, "")
	if err != nil {
		t.Fatalf("SchemaFromRulebook: %v", err)
	}
	schema, err := e.store.CreateSchema(ctx, e.orgID, in)
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}
	tmpl, err := e.store.CreateTemplate(ctx, e.orgID, attestation.Template{SchemaID: schema.ID, Name: in.DisplayName})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return schema, tmpl
}

// TestRulebookSchemaRoundTrip: an imported rulebook schema keeps its rulebook,
// and a second import of the same rulebook is refused on the vct.
func TestRulebookSchemaRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	schema, tmpl := rulebookTemplate(t, ctx, e, "eucc")
	if tmpl.Rulebook != "eucc" {
		t.Errorf("template rulebook = %q, want eucc", tmpl.Rulebook)
	}

	got, err := e.store.GetSchema(ctx, e.orgID, schema.ID)
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	if got.Rulebook != "eucc" || got.VCT != "uri:eu.eudi.eucc.1" {
		t.Errorf("stored schema rulebook=%q vct=%q", got.Rulebook, got.VCT)
	}

	again, _ := attestation.SchemaFromRulebook("eucc", "")
	if _, err := e.store.CreateSchema(ctx, e.orgID, again); !errors.Is(err, attestation.ErrSchemaVctTaken) {
		t.Errorf("second import err = %v, want ErrSchemaVctTaken", err)
	}
}

// TestIssueRulebookAttestationKeepsNestedClaims: issuing from a rulebook schema
// validates against the rulebook and stores the nested claims in the ledger.
func TestIssueRulebookAttestationKeepsNestedClaims(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, tmpl := rulebookTemplate(t, ctx, e, "eucc")
	rb, _ := rulebook.Get("eucc")
	claims := rb.Sample()

	result, err := e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: tmpl.ID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: claims,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	stored, err := e.store.GetIssued(ctx, e.orgID, result.ID)
	if err != nil {
		t.Fatalf("GetIssued: %v", err)
	}
	if !reflect.DeepEqual(stored.Attributes["registered_address"], claims["registered_address"]) {
		t.Errorf("registered_address = %#v, want %#v", stored.Attributes["registered_address"], claims["registered_address"])
	}
	if reps, ok := stored.Attributes["legal_representative"].([]any); !ok || len(reps) != 2 {
		t.Errorf("legal_representative = %#v", stored.Attributes["legal_representative"])
	}

	delete(claims, "legal_person_name")
	_, err = e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: tmpl.ID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: claims,
	})
	if !errors.Is(err, rulebook.ErrInvalidClaims) {
		t.Errorf("issue without legal_person_name err = %v, want ErrInvalidClaims", err)
	}
}

// TestIssuedToOrganizationClaimedOnceRedeemed: the issuer's ledger shows an offer
// to another organization as offered until that organization's wallet redeems it.
func TestIssuedToOrganizationClaimedOnceRedeemed(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, tmpl := rulebookTemplate(t, ctx, e, "vat-id")
	rb, _ := rulebook.Get("vat-id")

	result, err := e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: tmpl.ID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: rb.Sample(),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	statusOf := func() string {
		t.Helper()
		list, err := e.service.ListIssued(ctx, e.orgID)
		if err != nil {
			t.Fatalf("ListIssued: %v", err)
		}
		for _, row := range list {
			if row.ID == result.ID {
				return row.Status
			}
		}
		t.Fatalf("issued %s not listed", result.ID)
		return ""
	}
	if got := statusOf(); got != attestation.StatusOffered {
		t.Fatalf("status before the recipient redeems = %q, want offered", got)
	}

	if _, err := eudiholder.NewStubHolder().Redeem(ctx, uuid.New(), result.OfferURI); err != nil {
		t.Fatalf("recipient redeem: %v", err)
	}
	if got := statusOf(); got != attestation.StatusClaimed {
		t.Fatalf("status after the recipient redeemed = %q, want claimed", got)
	}
}

// TestOrganizationSchemaStillIssuesStrings: an organization-defined schema keeps
// refusing anything but string values.
func TestOrganizationSchemaStillIssuesStrings(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := orgTemplate(t, ctx, e.store, e.orgID)
	_, err := e.service.Issue(ctx, e.orgID, &e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: map[string]any{"name": map[string]any{"nested": "x"}, attestation.IdentifierClaim: map[string]any{"euid": "NLNHR.12345678"}},
	})
	if !errors.Is(err, attestation.ErrInvalidAttribute) {
		t.Errorf("nested value on an organization schema err = %v, want ErrInvalidAttribute", err)
	}
}

// TestOnboardingRefusesRulebookTemplates: a rulebook template about a person still
// cannot join the onboarding auto-issue set, since member data cannot fill it.
func TestOnboardingRefusesRulebookTemplates(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	_, tmpl := rulebookTemplate(t, ctx, e, "eu-poa")
	_, err := e.store.SetOnboardingAttestations(ctx, e.orgID, []uuid.UUID{tmpl.ID})
	if !errors.Is(err, attestation.ErrOnboardingRulebook) {
		t.Errorf("err = %v, want ErrOnboardingRulebook", err)
	}
}
