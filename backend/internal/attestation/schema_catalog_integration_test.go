//go:build integration

package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

// The catalogue spans organizations, names each type's issuer, and leaves out
// drafts, which never issued anything.
func TestListSchemaCatalogSpansIssuersAndSkipsDrafts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	var globex uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Globex', 'globex', 'kvk-globex', 'NL.KVK.globex', 'globex@qerds.localhost') RETURNING id`).Scan(&globex); err != nil {
		t.Fatalf("create org: %v", err)
	}
	create := func(orgID uuid.UUID, vct, status string) {
		t.Helper()
		if _, err := e.store.CreateSchema(ctx, orgID, attestation.Schema{
			VCT:                vct,
			DisplayName:        vct,
			CredentialConfigID: "Cfg",
			SubjectType:        attestation.SubjectOrganization,
			Status:             status,
			Attributes:         []attestation.AttributeDef{{Key: "legalName", Label: "Legal name", Type: "string"}},
		}); err != nil {
			t.Fatalf("CreateSchema %s: %v", vct, err)
		}
	}
	create(e.orgID, "nl.caesar.membership", attestation.SchemaActive)
	create(globex, "nl.globex.supplier", attestation.SchemaDeprecated)
	create(globex, "nl.globex.draft", attestation.SchemaDraft)

	entries, err := e.store.ListSchemaCatalog(ctx)
	if err != nil {
		t.Fatalf("ListSchemaCatalog: %v", err)
	}
	got := map[string]string{}
	for _, entry := range entries {
		got[entry.VCT] = entry.IssuerName
	}
	want := map[string]string{"nl.caesar.membership": "Caesar", "nl.globex.supplier": "Globex"}
	if len(got) != len(want) {
		t.Fatalf("catalogue = %v, want %v", got, want)
	}
	for vct, issuer := range want {
		if got[vct] != issuer {
			t.Errorf("%s issued by %q, want %q", vct, got[vct], issuer)
		}
	}
	for _, entry := range entries {
		if len(entry.Attributes) != 1 || entry.Attributes[0].Key != "legalName" {
			t.Errorf("%s attributes = %+v", entry.VCT, entry.Attributes)
		}
	}
}
