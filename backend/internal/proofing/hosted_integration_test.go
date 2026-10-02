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

// A flow's member, hosted and diploma settings share one row: saving one
// leaves the others as they were.
func TestFlowSettingsAreIndependent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	selection := NewSettingsStore(pool, audit.NopRecorder{})
	hosted := NewFlowHostedStore(pool, audit.NopRecorder{})
	diplomas := NewFlowDiplomaStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	custom := FlowHosted{Enabled: false, Locales: []email.Locale{email.LocaleNL}, Completion: CompletionDone}
	if _, err := hosted.Save(ctx, orgID, "flow-1", custom); err != nil {
		t.Fatalf("save hosted: %v", err)
	}
	if _, err := diplomas.Save(ctx, orgID, "flow-1", DiplomasRequired); err != nil {
		t.Fatalf("save diplomas: %v", err)
	}
	if sel, err := selection.FlowSelection(ctx, orgID); err != nil || len(sel.FlowIDs) != 0 {
		t.Fatalf("selection after hosted and diploma settings = %+v, %v; want none offered to members", sel, err)
	}

	if err := selection.SaveFlowSelection(ctx, orgID, FlowSelection{FlowIDs: []string{"flow-1", "flow-2"}, DefaultFlowID: "flow-1"}); err != nil {
		t.Fatalf("offer flows: %v", err)
	}
	if err := selection.SaveFlowSelection(ctx, orgID, FlowSelection{FlowIDs: []string{"flow-2"}, DefaultFlowID: "flow-2"}); err != nil {
		t.Fatalf("take flow-1 off: %v", err)
	}
	if sel, err := selection.FlowSelection(ctx, orgID); err != nil || !slices.Equal(sel.FlowIDs, []string{"flow-2"}) || sel.DefaultFlowID != "flow-2" {
		t.Errorf("selection = %+v, %v; want flow-2 only, as default", sel, err)
	}
	if got, err := hosted.Get(ctx, orgID, "flow-1"); err != nil || got.Enabled || got.Completion != CompletionDone || !slices.Equal(got.Locales, custom.Locales) {
		t.Errorf("hosted after the selection changed = %+v, %v; want %+v", got, err, custom)
	}
	if got, err := diplomas.Get(ctx, orgID, "flow-1"); err != nil || got != DiplomasRequired {
		t.Errorf("diplomas after the selection changed = %q, %v; want required", got, err)
	}
	if all, err := diplomas.All(ctx, orgID); err != nil || len(all) != 1 || all["flow-1"] != DiplomasRequired {
		t.Errorf("All = %v, %v; want only flow-1, required", all, err)
	}
}
