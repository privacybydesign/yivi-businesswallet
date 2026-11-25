//go:build integration

package wallet_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/wallet"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
)

// The stub KVK revoking a company's EBWOID revokes that company's wallet on the
// next status re-check: the holder reads the signed list over HTTP, as it would KVK's.
func TestRevokedEBWOIDRevokesTheWallet(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()

	signer, err := walletunit.NewEphemeralSigner("test KVK")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	var serve http.Handler
	kvk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serve.ServeHTTP(w, r) }))
	t.Cleanup(kvk.Close)
	statusList := registryprovider.NewEBWOIDStatusList(pool, signer, kvk.URL)
	mux := http.NewServeMux()
	registryprovider.NewEBWOIDStatusHandler(statusList).Register(mux)
	serve = http.StripPrefix("/api/v1", mux)

	var key [32]byte
	key[0] = 1
	holder := eudiholder.NewEngine(dsn, t.TempDir(), key, eudiholder.RedeemConfig{
		TrustChainPEM:     signer.RootPEM(),
		AllowInsecureHTTP: true,
	})
	t.Cleanup(func() { _ = holder.Close() })

	requester, err := user.NewStore(pool).Create(ctx, user.User{Email: "alice@example.com", GivenNames: "Alice", LastName: "Owner"})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	units := walletunit.NewStore(pool, audit.NopRecorder{})
	store := wallet.NewStore(pool, audit.NewDBRecorder(), units)
	reg := registryprovider.NewDeclaredRegistry(pool, audit.NopRecorder{})
	svc := wallet.NewService(store, reg, wallet.OwnerID{Issuer: registryprovider.StubEBWOIDIssuer{Status: statusList}, Holder: holder}, nil, nil, &captureInbox{}, "qerds.localhost")
	res, err := svc.OpenWallet(ctx, requester.ID, wallet.Requester{
		GivenNames: "Alice", FamilyName: "Owner", DateOfBirth: "1980-01-02", Disclosed: true, CompanyName: "Acme B.V.",
	}, "12345678", "acme")
	if err != nil {
		t.Fatalf("OpenWallet: %v", err)
	}
	org := res.Organization

	attestationStore := attestation.NewStore(pool, audit.NewDBRecorder())
	attestations := attestation.NewService(nil, nil, nil, nil, nil, attestationStore, nil, holder, "http://app.test")
	attestations.SetOwnerIDRevoker(svc)

	if _, err := attestations.RecheckHeld(ctx, org.ID); err != nil {
		t.Fatalf("RecheckHeld: %v", err)
	}
	if got := orgStatus(t, pool, org); got != organization.StatusActive {
		t.Fatalf("status before KVK revokes = %q, want active", got)
	}

	if n, err := statusList.RevokeCompany(ctx, org.EUID); err != nil || n != 1 {
		t.Fatalf("RevokeCompany = %d, %v; want 1 entry", n, err)
	}
	if _, err := attestations.RecheckHeld(ctx, org.ID); err != nil {
		t.Fatalf("RecheckHeld: %v", err)
	}
	if got := orgStatus(t, pool, org); got != organization.StatusRevoked {
		t.Fatalf("status after KVK revokes = %q, want revoked", got)
	}
	var audited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id = $1 AND action = $2 AND actor_user_id IS NULL`,
		org.ID, audit.WalletRevoked).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("wallet.revoked events without an actor = %d, %v; want 1", audited, err)
	}

	var unitStatus, unitReason string
	if err := pool.QueryRow(ctx, `SELECT status, revoked_reason FROM wallet_units WHERE organization_id = $1`, org.ID).
		Scan(&unitStatus, &unitReason); err != nil || unitStatus != walletunit.UnitRevoked || unitReason != walletunit.ReasonCessation {
		t.Fatalf("wallet unit = %q (%q), %v; want revoked for cessation", unitStatus, unitReason, err)
	}

	// A later re-check finds the wallet already revoked and leaves it.
	if _, err := attestations.RecheckHeld(ctx, org.ID); err != nil {
		t.Fatalf("RecheckHeld on a revoked wallet: %v", err)
	}
	if _, err := units.Activate(ctx, org.ID); !errors.Is(err, walletunit.ErrOrgNotActive) {
		t.Fatalf("Activate on a revoked wallet = %v, want ErrOrgNotActive", err)
	}
}

func orgStatus(t *testing.T, pool *pgxpool.Pool, org organization.Organization) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM organizations WHERE id = $1`, org.ID).Scan(&status); err != nil {
		t.Fatalf("org status: %v", err)
	}
	return status
}
