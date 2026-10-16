package openid4vpverifier

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStartQueryAsksForOneCredentialWithAllClaims pins the DCQL a template-built
// query sends: a single dc+sd-jwt credential of the given vct, every claim as a
// path, one required credential set, and a fresh nonce.
func TestStartQueryAsksForOneCredentialWithAllClaims(t *testing.T) {
	var got startRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != presentationsPath {
			t.Errorf("path = %q, want %q", r.URL.Path, presentationsPath)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode start request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"transaction_id": "tx-1", "client_id": "x509_san_dns:verifier.test", "request_uri": "https://verifier.test/req/1",
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "", "", "", srv.Client())
	sess, err := c.StartQuery(context.Background(), Query{VCT: "nl.nijmegen.apv.standplaatsvergunning", Claims: []string{"vergunningnummer", "markt"}})
	if err != nil {
		t.Fatalf("StartQuery: %v", err)
	}
	if sess.TransactionID != "tx-1" {
		t.Errorf("transaction id = %q, want tx-1", sess.TransactionID)
	}
	if sess.WalletLink == "" || sess.WalletLink[:len("openid4vp://?")] != "openid4vp://?" {
		t.Errorf("wallet link = %q, want an openid4vp:// deeplink", sess.WalletLink)
	}

	if got.Nonce == "" {
		t.Error("nonce must be set")
	}
	if len(got.DCQLQuery.Credentials) != 1 {
		t.Fatalf("credentials = %d, want 1", len(got.DCQLQuery.Credentials))
	}
	cred := got.DCQLQuery.Credentials[0]
	if cred.ID != QueryCredentialID || cred.Format != formatSDJWT {
		t.Errorf("credential = %+v, want id %q format %q", cred, QueryCredentialID, formatSDJWT)
	}
	if len(cred.Meta.VctValues) != 1 || cred.Meta.VctValues[0] != "nl.nijmegen.apv.standplaatsvergunning" {
		t.Errorf("vct_values = %v", cred.Meta.VctValues)
	}
	if len(cred.Claims) != 2 || cred.Claims[0].Path[0] != "vergunningnummer" || cred.Claims[1].Path[0] != "markt" {
		t.Errorf("claims = %+v", cred.Claims)
	}
	if len(got.DCQLQuery.CredentialSets) != 1 || len(got.DCQLQuery.CredentialSets[0].Options) != 1 || got.DCQLQuery.CredentialSets[0].Options[0][0] != QueryCredentialID {
		t.Errorf("credential_sets = %+v", got.DCQLQuery.CredentialSets)
	}
}

func TestStartQueryRefusesEmptyQuery(t *testing.T) {
	c := New("http://verifier.invalid", "", "", "", http.DefaultClient)
	if _, err := c.StartQuery(context.Background(), Query{VCT: "nl.x", Claims: nil}); err == nil {
		t.Error("expected an error for a query without claims")
	}
	if _, err := c.StartQuery(context.Background(), Query{VCT: "", Claims: []string{"a"}}); err == nil {
		t.Error("expected an error for a query without a vct")
	}
}

// issuerJWTWithExp is issuerJWT with an `exp` claim beside `iat`.
func issuerJWTWithExp(t *testing.T, issuedAt, expiresAt int64) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"iat": issuedAt, "exp": expiresAt})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return "eyJhbGciOiJFUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(raw) + ".sig"
}

func TestResultReadsQueryClaimsAndExpiry(t *testing.T) {
	exp := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	token := issuerJWTWithExp(t, 1_700_000_000, exp.Unix()) +
		"~" + disclosure(t, "vergunningnummer", "APV-2026-01834") +
		"~" + disclosure(t, "markt", "Grote Markt Nijmegen") + "~"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"vp_token": map[string][]string{QueryCredentialID: {token}}})
	}))
	defer srv.Close()

	c := New(srv.URL, "", "", "", srv.Client())
	p, err := c.Result(context.Background(), "tx-1")
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	claims := p.QueryClaims()
	if claims["vergunningnummer"] != "APV-2026-01834" || claims["markt"] != "Grote Markt Nijmegen" {
		t.Errorf("query claims = %v", claims)
	}
	if !p.QueryExpiresAt().Equal(exp) {
		t.Errorf("query expiry = %v, want %v", p.QueryExpiresAt(), exp)
	}
}

func TestQueryExpiresAtIsZeroWithoutExp(t *testing.T) {
	token := issuerJWT(t, 1_700_000_000) + "~" + disclosure(t, "a", "b") + "~"
	p := Presentation{ExpiresAt: expiresAtByCredential(map[string][]string{QueryCredentialID: {token}})}
	if !p.QueryExpiresAt().IsZero() {
		t.Errorf("expiry = %v, want zero", p.QueryExpiresAt())
	}
}
