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

	// The redeemed credential is now held: deleting its ref is a no-op success.
	if err := h.Delete(ctx, org, got.Ref); err != nil {
		t.Fatalf("delete redeemed ref: %v", err)
	}
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
