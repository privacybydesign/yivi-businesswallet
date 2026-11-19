//go:build integration

package openid4vprequester_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestTemplateLifecycle(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	orgID, otherOrg := createOrg(t, pool, "acme"), createOrg(t, pool, "globex")
	store := openid4vprequester.NewStore(pool, audit.NewDBRecorder())

	// Saved as a send would check it: names trimmed, a repeated claim kept once.
	created, err := store.CreateTemplate(ctx, openid4vprequester.NewTemplate{
		OrganizationID: orgID, Name: "  Beschikking  ",
		Credentials: []openid4vprequester.CredentialRequest{{VCT: " " + requestedVCT + " ", Claims: []string{"legalName", "legalName"}}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "Beschikking" {
		t.Errorf("name = %q, want it trimmed", created.Name)
	}
	if len(created.Credentials) != 1 || created.Credentials[0].VCT != requestedVCT || !slices.Equal(created.Credentials[0].Claims, []string{"legalName"}) {
		t.Errorf("credentials = %+v", created.Credentials)
	}

	if _, err := store.CreateTemplate(ctx, openid4vprequester.NewTemplate{
		OrganizationID: orgID, Name: "Beschikking",
		Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}},
	}); !errors.Is(err, openid4vprequester.ErrTemplateNameTaken) {
		t.Errorf("duplicate name: err = %v, want ErrTemplateNameTaken", err)
	}
	// The name is unique per organization, not globally.
	if _, err := store.CreateTemplate(ctx, openid4vprequester.NewTemplate{
		OrganizationID: otherOrg, Name: "Beschikking",
		Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}},
	}); err != nil {
		t.Errorf("same name in another org: %v", err)
	}
	for _, bad := range []openid4vprequester.NewTemplate{
		{OrganizationID: orgID, Name: " ", Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT}}},
		{OrganizationID: orgID, Name: "No credentials"},
		{OrganizationID: orgID, Name: "Bad claim", Credentials: []openid4vprequester.CredentialRequest{{VCT: requestedVCT, Claims: []string{"two words"}}}},
	} {
		if _, err := store.CreateTemplate(ctx, bad); !errors.Is(err, openid4vprequester.ErrInvalidInput) {
			t.Errorf("CreateTemplate(%q): err = %v, want ErrInvalidInput", bad.Name, err)
		}
	}

	list, err := store.ListTemplates(ctx, orgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, want only this org's template", list)
	}

	// Another organization cannot delete it; the owner can, once.
	if err := store.DeleteTemplate(ctx, otherOrg, created.ID); !errors.Is(err, openid4vprequester.ErrTemplateNotFound) {
		t.Errorf("delete from other org: err = %v, want ErrTemplateNotFound", err)
	}
	if err := store.DeleteTemplate(ctx, orgID, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := store.DeleteTemplate(ctx, orgID, created.ID); !errors.Is(err, openid4vprequester.ErrTemplateNotFound) {
		t.Errorf("second delete: err = %v, want ErrTemplateNotFound", err)
	}

	want := []string{audit.PresentationRequestTemplateCreated, audit.PresentationRequestTemplateDeleted}
	if got := auditActions(t, pool, created.ID); !slices.Equal(got, want) {
		t.Errorf("audit = %v, want %v", got, want)
	}
}
