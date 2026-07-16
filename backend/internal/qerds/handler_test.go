package qerds

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

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
