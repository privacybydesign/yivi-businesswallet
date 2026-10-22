//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// proofingListedFlowResp is a flow as a flow list has it.
type proofingListedFlowResp struct {
	proofingFlowResp
	Kind          string `json:"kind"`
	HostedEnabled *bool  `json:"hostedEnabled"`
}

type proofingKindedRequestResp struct {
	proofingRequestResp
	FlowKind string `json:"flowKind"`
}

// See my data and Delete my data are every org's: created the first time an
// admin needs them, never listed among the flows (nor to the API), never
// edited, and sent by their kind, from the wallet and through the API.
func TestProofingStandardFlows(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	const base = "/api/v1/orgs/acme/identity-proofing"
	own := env.proofingFlow("acme", "Passport only")

	standard := decodeJSON[[]proofingListedFlowResp](t, env.do(http.MethodGet, base+"/data-request-flows", nil))
	if len(standard) != 2 || standard[0].Kind != "data_access" || standard[1].Kind != "data_erasure" {
		t.Fatalf("data request flows = %+v; want See my data, then Delete my data", standard)
	}
	for _, f := range standard {
		if f.HostedEnabled == nil || !*f.HostedEnabled || !f.Completable || f.Allowed || f.Default {
			t.Errorf("standard flow = %+v; want sendable as a link, completable, not for members, not the default", f)
		}
	}
	again := decodeJSON[[]proofingListedFlowResp](t, env.do(http.MethodGet, base+"/data-request-flows", nil))
	if len(again) != 2 || again[0].ID != standard[0].ID || again[1].ID != standard[1].ID {
		t.Errorf("data request flows again = %+v; want the same two", again)
	}
	// The admin's own flow and the two the wallet created.
	if n := env.auditCount(orgID, audit.IdentityProofingFlowCreated); n != 3 {
		t.Errorf("flow_created audits = %d, want 3", n)
	}

	listed := decodeJSON[[]proofingListedFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(listed) != 1 || listed[0].ID != own.ID || listed[0].HostedEnabled == nil || !*listed[0].HostedEnabled {
		t.Errorf("flows = %+v; want only the admin's own, sendable as a link", listed)
	}

	access := standard[0].ID
	wantProofingError(t, "editing See my data", env.postJSON(base+"/flows/"+access+"/versions", map[string]any{
		"name": "Mine", "steps": []string{"document_capture", "nfc_read"},
	}), http.StatusConflict, "data_request_flow")
	wantProofingError(t, "switching off See my data's hosted page", env.putJSON(base+"/flows/"+access+"/hosted", map[string]any{
		"enabled": false, "locales": []string{}, "completion": "redirect",
	}), http.StatusConflict, "data_request_flow")
	resp := env.do(http.MethodPut, base+"/flows/"+own.ID+"/kind", nil)
	if resp.StatusCode == http.StatusOK {
		t.Errorf("setting a flow's kind = %d; want no such route", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.postJSON(base+"/requests", map[string]any{"email": "anna@example.org", "flowKind": "data_access"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send See my data = %d, want 201", resp.StatusCode)
	}
	if sent := decodeJSON[proofingKindedRequestResp](t, resp); sent.FlowID != access || sent.FlowKind != "data_access" {
		t.Errorf("sent = %+v; want a data_access request on %s", sent, access)
	}
	wantProofingError(t, "a data request naming the admin's flow", env.postJSON(base+"/requests", map[string]any{
		"email": "anna@example.org", "flowKind": "data_erasure", "flowId": own.ID,
	}), http.StatusBadRequest, "invalid_input")

	// Switched off, a flow's hosted page shows in the list.
	resp = env.putJSON(base+"/flows/"+own.ID+"/hosted", map[string]any{"enabled": false, "locales": []string{}, "completion": "redirect"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("switch off hosted = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	listed = decodeJSON[[]proofingListedFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(listed) != 1 || listed[0].HostedEnabled == nil || *listed[0].HostedEnabled {
		t.Errorf("flows = %+v; want the admin's flow not sendable as a link", listed)
	}

	key := env.proofingKey("acme", "Backend")
	apiFlows := decodeJSON[[]struct {
		ID string `json:"id"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil))
	if len(apiFlows) != 1 || apiFlows[0].ID != own.ID {
		t.Errorf("API flows = %+v; want only the admin's own", apiFlows)
	}
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"email": "anna@example.org", "flowKind": "data_erasure"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("API Delete my data = %d, want 201", resp.StatusCode)
	}
	if session := decodeJSON[struct {
		FlowID   string `json:"flowId"`
		FlowKind string `json:"flowKind"`
	}](t, resp); session.FlowID != standard[1].ID || session.FlowKind != "data_erasure" {
		t.Errorf("API session = %+v; want a data_erasure session on %s", session, standard[1].ID)
	}
	// A caller that kept a standard flow's id still sends on it.
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"email": "anna@example.org", "flowId": access})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("API session on See my data's id = %d, want 201", resp.StatusCode)
	}
	if session := decodeJSON[struct {
		FlowKind string `json:"flowKind"`
	}](t, resp); session.FlowKind != "data_access" {
		t.Errorf("API session on See my data's id = %+v; want data_access", session)
	}
}
