//go:build integration

package proofing

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/employment"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowStepAlwaysReviewSaved(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	addresses := NewFlowAddressStore(pool, audit.NewDBRecorder())
	if _, err := addresses.Save(ctx, orgID, "f1", AddressAlwaysReview); err != nil {
		t.Fatalf("Save address: %v", err)
	}
	if mode, err := addresses.Get(ctx, orgID, "f1"); err != nil || mode != AddressAlwaysReview {
		t.Errorf("address = %q, %v; want always_review", mode, err)
	}
	statements := NewFlowEmploymentStore(pool, audit.NewDBRecorder())
	if _, err := statements.Save(ctx, orgID, "f1", EmploymentAlwaysReview); err != nil {
		t.Fatalf("Save employment: %v", err)
	}
	if mode, err := statements.Get(ctx, orgID, "f1"); err != nil || mode != EmploymentAlwaysReview {
		t.Errorf("employment = %q, %v; want always_review", mode, err)
	}
}

// On a request that reviews every step, the registered address and a
// statement with a current job each wait for a reviewer; the session stays
// approved.
func TestAlwaysReviewStepsOpenReviews(t *testing.T) {
	f := newSealedFixture(t, EventSessionAddressAdded)
	ctx := context.Background()
	f.in.Address, f.in.Employment = AddressAlwaysReview, EmploymentAlwaysReview
	approved := approvedRequest(t, f.requests, f.in, "s1")
	if approved.Address != AddressAlwaysReview || approved.Employment != EmploymentAlwaysReview {
		t.Fatalf("stored modes = %q, %q; want always_review", approved.Address, approved.Employment)
	}

	registered := testAddress
	registered.Verification = AddressRegistered
	if added, err := f.requests.AddAddress(ctx, approved, registered); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	emp := Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{
		{Kind: string(employment.KindEmployment), Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", From: "2025-01-01", To: "2026-09-30"},
	}}
	if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}

	got, err := f.requests.Get(ctx, f.orgID, approved.ID)
	if err != nil || got.Status != StatusApproved || got.AddressReview != ReviewPending ||
		got.EmploymentReview != ReviewPending || got.ReviewReason != "" {
		t.Fatalf("Get = %s, address %q, employment %q (%q), %v; want approved, both pending, no reason",
			got.Status, got.AddressReview, got.EmploymentReview, got.ReviewReason, err)
	}
	counts, err := f.requests.ReviewCounts(ctx, f.orgID)
	if err != nil || counts.Kinds[ReviewKindAddress] != 1 || counts.Kinds[ReviewKindEmployment] != 1 || counts.StepSessions != 1 {
		t.Errorf("ReviewCounts = %+v, %v; want one address, one employment, one session waiting for a step", counts, err)
	}
}

// Under required, the registered address and a statement with a current job
// pass without a reviewer.
func TestRequiredStepsPassWithoutReview(t *testing.T) {
	f := newSealedFixture(t, EventSessionAddressAdded)
	ctx := context.Background()
	f.in.Address, f.in.Employment = AddressRequired, EmploymentRequired
	approved := approvedRequest(t, f.requests, f.in, "s1")

	registered := testAddress
	registered.Verification = AddressRegistered
	if added, err := f.requests.AddAddress(ctx, approved, registered); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	emp := Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{
		{Kind: string(employment.KindEmployment), Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", From: "2025-01-01", To: "2026-09-30"},
	}}
	if added, err := f.requests.AddEmployment(ctx, approved, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
	got, err := f.requests.Get(ctx, f.orgID, approved.ID)
	if err != nil || got.AddressReview != "" || got.EmploymentReview != "" {
		t.Errorf("Get = address %q, employment %q, %v; want no review", got.AddressReview, got.EmploymentReview, err)
	}
}
