package wscawallet

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

const testKEK = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 32 bytes hex

func configuredStore(t *testing.T) *wsca.Store {
	t.Helper()
	cipher, err := crypto.NewCipher(testKEK)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	// nil db is fine: these tests exercise paths that fail before any DB access.
	return wsca.NewStore(nil, cipher)
}

// fakeWallet is an in-memory WalletClient for testing the Activator without a
// live WSCA.
type fakeWallet struct {
	activated                  bool
	certID                     string
	activateErr, changeErr     error
	activateCalls, changeCalls int
}

func (f *fakeWallet) IsActivated() bool { return f.activated }

func (f *fakeWallet) Activate(string) error {
	f.activateCalls++
	if f.activateErr != nil {
		return f.activateErr
	}
	f.activated, f.certID = true, "cert-1"
	return nil
}

func (f *fakeWallet) ChangePIN(string, string) error {
	f.changeCalls++
	if f.changeErr != nil {
		return f.changeErr
	}
	f.certID = "cert-2"
	return nil
}

func (f *fakeWallet) CertificateID() string { return f.certID }

// TestActivateNotConfigured: when WSCA is disabled, Activate short-circuits to
// ErrNotConfigured without touching the wallet (no DB needed).
func TestActivateNotConfigured(t *testing.T) {
	t.Parallel()
	fake := &fakeWallet{}
	a := NewActivator(false, func(uuid.UUID) (WalletClient, error) { return fake, nil }, configuredStore(t))

	if a.Configured() {
		t.Fatal("expected Configured() == false when WSCA is disabled")
	}
	if _, err := a.Activate(context.Background(), uuid.New(), "12345"); !errors.Is(err, wsca.ErrNotConfigured) {
		t.Fatalf("Activate err = %v, want ErrNotConfigured", err)
	}
	if fake.activateCalls != 0 {
		t.Errorf("wallet Activate must not be called when not configured")
	}
}

// TestActivateNotConfiguredNoKEK: enabled but no KEK is also not configured.
func TestActivateNotConfiguredNoKEK(t *testing.T) {
	t.Parallel()
	a := NewActivator(true, func(uuid.UUID) (WalletClient, error) { return &fakeWallet{}, nil }, wsca.NewStore(nil, nil))
	if a.Configured() {
		t.Fatal("expected Configured() == false with a nil-cipher store")
	}
	if _, err := a.Activate(context.Background(), uuid.New(), "12345"); !errors.Is(err, wsca.ErrNotConfigured) {
		t.Fatalf("Activate err = %v, want ErrNotConfigured", err)
	}
}

// TestActivateAlreadyActivated: an already-activated wallet is not re-activated.
func TestActivateAlreadyActivated(t *testing.T) {
	t.Parallel()
	fake := &fakeWallet{activated: true, certID: "cert-1"}
	a := NewActivator(true, func(uuid.UUID) (WalletClient, error) { return fake, nil }, configuredStore(t))

	if _, err := a.Activate(context.Background(), uuid.New(), "12345"); !errors.Is(err, ErrAlreadyActivated) {
		t.Fatalf("Activate err = %v, want ErrAlreadyActivated", err)
	}
	if fake.activateCalls != 0 {
		t.Errorf("wallet Activate should not be called when already activated")
	}
}
