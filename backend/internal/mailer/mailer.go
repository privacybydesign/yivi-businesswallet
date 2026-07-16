// Package mailer is the SMTP transport seam: it sends a message given an explicit
// SMTP configuration (resolved per-org by the caller). It is the mail analogue of
// the other provider seams — no domain logic, just the wire protocol. Auth is
// omitted when Username is empty (e.g. MailHog in dev).
package mailer

import (
	"crypto/tls"
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

// Message is a single outbound e-mail (HTML + plain-text alternative).
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
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

// buildMIME renders a multipart/alternative message (text + HTML).
func buildMIME(cfg Config, msg Message) string {
	from := cfg.FromAddress
	if cfg.FromName != "" {
		from = fmt.Sprintf("%s <%s>", cfg.FromName, cfg.FromAddress)
	}
	const boundary = "ybw-boundary-9f1c2a"
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", msg.Subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%s\r\n\r\n", boundary)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n\r\n", msg.TextBody)

	fmt.Fprintf(&b, "--%s\r\n", boundary)
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
	fmt.Fprintf(&b, "%s\r\n\r\n", msg.HTMLBody)

	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}
