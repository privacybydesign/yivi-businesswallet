//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
)

// A flow's reasons are required in each language its hosted page offers when a
// version is saved, and the hosted page hands them to the person.
func TestFlowReasonsHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flowsPath := "/api/v1/orgs/acme/identity-proofing/flows"
	reasons := func(byLocale map[string]string) map[string]any {
		return map[string]any{
			"name": "Ticket", "steps": []string{"document_capture", "nfc_read"}, "requiredChecks": []string{"nfc.passive_auth"},
			"requestedAttributes": []string{"dg1"}, "attributeReasons": map[string]any{"dg1": byLocale},
		}
	}
	english := map[string]string{"en": "To check you are 18 or older."}
	both := map[string]string{"en": english["en"], "nl": "Om te controleren dat je 18 of ouder bent."}

	// A new flow's page offers every language.
	wantProofingError(t, "a new flow without the Dutch reason", env.postJSON(flowsPath, reasons(english)),
		http.StatusBadRequest, "invalid_input")
	resp := env.postJSON(flowsPath, reasons(both))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create flow with both reasons = %d, want 201", resp.StatusCode)
	}
	type flowView struct {
		ID               string                       `json:"id"`
		Version          int                          `json:"version"`
		AttributeReasons map[string]map[string]string `json:"attributeReasons"`
	}
	flow := decodeJSON[flowView](t, resp)
	if flow.AttributeReasons["dg1"]["nl"] != both["nl"] {
		t.Fatalf("created flow = %+v, want its Dutch reason", flow)
	}

	// An edit is held to the languages the page offers when it is saved.
	versionsPath := flowsPath + "/" + flow.ID + "/versions"
	wantProofingError(t, "a version without the Dutch reason on a page offering both", env.postJSON(versionsPath, reasons(english)),
		http.StatusBadRequest, "invalid_input")
	resp = env.putJSON(flowsPath+"/"+flow.ID+"/hosted", map[string]any{"enabled": true, "locales": []string{"en"}, "completion": proofing.CompletionRedirect})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("offer English only = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = env.postJSON(versionsPath, reasons(english))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("version with the English reason on an English page = %d, want 201", resp.StatusCode)
	}
	if edited := decodeJSON[flowView](t, resp); edited.Version != 2 || edited.AttributeReasons["dg1"]["en"] != english["en"] {
		t.Errorf("edited flow = %+v, want version 2 with the English reason", edited)
	}

	// Nor can the page take on a language the active version has no reasons in.
	for name, locales := range map[string][]string{"Dutch": {"nl"}, "every language": {}} {
		wantProofingError(t, "offer "+name+" on an English-only version",
			env.putJSON(flowsPath+"/"+flow.ID+"/hosted", map[string]any{"enabled": true, "locales": locales, "completion": proofing.CompletionRedirect}),
			http.StatusBadRequest, "reasons_missing")
	}

	// The list carries them to the editor, and the hosted page to the person.
	var listed []flowView
	listed = decodeJSON[[]flowView](t, env.do(http.MethodGet, flowsPath, nil))
	if len(listed) != 1 || listed[0].AttributeReasons["dg1"]["en"] != english["en"] {
		t.Errorf("flow list = %+v, want the reasons of the active version", listed)
	}
	resp = env.putJSON("/api/v1/orgs/acme/identity-proofing/flow-selection", map[string]any{"flowIds": []string{}, "defaultFlowId": flow.ID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set default flow = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
	key := env.proofingKey("acme", "Portal")
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hosted = %d, want 201", resp.StatusCode)
	}
	_, token, _ := strings.Cut(decodeJSON[struct {
		HostedURL string `json:"hostedUrl"`
	}](t, resp).HostedURL, "/p/")
	page := decodeJSON[struct {
		Flow struct {
			AttributeReasons map[string]map[string]string `json:"attributeReasons"`
		} `json:"flow"`
	}](t, env.apiCall(http.MethodGet, "/api/v1/proof/"+token, "", nil))
	if page.Flow.AttributeReasons["dg1"]["en"] != english["en"] {
		t.Errorf("hosted page reasons = %v, want the English reason of dg1", page.Flow.AttributeReasons)
	}
}
