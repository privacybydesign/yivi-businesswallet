//go:build integration

package integration

import (
	"net/http"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type proofingFlowResp struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Active      bool   `json:"active"`
	Name        string `json:"name"`
	Completable bool   `json:"completable"`
	Allowed     bool   `json:"allowed"`
	Default     bool   `json:"default"`
}

type proofingRequestResp struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FlowName      string `json:"flowName"`
	FlowVersion   int    `json:"flowVersion"`
	SubjectUserID string `json:"subjectUserId"`
	MailSent      bool   `json:"mailSent"`
}

type proofingMemberResp struct {
	UserID string `json:"userId"`
	Role   string `json:"role"`
}

func TestIdentityProofingAdminHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	// The org's IPS tenant is provisioned on first use, once.
	for range 2 {
		flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/flows", nil))
		if len(flows) != 0 {
			t.Fatalf("flows of a new org = %+v, want none", flows)
		}
	}
	if n := env.auditCount(orgID, audit.IdentityProofingProvisioned); n != 1 {
		t.Errorf("provisioned audits = %d, want 1", n)
	}

	resp := env.postJSON("/api/v1/orgs/acme/identity-proofing/flows", map[string]any{
		"name": "Passport + face", "steps": []string{"nfc_read", "face_match"}, "requiredChecks": []string{"nfc.passive_auth", "face.match"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow = %d, want 201", resp.StatusCode)
	}
	flow := decodeJSON[proofingFlowResp](t, resp)
	if !flow.Completable || flow.Allowed {
		t.Errorf("created flow = %+v; want completable and not yet available to members", flow)
	}

	members := decodeJSON[[]proofingMemberResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/members", nil))
	if len(members) != 1 || members[0].UserID != me.ID.String() || members[0].Role != string(organization.RoleAdmin) {
		t.Fatalf("members = %+v, want the admin themself", members)
	}

	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"userId": me.ID, "flowId": flow.ID,
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("request on a flow not made available = %d, want 422", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.putJSON("/api/v1/orgs/acme/identity-proofing/flow-selection", map[string]any{
		"flowIds": []string{flow.ID}, "defaultFlowId": flow.ID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flow selection = %d, want 200", resp.StatusCode)
	}
	if selected := decodeJSON[[]proofingFlowResp](t, resp); len(selected) != 1 || !selected[0].Allowed || !selected[0].Default {
		t.Errorf("flows after selection = %+v, want the flow allowed and default", selected)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingFlowsConfigured); n != 1 {
		t.Errorf("flows_configured audits = %d, want 1", n)
	}

	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"userId": me.ID, "flowId": flow.ID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create request = %d, want 201", resp.StatusCode)
	}
	created := decodeJSON[proofingRequestResp](t, resp)
	if created.Status != "pending" || created.FlowName != "Passport + face" || created.FlowVersion != 1 ||
		created.SubjectUserID != me.ID.String() || created.MailSent {
		t.Errorf("created request = %+v; want pending on version 1 for the admin, no mailer wired", created)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingRequested); n != 1 {
		t.Errorf("requested audits = %d, want 1", n)
	}

	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"userId": me.ID, "flowId": "unknown",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("request on unknown flow = %d, want 422", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Editing a flow adds a version that is active at once; activating the first
	// rolls it back.
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/flows/"+flow.ID+"/versions", map[string]any{
		"name": "Passport + face v2", "steps": []string{"nfc_read", "face_match"}, "requiredChecks": []string{"nfc.passive_auth", "face.match"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("new flow version = %d, want 201", resp.StatusCode)
	}
	if v2 := decodeJSON[proofingFlowResp](t, resp); v2.Version != 2 || !v2.Active {
		t.Errorf("new version = %+v, want version 2 active", v2)
	}
	versions := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/flows/"+flow.ID+"/versions", nil))
	if len(versions) != 2 || versions[0].Active || !versions[1].Active {
		t.Errorf("versions = %+v, want v1 inactive and v2 active", versions)
	}
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/flows/"+flow.ID+"/versions/1/activate", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("activate v1 = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := env.auditCount(orgID, audit.IdentityProofingFlowVersionCreated); n != 1 {
		t.Errorf("flow_version_created audits = %d, want 1", n)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingFlowVersionActivated); n != 1 {
		t.Errorf("flow_version_activated audits = %d, want 1", n)
	}

	list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests", nil))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v, want the one request", list)
	}

	resp = env.do(http.MethodGet, "/api/v1/identity-proofing/not-a-token", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown proofing link = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestIdentityProofingMemberCanRequestButNotManage(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.postJSON("/api/v1/orgs/acme/identity-proofing/flows", map[string]any{"name": "x", "steps": []string{"nfc_read"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member create flow = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.putJSON("/api/v1/orgs/acme/identity-proofing/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": ""})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member flow selection = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/flows/f/versions", map[string]any{"name": "x", "steps": []string{"nfc_read"}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member new flow version = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The request routes are member routes: with no flow made available the
	// request is refused on the flow, not on the role.
	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/flows", nil))
	if len(flows) != 0 {
		t.Errorf("member flows = %+v, want none made available", flows)
	}
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"userId": me.ID, "flowId": "f",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("member request on an unknown flow = %d, want 422", resp.StatusCode)
	}
	_ = resp.Body.Close()
	list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests", nil))
	if len(list) != 0 {
		t.Errorf("member list = %+v, want empty", list)
	}
}
