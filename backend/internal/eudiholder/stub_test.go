package eudiholder_test

import (
	"context"
	"strings"
	"testing"
	"time"

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
	claims, err := h.Claims(ctx, org, got.Ref, got.VCT, "en")
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
	viaVCT, err := h.Claims(ctx, org, "", got.VCT, "en")
	if err != nil {
		t.Fatalf("claims by vct: %v", err)
	}
	if got := attributeValue(viaVCT.Attributes, "company_name"); got != "Demo Supplier B.V." {
		t.Errorf("claims by vct[company_name] = %v, want the demo value", got)
	}

	// Claims for an unknown ref and vct yields an empty attribute set, not an error.
	empty, err := h.Claims(ctx, org, "does-not-exist", "", "en")
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

// TestStubHolderValidities covers the held view's status source: the expiry the
// credential was stored with, keyed by the ref the held index points at. The stub
// checks no status list, so nothing ever reads as revoked.
func TestStubHolderValidities(t *testing.T) {
	t.Parallel()
	h := eudiholder.NewStubHolder()
	ctx := context.Background()
	org := uuid.New()

	expires := time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)
	expiring, err := h.Store(ctx, org, eudiholder.Credential{
		VCT:       "nl.kvk.registration",
		ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatalf("store expiring: %v", err)
	}
	perpetual, err := h.Store(ctx, org, eudiholder.Credential{VCT: "eaa.perpetual"})
	if err != nil {
		t.Fatalf("store perpetual: %v", err)
	}

	validities, err := h.Validities(ctx, org)
	if err != nil {
		t.Fatalf("validities: %v", err)
	}
	if len(validities) != 2 {
		t.Fatalf("validities has %d entries, want one per held credential", len(validities))
	}
	if got := validities[expiring].ExpiresAt; got == nil || !got.Equal(expires) {
		t.Errorf("validities[expiring].ExpiresAt = %v, want %v", got, expires)
	}
	if got := validities[perpetual].ExpiresAt; got != nil {
		t.Errorf("validities[perpetual].ExpiresAt = %v, want nil: it does not expire", got)
	}
	for ref, validity := range validities {
		if validity.Revoked {
			t.Errorf("validities[%s].Revoked = true, want false: the stub checks no status list", ref)
		}
	}

	// A redeemed credential carries an expiry too, so the held view has one to show
	// in local dev / CI.
	redeemed, err := h.Redeem(ctx, org, "openid-credential-offer://?x=1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	validities, err = h.Validities(ctx, org)
	if err != nil {
		t.Fatalf("validities after redeem: %v", err)
	}
	if got := validities[redeemed.Ref].ExpiresAt; got == nil || !got.After(time.Now()) {
		t.Errorf("validities[redeemed].ExpiresAt = %v, want an expiry in the future", got)
	}

	// An org that holds nothing yields an empty map, never nil.
	empty, err := h.Validities(ctx, uuid.New())
	if err != nil {
		t.Fatalf("validities for unknown org: %v", err)
	}
	if empty == nil {
		t.Error("validities for an org holding nothing = nil, want an empty map")
	}
}

// TestStubHolderClaimsWithSeveralCredentialsOfOneVCT is the read half of the
// "held-credential detail shows the wrong credential" fix. Two credentials of one
// vct must each resolve to their own attributes by ref, and a row whose ref no
// longer resolves must yield nothing rather than an arbitrary sibling's data.
func TestStubHolderClaimsWithSeveralCredentialsOfOneVCT(t *testing.T) {
	t.Parallel()
	h := eudiholder.NewStubHolder()
	ctx := context.Background()
	org := uuid.New()

	const vct = "nl.kvk.registration"
	refA, err := h.Store(ctx, org, eudiholder.Credential{
		VCT:              vct,
		ProcessedPayload: []byte(`{"vct":"nl.kvk.registration","company_name":"Alpha B.V."}`),
	})
	if err != nil {
		t.Fatalf("store A: %v", err)
	}
	refB, err := h.Store(ctx, org, eudiholder.Credential{
		VCT:              vct,
		ProcessedPayload: []byte(`{"vct":"nl.kvk.registration","company_name":"Beta B.V."}`),
	})
	if err != nil {
		t.Fatalf("store B: %v", err)
	}

	// Each ref resolves to its own credential, not the first one stored.
	for _, tc := range []struct{ ref, want string }{{refA, "Alpha B.V."}, {refB, "Beta B.V."}} {
		claims, err := h.Claims(ctx, org, tc.ref, vct, "en")
		if err != nil {
			t.Fatalf("claims %s: %v", tc.ref, err)
		}
		if got := attributeValue(claims.Attributes, "company_name"); got != tc.want {
			t.Errorf("claims[company_name] for ref %s = %v, want %q", tc.ref, got, tc.want)
		}
	}

	// With no usable ref the vct names both credentials, so it cannot say which row
	// was asked for: the fallback must decline rather than return either one.
	for _, ref := range []string{"", "no-longer-stored"} {
		claims, err := h.Claims(ctx, org, ref, vct, "en")
		if err != nil {
			t.Fatalf("claims ref %q: %v", ref, err)
		}
		if len(claims.Attributes) != 0 {
			t.Errorf("claims for ref %q = %v, want empty: the vct matches two credentials",
				ref, claims.Attributes)
		}
	}

	// Once only one credential of the type is left, the vct is a discriminator again
	// and the fallback recovers it (the case a ref-less legacy row relies on).
	if err := h.Delete(ctx, org, refB); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	claims, err := h.Claims(ctx, org, "", vct, "en")
	if err != nil {
		t.Fatalf("claims by vct: %v", err)
	}
	if got := attributeValue(claims.Attributes, "company_name"); got != "Alpha B.V." {
		t.Errorf("claims by vct[company_name] = %v, want the only remaining credential", got)
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
