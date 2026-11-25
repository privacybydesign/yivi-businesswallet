//go:build integration

package issuerstatus

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/privacybydesign/irmago/eudi/credentials/statuslist"
	eudi_jwt "github.com/privacybydesign/irmago/eudi/jwt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vciissuer"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/walletunit"
)

const testAPIKey = "test-api-key"

type env struct {
	pool    *pgxpool.Pool
	svc     *Service
	srv     *httptest.Server
	checker *statuslist.Checker
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	signer, err := walletunit.NewEphemeralSigner("test rulebook issuer")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	svc := NewService(NewStore(pool), signer, srv.URL)
	api := http.NewServeMux()
	NewHandler(svc, testAPIKey).Register(api)
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", api))

	roots := x509.NewCertPool()
	roots.AddCert(signer.Leaf())
	checker := statuslist.NewChecker(statuslist.VerificationContext{
		X509Context: &eudi_jwt.StaticVerificationContext{
			VerifyOpts: x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}},
		},
		HTTPClient: srv.Client(),
	}, statuslist.NewInMemoryCache())
	return &env{pool: pool, svc: svc, srv: srv, checker: checker}
}

// seedOffer creates an organization with one rulebook offer whose issuer_state is
// state, and returns the offer's row id.
func (e *env) seedOffer(t *testing.T, state, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var orgID, id uuid.UUID
	if err := e.pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme B.V.', $1, $1, $1, $1 || '@qerds.localhost') RETURNING id`, "acme-"+state).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO issued_attestations (organization_id, schema_vct, recipient_kind, recipient_ref, status, issuance_id)
		VALUES ($1, 'eu.we-build.lei.1', 'organization', 'zonnedael', $2, $3) RETURNING id`,
		orgID, status, openid4vciissuer.EudiIssuanceID(state)).Scan(&id); err != nil {
		t.Fatalf("create offer: %v", err)
	}
	return id
}

// take posts the EU reference issuer's reservation and returns the response.
func (e *env) take(t *testing.T, apiKey string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/attestations/status-lists/take", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(apiKeyHeader, apiKey)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func takeForm(state string) url.Values {
	return url.Values{"doctype": {"eu.we-build.lei.1"}, "country": {"FC"}, "expiry_date": {"2027-11-23"}, "issuer_state": {state}}
}

func TestTakeThenRevoke(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.seedOffer(t, "state-1", "offered")

	if taken, err := e.svc.Taken(ctx, openid4vciissuer.EudiIssuanceID("state-1")); err != nil || taken {
		t.Fatalf("Taken before take = %v, %v; want false", taken, err)
	}
	resp := e.take(t, testAPIKey, takeForm("state-1"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("take status = %d", resp.StatusCode)
	}
	var claim struct {
		StatusList struct {
			Idx uint64 `json:"idx"`
			URI string `json:"uri"`
		} `json:"status_list"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claim); err != nil {
		t.Fatalf("decode take: %v", err)
	}
	if !strings.HasPrefix(claim.StatusList.URI, e.srv.URL+ListPath) {
		t.Errorf("uri = %q", claim.StatusList.URI)
	}
	if taken, err := e.svc.Taken(ctx, openid4vciissuer.EudiIssuanceID("state-1")); err != nil || !taken {
		t.Fatalf("Taken after take = %v, %v; want true", taken, err)
	}

	ref := statuslist.Reference{Index: claim.StatusList.Idx, URI: claim.StatusList.URI}
	if got, err := e.checker.Check(ctx, ref); err != nil || got != statuslist.StatusValid {
		t.Fatalf("status before revoke = %v, %v; want valid", got, err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE issued_attestations SET status = 'revoked' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if got, err := e.checker.Refresh(ctx, ref); err != nil || got != statuslist.StatusInvalid {
		t.Fatalf("status after revoke = %v, %v; want invalid", got, err)
	}
}

func TestTakeRefuses(t *testing.T) {
	e := newEnv(t)
	e.seedOffer(t, "cancelled", "expired")
	for name, tc := range map[string]struct {
		apiKey string
		form   url.Values
		want   int
	}{
		"a wrong API key":         {"wrong", takeForm("state-1"), http.StatusUnauthorized},
		"no issuer_state":         {testAPIKey, url.Values{"doctype": {"eu.we-build.lei.1"}}, http.StatusBadRequest},
		"an unknown offer":        {testAPIKey, takeForm("unknown"), http.StatusNotFound},
		"an offer no longer open": {testAPIKey, takeForm("cancelled"), http.StatusNotFound},
		"a malformed expiry":      {testAPIKey, url.Values{"issuer_state": {"cancelled"}, "expiry_date": {"tomorrow"}}, http.StatusBadRequest},
	} {
		if got := e.take(t, tc.apiKey, tc.form).StatusCode; got != tc.want {
			t.Errorf("%s: status = %d, want %d", name, got, tc.want)
		}
	}
}

func TestUnknownList(t *testing.T) {
	e := newEnv(t)
	resp, err := e.srv.Client().Get(e.srv.URL + ListPath + "16")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}
