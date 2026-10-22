//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const (
	headerContentType = "Content-Type"
	contentTypePNG    = "image/png"
)

type proofingFlowResp struct {
	ID          string `json:"id"`
	Version     int    `json:"version"`
	Active      bool   `json:"active"`
	Name        string `json:"name"`
	Completable bool   `json:"completable"`
	Allowed     bool   `json:"allowed"`
	Default     bool   `json:"default"`
	// RetentionDays is how many days an external person's data is kept on the flow.
	RetentionDays int `json:"retentionDays"`
}

type proofingRequestResp struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	FlowID        string `json:"flowId"`
	FlowName      string `json:"flowName"`
	FlowVersion   int    `json:"flowVersion"`
	SubjectUserID string `json:"subjectUserId"`
	SubjectEmail  string `json:"subjectEmail"`
	SubjectName   string `json:"subjectName"`
	External      bool   `json:"external"`
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

// proofingStatsResp is the per-flow count of the external requests sent lately.
type proofingStatsResp struct {
	Rows []struct {
		FlowID   string `json:"flowId"`
		Sessions int    `json:"sessions"`
	} `json:"rows"`
}

// wantProofingError requires resp to be the API's error with this status and code.
func wantProofingError(t *testing.T, what string, resp *http.Response, status int, code string) {
	t.Helper()
	got := resp.StatusCode
	body := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp)
	if got != status || body.Code != code {
		t.Errorf("%s = %d %q, want %d %q", what, got, body.Code, status, code)
	}
}

// proofingFlow defines a flow in org slug's flow list, as an admin.
func (e *testEnv) proofingFlow(slug, name string) proofingFlowResp {
	e.t.Helper()
	resp := e.postJSON("/api/v1/orgs/"+slug+"/identity-proofing/flows", map[string]any{
		"name": name, "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
	})
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create flow %q = %d, want 201", name, resp.StatusCode)
	}
	return decodeJSON[proofingFlowResp](e.t, resp)
}

// proofingDefaultFlow defines the "Passport only" flow and makes it org slug's
// default flow, without making it available to plain members.
func (e *testEnv) proofingDefaultFlow(slug string) proofingFlowResp {
	e.t.Helper()
	flow := e.proofingFlow(slug, "Passport only")
	resp := e.putJSON("/api/v1/orgs/"+slug+"/identity-proofing/flow-selection",
		map[string]any{"flowIds": []string{}, "defaultFlowId": flow.ID})
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("set default flow = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	return flow
}

// proofingKey creates an API key for org slug, as an admin.
func (e *testEnv) proofingKey(slug, name string) proofingAPIKeyResp {
	e.t.Helper()
	resp := e.postJSON("/api/v1/orgs/"+slug+"/identity-proofing/api-keys", map[string]any{"name": name})
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("create api key %q = %d, want 201", name, resp.StatusCode)
	}
	return decodeJSON[proofingAPIKeyResp](e.t, resp)
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
	wantProofingError(t, "request on a flow not made available", resp, http.StatusUnprocessableEntity, "flow_not_allowed")

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
		created.SubjectUserID != me.ID.String() || created.External || created.MailSent {
		t.Errorf("created request = %+v; want pending on version 1 for the admin as a member, no mailer wired", created)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingRequested); n != 1 {
		t.Errorf("requested audits = %d, want 1", n)
	}

	resp = env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"userId": me.ID, "flowId": "unknown",
	})
	wantProofingError(t, "request on unknown flow", resp, http.StatusUnprocessableEntity, "flow_not_found")

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

// A plain member reads the org's proofing settings and the flows made available
// to them, and sends requests on those; everything the admin manages (flows,
// settings, API keys, the webhook) is refused.
func TestProofingMemberCannotManage(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("member@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleMember)

	base := "/api/v1/orgs/acme/identity-proofing"
	const noID = "00000000-0000-0000-0000-000000000000"
	adminOnly := []struct{ method, path, body string }{
		{http.MethodPost, base + "/flows", `{"name":"x","steps":["nfc_read"]}`},
		{http.MethodPut, base + "/flow-selection", `{"flowIds":[],"defaultFlowId":""}`},
		{http.MethodPost, base + "/flows/f/versions", `{"name":"x","steps":["nfc_read"]}`},
		{http.MethodPatch, base + "/settings", `{"allowedRedirectOrigins":[]}`},
		{http.MethodPut, base + "/settings/branding", ""},
		{http.MethodGet, base + "/api-keys", ""},
		{http.MethodPost, base + "/api-keys", `{"name":"x"}`},
		{http.MethodDelete, base + "/api-keys/" + noID, ""},
		{http.MethodGet, base + "/webhook", ""},
		{http.MethodPut, base + "/webhook", `{"url":"https://hooks.example.org/yivi","events":["session.verified"]}`},
		{http.MethodDelete, base + "/webhook", ""},
		{http.MethodPost, base + "/webhook/rotate-secret", ""},
		{http.MethodPost, base + "/webhook/test", ""},
		{http.MethodGet, base + "/webhook/deliveries", ""},
	}
	for _, c := range adminOnly {
		resp := env.do(c.method, c.path, strings.NewReader(c.body))
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("member %s %s = %d, want 403", c.method, c.path, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}

	// Reading the settings and the logo is for every member.
	settings := decodeJSON[proofingSettingsResp](t, env.do(http.MethodGet, base+"/settings", nil))
	if settings.SessionTTLSeconds != int(proofing.SessionTTL.Seconds()) {
		t.Errorf("member settings = %+v, want the default session lifetime", settings)
	}
	wantProofingError(t, "member reads the logo of an org without one", env.do(http.MethodGet, base+"/settings/logo", nil),
		http.StatusNotFound, "not_found")

	// The request routes are member routes: with no flow made available the
	// request is refused on the flow, not on the role.
	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(flows) != 0 {
		t.Errorf("member flows = %+v, want none made available", flows)
	}
	resp := env.postJSON(base+"/requests", map[string]any{
		"userId": me.ID, "flowId": "f",
	})
	wantProofingError(t, "member request on an unknown flow", resp, http.StatusUnprocessableEntity, "flow_not_found")
	resp = env.postJSON(base+"/requests", map[string]any{"email": "anna@example.org"})
	wantProofingError(t, "member request to an external person with no default flow", resp,
		http.StatusUnprocessableEntity, "no_default_flow")
	list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests", nil))
	if len(list) != 0 {
		t.Errorf("member list = %+v, want empty", list)
	}
}

// An admin sends an external person a request by address alone, on any flow of
// the org (members need not be allowed it), or on the org's default flow when
// none is named. No API key is needed to send from the app.
func TestExternalRequestHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	flow := env.proofingFlow("acme", "Passport only")

	// The org has no API key and the flow is not made available to members: an
	// admin still sends it, to a person without a login.
	resp := env.postJSON(base+"/requests", map[string]any{"email": "anna@example.org", "flowId": flow.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("external request = %d, want 201", resp.StatusCode)
	}
	created := decodeJSON[proofingRequestResp](t, resp)
	if !created.External || created.SubjectUserID != "" || created.SubjectEmail != "anna@example.org" ||
		created.SubjectName != "" || created.FlowID != flow.ID {
		t.Errorf("created = %+v; want an external subject by address on the flow, no member", created)
	}

	// Without a flowId the org's default flow is used, and there is none yet.
	resp = env.postJSON(base+"/requests", map[string]any{"email": "bram@example.org", "name": "Bram de Vries"})
	wantProofingError(t, "external request with no flow and no default", resp, http.StatusUnprocessableEntity, "no_default_flow")

	// The default flow is chosen apart from the flows members may use.
	resp = env.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": "nope"})
	wantProofingError(t, "default flow that does not exist", resp, http.StatusUnprocessableEntity, "flow_not_found")
	resp = env.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": flow.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set default flow = %d, want 200", resp.StatusCode)
	}
	if flows := decodeJSON[[]proofingFlowResp](t, resp); len(flows) != 1 || flows[0].Allowed || !flows[0].Default {
		t.Errorf("flows = %+v, want the flow the default but not available to members", flows)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingFlowsConfigured); n != 1 {
		t.Errorf("flows_configured audits = %d, want 1", n)
	}

	resp = env.postJSON(base+"/requests", map[string]any{"email": "bram@example.org", "name": "Bram de Vries"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("external request on the default flow = %d, want 201", resp.StatusCode)
	}
	byDefault := decodeJSON[proofingRequestResp](t, resp)
	if !byDefault.External || byDefault.FlowID != flow.ID || byDefault.SubjectName != "Bram de Vries" {
		t.Errorf("by default = %+v; want Bram on the default flow", byDefault)
	}

	list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests", nil))
	if len(list) != 2 || list[0].ID != byDefault.ID || list[1].ID != created.ID {
		t.Errorf("requests = %+v, want the two, newest first", list)
	}
	stats := decodeJSON[proofingStatsResp](t, env.do(http.MethodGet, base+"/stats", nil))
	if len(stats.Rows) != 1 || stats.Rows[0].FlowID != flow.ID || stats.Rows[0].Sessions != 2 {
		t.Errorf("stats = %+v, want one row for the flow with 2 sessions", stats)
	}

	assertRequestTimeline(t, env, base, created.ID)
}

// assertRequestTimeline checks that request id's timeline is its audit trail,
// oldest first, and that an unknown request has none.
func assertRequestTimeline(t *testing.T, env *testEnv, base, id string) {
	t.Helper()
	timeline := decodeJSON[struct {
		Events []struct {
			Action   string `json:"action"`
			TargetID string `json:"targetId"`
		} `json:"events"`
	}](t, env.do(http.MethodGet, base+"/requests/"+id+"/events", nil))
	if len(timeline.Events) != 2 || timeline.Events[0].Action != audit.IdentityProofingRequested ||
		timeline.Events[1].Action != audit.IdentityProofingSessionCreated || timeline.Events[0].TargetID != id {
		t.Errorf("timeline = %+v; want requested, then session_created", timeline.Events)
	}
	resp := env.do(http.MethodGet, base+"/requests/00000000-0000-0000-0000-000000000000/events", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown request's timeline = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// A plain member sends an external person only a flow the admin made available
// to members (the org's default flow too, when it is one of them), and sees and
// counts only the requests they sent; the admin sees all of them.
func TestMemberExternalRequest(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	admin := env.login("admin@acme.test")
	env.addMembership(admin.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	shared := env.proofingFlow("acme", "Passport only")
	restricted := env.proofingFlow("acme", "Passport restricted")
	resp := env.putJSON(base+"/flow-selection", map[string]any{"flowIds": []string{shared.ID}, "defaultFlowId": restricted.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("flow selection = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// The admin is not held to the members' flows: the default one is theirs.
	resp = env.postJSON(base+"/requests", map[string]any{"email": "anna@example.org"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin request on the default flow = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	member := env.loginAs("member@acme.test")
	env.addMembership(member, orgID, organization.RoleMember)

	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(flows) != 1 || flows[0].ID != shared.ID || !flows[0].Allowed || flows[0].Default {
		t.Errorf("member flows = %+v, want only the shared flow, not the default", flows)
	}
	resp = env.postJSON(base+"/requests", map[string]any{"email": "bram@example.org", "flowId": restricted.ID})
	wantProofingError(t, "member request to an external person on a flow members may not use", resp,
		http.StatusUnprocessableEntity, "flow_not_allowed")
	resp = env.postJSON(base+"/requests", map[string]any{"email": "bram@example.org"})
	wantProofingError(t, "member request on a default flow members may not use", resp,
		http.StatusUnprocessableEntity, "flow_not_allowed")
	resp = env.postJSON(base+"/requests", map[string]any{"userId": member, "flowId": restricted.ID})
	wantProofingError(t, "member request to a member on a flow members may not use", resp,
		http.StatusUnprocessableEntity, "flow_not_allowed")

	resp = env.postJSON(base+"/requests", map[string]any{"email": "bram@example.org", "flowId": shared.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("member request to an external person = %d, want 201", resp.StatusCode)
	}
	if sent := decodeJSON[proofingRequestResp](t, resp); !sent.External || sent.SubjectUserID != "" || sent.FlowID != shared.ID {
		t.Errorf("sent = %+v, want an external subject on the shared flow", sent)
	}
	resp = env.postJSON(base+"/requests", map[string]any{"userId": member, "flowId": shared.ID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("member request to a member = %d, want 201", resp.StatusCode)
	}
	if sent := decodeJSON[proofingRequestResp](t, resp); sent.External || sent.SubjectUserID != member.String() {
		t.Errorf("sent = %+v, want the member as the subject", sent)
	}

	// The member's own two requests; the stats count external ones only.
	assertMemberRequestLists(t, env, base, shared, restricted)

	env.loginAs("admin@acme.test")
	assertAdminRequestLists(t, env, base, shared, restricted)
}

// assertMemberRequestLists checks that a member lists and counts only the two
// requests they sent on the shared flow, one of them external, and none on the
// restricted one.
func assertMemberRequestLists(t *testing.T, env *testEnv, base string, shared, restricted proofingFlowResp) {
	t.Helper()
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests", nil)); len(list) != 2 {
		t.Errorf("member requests = %+v, want the two they sent", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?subjectKind=external", nil)); len(list) != 1 || !list[0].External {
		t.Errorf("member external requests = %+v, want the one sent to an external person", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?flowId="+restricted.ID, nil)); len(list) != 0 {
		t.Errorf("member requests on the restricted flow = %+v, want none: the admin sent the one there", list)
	}
	stats := decodeJSON[proofingStatsResp](t, env.do(http.MethodGet, base+"/stats", nil))
	if len(stats.Rows) != 1 || stats.Rows[0].FlowID != shared.ID || stats.Rows[0].Sessions != 1 {
		t.Errorf("member stats = %+v, want one session on the shared flow", stats)
	}
}

// assertAdminRequestLists checks that an admin lists and counts all three
// requests: the member's two on the shared flow and their own on the
// restricted one.
func assertAdminRequestLists(t *testing.T, env *testEnv, base string, shared, restricted proofingFlowResp) {
	t.Helper()
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests", nil)); len(list) != 3 {
		t.Errorf("admin requests = %+v, want all three", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?subjectKind=external", nil)); len(list) != 2 {
		t.Errorf("admin external requests = %+v, want the two sent to external people", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?subjectKind=member", nil)); len(list) != 1 || list[0].External {
		t.Errorf("admin member requests = %+v, want the one sent to a member", list)
	}
	wantProofingError(t, "unknown subject kind", env.do(http.MethodGet, base+"/requests?subjectKind=robot", nil),
		http.StatusBadRequest, "invalid_input")
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?flowId="+shared.ID, nil)); len(list) != 2 ||
		list[0].FlowID != shared.ID || list[1].FlowID != shared.ID {
		t.Errorf("admin requests on the shared flow = %+v, want the member's two", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?flowId="+shared.ID+"&subjectKind=external", nil)); len(list) != 1 ||
		!list[0].External || list[0].FlowID != shared.ID {
		t.Errorf("admin external requests on the shared flow = %+v, want the member's external one", list)
	}
	if list := decodeJSON[[]proofingRequestResp](t, env.do(http.MethodGet, base+"/requests?flowId="+restricted.ID, nil)); len(list) != 1 || list[0].FlowID != restricted.ID {
		t.Errorf("admin requests on the restricted flow = %+v, want the admin's own", list)
	}
	stats := decodeJSON[proofingStatsResp](t, env.do(http.MethodGet, base+"/stats", nil))
	if len(stats.Rows) != 2 || stats.Rows[0].Sessions != 1 || stats.Rows[1].Sessions != 1 {
		t.Errorf("admin stats = %+v, want one session on each flow", stats)
	}
}

// fixedRetentionDays is an org's data retention, a fixed default a flow
// overrides (proofing.defaultDataRetentionDays).
const fixedRetentionDays = 7

type proofingSettingsResp struct {
	SessionTTLSeconds int    `json:"sessionTtlSeconds"`
	DataRetentionDays int    `json:"dataRetentionDays"`
	ErasureHandling   string `json:"erasureHandling"`
	Branding          struct {
		DisplayName    string `json:"displayName"`
		PrimaryColor   string `json:"primaryColor"`
		SupportContact string `json:"supportContact"`
		PrivacyURL     string `json:"privacyUrl"`
		LogoURI        string `json:"logoUri"`
		HidePoweredBy  bool   `json:"hidePoweredBy"`
	} `json:"branding"`
	LegalRetention *struct {
		Months   int    `json:"months"`
		ReasonEN string `json:"reasonEn"`
		ReasonNL string `json:"reasonNl"`
	} `json:"legalRetention"`
	AllowedRedirectOrigins []string `json:"allowedRedirectOrigins"`
}

// patchSettings sends an admin's partial update of the org's proofing settings.
func (e *testEnv) patchSettings(slug, body string) *http.Response {
	e.t.Helper()
	return e.do(http.MethodPatch, "/api/v1/orgs/"+slug+"/identity-proofing/settings", strings.NewReader(body))
}

// putBranding sends the admin's branding form: its text fields and, when logo
// is set, the logo file.
func (e *testEnv) putBranding(slug string, fields map[string]string, logo []byte) *http.Response {
	e.t.Helper()
	return e.putBrandingForm("/api/v1/orgs/"+slug+"/identity-proofing/settings/branding", fields, logo)
}

// putBrandingForm sends a branding form to path, the org's or a flow's.
func (e *testEnv) putBrandingForm(path string, fields map[string]string, logo []byte) *http.Response {
	e.t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			e.t.Fatalf("write form field %q: %v", name, err)
		}
	}

	if logo != nil {
		part, err := form.CreateFormFile("logo", "logo.png")
		if err != nil {
			e.t.Fatalf("create logo part: %v", err)
		}
		if _, err := part.Write(logo); err != nil {
			e.t.Fatalf("write logo part: %v", err)
		}
	}

	if err := form.Close(); err != nil {
		e.t.Fatalf("close form: %v", err)
	}

	req, err := http.NewRequest(http.MethodPut, e.server.URL+path, &body)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set(headerContentType, form.FormDataContentType())

	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("put branding: %v", err)
	}
	return resp
}

// testLogoPNG is a small valid PNG for a logo upload.
func testLogoPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatalf("encode logo: %v", err)
	}
	return buf.Bytes()
}

// A settings update that fails validation changes nothing, whichever of its
// fields is the bad one.
func TestBadPatchChangesNothing(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	wantProofingError(t, "valid erasure handling with a bad origin",
		env.patchSettings("acme", `{"erasureHandling":"automatic","allowedRedirectOrigins":["https://portal.example.org/a"]}`),
		http.StatusBadRequest, "invalid_input")
	wantProofingError(t, "bad erasure handling with a valid origin",
		env.patchSettings("acme", `{"erasureHandling":"sometimes","allowedRedirectOrigins":["https://portal.example.org"]}`),
		http.StatusBadRequest, "invalid_input")

	settings := decodeJSON[proofingSettingsResp](t, env.do(http.MethodGet, base+"/settings", nil))
	if settings.ErasureHandling != "review" || len(settings.AllowedRedirectOrigins) != 0 {
		t.Errorf("settings after rejected updates = %+v; want the defaults", settings)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingSettingsUpdated); n != 0 {
		t.Errorf("settings_updated audits = %d, want 0", n)
	}
}

// The org sets its branding and redirect origins once, for all its flows. Its
// session lifetime and data retention are fixed defaults a flow overrides: a
// change to either is refused.
func TestOrgSettingsHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	settings := decodeJSON[proofingSettingsResp](t, env.do(http.MethodGet, base+"/settings", nil))
	if settings.SessionTTLSeconds != int(proofing.SessionTTL.Seconds()) || settings.DataRetentionDays != fixedRetentionDays ||
		settings.Branding.DisplayName != "" || settings.Branding.LogoURI != "" || settings.AllowedRedirectOrigins == nil ||
		len(settings.AllowedRedirectOrigins) != 0 {
		t.Errorf("default settings = %+v; want the defaults, no branding and an empty (not null) origin list", settings)
	}

	wantProofingError(t, "empty update", env.patchSettings("acme", `{}`), http.StatusBadRequest, "invalid_body")
	for name, body := range map[string]string{
		"a session lifetime":          `{"sessionTtlSeconds":300}`,
		"the session lifetime it has": `{"sessionTtlSeconds":600}`,
		"a data retention":            `{"dataRetentionDays":90}`,
		"the data retention it has":   `{"dataRetentionDays":7}`,
	} {
		wantProofingError(t, name, env.patchSettings("acme", body), http.StatusBadRequest, "invalid_input")
	}
	wantProofingError(t, "redirect origin with a path", env.patchSettings("acme", `{"allowedRedirectOrigins":["https://portal.example.org/a"]}`),
		http.StatusBadRequest, "invalid_input")

	const origin = "https://portal.example.org"
	update := fmt.Sprintf(`{"allowedRedirectOrigins":[%q]}`, origin)
	resp := env.patchSettings("acme", update)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings = %d, want 200", resp.StatusCode)
	}
	settings = decodeJSON[proofingSettingsResp](t, resp)
	if settings.SessionTTLSeconds != int(proofing.SessionTTL.Seconds()) || settings.DataRetentionDays != fixedRetentionDays ||
		len(settings.AllowedRedirectOrigins) != 1 || settings.AllowedRedirectOrigins[0] != origin {
		t.Errorf("updated settings = %+v, want the fixed lifetime and retention, and the new origin", settings)
	}
	// Saving what is already saved changes and audits nothing.
	_ = env.patchSettings("acme", update).Body.Close()
	if n := env.auditCount(orgID, audit.IdentityProofingSettingsUpdated); n != 1 {
		t.Errorf("settings_updated audits = %d, want 1 (origins)", n)
	}

	// Every flow tells the external person how long their data is kept: the
	// org's fixed retention on a flow without its own, and the day the engine
	// keeps a session beyond it.
	env.proofingFlow("acme", "Passport only")
	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(flows) != 1 || flows[0].RetentionDays != fixedRetentionDays+1 {
		t.Errorf("flows = %+v, want a retention of %d days", flows, fixedRetentionDays+1)
	}

	assertOrgBranding(t, env, orgID, base)
}

// assertOrgBranding saves the org's branding with a logo, checks it round trips
// beside the other settings saved before, refuses bad fields, and keeps or
// removes the logo as asked.
func assertOrgBranding(t *testing.T, env *testEnv, orgID uuid.UUID, base string) {
	t.Helper()
	logo := testLogoPNG(t)
	form := map[string]string{
		"displayName": "Acme Identity", "primaryColor": "#1f5b4a", "supportContact": "help@acme.test",
		"privacyUrl": "https://acme.test/privacy", "hidePoweredBy": "true",
	}
	resp := env.putBranding("acme", form, logo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save branding = %d, want 200", resp.StatusCode)
	}
	settings := decodeJSON[proofingSettingsResp](t, resp)
	b := settings.Branding
	if b.DisplayName != "Acme Identity" || b.PrimaryColor != "#1f5b4a" || b.SupportContact != "help@acme.test" ||
		b.PrivacyURL != "https://acme.test/privacy" || !b.HidePoweredBy || !strings.HasPrefix(b.LogoURI, base+"/settings/logo?v=") {
		t.Errorf("branding = %+v; want the saved fields and a logo link", b)
	}
	if len(settings.AllowedRedirectOrigins) != 1 || settings.DataRetentionDays != fixedRetentionDays {
		t.Errorf("settings after branding = %+v, want the other settings kept", settings)
	}
	resp = env.do(http.MethodGet, base+"/settings/logo", nil)
	served, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get(headerContentType) != contentTypePNG || !bytes.Equal(served, logo) {
		t.Errorf("logo = %d %q (%d bytes, err %v), want the uploaded PNG", resp.StatusCode, resp.Header.Get(headerContentType), len(served), err)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingSettingsUpdated); n != 2 {
		t.Errorf("settings_updated audits = %d, want 2 after the branding", n)
	}

	bad := map[string]string{"displayName": "Acme", "primaryColor": "green"}
	wantProofingError(t, "branding with a colour that is no hex", env.putBranding("acme", bad, nil), http.StatusBadRequest, "invalid_input")
	bad = map[string]string{"displayName": "Acme", "privacyUrl": "http://acme.test/privacy"}
	wantProofingError(t, "branding with a privacy URL that is no https", env.putBranding("acme", bad, nil), http.StatusBadRequest, "invalid_input")

	// A form without a logo keeps it; removeLogo clears it.
	if kept := decodeJSON[proofingSettingsResp](t, env.putBranding("acme", form, nil)); kept.Branding.LogoURI == "" {
		t.Errorf("branding saved without a logo field = %+v, want the logo kept", kept.Branding)
	}
	form["removeLogo"] = "true"
	if removed := decodeJSON[proofingSettingsResp](t, env.putBranding("acme", form, nil)); removed.Branding.LogoURI != "" {
		t.Errorf("branding saved with removeLogo = %+v, want no logo", removed.Branding)
	}
	wantProofingError(t, "removed logo", env.do(http.MethodGet, base+"/settings/logo", nil), http.StatusNotFound, "not_found")
}

// The org's legal retention is a floor for every flow's retention: it is told
// to the external person on each flow, replaces nothing else, and is removed
// by null. A bad one changes nothing.
func TestLegalRetentionHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"
	env.proofingFlow("acme", "Passport only")

	settings := decodeJSON[proofingSettingsResp](t, env.do(http.MethodGet, base+"/settings", nil))
	if settings.LegalRetention != nil {
		t.Errorf("default legal retention = %+v; want null", settings.LegalRetention)
	}

	for name, body := range map[string]string{
		"no months":        `{"legalRetention":{"months":0,"reasonEn":"a","reasonNl":"b"}}`,
		"too many months":  `{"legalRetention":{"months":601,"reasonEn":"a","reasonNl":"b"}}`,
		"no English":       `{"legalRetention":{"months":60,"reasonEn":"","reasonNl":"b"}}`,
		"no Dutch":         `{"legalRetention":{"months":60,"reasonEn":"a"}}`,
		"an empty object":  `{"legalRetention":{}}`,
		"valid beside bad": `{"erasureHandling":"automatic","legalRetention":{"months":60,"reasonEn":"a"}}`,
	} {
		wantProofingError(t, name, env.patchSettings("acme", body), http.StatusBadRequest, "invalid_input")
	}
	for name, body := range map[string]string{
		"an unknown field": `{"legalRetention":{"months":60,"reasonEn":"a","reasonNl":"b","years":5}}`,
		"a string":         `{"legalRetention":"five years"}`,
	} {
		wantProofingError(t, name, env.patchSettings("acme", body), http.StatusBadRequest, "invalid_body")
	}
	settings = decodeJSON[proofingSettingsResp](t, env.do(http.MethodGet, base+"/settings", nil))
	if settings.LegalRetention != nil || settings.ErasureHandling != "review" {
		t.Errorf("settings after rejected updates = %+v; want the defaults", settings)
	}
	if n := env.auditCount(orgID, audit.IdentityProofingSettingsUpdated); n != 0 {
		t.Errorf("settings_updated audits = %d, want 0", n)
	}

	const reasonEN, reasonNL = "The Wwft requires us to keep proof of identity for five years.", "De Wwft verplicht ons het identiteitsbewijs vijf jaar te bewaren."
	update := fmt.Sprintf(`{"legalRetention":{"months":60,"reasonEn":%q,"reasonNl":%q}}`, reasonEN, reasonNL)
	resp := env.patchSettings("acme", update)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save legal retention = %d, want 200", resp.StatusCode)
	}
	settings = decodeJSON[proofingSettingsResp](t, resp)
	if legal := settings.LegalRetention; legal == nil || legal.Months != 60 || legal.ReasonEN != reasonEN || legal.ReasonNL != reasonNL {
		t.Fatalf("legal retention = %+v; want five years with both reasons", legal)
	}
	if settings.DataRetentionDays != fixedRetentionDays {
		t.Errorf("retention = %d days; want the org's own kept", settings.DataRetentionDays)
	}
	assertLegalRetentionApplied(t, env, orgID, base, update)
}

// assertLegalRetentionApplied checks that the legal retention saved by update,
// five years, is told on the org's flow, is kept through a change of another
// setting and a save of the same, and is removed by null.
func assertLegalRetentionApplied(t *testing.T, env *testEnv, orgID uuid.UUID, base, update string) {
	t.Helper()
	// Five years are 1826 or 1827 days, and the engine's grace adds one.
	flows := decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(flows) != 1 || flows[0].RetentionDays < 1827 || flows[0].RetentionDays > 1828 {
		t.Errorf("flows = %+v; want a retention of about five years", flows)
	}

	// A change of another setting leaves it; saving it again changes nothing.
	kept := decodeJSON[proofingSettingsResp](t, env.patchSettings("acme", `{"erasureHandling":"automatic"}`))
	if kept.LegalRetention == nil || kept.LegalRetention.Months != 60 {
		t.Errorf("legal retention after another change = %+v; want it kept", kept.LegalRetention)
	}
	_ = env.patchSettings("acme", update).Body.Close()
	if n := env.auditCount(orgID, audit.IdentityProofingSettingsUpdated); n != 2 {
		t.Errorf("settings_updated audits = %d, want 2 (legal retention, erasure handling)", n)
	}

	removed := decodeJSON[proofingSettingsResp](t, env.patchSettings("acme", `{"legalRetention":null}`))
	if removed.LegalRetention != nil {
		t.Errorf("legal retention after null = %+v; want none", removed.LegalRetention)
	}
	flows = decodeJSON[[]proofingFlowResp](t, env.do(http.MethodGet, base+"/flows", nil))
	if len(flows) != 1 || flows[0].RetentionDays != fixedRetentionDays+1 {
		t.Errorf("flows = %+v; want the org's retention again", flows)
	}
}

type proofingAPIKeyResp struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Prefix        string `json:"prefix"`
	Secret        string `json:"secret"`
	CreatedByName string `json:"createdByName"`
	RevokedAt     string `json:"revokedAt"`
}

type proofingAPISessionResp struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	FlowID       string `json:"flowId"`
	SubjectEmail string `json:"subjectEmail"`
	DeepLink     string `json:"deepLink"`
	MailSent     bool   `json:"mailSent"`
	ErrorCode    string `json:"errorCode"`
	PurgedAt     string `json:"purgedAt"`
}

// apiCall calls the public proofing API with an org's key and no cookie.
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

// The org's own purge through the API takes no approved check its legal
// retention still holds, as no "delete my data" request does: 409
// legal_retention. A check the law does not hold (a rejected one) is purged.
func TestLegalRetentionBlocksAPIPurge(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	base := "/api/v1/orgs/acme/identity-proofing"

	resp := env.postJSON(base+"/api-keys", map[string]any{"name": "Backend"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key = %d, want 201", resp.StatusCode)
	}
	key := decodeJSON[proofingAPIKeyResp](t, resp)
	send := func(email, status string) proofingAPISessionResp {
		t.Helper()
		resp := env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
			map[string]any{"email": email, "sendMail": false})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("api create session = %d, want 201", resp.StatusCode)
		}
		session := decodeJSON[proofingAPISessionResp](t, resp)
		// The engine's outcome, as reconcile records it.
		if _, err := env.pool.Exec(context.Background(), `UPDATE identity_proofing_requests
			SET status = $2, completed_at = now() WHERE subject_email = $1`, email, status); err != nil {
			t.Fatalf("decide the check: %v", err)
		}
		return session
	}
	approved, rejected := send("anna@example.org", "approved"), send("bob@example.org", "rejected")
	_ = env.patchSettings("acme", `{"legalRetention":{"months":60,"reasonEn":"The Wwft.","reasonNl":"De Wwft."}}`).Body.Close()

	wantProofingError(t, "purge of a held check",
		env.apiCall(http.MethodDelete, "/api/v1/proofing/sessions/"+approved.ID, key.Secret, nil),
		http.StatusConflict, "legal_retention")
	got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+approved.ID, key.Secret, nil))
	if got.PurgedAt != "" {
		t.Errorf("held check after the refused purge = %+v; want it kept", got)
	}

	resp = env.apiCall(http.MethodDelete, "/api/v1/proofing/sessions/"+rejected.ID, key.Secret, nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("purge of a rejected check = %d, want 204", resp.StatusCode)
	}
}

func TestProofingAPIKeyFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flow := env.proofingDefaultFlow("acme")
	base := "/api/v1/orgs/acme/identity-proofing"

	resp := env.postJSON(base+"/api-keys", map[string]any{"name": "Production backend"})
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
	if len(listed) == 1 && listed[0].CreatedByName == "" {
		t.Errorf("listed key = %+v; want it to name who created it", listed[0])
	}

	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", "yp_live_nope", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown key = %d, want 401", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// No flow named: the org's default. No mail: the caller shows the link.
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

	assertAPIKeyAuditActor(t, env, key)
	assertAPIIdempotentSessions(t, env, key, session)
	assertAPICancelAndErase(t, env, orgID, key)
	assertAPIKeyRevocation(t, env, orgID, base, key)
}

// assertAPIKeyAuditActor checks that the latest request was audited with key
// as its actor, not a member.
func assertAPIKeyAuditActor(t *testing.T, env *testEnv, key proofingAPIKeyResp) {
	t.Helper()
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
}

// assertAPIIdempotentSessions checks that a retry with the same
// Idempotency-Key replays the first answer, kept sealed, and that the key with
// another body is refused; then that key's sessions page from the newest,
// ending with session.
func assertAPIIdempotentSessions(t *testing.T, env *testEnv, key proofingAPIKeyResp, session proofingAPISessionResp) {
	t.Helper()
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
	resp := idem("dave@example.org")
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
}

// assertAPICancelAndErase checks that the org's software cancels a second
// session with key, then erases it, each once.
func assertAPICancelAndErase(t *testing.T, env *testEnv, orgID uuid.UUID, key proofingAPIKeyResp) {
	t.Helper()
	second := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "bob@example.org", "sendMail": false}))
	cancelURL := "/api/v1/proofing/sessions/" + second.ID + "/cancel"
	if got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, cancelURL, key.Secret, nil)); got.Status != "cancelled" {
		t.Errorf("cancel = %+v, want cancelled", got)
	}
	resp := env.apiCall(http.MethodPost, cancelURL, key.Secret, nil)
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
}

// assertAPIKeyRevocation checks that the org's list names key as the sender of
// its three sessions, and that revoking key stops it, audited and still
// listed.
func assertAPIKeyRevocation(t *testing.T, env *testEnv, orgID uuid.UUID, base string, key proofingAPIKeyResp) {
	t.Helper()
	list := decodeJSON[[]struct {
		ID         string `json:"id"`
		APIKeyName string `json:"apiKeyName"`
	}](t, env.do(http.MethodGet, base+"/requests", nil))
	if len(list) != 3 || list[0].APIKeyName != "Production backend" {
		t.Errorf("requests = %+v, want the three sent by the key", list)
	}

	resp := env.do(http.MethodDelete, base+"/api-keys/"+key.ID, nil)
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
	listed := decodeJSON[[]proofingAPIKeyResp](t, env.do(http.MethodGet, base+"/api-keys", nil))
	if len(listed) != 1 || listed[0].RevokedAt == "" {
		t.Errorf("listed keys after revoking = %+v; want the revoked key still listed", listed)
	}
	wantProofingError(t, "revoking an unknown key", env.do(http.MethodDelete, base+"/api-keys/00000000-0000-0000-0000-000000000000", nil),
		http.StatusNotFound, "api_key_not_found")
}

// A key reads an approved session's identity only with results:read, and
// creates one only with sessions:write.
func TestProofingAPIKeyScopesFlow(t *testing.T) {
	env := setup(t)
	env.proofing.Outcome = proofingprovider.StatusApproved
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "CI")
	if !strings.HasPrefix(key.Secret, "yp_live_") {
		t.Fatalf("key = %+v; want a yp_live_ secret", key)
	}
	approved := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	narrowed := env.proofingKey("acme", "Status")
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE identity_proofing_api_keys SET scopes = ARRAY['sessions:read'] WHERE id = $1`, narrowed.ID); err != nil {
		t.Fatalf("narrow key: %v", err)
	}
	resp := env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+approved.ID+"/result", narrowed.Secret, nil)
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

// hostedBranding is the org's branding as a hosted page carries it.
type hostedBranding struct {
	DisplayName    string `json:"displayName"`
	PrimaryColor   string `json:"primaryColor"`
	SupportContact string `json:"supportContact"`
	LogoURI        string `json:"logoUri"`
}

// hostedView is the public hosted page of a session, as its subject reads it.
type hostedView struct {
	Status       string `json:"status"`
	Started      bool   `json:"started"`
	DeepLink     string `json:"deepLink"`
	Organization struct {
		Name     string         `json:"name"`
		Branding hostedBranding `json:"branding"`
	} `json:"organization"`
	// The flow's name and level stay out of the person's page.
	Flow map[string]json.RawMessage `json:"flow"`
}

// A hosted session is a link: the subject opens the public page without an
// account, starts once for the app they pick, and the page follows the outcome.
func TestProofingHostedLinkFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")

	resp := env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true})
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

	view := decodeJSON[hostedView](t, env.apiCall(http.MethodGet, "/api/v1/proof/"+token, "", nil))
	if view.Status != "pending" || view.Started || view.Organization.Name != "Acme" || view.Flow["requestedAttributes"] == nil {
		t.Errorf("hosted page = %+v; want a pending, unstarted Acme page for the flow", view)
	}
	for _, internal := range []string{"name", "requiredAssuranceLevel"} {
		if _, ok := view.Flow[internal]; ok {
			t.Errorf("hosted page flow carries %q; want it left out", internal)
		}
	}
	resp = env.apiCall(http.MethodGet, "/api/v1/proof/not-a-token", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown link = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()

	assertHostedStartsOnce(t, env, token)
	assertHostedPageBranded(t, env, token)
	assertWalletHostedLink(t, env, flow.ID)
}

// assertHostedStartsOnce checks that the hosted page of token starts once, for
// the app picked, and that its status then reads started.
func assertHostedStartsOnce(t *testing.T, env *testEnv, token string) {
	t.Helper()
	resp := env.apiCall(http.MethodPost, "/api/v1/proof/"+token+"/start", "", map[string]any{"method": "idem_app"})
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
}

// assertHostedPageBranded checks that the hosted page of token carries the
// org's branding: its display name replaces the org's name.
func assertHostedPageBranded(t *testing.T, env *testEnv, token string) {
	t.Helper()
	resp := env.putBranding("acme", map[string]string{"displayName": "Acme Identity", "primaryColor": "#1f5b4a"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save branding = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	view := decodeJSON[hostedView](t, env.apiCall(http.MethodGet, "/api/v1/proof/"+token, "", nil))
	if view.Organization.Name != "Acme Identity" || view.Organization.Branding.DisplayName != "Acme Identity" {
		t.Errorf("branded hosted page = %+v; want the org's display name", view)
	}
}

// assertWalletHostedLink checks that an admin makes the same link on flowID in
// the wallet, to hand the subject.
func assertWalletHostedLink(t *testing.T, env *testEnv, flowID string) {
	t.Helper()
	resp := env.postJSON("/api/v1/orgs/acme/identity-proofing/requests", map[string]any{
		"flowId": flowID, "channel": "hosted", "name": "Dibran Mulder",
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

// An org with its own UI picks the app of a hosted session over the API and
// polls it from stored state; a session not created hosted has no such start.
func TestProofingHeadlessHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")

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
	resp := env.apiCall(http.MethodPost, start, key.Secret, nil)
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

	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "CI")
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	if read := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+session.ID,
		key.Secret, nil)); read.Status != "needs_review" {
		t.Fatalf("session = %+v, want needs_review", read)
	}

	review := "/api/v1/orgs/acme/identity-proofing/requests/" + session.ID + "/review"
	resp := env.postJSON(review, map[string]any{"decision": "approve"})
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

// The public API holds each org to proofing.APISessionLimit sessions, however
// many keys it has, answering 429 with Retry-After past it; reads have their own,
// larger budget. Another org has its own.
func TestProofingAPIRateLimit(t *testing.T) {
	env := setup(t)
	acmeID := env.createOrg("Acme", "acme")
	globexID := env.createOrg("Globex", "globex")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, acmeID, organization.RoleAdmin)
	env.addMembership(me.ID, globexID, organization.RoleAdmin)
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Backend")
	sibling := env.proofingKey("acme", "Sibling")
	env.proofingDefaultFlow("globex")
	foreign := env.proofingKey("globex", "Backend")

	create := func(key proofingAPIKeyResp) *http.Response {
		return env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
			map[string]any{"email": "anna@example.org", "sendMail": false})
	}
	for i := range proofing.APISessionLimit.Burst {
		resp := create(key)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create session %d = %d, want 201 within the limit", i+1, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
	resp := create(key)
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Errorf("create past the limit = %d, Retry-After %q; want 429 with Retry-After",
			resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if body := decodeJSON[struct {
		Code string `json:"code"`
	}](t, resp); body.Code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", body.Code)
	}

	// The budget is the org's: another key of it is held to the same limit.
	wantProofingError(t, "create past the limit with another key of the org", create(sibling),
		http.StatusTooManyRequests, "rate_limited")
	resp = create(foreign)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("create in another org = %d, want 201: its budget is its own", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read after the session limit = %d, want 200: reads have their own budget", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// An org's API keys are the org's: each works on every flow of the org and sees
// every session of the org, the ones keys created and the ones sent from the
// app, and none reaches another org's.
func TestProofingAPIKeysShareOrg(t *testing.T) {
	env := setup(t)
	acmeID := env.createOrg("Acme", "acme")
	globexID := env.createOrg("Globex", "globex")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, acmeID, organization.RoleAdmin)
	env.addMembership(me.ID, globexID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	byDefault := env.proofingDefaultFlow("acme")
	other := env.proofingFlow("acme", "Passport restricted")
	first := env.proofingKey("acme", "First")
	second := env.proofingKey("acme", "Second")
	env.proofingDefaultFlow("globex")
	foreign := env.proofingKey("globex", "Backend")

	// A key lists every flow a recipient can finish, the org's default marked.
	flows := decodeJSON[[]struct {
		ID      string `json:"id"`
		Default bool   `json:"default"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/flows", first.Secret, nil))
	isDefault := map[string]bool{}
	for _, f := range flows {
		isDefault[f.ID] = f.Default
	}
	if len(flows) != 2 || !isDefault[byDefault.ID] || isDefault[other.ID] {
		t.Errorf("api flows = %+v, want both flows with the default marked", flows)
	}

	// A key sends any flow of the org, not only the default one.
	onOther := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", first.Secret,
		map[string]any{"email": "anna@example.org", "flowId": other.ID, "sendMail": false}))
	if onOther.FlowID != other.ID {
		t.Errorf("session on a named flow = %+v, want the flow %s", onOther, other.ID)
	}
	onDefault := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", second.Secret,
		map[string]any{"email": "bram@example.org", "sendMail": false}))
	if onDefault.FlowID != byDefault.ID {
		t.Errorf("session on no named flow = %+v, want the default flow %s", onDefault, byDefault.ID)
	}
	resp := env.postJSON(base+"/requests", map[string]any{"email": "cleo@example.org"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("request sent from the app = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	sessions := func(key proofingAPIKeyResp) []proofingAPISessionResp {
		t.Helper()
		page := decodeJSON[struct {
			Sessions []proofingAPISessionResp `json:"sessions"`
		}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions", key.Secret, nil))
		return page.Sessions
	}
	emails := func(sessions []proofingAPISessionResp) map[string]bool {
		out := map[string]bool{}
		for _, s := range sessions {
			out[s.SubjectEmail] = true
		}
		return out
	}
	for _, key := range []proofingAPIKeyResp{first, second} {
		seen := sessions(key)
		if got := emails(seen); len(seen) != 3 || !got["anna@example.org"] || !got["bram@example.org"] || !got["cleo@example.org"] {
			t.Errorf("sessions of key %s = %+v, want all three of the org", key.Name, seen)
		}
	}
	if got := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+onOther.ID,
		second.Secret, nil)); got.ID != onOther.ID {
		t.Errorf("second key reading the first key's session = %+v, want %s", got, onOther.ID)
	}

	// The sessions are the org's: revoking the key that made one leaves them.
	resp = env.do(http.MethodDelete, base+"/api-keys/"+first.ID, nil)
	_ = resp.Body.Close()
	if seen := sessions(second); len(seen) != 3 {
		t.Errorf("sessions after revoking the first key = %+v, want all three", seen)
	}

	// Another org's key reaches none of them.
	wantProofingError(t, "foreign key reads a session", env.apiCall(http.MethodGet, "/api/v1/proofing/sessions/"+onOther.ID,
		foreign.Secret, nil), http.StatusNotFound, "session_not_found")
	if seen := sessions(foreign); len(seen) != 0 {
		t.Errorf("foreign key's sessions = %+v, want none", seen)
	}
}

type proofingWebhookResp struct {
	Configured  bool     `json:"configured"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	SecretLast4 string   `json:"secretLast4"`
	Secret      string   `json:"secret"`
	Health      struct {
		State string `json:"state"`
	} `json:"health"`
	AvailableEvents []string `json:"availableEvents"`
}

type proofingDeliveryResp struct {
	Event       string `json:"event"`
	SessionID   string `json:"sessionId"`
	EndpointURL string `json:"endpointUrl"`
	Status      string `json:"status"`
}

// The org has one webhook endpoint: set, kept apart from its secret, queued
// the events it subscribed to, rotated and removed by an admin.
func TestProofingWebhookHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"
	const endpoint = "https://hooks.example.org/yivi"

	hook := decodeJSON[proofingWebhookResp](t, env.do(http.MethodGet, base+"/webhook", nil))
	if hook.Configured || hook.Events == nil || len(hook.Events) != 0 || len(hook.AvailableEvents) == 0 ||
		hook.Health.State != proofing.WebhookNotConfigured {
		t.Errorf("webhook of an org without one = %+v; want not configured, no events, the available ones", hook)
	}
	wantProofingError(t, "test event without an endpoint", env.postJSON(base+"/webhook/test", map[string]any{}),
		http.StatusNotFound, "webhook_not_found")
	wantProofingError(t, "removing an endpoint that is not there", env.do(http.MethodDelete, base+"/webhook", nil),
		http.StatusNotFound, "webhook_not_found")

	for name, body := range map[string]map[string]any{
		"an endpoint that is not https": {"url": "http://hooks.example.org/yivi", "events": []string{"session.created"}},
		"no events":                     {"url": endpoint, "events": []string{}},
		"an unknown event":              {"url": endpoint, "events": []string{"session.nope"}},
	} {
		wantProofingError(t, name, env.putJSON(base+"/webhook", body), http.StatusBadRequest, "invalid_input")
	}

	resp := env.putJSON(base+"/webhook", map[string]any{"url": endpoint, "events": []string{"session.verified", "session.created"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set webhook = %d, want 200", resp.StatusCode)
	}
	set := decodeJSON[proofingWebhookResp](t, resp)
	if !set.Configured || set.URL != endpoint || len(set.Events) != 2 || set.Events[0] != "session.created" ||
		!strings.HasPrefix(set.Secret, "whsec_") || !strings.HasSuffix(set.Secret, set.SecretLast4) {
		t.Errorf("new webhook = %+v; want the endpoint, its events in display order and a one-time secret", set)
	}
	hook = decodeJSON[proofingWebhookResp](t, env.do(http.MethodGet, base+"/webhook", nil))
	if hook.Secret != "" || hook.SecretLast4 != set.SecretLast4 || hook.Health.State != proofing.WebhookDelivering {
		t.Errorf("webhook read back = %+v; want the secret hidden, only its last four kept", hook)
	}

	// Saving again changes the events and keeps the secret.
	resp = env.putJSON(base+"/webhook", map[string]any{"url": endpoint, "events": []string{"session.created"}})
	if again := decodeJSON[proofingWebhookResp](t, resp); again.Secret != "" || again.SecretLast4 != set.SecretLast4 || len(again.Events) != 1 {
		t.Errorf("webhook saved again = %+v; want one event and the same secret, not shown", again)
	}

	assertWebhookQueuesEvents(t, env, base, endpoint)
	assertWebhookRotatedAndRemoved(t, env, orgID, base, set.Secret)
}

// assertWebhookQueuesEvents checks that a session the org's software creates
// queues its event for the endpoint, and that a test event goes first, with no
// session.
func assertWebhookQueuesEvents(t *testing.T, env *testEnv, base, endpoint string) {
	t.Helper()
	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Backend")
	session := decodeJSON[proofingAPISessionResp](t, env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
		map[string]any{"email": "anna@example.org", "sendMail": false}))
	deliveries := decodeJSON[[]proofingDeliveryResp](t, env.do(http.MethodGet, base+"/webhook/deliveries", nil))
	if len(deliveries) != 1 || deliveries[0].Event != "session.created" || deliveries[0].SessionID != session.ID ||
		deliveries[0].EndpointURL != endpoint || deliveries[0].Status != proofing.DeliveryPending {
		t.Errorf("deliveries = %+v; want the session.created of %s queued for the endpoint", deliveries, session.ID)
	}
	resp := env.postJSON(base+"/webhook/test", map[string]any{})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("test event = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	deliveries = decodeJSON[[]proofingDeliveryResp](t, env.do(http.MethodGet, base+"/webhook/deliveries", nil))
	if len(deliveries) != 2 || deliveries[0].Event != "test" || deliveries[0].SessionID != "" || deliveries[1].Event != "session.created" {
		t.Errorf("deliveries after the test event = %+v; want it first, with no session", deliveries)
	}
}

// assertWebhookRotatedAndRemoved checks that the endpoint's secret, oldSecret
// until now, is rotated, and that removing the endpoint drops what was still
// queued for it, each change audited.
func assertWebhookRotatedAndRemoved(t *testing.T, env *testEnv, orgID uuid.UUID, base, oldSecret string) {
	t.Helper()
	resp := env.postJSON(base+"/webhook/rotate-secret", map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rotate secret = %d, want 200", resp.StatusCode)
	}
	if rotated := decodeJSON[proofingWebhookResp](t, resp); !strings.HasPrefix(rotated.Secret, "whsec_") ||
		rotated.Secret == oldSecret || !strings.HasSuffix(rotated.Secret, rotated.SecretLast4) {
		t.Errorf("rotated webhook = %+v; want a new secret shown once", rotated)
	}

	// Removing the endpoint drops what was still queued for it.
	resp = env.do(http.MethodDelete, base+"/webhook", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove webhook = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if hook := decodeJSON[proofingWebhookResp](t, env.do(http.MethodGet, base+"/webhook", nil)); hook.Configured {
		t.Errorf("webhook after removing = %+v, want not configured", hook)
	}
	if deliveries := decodeJSON[[]proofingDeliveryResp](t, env.do(http.MethodGet, base+"/webhook/deliveries", nil)); len(deliveries) != 0 {
		t.Errorf("deliveries after removing = %+v, want none", deliveries)
	}
	for action, want := range map[string]int{
		audit.IdentityProofingWebhookConfigured: 2, audit.IdentityProofingWebhookSecretRotated: 1, audit.IdentityProofingWebhookRemoved: 1,
	} {
		if n := env.auditCount(orgID, action); n != want {
			t.Errorf("%s audits = %d, want %d", action, n, want)
		}
	}
}

// Only a platform admin pauses an org's proofing: every org route and the
// org's API keys are then refused until it is lifted, and the org's own admin
// can read the pause but not set or lift it.
func TestPausedByPlatformAdmin(t *testing.T) {
	const root = "root@yivi.test"
	env := setup(t, root)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	base := "/api/v1/orgs/acme/identity-proofing"

	env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Backend")
	create := func() *http.Response {
		return env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret,
			map[string]any{"email": "anna@example.org", "sendMail": false})
	}
	resp := create()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session before the pause = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()

	platformPause := func(state proofing.PauseState) *http.Response {
		return env.putJSON(fmt.Sprintf("/api/v1/admin/organizations/%s/identity-proofing/pause", orgID), map[string]any{"paused": state == proofing.PauseOn})
	}
	resp = platformPause(proofing.PauseOn)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("the org's admin pausing through the platform route = %d, want 403", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.putJSON(base+"/pause", map[string]any{"paused": true})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("the org's admin pausing its own proofing = %d, want it refused: only a platform admin pauses", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if n := env.auditCount(orgID, audit.IdentityProofingPaused); n != 0 {
		t.Errorf("paused audits = %d, want 0 before a platform admin paused", n)
	}

	env.loginAs(root)
	resp = platformPause(proofing.PauseOn)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("platform pause = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	wantProofingError(t, "create session while paused", create(), http.StatusForbidden, "proofing_paused")
	// A pause refuses only what starts proofing: reading stays open.
	resp = env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("read flows with a key while paused = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	env.loginAs("admin@acme.test")
	pause := decodeJSON[struct {
		Paused           bool `json:"paused"`
		PlatformPausedBy *struct {
			Name string `json:"name"`
		} `json:"platformPausedBy"`
	}](t, env.do(http.MethodGet, base+"/pause", nil))
	if !pause.Paused || pause.PlatformPausedBy == nil {
		t.Errorf("pause read by the org's admin = %+v, want paused, with who paused it", pause)
	}
	resp = env.do(http.MethodGet, base+"/flows", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("list flows while paused = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	wantProofingError(t, "org request while paused", env.postJSON(base+"/requests", map[string]any{"email": "anna@example.org"}),
		http.StatusForbidden, "proofing_paused")

	env.loginAs(root)
	resp = platformPause(proofing.PauseOff)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("platform resume = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = create()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("create session after resuming = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()
	for action, want := range map[string]int{audit.IdentityProofingPaused: 1, audit.IdentityProofingResumed: 1} {
		if n := env.auditCount(orgID, action); n != want {
			t.Errorf("%s audits = %d, want %d", action, n, want)
		}
	}
}
