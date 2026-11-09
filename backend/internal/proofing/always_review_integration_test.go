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

// The registered address and a statement with a current job each wait for a
// reviewer on a request that reviews every step, and pass without one under
// required. The session stays approved either way.
func TestAlwaysReviewStepsOpenReviews(t *testing.T) {
	for _, tc := range []struct {
		address    AddressMode
		employment EmploymentMode
		want       ReviewState
		waiting    int
	}{
		{AddressAlwaysReview, EmploymentAlwaysReview, ReviewPending, 1},
		{AddressRequired, EmploymentRequired, "", 0},
	} {
		f := newSealedFixture(t, EventSessionAddressAdded)
		ctx := context.Background()
		f.in.Address, f.in.Employment = tc.address, tc.employment
		approved := approvedRequest(t, f.requests, f.in, "s1")
		if approved.Address != tc.address || approved.Employment != tc.employment {
			t.Fatalf("stored modes = %q, %q; want %q, %q", approved.Address, approved.Employment, tc.address, tc.employment)
		}
		addPassingSteps(t, f.requests, approved)

		got, err := f.requests.Get(ctx, f.orgID, approved.ID)
		if err != nil || got.Status != StatusApproved || got.AddressReview != tc.want ||
			got.EmploymentReview != tc.want || got.ReviewReason != "" {
			t.Errorf("%s: Get = %s, address %q, employment %q (%q), %v; want approved, both %q, no reason",
				tc.address, got.Status, got.AddressReview, got.EmploymentReview, got.ReviewReason, err, tc.want)
		}
		counts, err := f.requests.ReviewCounts(ctx, f.orgID)
		if err != nil || counts.Kinds[ReviewKindAddress] != tc.waiting || counts.Kinds[ReviewKindEmployment] != tc.waiting ||
			counts.StepSessions != tc.waiting {
			t.Errorf("%s: ReviewCounts = %+v, %v; want %d waiting of each", tc.address, counts, err, tc.waiting)
		}
	}
}

// addPassingSteps keeps on req steps whose checks pass: the registered
// address, and a statement with a current job and nothing else.
func addPassingSteps(t *testing.T, requests *RequestStore, req Request) {
	t.Helper()
	ctx := context.Background()
	registered := testAddress
	registered.Verification = AddressRegistered
	if added, err := requests.AddAddress(ctx, req, registered); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v; want added", added, err)
	}
	emp := Employment{StatementDate: "2026-10-15", Employed: true, Relations: []EmploymentRelation{
		{Kind: string(employment.KindEmployment), Employer: "VOORBEELD B.V.", PayrollTaxNumber: "123456789L01", From: "2025-01-01", To: "2026-09-30"},
	}}
	if added, err := requests.AddEmployment(ctx, req, emp); err != nil || !added {
		t.Fatalf("AddEmployment = %v, %v; want added", added, err)
	}
}
