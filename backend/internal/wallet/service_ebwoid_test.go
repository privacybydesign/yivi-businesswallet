package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
)

const testKVK = "90000010"

func validatedAttestation() registryprovider.RegistrationAttestation {
	return registryprovider.RegistrationAttestation{
		KVKNumber:                 testKVK,
		LegalName:                 "Register Name B.V.",
		EUID:                      "NLNHR." + testKVK,
		RequesterIsRepresentative: true,
		Representatives: []registryprovider.Representative{
			{Kind: registryprovider.KindBestuurder, GivenNames: "Alice", FamilyName: "Owner", Authority: registryprovider.AuthoritySole},
		},
	}
}

// claimsIssuer stores an EBWOID with the given claims, like a real issuer would.
type claimsIssuer struct {
	claims map[string]string
	err    error
}

func (c claimsIssuer) Obtain(ctx context.Context, h registryprovider.EBWOIDHolder, orgID uuid.UUID, _ registryprovider.RegistrationAttestation) (registryprovider.HeldEBWOID, error) {
	payload, err := json.Marshal(c.claims)
	if err != nil {
		return registryprovider.HeldEBWOID{}, err
	}
	ref, err := h.Store(ctx, orgID, eudiholder.Credential{
		VCT: registryprovider.EBWOIDVCT, IssuerURL: "https://ebwoid.test", CredentialIssuer: "https://ebwoid.test",
		ProcessedPayload: payload, IssuedAt: time.Now(),
	})
	if err != nil {
		return registryprovider.HeldEBWOID{}, err
	}
	return registryprovider.HeldEBWOID{Ref: ref, Issuer: "https://ebwoid.test"}, c.err
}

type recordingStore struct {
	walletStore
	got Registration
	err error
}

func (r *recordingStore) RegisterOrganization(_ context.Context, reg Registration) (organization.Organization, error) {
	r.got = reg
	if r.err != nil {
		return organization.Organization{}, r.err
	}
	return organization.Organization{ID: reg.OrgID, Name: reg.Attestation.LegalName, EUID: reg.Attestation.EUID}, nil
}

type nopInbox struct{}

func (nopInbox) CreateInbound(context.Context, uuid.UUID, qerdsprovider.InboundMessage) (qerds.Message, bool, error) {
	return qerds.Message{}, true, nil
}

func ebwoidService(store walletStore, issuer ownerIDIssuer, holder ownerIDHolder) *Service {
	return NewService(store, &fakeRegistry{att: validatedAttestation()}, OwnerID{Issuer: issuer, Holder: holder}, nil, nil, nopInbox{}, "qerds.localhost")
}

func openTestWallet(svc *Service) (RegistrationResult, error) {
	return svc.OpenWallet(context.Background(), uuid.New(), Requester{GivenNames: "Alice", FamilyName: "Owner"}, testKVK, "acme")
}

// heldCount reports how many credentials orgID holds.
func heldCount(t *testing.T, h *eudiholder.StubHolder, orgID uuid.UUID) int {
	t.Helper()
	v, err := h.Validities(context.Background(), orgID)
	if err != nil {
		t.Fatal(err)
	}
	return len(v)
}

func TestOpenWalletTakesIdentityFromEBWOID(t *testing.T) {
	store := &recordingStore{}
	holder := eudiholder.NewStubHolder()
	issuer := claimsIssuer{claims: map[string]string{
		"vct": registryprovider.EBWOIDVCT, "id": "NLNHR." + testKVK + ".00001", "name": "EBWOID Name B.V.",
	}}

	res, err := openTestWallet(ebwoidService(store, issuer, holder))
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	if res.Organization.Name != "EBWOID Name B.V." || res.Organization.EUID != "NLNHR."+testKVK+".00001" {
		t.Fatalf("org identity = %q/%q, want the EBWOID's", res.Organization.Name, res.Organization.EUID)
	}
	if store.got.EBWOID.Ref == "" || store.got.OrgID == uuid.Nil {
		t.Fatalf("registration = %+v, want the held EBWOID and org id", store.got)
	}
	if n := heldCount(t, holder, store.got.OrgID); n != 1 {
		t.Fatalf("held = %d, want the EBWOID", n)
	}
}

func TestOpenWalletStubEBWOIDCarriesTheRegisterIdentity(t *testing.T) {
	store := &recordingStore{}
	res, err := openTestWallet(ebwoidService(store, registryprovider.StubEBWOIDIssuer{}, eudiholder.NewStubHolder()))
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	att := validatedAttestation()
	if res.Organization.Name != att.LegalName || res.Organization.EUID != att.EUID {
		t.Fatalf("org identity = %q/%q, want %q/%q", res.Organization.Name, res.Organization.EUID, att.LegalName, att.EUID)
	}
}

func TestOpenWalletRefusesAnEBWOIDForAnotherCompany(t *testing.T) {
	store := &recordingStore{}
	holder := eudiholder.NewStubHolder()
	issuer := claimsIssuer{claims: map[string]string{"id": "NLNHR.12345678", "name": "Other B.V."}}

	_, err := openTestWallet(ebwoidService(store, issuer, holder))
	if !errors.Is(err, ErrEBWOIDMismatch) {
		t.Fatalf("err = %v, want ErrEBWOIDMismatch", err)
	}
	if store.got.OrgID != uuid.Nil {
		t.Fatal("an org was registered for a mismatched EBWOID")
	}
}

func TestOpenWalletDiscardsTheEBWOIDWhenItCannotBeUsed(t *testing.T) {
	cases := map[string]struct {
		issuer   claimsIssuer
		storeErr error
	}{
		"mismatch":       {issuer: claimsIssuer{claims: map[string]string{"id": "NLNHR.12345678", "name": "Other B.V."}}},
		"no name":        {issuer: claimsIssuer{claims: map[string]string{"id": "NLNHR." + testKVK}}},
		"invalid id":     {issuer: claimsIssuer{claims: map[string]string{"id": "NL.KVK." + testKVK, "name": "X"}}},
		"issuer error":   {issuer: claimsIssuer{claims: map[string]string{}, err: errors.New("issuer refused")}},
		"store conflict": {issuer: claimsIssuer{claims: map[string]string{"id": "NLNHR." + testKVK, "name": "X"}}, storeErr: ErrSlugTaken},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			holder := &trackingHolder{StubHolder: eudiholder.NewStubHolder()}
			store := &recordingStore{err: tc.storeErr}
			if _, err := openTestWallet(ebwoidService(store, tc.issuer, holder)); err == nil {
				t.Fatal("OpenWallet succeeded, want an error")
			}
			if n := heldCount(t, holder.StubHolder, holder.orgID); n != 0 {
				t.Fatalf("held = %d after a failed open, want the EBWOID discarded", n)
			}
		})
	}
}

// trackingHolder remembers the org id the service stored the EBWOID under.
type trackingHolder struct {
	*eudiholder.StubHolder
	orgID uuid.UUID
}

func (h *trackingHolder) Store(ctx context.Context, orgID uuid.UUID, cred eudiholder.Credential) (string, error) {
	h.orgID = orgID
	return h.StubHolder.Store(ctx, orgID, cred)
}
