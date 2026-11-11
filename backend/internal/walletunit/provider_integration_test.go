//go:build integration

package walletunit

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/privacybydesign/irmago/eudi/credentials/statuslist"
	eudi_jwt "github.com/privacybydesign/irmago/eudi/jwt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

type env struct {
	pool     *pgxpool.Pool
	store    *Store
	provider *Provider
	signer   *Signer
	checker  *statuslist.Checker
}

func newEnv(t *testing.T, ska *SKAConfig) *env {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	signer, err := NewEphemeralSigner("test provider")
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(pool, audit.NewDBRecorder())

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	provider := NewProvider(store, organization.NewStore(pool, audit.NopRecorder{}), signer, Config{
		BaseURL: srv.URL, WalletName: "Test Wallet", WalletVersion: "1.0.0", SKA: ska,
	})
	api := http.NewServeMux()
	pass := func(next http.Handler) http.Handler { return next }
	NewHandler(store, provider, pass, pass).Register(api)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", api))

	roots := x509.NewCertPool()
	roots.AddCert(signer.Leaf())
	checker := statuslist.NewChecker(statuslist.VerificationContext{
		X509Context: &eudi_jwt.StaticVerificationContext{
			VerifyOpts: x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}},
		},
		HTTPClient: srv.Client(),
	}, statuslist.NewInMemoryCache())
	return &env{pool: pool, store: store, provider: provider, signer: signer, checker: checker}
}

func seedOrg(t *testing.T, pool *pgxpool.Pool, slug, euid string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug+" B.V.", slug, "kvk-"+slug, euid, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

func newKey(t *testing.T) *ecdsa.PublicKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &k.PublicKey
}

// parse verifies token under the provider leaf and returns header and claims.
func (e *env) parse(t *testing.T, token string) (map[string]any, jwt.MapClaims) {
	t.Helper()
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return e.signer.Leaf().PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"}))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	return parsed.Header, claims
}

func statusOf(t *testing.T, claims jwt.MapClaims, field string) statuslist.Reference {
	t.Helper()
	ref := claims[field].(map[string]any)["status"].(map[string]any)["status_list"].(map[string]any)
	return statuslist.Reference{Index: uint64(ref["idx"].(float64)), URI: ref["uri"].(string)}
}

func TestIssueBWIA(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	org := seedOrg(t, e.pool, "acme", "NLNHR.12345678")

	issued, err := e.provider.Issue(ctx, org, IssueRequest{InstanceKey: newKey(t), AttestedKeys: []*ecdsa.PublicKey{newKey(t)}, KeysInHSM: true})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if issued.SKA != "" {
		t.Error("SKA issued without an HSM certification configured")
	}
	header, claims := e.parse(t, issued.BWIA)
	if header["typ"] != bwiaTyp {
		t.Errorf("typ = %v", header["typ"])
	}
	if x5c, ok := header["x5c"].([]any); !ok || len(x5c) == 0 {
		t.Errorf("x5c = %v", header["x5c"])
	}
	if _, ok := claims["iss"]; ok {
		t.Error("BWIA carries iss")
	}
	if claims["ebwoid_id"] != "NLNHR.12345678" || claims["legal_name"] != "acme B.V." {
		t.Errorf("subject = %v / %v", claims["ebwoid_id"], claims["legal_name"])
	}
	if jwk := claims["cnf"].(map[string]any)["jwk"].(map[string]any); jwk["kty"] != "EC" || jwk["crv"] != "P-256" {
		t.Errorf("cnf = %v", jwk)
	}
	now := time.Now()
	exp := time.Unix(int64(claims["exp"].(float64)), 0)
	if exp.Sub(now) >= 24*time.Hour {
		t.Errorf("token exp %s is not under 24h", exp)
	}
	statusExp := time.Unix(int64(claims["client_status"].(map[string]any)["exp"].(float64)), 0)
	if statusExp.Sub(now) < 31*24*time.Hour {
		t.Errorf("client_status exp %s is under 31 days", statusExp)
	}
	if got, err := e.checker.Check(ctx, statusOf(t, claims, "client_status")); err != nil || got != statuslist.StatusValid {
		t.Errorf("status = %v, %v; want valid", got, err)
	}
}

func TestIssueSKA(t *testing.T) {
	ska := &SKAConfig{CertificationURL: "https://hsm.example/cc", KeyStorage: []string{"iso_18045_high"}, UserAuthentication: []string{"iso_18045_moderate"}}
	e := newEnv(t, ska)
	ctx := context.Background()
	org := seedOrg(t, e.pool, "acme", "NLNHR.12345678")

	soft, err := e.provider.Issue(ctx, org, IssueRequest{InstanceKey: newKey(t), AttestedKeys: []*ecdsa.PublicKey{newKey(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if soft.SKA != "" {
		t.Error("SKA issued for keys outside the HSM")
	}

	issued, err := e.provider.Issue(ctx, org, IssueRequest{InstanceKey: newKey(t), AttestedKeys: []*ecdsa.PublicKey{newKey(t), newKey(t)}, KeysInHSM: true})
	if err != nil {
		t.Fatal(err)
	}
	header, claims := e.parse(t, issued.SKA)
	if header["typ"] != skaTyp {
		t.Errorf("typ = %v", header["typ"])
	}
	if keys := claims["attested_keys"].([]any); len(keys) != 2 {
		t.Errorf("attested_keys = %v", keys)
	}
	if claims["certification"] != ska.CertificationURL {
		t.Errorf("certification = %v", claims["certification"])
	}
	_, bwia := e.parse(t, issued.BWIA)
	if statusOf(t, claims, "key_storage_status") == statusOf(t, bwia, "client_status") {
		t.Error("BWIA and SKA share a status entry")
	}
}

func TestIssueRefusals(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	noEUID := seedOrg(t, e.pool, "noeuid", "")
	if _, err := e.provider.Issue(ctx, noEUID, IssueRequest{InstanceKey: newKey(t)}); !errors.Is(err, ErrNoEUID) {
		t.Errorf("no EUID: err = %v", err)
	}
	org := seedOrg(t, e.pool, "acme", "NLNHR.12345678")
	if _, err := e.provider.Issue(ctx, org, IssueRequest{}); !errors.Is(err, ErrNoInstanceKey) {
		t.Errorf("no key: err = %v", err)
	}
	setOrgStatus(t, e.pool, org, organization.StatusSuspended)
	if _, err := e.provider.Issue(ctx, org, IssueRequest{InstanceKey: newKey(t)}); !errors.Is(err, ErrOrgNotActive) {
		t.Errorf("suspended org: err = %v", err)
	}
}

func setOrgStatus(t *testing.T, pool *pgxpool.Pool, org uuid.UUID, status string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE organizations SET status = $2 WHERE id = $1`, org, status); err != nil {
		t.Fatal(err)
	}
}

func TestStatusLifecycle(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	org := seedOrg(t, e.pool, "acme", "NLNHR.12345678")
	other := seedOrg(t, e.pool, "other", "NLNHR.87654321")

	issue := func(orgID uuid.UUID) statuslist.Reference {
		t.Helper()
		issued, err := e.provider.Issue(ctx, orgID, IssueRequest{InstanceKey: newKey(t)})
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		_, claims := e.parse(t, issued.BWIA)
		return statusOf(t, claims, "client_status")
	}
	expect := func(ref statuslist.Reference, want statuslist.Status) {
		t.Helper()
		got, err := e.checker.Refresh(ctx, ref)
		if err != nil || got != want {
			t.Errorf("status = %v, %v; want %v", got, err, want)
		}
	}

	ref, otherRef := issue(org), issue(other)
	setOrgStatus(t, e.pool, org, organization.StatusSuspended)
	expect(ref, statuslist.StatusSuspended)
	setOrgStatus(t, e.pool, org, organization.StatusRevoked)
	expect(ref, statuslist.StatusInvalid)
	setOrgStatus(t, e.pool, org, organization.StatusActive)
	expect(ref, statuslist.StatusValid)

	if _, err := e.store.Revoke(ctx, org, "bogus"); !errors.Is(err, ErrInvalidReason) {
		t.Errorf("bogus reason: err = %v", err)
	}
	if _, err := e.store.Revoke(ctx, org, ReasonKeyCompromise); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	expect(ref, statuslist.StatusInvalid)
	expect(otherRef, statuslist.StatusValid)
	if _, err := e.provider.Issue(ctx, org, IssueRequest{InstanceKey: newKey(t)}); !errors.Is(err, ErrUnitRevoked) {
		t.Errorf("issue after revoke: err = %v", err)
	}

	if _, err := e.store.Activate(ctx, org); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := e.store.Activate(ctx, org); !errors.Is(err, ErrUnitActive) {
		t.Errorf("second activate: err = %v", err)
	}
	fresh := issue(org)
	expect(fresh, statuslist.StatusValid)
	expect(ref, statuslist.StatusInvalid)
	sum, err := e.store.Summary(ctx, org)
	if err != nil || sum.Unit.Status != UnitActive || sum.IssuedCount != 1 {
		t.Errorf("summary = %+v, %v", sum, err)
	}

	var events int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE organization_id = $1 AND action IN ($2, $3)`,
		org, audit.WalletUnitRevoked, audit.WalletUnitActivated).Scan(&events); err != nil || events != 2 {
		t.Errorf("audit events = %d, %v; want 2", events, err)
	}

	if err := organization.NewStore(e.pool, audit.NopRecorder{}).Delete(ctx, org); err != nil {
		t.Fatalf("delete org: %v", err)
	}
	expect(fresh, statuslist.StatusInvalid)
	expect(otherRef, statuslist.StatusValid)
}
