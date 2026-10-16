package proofing

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	// maxListedDeliveries is how many recent deliveries the endpoint shows.
	maxListedDeliveries = 20
	// deliveryLease is how long a claimed delivery is hidden from other
	// workers while one sends it; a worker that dies mid-send frees it then.
	deliveryLease = 5 * time.Minute
	// maxDeliveryError bounds the error kept on a delivery.
	maxDeliveryError = 300
	// DeliveryRetention is how long a delivered or failed delivery is kept, for
	// the recent deliveries and endpoint health, before PruneDeliveries drops it.
	DeliveryRetention = 30 * 24 * time.Hour
	// defaultSecretPurpose derives the default endpoint's signing secret from
	// the proofing key, so every replica signs and verifies alike.
	defaultSecretPurpose = "identity-proofing default webhook"
)

// Webhook is an org's endpoint, without its secret.
type Webhook struct {
	URL         string
	Events      []string
	SecretLast4 string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// WebhookHealth is how an org's endpoint has been answering. State is
// "not_configured", "delivering" (its last attempt succeeded, or none was made
// yet) or "failing" (its last attempt failed); FailingSince is the first
// attempt of the failing streak, PendingRetries the deliveries waiting for
// another attempt.
type WebhookHealth struct {
	State          string
	LastStatusCode *int
	FailingSince   *time.Time
	PendingRetries int
}

// Webhook health states.
const (
	WebhookNotConfigured = "not_configured"
	WebhookDelivering    = "delivering"
	WebhookFailing       = "failing"
)

// Delivery is one webhook event sent, or to be sent, to an org's endpoint.
// EndpointURL is the org's own endpoint; "" is the wallet's default one.
type Delivery struct {
	ID             uuid.UUID
	Event          string
	RequestID      *uuid.UUID
	EndpointURL    string
	Status         string
	Attempts       int
	LastStatusCode *int
	LastError      string
	LastAttemptAt  *time.Time
	DeliveredAt    *time.Time
	CreatedAt      time.Time
}

// dueDelivery is a claimed delivery with what sending it needs. Default is a
// delivery to the wallet's own endpoint, not one an org supplied.
type dueDelivery struct {
	ID        uuid.UUID
	Event     string
	Payload   json.RawMessage
	Attempts  int
	CreatedAt time.Time
	URL       string
	Secret    string
	Default   bool
}

// WebhookStore persists the orgs' webhook endpoints and their delivery outbox.
// Configuring an endpoint is audited; deliveries are not (they are the
// consequence of an audited change).
type WebhookStore struct {
	db         database.DB
	audit      audit.Recorder
	cipher     *crypto.Cipher
	defaultURL string
}

func NewWebhookStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *WebhookStore {
	return &WebhookStore{db: db, audit: recorder, cipher: cipher}
}

// SetDefaultEndpoint sets the wallet's own endpoint, where an org without one
// of its own is sent its events.
func (s *WebhookStore) SetDefaultEndpoint(url string) { s.defaultURL = url }

// DefaultSecret is the default endpoint's signing secret.
func (s *WebhookStore) DefaultSecret() (string, error) {
	if s.cipher == nil {
		return "", ErrNoEncryptionKey
	}
	secret, err := s.cipher.DeriveSecret(defaultSecretPurpose)
	if err != nil {
		return "", err
	}
	return webhookSecretPrefix + hex.EncodeToString(secret), nil
}

func (w Webhook) auditFields() map[string]any {
	return map[string]any{"url": w.URL, "events": w.Events}
}

// Get returns the org's endpoint, or ErrWebhookNotFound.
func (s *WebhookStore) Get(ctx context.Context, orgID uuid.UUID) (Webhook, error) {
	return getWebhook(ctx, s.db, orgID)
}

func getWebhook(ctx context.Context, q database.Querier, orgID uuid.UUID) (Webhook, error) {
	var w Webhook
	err := q.QueryRow(ctx, `SELECT url, events, secret_last4, created_at, updated_at
		FROM identity_proofing_webhooks WHERE organization_id = $1`, orgID).
		Scan(&w.URL, &w.Events, &w.SecretLast4, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Webhook{}, ErrWebhookNotFound
	}
	if err != nil {
		return Webhook{}, fmt.Errorf("proofing: read webhook org %s: %w", orgID, err)
	}
	return w, nil
}

// Save sets the org's endpoint URL and events. A new endpoint gets a fresh
// signing secret, returned the one time it is readable; an existing one keeps
// its secret and returns "". Audited identity_proofing.webhook_configured with
// before and after.
func (s *WebhookStore) Save(ctx context.Context, orgID uuid.UUID, url string, events []string) (Webhook, string, error) {
	if s.cipher == nil {
		return Webhook{}, "", ErrNoEncryptionKey
	}
	var secret string
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getWebhook(ctx, q, orgID)
		switch {
		case errors.Is(err, ErrWebhookNotFound):
			secret = newWebhookSecret()
			sealed, err := s.cipher.Encrypt([]byte(secret))
			if err != nil {
				return fmt.Errorf("proofing: seal webhook secret org %s: %w", orgID, err)
			}
			if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_webhooks
				(organization_id, url, events, secret_ciphertext, secret_last4)
				VALUES ($1, $2, $3, $4, $5)`, orgID, url, events, sealed, secretLast4(secret)); err != nil {
				return fmt.Errorf("proofing: create webhook org %s: %w", orgID, err)
			}
			return s.audit.Record(ctx, q, audit.IdentityProofingWebhookConfigured,
				audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
				audit.Created(Webhook{URL: url, Events: events}.auditFields()))
		case err != nil:
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_webhooks SET url = $2, events = $3, updated_at = now()
			WHERE organization_id = $1`, orgID, url, events); err != nil {
			return fmt.Errorf("proofing: update webhook org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookConfigured,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(before.auditFields(), Webhook{URL: url, Events: events}.auditFields()))
	})
	if err != nil {
		return Webhook{}, "", err
	}
	w, err := s.Get(ctx, orgID)
	return w, secret, err
}

// RotateSecret replaces an endpoint's signing secret and returns the new one,
// the one time it is readable. Deliveries still waiting are signed with it.
// Audited identity_proofing.webhook_secret_rotated.
func (s *WebhookStore) RotateSecret(ctx context.Context, orgID uuid.UUID) (Webhook, string, error) {
	if s.cipher == nil {
		return Webhook{}, "", ErrNoEncryptionKey
	}
	secret := newWebhookSecret()
	sealed, err := s.cipher.Encrypt([]byte(secret))
	if err != nil {
		return Webhook{}, "", fmt.Errorf("proofing: seal webhook secret org %s: %w", orgID, err)
	}
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getWebhook(ctx, q, orgID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_webhooks
			SET secret_ciphertext = $2, secret_last4 = $3, updated_at = now()
			WHERE organization_id = $1`, orgID, sealed, secretLast4(secret)); err != nil {
			return fmt.Errorf("proofing: rotate webhook secret org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookSecretRotated,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"secretLast4": before.SecretLast4}, map[string]any{"secretLast4": secretLast4(secret)}))
	})
	if err != nil {
		return Webhook{}, "", err
	}
	w, err := s.Get(ctx, orgID)
	return w, secret, err
}

// Remove deletes the org's endpoint and every delivery still waiting for it;
// ones queued for the default endpoint stay. Audited identity_proofing.webhook_removed.
func (s *WebhookStore) Remove(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getWebhook(ctx, q, orgID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_webhook_deliveries
			WHERE organization_id = $1 AND status = $2 AND endpoint_url IS NOT NULL`,
			orgID, DeliveryPending); err != nil {
			return fmt.Errorf("proofing: drop pending deliveries org %s: %w", orgID, err)
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_webhooks
			WHERE organization_id = $1`, orgID); err != nil {
			return fmt.Errorf("proofing: remove webhook org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookRemoved,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Deleted(before.auditFields()))
	})
}

// SendTest queues a test event for the org's endpoint.
func (s *WebhookStore) SendTest(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := getWebhook(ctx, q, orgID); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, orgID, EventTest, nil, map[string]any{"message": "test event"})
	})
}

// Deliveries returns the org's most recent deliveries, newest first.
func (s *WebhookStore) Deliveries(ctx context.Context, orgID uuid.UUID) ([]Delivery, error) {
	rows, err := s.db.Query(ctx, `SELECT id, event, request_id, COALESCE(endpoint_url, ''), status, attempts, last_status_code,
			COALESCE(last_error, ''), last_attempt_at, delivered_at, created_at
		FROM identity_proofing_webhook_deliveries
		WHERE organization_id = $1
		ORDER BY created_at DESC, id LIMIT $2`, orgID, maxListedDeliveries)
	if err != nil {
		return nil, fmt.Errorf("proofing: list deliveries org %s: %w", orgID, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := row.Scan(&d.ID, &d.Event, &d.RequestID, &d.EndpointURL, &d.Status, &d.Attempts, &d.LastStatusCode,
			&d.LastError, &d.LastAttemptAt, &d.DeliveredAt, &d.CreatedAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("proofing: list deliveries org %s: %w", orgID, err)
	}
	return out, nil
}

// Health reports how the org's endpoint has been answering; an org without an
// endpoint is WebhookNotConfigured.
func (s *WebhookStore) Health(ctx context.Context, orgID uuid.UUID) (WebhookHealth, error) {
	// The last attempt decides the state. The failing streak runs back from it
	// to the first attempt after the last success. Only the org's own endpoint
	// counts: the default one is the wallet's.
	var lastStatus *string
	h := WebhookHealth{State: WebhookNotConfigured}
	err := s.db.QueryRow(ctx, `SELECT last.status, last.last_status_code,
			(SELECT min(d.last_attempt_at) FROM identity_proofing_webhook_deliveries d
			 WHERE d.organization_id = w.organization_id AND d.endpoint_url IS NOT NULL AND d.status <> $2 AND d.last_attempt_at IS NOT NULL
				AND d.last_attempt_at > COALESCE((SELECT max(ok.delivered_at) FROM identity_proofing_webhook_deliveries ok
					WHERE ok.organization_id = w.organization_id AND ok.status = $2 AND ok.endpoint_url IS NOT NULL), '-infinity')),
			(SELECT count(*) FROM identity_proofing_webhook_deliveries d
			 WHERE d.organization_id = w.organization_id AND d.endpoint_url IS NOT NULL AND d.status = $3 AND d.attempts > 0)
		FROM identity_proofing_webhooks w
		LEFT JOIN LATERAL (
			SELECT d.status, d.last_status_code FROM identity_proofing_webhook_deliveries d
			WHERE d.organization_id = w.organization_id AND d.endpoint_url IS NOT NULL AND d.last_attempt_at IS NOT NULL
			ORDER BY d.last_attempt_at DESC LIMIT 1
		) last ON true
		WHERE w.organization_id = $1`, orgID, DeliveryDelivered, DeliveryPending).
		Scan(&lastStatus, &h.LastStatusCode, &h.FailingSince, &h.PendingRetries)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, nil
	}
	if err != nil {
		return WebhookHealth{}, fmt.Errorf("proofing: webhook health org %s: %w", orgID, err)
	}
	h.State = WebhookDelivering
	if lastStatus != nil && *lastStatus != DeliveryDelivered {
		h.State = WebhookFailing
	} else {
		h.FailingSince = nil
	}
	return h, nil
}

// claimDue leases up to limit due deliveries, across every org, and returns
// them with their endpoint's URL and secret: the org's own, or the
// default one's for a delivery without one. The lease keeps another worker
// from sending the same delivery while this one does.
func (s *WebhookStore) claimDue(ctx context.Context, limit int) ([]dueDelivery, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	defaultSecret, err := s.DefaultSecret()
	if err != nil {
		return nil, err
	}

	// A delivery for an org endpoint that is gone is never sent to the default.
	rows, err := s.db.Query(ctx, `WITH due AS (
			SELECT d.id, w.url, w.secret_ciphertext FROM identity_proofing_webhook_deliveries d
			LEFT JOIN identity_proofing_webhooks w ON w.organization_id = d.organization_id
			WHERE d.status = $1 AND d.next_attempt_at <= now()
				AND (d.endpoint_url IS NULL OR w.organization_id IS NOT NULL)
			ORDER BY d.next_attempt_at LIMIT $2 FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE identity_proofing_webhook_deliveries d
		SET next_attempt_at = now() + make_interval(secs => $3),
			endpoint_url = CASE WHEN d.endpoint_url IS NULL THEN NULL ELSE due.url END
		FROM due
		WHERE d.id = due.id
		RETURNING d.id, d.event, d.payload, d.attempts, d.created_at, d.endpoint_url IS NULL, due.url, due.secret_ciphertext`,
		DeliveryPending, limit, deliveryLease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("proofing: claim deliveries: %w", err)
	}

	defer rows.Close()
	var out []dueDelivery
	for rows.Next() {
		var d dueDelivery
		var url *string
		var sealed []byte
		if err := rows.Scan(&d.ID, &d.Event, &d.Payload, &d.Attempts, &d.CreatedAt, &d.Default, &url, &sealed); err != nil {
			return nil, fmt.Errorf("proofing: scan delivery: %w", err)
		}
		if d.Default {
			d.URL, d.Secret = s.defaultURL, defaultSecret
			out = append(out, d)
			continue
		}
		secret, err := s.cipher.Decrypt(sealed)
		if err != nil {
			// Skip only this one; its lease lapses and it is retried, e.g. after a key fix.
			slog.ErrorContext(ctx, "identity proofing: cannot open webhook secret",
				slog.String("delivery_id", d.ID.String()), slog.Any("error", err))
			continue
		}
		d.URL, d.Secret = *url, string(secret)
		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: claim deliveries: %w", err)
	}

	return out, nil
}

// nextDue is when the earliest pending delivery falls due; zero when none is.
func (s *WebhookStore) nextDue(ctx context.Context) (time.Time, error) {
	var next *time.Time
	// Joined like claimDue: a delivery it cannot claim must not wake the job at once, forever.
	if err := s.db.QueryRow(ctx, `SELECT min(d.next_attempt_at) FROM identity_proofing_webhook_deliveries d
		LEFT JOIN identity_proofing_webhooks w ON w.organization_id = d.organization_id
		WHERE d.status = $1 AND (d.endpoint_url IS NULL OR w.organization_id IS NOT NULL)`, DeliveryPending).Scan(&next); err != nil {
		return time.Time{}, fmt.Errorf("proofing: next delivery: %w", err)
	}
	if next == nil {
		return time.Time{}, nil
	}
	return *next, nil
}

// recordAttempt stores one attempt's outcome: delivered on a 2xx, else another
// attempt after the back-off, or failed once the attempts run out.
func (s *WebhookStore) recordAttempt(ctx context.Context, d dueDelivery, statusCode *int, sendErr string) error {
	attempts := d.Attempts + 1
	status, next := DeliveryDelivered, time.Now()
	var deliveredAt *time.Time
	if sendErr == "" {
		deliveredAt = &next
	} else if delay, ok := nextAttemptDelay(attempts); ok {
		status, next = DeliveryPending, next.Add(delay)
	} else {
		status = DeliveryFailed
	}
	if len(sendErr) > maxDeliveryError {
		sendErr = sendErr[:maxDeliveryError]
	}
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries
		SET status = $2, attempts = $3, next_attempt_at = $4, last_attempt_at = now(),
			last_status_code = $5, last_error = NULLIF($6, ''), delivered_at = $7
		WHERE id = $1`, d.ID, status, attempts, next, statusCode, sendErr, deliveredAt); err != nil {
		return fmt.Errorf("proofing: record delivery %s: %w", d.ID, err)
	}
	return nil
}

// PruneDeliveries drops delivered and failed deliveries older than
// DeliveryRetention; a pending one stays until it is sent or fails.
func (s *WebhookStore) PruneDeliveries(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM identity_proofing_webhook_deliveries
		WHERE status <> $1 AND created_at < now() - $2::interval`, DeliveryPending, DeliveryRetention.String())
	if err != nil {
		return 0, fmt.Errorf("proofing: prune webhook deliveries: %w", err)
	}
	return tag.RowsAffected(), nil
}
