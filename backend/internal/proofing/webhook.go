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
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Webhook events a customer can subscribe to. EventTest is sent on request and
// reaches the endpoint whatever it subscribed to.
const (
	// EventSessionCreated is sent when a request's engine session is created: the
	// subject can now open it in their app.
	EventSessionCreated = "session.created"
	// EventSessionStarted is sent when the subject's app joins the session.
	EventSessionStarted = "session.started"
	// EventSessionHandover is sent when a running Idem session is handed to
	// another phone, once the one holding it left.
	EventSessionHandover = "session.handover"
	EventSessionVerified = "session.verified"
	EventSessionFailed   = "session.failed"
	// EventSessionReviewOpened is sent when a session goes to manual review;
	// its decision then sends verified or failed.
	EventSessionReviewOpened = "session.review_opened"
	EventSessionExpired      = "session.expired"
	EventSessionCancelled    = "session.cancelled"
	EventSessionPurged       = "session.purged"
	// EventSessionDiplomaAdded is sent for each DUO diploma extract the
	// subject added after their identity was approved.
	EventSessionDiplomaAdded = "session.diploma_added"
	EventTest                = "test"
)

// WebhookEvents are the events an endpoint may subscribe to, in display order.
var WebhookEvents = []string{
	EventSessionCreated, EventSessionStarted, EventSessionHandover,
	EventSessionVerified, EventSessionFailed, EventSessionReviewOpened, EventSessionExpired,
	EventSessionCancelled, EventSessionPurged, EventSessionDiplomaAdded,
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
// HMAC-SHA256 of "<t>.<body>" under the endpoint's secret>". Right after a
// secret rotation it carries a v1 for the new and the previous secret, so a
// receiver still on the previous one keeps verifying (webhookSecretOverlap).
// The timestamp lets a receiver refuse replays; the signed body is the exact
// bytes sent.
const SignatureHeader = "Yivi-Signature"

// webhookSignature signs body at at under each of secrets, in order.
func webhookSignature(secrets []string, at time.Time, body []byte) string {
	t := strconv.FormatInt(at.Unix(), 10)
	header := "t=" + t
	for _, secret := range secrets {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(t))
		mac.Write([]byte("."))
		mac.Write(body)
		header += ",v1=" + hex.EncodeToString(mac.Sum(nil))
	}
	return header
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
		"sessionId": publicSessionID(req.ID), "status": string(status), "flowId": req.FlowID,
	}
	for key, value := range map[string]string{
		"assuranceLevel": req.AssuranceLevel, "eidasLevel": req.EIDASLevel, "errorCode": req.ErrorCode,
		"method": string(req.Method),
	} {
		if value != "" {
			data[key] = value
		}
	}
	if req.ExpectsSubject {
		data["expectedSubject"] = true
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
	// A customer without an endpoint is sent nothing: its results are read
	// through the API or the app.
	hook, err := getWebhook(ctx, q, orgID, *customerID)
	switch {
	case errors.Is(err, ErrWebhookNotFound):
		return nil
	case err != nil:
		return err
	case event != EventTest && !slices.Contains(hook.Events, event):
		return nil
	}
	if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_webhook_deliveries
			(organization_id, customer_id, event, request_id, payload, endpoint_url)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		orgID, *customerID, event, requestID, payload, hook.URL); err != nil {
		return fmt.Errorf("proofing: enqueue webhook %s customer %s: %w", event, *customerID, err)
	}
	// Wakes the deliverer as soon as the change commits.
	return database.Notify(ctx, q, WebhookChannel)
}

// WebhookChannel is the Postgres NOTIFY channel a queued delivery signals.
const WebhookChannel = "identity_proofing_webhooks"
