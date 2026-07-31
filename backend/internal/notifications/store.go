package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// DefaultClaimBatch is how many outbox rows one dispatch pass takes at a time.
// It bounds the work of a single pass so a backlog drains over several ticks
// instead of one long one.
const DefaultClaimBatch = 100

// Store persists per-org notification subscriptions and owns the outbox queue.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// GetSettings returns an org's subscriptions (Configured false, and no
// subscriptions, when the org has never saved any).
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT subscriptions, updated_at
		FROM org_notification_settings WHERE organization_id = $1`
	var (
		raw []byte
		out Settings
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(&raw, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Subscriptions: map[string][]ChannelID{}}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("notifications: get settings org %s: %w", orgID, err)
	}
	subs, err := decodeSubscriptions(raw)
	if err != nil {
		return Settings{}, fmt.Errorf("notifications: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	out.Subscriptions = subs
	return out, nil
}

// Save replaces an org's subscriptions with the (already normalized) input and
// audits the change in the same transaction.
func (s *Store) Save(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	subs := in.Subscriptions
	if subs == nil {
		subs = map[string][]ChannelID{}
	}
	encoded, err := json.Marshal(subs)
	if err != nil {
		return Settings{}, fmt.Errorf("notifications: encode subscriptions org %s: %w", orgID, err)
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		// Read the current document inside the transaction (and lock the row) so the
		// audited {before, after} diff is the change this save actually made.
		const current = `SELECT subscriptions FROM org_notification_settings
			WHERE organization_id = $1 FOR UPDATE`
		var raw []byte
		switch err := q.QueryRow(ctx, current, orgID).Scan(&raw); {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("notifications: read settings org %s: %w", orgID, err)
		}
		before, err := decodeSubscriptions(raw)
		if err != nil {
			return fmt.Errorf("notifications: read settings org %s: %w", orgID, err)
		}

		const upsert = `INSERT INTO org_notification_settings (organization_id, subscriptions)
			VALUES ($1, $2)
			ON CONFLICT (organization_id) DO UPDATE SET
				subscriptions = EXCLUDED.subscriptions,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, encoded); err != nil {
			return fmt.Errorf("notifications: save settings org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.NotificationSettingsUpdated,
			audit.Target{Type: audit.TargetNotificationSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(auditSnapshot(before), auditSnapshot(subs)))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// Enqueue adds an event to the outbox on the caller's querier, so it commits with
// the transaction that recorded the audit event.
func (s *Store) Enqueue(ctx context.Context, q database.Querier, e Event) error {
	metadata := []byte("{}")
	if e.Metadata != nil {
		m, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("notifications: encode metadata for %s: %w", e.Action, err)
		}
		metadata = m
	}
	const insert = `INSERT INTO notification_outbox
		(organization_id, actor_user_id, action, target_type, target_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := q.Exec(ctx, insert, e.OrgID, e.ActorUserID, e.Action, e.TargetType, e.TargetID, metadata); err != nil {
		return fmt.Errorf("notifications: enqueue %s: %w", e.Action, err)
	}
	return nil
}

// Claim takes up to limit queued events off the outbox, oldest first, and returns
// them. The delete and the read are one statement, so two API replicas polling at
// the same time never hand the same event to a channel twice — and an event that
// is claimed but not delivered is gone, which is the at-most-once delivery this
// layer promises (see the package doc).
func (s *Store) Claim(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = DefaultClaimBatch
	}
	const claim = `DELETE FROM notification_outbox
		WHERE id IN (
			SELECT id FROM notification_outbox
			ORDER BY occurred_at, id
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, organization_id, actor_user_id, action, target_type, target_id, metadata, occurred_at`
	rows, err := s.db.Query(ctx, claim, limit)
	if err != nil {
		return nil, fmt.Errorf("notifications: claim outbox: %w", err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var (
			e   Event
			raw []byte
		)
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ActorUserID, &e.Action, &e.TargetType,
			&e.TargetID, &raw, &e.OccurredAt); err != nil {
			return nil, fmt.Errorf("notifications: claim outbox scan: %w", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &e.Metadata); err != nil {
				return nil, fmt.Errorf("notifications: decode metadata for %s: %w", e.Action, err)
			}
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("notifications: claim outbox rows: %w", err)
	}
	return events, nil
}

// decodeSubscriptions reads a stored subscriptions document. It is not run
// through Normalize: what was written was normalized at save time, and silently
// dropping an event that has since left the catalog would hide the change.
func decodeSubscriptions(raw []byte) (map[string][]ChannelID, error) {
	subs := map[string][]ChannelID{}
	if len(raw) == 0 {
		return subs, nil
	}
	if err := json.Unmarshal(raw, &subs); err != nil {
		return nil, fmt.Errorf("decode subscriptions: %w", err)
	}
	return subs, nil
}
