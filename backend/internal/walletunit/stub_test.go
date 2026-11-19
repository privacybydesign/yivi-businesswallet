package walletunit

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type fixedOrgs struct{ org organization.Organization }

func (f fixedOrgs) GetByID(context.Context, uuid.UUID) (organization.Organization, error) {
	return f.org, nil
}

func stubKey(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &k.PublicKey
}

func stubClaims(t *testing.T, token string) jwt.MapClaims {
	t.Helper()
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		t.Fatalf("parse %q: %v", token, err)
	}
	return claims
}

func newTestStub(t *testing.T, org organization.Organization, ska *SKAConfig) *StubAttester {
	t.Helper()
	signer, err := NewEphemeralSigner("stub test")
	if err != nil {
		t.Fatal(err)
	}
	return NewStubAttester(fixedOrgs{org}, signer, Config{BaseURL: "https://wallet.example/", WalletName: "Test", SKA: ska})
}

func TestStubAttesterIssues(t *testing.T) {
	t.Parallel()
	org := organization.Organization{ID: uuid.New(), Name: "Stub BV", EUID: "NLNHR.12345678", Status: organization.StatusActive}
	stub := newTestStub(t, org, &SKAConfig{CertificationURL: "https://cert.example", KeyStorage: []string{"iso_18045_high"}, UserAuthentication: []string{"iso_18045_high"}})

	out, err := stub.Issue(context.Background(), org.ID, IssueRequest{InstanceKey: stubKey(t), AttestedKeys: []*ecdsa.PublicKey{stubKey(t)}, KeysInHSM: true})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	bwia := stubClaims(t, out.BWIA)
	if sub, _ := bwia["sub"].(string); !strings.HasPrefix(sub, "https://wallet.example/wallet-units/stub-") {
		t.Errorf("BWIA sub = %q, want a stub unit under the base URL", sub)
	}
	if bwia["ebwoid_id"] != org.EUID {
		t.Errorf("BWIA ebwoid_id = %v, want %q", bwia["ebwoid_id"], org.EUID)
	}
	if out.SKA == "" {
		t.Fatal("no SKA for HSM-held keys")
	}
	if ska := stubClaims(t, out.SKA); ska["certification"] != "https://cert.example" {
		t.Errorf("SKA certification = %v", ska["certification"])
	}
}

func TestStubAttesterRefusesInactiveOrg(t *testing.T) {
	t.Parallel()
	org := organization.Organization{ID: uuid.New(), EUID: "NLNHR.12345678", Status: organization.StatusSuspended}
	_, err := newTestStub(t, org, nil).Issue(context.Background(), org.ID, IssueRequest{InstanceKey: stubKey(t)})
	if !errors.Is(err, ErrOrgNotActive) {
		t.Fatalf("err = %v, want ErrOrgNotActive", err)
	}
}

func TestStubAttesterStatusList(t *testing.T) {
	t.Parallel()
	stub := newTestStub(t, organization.Organization{}, nil)
	token, err := stub.StatusList(context.Background(), 0)
	if err != nil {
		t.Fatalf("StatusList: %v", err)
	}
	if sub := stubClaims(t, token)["sub"]; sub != statusListURI("https://wallet.example", 0) {
		t.Errorf("status list sub = %v", sub)
	}
	if _, err := stub.StatusList(context.Background(), statusListCount); !errors.Is(err, ErrUnknownList) {
		t.Errorf("err = %v, want ErrUnknownList", err)
	}
}
