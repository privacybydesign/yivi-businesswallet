package attestation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
)

// fakeIssuer records the revoke-credential calls the service makes and can be
// told to fail, so the test can assert the local ledger is only flipped once the
// issuer's status list is updated.
type fakeIssuer struct {
	revoked      []string // credential uuids passed to RevokeCredential
	revokeErr    error
	lastInstance string
}

func (f *fakeIssuer) CreateOffer(context.Context, openid4vciissuer.OfferRequest) (openid4vciissuer.Offer, error) {
	return openid4vciissuer.Offer{}, nil
}

func (f *fakeIssuer) Status(context.Context, string, string) (openid4vciissuer.IssuanceStatus, error) {
	return openid4vciissuer.IssuanceStatus{}, nil
}

func (f *fakeIssuer) RevokeCredential(_ context.Context, instance, credentialUUID string) error {
	f.lastInstance = instance
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked = append(f.revoked, credentialUUID)
	return nil
}

// fakeStore is a minimal issuedStore holding a single row, enough to drive
// Service.Revoke. Only GetIssued and Revoke do real work.
type fakeStore struct {
	row      Issued
	revoked  bool // whether the local Revoke ran
	notFound bool
}

func (s *fakeStore) GetIssued(_ context.Context, _, _ uuid.UUID) (Issued, error) {
	if s.notFound {
		return Issued{}, ErrIssuedNotFound
	}
	return s.row, nil
}

func (s *fakeStore) Revoke(_ context.Context, _, _ uuid.UUID) (Issued, error) {
	if !isRevocable(s.row.Status) {
		return Issued{}, ErrNotOfferable
	}
	s.revoked = true
	s.row.Status = StatusRevoked
	now := time.Now()
	s.row.RevokedAt = &now
	return s.row, nil
}

func (s *fakeStore) GetTemplateDetail(context.Context, uuid.UUID, uuid.UUID) (TemplateDetail, error) {
	return TemplateDetail{}, nil
}

func (s *fakeStore) CreateOffered(context.Context, uuid.UUID, IssueInput, TemplateDetail, uuid.UUID, *time.Time, string, string) (Issued, error) {
	return Issued{}, nil
}

func (s *fakeStore) SetOffer(context.Context, uuid.UUID, uuid.UUID, string, string, string) error {
	return nil
}
func (s *fakeStore) MarkFailed(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (s *fakeStore) MarkClaimed(context.Context, uuid.UUID, uuid.UUID, string) (Issued, error) {
	return Issued{}, nil
}
func (s *fakeStore) GetClaim(context.Context, string) (claimRow, error) { return claimRow{}, nil }

type fakeInstances struct{ name string }

func (f fakeInstances) InstanceFor(context.Context, uuid.UUID) (string, error) { return f.name, nil }

func newRevokeService(store *fakeStore, iss *fakeIssuer) *Service {
	return NewService(store, iss, fakeInstances{name: "org-yivi"}, nil, nil, nil, nil, "http://app.test")
}

// TestRevokePropagatesToIssuer asserts a claimed credential's revocation is
// pushed to the issuer's status list (keyed on the captured credential uuid,
// routed to the org's instance) before the local ledger is flipped.
func TestRevokePropagatesToIssuer(t *testing.T) {
	store := &fakeStore{row: Issued{Status: StatusClaimed, CredentialUUID: "cred-1"}}
	iss := &fakeIssuer{}
	svc := newRevokeService(store, iss)

	revoked, err := svc.Revoke(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.Status != StatusRevoked {
		t.Fatalf("expected local status revoked, got %q", revoked.Status)
	}
	if len(iss.revoked) != 1 || iss.revoked[0] != "cred-1" {
		t.Fatalf("issuer status list not updated with the credential uuid: %+v", iss.revoked)
	}
	if iss.lastInstance != "org-yivi" {
		t.Fatalf("issuer revoke not routed to the org instance: %q", iss.lastInstance)
	}
}

// TestRevokeAbortsWhenIssuerFails asserts that if the issuer status-list update
// fails, the local ledger is left untouched (local and published must not drift).
func TestRevokeAbortsWhenIssuerFails(t *testing.T) {
	store := &fakeStore{row: Issued{Status: StatusClaimed, CredentialUUID: "cred-1"}}
	iss := &fakeIssuer{revokeErr: errors.New("status list unreachable")}
	svc := newRevokeService(store, iss)

	if _, err := svc.Revoke(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatalf("expected error when issuer revoke fails")
	}
	if store.revoked {
		t.Fatalf("local ledger was flipped despite the issuer revoke failing")
	}
}

// TestRevokeDegradesWhenNoStatusListBit asserts that in a deployment issuing
// without a Token Status List — where a claimed credential has a captured uuid
// but the issuer reserved no bit and answers UNKNOWN — the revoke degrades to a
// local-only flip instead of hard-failing, so an admin can still revoke.
func TestRevokeDegradesWhenNoStatusListBit(t *testing.T) {
	store := &fakeStore{row: Issued{Status: StatusClaimed, CredentialUUID: "cred-1"}}
	iss := &fakeIssuer{revokeErr: openid4vciissuer.ErrNoStatusListBit}
	svc := newRevokeService(store, iss)

	revoked, err := svc.Revoke(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("Revoke should degrade to local-only when the issuer has no status-list bit, got: %v", err)
	}
	if revoked.Status != StatusRevoked {
		t.Fatalf("expected local status revoked, got %q", revoked.Status)
	}
	if !store.revoked {
		t.Fatalf("local ledger should still be flipped when the issuer has no status-list bit")
	}
}

// TestRevokeOfferedSkipsIssuer asserts an offered row (nothing published yet, no
// credential uuid) is revoked locally without an issuer call.
func TestRevokeOfferedSkipsIssuer(t *testing.T) {
	store := &fakeStore{row: Issued{Status: StatusOffered}}
	iss := &fakeIssuer{}
	svc := newRevokeService(store, iss)

	if _, err := svc.Revoke(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(iss.revoked) != 0 {
		t.Fatalf("issuer should not be called for an offered row: %+v", iss.revoked)
	}
	if !store.revoked {
		t.Fatalf("offered row should still be revoked locally")
	}
}
