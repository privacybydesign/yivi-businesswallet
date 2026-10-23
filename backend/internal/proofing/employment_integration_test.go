//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowEmploymentSaveAudit(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowEmploymentStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	if mode, err := store.Get(ctx, orgID, "f1"); err != nil || mode != EmploymentOff {
		t.Errorf("Get unset = %q, %v; want off", mode, err)
	}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "f1", EmploymentRequired); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	all, err := store.All(ctx, orgID)
	if err != nil || len(all) != 1 || all["f1"] != EmploymentRequired {
		t.Errorf("All = %v, %v; want f1 required", all, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowEmploymentConfigured); n != 1 {
		t.Errorf("audited %d times, want once: saving the same mode changes nothing", n)
	}
}

func TestEmploymentSealedTillPurge(t *testing.T) {
	f := newSealedFixture(t, "Casino", EventSessionEmploymentAdded)
	pool, requests, webhooks, orgID, in := f.pool, f.requests, f.webhooks, f.orgID, f.in
	ctx := context.Background()
	in.Employment = EmploymentRequired
	req := createStarted(t, requests, in, "s1")
	if req.Employment != EmploymentRequired {
		t.Errorf("stored employment mode = %q, want required", req.Employment)
	}
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	if err := requests.RecordEmploymentRejected(ctx, req, "holder_mismatch", ""); err != nil {
		t.Fatalf("RecordEmploymentRejected: %v", err)
	}
	emp := Employment{StatementDate: "2026-10-15", Relations: []EmploymentRelation{{
		Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", ContractForm: "schriftelijke arbeidsovereenkomst voor onbepaalde tijd",
		From: "2025-01-01", To: "2026-09-30",
	}}}
	if added, err := requests.AddEmployment(ctx, req, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	if added, err := requests.AddEmployment(ctx, req, emp); err != nil || added {
		t.Errorf("AddEmployment again = %v, %v; want refused", added, err)
	}
	got, err := requests.Get(ctx, orgID, req.ID)
	if err != nil || got.SubjectEmployment == nil || got.SubjectEmployment.Relations[0] != emp.Relations[0] || got.EmploymentAddedAt == nil {
		t.Fatalf("Get = %+v, %v; want the employment", got.SubjectEmployment, err)
	}
	checkEmploymentSealed(t, pool, webhooks, got)

	if err := requests.Purge(ctx, got, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	checkEmploymentPurged(t, pool, requests, got)
}

// checkEmploymentSealed checks req's employment is stored sealed, audited and
// sent once.
func checkEmploymentSealed(t *testing.T, pool *pgxpool.Pool, webhooks *WebhookStore, req Request) {
	t.Helper()
	ctx := context.Background()
	var plain bool
	if err := pool.QueryRow(ctx, `SELECT position('VOORBEELD' in encode(employment_ciphertext, 'escape')) > 0
		FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&plain); err != nil || plain {
		t.Errorf("employment stored in the clear (%v)", err)
	}
	if auditCount(t, pool, audit.IdentityProofingEmploymentAdded) != 1 || auditCount(t, pool, audit.IdentityProofingEmploymentRejected) != 1 {
		t.Error("want one employment_added and one employment_rejected event")
	}
	deliveries, err := webhooks.Deliveries(ctx, req.OrganizationID, *req.CustomerID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionEmploymentAdded {
		t.Errorf("deliveries = %+v, %v; want one session.employment_added", deliveries, err)
	}
}

// checkEmploymentPurged checks a purge left none of req's employment behind.
func checkEmploymentPurged(t *testing.T, pool *pgxpool.Pool, requests *RequestStore, req Request) {
	t.Helper()
	ctx := context.Background()
	purged, err := requests.Get(ctx, req.OrganizationID, req.ID)
	if err != nil || purged.SubjectEmployment != nil {
		t.Errorf("after purge employment = %+v, %v; want none", purged.SubjectEmployment, err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_webhook_deliveries
		WHERE request_id = $1 AND payload ? 'employment'`, req.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("deliveries still carrying the employment = %d (%v), want none", left, err)
	}
}
