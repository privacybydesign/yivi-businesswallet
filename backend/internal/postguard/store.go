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

// Store is the pgx-backed persistence for PostGuard API keys and sent-file
// records. The API key is encrypted with cipher before it touches the database;
// cipher is nil when the deployment has no key-encryption key, in which case
// key operations return ErrNotConfigured.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	cipher *Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, cipher *Cipher) *Store {
	return &Store{db: db, audit: recorder, cipher: cipher}
}

// APIKeyInfo returns the non-secret view of an org's stored key.
func (s *Store) APIKeyInfo(ctx context.Context, orgID uuid.UUID) (APIKeyInfo, error) {
	const query = `SELECT key_last4, updated_at FROM postguard_api_keys WHERE organization_id = $1`
	var (
		info APIKeyInfo
		row  = s.db.QueryRow(ctx, query, orgID)
	)
	err := row.Scan(&info.Last4, &info.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKeyInfo{Configured: false}, nil
	}
	if err != nil {
		return APIKeyInfo{}, fmt.Errorf("postguard: get api key info org %s: %w", orgID, err)
	}
	info.Configured = true
	return info, nil
}

// SetAPIKey encrypts and upserts an org's API key, auditing the change (last4
// only — never the key material).
func (s *Store) SetAPIKey(ctx context.Context, orgID uuid.UUID, apiKey string) error {
	if s.cipher == nil {
		return ErrNotConfigured
	}
	encrypted, err := s.cipher.Encrypt([]byte(apiKey))
	if err != nil {
		return err
	}
	last4 := lastN(apiKey, 4)

	return database.InTx(ctx, s.db, func(q database.Querier) error {
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

// DeleteAPIKey removes an org's API key, auditing the removal. It is a no-op (no
// error) when no key is stored.
func (s *Store) DeleteAPIKey(ctx context.Context, orgID uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM postguard_api_keys WHERE organization_id = $1`
		tag, err := q.Exec(ctx, del, orgID)
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
// ErrKeyNotSet when the org has none, ErrNotConfigured when no cipher is set.
func (s *Store) DecryptedAPIKey(ctx context.Context, orgID uuid.UUID) (string, error) {
	if s.cipher == nil {
		return "", ErrNotConfigured
	}
	const query = `SELECT encrypted_key FROM postguard_api_keys WHERE organization_id = $1`
	var encrypted []byte
	err := s.db.QueryRow(ctx, query, orgID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrKeyNotSet
	}
	if err != nil {
		return "", fmt.Errorf("postguard: get api key org %s: %w", orgID, err)
	}
	plaintext, err := s.cipher.Decrypt(encrypted)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

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
