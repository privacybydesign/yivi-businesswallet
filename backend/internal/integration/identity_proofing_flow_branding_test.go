//go:build integration

package integration

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// A flow's own branding is set by an admin, shown on the flow's hosted page
// over the org's, and gone again once the flow goes back to the org's.
func TestFlowBrandingHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")
	path := "/api/v1/orgs/acme/identity-proofing/flows/" + flow.ID + "/branding"
	logo := testLogoPNG(t)

	type brandingView struct {
		Custom         bool   `json:"custom"`
		DisplayName    string `json:"displayName"`
		SupportContact string `json:"supportContact"`
		LogoURI        string `json:"logoUri"`
	}
	if got := decodeJSON[brandingView](t, env.do(http.MethodGet, path, nil)); got.Custom || got.DisplayName != "" || got.LogoURI != "" {
		t.Errorf("a flow never set = %+v, want the org's", got)
	}

	// The org's branding is what a flow without its own, or a field it leaves
	// empty, falls back to.
	resp := env.putBranding("acme", map[string]string{"displayName": "Acme Identity", "supportContact": "help@acme.example"}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save org branding = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	wantProofingError(t, "a form without custom", env.putBrandingForm(path, map[string]string{"displayName": "Acme Reizen"}, nil),
		http.StatusBadRequest, "invalid_body")
	wantProofingError(t, "a bad colour", env.putBrandingForm(path, map[string]string{"custom": "true", "primaryColor": "green"}, nil),
		http.StatusBadRequest, "invalid_input")
	wantProofingError(t, "an unknown flow",
		env.putBrandingForm("/api/v1/orgs/acme/identity-proofing/flows/nope/branding", map[string]string{"custom": "true"}, nil),
		http.StatusUnprocessableEntity, "flow_not_found")

	resp = env.putBrandingForm(path, map[string]string{"custom": "true", "displayName": "Acme Reizen"}, logo)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("save flow branding = %d, want 200", resp.StatusCode)
	}
	saved := decodeJSON[brandingView](t, resp)
	if !saved.Custom || saved.DisplayName != "Acme Reizen" || saved.SupportContact != "" ||
		!strings.Contains(saved.LogoURI, "/flows/"+flow.ID+"/branding/logo") {
		t.Fatalf("saved = %+v; want custom, named Acme Reizen, no contact of its own, with a logo", saved)
	}
	served := env.do(http.MethodGet, path+"/logo", nil)
	if body, _ := io.ReadAll(served.Body); served.StatusCode != http.StatusOK || !bytes.Equal(body, logo) ||
		served.Header.Get(headerContentType) != contentTypePNG {
		t.Errorf("flow logo = %d %q, want 200 with the uploaded PNG", served.StatusCode, served.Header.Get(headerContentType))
	}
	_ = served.Body.Close()

	// The hosted page of the flow wears it: the flow's name and logo, and the
	// org's support contact.
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hosted = %d, want 201", resp.StatusCode)
	}
	_, token, _ := strings.Cut(decodeJSON[struct {
		HostedURL string `json:"hostedUrl"`
	}](t, resp).HostedURL, "/p/")
	hostedPath := "/api/v1/proof/" + token
	type hostedPage struct {
		Organization struct {
			Name     string `json:"name"`
			Branding struct {
				DisplayName    string `json:"displayName"`
				SupportContact string `json:"supportContact"`
				LogoURI        string `json:"logoUri"`
			} `json:"branding"`
		} `json:"organization"`
	}
	page := decodeJSON[hostedPage](t, env.apiCall(http.MethodGet, hostedPath, "", nil))
	if b := page.Organization.Branding; page.Organization.Name != "Acme Reizen" || b.DisplayName != "Acme Reizen" ||
		b.SupportContact != "help@acme.example" || !strings.HasPrefix(b.LogoURI, hostedPath+"/logo") {
		t.Errorf("hosted page = %+v; want the flow's name and logo over the org's contact", page)
	}
	hostedLogo := env.apiCall(http.MethodGet, hostedPath+"/logo", "", nil)
	if body, _ := io.ReadAll(hostedLogo.Body); hostedLogo.StatusCode != http.StatusOK || !bytes.Equal(body, logo) {
		t.Errorf("hosted logo = %d, want 200 with the flow's logo", hostedLogo.StatusCode)
	}
	_ = hostedLogo.Body.Close()

	// Back to the org's branding: the flow's fields and logo are gone.
	resp = env.putBrandingForm(path, map[string]string{"custom": "false"}, nil)
	if got := decodeJSON[brandingView](t, resp); got.Custom || got.DisplayName != "" || got.LogoURI != "" {
		t.Errorf("after going back = %+v, want the org's", got)
	}
	page = decodeJSON[hostedPage](t, env.apiCall(http.MethodGet, hostedPath, "", nil))
	if page.Organization.Name != "Acme Identity" || page.Organization.Branding.LogoURI != "" {
		t.Errorf("hosted page after going back = %+v; want the org's name and no logo", page)
	}
	wantProofingError(t, "the logo of a flow without one", env.do(http.MethodGet, path+"/logo", nil), http.StatusNotFound, "not_found")
}
