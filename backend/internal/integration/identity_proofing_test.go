//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
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

// putJSON sends a JSON body with PUT and returns the response.
func (e *testEnv) putJSON(path string, body any) *http.Response {
	e.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		e.t.Fatalf("marshal body: %v", err)
	}
	return e.do(http.MethodPut, path, bytes.NewReader(raw))
}

func TestProofingAdminHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	// The org is the engine's tenant: nothing is provisioned on first use.
	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/flows", nil))
	if len(flows) != 0 {
		t.Fatalf("flows of a new org = %+v, want none", flows)
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

func TestProofingMemberCannotManage(t *testing.T) {
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
	HasAPIKey     bool     `json:"hasApiKey"`
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
func TestProofingCustomerHTTPFlow(t *testing.T) {
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
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("request before a live key = %d, want 409", resp.StatusCode)
	}
	if body := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp); body.Code != "customer_no_api_key" {
		t.Errorf("code = %q, want customer_no_api_key", body.Code)
	}
	resp = env.postJSON(base+"/api-keys", map[string]any{"name": "Backend"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create api key = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if got := decodeJSON[proofingCustomerResp](t, env.do(http.MethodGet, base, nil)); !got.HasAPIKey {
		t.Errorf("customer = %+v, want hasApiKey", got)
	}

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

func TestProofingMemberCustomerUse(t *testing.T) {
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
	ID        string `json:"id"`
	Status    string `json:"status"`
	FlowID    string `json:"flowId"`
	DeepLink  string `json:"deepLink"`
	MailSent  bool   `json:"mailSent"`
	ErrorCode string `json:"errorCode"`
	PurgedAt  string `json:"purgedAt"`
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

func TestProofingCustomerAPIKeyFlow(t *testing.T) {
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

	// The key is the audit actor, not a member.
	var actorUser *string
	var actorLabel string
	if err := env.pool.QueryRow(context.Background(), `SELECT actor_user_id::text, COALESCE(actor_label, '')
		FROM audit_events WHERE action = $1 ORDER BY occurred_at DESC LIMIT 1`,
		audit.IdentityProofingRequested).Scan(&actorUser, &actorLabel); err != nil {
		t.Fatalf("read requested audit: %v", err)
	}
	if actorUser != nil || actorLabel != "api_key:"+key.Prefix {
		t.Errorf("requested actor = %v / %q, want no user and api_key:%s", actorUser, actorLabel, key.Prefix)
	}

	// A retry with the same Idempotency-Key replays the first answer; the key
	// with another body is refused.
	idem := func(email string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/v1/proofing/sessions",
			strings.NewReader(`{"email":"`+email+`","sendMail":false}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+key.Secret)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "retry-1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("idempotent create: %v", err)
		}
		return resp
	}
	first := decodeJSON[proofingAPISessionResp](t, idem("cleo@example.org"))
	again := idem("cleo@example.org")
	if again.Header.Get("Idempotent-Replayed") != "true" {
		t.Errorf("retry not replayed: %v", again.Header)
	}
	if replayed := decodeJSON[proofingAPISessionResp](t, again); replayed.ID != first.ID {
		t.Errorf("retry = %s, want the first session %s", replayed.ID, first.ID)
	}
	resp = idem("dave@example.org")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("key reused with another body = %d, want 422", resp.StatusCode)
	}
	_ = resp.Body.Close()
	// The kept answer is sealed: the subject's address is not in it.
	var stored []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT response FROM identity_proofing_idempotency_keys
		WHERE key LIKE '%retry-1'`).Scan(&stored); err != nil {
		t.Fatalf("read idempotent answer: %v", err)
	}
	if strings.Contains(string(stored), "cleo@example.org") {
		t.Error("idempotent answer stored in plaintext")
	}
	page := decodeJSON[struct {
		Sessions   []proofingAPISessionResp `json:"sessions"`
		NextCursor *string                  `json:"nextCursor"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions?limit=1", key.Secret, nil))
	if len(page.Sessions) != 1 || page.Sessions[0].ID != first.ID || page.NextCursor == nil {
		t.Fatalf("first page = %+v; want the newest session and a cursor", page)
	}
	rest := decodeJSON[struct {
		Sessions []proofingAPISessionResp `json:"sessions"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions?cursor="+*page.NextCursor, key.Secret, nil))
	if len(rest.Sessions) != 1 || rest.Sessions[0].ID != session.ID {
		t.Errorf("next page = %+v; want the one session before it", rest.Sessions)
	}

	// The customer cancels a second session, then erases it.
	second := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "bob@example.org", "sendMail": false}))
	cancelURL := "/api/v1/proofing/sessions/" + second.ID + "/cancel"
	if got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, cancelURL, key.Secret, nil)); got.Status != "cancelled" {
		t.Errorf("cancel = %+v, want cancelled", got)
	}
	resp = env.apiCall(http.MethodPost, cancelURL, key.Secret, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second cancel = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.apiCall(http.MethodDelete, "/api/v1/proofing/sessions/"+second.ID, key.Secret, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+second.ID, key.Secret, nil)); got.PurgedAt == "" {
		t.Errorf("after delete = %+v, want purgedAt", got)
	}
	for _, action := range []string{audit.IdentityProofingSessionCancelled, audit.IdentityProofingSessionPurged} {
		if n := env.auditCount(orgID, action); n != 1 {
			t.Errorf("%s audits = %d, want 1", action, n)
		}
	}

	// The org's list names the key as the sender.
	list := decodeJSON[[]struct {
		ID         string `json:"id"`
		APIKeyName string `json:"apiKeyName"`
	}](t, env.do(http.MethodGet, "/api/v1/orgs/acme/identity-proofing/requests?customerId="+customer.ID, nil))
	if len(list) != 3 || list[0].APIKeyName != "Production backend" {
		t.Errorf("requests = %+v, want the three sent by the key", list)
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

// A key reads an approved session's identity only with results:read, and
// creates one only with sessions:write.
func TestProofingAPIKeyScopesFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusApproved
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

	key := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "CI"}))
	if !strings.HasPrefix(key.Secret, "yp_live_") {
		t.Fatalf("key = %+v; want a yp_live_ secret", key)
	}
	approved := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	narrowed := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "Status"}))
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE identity_proofing_api_keys SET scopes = ARRAY['sessions:read'] WHERE id = $1`, narrowed.ID); err != nil {
		t.Fatalf("narrow key: %v", err)
	}
	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+approved.ID+"/result", narrowed.Secret, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("result without results:read = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", narrowed.Secret, map[string]any{"email": "anna@example.org"})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("create without sessions:write = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	result := decodeJSON[struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Identity *struct {
			FamilyName string `json:"familyName"`
		} `json:"identity"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+approved.ID+"/result", key.Secret, nil))
	if result.ID != approved.ID || result.Status != "approved" || result.Identity == nil || result.Identity.FamilyName == "" {
		t.Errorf("result = %+v; want the approved identity", result)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingResultRead); n != 1 {
		t.Errorf("result_read audits = %d, want 1", n)
	}
}

// A hosted session is a link: the subject opens the public page without an
// account, starts once for the app they pick, and the page follows the outcome.
func TestProofingHostedLinkFlow(t *testing.T) {
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
	key := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "Portal"}))

	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hosted = %d, want 201", resp.StatusCode)
	}
	created := decodeJSON[struct {
		Status    string `json:"status"`
		HostedURL string `json:"hostedUrl"`
		DeepLink  string `json:"deepLink"`
		MailSent  bool   `json:"mailSent"`
	}](t, resp)
	_, token, ok := strings.Cut(created.HostedURL, "/p/")
	if !ok || token == "" || created.Status != "pending" || created.DeepLink != "" || created.MailSent {
		t.Fatalf("hosted session = %+v; want a pending link to /p/<token>, no deep link, no mail", created)
	}

	type hostedView struct {
		Status   string `json:"status"`
		Started  bool   `json:"started"`
		DeepLink string `json:"deepLink"`
		Customer struct {
			Name string `json:"name"`
		} `json:"customer"`
		Flow struct {
			Name string `json:"name"`
		} `json:"flow"`
	}
	view := decodeJSON[hostedView](t, env.apiCall(http.MethodGet, "/api/v1/proof/"+token, "", nil))
	if view.Status != "pending" || view.Started || view.Customer.Name != "Initech" || view.Flow.Name != "Passport only" {
		t.Errorf("hosted page = %+v; want a pending, unstarted Initech page for the flow", view)
	}
	resp = env.apiCall(http.MethodGet, "/api/v1/proof/not-a-token", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown link = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.apiCall(http.MethodPost, "/api/v1/proof/"+token+"/start", "", map[string]any{"method": "idem_app"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start = %d, want 201", resp.StatusCode)
	}
	if started := decodeJSON[hostedView](t, resp); !started.Started || !strings.HasPrefix(started.DeepLink, "vcmrtd://") {
		t.Errorf("started = %+v; want started with its vcmrtd deep link", started)
	}
	resp = env.apiCall(http.MethodPost, "/api/v1/proof/"+token+"/start", "", map[string]any{"method": "idem_app"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second start = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if status := decodeJSON[hostedView](t, env.apiCall(http.MethodGet, "/api/v1/proof/"+token+"/status", "", nil)); !status.Started {
		t.Errorf("status = %+v, want started", status)
	}

	// A member makes the same link in the wallet, to hand the subject.
	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"customerId": customer.ID, "flowId": flow.ID, "channel": "hosted", "name": "Dibran Mulder",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("wallet hosted request = %d, want 201", resp.StatusCode)
	}
	fromWallet := decodeJSON[struct {
		Status    string `json:"status"`
		HostedURL string `json:"hostedUrl"`
		MailSent  bool   `json:"mailSent"`
	}](t, resp)
	if _, walletToken, ok := strings.Cut(fromWallet.HostedURL, "/p/"); !ok || walletToken == "" || fromWallet.Status != "pending" || fromWallet.MailSent {
		t.Errorf("wallet hosted request = %+v; want a pending link to /p/<token>, no mail", fromWallet)
	}
}

// A customer with its own UI picks the app of a hosted session over the API and
// polls it from stored state; a session not created hosted has no such start.
func TestProofingHeadlessHTTPFlow(t *testing.T) {
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
	key := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "Portal"}))

	hosted := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"hosted": true}))
	start := "/api/v1/proofing/sessions/" + hosted.ID + "/methods/idem_app"
	started := decodeJSON[struct {
		ID      string `json:"id"`
		AppLink string `json:"appLink"`
	}](t, env.apiCall(http.MethodPost, start, key.Secret, nil))
	if started.ID != hosted.ID || !strings.HasPrefix(started.AppLink, "vcmrtd://") {
		t.Fatalf("headless start = %+v; want the Idem app link", started)
	}
	status := decodeJSON[struct {
		Status string `json:"status"`
		Done   bool   `json:"done"`
	}](t, env.apiCall(http.MethodGet, start+"/status", key.Secret, nil))
	if status.Status != "pending" || status.Done {
		t.Errorf("headless status = %+v, want pending", status)
	}
	resp = env.apiCall(http.MethodPost, start, key.Secret, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("second start = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()

	mailed := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions/"+mailed.ID+"/methods/idem_app", key.Secret, nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("headless start of a mailed session = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// An admin decides a session under review; the outcome then lands as any
// other.
func TestProofingReviewDecisionFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusNeedsReview
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
	key := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "CI"}))
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID,
		key.Secret, nil)); read.Status != "needs_review" {
		t.Fatalf("session = %+v, want needs_review", read)
	}

	review := "/api/v1/orgs/acme/identity-proofing/requests/" + session.ID + "/review"
	resp = env.postJSON(review, map[string]any{"decision": "approve"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("decision without a reason = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.postJSON(review, map[string]any{"decision": "approve", "reason": "document checked by hand"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("decision = %d, want 200", resp.StatusCode)
	}
	if decided := decodeJSON[proofingRequestResp](t, resp); decided.Status != "approved" {
		t.Errorf("decided = %+v, want approved", decided)
	}
	resp = env.postJSON(review, map[string]any{"decision": "reject", "reason": "again"})
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("a second decision = %d, want 409", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := env.auditCount(orgID, audit.IdentityProofingReviewDecided); n != 1 {
		t.Errorf("review_decided audits = %d, want 1", n)
	}
}

// The public API holds each customer to proofing.APISessionLimit sessions,
// answering 429 with Retry-After past it; reads have their own, larger budget.
func TestProofingCustomerRateLimit(t *testing.T) {
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
	key := decodeJSON[proofingAPIKeyResp](t, env.postJSON(base+"/api-keys", map[string]any{"name": "Backend"}))

	create := func() *http.Response {
		return env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
			map[string]any{"email": "anna@example.org", "sendMail": false})
	}
	for i := range proofing.APISessionLimit.Burst {
		resp := create()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create session %d = %d, want 201 within the limit", i+1, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	resp = create()
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("create past the limit = %d, Retry-After %q; want 429 with Retry-After",
			resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if body := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp); body.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Code)
	}

	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read after the session limit = %d, want 200: reads have their own budget", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// proofingTenant is one org with a customer, its API key and a session
// created through that key.
type proofingTenant struct {
	slug, customerID, key, sessionID string
}

// newProofingTenant sets up slug as its admin (the caller logged in) with
// customerName.
func (e *testEnv) newProofingTenant(slug, customerName string) proofingTenant {
	e.t.Helper()
	flow := decodeJSON[proofingFlowResp](e.t, e.postJSON("/api/v1/orgs/"+slug+"/identity-proofing/flows", map[string]any{
		"name": "Passport only", "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
	}))
	customer := decodeJSON[proofingCustomerResp](e.t, e.postJSON("/api/v1/orgs/"+slug+"/customers", map[string]any{"name": customerName}))
	base := "/api/v1/orgs/" + slug + "/customers/" + customer.ID
	resp := e.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{flow.ID}, "defaultFlowId": flow.ID})
	_ = resp.Body.Close()
	key := decodeJSON[proofingAPIKeyResp](e.t, e.postJSON(base+"/api-keys", map[string]any{"name": "backend"}))
	session := decodeJSON[proofingAPISessionResp](e.t, e.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "person@" + slug + ".test", "sendMail": false}))
	return proofingTenant{slug: slug, customerID: customer.ID, key: key.Secret, sessionID: session.ID}
}

// One customer's key never reaches another customer's sessions, in its own
// org or another; one org's admin never reaches another org's customers.
func TestProofingTenantIsolation(t *testing.T) {
	env := setup(t)
	acmeID := env.createOrg("Acme", "acme")
	globexID := env.createOrg("Globex", "globex")

	acmeAdmin := env.loginAs("admin@acme.test")
	env.addMembership(acmeAdmin, acmeID, organization.RoleAdmin)
	initech := env.newProofingTenant("acme", "Initech")
	hooli := env.newProofingTenant("acme", "Hooli")

	globexAdmin := env.loginAs("admin@globex.test")
	env.addMembership(globexAdmin, globexID, organization.RoleAdmin)
	umbrella := env.newProofingTenant("globex", "Umbrella")

	for name, pair := range map[string][2]proofingTenant{
		"another customer of the org": {initech, hooli},
		"a customer of another org":   {initech, umbrella},
	} {
		caller, target := pair[0], pair[1]
		t.Run(name, func(t *testing.T) {
			for _, call := range []struct{ method, path string }{
				{http.MethodGet, "/api/v1/proofing/sessions/" + target.sessionID},
				{http.MethodGet, "/api/v1/proofing/sessions/" + target.sessionID + "/result"},
				{http.MethodGet, "/api/v1/proofing/sessions/" + target.sessionID + "/data-export"},
				{http.MethodPost, "/api/v1/proofing/sessions/" + target.sessionID + "/cancel"},
				{http.MethodDelete, "/api/v1/proofing/sessions/" + target.sessionID},
			} {
				resp := env.apiCall(call.method, call.path, caller.key, map[string]any{})
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("%s %s with another customer's key = %d, want 404", call.method, call.path, resp.StatusCode)
				}
			}
			listed := decodeJSON[struct {
				Sessions []proofingAPISessionResp `json:"sessions"`
			}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions", caller.key, nil))
			if len(listed.Sessions) != 1 {
				t.Errorf("listed %d sessions, want only the caller's own", len(listed.Sessions))
			}
			for _, s := range listed.Sessions {
				if s.ID != caller.sessionID {
					t.Errorf("listed session %s, want only the caller's own %s", s.ID, caller.sessionID)
				}
			}
		})
	}

	// Still logged in as Globex's admin: Acme's customers are out of reach,
	// by Acme's path or by Globex's with an Acme customer's id.
	for _, path := range []string{
		"/api/v1/orgs/acme/customers/" + initech.customerID,
		"/api/v1/orgs/globex/customers/" + initech.customerID,
		"/api/v1/orgs/globex/customers/" + initech.customerID + "/api-keys",
	} {
		resp := env.do(http.MethodGet, path, nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s as another org's admin = %d, want 403 or 404", path, resp.StatusCode)
		}
	}
}
