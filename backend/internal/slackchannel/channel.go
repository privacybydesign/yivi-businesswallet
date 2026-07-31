package slackchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

const (
	// auditLogPath is the app route the posted link opens, under the org's slug. The
	// full record of the event lives there, behind the same access control as every
	// other org page — the message itself repeats only what Summarize allows.
	auditLogPath = "/audit-log"
	// postTimeout bounds one webhook POST end to end. It sits under the dispatcher's
	// notify timeout on purpose: the dispatcher abandons a channel that has not
	// returned by then and leaks the goroutine until it does, so this channel owns a
	// deadline of its own rather than relying on being walked away from.
	postTimeout = 10 * time.Second
	// maxReasonBytes bounds how much of Slack's refusal is read back. Slack answers a
	// rejected post with a short token ("no_service", "invalid_payload"); this is a
	// backstop against a proxy answering with an HTML error page instead.
	maxReasonBytes = 200
	// redactedWebhook stands in for the URL in a reason that quoted it back.
	redactedWebhook = "[webhook url]"
	contentTypeJSON = "application/json"
)

// webhookStore resolves the URL to post to (implemented by *Store).
type webhookStore interface {
	webhookFor(ctx context.Context, orgID uuid.UUID) (string, error)
}

// orgDirectory resolves what to call the organization and where its audit log is
// (implemented by *organization.Store).
type orgDirectory interface {
	GetByID(ctx context.Context, id uuid.UUID) (organization.Organization, error)
}

// Channel delivers notifications to an organization's Slack incoming webhook.
// Register it on the dispatcher at startup; a deployment that leaves it out keeps
// orgs' saved Slack preferences and simply does not deliver them (see
// notifications.Dispatcher).
type Channel struct {
	store      webhookStore
	orgs       orgDirectory
	appBaseURL string
	locale     email.Locale
	client     *http.Client
	// unconfigured remembers the orgs already warned about a missing webhook, so a
	// misconfiguration costs one log line per org instead of one per event: a dispatch
	// pass claims up to notifications.DefaultClaimBatch events, and the same org's
	// whole batch would repeat the line every tick, per replica. A channel sees no
	// pass boundary, so the memo lives as long as the process.
	unconfigured sync.Map
}

// New builds the channel. appBaseURL is the deployment's frontend base URL the
// posted audit-log link is built on (config.Load has already checked it is an
// absolute http(s) URL); locale is the deployment's notification language, the
// same one the mail channel renders in.
func New(store webhookStore, orgs orgDirectory, appBaseURL string, locale email.Locale) *Channel {
	return &Channel{
		store:      store,
		orgs:       orgs,
		appBaseURL: strings.TrimRight(appBaseURL, "/"),
		locale:     locale,
		client: &http.Client{
			Timeout: postTimeout,
			// Slack answers a webhook post itself. A redirect could only take the request
			// — and the organization's event metadata in its body — to a host other than
			// the one NormalizeWebhookURL pinned, so the 3xx is handed back as the
			// response and reported as a refusal.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Channel) ID() notifications.ChannelID { return notifications.ChannelSlack }

// Notify posts one event to the organization's webhook. An org that subscribed to
// Slack without configuring (or after disabling) a webhook is a misconfiguration
// to warn about once, not an error to log per event.
func (c *Channel) Notify(ctx context.Context, e notifications.Event) error {
	webhook, err := c.store.webhookFor(ctx, e.OrgID)
	switch {
	case errors.Is(err, ErrNotConfigured):
		c.warnUnconfigured(ctx, e.OrgID)
		return nil
	case err != nil:
		return err
	}

	org, err := c.orgs.GetByID(ctx, e.OrgID)
	if err != nil {
		return fmt.Errorf("slackchannel: organization %s: %w", e.OrgID, err)
	}
	msg, err := eventMessage(e, org.Name, c.auditURL(org.Slug), c.locale)
	if err != nil {
		return err
	}
	return c.post(ctx, webhook, msg)
}

// SendTest posts a specimen message so an admin can confirm the webhook works
// before relying on it. ErrNotConfigured (nothing to post to) and *DeliveryError
// (Slack refused it) are both answers the admin can act on, so they are returned
// for the handler to show rather than logged here.
func (c *Channel) SendTest(ctx context.Context, orgID uuid.UUID, orgName string) error {
	webhook, err := c.store.webhookFor(ctx, orgID)
	if err != nil {
		return err
	}
	return c.post(ctx, webhook, testMessage(orgName))
}

// post sends one message to the webhook. Every failure comes back as a
// *DeliveryError whose Reason names what went wrong without the URL: net/http
// reports a failed request as `Post "<url>": ...`, and that URL is the workspace's
// secret, while this reason reaches both a log line and the admin's screen.
func (c *Channel) post(ctx context.Context, webhook string, m message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("slackchannel: encode message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return &DeliveryError{Reason: "the stored webhook url is not a usable request target"}
	}
	req.Header.Set("Content-Type", contentTypeJSON)

	resp, err := c.client.Do(req)
	if err != nil {
		return &DeliveryError{Reason: redact(transportReason(err), webhook)}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the answer either way: an accepted post answers "ok", and draining the
	// body is what lets the connection be reused.
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxReasonBytes))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &DeliveryError{Reason: redact(oneLine(resp.Status+": "+string(answer)), webhook)}
	}
	return nil
}

func (c *Channel) auditURL(slug string) string {
	return c.appBaseURL + "/" + slug + auditLogPath
}

// warnUnconfigured reports an org subscribed to Slack without a usable webhook,
// once per org (see Channel.unconfigured). The line names no event: what the admin
// has to fix is the same whichever event ran into it.
func (c *Channel) warnUnconfigured(ctx context.Context, orgID uuid.UUID) {
	if _, warned := c.unconfigured.LoadOrStore(orgID, struct{}{}); warned {
		return
	}
	slog.WarnContext(ctx, "notifications: slack is subscribed but no webhook is configured",
		slog.String("organizationId", orgID.String()))
}

// transportReason states why a post never got an answer, without the URL net/http
// wraps its errors in: a *url.Error carries the request URL verbatim.
func transportReason(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return oneLine(urlErr.Err.Error())
	}
	return oneLine(err.Error())
}

// redact keeps the webhook out of a reason about to be logged or shown. Slack's
// own refusals never quote the URL, but the answer is not always Slack's: a proxy
// in front of it may put the request URL in an error page of its own.
func redact(reason, webhook string) string {
	reason = strings.ReplaceAll(reason, webhook, redactedWebhook)
	// The path is the secret half, and an answer may quote the URL in a shape of its
	// own (a rewritten scheme, an added query), so it is removed on its own too.
	if parsed, err := url.Parse(webhook); err == nil {
		if path := strings.Trim(parsed.Path, "/"); path != "" {
			reason = strings.ReplaceAll(reason, path, redactedWebhook)
		}
	}
	return reason
}

// oneLine collapses whitespace, so a refusal stays one line of a log or an error.
func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
