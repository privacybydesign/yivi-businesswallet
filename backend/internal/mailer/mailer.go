// Package mailer is the SMTP transport seam: it sends a message given an explicit
// SMTP configuration (resolved per-org by the caller). It is the mail analogue of
// the other provider seams — no domain logic, just the wire protocol. Auth is
// omitted when Username is empty (e.g. MailHog in dev).
package mailer

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const dialTimeout = 15 * time.Second

// Config is the SMTP connection + identity a single send uses.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// FromName is the display name; FromAddress the envelope/from address.
	FromName    string
	FromAddress string
}

// Message is a single outbound e-mail (HTML + plain-text alternative), with any
// inline images the HTML references by cid:.
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
	// Inline holds images embedded as related parts, each referenced from HTMLBody
	// by cid:<ContentID> (e.g. an org's logo). Empty for a plain message, in which
	// case the wire form stays a bare multipart/alternative.
	Inline []InlineImage
}

// InlineImage is an image carried inside the message as a related MIME part and
// referenced from the HTML by cid:<ContentID>, so it renders in clients that block
// remote images (Gmail, Outlook) and offline. The bytes are sent as-is; the
// transport base64-encodes them.
type InlineImage struct {
	ContentID   string
	ContentType string
	Bytes       []byte
}

// Sender sends a message over SMTP using the given config.
type Sender interface {
	Send(cfg Config, msg Message) error
}

// SMTPSender is the real net/smtp-backed sender.
type SMTPSender struct{}

func New() SMTPSender { return SMTPSender{} }

// Send delivers the message. It uses STARTTLS when the server offers it and
// authenticates with PLAIN when a username is set; a MailHog-style open relay
// (no auth, no TLS) works with an empty username.
func (SMTPSender) Send(cfg Config, msg Message) error {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("mailer: dial %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mailer: smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	}

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("mailer: auth: %w", err)
		}
	}

	if err := client.Mail(cfg.FromAddress); err != nil {
		return fmt.Errorf("mailer: mail from: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mailer: rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: data: %w", err)
	}
	if _, err := w.Write([]byte(buildMIME(cfg, msg))); err != nil {
		return fmt.Errorf("mailer: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer: close body: %w", err)
	}
	return client.Quit()
}

// headerReplacer strips CR and LF from header values so a newline embedded in an
// org-admin-controlled input (from name/address, recipient, subject) cannot
// inject extra headers or split the message. The SMTP envelope (Mail/Rcpt) is
// already CR/LF-validated by net/smtp; the hand-built MIME headers below are not.
var headerReplacer = strings.NewReplacer("\r", "", "\n", "")

// Distinct boundaries for the two nesting levels: the alternative (text + HTML)
// and, when the message has inline images, the related wrapper around it.
const (
	altBoundary = "ybw-alt-9f1c2a"
	relBoundary = "ybw-rel-7d2b4e"
)

// buildMIME renders the message on the wire. With no inline images it is a bare
// multipart/alternative (text + HTML), unchanged from before; with inline images
// the alternative is wrapped in a multipart/related so the HTML's cid: references
// resolve to the attached parts.
func buildMIME(cfg Config, msg Message) string {
	from := cfg.FromAddress
	if cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.FromAddress)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", headerReplacer.Replace(from))
	fmt.Fprintf(&b, "To: %s\r\n", headerReplacer.Replace(msg.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", headerReplacer.Replace(msg.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Inline) == 0 {
		writeAlternative(&b, msg)
		return b.String()
	}

	fmt.Fprintf(&b, "Content-Type: multipart/related; boundary=%s\r\n\r\n", relBoundary)
	fmt.Fprintf(&b, "--%s\r\n", relBoundary)
	writeAlternative(&b, msg)
	for _, img := range msg.Inline {
		writeInlineImage(&b, img)
	}
	fmt.Fprintf(&b, "--%s--\r\n", relBoundary)
	return b.String()
}

// writeAlternative writes the multipart/alternative entity: its own Content-Type
// header, the text/plain part, then the text/html part. Written both at the top
// level (a plain message) and as the first part of the related wrapper, so the two
// paths cannot render the body differently.
func writeAlternative(b *strings.Builder, msg Message) {
	fmt.Fprintf(b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", altBoundary)

	fmt.Fprintf(b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(b, "%s\r\n\r\n", msg.TextBody)

	fmt.Fprintf(b, "--%s\r\n", altBoundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(b, "%s\r\n\r\n", msg.HTMLBody)

	fmt.Fprintf(b, "--%s--\r\n", altBoundary)
}

// writeInlineImage writes one base64 image part of the related wrapper, keyed by a
// Content-ID the HTML references as cid:<ContentID>. The angle brackets are the
// RFC 2045 form; the cid: URL in the HTML omits them.
func writeInlineImage(b *strings.Builder, img InlineImage) {
	contentType := img.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	fmt.Fprintf(b, "--%s\r\n", relBoundary)
	fmt.Fprintf(b, "Content-Type: %s\r\n", headerReplacer.Replace(contentType))
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	fmt.Fprintf(b, "Content-ID: <%s>\r\n", headerReplacer.Replace(img.ContentID))
	b.WriteString("Content-Disposition: inline\r\n\r\n")
	b.WriteString(wrapBase64(img.Bytes))
	b.WriteString("\r\n")
}

// wrapBase64 encodes the bytes and folds them to 76-character lines, the RFC 2045
// limit some SMTP servers enforce on a single line.
func wrapBase64(data []byte) string {
	const lineLength = 76
	encoded := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(encoded) > lineLength {
		b.WriteString(encoded[:lineLength])
		b.WriteString("\r\n")
		encoded = encoded[lineLength:]
	}
	b.WriteString(encoded)
	return b.String()
}
