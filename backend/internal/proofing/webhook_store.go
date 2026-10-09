package proofing

import (
	"context"
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
	// maxListedDeliveries is how many recent deliveries the Webhooks tab shows.
	maxListedDeliveries = 20
	// deliveryLease is how long a claimed delivery is hidden from other
	// workers while one sends it; a worker that dies mid-send frees it then.
	deliveryLease = 5 * time.Minute
	// maxDeliveryError bounds the error kept on a delivery.
	maxDeliveryError = 300
	// DeliveryRetention is how long a delivered or failed delivery is kept, for
	// the Webhooks tab and endpoint health, before PruneDeliveries drops it.
	DeliveryRetention = 30 * 24 * time.Hour
	// webhookSecretOverlap is how long a rotated-out secret still signs
	// deliveries next to the new one, so a receiver can switch without
	// failing the deliveries meanwhile.
	webhookSecretOverlap = 24 * time.Hour
)

// Webhook is a customer's endpoint, without its secret.
type Webhook struct {
	CustomerID  uuid.UUID
	URL         string
	Events      []string
	SecretLast4 string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// WebhookHealth is how a customer's endpoint has been answering. State is
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

// Delivery is one webhook event sent, or to be sent, to a customer's endpoint.
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

// dueDelivery is a claimed delivery with what sending it needs: the secrets
// it is signed with (the current one, then a rotated-out one still in its
// overlap) and the lease it was claimed under, which recordAttempt checks.
type dueDelivery struct {
	ID          uuid.UUID
	Event       string
	Payload     json.RawMessage
	Attempts    int
	CreatedAt   time.Time
	URL         string
	Secrets     []string
	LeasedUntil time.Time
}

// WebhookStore persists customer webhook endpoints and their delivery outbox.
// Configuring an endpoint is audited; deliveries are not (they are the
// consequence of an audited change).
type WebhookStore struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewWebhookStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *WebhookStore {
	return &WebhookStore{db: db, audit: recorder, cipher: cipher}
}

func (w Webhook) auditFields() map[string]any {
	return map[string]any{"url": w.URL, "events": w.Events}
}

// Get returns a customer's endpoint, or ErrWebhookNotFound.
func (s *WebhookStore) Get(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, error) {
	return getWebhook(ctx, s.db, orgID, customerID)
}

func getWebhook(ctx context.Context, q database.Querier, orgID, customerID uuid.UUID) (Webhook, error) {
	var w Webhook
	err := q.QueryRow(ctx, `SELECT customer_id, url, events, secret_last4, created_at, updated_at
		FROM identity_proofing_webhooks WHERE organization_id = $1 AND customer_id = $2`, orgID, customerID).
		Scan(&w.CustomerID, &w.URL, &w.Events, &w.SecretLast4, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Webhook{}, ErrWebhookNotFound
	}
	if err != nil {
		return Webhook{}, fmt.Errorf("proofing: read webhook customer %s: %w", customerID, err)
	}
	return w, nil
}

// Save sets a customer's endpoint URL and events. A new endpoint gets a fresh
// signing secret, returned the one time it is readable; an existing one keeps
// its secret and returns "". Audited identity_proofing.webhook_configured with
// before and after.
func (s *WebhookStore) Save(ctx context.Context, orgID, customerID uuid.UUID, url string, events []string) (Webhook, string, error) {
	if s.cipher == nil {
		return Webhook{}, "", ErrNoEncryptionKey
	}
	var secret string
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := getCustomer(ctx, q, orgID, customerID); err != nil {
			return err
		}
		before, err := getWebhook(ctx, q, orgID, customerID)
		switch {
		case errors.Is(err, ErrWebhookNotFound):
			secret = newWebhookSecret()
			sealed, err := s.cipher.Encrypt([]byte(secret))
			if err != nil {
				return fmt.Errorf("proofing: seal webhook secret customer %s: %w", customerID, err)
			}
			if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_webhooks
				(customer_id, organization_id, url, events, secret_ciphertext, secret_last4)
				VALUES ($1, $2, $3, $4, $5, $6)`, customerID, orgID, url, events, sealed, secretLast4(secret)); err != nil {
				return fmt.Errorf("proofing: create webhook customer %s: %w", customerID, err)
			}
			return s.audit.Record(ctx, q, audit.IdentityProofingWebhookConfigured,
				audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
				audit.Created(Webhook{URL: url, Events: events}.auditFields()))
		case err != nil:
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_webhooks SET url = $3, events = $4, updated_at = now()
			WHERE organization_id = $1 AND customer_id = $2`, orgID, customerID, url, events); err != nil {
			return fmt.Errorf("proofing: update webhook customer %s: %w", customerID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookConfigured,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
			audit.Updated(before.auditFields(), Webhook{URL: url, Events: events}.auditFields()))
	})
	if err != nil {
		return Webhook{}, "", err
	}
	w, err := s.Get(ctx, orgID, customerID)
	return w, secret, err
}

// RotateSecret replaces an endpoint's signing secret and returns the new one,
// the one time it is readable. For webhookSecretOverlap the previous one
// signs every delivery too, so the customer can switch over without its
// endpoint refusing deliveries meanwhile.
// Audited identity_proofing.webhook_secret_rotated.
func (s *WebhookStore) RotateSecret(ctx context.Context, orgID, customerID uuid.UUID) (Webhook, string, error) {
	if s.cipher == nil {
		return Webhook{}, "", ErrNoEncryptionKey
	}
	secret := newWebhookSecret()
	sealed, err := s.cipher.Encrypt([]byte(secret))
	if err != nil {
		return Webhook{}, "", fmt.Errorf("proofing: seal webhook secret customer %s: %w", customerID, err)
	}
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getWebhook(ctx, q, orgID, customerID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_webhooks
			SET previous_secret_ciphertext = secret_ciphertext, previous_secret_until = now() + $5::interval,
				secret_ciphertext = $3, secret_last4 = $4, updated_at = now()
			WHERE organization_id = $1 AND customer_id = $2`,
			orgID, customerID, sealed, secretLast4(secret), webhookSecretOverlap.String()); err != nil {
			return fmt.Errorf("proofing: rotate webhook secret customer %s: %w", customerID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookSecretRotated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"secretLast4": before.SecretLast4}, map[string]any{"secretLast4": secretLast4(secret)}))
	})
	if err != nil {
		return Webhook{}, "", err
	}
	w, err := s.Get(ctx, orgID, customerID)
	return w, secret, err
}

// Remove deletes a customer's endpoint and every delivery still waiting for
// it. Audited identity_proofing.webhook_removed.
func (s *WebhookStore) Remove(ctx context.Context, orgID, customerID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getWebhook(ctx, q, orgID, customerID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_webhook_deliveries
			WHERE organization_id = $1 AND customer_id = $2 AND status = $3`,
			orgID, customerID, DeliveryPending); err != nil {
			return fmt.Errorf("proofing: drop pending deliveries customer %s: %w", customerID, err)
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_webhooks
			WHERE organization_id = $1 AND customer_id = $2`, orgID, customerID); err != nil {
			return fmt.Errorf("proofing: remove webhook customer %s: %w", customerID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingWebhookRemoved,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: customerID.String(), OrgID: &orgID},
			audit.Deleted(before.auditFields()))
	})
}

// SendTest queues a test event for a customer's endpoint.
func (s *WebhookStore) SendTest(ctx context.Context, orgID, customerID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := getWebhook(ctx, q, orgID, customerID); err != nil {
			return err
		}
		return enqueueWebhook(ctx, q, orgID, &customerID, EventTest, nil, map[string]any{"message": "test event"})
	})
}

// Deliveries returns a customer's most recent deliveries, newest first.
func (s *WebhookStore) Deliveries(ctx context.Context, orgID, customerID uuid.UUID) ([]Delivery, error) {
	rows, err := s.db.Query(ctx, `SELECT id, event, request_id, COALESCE(endpoint_url, ''), status, attempts, last_status_code,
			COALESCE(last_error, ''), last_attempt_at, delivered_at, created_at
		FROM identity_proofing_webhook_deliveries
		WHERE organization_id = $1 AND customer_id = $2
		ORDER BY created_at DESC, id LIMIT $3`, orgID, customerID, maxListedDeliveries)
	if err != nil {
		return nil, fmt.Errorf("proofing: list deliveries customer %s: %w", customerID, err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Delivery, error) {
		var d Delivery
		err := row.Scan(&d.ID, &d.Event, &d.RequestID, &d.EndpointURL, &d.Status, &d.Attempts, &d.LastStatusCode,
			&d.LastError, &d.LastAttemptAt, &d.DeliveredAt, &d.CreatedAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("proofing: list deliveries customer %s: %w", customerID, err)
	}
	return out, nil
}

// Health reports how each of the org's customers' endpoints has been
// answering, keyed by customer; a customer without an endpoint is absent.
func (s *WebhookStore) Health(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]WebhookHealth, error) {
	// The last attempt decides the state. The failing streak runs back from it
	// to the first attempt after the last success.
	rows, err := s.db.Query(ctx, `SELECT w.customer_id, last.status, last.last_status_code,
			(SELECT min(d.last_attempt_at) FROM identity_proofing_webhook_deliveries d
			 WHERE d.customer_id = w.customer_id AND d.status <> $2 AND d.last_attempt_at IS NOT NULL
				AND d.last_attempt_at > COALESCE((SELECT max(ok.delivered_at) FROM identity_proofing_webhook_deliveries ok
					WHERE ok.customer_id = w.customer_id AND ok.status = $2), '-infinity')),
			(SELECT count(*) FROM identity_proofing_webhook_deliveries d
			 WHERE d.customer_id = w.customer_id AND d.status = $3 AND d.attempts > 0)
		FROM identity_proofing_webhooks w
		LEFT JOIN LATERAL (
			SELECT d.status, d.last_status_code FROM identity_proofing_webhook_deliveries d
			WHERE d.customer_id = w.customer_id AND d.last_attempt_at IS NOT NULL
			ORDER BY d.last_attempt_at DESC LIMIT 1
		) last ON true
		WHERE w.organization_id = $1`, orgID, DeliveryDelivered, DeliveryPending)
	if err != nil {
		return nil, fmt.Errorf("proofing: webhook health org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := map[uuid.UUID]WebhookHealth{}
	for rows.Next() {
		var id uuid.UUID
		var lastStatus *string
		var h WebhookHealth
		if err := rows.Scan(&id, &lastStatus, &h.LastStatusCode, &h.FailingSince, &h.PendingRetries); err != nil {
			return nil, fmt.Errorf("proofing: scan webhook health org %s: %w", orgID, err)
		}
		h.State = WebhookDelivering
		if lastStatus != nil && *lastStatus != DeliveryDelivered {
			h.State = WebhookFailing
		} else {
			h.FailingSince = nil
		}
		out[id] = h
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: webhook health org %s: %w", orgID, err)
	}
	return out, nil
}

// claimDue leases up to limit due deliveries, across every org, and returns
// them with their endpoint's URL and secrets. The lease keeps another worker
// from sending the same delivery while this one does.
func (s *WebhookStore) claimDue(ctx context.Context, limit int) ([]dueDelivery, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}

	// A delivery whose endpoint is gone is never claimed; Remove drops it.
	rows, err := s.db.Query(ctx, `WITH due AS (
			SELECT d.id, w.url, w.secret_ciphertext,
				CASE WHEN w.previous_secret_until > now() THEN w.previous_secret_ciphertext END AS previous_ciphertext
			FROM identity_proofing_webhook_deliveries d
			JOIN identity_proofing_webhooks w ON w.customer_id = d.customer_id
			WHERE d.status = $1 AND d.next_attempt_at <= now()
			ORDER BY d.next_attempt_at LIMIT $2 FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE identity_proofing_webhook_deliveries d
		SET next_attempt_at = now() + make_interval(secs => $3), endpoint_url = due.url
		FROM due
		WHERE d.id = due.id
		RETURNING d.id, d.event, d.payload, d.attempts, d.created_at, d.next_attempt_at, due.url,
			due.secret_ciphertext, due.previous_ciphertext`,
		DeliveryPending, limit, deliveryLease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("proofing: claim deliveries: %w", err)
	}

	defer rows.Close()
	var out []dueDelivery
	for rows.Next() {
		var d dueDelivery
		var sealed, previous []byte
		if err := rows.Scan(&d.ID, &d.Event, &d.Payload, &d.Attempts, &d.CreatedAt, &d.LeasedUntil, &d.URL,
			&sealed, &previous); err != nil {
			return nil, fmt.Errorf("proofing: scan delivery: %w", err)
		}
		secrets, err := s.openSecrets(sealed, previous)
		if err != nil {
			// Skip only this one; its lease lapses and it is retried, e.g. after a key fix.
			slog.ErrorContext(ctx, "identity proofing: cannot open webhook secret",
				slog.String("delivery_id", d.ID.String()), slog.Any("error", err))
			continue
		}
		d.Secrets = secrets
		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: claim deliveries: %w", err)
	}

	return out, nil
}

// openSecrets unseals an endpoint's current secret and, when set, the
// previous one still in its overlap.
func (s *WebhookStore) openSecrets(sealed, previous []byte) ([]string, error) {
	secret, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return nil, err
	}
	secrets := []string{string(secret)}
	if previous != nil {
		old, err := s.cipher.Decrypt(previous)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, string(old))
	}
	return secrets, nil
}

// nextDue is when the earliest pending delivery falls due; zero when none is.
func (s *WebhookStore) nextDue(ctx context.Context) (time.Time, error) {
	var next *time.Time
	// Joined like claimDue: a delivery it cannot claim must not wake the job at once, forever.
	if err := s.db.QueryRow(ctx, `SELECT min(d.next_attempt_at) FROM identity_proofing_webhook_deliveries d
		JOIN identity_proofing_webhooks w ON w.customer_id = d.customer_id
		WHERE d.status = $1`, DeliveryPending).Scan(&next); err != nil {
		return time.Time{}, fmt.Errorf("proofing: next delivery: %w", err)
	}
	if next == nil {
		return time.Time{}, nil
	}
	return *next, nil
}

// recordAttempt stores one attempt's outcome: delivered on a 2xx, else another
// attempt after the back-off, or failed once the attempts run out. Only while
// d's lease holds: a worker whose lease lapsed (and whose delivery another
// worker then claimed) records nothing, so its late outcome never overwrites
// the newer one.
func (s *WebhookStore) recordAttempt(ctx context.Context, d dueDelivery, statusCode *int, sendErr string) error {
	attempts := d.Attempts + 1
	// The database's clock sets the retry, as it decides what claimDue finds
	// due: an API replica whose clock runs behind must not make a failed
	// delivery due again at once.
	status, delay := DeliveryDelivered, time.Duration(0)
	if sendErr != "" {
		var ok bool
		if delay, ok = nextAttemptDelay(attempts); ok {
			status = DeliveryPending
		} else {
			status = DeliveryFailed
		}
	}
	if len(sendErr) > maxDeliveryError {
		sendErr = sendErr[:maxDeliveryError]
	}
	tag, err := s.db.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries
		SET status = $2, attempts = $3, next_attempt_at = now() + make_interval(secs => $4), last_attempt_at = now(),
			last_status_code = $5, last_error = NULLIF($6, ''),
			delivered_at = CASE WHEN $2 = $7 THEN now() END
		WHERE id = $1 AND status = $8 AND attempts = $9 AND next_attempt_at = $10`,
		d.ID, status, attempts, delay.Seconds(), statusCode, sendErr, DeliveryDelivered, DeliveryPending, d.Attempts, d.LeasedUntil)
	if err != nil {
		return fmt.Errorf("proofing: record delivery %s: %w", d.ID, err)
	}
	if tag.RowsAffected() == 0 {
		slog.WarnContext(ctx, "identity proofing: webhook delivery lease lapsed; outcome not recorded",
			slog.String("delivery_id", d.ID.String()))
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
