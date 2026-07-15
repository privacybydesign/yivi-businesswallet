package qerds

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

// insertEvidence appends provider evidence for a message. Evidence is
// append-only; it is written on the same transaction as the state change that
// produced it.
func insertEvidence(ctx context.Context, q database.Querier, messageID uuid.UUID, evidence []qerdsprovider.Evidence) error {
	const insert = `INSERT INTO qerds_evidence
		(message_id, evidence_type, provider_ref, qualified_timestamp, raw_evidence)
		VALUES ($1, $2, $3, $4, $5)`
	for _, e := range evidence {
		if _, err := q.Exec(ctx, insert, messageID, e.Type, e.ProviderRef, e.QualifiedTimestamp, e.Raw); err != nil {
			return fmt.Errorf("qerds: insert evidence for message %s: %w", messageID, err)
		}
	}
	return nil
}

func (s *Store) listEvidence(ctx context.Context, messageID uuid.UUID) ([]Evidence, error) {
	const query = `SELECT id, message_id, evidence_type, provider_ref, qualified_timestamp, raw_evidence, created_at
		FROM qerds_evidence WHERE message_id = $1 ORDER BY qualified_timestamp, created_at`
	rows, err := s.db.Query(ctx, query, messageID)
	if err != nil {
		return nil, fmt.Errorf("qerds: list evidence for message %s: %w", messageID, err)
	}
	defer rows.Close()

	evidence := []Evidence{}
	for rows.Next() {
		var e Evidence
		if err := rows.Scan(&e.ID, &e.MessageID, &e.Type, &e.ProviderRef, &e.QualifiedTimestamp, &e.Raw, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("qerds: list evidence scan: %w", err)
		}
		evidence = append(evidence, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qerds: list evidence rows: %w", err)
	}
	return evidence, nil
}
