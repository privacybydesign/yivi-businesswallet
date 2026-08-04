package teamschannel

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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/notifications"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
)

const (
	// postTimeout bounds one webhook POST end to end. It sits under the dispatcher's
	// notify timeout on purpose: the dispatcher abandons a channel that has not
	// returned by then and leaks the goroutine until it does, so this channel owns a
	// deadline of its own rather than relying on being walked away from.
	postTimeout = 10 * time.Second
	// maxDrainBytes bounds how much of the answer is read back. The bytes are thrown
	// away — nothing the far side wrote reaches a reason (see refusalReason) — but the
	// body still has to be drained for the connection to be reusable, and an error
	// page from an intermediary can be arbitrarily long.
	maxDrainBytes = 4096
	// redactedWebhook stands in for the URL in a reason that quoted it back.
	redactedWebhook = "[webhook url]"
	// answerNotRepeated is what a refusal says instead of the answer. See
	// refusalReason for why there is nothing to repeat.
	answerNotRepeated = "the answer is not repeated: it can quote the webhook url"
	contentTypeJSON   = "application/json"
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

// Channel delivers notifications to an organization's Microsoft Teams webhook.
// Register it on the dispatcher at startup; a deployment that leaves it out keeps
// orgs' saved Teams preferences and simply does not deliver them (see
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
// same one the mail and Slack channels render in.
func New(store webhookStore, orgs orgDirectory, appBaseURL string, locale email.Locale) *Channel {
	return &Channel{
		store:      store,
		orgs:       orgs,
		appBaseURL: appBaseURL,
		locale:     locale,
		client: &http.Client{
			Timeout: postTimeout,
			// Microsoft answers a webhook post itself. A redirect could only take the
			// request — and the organization's event metadata in its body — to a host other
			// than the one NormalizeWebhookURL pinned, so the 3xx is handed back as the
			// response and reported as a refusal.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Channel) ID() notifications.ChannelID { return notifications.ChannelTeams }

// Notify posts one event to the organization's webhook. An org that subscribed to
// Teams without configuring (or after disabling) a webhook is a misconfiguration
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
		return fmt.Errorf("teamschannel: organization %s: %w", e.OrgID, err)
	}
	msg, err := eventMessage(e, org.Name, notifications.AuditLogURL(c.appBaseURL, org.Slug), c.locale)
	if err != nil {
		return err
	}
	return c.post(ctx, webhook, msg)
}

// SendTest posts a specimen card so an admin can confirm the webhook works before
// relying on it. ErrNotConfigured (nothing to post to) and *DeliveryError (Teams
// refused it) are both answers the admin can act on, so they are returned for the
// handler to show rather than logged here.
func (c *Channel) SendTest(ctx context.Context, orgID uuid.UUID, orgName string) error {
	webhook, err := c.store.webhookFor(ctx, orgID)
	if err != nil {
		return err
	}
	return c.post(ctx, webhook, testMessage(orgName))
}

// post sends one card to the webhook. Every failure comes back as a *DeliveryError
// whose Reason names what went wrong without the URL: net/http reports a failed
// request as `Post "<url>": ...`, and that URL is the tenant's secret, while this
// reason reaches both a log line and the admin's screen.
//
// A 2xx is taken as delivered. An Office 365 connector is known to answer some
// failures 200 with an error sentence in the body rather than a 4xx, so a card the
// connector dropped can still be reported as sent here; that is not detected,
// because the alternative — deciding delivery from bytes the far side wrote — would
// have to treat an unrecognised success body as a failure and report a Power
// Automate workflow's own 202 answers as refusals. The status code is the one
// signal both endpoint kinds use honestly.
func (c *Channel) post(ctx context.Context, webhook string, m message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("teamschannel: encode message: %w", err)
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

	// Drain the answer either way and discard it: reading it is what lets the
	// connection be reused, and none of it is repeated.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &DeliveryError{Reason: refusalReason(resp.StatusCode)}
	}
	return nil
}

// refusalReason says why the post was rejected, from the status code alone.
//
// None of the answer is repeated, and there is no allowlist of answers that would
// be. Slack's incoming-webhook endpoint documents a closed set of short refusal
// tokens ("no_service"), so its channel can repeat one and know where it came from;
// neither a Teams connector nor a Power Automate workflow does. A workflow's
// refusal is a JSON document from Azure, a connector's is an English sentence, and
// what sits between this deployment and Microsoft can answer with anything at all —
// including the request URL it refused, which is the credential. With nothing to
// allowlist, the safe reading is that no byte of the answer is this side's to show.
//
// The status is the numeric code, not resp.Status: the reason phrase after the code
// is also written by the far side. So a reason built here holds nothing anyone else
// chose, which is what leaves redact only the transport error to strip. What the
// admin acts on survives: 404 the webhook is gone, 401/403 revoked, 429 throttled,
// 400 the card was rejected.
func refusalReason(statusCode int) string {
	return "status " + strconv.Itoa(statusCode) + " (" + answerNotRepeated + ")"
}

// warnUnconfigured reports an org subscribed to Teams without a usable webhook,
// once per org (see Channel.unconfigured). The line names no event: what the admin
// has to fix is the same whichever event ran into it.
func (c *Channel) warnUnconfigured(ctx context.Context, orgID uuid.UUID) {
	if _, warned := c.unconfigured.LoadOrStore(orgID, struct{}{}); warned {
		return
	}
	slog.WarnContext(ctx, "notifications: microsoft teams is subscribed but no webhook is configured",
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

// redact keeps the webhook out of a reason about to be logged or shown. A refused
// post repeats no part of the answer and states the status as a number
// (refusalReason), so the only bytes reaching here are the transport failure
// net/http names the URL in — which really does carry it. It still covers the
// quoted shapes of the secret parts, because a reason built here later should not
// have to be safe on its own.
func redact(reason, webhook string) string {
	for _, secret := range secretForms(webhook) {
		reason = strings.ReplaceAll(reason, secret, redactedWebhook)
	}
	return reason
}

// secretForms lists the shapes the webhook has been seen quoted back in, which is
// not the same as all of them ("&#47;" and "%252F" are as writable as the rest) —
// the reason a refusal repeats no answer at all rather than trusting this list.
//
// Both halves of the URL that a webhook keeps its secret in are covered, because
// the two kinds of URL put it in different places: a connector's is the path
// ("/webhookb2/…"), a Power Automate trigger's is the "sig" in the query. Matching
// the stored URL verbatim is not enough, since something that names the request it
// refused writes those in a shape of its own — percent-encoded separators in either
// case, or a JSON document's "\/", neither of which is a substring of the decoded
// form.
func secretForms(webhook string) []string {
	forms := []string{webhook}
	parsed, err := url.Parse(webhook)
	if err != nil {
		return forms
	}
	// The decoded and the escaped path are kept apart by url.Parse and an answer may
	// quote either; for these webhooks they are usually the same string, and a
	// duplicate form only costs a second pass that matches nothing.
	parts := []string{parsed.Path, parsed.EscapedPath(), parsed.RawQuery}
	for _, part := range parts {
		trimmed := strings.Trim(part, "/")
		if trimmed == "" {
			continue
		}
		forms = append(forms, trimmed,
			strings.ReplaceAll(trimmed, "/", "%2F"),
			strings.ReplaceAll(trimmed, "/", "%2f"),
			strings.ReplaceAll(trimmed, "/", `\/`))
	}
	// Longest first, so a form contained in a longer one does not blank out the part
	// that would have matched the longer one and leave the rest of it standing.
	sort.SliceStable(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	return forms
}

// oneLine collapses whitespace, so a refusal stays one line of a log or an error.
func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
