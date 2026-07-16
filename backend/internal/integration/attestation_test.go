//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// postJSON sends a JSON body and returns the response.
func (e *testEnv) postJSON(path string, body any) *http.Response {
	e.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("marshal body: %v", err)
	}
	return e.do(http.MethodPost, path, bytes.NewReader(raw))
}

func decodeJSON[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

type attSchemaResp struct {
	ID string `json:"id"`
}

type attIssuedResp struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	OfferURI string `json:"offerUri"`
}

// seedAttestationTemplate logs in an admin for a fresh org and creates a schema +
// template, returning the created template id.
func seedAttestationTemplate(t *testing.T, env *testEnv, slug string) string {
	t.Helper()
	admin := env.login("admin@" + slug + ".test")
	orgID := env.createOrg("Org "+slug, slug)
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)

	resp := env.postJSON("/api/v1/orgs/"+slug+"/attestations/schemas", map[string]any{
		"vct":                "nl." + slug + ".employee",
		"displayName":        "Employee",
		"credentialConfigId": "EmailCredentialSdJwt",
		"attributes": []map[string]any{
			{"key": "email", "label": "E-mail", "type": "string", "required": true},
			{"key": "department", "label": "Department", "type": "string"},
		},
		"qualified": false,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create schema = %d, want 201", resp.StatusCode)
	}
	schema := decodeJSON[attSchemaResp](t, resp)

	resp = env.postJSON("/api/v1/orgs/"+slug+"/attestations/templates", map[string]any{
		"schemaId": schema.ID,
		"name":     "Employee",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create template = %d, want 201", resp.StatusCode)
	}
	return decodeJSON[attSchemaResp](t, resp).ID
}

// TestAttestationIssuanceHTTPFlow drives the assembled router as an admin: define
// a schema + template, issue to an external recipient (202 + offerUri), reject an
// undeclared attribute (data minimisation), and poll the stub issuer to claimed.
func TestAttestationIssuanceHTTPFlow(t *testing.T) {
	env := setup(t)
	templateID := seedAttestationTemplate(t, env, "caesar")

	resp := env.postJSON("/api/v1/orgs/caesar/attestations", map[string]any{
		"templateId": templateID,
		"recipient":  map[string]any{"kind": "external", "ref": "anna@example.com"},
		"attributes": map[string]string{"email": "anna@example.com", "department": "Platform"},
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("issue = %d, want 202", resp.StatusCode)
	}
	issued := decodeJSON[attIssuedResp](t, resp)
	if issued.Status != "offered" || issued.OfferURI == "" {
		t.Fatalf("expected offered with offerUri, got %+v", issued)
	}

	// Data minimisation: an undeclared attribute is rejected (400).
	resp = env.postJSON("/api/v1/orgs/caesar/attestations", map[string]any{
		"templateId": templateID,
		"recipient":  map[string]any{"kind": "external", "ref": "x@example.com"},
		"attributes": map[string]string{"email": "x@example.com", "salary": "secret"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("undeclared attribute issue = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Polling reconciles with the stub issuer -> claimed.
	resp = env.do(http.MethodGet, "/api/v1/orgs/caesar/attestations/"+issued.ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get issued = %d, want 200", resp.StatusCode)
	}
	if polled := decodeJSON[attIssuedResp](t, resp); polled.Status != "claimed" {
		t.Fatalf("expected claimed after poll, got %q", polled.Status)
	}

	// The ledger has exactly the one successfully-issued row.
	resp = env.do(http.MethodGet, "/api/v1/orgs/caesar/attestations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list ledger = %d, want 200", resp.StatusCode)
	}
	if ledger := decodeJSON[[]attIssuedResp](t, resp); len(ledger) != 1 {
		t.Fatalf("expected 1 ledger row, got %d", len(ledger))
	}
}

// TestAttestationMemberCannotManageOrIssue verifies a non-admin member can read
// the ledger but cannot manage schemas or issue attestations.
func TestAttestationMemberCannotManageOrIssue(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	// Member can read the (empty) ledger.
	resp := env.do(http.MethodGet, "/api/v1/orgs/acme/attestations", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("member list ledger = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Member cannot create a schema.
	resp = env.postJSON("/api/v1/orgs/acme/attestations/schemas", map[string]any{
		"vct": "nl.acme.sneaky", "displayName": "Sneaky", "credentialConfigId": "X",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member create schema = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Member cannot issue.
	resp = env.postJSON("/api/v1/orgs/acme/attestations", map[string]any{
		"templateId": "00000000-0000-0000-0000-000000000000",
		"recipient":  map[string]any{"kind": "external", "ref": "y@example.com"},
		"attributes": map[string]string{},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member issue = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
