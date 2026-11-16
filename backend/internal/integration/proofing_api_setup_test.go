//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

type apiSetupFlowResp struct {
	ID              string `json:"id"`
	Version         int    `json:"version"`
	Default         bool   `json:"default"`
	HostedEnabled   bool   `json:"hostedEnabled"`
	HostedReturnURL string `json:"hostedReturnUrl"`
	AddressMode     string `json:"addressMode"`
}

// An org's software sets up what an admin sets up in the wallet with its API
// key alone: a flow and its next version, the org's default, the flow's hosted
// page with a return page on an allowed origin, a step, and the webhook.
func TestProofingAPISetupFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	key := env.proofingKey("acme", "Portal")
	const origin = "https://portal.example.org"

	resp := env.apiCall(http.MethodPost, "/api/v1/proofing/flows", key.Secret, map[string]any{
		"name": "Passport only", "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow = %d, want 201", resp.StatusCode)
	}
	flow := decodeJSON[proofingFlowResp](t, resp)
	flowURL := "/api/v1/proofing/flows/" + flow.ID

	resp = env.apiCall(http.MethodPost, flowURL+"/versions", key.Secret, map[string]any{
		"name": "Passport only", "steps": []string{"document_capture", "nfc_read"},
		"requiredChecks": []string{"nfc.passive_auth", "nfc.active_auth"},
	})
	if edited := decodeJSON[proofingFlowResp](t, resp); resp.StatusCode != http.StatusCreated || edited.Version != 2 {
		t.Errorf("edit flow = %d %+v, want 201 and version 2", resp.StatusCode, edited)
	}
	if versions := decodeJSON[[]proofingFlowResp](t, env.apiCall(http.MethodGet, flowURL+"/versions", key.Secret, nil)); len(versions) != 2 {
		t.Errorf("versions = %+v, want 2", versions)
	}

	flows := decodeJSON[[]apiSetupFlowResp](t, env.apiCall(http.MethodPut, "/api/v1/proofing/flow-selection", key.Secret,
		map[string]any{"flowIds": []string{}, "defaultFlowId": flow.ID}))
	if len(flows) != 1 || !flows[0].Default {
		t.Errorf("flows after the selection = %+v, want the flow as default", flows)
	}

	// A return page needs its origin allowed first.
	hosted := map[string]any{"enabled": true, "locales": []string{}, "completion": "redirect", "returnUrl": origin + "/done"}
	wantProofingError(t, "a return page off the allowed origins",
		env.apiCall(http.MethodPut, flowURL+"/hosted", key.Secret, hosted), http.StatusBadRequest, "redirect_not_allowed")
	settings := decodeJSON[struct {
		AllowedRedirectOrigins []string `json:"allowedRedirectOrigins"`
	}](t, env.apiCall(http.MethodPatch, "/api/v1/proofing/settings", key.Secret,
		map[string]any{"allowedRedirectOrigins": []string{origin}}))
	if len(settings.AllowedRedirectOrigins) != 1 || settings.AllowedRedirectOrigins[0] != origin {
		t.Errorf("settings = %+v, want the origin allowed", settings)
	}
	if resp := env.apiCall(http.MethodPut, flowURL+"/hosted", key.Secret, hosted); resp.StatusCode != http.StatusOK {
		t.Errorf("save hosted page = %d, want 200", resp.StatusCode)
	}
	if resp := env.apiCall(http.MethodPut, flowURL+"/address", key.Secret, map[string]any{"addressMode": "required"}); resp.StatusCode != http.StatusOK {
		t.Errorf("save address step = %d, want 200", resp.StatusCode)
	}
	flows = decodeJSON[[]apiSetupFlowResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/flows", key.Secret, nil))
	if len(flows) != 1 || !flows[0].HostedEnabled || flows[0].HostedReturnURL != origin+"/done" || flows[0].AddressMode != "required" {
		t.Errorf("flows = %+v, want the hosted page, its return page and the address step", flows)
	}

	assertAPIWebhookSetup(t, env, key)

	// What the key changed is audited as the key's, as in the wallet.
	if n := env.auditCount(orgID, "identity_proofing.settings_updated"); n != 1 {
		t.Errorf("settings audits = %d, want 1", n)
	}
}

// assertAPIWebhookSetup sets, reads, tests, rotates and removes the org's
// webhook endpoint with key.
func assertAPIWebhookSetup(t *testing.T, env *testEnv, key proofingAPIKeyResp) {
	t.Helper()
	const endpoint = "https://hooks.example.org/yivi"
	webhook := "/api/v1/proofing/webhook"

	wantProofingError(t, "an endpoint that is not https",
		env.apiCall(http.MethodPut, webhook, key.Secret, map[string]any{"url": "http://hooks.example.org/yivi", "events": []string{"session.created"}}),
		http.StatusBadRequest, "invalid_input")
	set := decodeJSON[proofingWebhookResp](t, env.apiCall(http.MethodPut, webhook, key.Secret,
		map[string]any{"url": endpoint, "events": []string{"session.cancelled"}}))
	if !set.Configured || set.URL != endpoint || !strings.HasPrefix(set.Secret, "whsec_") {
		t.Errorf("set webhook = %+v, want the endpoint and its one-time secret", set)
	}
	if read := decodeJSON[proofingWebhookResp](t, env.apiCall(http.MethodGet, webhook, key.Secret, nil)); read.Secret != "" || read.URL != endpoint {
		t.Errorf("read webhook = %+v, want the endpoint without its secret", read)
	}
	if resp := env.apiCall(http.MethodPost, webhook+"/test", key.Secret, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("test event = %d, want 204", resp.StatusCode)
	}
	if deliveries := decodeJSON[[]proofingDeliveryResp](t, env.apiCall(http.MethodGet, webhook+"/deliveries", key.Secret, nil)); len(deliveries) != 1 {
		t.Errorf("deliveries = %+v, want the test event", deliveries)
	}
	rotated := decodeJSON[proofingWebhookResp](t, env.apiCall(http.MethodPost, webhook+"/rotate-secret", key.Secret, nil))
	if rotated.Secret == "" || rotated.Secret == set.Secret {
		t.Errorf("rotated = %+v, want a new secret", rotated)
	}
	if resp := env.apiCall(http.MethodDelete, webhook, key.Secret, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("remove webhook = %d, want 204", resp.StatusCode)
	}
}

// The setup routes answer what went wrong with a code an org's software can
// act on, as the API docs list them.
func TestProofingAPISetupErrors(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")
	flowURL := "/api/v1/proofing/flows/" + flow.ID
	const unknown = "/api/v1/proofing/flows/no-such-flow"
	spec := map[string]any{"name": "Passport only", "steps": []string{"document_capture", "nfc_read"}}

	for _, tc := range []struct {
		what         string
		method, path string
		body         any
		status       int
		code         string
	}{
		{"a flow without steps", http.MethodPost, "/api/v1/proofing/flows", map[string]any{"name": "Empty"}, http.StatusBadRequest, "invalid_input"},
		{"a flow with an unknown field", http.MethodPost, "/api/v1/proofing/flows", map[string]any{"name": "X", "steps": []string{"nfc_read"}, "nope": true}, http.StatusBadRequest, "invalid_body"},
		{"editing an unknown flow", http.MethodPost, unknown + "/versions", spec, http.StatusUnprocessableEntity, "flow_not_found"},
		{"the versions of an unknown flow", http.MethodGet, unknown + "/versions", nil, http.StatusUnprocessableEntity, "flow_not_found"},
		{"activating version 0", http.MethodPost, flowURL + "/versions/0/activate", nil, http.StatusBadRequest, "invalid_input"},
		{"activating a version that does not exist", http.MethodPost, flowURL + "/versions/9/activate", nil, http.StatusUnprocessableEntity, "flow_not_found"},
		{"an unknown default flow", http.MethodPut, "/api/v1/proofing/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": "no-such-flow"}, http.StatusUnprocessableEntity, "flow_not_found"},
		{"the hosted page of an unknown flow", http.MethodGet, unknown + "/hosted", nil, http.StatusUnprocessableEntity, "flow_not_found"},
		{"a hosted page without enabled", http.MethodPut, flowURL + "/hosted", map[string]any{"completion": "done"}, http.StatusBadRequest, "invalid_body"},
		{"an unknown completion", http.MethodPut, flowURL + "/hosted", map[string]any{"enabled": true, "completion": "maybe"}, http.StatusBadRequest, "invalid_input"},
		{"a return page on a done flow", http.MethodPut, flowURL + "/hosted", map[string]any{"enabled": true, "completion": "done", "returnUrl": "https://portal.example.org/done"}, http.StatusBadRequest, "invalid_input"},
		{"a return page off the allowed origins", http.MethodPut, flowURL + "/hosted", map[string]any{"enabled": true, "completion": "redirect", "returnUrl": "https://elsewhere.example.org/"}, http.StatusBadRequest, "redirect_not_allowed"},
		{"an unknown address mode", http.MethodPut, flowURL + "/address", map[string]any{"addressMode": "sometimes"}, http.StatusBadRequest, "invalid_input"},
		{"the address step of an unknown flow", http.MethodGet, unknown + "/address", nil, http.StatusUnprocessableEntity, "flow_not_found"},
		{"a settings change of nothing", http.MethodPatch, "/api/v1/proofing/settings", map[string]any{}, http.StatusBadRequest, "invalid_body"},
		{"an origin that is not one", http.MethodPatch, "/api/v1/proofing/settings", map[string]any{"allowedRedirectOrigins": []string{"https://portal.example.org/path"}}, http.StatusBadRequest, "invalid_input"},
		{"a webhook test without an endpoint", http.MethodPost, "/api/v1/proofing/webhook/test", nil, http.StatusNotFound, "webhook_not_found"},
		{"rotating without an endpoint", http.MethodPost, "/api/v1/proofing/webhook/rotate-secret", nil, http.StatusNotFound, "webhook_not_found"},
		{"removing without an endpoint", http.MethodDelete, "/api/v1/proofing/webhook", nil, http.StatusNotFound, "webhook_not_found"},
		{"a webhook without events", http.MethodPut, "/api/v1/proofing/webhook", map[string]any{"url": "https://hooks.example.org/yivi", "events": []string{}}, http.StatusBadRequest, "invalid_input"},
	} {
		wantProofingError(t, tc.what, env.apiCall(tc.method, tc.path, key.Secret, tc.body), tc.status, tc.code)
	}

	// An origin a flow's return page is on stays until the flow moves off it.
	const origin = "https://portal.example.org"
	_ = env.apiCall(http.MethodPatch, "/api/v1/proofing/settings", key.Secret, map[string]any{"allowedRedirectOrigins": []string{origin}}).Body.Close()
	_ = env.apiCall(http.MethodPut, flowURL+"/hosted", key.Secret, map[string]any{"enabled": true, "completion": "redirect", "returnUrl": origin + "/done"}).Body.Close()
	wantProofingError(t, "removing an origin in use",
		env.apiCall(http.MethodPatch, "/api/v1/proofing/settings", key.Secret, map[string]any{"allowedRedirectOrigins": []string{}}),
		http.StatusConflict, "redirect_origin_in_use")

	wantProofingError(t, "no key", env.apiCall(http.MethodGet, "/api/v1/proofing/settings", "", nil), http.StatusUnauthorized, "unauthorized")
	wantProofingError(t, "an unknown key", env.apiCall(http.MethodGet, "/api/v1/proofing/settings", "yp_live_nope", nil), http.StatusUnauthorized, "invalid_api_key")
}

// An admin reads an active API key back as often as needed, each read
// audited; a revoked key is not read.
func TestProofingAPIKeyReveal(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)
	key := env.proofingKey("acme", "Portal")
	base := "/api/v1/orgs/acme/identity-proofing/api-keys"

	listed := decodeJSON[[]struct {
		ID         string `json:"id"`
		Revealable bool   `json:"revealable"`
	}](t, env.do(http.MethodGet, base, nil))
	if len(listed) != 1 || !listed[0].Revealable {
		t.Errorf("keys = %+v, want the key revealable", listed)
	}
	for range 2 {
		resp := env.do(http.MethodGet, base+"/"+key.ID+"/secret", nil)
		if got := decodeJSON[struct {
			Secret string `json:"secret"`
		}](t, resp); got.Secret != key.Secret || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("secret = %q (Cache-Control %q), want the key itself, not cached", got.Secret, resp.Header.Get("Cache-Control"))
		}
	}
	if n := env.auditCount(orgID, "identity_proofing.api_key_revealed"); n != 2 {
		t.Errorf("reveals audited %d times, want 2", n)
	}

	_ = env.do(http.MethodDelete, base+"/"+key.ID, nil).Body.Close()
	wantProofingError(t, "a revoked key", env.do(http.MethodGet, base+"/"+key.ID+"/secret", nil), http.StatusNotFound, "api_key_not_found")
}

// A key sees and changes only its own org's flows and sessions: another org's
// flow is not listed, not read, not edited and not sent, as in the wallet.
func TestProofingAPIFlowsStayInOrg(t *testing.T) {
	env := setup(t)
	acmeID := env.createOrg("Acme", "acme")
	globexID := env.createOrg("Globex", "globex")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, acmeID, organization.RoleAdmin)
	env.addMembership(me.ID, globexID, organization.RoleAdmin)
	acmeFlow := env.proofingDefaultFlow("acme")
	// The stub numbers flows per org: an extra one gives globex's default an
	// id acme has none of.
	_ = env.proofingFlow("globex", "Spare")
	globexFlow := env.proofingDefaultFlow("globex")
	acmeKey := env.proofingKey("acme", "Acme portal")
	globexKey := env.proofingKey("globex", "Globex portal")

	for _, tc := range []struct {
		key  proofingAPIKeyResp
		want string
	}{{acmeKey, acmeFlow.ID}, {globexKey, globexFlow.ID}} {
		flows := decodeJSON[[]apiSetupFlowResp](t, env.apiCall(http.MethodGet, "/api/v1/proofing/flows", tc.key.Secret, nil))
		ids := map[string]bool{}
		for _, f := range flows {
			ids[f.ID] = true
		}
		if !ids[tc.want] || (tc.key.Name == "Acme portal" && len(flows) != 1) {
			t.Errorf("flows of %s = %+v, want only its own, with %s", tc.key.Name, flows, tc.want)
		}
	}

	foreign := "/api/v1/proofing/flows/" + globexFlow.ID
	for _, tc := range []struct {
		what         string
		method, path string
		body         any
	}{
		{"the versions of another org's flow", http.MethodGet, foreign + "/versions", nil},
		{"editing another org's flow", http.MethodPost, foreign + "/versions", map[string]any{"name": "Taken", "steps": []string{"document_capture", "nfc_read"}}},
		{"activating another org's flow", http.MethodPost, foreign + "/versions/1/activate", nil},
		{"the hosted page of another org's flow", http.MethodGet, foreign + "/hosted", nil},
		{"the address step of another org's flow", http.MethodPut, foreign + "/address", map[string]any{"addressMode": "required"}},
		{"another org's flow as default", http.MethodPut, "/api/v1/proofing/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": globexFlow.ID}},
		{"a session on another org's flow", http.MethodPost, "/api/v1/proofing/sessions", map[string]any{"email": "anna@example.org", "flowId": globexFlow.ID, "sendMail": false}},
	} {
		wantProofingError(t, tc.what, env.apiCall(tc.method, tc.path, acmeKey.Secret, tc.body), http.StatusUnprocessableEntity, "flow_not_found")
	}

	// Globex's flow is as it was.
	versions := decodeJSON[[]proofingFlowResp](t, env.apiCall(http.MethodGet, foreign+"/versions", globexKey.Secret, nil))
	if len(versions) != 1 || versions[0].Name != "Passport only" {
		t.Errorf("globex's flow = %+v, want it untouched", versions)
	}

	// Each key's session list is its own org's.
	_ = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", globexKey.Secret, map[string]any{"email": "bram@example.org", "sendMail": false}).Body.Close()
	page := decodeJSON[struct {
		Sessions []proofingAPISessionResp `json:"sessions"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proofing/sessions", acmeKey.Secret, nil))
	if len(page.Sessions) != 0 {
		t.Errorf("acme's sessions = %+v, want none of globex's", page.Sessions)
	}
}
