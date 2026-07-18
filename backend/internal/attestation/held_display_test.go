package attestation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/privacybydesign/irmago/common/clientmodels"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
)

type fakeHeldList struct {
	rows []attestation.HeldAttestation
}

func (f *fakeHeldList) ListHeld(context.Context, uuid.UUID) ([]attestation.HeldAttestation, error) {
	return f.rows, nil
}

func (f *fakeHeldList) GetHeld(context.Context, uuid.UUID, uuid.UUID) (attestation.HeldAttestation, error) {
	return attestation.HeldAttestation{}, nil
}

func (f *fakeHeldList) SoftDeleteHeld(context.Context, uuid.UUID, uuid.UUID) error { return nil }

type fakeHolderList struct {
	creds []*clientmodels.Credential
}

func (f *fakeHolderList) List(context.Context, uuid.UUID) ([]*clientmodels.Credential, error) {
	return f.creds, nil
}

func (f *fakeHolderList) Delete(context.Context, uuid.UUID, string) error { return nil }

// TestListHeldDisplayMergesAndFallsBack proves the display list correlates index
// rows with the holder engine's clientmodels by credential ref, and synthesises a
// minimal display from the index when the engine has no data for a row.
func TestListHeldDisplayMergesAndFallsBack(t *testing.T) {
	matched := attestation.HeldAttestation{
		ID: uuid.New(), CredentialRef: "ref-1", VCT: "nl.kvk.registration",
		Issuer: "https://issuer.test", Source: attestation.HeldSourceQERDS, ReceivedAt: time.Now(),
	}
	orphan := attestation.HeldAttestation{
		ID: uuid.New(), CredentialRef: "ref-missing", VCT: "nl.kvk.extract",
		Issuer: "https://issuer.test", Source: attestation.HeldSourceBootstrap, ReceivedAt: time.Now(),
	}
	held := &fakeHeldList{rows: []attestation.HeldAttestation{matched, orphan}}
	holder := &fakeHolderList{creds: []*clientmodels.Credential{{
		CredentialId:          "nl.kvk.registration",
		Name:                  clientmodels.TranslatedString{"en": "KVK registration"},
		CredentialInstanceIds: map[clientmodels.CredentialFormat]string{clientmodels.Format_SdJwtVc: "ref-1"},
	}}}

	svc := attestation.NewService(nil, nil, nil, nil, nil, held, holder, "")

	views, err := svc.ListHeldDisplay(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListHeldDisplay: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}

	// Row 0 is enriched with the engine's display model.
	if views[0].HeldID != matched.ID {
		t.Errorf("view[0] HeldID = %v, want %v", views[0].HeldID, matched.ID)
	}
	if views[0].Credential == nil || views[0].Credential.Name["en"] != "KVK registration" {
		t.Errorf("view[0] should carry the engine display name, got %+v", views[0].Credential)
	}

	// Row 1 has no engine data → minimal fallback built from the index.
	if views[1].Credential == nil {
		t.Fatal("view[1] Credential must not be nil (index is source of truth)")
	}
	if views[1].Credential.CredentialId != orphan.VCT {
		t.Errorf("view[1] fallback CredentialId = %q, want %q", views[1].Credential.CredentialId, orphan.VCT)
	}
	if views[1].Credential.Name["en"] != orphan.VCT {
		t.Errorf("view[1] fallback name = %q, want %q", views[1].Credential.Name["en"], orphan.VCT)
	}
}
