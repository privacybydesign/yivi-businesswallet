package qerds

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerdsprovider"
)

const messageColumns = `id, organization_id, direction, sender_address, recipient_address,
	subject, body, provider_ref, status, submitted_at, delivered_at,
	qualified_timestamp_send, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanMessage(row rowScanner) (Message, error) {
	var (
		m           Message
		providerRef *string
	)
	if err := row.Scan(
		&m.ID, &m.OrganizationID, &m.Direction, &m.SenderAddress, &m.RecipientAddress,
		&m.Subject, &m.Body, &providerRef, &m.Status, &m.SubmittedAt, &m.DeliveredAt,
		&m.QualifiedTimestampSend, &m.CreatedAt, &m.UpdatedAt,
	); err != nil {
		return Message{}, err
	}
	if providerRef != nil {
		m.ProviderRef = *providerRef
	}
	return m, nil
}

// CreateOutbound persists a new outbound message in the "submitted" state and
// audits the send, before the provider is called. If the provider call later
// fails, the row stays submitted and is retryable.
func (s *Store) CreateOutbound(ctx context.Context, orgID uuid.UUID, sender, recipient, subject, body string) (Message, error) {
	var m Message
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO qerds_messages
			(organization_id, direction, sender_address, recipient_address, subject, body, status, submitted_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			RETURNING ` + messageColumns
		var err error
		m, err = scanMessage(q.QueryRow(ctx, insert, orgID, DirectionOutbound, sender, recipient, subject, body, StatusSubmitted))
		if err != nil {
			return fmt.Errorf("qerds: create outbound org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.QerdsMessageSent,
			audit.Target{Type: audit.TargetQerdsMessage, ID: m.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"recipient": recipient, "subject": subject}))
	})
	return m, err
}

// RecordSent applies a provider send receipt to a message: it advances the
// status, stamps the qualified send timestamp, and appends the returned
// evidence — all atomically.
func (s *Store) RecordSent(ctx context.Context, messageID uuid.UUID, receipt qerdsprovider.SendReceipt) error {
	status := StatusAccepted
	var deliveredAt *time.Time
	if receipt.Status == qerdsprovider.StatusDelivered {
		status = StatusDelivered
	}

	var qualifiedTS *time.Time
	for _, e := range receipt.Evidence {
		if e.Type == qerdsprovider.EvidenceSubmissionAcceptance {
			ts := e.QualifiedTimestamp
			qualifiedTS = &ts
		}
		if e.Type == qerdsprovider.EvidenceDelivery {
			ts := e.QualifiedTimestamp
			deliveredAt = &ts
		}
	}

	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE qerds_messages
			SET provider_ref = $2, status = $3, delivered_at = $4, qualified_timestamp_send = $5, updated_at = now()
			WHERE id = $1`
		if _, err := q.Exec(ctx, update, messageID, receipt.ProviderRef, status, deliveredAt, qualifiedTS); err != nil {
			return fmt.Errorf("qerds: record sent %s: %w", messageID, err)
		}
		return insertEvidence(ctx, q, messageID, receipt.Evidence)
	})
}

// CreateInbound persists a message received from the provider, deduping on the
// provider ref (webhooks and polls retry). It reports whether a new row was
// created; evidence and the audit event are written only on first receipt.
func (s *Store) CreateInbound(ctx context.Context, orgID uuid.UUID, in qerdsprovider.InboundMessage) (Message, bool, error) {
	var (
		m       Message
		created bool
	)
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO qerds_messages
			(organization_id, direction, sender_address, recipient_address, subject, body, provider_ref, status, delivered_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (provider_ref) DO NOTHING
			RETURNING ` + messageColumns
		var err error
		m, err = scanMessage(q.QueryRow(ctx, insert, orgID, DirectionInbound,
			string(in.Sender), string(in.Recipient), in.Subject, in.Body, in.ProviderRef, StatusReceived))
		if errors.Is(err, pgx.ErrNoRows) {
			// Already received — idempotent no-op.
			return nil
		}
		if err != nil {
			return fmt.Errorf("qerds: create inbound org %s: %w", orgID, err)
		}
		created = true

		if err := insertEvidence(ctx, q, m.ID, in.Evidence); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.QerdsMessageReceived,
			audit.Target{Type: audit.TargetQerdsMessage, ID: m.ID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"sender": string(in.Sender), "subject": in.Subject}))
	})
	return m, created, err
}

// List returns an organization's messages, newest first.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Message, error) {
	const query = `SELECT ` + messageColumns + ` FROM qerds_messages WHERE organization_id = $1 ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("qerds: list messages org %s: %w", orgID, err)
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("qerds: list messages scan: %w", err)
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qerds: list messages rows: %w", err)
	}
	return messages, nil
}

// GetWithEvidence returns a single org-scoped message and its evidence chain.
func (s *Store) GetWithEvidence(ctx context.Context, orgID, id uuid.UUID) (MessageWithEvidence, error) {
	const query = `SELECT ` + messageColumns + ` FROM qerds_messages WHERE organization_id = $1 AND id = $2`
	m, err := scanMessage(s.db.QueryRow(ctx, query, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return MessageWithEvidence{}, ErrMessageNotFound
	}
	if err != nil {
		return MessageWithEvidence{}, fmt.Errorf("qerds: get message %s: %w", id, err)
	}

	evidence, err := s.listEvidence(ctx, m.ID)
	if err != nil {
		return MessageWithEvidence{}, err
	}
	return MessageWithEvidence{Message: m, Evidence: evidence}, nil
}
