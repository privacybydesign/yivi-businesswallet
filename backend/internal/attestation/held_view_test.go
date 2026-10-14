package attestation_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
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
	rows          []attestation.HeldAttestation
	statusChanges []heldStatusChange
	// lastRevoked is each credential's last audited status, valid when absent.
	lastRevoked map[uuid.UUID]bool
	// failing makes recording a status change fail for these credentials.
	failing map[uuid.UUID]bool
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

func (s *heldViewStore) RecordHeldStatusChange(ctx context.Context, _, id uuid.UUID, _ string, status attestation.HeldStatus) (bool, error) {
	if s.failing[id] {
		return false, errRecordStatus
	}
	revoked := status == attestation.HeldRevoked
	if s.lastRevoked[id] == revoked {
		return false, nil
	}
	if s.lastRevoked == nil {
		s.lastRevoked = map[uuid.UUID]bool{}
	}
	s.lastRevoked[id] = revoked
	_, hasActor := audit.ActorFromContext(ctx)
	s.statusChanges = append(s.statusChanges, heldStatusChange{id: id, revoked: revoked, hasActor: hasActor})
	return true, nil
}

var errRecordStatus = errors.New("record status change failed")

func (s *heldViewStore) HeldHistory(context.Context, uuid.UUID, uuid.UUID) ([]audit.Event, error) {
	return []audit.Event{}, nil
}

func (s *heldViewStore) HolderOrgs(context.Context) ([]uuid.UUID, error) { return nil, nil }

type heldStatusChange struct {
	id       uuid.UUID
	revoked  bool
	hasActor bool
}

// heldViewHolder is a holder engine whose display and validity answers are fixtures.
type heldViewHolder struct {
	displays   map[string]eudiholder.HeldDisplay
	validities map[string]eudiholder.HeldValidity
	// refreshed is what a status refresh makes the validities read, per ref.
	refreshed map[string]eudiholder.HeldValidity
	// refreshErr is what a refresh returns after writing refreshed back, as
	// irmago's does when its context ends mid-sweep.
	refreshErr error
	// refreshedRefs lists the refs a refresh targeted, nil for the whole org.
	refreshedRefs []string
}

func (h *heldViewHolder) RefreshStatuses(context.Context, uuid.UUID) (int, error) {
	maps.Copy(h.validities, h.refreshed)
	return len(h.refreshed), h.refreshErr
}

func (h *heldViewHolder) RefreshStatus(_ context.Context, _ uuid.UUID, ref string) (int, error) {
	h.refreshedRefs = append(h.refreshedRefs, ref)
	v, ok := h.refreshed[ref]
	if !ok {
		return 0, h.refreshErr
	}
	h.validities[ref] = v
	return 1, h.refreshErr
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

// statusListed is a validity read from an issuer status list.
func statusListed(revoked bool) eudiholder.HeldValidity {
	return eudiholder.HeldValidity{HasStatusList: true, Revoked: revoked}
}

// A re-check audits exactly the credentials whose status differs from their
// last audited one, in either direction; a re-confirmed one is not audited, nor
// is the caller recorded as having moved it.
func TestRecheckAuditsStatusChange(t *testing.T) {
	t.Parallel()
	flipped := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-flipped", VCT: "eaa.supplier"}
	lifted := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-lifted", VCT: "eaa.supplier"}
	steady := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-steady", VCT: "eaa.supplier"}
	store := &heldViewStore{
		rows:        []attestation.HeldAttestation{flipped, lifted, steady},
		lastRevoked: map[uuid.UUID]bool{lifted.ID: true},
	}
	holder := &heldViewHolder{
		validities: map[string]eudiholder.HeldValidity{
			"ref-flipped": statusListed(false), "ref-lifted": statusListed(true), "ref-steady": statusListed(false),
		},
		refreshed: map[string]eudiholder.HeldValidity{
			"ref-flipped": statusListed(true), "ref-lifted": statusListed(false), "ref-steady": statusListed(false),
		},
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{UserID: uuid.New()})

	changed, err := service.RecheckHeld(ctx, uuid.New())
	if err != nil || changed != 2 {
		t.Fatalf("RecheckHeld = %d, %v; want 2 changes", changed, err)
	}
	want := map[uuid.UUID]bool{flipped.ID: true, lifted.ID: false}
	if len(store.statusChanges) != len(want) {
		t.Fatalf("audited %v, want the flipped and the lifted credential", store.statusChanges)
	}
	for _, c := range store.statusChanges {
		if revoked, ok := want[c.id]; !ok || revoked != c.revoked {
			t.Errorf("audited %+v, want %v", c, want)
		}
		if c.hasActor {
			t.Errorf("audited %+v with the caller as actor, want none: the issuer moved it", c)
		}
	}
}

// A refresh cut short keeps the statuses it already wrote, so those are
// audited all the same, and the cut is reported.
func TestRecheckAuditsCutShortRefresh(t *testing.T) {
	t.Parallel()
	flipped := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-flipped", VCT: "eaa.supplier"}
	store := &heldViewStore{rows: []attestation.HeldAttestation{flipped}}
	holder := &heldViewHolder{
		validities: map[string]eudiholder.HeldValidity{"ref-flipped": statusListed(false)},
		refreshed:  map[string]eudiholder.HeldValidity{"ref-flipped": statusListed(true)},
		refreshErr: context.DeadlineExceeded,
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	changed, err := service.RecheckHeld(context.Background(), uuid.New())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RecheckHeld error = %v, want the refresh's deadline", err)
	}
	if changed != 1 || len(store.statusChanges) != 1 || store.statusChanges[0].id != flipped.ID {
		t.Errorf("RecheckHeld = %d, audited %v; want the flipped credential", changed, store.statusChanges)
	}
}

// A status the holder already has but no audit recorded (an earlier re-check
// failed to record it) is audited by the next re-check, though the refresh
// itself moves nothing; credentials without a status list are left alone.
func TestRecheckAuditsEarlierUnrecordedChange(t *testing.T) {
	t.Parallel()
	failing := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-failing", VCT: "eaa.supplier"}
	pending := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-pending", VCT: "eaa.supplier"}
	unlisted := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-unlisted", VCT: "eaa.supplier"}
	store := &heldViewStore{
		rows:    []attestation.HeldAttestation{failing, pending, unlisted},
		failing: map[uuid.UUID]bool{failing.ID: true},
	}
	holder := &heldViewHolder{validities: map[string]eudiholder.HeldValidity{
		"ref-failing": statusListed(true), "ref-pending": statusListed(true), "ref-unlisted": {Revoked: true},
	}}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	changed, err := service.RecheckHeld(context.Background(), uuid.New())
	if !errors.Is(err, errRecordStatus) {
		t.Errorf("RecheckHeld error = %v, want the failed record", err)
	}
	if changed != 1 || len(store.statusChanges) != 1 || store.statusChanges[0].id != pending.ID {
		t.Errorf("RecheckHeld = %d, audited %v; want only the pending credential", changed, store.statusChanges)
	}
}

// A credential's own re-check refreshes only that credential's status list and
// reports its change.
func TestRecheckHeldOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	flipped := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-flipped", VCT: "eaa.supplier"}
	steady := attestation.HeldAttestation{ID: uuid.New(), CredentialRef: "ref-steady", VCT: "eaa.supplier"}
	store := &heldViewStore{rows: []attestation.HeldAttestation{flipped, steady}}
	holder := &heldViewHolder{
		validities: map[string]eudiholder.HeldValidity{"ref-flipped": statusListed(false), "ref-steady": statusListed(false)},
		refreshed:  map[string]eudiholder.HeldValidity{"ref-flipped": statusListed(true), "ref-steady": statusListed(false)},
	}
	service := attestation.NewService(nil, nil, nil, nil, nil, store, nil, holder, "http://app.test")

	changed, err := service.RecheckHeldOne(ctx, uuid.New(), steady.ID)
	if err != nil || changed {
		t.Fatalf("RecheckHeldOne(steady) = %v, %v; want false", changed, err)
	}
	if !slices.Equal(holder.refreshedRefs, []string{"ref-steady"}) {
		t.Errorf("refreshed %v, want only ref-steady", holder.refreshedRefs)
	}
	if len(store.statusChanges) != 0 {
		t.Errorf("audited %v, want nothing: the flipped credential's list was not re-read", store.statusChanges)
	}

	changed, err = service.RecheckHeldOne(ctx, uuid.New(), flipped.ID)
	if err != nil || !changed {
		t.Fatalf("RecheckHeldOne(flipped) = %v, %v; want true", changed, err)
	}
}
