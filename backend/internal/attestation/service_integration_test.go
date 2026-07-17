//go:build integration

package attestation_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
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
	service := attestation.NewService(store, openid4vciissuer.NewStubIssuer(), stubInstances{}, mail, qerds, "http://app.test")
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
