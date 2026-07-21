package openid4vciissuer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testInstance = "test-issuer"

// veramoStub mimics the Veramo issuer's create-offer / check-offer endpoints,
// capturing the last create-offer body and the instance path segment so tests
// can assert the request shape and per-organization routing.
type veramoStub struct {
	server       *httptest.Server
	lastOffer    createOfferRequest
	lastAuth     string
	lastInstance string
	checkStatus  string
	checkUUID    string
	lastRevoke   revokeCredentialRequest
	revokeStatus string
}

func newVeramoStub(t *testing.T) *veramoStub {
	t.Helper()
	s := &veramoStub{checkStatus: StatusIssued, checkUUID: "cred-uuid-1", revokeStatus: revokedStatus}
	mux := http.NewServeMux()
	// Match any {instance} so tests can assert which instance the offer routed to.
	mux.HandleFunc("POST /{instance}/api/create-offer", func(w http.ResponseWriter, r *http.Request) {
		s.lastAuth = r.Header.Get("Authorization")
		s.lastInstance = r.PathValue("instance")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &s.lastOffer); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":     "offer-123",
			"uri":    "openid-credential-offer://issuer?credential_offer=x",
			"txCode": "654321",
		})
	})
	mux.HandleFunc("POST /{instance}/api/check-offer", func(w http.ResponseWriter, r *http.Request) {
		s.lastInstance = r.PathValue("instance")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": s.checkStatus, "uuid": s.checkUUID})
	})
	mux.HandleFunc("POST /{instance}/api/revoke-credential", func(w http.ResponseWriter, r *http.Request) {
		s.lastInstance = r.PathValue("instance")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &s.lastRevoke); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": s.revokeStatus})
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func TestCreateOfferSendsVeramoEnvelope(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, testInstance, NewBearerAuthenticator("admin-token"), "", http.DefaultClient)

	offer, err := client.CreateOffer(context.Background(), OfferRequest{
		CredentialConfigID: "EmailCredentialSdJwt",
		Claims:             map[string]any{"email": "alice@example.com"},
		ExpirationSeconds:  3600,
		UseTxCode:          true,
	})
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	if offer.IssuanceID != "offer-123" || offer.TxCode != "654321" {
		t.Fatalf("unexpected offer: %+v", offer)
	}
	if !strings.HasPrefix(offer.OfferURI, "openid-credential-offer://") {
		t.Fatalf("offer uri not a credential offer: %q", offer.OfferURI)
	}
	if stub.lastAuth != "Bearer admin-token" {
		t.Fatalf("missing/incorrect auth header: %q", stub.lastAuth)
	}
	if len(stub.lastOffer.Credentials) != 1 || stub.lastOffer.Credentials[0] != "EmailCredentialSdJwt" {
		t.Fatalf("unexpected credentials: %+v", stub.lastOffer.Credentials)
	}
	grant, ok := stub.lastOffer.Grants[preAuthGrant].(map[string]any)
	if !ok {
		t.Fatalf("missing pre-authorized_code grant: %+v", stub.lastOffer.Grants)
	}
	if grant["pre-authorized_code"] != "generate" {
		t.Fatalf("pre-authorized_code not requested: %+v", grant)
	}
	if _, ok := grant["tx_code"]; !ok {
		t.Fatalf("tx_code not requested when UseTxCode is set: %+v", grant)
	}
	if stub.lastOffer.CredentialDataSupplierInput["email"] != "alice@example.com" {
		t.Fatalf("claims not forwarded: %+v", stub.lastOffer.CredentialDataSupplierInput)
	}
}

func TestStatusMapsIssuedAndPending(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, testInstance, NewBearerAuthenticator(""), "", http.DefaultClient)

	stub.checkStatus = StatusIssued
	stub.checkUUID = "cred-uuid-42"
	st, err := client.Status(context.Background(), "", "offer-123")
	if err != nil || st.Status != StatusIssued {
		t.Fatalf("expected issued, got %+v err %v", st, err)
	}
	if st.CredentialUUID != "cred-uuid-42" {
		t.Fatalf("credential uuid not captured: %q", st.CredentialUUID)
	}

	stub.checkStatus = "PENDING"
	st, err = client.Status(context.Background(), "", "offer-123")
	if err != nil || st.Status != StatusPending {
		t.Fatalf("expected pending, got %+v err %v", st, err)
	}
	if st.CredentialUUID != "" {
		t.Fatalf("pending status must not carry a credential uuid: %q", st.CredentialUUID)
	}
}

// TestCreateOfferEnablesStatusLists asserts issued credentials request a Token
// Status List reference (the create-offer credentialMetadata flag), so a later
// revocation is observable to a verifier.
func TestCreateOfferEnablesStatusLists(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, testInstance, NewBearerAuthenticator("admin-token"), "", http.DefaultClient)

	if _, err := client.CreateOffer(context.Background(), OfferRequest{CredentialConfigID: "EmailCredentialSdJwt"}); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if stub.lastOffer.CredentialMetadata["enableStatusLists"] != true {
		t.Fatalf("create-offer did not enable status lists: %+v", stub.lastOffer.CredentialMetadata)
	}
}

// TestRevokeCredentialSetsBit asserts the revoke-credential call requests the
// revoked state for the credential uuid and treats REVOKED/WAS_REVOKED as
// success while UNKNOWN is an error.
func TestRevokeCredentialSetsBit(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, "default-issuer", NewBearerAuthenticator("admin-token"), "", http.DefaultClient)

	for _, ok := range []string{revokedStatus, wasRevokedStatus} {
		stub.revokeStatus = ok
		if err := client.RevokeCredential(context.Background(), "org-yivi", "cred-uuid-9"); err != nil {
			t.Fatalf("RevokeCredential(%s): %v", ok, err)
		}
		if stub.lastRevoke.UUID != "cred-uuid-9" || stub.lastRevoke.State != revokeState {
			t.Fatalf("unexpected revoke body: %+v", stub.lastRevoke)
		}
		if stub.lastInstance != "org-yivi" {
			t.Fatalf("revoke did not route to per-org instance: %q", stub.lastInstance)
		}
	}

	stub.revokeStatus = unknownStatus
	if err := client.RevokeCredential(context.Background(), "", "cred-uuid-9"); err == nil {
		t.Fatalf("expected error for UNKNOWN revoke status")
	}
}

// TestCreateOfferRoutesToPerOrgInstance verifies a per-call instance overrides
// the client's configured default, and that an empty instance falls back to it.
func TestCreateOfferRoutesToPerOrgInstance(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, "default-issuer", NewBearerAuthenticator("admin-token"), "", http.DefaultClient)

	if _, err := client.CreateOffer(context.Background(), OfferRequest{
		Instance:           "org-yivi",
		CredentialConfigID: "EmailCredentialSdJwt",
	}); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if stub.lastInstance != "org-yivi" {
		t.Fatalf("per-org instance not routed: got %q want %q", stub.lastInstance, "org-yivi")
	}

	if _, err := client.CreateOffer(context.Background(), OfferRequest{
		CredentialConfigID: "EmailCredentialSdJwt",
	}); err != nil {
		t.Fatalf("CreateOffer (default): %v", err)
	}
	if stub.lastInstance != "default-issuer" {
		t.Fatalf("empty instance did not fall back to default: got %q", stub.lastInstance)
	}

	if _, err := client.Status(context.Background(), "org-yivi", "offer-123"); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if stub.lastInstance != "org-yivi" {
		t.Fatalf("Status did not route to per-org instance: got %q", stub.lastInstance)
	}

	if err := client.RevokeCredential(context.Background(), "org-yivi", "cred-uuid-1"); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}
	if stub.lastInstance != "org-yivi" {
		t.Fatalf("RevokeCredential did not route to per-org instance: got %q", stub.lastInstance)
	}
}

func TestPingOffersConfiguredCredential(t *testing.T) {
	stub := newVeramoStub(t)
	client := NewVeramoIssuer(stub.server.URL, testInstance, NewBearerAuthenticator("admin-token"), "EmailCredentialSdJwt", http.DefaultClient)

	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if len(stub.lastOffer.Credentials) != 1 || stub.lastOffer.Credentials[0] != "EmailCredentialSdJwt" {
		t.Fatalf("ping did not offer the configured credential: %+v", stub.lastOffer.Credentials)
	}
}

func TestPingNoopWithoutCredential(t *testing.T) {
	client := NewVeramoIssuer("http://127.0.0.1:0", testInstance, NewBearerAuthenticator(""), "", http.DefaultClient)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping should be a no-op without a ping credential: %v", err)
	}
}
