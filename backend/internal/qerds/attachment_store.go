package qerds

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// insertAttachments persists a message's payloads on the same transaction as
// the message row. Bytes are stored in the content column (blob-column MVP);
// the row also records the sha256 hash + size as content-opaque integrity
// metadata. storage_ref is left NULL until an object-storage backend lands.
func insertAttachments(ctx context.Context, q database.Querier, messageID uuid.UUID, attachments []qerdsprovider.Attachment) error {
	const insert = `INSERT INTO qerds_attachments
		(message_id, filename, content_type, content_hash, size_bytes, content)
		VALUES ($1, $2, $3, $4, $5, $6)`
	for _, a := range attachments {
		sum := sha256.Sum256(a.Content)
		hash := hex.EncodeToString(sum[:])
		if _, err := q.Exec(ctx, insert, messageID, a.Filename, a.ContentType, hash, len(a.Content), a.Content); err != nil {
			return fmt.Errorf("qerds: insert attachment for message %s: %w", messageID, err)
		}
	}
	return nil
}

// listAttachments returns a message's attachment metadata (no bytes), oldest
// first, matching upload order.
func (s *Store) listAttachments(ctx context.Context, messageID uuid.UUID) ([]Attachment, error) {
	const query = `SELECT id, message_id, filename, content_type, content_hash, size_bytes, created_at
		FROM qerds_attachments WHERE message_id = $1 ORDER BY created_at, id`
	rows, err := s.db.Query(ctx, query, messageID)
	if err != nil {
		return nil, fmt.Errorf("qerds: list attachments for message %s: %w", messageID, err)
	}
	defer rows.Close()

	attachments := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.ContentHash, &a.SizeBytes, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("qerds: list attachments scan: %w", err)
		}
		attachments = append(attachments, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qerds: list attachments rows: %w", err)
	}
	return attachments, nil
}

// AttachmentContent is a single attachment's metadata plus its bytes, for the
// download endpoint.
type AttachmentContent struct {
	Filename    string
	ContentType string
	Content     []byte
}

// GetAttachmentContent returns one attachment's bytes, scoped to the owning
// organization and message via a join so a caller cannot read another org's
// payloads by id. Returns ErrAttachmentNotFound when no such row is visible.
func (s *Store) GetAttachmentContent(ctx context.Context, orgID, messageID, attachmentID uuid.UUID) (AttachmentContent, error) {
	const query = `SELECT a.filename, a.content_type, a.content
		FROM qerds_attachments a
		JOIN qerds_messages m ON m.id = a.message_id
		WHERE a.id = $1 AND a.message_id = $2 AND m.organization_id = $3`
	var out AttachmentContent
	err := s.db.QueryRow(ctx, query, attachmentID, messageID, orgID).Scan(&out.Filename, &out.ContentType, &out.Content)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttachmentContent{}, ErrAttachmentNotFound
	}
	if err != nil {
		return AttachmentContent{}, fmt.Errorf("qerds: get attachment %s: %w", attachmentID, err)
	}
	return out, nil
}
