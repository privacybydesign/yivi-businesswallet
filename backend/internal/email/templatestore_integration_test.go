//go:build integration

package email_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestResolveTemplateFallsBackToTheShippedDefault(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")

	got, err := store.ResolveTemplate(context.Background(), orgID, email.KindInvitation, email.LocaleEN)
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	shipped, _ := email.DefaultTemplate(email.KindInvitation, email.LocaleEN)
	if got.Subject != shipped.Subject {
		t.Errorf("subject = %q, want the shipped default %q", got.Subject, shipped.Subject)
	}
}

func TestSaveTemplateRoundtripsAndTakesOverFromTheDefault(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	in := email.Template{
		Subject:   "{{orgName}} invites you",
		Preheader: "One click to join",
		Blocks: []email.Block{
			{Type: email.BlockLogo},
			{Type: email.BlockHeading, Text: "Join {{orgName}}"},
			{Type: email.BlockParagraph, Text: "We would like you on board."},
			{Type: email.BlockDivider},
			{Type: email.BlockParagraph, Text: "It takes a minute."},
			{Type: email.BlockButton, Label: "Accept", URL: "{{acceptUrl}}", LinkFallback: "Or open this link:"},
			{Type: email.BlockFooter, Text: "Sent by {{orgName}}."},
		},
	}
	saved, err := store.SaveTemplate(ctx, orgID, email.KindInvitation, email.LocaleEN, in)
	if err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}
	if saved.Template.Subject != in.Subject || len(saved.Template.Blocks) != len(in.Blocks) {
		t.Fatalf("SaveTemplate returned %+v, want the saved copy", saved.Template)
	}
	if saved.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero after SaveTemplate")
	}

	resolved, err := store.ResolveTemplate(ctx, orgID, email.KindInvitation, email.LocaleEN)
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	// The whole layout must round-trip through the JSONB column: every block, in
	// order, with every field.
	if len(resolved.Blocks) != len(in.Blocks) {
		t.Fatalf("resolved %d blocks, want %d", len(resolved.Blocks), len(in.Blocks))
	}
	for i, blk := range in.Blocks {
		if resolved.Blocks[i] != blk {
			t.Errorf("blocks[%d] = %+v, want %+v", i, resolved.Blocks[i], blk)
		}
	}

	// A different locale is customised independently.
	dutch, err := store.ResolveTemplate(ctx, orgID, email.KindInvitation, email.LocaleNL)
	if err != nil {
		t.Fatalf("ResolveTemplate(nl): %v", err)
	}
	shippedNL, _ := email.DefaultTemplate(email.KindInvitation, email.LocaleNL)
	if dutch.Subject != shippedNL.Subject {
		t.Errorf("nl subject = %q, want the shipped Dutch default", dutch.Subject)
	}
}

func TestSaveTemplateRejectsAnUnknownPlaceholder(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	_, err := store.SaveTemplate(ctx, orgID, email.KindInvitation, email.LocaleEN, email.Template{
		Subject: "Join {{orgName}}",
		Blocks:  []email.Block{{Type: email.BlockHeading, Text: "Hello {{recipientName}}"}},
	})
	if _, ok := errors.AsType[*email.InvalidTemplateError](err); !ok {
		t.Fatalf("err = %v, want an InvalidTemplateError", err)
	}
	if _, ok, getErr := store.GetTemplate(ctx, orgID, email.KindInvitation, email.LocaleEN); getErr != nil || ok {
		t.Errorf("a rejected template was stored (ok = %v, err = %v)", ok, getErr)
	}
}

func TestDeleteTemplateRevertsToTheShippedDefault(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if _, err := store.SaveTemplate(ctx, orgID, email.KindSMTPTest, email.LocaleEN, email.Template{
		Subject: "Ours",
		Blocks:  []email.Block{{Type: email.BlockHeading, Text: "Ours"}},
	}); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	deleted, err := store.DeleteTemplate(ctx, orgID, email.KindSMTPTest, email.LocaleEN)
	if err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if !deleted {
		t.Fatal("DeleteTemplate reported nothing deleted")
	}

	resolved, err := store.ResolveTemplate(ctx, orgID, email.KindSMTPTest, email.LocaleEN)
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	shipped, _ := email.DefaultTemplate(email.KindSMTPTest, email.LocaleEN)
	if resolved.Subject != shipped.Subject {
		t.Errorf("subject = %q, want the shipped default %q", resolved.Subject, shipped.Subject)
	}

	// Reverting twice is not an error, but the second call has nothing to audit.
	deleted, err = store.DeleteTemplate(ctx, orgID, email.KindSMTPTest, email.LocaleEN)
	if err != nil {
		t.Fatalf("DeleteTemplate (second): %v", err)
	}
	if deleted {
		t.Error("DeleteTemplate reported a deletion for a template that was not customized")
	}
}

func TestListTemplatesReturnsOnlyCustomizedCells(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	for _, locale := range []email.Locale{email.LocaleEN, email.LocaleNL} {
		if _, err := store.SaveTemplate(ctx, orgID, email.KindSMTPTest, locale, email.Template{
			Subject: "Ours",
			Blocks:  []email.Block{{Type: email.BlockHeading, Text: "Ours"}},
		}); err != nil {
			t.Fatalf("SaveTemplate(%s): %v", locale, err)
		}
	}

	got, err := store.ListTemplates(ctx, orgID)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTemplates returned %d records, want 2 (only the customized cells)", len(got))
	}
	for _, record := range got {
		if record.Kind != email.KindSMTPTest {
			t.Errorf("unexpected kind %q", record.Kind)
		}
	}
}

// One org's copy must never leak into another's mail.
func TestTemplatesAreScopedToTheirOrganization(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := email.NewStore(pool, audit.NopRecorder{}, nil)
	ctx := context.Background()
	acme := makeOrg(t, pool, "acme")
	other := makeOrg(t, pool, "other")

	if _, err := store.SaveTemplate(ctx, acme, email.KindSMTPTest, email.LocaleEN, email.Template{
		Subject: "Acme only",
		Blocks:  []email.Block{{Type: email.BlockHeading, Text: "Acme only"}},
	}); err != nil {
		t.Fatalf("SaveTemplate: %v", err)
	}

	resolved, err := store.ResolveTemplate(ctx, other, email.KindSMTPTest, email.LocaleEN)
	if err != nil {
		t.Fatalf("ResolveTemplate: %v", err)
	}
	shipped, _ := email.DefaultTemplate(email.KindSMTPTest, email.LocaleEN)
	if resolved.Subject != shipped.Subject {
		t.Errorf("subject = %q, want the shipped default for an org that customized nothing", resolved.Subject)
	}
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}
