package themesettings

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

func TestValidateColorsAcceptsEmptyAndWellFormedValues(t *testing.T) {
	inputs := []SettingsInput{
		{},                        // all empty clears the theme
		{PrimaryColor: "#1d4e89"}, // primary only
		{PrimaryColor: "#000000", AccentColor: "#FFFFFF"}, // mixed case hex
		{PrimaryColor: "#ba3354", AccentColor: "#9a2744"},
		{ // full palette set
			PrimaryColor: "#1d4e89", AccentColor: "#ba3354", TextColor: "#111827",
			SurfaceColor: "#ffffff", BorderColor: "#e5e7eb", LinkColor: "#2563eb",
			SuccessColor: "#16a34a", WarningColor: "#d97706", ErrorColor: "#dc2626",
		},
		{TextColor: "#abcdef", SuccessColor: "#012345"}, // a couple of the new fields only
		{ // navigation chrome colours and a valid font-family list
			SidebarColor: "#0f172a", TopbarColor: "#1e293b",
			FontFamily: `"Open Sans", system-ui, sans-serif`,
		},
	}
	for _, in := range inputs {
		if err := validateColors(in); err != nil {
			t.Errorf("validateColors(%+v) = %v, want nil", in, err)
		}
	}
}

func TestValidateColorsRejectsMalformedValues(t *testing.T) {
	cases := map[string]SettingsInput{
		"3-digit hex":    {PrimaryColor: "#fff"},
		"missing hash":   {PrimaryColor: "1d4e89"},
		"non-hex char":   {PrimaryColor: "#gggggg"},
		"bad accent":     {AccentColor: "red"},
		"bad success":    {SuccessColor: "#12345"},
		"bad text":       {TextColor: "green"},
		"bad sidebar":    {SidebarColor: "navy"},
		"font injection": {FontFamily: `"a; } body{}`},
		"font too long":  {FontFamily: strings.Repeat("a", 121)},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateColors(in)
			if err == nil {
				t.Fatalf("validateColors(%+v) = nil, want error", in)
			}
			var apiErr *respond.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
				t.Errorf("err = %v, want 400 APIError", err)
			}
		})
	}
}

func TestDetectLogoType(t *testing.T) {
	pngHeader := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	gifHeader := []byte("GIF89a")
	jpegHeader := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	webpHeader := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)

	cases := map[string]struct {
		data     []byte
		wantType string
		wantOK   bool
	}{
		"png":            {pngHeader, "image/png", true},
		"gif":            {gifHeader, "image/gif", true},
		"jpeg":           {jpegHeader, "image/jpeg", true},
		"webp":           {webpHeader, "image/webp", true},
		"svg plain":      {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "image/svg+xml", true},
		"svg xml decl":   {[]byte("<?xml version=\"1.0\"?>\n<svg></svg>"), "image/svg+xml", true},
		"svg leading ws": {[]byte("  \n<svg></svg>"), "image/svg+xml", true},
		"html":           {[]byte("<!doctype html><html></html>"), "", false},
		"plain text":     {[]byte("just some text, not an image at all"), "", false},
		"xml not svg":    {[]byte("<?xml version=\"1.0\"?>\n<rss></rss>"), "", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			gotType, gotOK := detectLogoType(tc.data)
			if gotOK != tc.wantOK || gotType != tc.wantType {
				t.Errorf("detectLogoType = (%q, %v), want (%q, %v)", gotType, gotOK, tc.wantType, tc.wantOK)
			}
		})
	}
}

func TestLogoURL(t *testing.T) {
	updated := time.Unix(1_700_000_000, 0)
	cases := map[string]struct {
		settings Settings
		want     string
	}{
		"no logo": {Settings{HasLogo: false, UpdatedAt: &updated}, ""},
		"with logo": {
			Settings{HasLogo: true, UpdatedAt: &updated},
			"/api/v1/orgs/acme/theme/logo?v=1700000000",
		},
		"logo but no timestamp": {
			Settings{HasLogo: true},
			"/api/v1/orgs/acme/theme/logo?v=0",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := logoURL("acme", tc.settings); got != tc.want {
				t.Errorf("logoURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSetLogoResponseHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	setLogoResponseHeaders(rec.Header(), "image/svg+xml")

	want := map[string]string{
		"Content-Type":           "image/svg+xml",
		"X-Content-Type-Options": "nosniff",
		// The sandbox + null default-src stop an uploaded SVG from running script.
		"Content-Security-Policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
	}
	for header, wantValue := range want {
		if got := rec.Header().Get(header); got != wantValue {
			t.Errorf("%s = %q, want %q", header, got, wantValue)
		}
	}
}

// putSettings parses and validates the form before it reads the org from
// context, so the rejection paths are exercisable without the full middleware
// chain.
func TestPutSettingsRejectsNonMultipartBody(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/theme", strings.NewReader("not a form"))
	rec := httptest.NewRecorder()

	err := h.putSettings(rec, req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_body" {
		t.Fatalf("err = %v, want invalid_body APIError", err)
	}
}

func TestPutSettingsRejectsInvalidColor(t *testing.T) {
	h := &Handler{}
	body, contentType := multipartForm(t, map[string]string{"primaryColor": "blue"}, "", nil)
	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/theme", body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	err := h.putSettings(rec, req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}

func TestParseLogoUpdateKeepsWhenNoFileOrFlag(t *testing.T) {
	body, contentType := multipartForm(t, map[string]string{"primaryColor": "#1d4e89"}, "", nil)
	req := newMultipartRequest(t, body, contentType)

	logo, err := parseLogoUpdate(req)
	if err != nil {
		t.Fatalf("parseLogoUpdate: %v", err)
	}
	if logo.Replace {
		t.Errorf("Replace = true, want false when no file and no removeLogo flag")
	}
}

func TestParseLogoUpdateClearsOnRemoveFlag(t *testing.T) {
	body, contentType := multipartForm(t, map[string]string{"removeLogo": "true"}, "", nil)
	req := newMultipartRequest(t, body, contentType)

	logo, err := parseLogoUpdate(req)
	if err != nil {
		t.Fatalf("parseLogoUpdate: %v", err)
	}
	if !logo.Replace || len(logo.Logo.Bytes) != 0 {
		t.Errorf("logo = %+v, want Replace with empty bytes (clear)", logo)
	}
}

func TestParseLogoUpdateStoresUploadedImage(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02}
	body, contentType := multipartForm(t, nil, "logo.png", png)
	req := newMultipartRequest(t, body, contentType)

	logo, err := parseLogoUpdate(req)
	if err != nil {
		t.Fatalf("parseLogoUpdate: %v", err)
	}
	if !logo.Replace || logo.Logo.ContentType != "image/png" || !bytes.Equal(logo.Logo.Bytes, png) {
		t.Errorf("logo = %+v, want the uploaded PNG", logo)
	}
}

func TestParseLogoUpdateRejectsNonImage(t *testing.T) {
	body, contentType := multipartForm(t, nil, "notes.txt", []byte("this is plainly not an image file"))
	req := newMultipartRequest(t, body, contentType)

	_, err := parseLogoUpdate(req)
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError", err)
	}
}

func TestParseLogoUpdateRejectsEmptyFile(t *testing.T) {
	body, contentType := multipartForm(t, nil, "empty.png", []byte{})
	req := newMultipartRequest(t, body, contentType)

	_, err := parseLogoUpdate(req)
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "invalid_input" {
		t.Fatalf("err = %v, want invalid_input APIError for an empty file", err)
	}
}

// newMultipartRequest parses a multipart body into a request ready for
// parseLogoUpdate (which expects ParseMultipartForm to have run).
func newMultipartRequest(t *testing.T, body *bytes.Buffer, contentType string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/orgs/acme/theme", body)
	req.Header.Set("Content-Type", contentType)
	if err := req.ParseMultipartForm(multipartMemory); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}
	return req
}

// multipartForm builds a multipart body from string fields and an optional file
// part, returning the body and its Content-Type header.
func multipartForm(t *testing.T, fields map[string]string, fileName string, fileData []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %q: %v", k, err)
		}
	}
	if fileName != "" {
		part, err := mw.CreateFormFile("logo", fileName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(fileData); err != nil {
			t.Fatalf("write file data: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
