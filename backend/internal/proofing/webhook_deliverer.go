package proofing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

const (
	// deliveryBatch is how many deliveries one worker round sends at most.
	deliveryBatch = 50
	// responseDrainLimit bounds how much of a receiver's answer is read: only
	// its status matters.
	responseDrainLimit = 4 << 10
)

// Webhook request headers besides SignatureHeader: the event type and the
// delivery id a receiver deduplicates on.
const (
	EventHeader    = "Yivi-Event"
	DeliveryHeader = "Yivi-Delivery"
)

// Deliverer sends due webhook deliveries: to a customer's endpoint through the
// SSRF-guarded client, to the default endpoint through a client that may reach
// it on a private address, since the deployment configured it, not a customer.
type Deliverer struct {
	store         *WebhookStore
	policy        safehttp.Policy
	client        *http.Client
	defaultPolicy safehttp.Policy
	defaultClient *http.Client
	now           func() time.Time
}

// NewDeliverer builds the delivery worker. The zero policy is production:
// https endpoints at public addresses only.
func NewDeliverer(store *WebhookStore, policy safehttp.Policy) *Deliverer {
	trusted := safehttp.Policy{AllowInsecureHTTP: true}
	return &Deliverer{
		store: store, policy: policy, client: safehttp.NewClient(policy),
		defaultPolicy: trusted, defaultClient: safehttp.NewClient(trusted), now: time.Now,
	}
}

// DeliverDue sends the deliveries that are due and records each outcome; it
// reports how many it attempted. One endpoint failing does not stop the round.
func (d *Deliverer) DeliverDue(ctx context.Context) (int64, error) {
	due, err := d.store.claimDue(ctx, deliveryBatch)
	if err != nil {
		return 0, err
	}
	for _, delivery := range due {
		code, sendErr := d.send(ctx, delivery)
		msg := ""
		if sendErr != nil {
			msg = sendErr.Error()
		}
		if err := d.store.recordAttempt(ctx, delivery, code, msg); err != nil {
			slog.ErrorContext(ctx, "identity proofing: record webhook delivery failed",
				slog.String("delivery_id", delivery.ID.String()), slog.Any("error", err))
		}
	}
	return int64(len(due)), nil
}

// Run sends everything due, then returns when the next delivery (a retry, or
// a lapsed lease) falls due: a database.Job, woken early by WebhookChannel.
func (d *Deliverer) Run(ctx context.Context) (time.Time, error) {
	for {
		n, err := d.DeliverDue(ctx)
		if err != nil {
			return time.Time{}, err
		}
		if n < deliveryBatch {
			break
		}
	}
	return d.store.nextDue(ctx)
}

var errNon2xx = errors.New("endpoint answered with a non-2xx status")

// send POSTs one delivery and returns the status the endpoint answered, if it
// answered. A transport error is stripped of its URL, which a log line must
// not carry.
func (d *Deliverer) send(ctx context.Context, delivery dueDelivery) (*int, error) {
	policy, client := d.policy, d.client
	if delivery.Default {
		policy, client = d.defaultPolicy, d.defaultClient
	}
	if _, err := policy.CheckURL(delivery.URL); err != nil {
		return nil, err
	}
	body, err := webhookBody(delivery.ID, delivery.Event, delivery.CreatedAt, delivery.Payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(EventHeader, delivery.Event)
	req.Header.Set(DeliveryHeader, delivery.ID.String())
	req.Header.Set(SignatureHeader, webhookSignature(delivery.Secret, d.now(), body))
	resp, err := client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("%s: %w", uerr.Op, uerr.Err)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, responseDrainLimit))
	code := resp.StatusCode
	if code/100 != 2 {
		return &code, fmt.Errorf("%w: %d", errNon2xx, code)
	}
	return &code, nil
}
