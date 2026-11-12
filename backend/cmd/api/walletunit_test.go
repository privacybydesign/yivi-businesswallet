package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/config"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wsca"
)

func TestNewWalletUnitAttesterFollowsConfig(t *testing.T) {
	t.Parallel()
	signer, err := walletunit.NewEphemeralSigner("test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := newWalletUnitAttester(config.Config{WalletUnitAttester: config.ProviderStub}, nil, nil, signer).(*walletunit.StubAttester); !ok {
		t.Error("stub config did not select the StubAttester")
	}
	if _, ok := newWalletUnitAttester(config.Config{WalletUnitAttester: config.WalletUnitAttesterLocal}, nil, nil, signer).(*walletunit.Provider); !ok {
		t.Error("local config did not select the Provider")
	}
}

type fakeWSCA struct{ err error }

func (f fakeWSCA) Get(context.Context, uuid.UUID) (wsca.Account, error) { return wsca.Account{}, f.err }

func TestWalletUnitKeysOnlyForAnSKA(t *testing.T) {
	t.Parallel()
	levels := []string{"iso_18045_high"}
	withWSCA := config.Config{AttestationHolder: config.HolderIrmago, AttestationHolderWSCAURL: "https://wsca.example", WalletUnitSKAKeyStorage: levels}
	stubHolder := withWSCA
	stubHolder.AttestationHolder = config.HolderStub
	tests := []struct {
		name    string
		cfg     config.Config
		account error
		want    []string
	}{
		{"activated WSCA with an HSM described", withWSCA, nil, levels},
		{"org did not activate its WSCA", withWSCA, wsca.ErrNotActivated, nil},
		{"no HSM described", config.Config{AttestationHolder: config.HolderIrmago, AttestationHolderWSCAURL: "https://wsca.example"}, nil, nil},
		{"stub holder keeps software keys", stubHolder, nil, nil},
		{"no WSCA on the deployment", config.Config{WalletUnitSKAKeyStorage: levels}, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := newWalletUnitKeys(tc.cfg, fakeWSCA{tc.account}).KeyAttestation(context.Background(), uuid.New())
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want no key attestation", got)
				}
				return
			}
			if got == nil || !slices.Equal(got.Levels, tc.want) {
				t.Fatalf("got %+v, want levels %v", got, tc.want)
			}
		})
	}

	if _, err := newWalletUnitKeys(withWSCA, fakeWSCA{errors.New("db down")}).KeyAttestation(context.Background(), uuid.New()); err == nil {
		t.Error("a failed account lookup was swallowed")
	}
}
