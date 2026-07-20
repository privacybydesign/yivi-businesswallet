package postguard

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store is the pgx-backed persistence for PostGuard.
//
// Envelope encryption: each org has a data-encryption key (DEK) it configures;
// the DEK is stored wrapped by the deployment master key (`master`) in
// postguard_org_keys, and the DEK in turn encrypts the org's API key in
// postguard_api_keys. `master` is nil when the deployment has no master key, in
// which case every key operation returns ErrNotConfigured.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	master *Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, master *Cipher) *Store {
	return &Store{db: db, audit: recorder, master: master}
}

// --- Per-org encryption key (DEK) ---------------------------------------

// EncryptionKeyInfo returns the non-secret view of an org's encryption key.
func (s *Store) EncryptionKeyInfo(ctx context.Context, orgID uuid.UUID) (EncryptionKeyInfo, error) {
	const query = `SELECT updated_at FROM postguard_org_keys WHERE organization_id = $1`
	var info EncryptionKeyInfo
	err := s.db.QueryRow(ctx, query, orgID).Scan(&info.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EncryptionKeyInfo{Configured: false}, nil
	}
	if err != nil {
		return EncryptionKeyInfo{}, fmt.Errorf("postguard: get encryption key info org %s: %w", orgID, err)
	}
	info.Configured = true
	return info, nil
}

// SetEncryptionKey derives a DEK from the owner-supplied secret, wraps it with
// the master key and stores it. If the org already has an API key (encrypted
// under the previous DEK), it is transparently re-encrypted under the new DEK in
// the same transaction. Auditing records only that the key changed.
func (s *Store) SetEncryptionKey(ctx context.Context, orgID uuid.UUID, secret string) error {
	if s.master == nil {
		return ErrNotConfigured
	}
	newDEK := deriveDEK(secret)
	wrapped, err := s.master.Encrypt(newDEK)
	if err != nil {
		return err
	}
	newCipher, err := newCipherFromKey(newDEK)
	if err != nil {
		return err
	}

	return database.InTx(ctx, s.db, func(q database.Querier) error {
		// Re-encrypt an existing API key, if any, under the new DEK.
		if err := s.reencryptAPIKey(ctx, q, orgID, newCipher); err != nil {
			return err
		}

		const upsert = `INSERT INTO postguard_org_keys (organization_id, wrapped_dek)
			VALUES ($1, $2)
			ON CONFLICT (organization_id)
			DO UPDATE SET wrapped_dek = EXCLUDED.wrapped_dek, updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, wrapped); err != nil {
			return fmt.Errorf("postguard: upsert encryption key org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.PostGuardEncryptionKeySet,
			audit.Target{Type: audit.TargetPostGuardEncryptionKey, ID: orgID.String(), OrgID: &orgID}, nil)
	})
}

// reencryptAPIKey re-seals an existing API key under newCipher. No-op when the
// org has no API key stored.
func (s *Store) reencryptAPIKey(ctx context.Context, q database.Querier, orgID uuid.UUID, newCipher *Cipher) error {
	oldCipher, err := s.loadDEKCipher(ctx, q, orgID)
	if errors.Is(err, ErrEncryptionKeyNotSet) {
		return nil // first-time set: nothing encrypted yet
	}
	if err != nil {
		return err
	}

	const sel = `SELECT encrypted_key FROM postguard_api_keys WHERE organization_id = $1`
	var encrypted []byte
	err = q.QueryRow(ctx, sel, orgID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("postguard: read api key for re-encryption org %s: %w", orgID, err)
	}

	plaintext, err := oldCipher.Decrypt(encrypted)
	if err != nil {
		return err
	}
	resealed, err := newCipher.Encrypt(plaintext)
	if err != nil {
		return err
	}
	const upd = `UPDATE postguard_api_keys SET encrypted_key = $2, updated_at = now() WHERE organization_id = $1`
	if _, err := q.Exec(ctx, upd, orgID, resealed); err != nil {
		return fmt.Errorf("postguard: re-encrypt api key org %s: %w", orgID, err)
	}
	return nil
}

// RemoveEncryptionKey removes an org's encryption key. Because the API key is
// unusable without it, any stored API key is removed in the same transaction.
func (s *Store) RemoveEncryptionKey(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `DELETE FROM postguard_api_keys WHERE organization_id = $1`, orgID); err != nil {
			return fmt.Errorf("postguard: delete api key org %s: %w", orgID, err)
		}
		tag, err := q.Exec(ctx, `DELETE FROM postguard_org_keys WHERE organization_id = $1`, orgID)
		if err != nil {
			return fmt.Errorf("postguard: delete encryption key org %s: %w", orgID, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrEncryptionKeyNotSet
		}
		return s.audit.Record(ctx, q, audit.PostGuardEncryptionKeyRemoved,
			audit.Target{Type: audit.TargetPostGuardEncryptionKey, ID: orgID.String(), OrgID: &orgID}, nil)
	})
}

// loadDEKCipher loads and unwraps the org's DEK, returning a Cipher for it.
// Returns ErrEncryptionKeyNotSet when the org has none, ErrNotConfigured when no
// master key is set.
func (s *Store) loadDEKCipher(ctx context.Context, q database.Querier, orgID uuid.UUID) (*Cipher, error) {
	if s.master == nil {
		return nil, ErrNotConfigured
	}
	const query = `SELECT wrapped_dek FROM postguard_org_keys WHERE organization_id = $1`
	var wrapped []byte
	err := q.QueryRow(ctx, query, orgID).Scan(&wrapped)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEncryptionKeyNotSet
	}
	if err != nil {
		return nil, fmt.Errorf("postguard: load encryption key org %s: %w", orgID, err)
	}
	dek, err := s.master.Decrypt(wrapped)
	if err != nil {
		return nil, err
	}
	return newCipherFromKey(dek)
}

// --- Notification delivery ---------------------------------------------

// NotificationDelivery returns the org's recipient-notification delivery method,
// defaulting to NotifyPostGuard when the org has no row yet.
func (s *Store) NotificationDelivery(ctx context.Context, orgID uuid.UUID) (NotificationDelivery, error) {
	const query = `SELECT notification_delivery FROM postguard_org_settings WHERE organization_id = $1`
	var method string
	err := s.db.QueryRow(ctx, query, orgID).Scan(&method)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotifyPostGuard, nil
	}
	if err != nil {
		return "", fmt.Errorf("postguard: get notification delivery org %s: %w", orgID, err)
	}
	return NotificationDelivery(method), nil
}

// SetNotificationDelivery upserts the org's recipient-notification delivery
// method and audits the change.
func (s *Store) SetNotificationDelivery(ctx context.Context, orgID uuid.UUID, method NotificationDelivery) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const upsert = `INSERT INTO postguard_org_settings (organization_id, notification_delivery)
			VALUES ($1, $2)
			ON CONFLICT (organization_id)
			DO UPDATE SET notification_delivery = EXCLUDED.notification_delivery, updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, string(method)); err != nil {
			return fmt.Errorf("postguard: upsert notification delivery org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.PostGuardNotificationSet,
			audit.Target{Type: audit.TargetPostGuardSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"notificationDelivery": string(method)}))
	})
}

// --- API key ------------------------------------------------------------

// APIKeyInfo returns the non-secret view of an org's stored key.
func (s *Store) APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error) {
	const query = `SELECT key_last4, updated_at FROM postguard_api_keys WHERE organization_id = $1`
	var info APIKeyInfo
	err := s.db.QueryRow(ctx, query, orgID).Scan(&info.Last4, &info.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKeyInfo{Configured: false}, nil
	}
	if err != nil {
		return APIKeyInfo{}, fmt.Errorf("postguard: get api key info org %s: %w", orgID, err)
	}
	info.Configured = true
	return info, nil
}

// SetAPIKey encrypts and upserts an org's API key with the org's DEK. Requires
// the org's encryption key to be configured first.
func (s *Store) SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error {
	last4 := lastN(apiKey, 4)
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		dek, err := s.loadDEKCipher(ctx, q, orgID)
		if err != nil {
			return err
		}
		encrypted, err := dek.Encrypt([]byte(apiKey))
		if err != nil {
			return err
		}
		const upsert = `INSERT INTO postguard_api_keys (organization_id, encrypted_key, key_last4)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id)
			DO UPDATE SET encrypted_key = EXCLUDED.encrypted_key, key_last4 = EXCLUDED.key_last4, updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, encrypted, last4); err != nil {
			return fmt.Errorf("postguard: upsert api key org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.PostGuardKeySet,
			audit.Target{Type: audit.TargetPostGuardKey, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(nil, map[string]any{"last4": last4}))
	})
}

// DeleteAPIKey removes an org's API key. Returns ErrKeyNotSet when none exists.
func (s *Store) DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `DELETE FROM postguard_api_keys WHERE organization_id = $1`, orgID)
		if err != nil {
			return fmt.Errorf("postguard: delete api key org %s: %w", orgID, err)
		}
		if tag.RowsAffected() == 0 {
			return ErrKeyNotSet
		}
		return s.audit.Record(ctx, q, audit.PostGuardKeyRemoved,
			audit.Target{Type: audit.TargetPostGuardKey, ID: orgID.String(), OrgID: &orgID}, nil)
	})
}

// DecryptedAPIKey returns the plaintext API key for use on send. Returns
// ErrKeyNotSet when the org has none, ErrEncryptionKeyNotSet when the org's
// encryption key is missing, ErrNotConfigured when no master key is set.
func (s *Store) DecryptedAPIKey(ctx context.Context, orgID uuid.UUID) (string, error) {
	dek, err := s.loadDEKCipher(ctx, s.db, orgID)
	if err != nil {
		return "", err
	}
	const query = `SELECT encrypted_key FROM postguard_api_keys WHERE organization_id = $1`
	var encrypted []byte
	err = s.db.QueryRow(ctx, query, orgID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrKeyNotSet
	}
	if err != nil {
		return "", fmt.Errorf("postguard: get api key org %s: %w", orgID, err)
	}
	plaintext, err := dek.Decrypt(encrypted)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// --- Sent files ---------------------------------------------------------

// RecordSentFile persists a sent-file record and audits the transfer.
func (s *Store) RecordSentFile(ctx context.Context, orgID uuid.UUID, senderUserID uuid.UUID, f SentFile) (SentFile, error) {
	var expiresAfter *string
	if f.ExpiresAfter != "" {
		expiresAfter = &f.ExpiresAfter
	}
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO postguard_sent_files
			(organization_id, sender_user_id, file_name, size_bytes, recipients, cryptify_uuid, expires_after)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			RETURNING id, status, created_at`
		if err := q.QueryRow(ctx, insert, orgID, senderUserID, f.FileName, f.SizeBytes, f.Recipients, f.CryptifyUUID, expiresAfter).
			Scan(&f.ID, &f.Status, &f.CreatedAt); err != nil {
			return fmt.Errorf("postguard: record sent file org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.PostGuardFileSent,
			audit.Target{Type: audit.TargetPostGuardFile, ID: f.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{
				"fileName":     f.FileName,
				"sizeBytes":    f.SizeBytes,
				"recipients":   len(f.Recipients),
				"cryptifyUuid": f.CryptifyUUID,
			}))
	})
	return f, err
}

// ListSentFiles returns an org's sent-file history, newest first.
func (s *Store) ListSentFiles(ctx context.Context, orgID uuid.UUID) ([]SentFile, error) {
	const query = `SELECT id, file_name, size_bytes, recipients, cryptify_uuid, expires_after, status, created_at
		FROM postguard_sent_files WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("postguard: list sent files org %s: %w", orgID, err)
	}
	defer rows.Close()

	files := []SentFile{}
	for rows.Next() {
		var (
			f            SentFile
			expiresAfter *string
		)
		if err := rows.Scan(&f.ID, &f.FileName, &f.SizeBytes, &f.Recipients, &f.CryptifyUUID, &expiresAfter, &f.Status, &f.CreatedAt); err != nil {
			return nil, fmt.Errorf("postguard: list sent files scan: %w", err)
		}
		if expiresAfter != nil {
			f.ExpiresAfter = *expiresAfter
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postguard: list sent files rows: %w", err)
	}
	return files, nil
}

func lastN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
