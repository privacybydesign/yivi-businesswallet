//go:build integration

package proofingengine_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	pp "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const brandedReference = "req-branded"

var brandingLogo = []byte("\x89PNG\r\n\x1a\nlogo")

// fakeBranding brands brandedReference's sessions only, and records which
// tenant and reference each lookup was for.
type fakeBranding struct {
	mu      sync.Mutex
	lookups [][2]string
}

func (f *fakeBranding) record(tenantID, clientReference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, [2]string{tenantID, clientReference})
}

func (f *fakeBranding) Branding(_ context.Context, tenantID, clientReference string) (*proofingengine.Branding, error) {
	f.record(tenantID, clientReference)
	if clientReference != brandedReference {
		return nil, nil
	}
	return &proofingengine.Branding{
		Name: "CM Tickets", PrimaryColor: "#036BD2", SecondaryColor: "#5412C7", HeaderColor: "#101E1E",
		FontFamily: `"Alexandria", sans-serif`, HasLogo: true,
	}, nil
}

func (f *fakeBranding) Logo(_ context.Context, tenantID, clientReference string) (proofingengine.BrandingLogo, error) {
	f.record(tenantID, clientReference)
	if clientReference != brandedReference {
		return proofingengine.BrandingLogo{}, proofingengine.ErrNoBrandingLogo
	}
	return proofingengine.BrandingLogo{Bytes: brandingLogo, ContentType: "image/png"}, nil
}

// The app gets the branding of the session's own tenant and reference, and
// its logo only from a device holding the session.
func TestIdemBranding(t *testing.T) {
	branding := &fakeBranding{}
	h := newHarness(t, func(c *proofingengine.Config) { c.Branding = branding })
	ctx := context.Background()
	f, err := h.engine.CreateFlow(ctx, h.tenant, chipFlow)
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	claimed := func(reference string) (token, device string) {
		t.Helper()
		sess, err := h.engine.CreateSession(ctx, h.tenant, pp.SessionInput{FlowID: f.ID, ClientReference: reference})
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		link, err := url.Parse(sess.Claim.DeepLink)
		if err != nil {
			t.Fatalf("deep link: %v", err)
		}
		code, claim := h.do(t, http.MethodPost, "/app/handover/"+link.Query().Get("handover")+"/claim", "", nil)
		if code != http.StatusOK {
			t.Fatalf("claim = %d %v", code, claim)
		}
		return claim["token"].(string), claim["deviceToken"].(string)
	}
	getLogo := func(token, device string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/app/"+token+"/branding/logo", nil)
		if device != "" {
			req.Header.Set("X-Device-Token", device)
		}
		rec := httptest.NewRecorder()
		h.mux.ServeHTTP(rec, req)
		return rec
	}

	token, device := claimed(brandedReference)
	code, view := h.do(t, http.MethodGet, "/app/"+token, device, nil)
	if code != http.StatusOK {
		t.Fatalf("GET session = %d %v", code, view)
	}
	got, _ := view["branding"].(map[string]any)
	if got["name"] != "CM Tickets" || got["primaryColor"] != "#036BD2" || got["secondaryColor"] != "#5412C7" ||
		got["headerColor"] != "#101E1E" || got["fontFamily"] != `"Alexandria", sans-serif` || got["hasLogo"] != true {
		t.Errorf("branding = %v", view["branding"])
	}
	if view["relyingParty"] != orgName {
		t.Errorf("relyingParty = %v, want the org's own name next to its branding", view["relyingParty"])
	}
	for _, lookup := range branding.lookups {
		if lookup != [2]string{h.tenant.ID, brandedReference} {
			t.Errorf("branding looked up for %v, want the session's own tenant and reference", lookup)
		}
	}

	if rec := getLogo(token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("logo without a device = %d, want 401", rec.Code)
	}
	if rec := getLogo(token, "not-the-device"); rec.Code != http.StatusUnauthorized {
		t.Errorf("logo for another device = %d, want 401", rec.Code)
	}
	rec := getLogo(token, device)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), brandingLogo) {
		t.Fatalf("logo = %d %q", rec.Code, rec.Body.Bytes())
	}
	if rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("logo headers = %v", rec.Header())
	}

	token, device = claimed("req-plain")
	if code, view := h.do(t, http.MethodGet, "/app/"+token, device, nil); code != http.StatusOK || view["branding"] != nil {
		t.Errorf("unbranded GET session = %d, branding %v; want none", code, view["branding"])
	}
	if rec := getLogo(token, device); rec.Code != http.StatusNotFound {
		t.Errorf("unbranded logo = %d, want 404", rec.Code)
	}
}
