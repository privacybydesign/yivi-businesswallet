//go:build integration

package attestation_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// recEmail / recQerds record the recipients delivery was routed to.
type recEmail struct{ to []string }

func (r *recEmail) SendCredentialOffer(_ context.Context, _ uuid.UUID, to, _, _, _, _ string) error {
	r.to = append(r.to, to)
	return nil
}

type recQerds struct{ to []string }

func (r *recQerds) SendCredentialOffer(_ context.Context, _ uuid.UUID, toAddress, _, _, _ string) error {
	r.to = append(r.to, toAddress)
	return nil
}

// stubInstances resolves every org to the default issuer instance (empty), so
// the stub issuer's offer/claim loop runs without per-org routing.
type stubInstances struct{}

func (stubInstances) InstanceFor(_ context.Context, _ uuid.UUID) (string, error) { return "", nil }

type env struct {
	pool    *pgxpool.Pool
	store   *attestation.Store
	service *attestation.Service
	orgID   uuid.UUID
	actorID uuid.UUID
	email   *recEmail
	qerds   *recQerds
}

func setup(t *testing.T) env {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Caesar', 'caesar', 'kvk-caesar', 'NL.KVK.caesar', 'caesar@qerds.localhost')`); err != nil {
		t.Fatalf("create org: %v", err)
	}
	org, err := organization.NewStore(pool, audit.NopRecorder{}).GetBySlug(ctx, "caesar")
	if err != nil {
		t.Fatalf("get org: %v", err)
	}

	var actorID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, given_names, last_name) VALUES ('admin@caesar.nl', 'Ad', 'Min') RETURNING id`).Scan(&actorID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	store := attestation.NewStore(pool, audit.NewDBRecorder())
	mail := &recEmail{}
	qerds := &recQerds{}
	service := attestation.NewService(store, openid4vciissuer.NewStubIssuer(), stubInstances{}, mail, qerds, store, eudiholder.NewStubHolder(), "http://app.test")
	return env{pool, store, service, org.ID, actorID, mail, qerds}
}

// personTemplate creates a natural-person schema + template, returning its id.
func personTemplate(t *testing.T, ctx context.Context, store *attestation.Store, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	schema, err := store.CreateSchema(ctx, orgID, attestation.Schema{
		VCT:                "nl.caesar.employee",
		DisplayName:        "Employee",
		CredentialConfigID: "EmailCredentialSdJwt",
		SubjectType:        attestation.SubjectNaturalPerson,
		Attributes: []attestation.AttributeDef{
			{Key: "email", Label: "E-mail", Type: "string", Required: true},
			{Key: "department", Label: "Department", Type: "string"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}
	tmpl, err := store.CreateTemplate(ctx, orgID, attestation.Template{SchemaID: schema.ID, Name: "Employee"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return tmpl.ID
}

// orgTemplate creates an organization-subject schema + template.
func orgTemplate(t *testing.T, ctx context.Context, store *attestation.Store, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	schema, err := store.CreateSchema(ctx, orgID, attestation.Schema{
		VCT:                "nl.caesar.supplier",
		DisplayName:        "Approved supplier",
		CredentialConfigID: "OrganizationCredentialSdJwt",
		SubjectType:        attestation.SubjectOrganization,
		Attributes:         []attestation.AttributeDef{{Key: "name", Label: "Name", Type: "string", Required: true}},
	})
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}
	tmpl, err := store.CreateTemplate(ctx, orgID, attestation.Template{SchemaID: schema.ID, Name: "Approved supplier"})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	return tmpl.ID
}

// TestIssuePersonEmailsAndClaims: a person-subject issue e-mails the recipient,
// and the claim token resolves to claimed after the (stub) issuer issues it.
func TestIssuePersonEmailsAndClaims(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := personTemplate(t, ctx, e.store, e.orgID)

	result, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientExternal, Ref: "anna@example.com"},
		Attributes: map[string]string{"email": "anna@example.com", "department": "Platform"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if result.Status != attestation.StatusOffered || result.OfferURI == "" {
		t.Fatalf("expected offered with offerUri, got %+v", result)
	}
	if len(e.email.to) != 1 || e.email.to[0] != "anna@example.com" {
		t.Fatalf("expected email delivery to anna@example.com, got %v", e.email.to)
	}
	if len(e.qerds.to) != 0 {
		t.Fatalf("expected no qerds delivery, got %v", e.qerds.to)
	}

	// The claim token (from the DB) resolves to the offer and polls to claimed.
	var token string
	if err := e.pool.QueryRow(ctx, `SELECT claim_token FROM issued_attestations WHERE id = $1`, result.ID).Scan(&token); err != nil {
		t.Fatalf("read claim token: %v", err)
	}
	view, err := e.service.ClaimStatus(ctx, token)
	if err != nil {
		t.Fatalf("ClaimStatus: %v", err)
	}
	if view.Status != attestation.StatusClaimed || view.OfferURI == "" || view.OrganizationName != "Caesar" {
		t.Fatalf("unexpected claim view: %+v", view)
	}
}

// TestIssuePersonQrMethodSkipsEmail: the "show QR directly" method creates the
// offer but sends nothing — delivery is recorded as none and no e-mail goes out.
func TestIssuePersonQrMethodSkipsEmail(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := personTemplate(t, ctx, e.store, e.orgID)

	result, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID:     templateID,
		Recipient:      attestation.Recipient{Kind: attestation.RecipientMember, Ref: "anna@example.com"},
		Attributes:     map[string]string{"email": "anna@example.com"},
		DeliveryMethod: attestation.DeliveryMethodQR,
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if result.OfferURI == "" {
		t.Fatalf("expected an offerUri to render as a QR, got %+v", result)
	}
	if result.Delivery != attestation.DeliveryNone {
		t.Fatalf("delivery = %q, want %q", result.Delivery, attestation.DeliveryNone)
	}
	if len(e.email.to) != 0 || len(e.qerds.to) != 0 {
		t.Fatalf("expected no delivery, got email=%v qerds=%v", e.email.to, e.qerds.to)
	}
}

// TestIssueOrganizationDeliversOverQerds: an org-subject issue routes to QERDS.
func TestIssueOrganizationDeliversOverQerds(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := orgTemplate(t, ctx, e.store, e.orgID)

	_, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientOrganization, Ref: "supplier@qerds.example"},
		Attributes: map[string]string{"name": "Supplier B.V."},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(e.qerds.to) != 1 || e.qerds.to[0] != "supplier@qerds.example" {
		t.Fatalf("expected qerds delivery to supplier address, got %v", e.qerds.to)
	}
	if len(e.email.to) != 0 {
		t.Fatalf("expected no email delivery, got %v", e.email.to)
	}
}

// TestRecipientKindMustMatchSubjectType rejects a person recipient for an org schema.
func TestRecipientKindMustMatchSubjectType(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := orgTemplate(t, ctx, e.store, e.orgID)

	_, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientExternal, Ref: "a@b.com"},
		Attributes: map[string]string{"name": "X"},
	})
	if !errors.Is(err, attestation.ErrRecipientKindMismatch) {
		t.Fatalf("expected ErrRecipientKindMismatch, got %v", err)
	}
}

// TestTemplateAttributeSourcesRoundTrip persists a template's subject-source
// bindings and reads them back enriched, and confirms an empty map round-trips.
func TestTemplateAttributeSourcesRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	schema, err := e.store.CreateSchema(ctx, e.orgID, attestation.Schema{
		VCT:                "nl.caesar.badge",
		DisplayName:        "Badge",
		CredentialConfigID: "EmailCredentialSdJwt",
		SubjectType:        attestation.SubjectNaturalPerson,
		Attributes: []attestation.AttributeDef{
			{Key: "email", Label: "E-mail", Type: "string", Required: true},
			{Key: "department", Label: "Department", Type: "string"},
		},
	})
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}

	sources := map[string]string{
		"email":      attestation.SourceMemberEmail,
		"department": attestation.SourceMemberDepartment,
	}
	created, err := e.store.CreateTemplate(ctx, e.orgID, attestation.Template{
		SchemaID: schema.ID, Name: "Badge", AttributeSources: sources,
	})
	if err != nil {
		t.Fatalf("CreateTemplate: %v", err)
	}
	if got := created.AttributeSources; len(got) != 2 || got["email"] != attestation.SourceMemberEmail || got["department"] != attestation.SourceMemberDepartment {
		t.Fatalf("AttributeSources round-trip = %v, want %v", got, sources)
	}

	// Clearing the bindings on update round-trips to an empty map.
	updated, err := e.store.UpdateTemplate(ctx, e.orgID, created.ID, attestation.Template{
		Name: "Badge", AttributeSources: nil,
	})
	if err != nil {
		t.Fatalf("UpdateTemplate: %v", err)
	}
	if len(updated.AttributeSources) != 0 {
		t.Fatalf("AttributeSources after clear = %v, want empty", updated.AttributeSources)
	}
}

// TestSchemaLogoRoundTrip stores, replaces and clears a schema's credential image
// and checks it is embedded into the generated issuer config as a data: URI.
func TestSchemaLogoRoundTrip(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// A 1x1 PNG so http.DetectContentType recognises image/png.
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	}

	schema, err := e.store.CreateSchema(ctx, e.orgID, attestation.Schema{
		VCT:                "nl.caesar.card",
		DisplayName:        "Card",
		CredentialConfigID: "nl.caesar.card",
		SubjectType:        attestation.SubjectNaturalPerson,
		Attributes:         []attestation.AttributeDef{{Key: "email", Label: "E-mail", Type: "string", Required: true}},
		Display:            []attestation.LocalizedName{{Lang: "en", Name: "Card"}},
	})
	if err != nil {
		t.Fatalf("CreateSchema: %v", err)
	}
	if schema.HasLogo {
		t.Fatalf("new schema should have no image")
	}
	if _, err := e.store.GetSchemaLogo(ctx, e.orgID, schema.ID); !errors.Is(err, attestation.ErrNoSchemaLogo) {
		t.Fatalf("GetSchemaLogo on empty = %v, want ErrNoSchemaLogo", err)
	}

	// Store an image; it round-trips and flags HasLogo.
	updated, err := e.store.SetSchemaLogo(ctx, e.orgID, schema.ID, attestation.LogoUpdate{
		Replace: true, Logo: attestation.Logo{Bytes: png, ContentType: "image/png"},
	})
	if err != nil {
		t.Fatalf("SetSchemaLogo: %v", err)
	}
	if !updated.HasLogo {
		t.Fatalf("schema should have an image after upload")
	}
	logo, err := e.store.GetSchemaLogo(ctx, e.orgID, schema.ID)
	if err != nil {
		t.Fatalf("GetSchemaLogo: %v", err)
	}
	if !bytes.Equal(logo.Bytes, png) || logo.ContentType != "image/png" {
		t.Fatalf("logo round-trip mismatch: type=%q len=%d", logo.ContentType, len(logo.Bytes))
	}

	// The image reaches the generated issuer config as a data: URI.
	stored, err := e.store.GetSchema(ctx, e.orgID, schema.ID)
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(logo.Bytes)
	cfg := attestation.BuildIssuerConfig(stored, "", dataURI)
	raw, err := json.Marshal(cfg.Metadata)
	if err != nil {
		t.Fatalf("marshal issuer config: %v", err)
	}
	if !strings.Contains(string(raw), dataURI) {
		t.Fatalf("issuer config credential display missing embedded image: %s", raw)
	}

	// Clearing the image resets HasLogo and returns ErrNoSchemaLogo.
	cleared, err := e.store.SetSchemaLogo(ctx, e.orgID, schema.ID, attestation.LogoUpdate{Replace: true})
	if err != nil {
		t.Fatalf("SetSchemaLogo clear: %v", err)
	}
	if cleared.HasLogo {
		t.Fatalf("schema should have no image after clear")
	}
	if _, err := e.store.GetSchemaLogo(ctx, e.orgID, schema.ID); !errors.Is(err, attestation.ErrNoSchemaLogo) {
		t.Fatalf("GetSchemaLogo after clear = %v, want ErrNoSchemaLogo", err)
	}

	// A missing schema is distinguished from a missing image.
	if _, err := e.store.GetSchemaLogo(ctx, e.orgID, uuid.New()); !errors.Is(err, attestation.ErrSchemaNotFound) {
		t.Fatalf("GetSchemaLogo unknown schema = %v, want ErrSchemaNotFound", err)
	}
	if _, err := e.store.SetSchemaLogo(ctx, e.orgID, uuid.New(), attestation.LogoUpdate{Replace: true}); !errors.Is(err, attestation.ErrSchemaNotFound) {
		t.Fatalf("SetSchemaLogo unknown schema = %v, want ErrSchemaNotFound", err)
	}
}

// TestDataMinimisationRejectsUndeclaredAttribute enforces the schema allow-list.
func TestDataMinimisationRejectsUndeclaredAttribute(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := personTemplate(t, ctx, e.store, e.orgID)

	_, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientExternal, Ref: "x@example.com"},
		Attributes: map[string]string{"email": "x@example.com", "salary": "secret"},
	})
	if !errors.Is(err, attestation.ErrUnknownAttribute) {
		t.Fatalf("expected ErrUnknownAttribute, got %v", err)
	}
	list, err := e.store.ListIssued(ctx, e.orgID)
	if err != nil {
		t.Fatalf("ListIssued: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no ledger rows, got %d", len(list))
	}
}

// TestRevoke flips an issued attestation to revoked.
func TestRevoke(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	templateID := personTemplate(t, ctx, e.store, e.orgID)

	result, err := e.service.Issue(ctx, e.orgID, e.actorID, "Caesar", attestation.IssueInput{
		TemplateID: templateID,
		Recipient:  attestation.Recipient{Kind: attestation.RecipientExternal, Ref: "z@example.com"},
		Attributes: map[string]string{"email": "z@example.com"},
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	revoked, err := e.service.Revoke(ctx, e.orgID, result.ID)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.Status != attestation.StatusRevoked || revoked.RevokedAt == nil {
		t.Fatalf("expected revoked, got %+v", revoked)
	}
	if _, err := e.service.Revoke(ctx, e.orgID, result.ID); !errors.Is(err, attestation.ErrNotOfferable) {
		t.Fatalf("expected ErrNotOfferable on re-revoke, got %v", err)
	}
}
