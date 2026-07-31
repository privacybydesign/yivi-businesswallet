package mailer

import (
	"encoding/base64"
	"strings"
	"testing"
)

var testCfg = Config{
	Host:        "mail.example.org",
	Port:        25,
	FromName:    "Acme BV",
	FromAddress: "no-reply@example.org",
}

// A message with no inline images stays a bare multipart/alternative, so ordinary
// mail is unchanged by the related-wrapper support.
func TestBuildMIMEPlainMessageIsAlternativeOnly(t *testing.T) {
	mime := buildMIME(testCfg, Message{
		To:       "user@example.com",
		Subject:  "Hello",
		TextBody: "plain body",
		HTMLBody: "<p>html body</p>",
	})

	if !strings.Contains(mime, "Content-Type: multipart/alternative; boundary="+altBoundary) {
		t.Errorf("plain message is not multipart/alternative:\n%s", mime)
	}
	if strings.Contains(mime, "multipart/related") {
		t.Errorf("plain message should not carry a related wrapper:\n%s", mime)
	}
	if !strings.Contains(mime, "plain body") || !strings.Contains(mime, "<p>html body</p>") {
		t.Errorf("plain message is missing a part:\n%s", mime)
	}
}

// A message with an inline image wraps the alternative in a multipart/related and
// attaches the image as a base64 part keyed by its Content-ID.
func TestBuildMIMEInlineImageWrapsInRelated(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n-not-a-real-image-but-enough-bytes-to-wrap-past-seventy-six-base64-chars")
	mime := buildMIME(testCfg, Message{
		To:       "user@example.com",
		Subject:  "Hello",
		TextBody: "plain body",
		HTMLBody: `<img src="cid:orglogo" />`,
		Inline: []InlineImage{{
			ContentID:   "orglogo",
			ContentType: "image/png",
			Bytes:       png,
		}},
	})

	if !strings.Contains(mime, "Content-Type: multipart/related; boundary="+relBoundary) {
		t.Errorf("inline message is not wrapped in multipart/related:\n%s", mime)
	}
	// The alternative still carries both body parts, nested inside the related.
	if !strings.Contains(mime, "Content-Type: multipart/alternative; boundary="+altBoundary) {
		t.Errorf("inline message lost its alternative:\n%s", mime)
	}
	for _, want := range []string{
		"Content-Type: image/png",
		"Content-Transfer-Encoding: base64",
		"Content-ID: <orglogo>",
		"Content-Disposition: inline",
	} {
		if !strings.Contains(mime, want) {
			t.Errorf("inline part is missing %q:\n%s", want, mime)
		}
	}
	// The image bytes round-trip through base64.
	if !strings.Contains(mime, base64.StdEncoding.EncodeToString(png)[:20]) {
		t.Errorf("inline part does not carry the encoded image:\n%s", mime)
	}
	if !strings.HasSuffix(strings.TrimRight(mime, "\r\n"), "--"+relBoundary+"--") {
		t.Errorf("inline message does not close the related wrapper:\n%s", mime)
	}
}

// A long payload is folded to 76-character lines, the RFC 2045 limit.
func TestWrapBase64FoldsLines(t *testing.T) {
	wrapped := wrapBase64(make([]byte, 300))
	for line := range strings.SplitSeq(wrapped, "\r\n") {
		if len(line) > 76 {
			t.Errorf("base64 line exceeds 76 chars (%d): %q", len(line), line)
		}
	}
}
