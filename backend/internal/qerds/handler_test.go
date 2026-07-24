package qerds

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

type multipartFile struct {
	field       string
	filename    string
	contentType string
	content     []byte
}

func newMultipartRequest(t *testing.T, fields map[string]string, files []multipartFile) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %q: %v", k, err)
		}
	}
	for _, f := range files {
		header := textproto.MIMEHeader{}
		if f.contentType != "" {
			header.Set("Content-Type", f.contentType)
		}
		part, err := w.CreatePart(mimeHeader(f.field, f.filename, header))
		if err != nil {
			t.Fatalf("create part %q: %v", f.filename, err)
		}
		if _, err := part.Write(f.content); err != nil {
			t.Fatalf("write part %q: %v", f.filename, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/qerds/messages", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(multipartMemoryBytes); err != nil {
		t.Fatalf("parse multipart: %v", err)
	}
	return req
}

func mimeHeader(field, filename string, extra textproto.MIMEHeader) textproto.MIMEHeader {
	h := textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="` + field + `"; filename="` + filename + `"`},
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func TestParseAttachments(t *testing.T) {
	req := newMultipartRequest(t, map[string]string{"subject": "hi"}, []multipartFile{
		{field: attachmentFormKey, filename: "a.pdf", contentType: "application/pdf", content: []byte("one")},
		{field: attachmentFormKey, filename: "no-type.bin", content: []byte("two")},
		{field: "ignored", filename: "other.txt", contentType: "text/plain", content: []byte("skip")},
	})

	attachments, err := parseAttachments(req)
	if err != nil {
		t.Fatalf("parseAttachments: %v", err)
	}
	if len(attachments) != 2 {
		t.Fatalf("got %d attachments, want 2 (non-%q parts ignored)", len(attachments), attachmentFormKey)
	}
	if attachments[0].Filename != "a.pdf" || attachments[0].ContentType != "application/pdf" || string(attachments[0].Content) != "one" {
		t.Errorf("attachment[0] = %+v", attachments[0])
	}
	// Missing part content type falls back to octet-stream.
	if attachments[1].ContentType != defaultContentType {
		t.Errorf("attachment[1] content type = %q, want %q", attachments[1].ContentType, defaultContentType)
	}
}

func TestParseAttachmentsStripsPathFromFilename(t *testing.T) {
	req := newMultipartRequest(t, nil, []multipartFile{
		{field: attachmentFormKey, filename: "../../etc/passwd", content: []byte("x")},
	})
	attachments, err := parseAttachments(req)
	if err != nil {
		t.Fatalf("parseAttachments: %v", err)
	}
	if len(attachments) != 1 || attachments[0].Filename != "passwd" {
		t.Fatalf("filename = %q, want the base name %q", attachments[0].Filename, "passwd")
	}
}

func TestParseAttachmentsRejectsTooMany(t *testing.T) {
	files := make([]multipartFile, maxAttachmentCount+1)
	for i := range files {
		files[i] = multipartFile{field: attachmentFormKey, filename: "f.txt", content: []byte("x")}
	}
	req := newMultipartRequest(t, nil, files)

	_, err := parseAttachments(req)
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "too_many_attachments" {
		t.Fatalf("err = %v, want a too_many_attachments API error", err)
	}
}

func TestNamespacedLocalPart(t *testing.T) {
	const slug = "acme"
	cases := []struct {
		name  string
		input string
		want  string
		err   bool
	}{
		{name: "empty defaults to slug", input: "", want: slug},
		{name: "whitespace defaults to slug", input: "  ", want: slug},
		{name: "bare slug", input: "acme", want: "acme"},
		{name: "uppercased slug is lowered", input: "ACME", want: "acme"},
		{name: "subdivision under slug", input: "acme.sales", want: "acme.sales"},
		{name: "nested subdivision", input: "acme.sales.eu", want: "acme.sales.eu"},
		{name: "hyphenated subdivision", input: "acme.legal-dept", want: "acme.legal-dept"},
		{name: "mixed case subdivision is lowered", input: "Acme.Sales", want: "acme.sales"},
		{name: "another org's slug is rejected", input: "radboud", err: true},
		{name: "another org's namespace is rejected", input: "radboud.sales", err: true},
		{name: "slug as a prefix without separator is rejected", input: "acmecorp", err: true},
		{name: "slug with a non-separator boundary is rejected", input: "acme-corp", err: true},
		{name: "trailing separator with empty label is rejected", input: "acme.", err: true},
		{name: "double separator is rejected", input: "acme..sales", err: true},
		{name: "illegal characters are rejected", input: "acme.sa_les", err: true},
		{name: "embedded at-sign is rejected", input: "acme.sales@evil", err: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := namespacedLocalPart(slug, tc.input)
			if tc.err {
				if !errors.Is(err, ErrAddressOutsideNamespace) {
					t.Fatalf("input %q: err = %v, want ErrAddressOutsideNamespace", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("input %q: unexpected err %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("input %q: got %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// recordingAddressManager captures the addresses passed to ProvisionAddress so a
// handler test can assert what (if anything) reached the store.
type recordingAddressManager struct {
	existing    []Address
	provisioned []string
}

func (m *recordingAddressManager) ProvisionAddress(_ context.Context, orgID uuid.UUID, address string, makeDefault bool, _ string) (Address, error) {
	m.provisioned = append(m.provisioned, address)
	return Address{ID: uuid.New(), OrganizationID: orgID, Address: address, IsDefault: makeDefault}, nil
}

func (m *recordingAddressManager) ListAddresses(_ context.Context, _ uuid.UUID) ([]Address, error) {
	return m.existing, nil
}

func (m *recordingAddressManager) SetDefaultAddress(_ context.Context, _, _ uuid.UUID) (Address, error) {
	return Address{}, nil
}

func provisionRequest(t *testing.T, slug, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/orgs/"+slug+"/qerds/addresses", bytes.NewBufferString(body))
	ctx := organization.ContextWithOrg(req.Context(), organization.Organization{ID: uuid.New(), Slug: slug})
	return httptest.NewRecorder(), req.WithContext(ctx)
}

// TestProvisionAddressRejectsCrossOrgSquat is the regression guard for the
// namespace-ownership fix: an org admin who asks for a local part belonging to
// another org is rejected before the store is ever touched.
func TestProvisionAddressRejectsCrossOrgSquat(t *testing.T) {
	mgr := &recordingAddressManager{}
	h := &Handler{addresses: mgr, addressDomain: "qerds.localhost"}

	rec, req := provisionRequest(t, "acme", `{"localPart":"radboud"}`)
	err := h.provisionAddress(rec, req)

	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "address_outside_namespace" {
		t.Fatalf("err = %v, want an address_outside_namespace API error", err)
	}
	if len(mgr.provisioned) != 0 {
		t.Fatalf("store was called with %v, want no provisioning attempt", mgr.provisioned)
	}
}

// TestProvisionAddressAnchorsOnSlug covers the allowed paths: an empty local
// part yields the bare slug address, and a subdivision is anchored under the
// org's slug namespace.
func TestProvisionAddressAnchorsOnSlug(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "empty defaults to slug address", body: `{}`, want: "acme@qerds.localhost"},
		{name: "subdivision under slug", body: `{"localPart":"acme.legal"}`, want: "acme.legal@qerds.localhost"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &recordingAddressManager{}
			h := &Handler{addresses: mgr, addressDomain: "qerds.localhost"}

			rec, req := provisionRequest(t, "acme", tc.body)
			if err := h.provisionAddress(rec, req); err != nil {
				t.Fatalf("provisionAddress: %v", err)
			}
			if len(mgr.provisioned) != 1 || mgr.provisioned[0] != tc.want {
				t.Fatalf("provisioned = %v, want [%q]", mgr.provisioned, tc.want)
			}
		})
	}
}
