package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildMIMEWithoutAttachmentsIsUnchanged(t *testing.T) {
	cfg := Config{FromAddress: "no-reply@example.org"}
	msg := Message{To: "a@example.org", Subject: "Hi", TextBody: "text", HTMLBody: "<p>html</p>"}
	got := buildMIME(cfg, msg)
	if strings.Contains(got, "multipart/mixed") {
		t.Fatal("a message without attachments must not be wrapped in multipart/mixed")
	}
	if !strings.Contains(got, "multipart/alternative") {
		t.Fatal("expected a multipart/alternative body")
	}
}

func TestBuildMIMEWithAttachment(t *testing.T) {
	cfg := Config{FromAddress: "no-reply@example.org", FromName: "Acme"}
	pdf := []byte("%PDF-1.7 fake bytes")
	msg := Message{
		To:       "recipient@example.org",
		Subject:  "Your signed document",
		TextBody: "text",
		HTMLBody: "<p>html</p>",
		Attachments: []Attachment{
			{Filename: "contract.pdf", ContentType: "application/pdf", Bytes: pdf},
		},
	}
	got := buildMIME(cfg, msg)

	if !strings.Contains(got, "Content-Type: multipart/mixed; boundary="+mixedBoundary) {
		t.Fatal("expected a multipart/mixed wrapper")
	}
	if !strings.Contains(got, "Content-Type: multipart/alternative") {
		t.Fatal("the body alternative must still be present inside the mixed wrapper")
	}
	// mime.FormatMediaType emits an unquoted token for a plain ASCII filename.
	if !strings.Contains(got, "Content-Disposition: attachment; filename=contract.pdf") {
		t.Fatal("expected the PDF as a disposition:attachment part")
	}
	if !strings.Contains(got, "Content-Type: application/pdf") {
		t.Fatal("expected the attachment content type")
	}
	if !strings.Contains(got, base64.StdEncoding.EncodeToString(pdf)) {
		t.Fatal("expected the base64-encoded attachment bytes")
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\r\n"), "--"+mixedBoundary+"--") {
		t.Fatal("expected the mixed wrapper to be closed")
	}
}

// A non-ASCII (Dutch) filename must ride as the RFC 2231 filename*= form, not raw
// 8-bit bytes in a header, and must not inject header lines.
func TestBuildMIMEAttachmentEncodesNonASCIIFilename(t *testing.T) {
	cfg := Config{FromAddress: "no-reply@example.org"}
	msg := Message{
		To:      "recipient@example.org",
		Subject: "Doc",
		Attachments: []Attachment{
			{Filename: "reçu \"final\".pdf", ContentType: "application/pdf", Bytes: []byte("x")},
		},
	}
	got := buildMIME(cfg, msg)
	if !strings.Contains(got, "filename*=utf-8''") {
		t.Fatalf("expected an RFC 2231 filename* form, got:\n%s", got)
	}
	if strings.Contains(got, "reçu") {
		t.Fatal("the raw non-ASCII filename must not appear unencoded in the header")
	}
}
