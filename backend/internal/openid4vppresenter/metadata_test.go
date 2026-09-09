package openid4vppresenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
)

func TestMetadataDerivesFromConfigAndCapabilities(t *testing.T) {
	m := NewMetadata("https://wallet.example.com/", eudiholder.Formats(), NewUnverifiedDecoder(Policy{}))
	if m.Issuer != "https://wallet.example.com" || m.AuthorizationEndpoint != "https://wallet.example.com/openid4vp" {
		t.Errorf("issuer/endpoint = %q / %q", m.Issuer, m.AuthorizationEndpoint)
	}
	if got := m.ClientIDPrefixesSupported; len(got) != 1 || got[0] != "x509_san_dns" {
		t.Errorf("client_id_prefixes_supported = %v", got)
	}
	sd, ok := m.VPFormatsSupported["dc+sd-jwt"].(*eudiholder.SDJWTVCFormat)
	if !ok || len(sd.KBJWTAlgValues) == 0 || sd.KBJWTAlgValues[0] != "ES256" {
		t.Errorf("vp_formats_supported = %#v", m.VPFormatsSupported)
	}

	// A validator that binds no prefix leaves the field out, not asserted.
	none := NewMetadata("https://wallet.example.com", eudiholder.Formats(), noPrefixValidator{})
	body, err := json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if _, present := doc["client_id_prefixes_supported"]; present {
		t.Error("client_id_prefixes_supported advertised without a validator")
	}
}

func TestMetadataHandlerServesJSONOnRootMux(t *testing.T) {
	h, err := NewMetadataHandler(NewMetadata("https://wallet.example.com", eudiholder.Formats(), noPrefixValidator{}))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.RegisterRoot(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, WellKnownPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != metadataCacheControl {
		t.Errorf("Cache-Control = %q", cc)
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Issuer == "" {
		t.Fatalf("body is not the metadata document: %v %s", err, rec.Body.String())
	}
}

// noPrefixValidator stands in for a validator that can bind no client_id prefix.
type noPrefixValidator struct{}

func (noPrefixValidator) Validate(context.Context, string, []byte) (RequestObject, error) {
	return RequestObject{}, ErrInvalidRequestObject
}
func (noPrefixValidator) ClientIDPrefixes() []string { return nil }
