package themesettings

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func TestValidateAcceptsEmptyAndWellFormedValues(t *testing.T) {
	inputs := []SettingsInput{
		{},                        // all empty clears the theme
		{PrimaryColor: "#1d4e89"}, // primary only
		{PrimaryColor: "#000000", AccentColor: "#FFFFFF"}, // mixed case hex
		{LogoURI: "https://example.com/logo.svg"},         // https logo
		{LogoURI: "http://example.com/logo.png"},          // http logo
		{LogoURI: "data:image/png;base64,iVBORw0KGgo="},   // data:image logo
		{PrimaryColor: "#ba3354", AccentColor: "#9a2744", LogoURI: "https://x/y.svg"},
	}
	for _, in := range inputs {
		if err := validate(in); err != nil {
			t.Errorf("validate(%+v) = %v, want nil", in, err)
		}
	}
}

func TestValidateRejectsMalformedValues(t *testing.T) {
	cases := map[string]SettingsInput{
		"3-digit hex":    {PrimaryColor: "#fff"},
		"missing hash":   {PrimaryColor: "1d4e89"},
		"non-hex char":   {PrimaryColor: "#gggggg"},
		"bad accent":     {AccentColor: "red"},
		"javascript uri": {LogoURI: "javascript:alert(1)"},
		"data html uri":  {LogoURI: "data:text/html,<script>"},
		"relative uri":   {LogoURI: "/logo.svg"},
		"oversized logo": {LogoURI: "data:image/png;base64," + strings.Repeat("A", MaxLogoURILength)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			err := validate(in)
			if err == nil {
				t.Fatalf("validate(%+v) = nil, want error", in)
			}
			var apiErr *respond.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
				t.Errorf("err = %v, want 400 APIError", err)
			}
		})
	}
}

// putSettings validates the body before it reads the org from context, so the
// rejection paths are exercisable without the full middleware chain.
func TestPutSettingsRejectsInvalidBody(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/theme", strings.NewReader("not json"))
	rec := httptest.NewRecorder()

	err := h.putSettings(rec, req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_body" {
		t.Fatalf("err = %v, want invalid_body APIError", err)
	}
}

func TestPutSettingsRejectsInvalidColor(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/theme",
		bytes.NewReader([]byte(`{"primaryColor":"blue"}`)))
	rec := httptest.NewRecorder()

	err := h.putSettings(rec, req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}
