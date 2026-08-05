// Package teamschannel is the Microsoft Teams delivery route of the notification
// layer: an organization stores one Teams webhook URL, and every event the org
// subscribed to the Teams channel for is posted to it as a card.
//
// It is a webhook rather than a Teams app on purpose, the same choice the Slack
// channel made. A webhook is a single value an org admin pastes in — no app
// registration per deployment, no Entra consent flow, no token to refresh — and
// the Teams admin who created it decided there and then which channel it may post
// to, so this side cannot widen the audience.
//
// Three kinds of URL are accepted, because Microsoft is midway through replacing the
// first with the others: an Office 365 connector's incoming webhook (at the tenant's
// own <tenant>.webhook.office.com) and a Power Automate workflow trigger, which is
// what the Teams "Workflows" app hands out — issued either at *.logic.azure.com or,
// newer, at *.environment.api.powerplatform.com. All three take the same payload —
// see message.go.
//
// The webhook URL is the tenant's secret: whoever holds it can post as the
// integration. So it is encrypted at rest under the deployment Teams key, it is
// never returned to the frontend (Settings reports HasWebhook, not the URL), and
// it is kept out of every log line and error — the errors here name the
// organization, and a transport failure is stripped of the URL net/http puts in it
// (see Channel.post).
//
// What is posted is the notification layer's own rendering: the mail catalogue's
// name for the audit action plus notifications.Summarize of its metadata, the same
// lines the e-mail and Slack channels send. The catalog in internal/notifications
// is the gate on which events may reach an outside system at all.
package teamschannel

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// ErrNotConfigured means the organization has no usable (present + enabled) Teams
// webhook, so nothing can be posted for it.
var ErrNotConfigured = errors.New("teamschannel: no teams webhook configured for organization")

// ErrNoEncryptionKey means the deployment has no Teams encryption key, so a
// webhook URL cannot be stored. It is a deployment misconfiguration, not a
// mistake by the admin saving the settings — but they are the one who sees it, so
// the handler answers it with its own code rather than a 500.
var ErrNoEncryptionKey = errors.New("teamschannel: no encryption key configured; cannot store a webhook url")

// ErrInvalidWebhookURL means the URL is not a Microsoft Teams webhook. Its message
// deliberately repeats no part of the input: the value is a secret, and this error
// is rendered into an API response.
var ErrInvalidWebhookURL = errors.New("teamschannel: the webhook url must be an https url at " + WebhookHostsDescription())

const (
	webhookScheme = "https"
	// webhookPort is the only port a webhook URL may name. A Power Automate trigger
	// URL writes https' own default port out in full
	// ("prod-27.westeurope.logic.azure.com:443"), so refusing every port would refuse
	// the paste an admin copied straight out of Teams; any other port would be a way
	// to aim this deployment's request somewhere else on a host that reads as
	// Microsoft's.
	webhookPort = "443"
)

// webhookHostSuffixes is the set of hosts a webhook URL may point at. Posting is
// the one outbound request an org admin gets to aim, so it is pinned at
// Microsoft's own hosts: without that, saved settings would be a request-forgery
// primitive against whatever the backend can reach, with an org's audit metadata
// as the payload.
//
// Both entries are suffixes rather than whole hosts because, unlike Slack's single
// hooks.slack.com, a Teams webhook lives on a per-tenant or per-region subdomain.
// Each begins with a dot, so the host must have at least one label of its own in
// front of it and "a.webhook.office.com.example.org" does not match.
//
//   - .webhook.office.com — an Office 365 connector's incoming webhook, at the
//     tenant's own subdomain. Microsoft is retiring connectors, but existing ones
//     are what many orgs still have.
//   - .logic.azure.com — a Power Automate workflow trigger, which is what the Teams
//     "Workflows" app hands out and what Microsoft's migration guidance points at.
//   - .powerplatform.com — the same Power Automate workflow trigger, on the newer
//     per-environment Power Platform API host (e.g.
//     <env>.environment.api.powerplatform.com) that Microsoft has begun issuing these
//     URLs on in place of *.logic.azure.com.
//
// Pinning these says the request goes to Microsoft, not that it goes to the org's
// own tenant: anyone can create a workflow, exactly as anyone can create a Slack
// webhook. That is the same trust boundary the Slack channel draws, and the point
// of it is that an internal address or an attacker's host cannot be aimed at.
// A tenant whose Workflows URL sits on some other Microsoft host reads as invalid
// until it is added here, which costs a paste and lets nothing else through.
var webhookHostSuffixes = []string{
	".webhook.office.com",
	".logic.azure.com",
	".powerplatform.com",
}

// WebhookHostsDescription names the accepted hosts for the copy that has to tell
// an admin which URL to paste. It is exported so the API error and the OpenAPI
// description cannot drift from the list above.
func WebhookHostsDescription() string {
	trimmed := make([]string, 0, len(webhookHostSuffixes))
	for _, suffix := range webhookHostSuffixes {
		trimmed = append(trimmed, strings.TrimPrefix(suffix, "."))
	}
	return strings.Join(trimmed, " or ")
}

// DeliveryError reports a webhook Teams or the network refused. Reason is safe to
// show an admin: it carries the status this side read and, for a failure that never
// got an answer, what went wrong — never the URL, and never a byte the far side
// wrote (see refusalReason).
type DeliveryError struct {
	Reason string
}

func (e *DeliveryError) Error() string {
	return "teamschannel: the webhook was not accepted: " + e.Reason
}

// Settings is the non-secret view of an org's Teams configuration (never the
// webhook URL). Configured is false when no row exists yet.
type Settings struct {
	Configured bool `json:"configured"`
	// HasWebhook reports whether a webhook URL is stored, so the settings screen can
	// show that one is in place without ever receiving it.
	HasWebhook bool       `json:"hasWebhook"`
	Enabled    bool       `json:"enabled"`
	UpdatedAt  *time.Time `json:"updatedAt,omitempty"`
}

// SettingsInput is an upsert of an org's Teams configuration. WebhookURL is
// optional on update, the same shape the SMTP password and the Slack webhook use:
// nil keeps the stored URL, a non-nil empty string clears it, and any other value
// replaces it.
type SettingsInput struct {
	WebhookURL *string
	Enabled    bool
}

// NormalizeWebhookURL trims a submitted webhook URL and checks it is a Teams
// webhook: https, at one of webhookHostSuffixes, with a path (the path is where a
// connector keeps its secret half — a bare host addresses nothing). An empty
// string is returned as such, because clearing the webhook is not an invalid URL.
//
// The query is kept verbatim: a Power Automate trigger URL carries its signature
// there ("...&sig=..."), so it is as much the credential as a connector's path is.
func NormalizeWebhookURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", ErrInvalidWebhookURL
	}
	// Credentials in the URL are refused rather than sent: a real webhook has none, so
	// their only use here is to make a different host read as Microsoft's at a glance.
	if parsed.Scheme != webhookScheme || parsed.User != nil {
		return "", ErrInvalidWebhookURL
	}
	// Hostname() drops the port, so the suffix check cannot be slipped by naming
	// another endpoint's port. The comparison folds case because DNS does: a
	// capitalised paste is the same webhook, and refusing it would read as
	// contradicting the value the admin is looking at — the error cannot quote it to
	// show the difference.
	host := strings.ToLower(parsed.Hostname())
	if !allowedHost(host) {
		return "", ErrInvalidWebhookURL
	}
	if port := parsed.Port(); port != "" && port != webhookPort {
		return "", ErrInvalidWebhookURL
	}
	if strings.Trim(parsed.Path, "/") == "" {
		return "", ErrInvalidWebhookURL
	}
	// Store the host lowercased and without the redundant :443, so what is kept (and
	// posted to) is one shape whatever was pasted. Only the host is rewritten; the
	// path and query are the secret and are left exactly as given.
	parsed.Host = host
	return parsed.String(), nil
}

// allowedHost reports whether host sits under one of the accepted suffixes with a
// label of its own in front of it. The length check is what enforces that label:
// the bare suffix without its leading dot is not one of these hosts, and with the
// dot it is not a host at all.
func allowedHost(host string) bool {
	for _, suffix := range webhookHostSuffixes {
		if len(host) > len(suffix) && strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
