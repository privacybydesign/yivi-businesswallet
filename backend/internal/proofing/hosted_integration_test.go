//go:build integration

package proofing

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestHostedCompletionIsStored(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if len(customer.RedirectOrigins) != 0 {
		t.Errorf("new customer origins = %v, want none", customer.RedirectOrigins)
	}
	origins := []string{"https://portal.initech.example", "http://localhost:3000"}
	if customer, err = customers.SaveRedirectOrigins(ctx, orgID, customer.ID, origins); err != nil || !slices.Equal(customer.RedirectOrigins, origins) {
		t.Fatalf("SaveRedirectOrigins = %v, %v; want %v", customer.RedirectOrigins, err, origins)
	}

	_, hash, err := newLinkToken()
	if err != nil {
		t.Fatal(err)
	}
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID})
	in.LinkTokenHash, in.LinkExpiresAt = hash, time.Now().Add(HostedLinkTTL)
	in.RedirectURL, in.Language = "https://portal.initech.example/done", email.LocaleNL
	if _, err := store.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req, err := store.GetByLinkToken(ctx, hash)
	if err != nil || req.RedirectURL != in.RedirectURL || req.Language != email.LocaleNL {
		t.Errorf("GetByLinkToken = redirect %q language %q, %v; want them as stored", req.RedirectURL, req.Language, err)
	}
}

func TestFlowHostedStore(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowHostedStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	if got, err := store.Get(ctx, orgID, "flow-1"); err != nil || !got.Enabled || got.Completion != CompletionRedirect || len(got.Locales) != 0 {
		t.Fatalf("never set = %+v, %v; want the defaults", got, err)
	}
	want := FlowHosted{Enabled: false, Locales: []email.Locale{email.LocaleNL}, Completion: CompletionDone}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "flow-1", want); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := store.Get(ctx, orgID, "flow-1")
	if err != nil || got.Enabled || got.Completion != CompletionDone || !slices.Equal(got.Locales, want.Locales) {
		t.Errorf("Get = %+v, %v; want %+v", got, err, want)
	}
	var audited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = $1 AND target_id = 'flow-1'`,
		audit.IdentityProofingFlowHostedConfigured).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audited %d times, %v; want once (a repeat changes nothing)", audited, err)
	}
	if other, _ := store.Get(ctx, orgID, "flow-2"); !other.Enabled {
		t.Errorf("another flow = %+v, want the defaults", other)
	}
}
