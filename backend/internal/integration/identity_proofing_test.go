//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
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

type proofingCustomerResp struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	FlowIDs       []string `json:"flowIds"`
	DefaultFlowID string   `json:"defaultFlowId"`
}

type proofingCustomerFlowResp struct {
	ID       string `json:"id"`
	Assigned bool   `json:"assigned"`
	Default  bool   `json:"default"`
}

type proofingCustomerRequestResp struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	SubjectUserID string `json:"subjectUserId"`
	CustomerID    string `json:"customerId"`
	CustomerName  string `json:"customerName"`
	SubjectEmail  string `json:"subjectEmail"`
	SubjectName   string `json:"subjectName"`
}

// An admin creates a customer and assigns it a flow members may not use; a
// request for that customer then goes to an external subject by address alone.
func TestIdentityProofingCustomerHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	resp := env.postJSON("/api/v1/orgs/acme/identity-proofing/flows", map[string]any{
		"name": "Passport only", "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow = %d, want 201", resp.StatusCode)
	}
	flow := decodeJSON[proofingFlowResp](t, resp)

	resp = env.postJSON("/api/v1/orgs/acme/customers", map[string]any{"name": "Initech"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create customer = %d, want 201", resp.StatusCode)
	}
	customer := decodeJSON[proofingCustomerResp](t, resp)
	if customer.Name != "Initech" || len(customer.FlowIDs) != 0 {
		t.Errorf("customer = %+v, want Initech with no flows", customer)
	}
	resp = env.postJSON("/api/v1/orgs/acme/customers", map[string]any{"name": "initech"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate customer = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()

	base := "/api/v1/orgs/acme/customers/" + customer.ID
	subject := map[string]any{"customerId": customer.ID, "email": "anna@example.org", "flowId": flow.ID}
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", subject)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("request on an unassigned flow = %d, want 422", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{flow.ID}, "defaultFlowId": flow.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assign flows = %d, want 200", resp.StatusCode)
	}
	if flows := decodeJSON[[]proofingCustomerFlowResp](t, resp); len(flows) != 1 || !flows[0].Assigned || !flows[0].Default {
		t.Errorf("customer flows = %+v, want the flow assigned and default", flows)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingCustomerFlowsConfigured); n != 1 {
		t.Errorf("customer_flows_configured audits = %d, want 1", n)
	}

	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", subject)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("request for customer = %d, want 201", resp.StatusCode)
	}
	created := decodeJSON[proofingCustomerRequestResp](t, resp)
	if created.CustomerID != customer.ID || created.CustomerName != "Initech" || created.SubjectUserID != "" ||
		created.SubjectEmail != "anna@example.org" || created.SubjectName != "" {
		t.Errorf("created = %+v; want Initech's subject by address, no member", created)
	}

	list := decodeJSON[[]proofingCustomerRequestResp](t, env.do(http.MethodGet,
		"/api/v1/orgs/acme/identity-proofing/requests?customerId="+customer.ID, nil))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("customer's requests = %+v, want the one", list)
	}

	// The request's timeline is its audit trail, oldest first.
	timeline := decodeJSON[struct {
		Events []struct {
			Action   string `json:"action"`
			TargetID string `json:"targetId"`
		} `json:"events"`
	}](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests/"+created.ID+"/events", nil))
	if len(timeline.Events) != 2 || timeline.Events[0].Action != audit.IdentityProofingRequested ||
		timeline.Events[1].Action != audit.IdentityProofingSessionCreated || timeline.Events[0].TargetID != created.ID {
		t.Errorf("timeline = %+v; want requested, then session_created", timeline.Events)
	}
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests/00000000-0000-0000-0000-000000000000/events", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown request's timeline = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestIdentityProofingMemberUsesButCannotManageCustomers(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	resp := env.postJSON("/api/v1/orgs/acme/customers", map[string]any{"name": "Initech"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member create customer = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	customers := decodeJSON[[]proofingCustomerResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/customers", nil))
	if len(customers) != 0 {
		t.Errorf("member customers = %+v, want none", customers)
	}
	resp = env.putJSON("/api/v1/orgs/acme/customers/00000000-0000-0000-0000-000000000000/flow-selection",
		map[string]any{"flowIds": []string{}, "defaultFlowId": ""})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("member assign flows = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.do(http.MethodGet, "/api/v1/orgs/acme/customers/00000000-0000-0000-0000-000000000000", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown customer = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

type proofingAPIKeyResp struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	Secret    string `json:"secret"`
	RevokedAt string `json:"revokedAt"`
}

type proofingAPISessionResp struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	FlowID   string `json:"flowId"`
	DeepLink string `json:"deepLink"`
	MailSent bool   `json:"mailSent"`
}

// apiCall calls the public proofing API with a customer key and no cookie.
func (e *testEnv) apiCall(method, path, key string, body any) *http.Response {
	e.t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

func TestIdentityProofingCustomerAPIKeyHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flow := decodeJSON[proofingFlowResp](t, env.postJSON("/api/v1/orgs/acme/identity-proofing/flows", map[string]any{
		"name": "Passport only", "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
	}))
	customer := decodeJSON[proofingCustomerResp](t, env.postJSON("/api/v1/orgs/acme/customers", map[string]any{"name": "Initech"}))
	base := "/api/v1/orgs/acme/customers/" + customer.ID
	resp := env.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{flow.ID}, "defaultFlowId": flow.ID})
	_ = resp.Body.Close()

	resp = env.postJSON(base+"/api-keys", map[string]any{"name": "Production backend"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d, want 201", resp.StatusCode)
	}
	key := decodeJSON[proofingAPIKeyResp](t, resp)
	if !strings.HasPrefix(key.Secret, "yp_live_") || !strings.HasPrefix(key.Secret, key.Prefix) {
		t.Fatalf("created key = %+v; want a yp_live_ secret starting with its prefix", key)
	}
	listed := decodeJSON[[]proofingAPIKeyResp](t, env.do(http.MethodGet, base+"/api-keys", nil))
	if len(listed) != 1 || listed[0].Secret != "" {
		t.Errorf("listed keys = %+v; want the one key without its secret", listed)
	}

	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", "yp_live_nope", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown key = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// No flow named: the customer's default. No mail: the caller shows the link.
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("api create session = %d, want 201", resp.StatusCode)
	}
	session := decodeJSON[proofingAPISessionResp](t, resp)
	if session.Status != "pending" || session.FlowID != flow.ID || !strings.HasPrefix(session.DeepLink, "vcmrtd://") || session.MailSent {
		t.Errorf("api session = %+v; want pending on the default flow with its deep link and no mail", session)
	}
	got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID, key.Secret, nil))
	if got.ID != session.ID {
		t.Errorf("api get session = %+v, want %s", got, session.ID)
	}

	// The org's list names the key as the sender.
	list := decodeJSON[[]struct {
		ID         string `json:"id"`
		APIKeyName string `json:"apiKeyName"`
	}](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests?customerId="+customer.ID, nil))
	if len(list) != 1 || list[0].APIKeyName != "Production backend" {
		t.Errorf("requests = %+v, want the one sent by the key", list)
	}

	resp = env.do(http.MethodPatch, base, strings.NewReader(`{"paused":true}`))
	_ = resp.Body.Close()
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"email": "anna@example.org"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("api create session while paused = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.do(http.MethodDelete, base+"/api-keys/"+key.ID, nil)
	if revoked := decodeJSON[proofingAPIKeyResp](t, resp); revoked.RevokedAt == "" {
		t.Errorf("revoked key = %+v, want revokedAt", revoked)
	}
	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked key = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()
	for action, want := range map[string]int{
		audit.IdentityProofingAPIKeyCreated: 1, audit.IdentityProofingAPIKeyRevoked: 1,
	} {
		if n := env.auditCount(orgID, action); n != want {
			t.Errorf("%s audits = %d, want %d", action, n, want)
		}
	}

	resp = env.do(http.MethodDelete, base, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove customer = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.do(http.MethodGet, base, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("removed customer = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
