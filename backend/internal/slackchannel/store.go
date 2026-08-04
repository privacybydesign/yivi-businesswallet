package slackchannel

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists per-org Slack settings. The webhook URL is encrypted at rest
// with the deployment Slack key (`cipher`); when cipher is nil the deployment has
// no Slack key and storing a webhook URL is refused rather than kept in the clear.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *Store {
	return &Store{db: db, audit: recorder, cipher: cipher}
}

// GetSettings returns the non-secret view of an org's Slack settings.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT webhook_url_ciphertext IS NOT NULL, enabled, updated_at
		FROM org_slack_settings WHERE organization_id = $1`
	var out Settings
	err := s.db.QueryRow(ctx, query, orgID).Scan(&out.HasWebhook, &out.Enabled, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Configured: false}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("slackchannel: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	return out, nil
}

// Upsert creates or updates an org's Slack settings and audits the change in the
// same transaction. A nil input URL preserves the stored one, a non-nil URL is
// (re)encrypted, and a non-nil empty URL clears it. Returns ErrNoEncryptionKey
// when a URL is offered and the deployment has no Slack key.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	var webhookArg any // nil => clear (or, with setWebhook false, keep the stored one)
	setWebhook := false
	if in.WebhookURL != nil {
		setWebhook = true
		if *in.WebhookURL != "" {
			if s.cipher == nil {
				return Settings{}, ErrNoEncryptionKey
			}
			ciphertext, err := s.cipher.Encrypt([]byte(*in.WebhookURL))
			if err != nil {
				return Settings{}, fmt.Errorf("slackchannel: encrypt webhook org %s: %w", orgID, err)
			}
			webhookArg = ciphertext
		}
	}

	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Read the current state inside the transaction (locking the row) so the
		// audited {before, after} pair is the change this save actually made.
		before, err := lockedState(ctx, q, orgID)
		if err != nil {
			return err
		}

		// What the row will hold once this save lands, decided before it is written so
		// the stored state and the audited one cannot disagree.
		after := nextState(before, in, setWebhook, webhookArg != nil)

		// With setWebhook false the stored ciphertext is kept, so the enabled flag can
		// be changed without re-pasting the URL.
		const upsert = `INSERT INTO org_slack_settings
			(organization_id, webhook_url_ciphertext, enabled)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id) DO UPDATE SET
				webhook_url_ciphertext = CASE WHEN $4 THEN EXCLUDED.webhook_url_ciphertext
				                              ELSE org_slack_settings.webhook_url_ciphertext END,
				enabled = EXCLUDED.enabled,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, webhookArg, after.enabled, setWebhook); err != nil {
			return fmt.Errorf("slackchannel: upsert settings org %s: %w", orgID, err)
		}

		// The URL itself is never audited: an audit event is read back by every org
		// admin and exported, and the notification catalog hands metadata to outside
		// systems. Whether one is set is what a reader needs.
		return s.audit.Record(ctx, q, audit.SlackSettingsUpdated,
			audit.Target{Type: audit.TargetSlackSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(before.auditSnapshot(), after.auditSnapshot()))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// state is the pair of facts about an org's Slack settings that the audit event
// carries.
type state struct {
	hasWebhook bool
	enabled    bool
}

func (s state) auditSnapshot() map[string]any {
	return map[string]any{"hasWebhook": s.hasWebhook, "enabled": s.enabled}
}

// nextState is the state a save leaves behind: the webhook it keeps, sets or
// clears, and whether delivery is on. Delivery is clamped off when no webhook is
// stored, because "enabled with nothing to post to" is not a state worth keeping
// — the settings screen already refuses to offer it, GET would report it as on
// while the screen renders it off, and it would silently become a live setting
// the moment a URL is pasted. Only the API can ask for it; this is where both
// sides are made to agree.
func nextState(before state, in SettingsInput, setWebhook, hasNewWebhook bool) state {
	next := state{enabled: in.Enabled, hasWebhook: before.hasWebhook}
	if setWebhook {
		next.hasWebhook = hasNewWebhook
	}
	if !next.hasWebhook {
		next.enabled = false
	}
	return next
}

// lockedState reads (and locks) an org's current settings row. A missing row is
// the zero state, not an error: the first save creates it.
func lockedState(ctx context.Context, q database.Querier, orgID uuid.UUID) (state, error) {
	const query = `SELECT webhook_url_ciphertext IS NOT NULL, enabled
		FROM org_slack_settings WHERE organization_id = $1 FOR UPDATE`
	var current state
	switch err := q.QueryRow(ctx, query, orgID).Scan(&current.hasWebhook, &current.enabled); {
	case errors.Is(err, pgx.ErrNoRows):
		return state{}, nil
	case err != nil:
		return state{}, fmt.Errorf("slackchannel: read settings org %s: %w", orgID, err)
	}
	return current, nil
}

// webhookFor resolves the decrypted webhook URL to post to. It returns
// ErrNotConfigured when the org has no row, has no URL stored, or has switched
// Slack off — none of which is a failure, they are all "do not post".
func (s *Store) webhookFor(ctx context.Context, orgID uuid.UUID) (string, error) {
	const query = `SELECT webhook_url_ciphertext, enabled
		FROM org_slack_settings WHERE organization_id = $1`
	var (
		ciphertext []byte
		enabled    bool
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(&ciphertext, &enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotConfigured
	}
	if err != nil {
		return "", fmt.Errorf("slackchannel: webhook for org %s: %w", orgID, err)
	}
	if !enabled || len(ciphertext) == 0 {
		return "", ErrNotConfigured
	}
	if s.cipher == nil {
		// The key that sealed this row is gone from the deployment, so the URL cannot
		// be recovered. That is a misconfiguration to state, not a silent skip.
		return "", fmt.Errorf("slackchannel: org %s has a stored webhook but %w", orgID, ErrNoEncryptionKey)
	}
	plaintext, err := s.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("slackchannel: decrypt webhook org %s: %w", orgID, err)
	}
	return string(plaintext), nil
}
