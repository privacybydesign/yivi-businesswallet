//go:build integration

package wallet_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
)

// captureInbox records the deposited attestation instead of writing it, so the
// OpenWallet flow can run without a real QERDS store.
type captureInbox struct{ deposited int }

func (c *captureInbox) CreateInbound(context.Context, uuid.UUID, qerdsprovider.InboundMessage) (qerds.Message, bool, error) {
	c.deposited++
	return qerds.Message{}, true, nil
}

// TestOpenWalletSucceedsForSeededRegisterCompany is the end-to-end regression for
// the open-wallet happy path over seeded data: a requester whose identification
// data matches the register-only demo company (validatable, not pre-seeded as an
// org) can open its wallet. Before the register-only entry existed, every
// validatable KVK number was already a seeded org, so this path was unreachable —
// a validated requester was bounced with ErrAlreadyRegistered.
func TestOpenWalletSucceedsForSeededRegisterCompany(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	// The KVK register participant must exist so the consult decision has a log to
	// be audited against (best-effort, but seed it for a faithful end-to-end run).
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5)`,
		registryprovider.RegisterLegalName, registryprovider.RegisterSlug,
		registryprovider.RegisterKVKNumber, registryprovider.RegisterEUID, "kvk@qerds.localhost"); err != nil {
		t.Fatalf("seed register org: %v", err)
	}

	requester, err := user.NewStore(pool).Create(ctx, user.User{
		Email:      "sanne@example.com",
		GivenNames: "Sanne Marijke",
		LastName:   "Visser",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}

	store := wallet.NewStore(pool, audit.NopRecorder{}, walletunit.NewStore(pool, audit.NopRecorder{}))
	reg := registryprovider.NewSeededRegistry(pool, audit.NewDBRecorder())
	inbox := &captureInbox{}
	holder := eudiholder.NewStubHolder()
	svc := wallet.NewService(store, reg, wallet.OwnerID{Issuer: registryprovider.StubEBWOIDIssuer{}, Holder: holder}, nil, nil, inbox, "qerds.localhost")

	res, err := svc.OpenWallet(ctx, requester.ID, wallet.Requester{
		GivenNames:  "Sanne Marijke",
		FamilyName:  "Visser",
		DateOfBirth: "1983-07-08",
	}, registryprovider.OpenableKVKNumber, "zonnedael")
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	org := res.Organization
	if org.KVKNumber != registryprovider.OpenableKVKNumber {
		t.Fatalf("org kvk = %q, want %q", org.KVKNumber, registryprovider.OpenableKVKNumber)
	}
	if want := "NLNHR." + registryprovider.OpenableKVKNumber; org.EUID != want {
		t.Fatalf("org euid = %q, want %q", org.EUID, want)
	}
	if org.Name != "Zonnedael B.V." {
		t.Fatalf("org name = %q, want the register's legal name Zonnedael B.V.", org.Name)
	}
	if org.Status != organization.StatusActive {
		t.Fatalf("status = %q, want active", org.Status)
	}
	if res.RepresentationKind != registryprovider.KindBestuurder || res.RepresentationAuthority != registryprovider.AuthoritySole {
		t.Fatalf("representation = %q/%q, want bestuurder/sole", res.RepresentationKind, res.RepresentationAuthority)
	}

	// The requester is the org's admin owner.
	var role string
	if err := pool.QueryRow(ctx, `SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`, org.ID, requester.ID).Scan(&role); err != nil {
		t.Fatalf("owner membership missing: %v", err)
	}
	if role != organization.RoleAdmin {
		t.Fatalf("owner role = %q, want admin", role)
	}
	// The wallet opens holding its EBWOID, and the org identity matches its claims.
	var ref string
	if err := pool.QueryRow(ctx, `SELECT credential_ref FROM held_attestations WHERE organization_id = $1 AND vct = $2 AND source = 'bootstrap'`,
		org.ID, registryprovider.EBWOIDVCT).Scan(&ref); err != nil {
		t.Fatalf("held EBWOID missing: %v", err)
	}
	cred, err := holder.Claims(ctx, org.ID, ref, registryprovider.EBWOIDVCT, "")
	if err != nil {
		t.Fatalf("EBWOID claims: %v", err)
	}
	claims := map[string]any{}
	for _, a := range cred.Attributes {
		claims[a.Key] = a.Value
	}
	if claims[registryprovider.EBWOIDClaimID] != org.EUID || claims[registryprovider.EBWOIDClaimName] != org.Name {
		t.Fatalf("EBWOID claims = %v, want id %q and name %q", claims, org.EUID, org.Name)
	}
	if inbox.deposited != 1 {
		t.Fatalf("deposited attestations = %d, want 1", inbox.deposited)
	}
}

// A declaring register consults nothing, so a company no dataset knows can open
// its wallet under the name its registrant states, with the registrant on record
// as a representative no register confirmed.
func TestOpenWalletSucceedsForADeclaredCompany(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	requester, err := user.NewStore(pool).Create(ctx, user.User{Email: "alice@example.com", GivenNames: "Alice", LastName: "Owner"})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}

	store := wallet.NewStore(pool, audit.NopRecorder{}, walletunit.NewStore(pool, audit.NopRecorder{}))
	reg := registryprovider.NewDeclaredRegistry(pool, audit.NewDBRecorder())
	holder := eudiholder.NewStubHolder()
	svc := wallet.NewService(store, reg, wallet.OwnerID{Issuer: registryprovider.StubEBWOIDIssuer{}, Holder: holder}, nil, nil, &captureInbox{}, "qerds.localhost")

	const kvkNumber = "12345678"
	res, err := svc.OpenWallet(ctx, requester.ID, wallet.Requester{
		GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02", Disclosed: true, CompanyName: "Acme B.V.",
	}, kvkNumber, "acme")
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}

	org := res.Organization
	if org.Name != "Acme B.V." || org.KVKNumber != kvkNumber || org.EUID != "NLNHR."+kvkNumber {
		t.Fatalf("org = %q kvk %q euid %q, want the stated company", org.Name, org.KVKNumber, org.EUID)
	}
	if !org.TestMode {
		t.Fatal("a registered org must start in test mode")
	}
	if res.RepresentationKind != "" {
		t.Fatalf("representation kind = %q, want none: no register stated one", res.RepresentationKind)
	}
	var kind string
	if err := pool.QueryRow(ctx, `SELECT kind FROM wallet_representations WHERE organization_id = $1 AND claimed_by_user_id = $2`,
		org.ID, requester.ID).Scan(&kind); err != nil {
		t.Fatalf("claimed representation missing: %v", err)
	}
	if kind != registryprovider.KindOverig {
		t.Fatalf("representation kind = %q, want overig", kind)
	}

	// The KVK number is taken from then on.
	if _, err := svc.OpenWallet(ctx, requester.ID, wallet.Requester{GivenNames: "Alice", FamilyName: "Owner", CompanyName: "Acme Again"},
		kvkNumber, "acme-again"); !errors.Is(err, wallet.ErrAlreadyRegistered) {
		t.Fatalf("second registration err = %v, want ErrAlreadyRegistered", err)
	}
}
