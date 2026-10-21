//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func TestFlowBrandingStore(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewFlowBrandingStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	audited := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = $1 AND target_id = 'flow-1'`,
			audit.IdentityProofingFlowBrandingConfigured).Scan(&n); err != nil {
			t.Fatalf("count audit events: %v", err)
		}
		return n
	}

	if got, err := store.Get(ctx, orgID, "flow-1"); err != nil || got.Custom || got.HasLogo {
		t.Fatalf("never set = %+v, %v; want not custom", got, err)
	}
	if _, err := store.Logo(ctx, orgID, "flow-1"); !errors.Is(err, ErrNoLogo) {
		t.Errorf("Logo of a flow never set = %v, want ErrNoLogo", err)
	}

	logo := Logo{Bytes: []byte("\x89PNG"), ContentType: contentTypePNG}
	want := FlowBranding{Custom: true, Branding: Branding{DisplayName: "Acme Reizen", PrimaryColor: "#22337A"}}
	for range 2 {
		if _, err := store.Save(ctx, orgID, "flow-1", want, LogoChange{Replace: true, Logo: logo}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	got, err := store.Get(ctx, orgID, "flow-1")
	if err != nil || !got.Custom || got.DisplayName != "Acme Reizen" || got.PrimaryColor != "#22337A" ||
		got.SupportContact != "" || got.PrivacyURL != "" || !got.HasLogo {
		t.Fatalf("Get = %+v, %v; want %+v with a logo", got, err, want)
	}
	if read, err := store.Logo(ctx, orgID, "flow-1"); err != nil || !bytes.Equal(read.Bytes, logo.Bytes) || read.ContentType != logo.ContentType {
		t.Errorf("Logo = %+v, %v; want what was saved", read, err)
	}
	// The same logo uploaded again changes only its bytes: still a change, so
	// the audit log shows it. The second Save above was one.
	if n := audited(); n != 2 {
		t.Errorf("audited %d times, want 2 (a new logo is a change)", n)
	}

	if _, err := store.Save(ctx, orgID, "flow-1", want, LogoChange{}); err != nil {
		t.Fatalf("Save without a logo change: %v", err)
	}
	if n := audited(); n != 2 {
		t.Errorf("audited %d times, want 2 (the same fields change nothing)", n)
	}
	if kept, err := store.Get(ctx, orgID, "flow-1"); err != nil || !kept.HasLogo {
		t.Errorf("after a save that leaves the logo = %+v, %v; want the logo kept", kept, err)
	}

	cleared, err := store.Save(ctx, orgID, "flow-1", want, LogoChange{Replace: true})
	if err != nil || cleared.HasLogo {
		t.Fatalf("Save removing the logo = %+v, %v; want none", cleared, err)
	}
	if _, err := store.Logo(ctx, orgID, "flow-1"); !errors.Is(err, ErrNoLogo) {
		t.Errorf("Logo after removing it = %v, want ErrNoLogo", err)
	}

	if other, _ := store.Get(ctx, orgID, "flow-2"); other.Custom {
		t.Errorf("another flow = %+v, want not custom", other)
	}

	if _, err := store.Save(ctx, orgID, "flow-1", FlowBranding{}, LogoChange{}); err != nil {
		t.Fatalf("Save not custom: %v", err)
	}
	if back, err := store.Get(ctx, orgID, "flow-1"); err != nil || back.Custom || back.DisplayName != "" {
		t.Errorf("after going back to the org's = %+v, %v; want not custom and its fields gone", back, err)
	}
	if _, err := store.Save(ctx, orgID, "flow-1", FlowBranding{}, LogoChange{}); err != nil {
		t.Fatalf("Save not custom again: %v", err)
	}
	if n := audited(); n != 4 {
		t.Errorf("audited %d times, want 4 (going back is one change, repeating it none)", n)
	}
}
