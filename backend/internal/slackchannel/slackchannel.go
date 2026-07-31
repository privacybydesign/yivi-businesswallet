// Package slackchannel is the Slack delivery route of the notification layer: an
// organization stores one Slack incoming-webhook URL, and every event the org
// subscribed to the Slack channel for is posted to it as a message.
//
// It is a webhook rather than a Slack app on purpose. A webhook is a single value
// an org admin pastes in — no OAuth flow, no per-deployment Slack app to register,
// no token to refresh — and the workspace admin who created it decided there and
// then which channel it may post to, so this side cannot widen the audience.
//
// The webhook URL is the workspace's secret: whoever holds it can post as the
// integration. So it is encrypted at rest under the deployment Slack key, it is
// never returned to the frontend (Settings reports HasWebhook, not the URL), and
// it is kept out of every log line and error — the errors here name the
// organization, and a transport failure is stripped of the URL net/http puts in it
// (see Channel.post).
//
// What is posted is the notification layer's own rendering: the mail catalogue's
// name for the audit action plus notifications.Summarize of its metadata, the same
// lines the e-mail channel sends. The catalog in internal/notifications is the
// gate on which events may reach an outside system at all.
package slackchannel

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured means the organization has no usable (present + enabled) Slack
// webhook, so nothing can be posted for it.
var ErrNotConfigured = errors.New("slackchannel: no slack webhook configured for organization")

// ErrNoEncryptionKey means the deployment has no Slack encryption key, so a
// webhook URL cannot be stored. It is a deployment misconfiguration, not a
// mistake by the admin saving the settings — but they are the one who sees it, so
// the handler answers it with its own code rather than a 500.
var ErrNoEncryptionKey = errors.New("slackchannel: no encryption key configured; cannot store a webhook url")

// ErrInvalidWebhookURL means the URL is not a Slack incoming webhook. Its message
// deliberately repeats no part of the input: the value is a secret, and this error
// is rendered into an API response.
var ErrInvalidWebhookURL = errors.New("slackchannel: the webhook url must be an https url at " + webhookHost)

const (
	webhookScheme = "https"
	// webhookHost is the only host a webhook URL may point at. Posting is the one
	// outbound request an org admin gets to aim, so it is pinned at Slack's own host:
	// without that, saved settings would be a request-forgery primitive against
	// whatever the backend can reach, with an org's audit metadata as the payload.
	webhookHost = "hooks.slack.com"
)

// DeliveryError reports a webhook Slack or the network refused. Reason is safe to
// show an admin: it carries the status and Slack's own short refusal
// ("no_service", "invalid_payload") or the transport failure, never the URL.
type DeliveryError struct {
	Reason string
}

func (e *DeliveryError) Error() string {
	return "slackchannel: the webhook was not accepted: " + e.Reason
}

// Settings is the non-secret view of an org's Slack configuration (never the
// webhook URL). Configured is false when no row exists yet.
type Settings struct {
	Configured bool `json:"configured"`
	// HasWebhook reports whether a webhook URL is stored, so the settings screen can
	// show that one is in place without ever receiving it.
	HasWebhook bool       `json:"hasWebhook"`
	Enabled    bool       `json:"enabled"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's Slack configuration. WebhookURL is
// optional on update, the same shape the SMTP password uses: nil keeps the stored
// URL, a non-nil empty string clears it, and any other value replaces it.
type SettingsInput struct {
	WebhookURL *string
	Enabled    bool
}

// NormalizeWebhookURL trims a submitted webhook URL and checks it is a Slack
// incoming webhook: https, at hooks.slack.com, with a path (the path is the
// secret half — a bare host addresses nothing). An empty string is returned as
// such, because clearing the webhook is not an invalid URL.
func NormalizeWebhookURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ErrInvalidWebhookURL
	}
	// Host carries the port, so a value that dresses another endpoint up as Slack
	// ("hooks.slack.com:8080") does not match. Credentials in the URL are refused
	// rather than sent: a real webhook has none, so their only use here is to make a
	// different host read as Slack at a glance. The comparison folds case because DNS
	// does: a capitalised paste is the same webhook, and refusing it would read as
	// contradicting the value the admin is looking at — the error cannot quote it to
	// show the difference.
	if parsed.Scheme != webhookScheme || !strings.EqualFold(parsed.Host, webhookHost) || parsed.User != nil {
		return "", ErrInvalidWebhookURL
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return "", ErrInvalidWebhookURL
	}
	// Store the host as Slack writes it, so what is kept (and posted to) is one shape
	// whatever was pasted. Only the host is rewritten; the path is the secret and is
	// left exactly as given.
	parsed.Host = webhookHost
	return parsed.String(), nil
}
