package eudiholder

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"testing"

	"github.com/google/uuid"
)

type fakeWalletUnit struct {
	bwiaErr error
	skaKeys [][]*ecdsa.PublicKey
}

func (f *fakeWalletUnit) IssueBWIA(context.Context, uuid.UUID, *ecdsa.PublicKey) (string, error) {
	if f.bwiaErr != nil {
		return "", f.bwiaErr
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"https://bw.example/wallet-units/1"}`))
	return "e30." + payload + ".sig", nil
}

func (f *fakeWalletUnit) IssueSKA(_ context.Context, _ uuid.UUID, keys []*ecdsa.PublicKey) (string, error) {
	f.skaKeys = append(f.skaKeys, keys)
	return "ska", nil
}

func TestOrgWalletAttesterClientAttestation(t *testing.T) {
	t.Parallel()
	a := newOrgWalletAttester(context.Background(), uuid.New(), &fakeWalletUnit{}, false)
	ca, err := a.ClientAttestation("https://issuer.example")
	if err != nil {
		t.Fatal(err)
	}
	if ca.ClientID != "https://bw.example/wallet-units/1" {
		t.Errorf("ClientID = %q, want the BWIA sub", ca.ClientID)
	}
	sig, err := ca.Sign([]byte("input"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("input"))
	r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(ca.Key, digest[:], r, s) {
		t.Error("Sign does not sign with the attested instance key")
	}
}

func TestOrgWalletAttesterNotAttestable(t *testing.T) {
	t.Parallel()
	a := newOrgWalletAttester(context.Background(), uuid.New(), &fakeWalletUnit{bwiaErr: fmt.Errorf("%w: no EUID", ErrNotAttestable)}, false)
	ca, err := a.ClientAttestation("https://issuer.example")
	if err != nil || ca != nil {
		t.Errorf("got %v, %v; want no attestation and no error", ca, err)
	}

	a = newOrgWalletAttester(context.Background(), uuid.New(), &fakeWalletUnit{bwiaErr: fmt.Errorf("db down")}, false)
	if _, err := a.ClientAttestation("https://issuer.example"); err == nil {
		t.Error("another error was swallowed")
	}
}

func TestOrgWalletAttesterKeyAttestationOnlyForHSMKeys(t *testing.T) {
	t.Parallel()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unit := &fakeWalletUnit{}
	a := newOrgWalletAttester(context.Background(), uuid.New(), unit, false)
	if ka, _ := a.KeyAttestation("https://issuer.example", &k.PublicKey, nil); ka != "" || len(unit.skaKeys) != 0 {
		t.Errorf("a software key got an SKA: %q", ka)
	}
	a.keysInHSM = true
	if ka, _ := a.KeyAttestation("https://issuer.example", &k.PublicKey, nil); ka != "ska" || len(unit.skaKeys) != 1 || !unit.skaKeys[0][0].Equal(&k.PublicKey) {
		t.Errorf("an HSM key got %q for %v", ka, unit.skaKeys)
	}
}
