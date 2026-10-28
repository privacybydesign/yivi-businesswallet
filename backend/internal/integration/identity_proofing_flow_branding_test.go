//go:build integration

package integration

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

// A flow's own branding is set by an admin, shown on the flow's hosted page
// over the org's (its name, its colour as the page's theme, its logo; the org's
// white-label and contact for the rest), and gone again once the flow goes
// back to the org's. The harness wires the app branding the hosted page takes
// its theme and logo from, as cmd/api does.
func TestFlowBrandingHTTPFlow(t *testing.T) {
	env := setup(t)
	orgID := env.createOrg("Acme", "acme")
	me := env.login("admin@acme.test")
	env.addMembership(me.ID, orgID, organization.RoleAdmin)

	flow := env.proofingDefaultFlow("acme")
	key := env.proofingKey("acme", "Portal")
	path := "/api/v1/orgs/acme/identity-proofing/flows/" + flow.ID + "/branding"
	logo := testLogoPNG(t)
	var orgLogo bytes.Buffer
	if err := png.Encode(&orgLogo, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatalf("encode org logo: %v", err)
	}

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
	resp := env.putBranding("acme", map[string]string{
		"displayName": "Acme Identity", "supportContact": "help@acme.example",
		"primaryColor": "#1F5B4A", "secondaryColor": "#112233",
	}, orgLogo.Bytes())
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

	resp = env.putBrandingForm(path, map[string]string{"custom": "true", "displayName": "Acme Reizen", "primaryColor": "#22337A"}, logo)
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

	// The hosted page of the flow wears it: the flow's name, colour and logo,
	// and the org's support contact and second colour.
	resp = env.apiCall(http.MethodPost, "/api/v1/proofing/sessions", key.Secret, map[string]any{"hosted": true})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create hosted = %d, want 201", resp.StatusCode)
	}
	_, token, _ := strings.Cut(decodeJSON[struct {
		HostedURL string `json:"hostedUrl"`
	}](t, resp).HostedURL, "/p/")
	hostedPath := "/api/v1/proof/" + token
	wantPage := func(when, name, color string, logo []byte) {
		t.Helper()
		wantHostedPageBranding(t, env, hostedPath, when, name, color, logo)
	}
	wantPage("custom", "Acme Reizen", "#22337A", logo)

	// Back to the org's branding: the flow's fields and logo are gone, and the
	// page wears the org's.
	resp = env.putBrandingForm(path, map[string]string{"custom": "false"}, nil)
	if got := decodeJSON[brandingView](t, resp); got.Custom || got.DisplayName != "" || got.LogoURI != "" {
		t.Errorf("after going back = %+v, want the org's", got)
	}
	wantPage("the org's", "Acme Identity", "#1F5B4A", orgLogo.Bytes())
	wantProofingError(t, "the logo of a flow without one", env.do(http.MethodGet, path+"/logo", nil), http.StatusNotFound, "not_found")
}

// flowHostedPage is the hosted page of a session as it wears a branding: the
// org's name and branding, and the theme it takes from them.
type flowHostedPage struct {
	Organization struct {
		Name     string         `json:"name"`
		Branding hostedBranding `json:"branding"`
	} `json:"organization"`
	Theme *struct {
		PrimaryColor   string `json:"primaryColor"`
		SecondaryColor string `json:"secondaryColor"`
	} `json:"theme"`
}

// wantHostedPageBranding checks that the hosted page at hostedPath wears name,
// color and the logo bytes, over the org's contact and second colour.
func wantHostedPageBranding(t *testing.T, env *testEnv, hostedPath, when, name, color string, logo []byte) {
	t.Helper()
	page := decodeJSON[flowHostedPage](t, env.apiCall(http.MethodGet, hostedPath, "", nil))
	b := page.Organization.Branding
	if page.Organization.Name != name || b.DisplayName != name || b.PrimaryColor != color ||
		b.SupportContact != "help@acme.example" || !strings.HasPrefix(b.LogoURI, hostedPath+"/logo") {
		t.Errorf("%s: hosted page = %+v; want %s in %s with a logo, over the org's contact", when, page, name, color)
	}
	if page.Theme == nil || page.Theme.PrimaryColor != color || page.Theme.SecondaryColor != "#112233" {
		t.Errorf("%s: hosted theme = %+v; want %s over the org's second colour", when, page.Theme, color)
	}
	served := env.apiCall(http.MethodGet, hostedPath+"/logo", "", nil)
	if body, _ := io.ReadAll(served.Body); served.StatusCode != http.StatusOK || !bytes.Equal(body, logo) {
		t.Errorf("%s: hosted logo = %d, want 200 with the right logo", when, served.StatusCode)
	}
	_ = served.Body.Close()
}
