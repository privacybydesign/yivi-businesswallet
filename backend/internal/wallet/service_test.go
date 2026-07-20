package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

// fakeRegistry returns a scripted consult result, so the service's handling of
// each KVK decision (validate / bounce / unknown) can be exercised in isolation.
type fakeRegistry struct {
	att registryprovider.RegistrationAttestation
	err error
	got registryprovider.ConsultRequest
}

func (f *fakeRegistry) Consult(_ context.Context, req registryprovider.ConsultRequest) (registryprovider.RegistrationAttestation, error) {
	f.got = req
	return f.att, f.err
}

// serviceWith builds a Service whose only wired dependency is the registry; the
// bounce paths under test return before any store/inbox is touched.
func serviceWith(reg registry) *Service {
	return NewService(nil, reg, nil, nil, nil, "qerds.localhost")
}

func TestOpenWalletBouncesUnknownKVK(t *testing.T) {
	reg := &fakeRegistry{err: registryprovider.ErrUnknownKVK}
	_, err := serviceWith(reg).OpenWallet(context.Background(), uuid.New(),
		Requester{GivenNames: "Alice", FamilyName: "Owner"}, "00000001", "acme")
	if !errors.Is(err, ErrUnknownKVK) {
		t.Fatalf("err = %v, want ErrUnknownKVK", err)
	}
}

func TestOpenWalletBouncesNonRepresentative(t *testing.T) {
	reg := &fakeRegistry{att: registryprovider.RegistrationAttestation{
		KVKNumber:                 "90000010",
		LegalName:                 "Yivi B.V.",
		RequesterIsRepresentative: false,
	}}
	_, err := serviceWith(reg).OpenWallet(context.Background(), uuid.New(),
		Requester{GivenNames: "Mallory", FamilyName: "Impostor"}, "90000010", "acme")
	if !errors.Is(err, ErrNotRepresentative) {
		t.Fatalf("err = %v, want ErrNotRepresentative", err)
	}
}

func TestOpenWalletForwardsIdentificationData(t *testing.T) {
	reg := &fakeRegistry{err: registryprovider.ErrUnknownKVK}
	_, _ = serviceWith(reg).OpenWallet(context.Background(), uuid.New(),
		Requester{GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02"}, "90000010", "acme")
	want := registryprovider.ConsultRequest{
		KVKNumber: "90000010", GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02",
	}
	if reg.got != want {
		t.Fatalf("consult request = %+v, want %+v", reg.got, want)
	}
}
