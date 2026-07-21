package eudiholder_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func TestStubHolderStoreDeleteRoundTrip(t *testing.T) {
	t.Parallel()
	h := eudiholder.NewStubHolder()
	ctx := context.Background()
	org := uuid.New()

	if err := h.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	ref, err := h.Store(ctx, org, eudiholder.Credential{VCT: "nl.kvk.registration"})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if ref == "" {
		t.Fatal("store returned empty ref")
	}

	// Two stores yield distinct refs.
	ref2, err := h.Store(ctx, org, eudiholder.Credential{VCT: "nl.kvk.registration"})
	if err != nil {
		t.Fatalf("store 2: %v", err)
	}
	if ref == ref2 {
		t.Fatalf("expected distinct refs, both %q", ref)
	}

	// Delete is idempotent: deleting a present then absent ref both succeed.
	if err := h.Delete(ctx, org, ref); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := h.Delete(ctx, org, ref); err != nil {
		t.Fatalf("delete absent ref should be a no-op: %v", err)
	}
	// Deleting for an org that never stored anything is a no-op.
	if err := h.Delete(ctx, uuid.New(), "whatever"); err != nil {
		t.Fatalf("delete unknown org should be a no-op: %v", err)
	}
}

func TestStubHolderRedeem(t *testing.T) {
	t.Parallel()
	h := eudiholder.NewStubHolder()
	ctx := context.Background()
	org := uuid.New()

	const offer = "openid-credential-offer://?credential_offer=%7B%7D"
	got, err := h.Redeem(ctx, org, offer)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if got.Ref == "" {
		t.Error("redeem returned empty ref")
	}
	if got.VCT == "" {
		t.Error("redeem returned empty vct")
	}

	// A redeemed credential exposes its disclosed attributes (the stub attaches a
	// synthetic payload so the held-detail view has data in dev/CI).
	claims, err := h.Claims(ctx, org, got.Ref, got.VCT)
	if err != nil {
		t.Fatalf("claims: %v", err)
	}
	if got := attributeValue(claims.Attributes, "company_name"); got != "Demo Supplier B.V." {
		t.Errorf("claims[company_name] = %v, want the demo value", got)
	}
	if hasAttribute(claims.Attributes, "vct") {
		t.Error("claims should not include the registered vct claim")
	}

	// The vct fallback resolves the same credential when the ref is unknown.
	viaVCT, err := h.Claims(ctx, org, "", got.VCT)
	if err != nil {
		t.Fatalf("claims by vct: %v", err)
	}
	if got := attributeValue(viaVCT.Attributes, "company_name"); got != "Demo Supplier B.V." {
		t.Errorf("claims by vct[company_name] = %v, want the demo value", got)
	}

	// Claims for an unknown ref and vct yields an empty attribute set, not an error.
	empty, err := h.Claims(ctx, org, "does-not-exist", "")
	if err != nil {
		t.Fatalf("claims unknown ref: %v", err)
	}
	if len(empty.Attributes) != 0 {
		t.Errorf("claims for unknown ref = %v, want empty", empty.Attributes)
	}

	// The redeemed credential is now held: deleting its ref is a no-op success.
	if err := h.Delete(ctx, org, got.Ref); err != nil {
		t.Fatalf("delete redeemed ref: %v", err)
	}
}

// attributeValue returns the value of the attribute with the given key, or nil.
func attributeValue(attrs []eudiholder.HeldAttribute, key string) any {
	for _, a := range attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return nil
}

// hasAttribute reports whether an attribute with the given key is present.
func hasAttribute(attrs []eudiholder.HeldAttribute, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}

func TestParseMasterKey(t *testing.T) {
	t.Parallel()
	valid := strings.Repeat("ab", 32) // 64 hex chars = 32 bytes
	if _, err := eudiholder.ParseMasterKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, bad := range []string{"", "zz", strings.Repeat("ab", 16), "nothex!!"} {
		if _, err := eudiholder.ParseMasterKey(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}
