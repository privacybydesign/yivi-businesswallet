package attestation_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

// The held view badges each credential Valid / Expired / Revoked, and the facts
// behind that badge live in the holder engine (the index row carries neither an
// expiry nor a status). These tests pin the mapping: the engine answers per
// credential-instance ref, the held row carries that ref, and a row whose ref the
// engine does not know must come back without an expiry rather than borrowing
// another credential's.

// heldViewStore is an in-memory held index implementing the service's held seam.
type heldViewStore struct {
	rows []attestation.HeldAttestation
}

func (s *heldViewStore) ListHeld(_ context.Context, _ uuid.UUID) ([]attestation.HeldAttestation, error) {
	return s.rows, nil
}

func (s *heldViewStore) GetHeld(_ context.Context, _, id uuid.UUID) (attestation.HeldAttestation, error) {
	for _, row := range s.rows {
		if row.ID == id {
			return row, nil
		}
	}
	return attestation.HeldAttestation{}, attestation.ErrHeldNotFound
}

func (s *heldViewStore) SoftDeleteHeld(_ context.Context, _, _ uuid.UUID) error {
	return nil
}

// heldViewHolder is a holder engine whose display and validity answers are fixtures.
type heldViewHolder struct {
	displays   map[string]eudiholder.HeldDisplay
	validities map[string]eudiholder.HeldValidity
}

func (*heldViewHolder) Redeem(_ context.Context, _ uuid.UUID, _ string) (eudiholder.Redeemed, error) {
	return eudiholder.Redeemed{}, nil
}

func (*heldViewHolder) Delete(_ context.Context, _ uuid.UUID, _ string) error { return nil }

func (*heldViewHolder) Claims(_ context.Context, _ uuid.UUID, _, _, _ string) (eudiholder.HeldCredential, error) {
	return eudiholder.HeldCredential{Attributes: []eudiholder.HeldAttribute{}}, nil
}

func (h *heldViewHolder) Displays(_ context.Context, _ uuid.UUID, _ string) (map[string]eudiholder.HeldDisplay, error) {
	return h.displays, nil
}

func (h *heldViewHolder) Validities(_ context.Context, _ uuid.UUID) (map[string]eudiholder.HeldValidity, error) {
	return h.validities, nil
}

func TestListHeldCarriesEachCredentialsValidity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	org := uuid.New()

	expires := time.Date(2027, time.June, 1, 9, 0, 0, 0, time.UTC)
	expiring := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-expiring", VCT: "nl.kvk.registration"}
	revoked := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-revoked", VCT: "eaa.supplier"}
	// A row received before instance refs were captured: the engine knows no such ref.
	refless := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "demo-kvk-registration", VCT: "nl.kvk.registration"}

	store := &heldViewStore{rows: []attestation.HeldAttestation{expiring, revoked, refless}}
	holder := &heldViewHolder{
		displays: map[string]eudiholder.HeldDisplay{
			"nl.kvk.registration": {DisplayName: "KVK registration"},
		},
		validities: map[string]eudiholder.HeldValidity{
			"ref-expiring": {ExpiresAt: &expires},
			"ref-revoked":  {Revoked: true},
		},
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	views, err := service.ListHeld(ctx, org, "en")
	if err != nil {
		t.Fatalf("list held: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("list held returned %d rows, want 3", len(views))
	}

	byID := map[uuid.UUID]attestation.HeldListView{}
	for _, view := range views {
		byID[view.ID] = view
	}

	if got := byID[expiring.ID].ExpiresAt; got == nil || !got.Equal(expires) {
		t.Errorf("expiring row ExpiresAt = %v, want %v", got, expires)
	}
	if byID[expiring.ID].Revoked {
		t.Error("expiring row Revoked = true, want false")
	}
	if got := byID[expiring.ID].DisplayName; got != "KVK registration" {
		t.Errorf("expiring row DisplayName = %q, want the engine's localized title", got)
	}
	if !byID[revoked.ID].Revoked {
		t.Error("revoked row Revoked = false, want true")
	}
	if got := byID[revoked.ID].ExpiresAt; got != nil {
		t.Errorf("revoked row ExpiresAt = %v, want nil: the engine stored no expiry", got)
	}
	// The ref-less row shares its vct with the expiring one, so a vct-keyed lookup
	// would hand it that credential's expiry.
	if got := byID[refless.ID].ExpiresAt; got != nil {
		t.Errorf("ref-less row ExpiresAt = %v, want nil: the engine knows no such ref", got)
	}
	if byID[refless.ID].Revoked {
		t.Error("ref-less row Revoked = true, want false: an unknown ref says nothing")
	}
}

func TestListHeldCarriesIssuerName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	org := uuid.New()

	// One vct the engine resolves an issuer name for, one it does not.
	named := attestation.HeldAttestation{
		ID:     uuid.New(),
		VCT:    "https://veramo-issuer.test/vct/nl-yivi-supplier",
		Issuer: "https://veramo-issuer.test/yivi",
	}
	unnamed := attestation.HeldAttestation{
		ID:     uuid.New(),
		VCT:    "nl.kvk.registration",
		Issuer: "KVK",
	}

	store := &heldViewStore{rows: []attestation.HeldAttestation{named, unnamed}}
	holder := &heldViewHolder{
		displays: map[string]eudiholder.HeldDisplay{
			named.VCT: {DisplayName: "Approved supplier", IssuerName: "Yivi B.V."},
			// unnamed.VCT: no display entry -> empty IssuerName.
		},
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	views, err := service.ListHeld(ctx, org, "en")
	if err != nil {
		t.Fatalf("list held: %v", err)
	}
	byID := map[uuid.UUID]attestation.HeldListView{}
	for _, view := range views {
		byID[view.ID] = view
	}
	if got := byID[named.ID].IssuerName; got != "Yivi B.V." {
		t.Errorf("named row IssuerName = %q, want the engine's localized issuer name", got)
	}
	// No issuer display metadata: the list falls back to the raw issuer identifier,
	// the same rule the detail view follows.
	if got := byID[unnamed.ID].IssuerName; got != unnamed.Issuer {
		t.Errorf("unnamed row IssuerName = %q, want the issuer identifier %q", got, unnamed.Issuer)
	}
}

func TestHeldClaimsCarriesValidity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	org := uuid.New()

	expires := time.Date(2026, time.September, 30, 23, 59, 0, 0, time.UTC)
	row := attestation.HeldAttestation{
		ID:            uuid.New(),
		CredentialRef: "ref-detail",
		VCT:           "eaa.supplier",
		Issuer:        "https://issuer.test",
		Source:        attestation.HeldSourceQERDS,
		ReceivedAt:    time.Date(2026, time.January, 5, 8, 0, 0, 0, time.UTC),
	}
	store := &heldViewStore{rows: []attestation.HeldAttestation{row}}
	holder := &heldViewHolder{
		validities: map[string]eudiholder.HeldValidity{
			"ref-detail": {ExpiresAt: &expires, Revoked: true},
		},
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	view, err := service.HeldClaims(ctx, org, row.ID, "en")
	if err != nil {
		t.Fatalf("held claims: %v", err)
	}
	if got := view.ExpiresAt; got == nil || !got.Equal(expires) {
		t.Errorf("detail ExpiresAt = %v, want %v", got, expires)
	}
	if !view.Revoked {
		t.Error("detail Revoked = false, want true")
	}
	// The issuer display name is empty in the engine fixture, so the detail falls
	// back to the issuer identifier the index row carries.
	if view.IssuerName != row.Issuer {
		t.Errorf("detail IssuerName = %q, want the issuer identifier %q", view.IssuerName, row.Issuer)
	}
}
