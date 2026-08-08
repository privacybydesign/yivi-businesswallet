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
	if !strings.Contains(got, `Content-Disposition: attachment; filename="contract.pdf"`) {
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
