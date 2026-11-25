package openid4vciissuer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// eudiStub mimics the EU reference issuer's /credentialOfferReq2, keeping the
// decoded offer payload it was sent.
func eudiStub(t *testing.T, got *eudiOfferPayload) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /credentialOfferReq2", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.FormValue("request"), ".")
		if len(parts) != 3 {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || json.Unmarshal(raw, got) != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"credential_issuer":            "https://eudi-issuer.example",
			"credential_configuration_ids": []string{got.Credentials[0].CredentialConfigurationID},
			"grants": map[string]any{preAuthGrant: map[string]any{
				"pre-authorized_code": "code-1",
				"issuer_state":        "state-1",
				"tx_code":             map[string]any{"length": 5, "input_mode": "numeric", "value": 12345},
			}},
		})
	})
	mux.HandleFunc("GET /.well-known/openid-credential-issuer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEudiIssuerCreateOffer(t *testing.T) {
	var sent eudiOfferPayload
	srv := eudiStub(t, &sent)
	iss := NewEudiIssuer(srv.URL+"/", srv.Client())

	offer, err := iss.CreateOffer(context.Background(), OfferRequest{
		CredentialConfigID: "we-build-lei",
		Claims:             map[string]any{"lei": "5299001GCLKH6FUDMR36"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cred := sent.Credentials[0]
	if cred.CredentialConfigurationID != "we-build-lei" || cred.Data["lei"] != "5299001GCLKH6FUDMR36" {
		t.Errorf("sent %+v", cred)
	}
	if cred.Data["issuing_country"] != eudiEvidenceCountry {
		t.Errorf("issuing_country = %v, want %q", cred.Data["issuing_country"], eudiEvidenceCountry)
	}
	if offer.IssuanceID != "eudi:state-1" || offer.TxCode != "12345" {
		t.Errorf("offer = %+v", offer)
	}
	u, err := url.Parse(offer.OfferURI)
	if err != nil || u.Scheme != "openid-credential-offer" {
		t.Fatalf("offer uri %q: %v", offer.OfferURI, err)
	}
	var decoded eudiOffer
	if err := json.Unmarshal([]byte(u.Query().Get("credential_offer")), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Grants[preAuthGrant].PreAuthorizedCode != "code-1" {
		t.Errorf("offer uri carries %+v", decoded)
	}
}

func TestEudiIssuerKeepsAGivenIssuingCountry(t *testing.T) {
	var sent eudiOfferPayload
	srv := eudiStub(t, &sent)
	_, err := NewEudiIssuer(srv.URL, srv.Client()).CreateOffer(context.Background(), OfferRequest{
		CredentialConfigID: "we-build-eucc",
		Claims:             map[string]any{"issuing_country": "BE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := sent.Credentials[0].Data["issuing_country"]; got != "BE" {
		t.Errorf("issuing_country = %v, want BE", got)
	}
}

func TestEudiIssuerHasNoStatusOrRevocation(t *testing.T) {
	iss := NewEudiIssuer("http://unused", http.DefaultClient)
	st, err := iss.Status(context.Background(), "", "eudi:state-1")
	if err != nil || st.Status != StatusPending {
		t.Errorf("status = %+v, %v", st, err)
	}
	if err := iss.RevokeCredential(context.Background(), "", "x"); !errors.Is(err, ErrNoStatusListBit) {
		t.Errorf("revoke = %v, want ErrNoStatusListBit", err)
	}
}

func TestRulebookRouterRoutesRulebookOffers(t *testing.T) {
	var sent eudiOfferPayload
	srv := eudiStub(t, &sent)
	router := RulebookRouter{Default: NewStubIssuer(), Rulebooks: NewEudiIssuer(srv.URL, srv.Client())}
	ctx := context.Background()

	rb, err := router.CreateOffer(ctx, OfferRequest{CredentialConfigID: "we-build-lei", Rulebook: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rb.IssuanceID, eudiIDPrefix) || len(sent.Credentials) != 1 {
		t.Errorf("rulebook offer = %+v, sent %+v", rb, sent)
	}
	st, err := router.Status(ctx, "", rb.IssuanceID)
	if err != nil || st.Status != StatusPending {
		t.Errorf("rulebook status = %+v, %v", st, err)
	}

	own, err := router.CreateOffer(ctx, OfferRequest{CredentialConfigID: "nl.caesar.employee"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(own.IssuanceID, eudiIDPrefix) {
		t.Errorf("own schema offer went to the rulebook issuer: %+v", own)
	}
	if err := router.Ping(ctx); err != nil {
		t.Errorf("ping: %v", err)
	}
}

// fakeEntries holds the issuance ids the issuer reserved a status entry for.
type fakeEntries map[string]bool

func (f fakeEntries) Taken(_ context.Context, issuanceID string) (bool, error) {
	return f[issuanceID], nil
}

func TestEudiIssuerWithStatusList(t *testing.T) {
	ctx := context.Background()
	collected := EudiIssuanceID("state-1")
	iss := NewEudiIssuer("http://unused", http.DefaultClient).WithStatusList(fakeEntries{collected: true})

	st, err := iss.Status(ctx, "", collected)
	if err != nil || st.Status != StatusIssued || st.CredentialUUID != collected {
		t.Errorf("collected offer status = %+v, %v; want issued with its issuance id", st, err)
	}
	if st, err := iss.Status(ctx, "", EudiIssuanceID("state-2")); err != nil || st.Status != StatusPending {
		t.Errorf("open offer status = %+v, %v; want pending", st, err)
	}
	if err := iss.RevokeCredential(ctx, "", collected); err != nil {
		t.Errorf("revoke = %v, want nil: the ledger revoke flips the entry", err)
	}
}

// revokeRecorder is a default issuer that records what it was asked to revoke.
type revokeRecorder struct {
	StubIssuer
	revoked []string
}

func (r *revokeRecorder) RevokeCredential(_ context.Context, _, credentialUUID string) error {
	r.revoked = append(r.revoked, credentialUUID)
	return nil
}

func TestRulebookRouterRoutesRevocations(t *testing.T) {
	ctx := context.Background()
	def := &revokeRecorder{StubIssuer: NewStubIssuer()}
	router := RulebookRouter{Default: def, Rulebooks: NewEudiIssuer("http://unused", http.DefaultClient)}

	if err := router.RevokeCredential(ctx, "", EudiIssuanceID("state-1")); !errors.Is(err, ErrNoStatusListBit) {
		t.Errorf("rulebook revoke without a status list = %v, want ErrNoStatusListBit", err)
	}
	if err := router.RevokeCredential(ctx, "", "veramo-uuid"); err != nil || len(def.revoked) != 1 || def.revoked[0] != "veramo-uuid" {
		t.Errorf("own revoke = %v, default got %v", err, def.revoked)
	}
	if router.RulebookStatusList() {
		t.Error("RulebookStatusList = true without a status list")
	}
	router.Rulebooks = router.Rulebooks.WithStatusList(fakeEntries{})
	if !router.RulebookStatusList() {
		t.Error("RulebookStatusList = false with a status list")
	}
}
