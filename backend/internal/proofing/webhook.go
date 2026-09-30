package proofing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Webhook events a customer can subscribe to. EventTest is sent on request and
// reaches the endpoint whatever it subscribed to.
const (
	EventSessionVerified = "session.verified"
	EventSessionFailed   = "session.failed"
	// EventSessionReviewOpened is sent when a session goes to manual review;
	// its decision then sends verified or failed.
	EventSessionReviewOpened = "session.review_opened"
	EventSessionExpired      = "session.expired"
	EventSessionPurged       = "session.purged"
	EventTest                = "test"
)

// WebhookEvents are the events an endpoint may subscribe to, in display order.
var WebhookEvents = []string{
	EventSessionVerified, EventSessionFailed, EventSessionReviewOpened, EventSessionExpired, EventSessionPurged,
}

// outcomeEvents is the webhook event each recorded outcome sends.
var outcomeEvents = map[Status]string{
	StatusApproved:    EventSessionVerified,
	StatusRejected:    EventSessionFailed,
	StatusNeedsReview: EventSessionReviewOpened,
}

// Delivery states.
const (
	DeliveryPending   = "pending"
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
)

// webhookRetryDelays is the wait after each failed attempt: eight attempts
// spread over about 24 hours. A delivery that fails its last attempt is failed.
var webhookRetryDelays = []time.Duration{
	time.Minute, 5 * time.Minute, 30 * time.Minute, time.Hour,
	2 * time.Hour, 6 * time.Hour, 14 * time.Hour,
}

// MaxWebhookAttempts is how often a delivery is tried before it is failed.
var MaxWebhookAttempts = len(webhookRetryDelays) + 1

// nextAttemptDelay is the wait after the attempts-th failed attempt, and false
// once none is left.
func nextAttemptDelay(attempts int) (time.Duration, bool) {
	if attempts < 1 || attempts > len(webhookRetryDelays) {
		return 0, false
	}
	return webhookRetryDelays[attempts-1], true
}

const (
	// webhookSecretPrefix starts every signing secret, so it is recognisable.
	webhookSecretPrefix = "whsec_"
	webhookSecretBytes  = 32
	// secretShownSuffix is how many trailing characters of a secret stay
	// visible after it was shown once.
	secretShownSuffix = 4
)

func newWebhookSecret() string {
	b := make([]byte, webhookSecretBytes)
	_, _ = rand.Read(b)
	return webhookSecretPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func secretLast4(secret string) string {
	return secret[len(secret)-secretShownSuffix:]
}

// SignatureHeader carries a delivery's signature: "t=<unix seconds>,v1=<hex
// HMAC-SHA256 of "<t>.<body>" under the endpoint's secret>". The timestamp
// lets a receiver refuse replays; the signed body is the exact bytes sent.
const SignatureHeader = "Yivi-Signature"

func webhookSignature(secret string, at time.Time, body []byte) string {
	t := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + t + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookMaxSkew is how far a received delivery's timestamp may be from now.
const webhookMaxSkew = 5 * time.Minute

// verifyWebhookSignature checks a SignatureHeader value against body under
// secret, as a receiver does: a timestamp within webhookMaxSkew and a matching
// v1 HMAC.
func verifyWebhookSignature(secret, header string, body []byte, now time.Time) error {
	var ts, v1 string
	for part := range strings.SplitSeq(header, ",") {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "t":
			ts = value
		case "v1":
			v1 = value
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	at := time.Unix(unix, 0)
	if skew := now.Sub(at); skew > webhookMaxSkew || skew < -webhookMaxSkew {
		return ErrBadSignature
	}
	want := webhookSignature(secret, at, body)
	if !hmac.Equal([]byte(want), []byte("t="+ts+",v1="+v1)) {
		return ErrBadSignature
	}
	return nil
}

// webhookBody is what a delivery POSTs: the same bytes on every attempt, so a
// receiver can drop a duplicate by id.
func webhookBody(id uuid.UUID, event string, createdAt time.Time, data json.RawMessage) ([]byte, error) {
	return json.Marshal(struct {
		ID        uuid.UUID       `json:"id"`
		Type      string          `json:"type"`
		CreatedAt time.Time       `json:"createdAt"`
		Data      json.RawMessage `json:"data"`
	}{id, event, createdAt.UTC(), data})
}

// sessionEventData is a session event's data: what happened to which session,
// never who it is about.
func sessionEventData(req Request, status Status) map[string]any {
	data := map[string]any{
		"sessionId": PublicSessionID(req.ID), "status": string(status), "flowId": req.FlowID, "livemode": req.mode() == ModeLive,
	}
	for key, value := range map[string]string{
		"assuranceLevel": req.AssuranceLevel, "eidasLevel": req.EIDASLevel, "errorCode": req.ErrorCode,
	} {
		if value != "" {
			data[key] = value
		}
	}
	return data
}

// enqueueWebhook writes a delivery of event in the caller's transaction, so it
// is sent exactly when the change it reports commits: to the customer's own
// endpoint if it has one subscribed to the event, else to the wallet's default
// endpoint (endpoint_url NULL). A member's request (no customer) sends nothing.
func enqueueWebhook(ctx context.Context, q database.Querier, orgID uuid.UUID, customerID *uuid.UUID,
	event string, requestID *uuid.UUID, data map[string]any,
) error {
	if customerID == nil {
		return nil
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("proofing: marshal webhook %s: %w", event, err)
	}
	var endpointURL *string
	hook, err := getWebhook(ctx, q, orgID, *customerID)
	switch {
	case errors.Is(err, ErrWebhookNotFound):
		if event == EventTest {
			return nil
		}
	case err != nil:
		return err
	case event != EventTest && !slices.Contains(hook.Events, event):
		return nil
	default:
		endpointURL = &hook.URL
	}
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_webhook_deliveries
			(organization_id, customer_id, event, request_id, payload, endpoint_url)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		orgID, *customerID, event, requestID, payload, endpointURL); err != nil {
		return fmt.Errorf("proofing: enqueue webhook %s customer %s: %w", event, *customerID, err)
	}
	// Wakes the deliverer as soon as the change commits.
	return database.Notify(ctx, q, WebhookChannel)
}

// WebhookChannel is the Postgres NOTIFY channel a queued delivery signals.
const WebhookChannel = "identity_proofing_webhooks"
