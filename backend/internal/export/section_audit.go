package export

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
)

type auditReader interface {
	ListForOrganization(ctx context.Context, orgID uuid.UUID, after *audit.Cursor, limit int) (audit.Page, error)
}

// AuditRecordsWriter fills the interaction-records section: the organization's
// whole audit trail.
type AuditRecordsWriter struct {
	reader auditReader
}

func NewAuditRecordsWriter(reader auditReader) *AuditRecordsWriter {
	return &AuditRecordsWriter{reader: reader}
}

func (w *AuditRecordsWriter) Key() string { return SectionAuditRecords }

func (w *AuditRecordsWriter) Write(ctx context.Context, orgID uuid.UUID, s *SectionBundle) error {
	events, err := w.readAll(ctx, orgID)
	if err != nil {
		return err
	}
	s.Count("events", len(events))
	return s.AddJSON("events.json", events)
}

// readAll follows the reader's cursor to the end. The API caps a page at
// MaxListLimit, so an org with a long trail needs the loop; exporting only the
// first page would silently truncate the record.
func (w *AuditRecordsWriter) readAll(ctx context.Context, orgID uuid.UUID) ([]auditRecord, error) {
	records := []auditRecord{}
	var after *audit.Cursor

	for {
		page, err := w.reader.ListForOrganization(ctx, orgID, after, audit.MaxListLimit)
		if err != nil {
			return nil, fmt.Errorf("export: reading audit events: %w", err)
		}
		for _, event := range page.Events {
			records = append(records, auditRecordOf(event))
		}
		if page.NextCursor == nil {
			return records, nil
		}
		cursor, err := audit.DecodeCursor(*page.NextCursor)
		if err != nil {
			return nil, fmt.Errorf("export: decoding audit cursor: %w", err)
		}
		after = &cursor
	}
}

// auditRecord is the exported shape. The fields are copied explicitly rather
// than marshalling audit.Event, whose actor also carries avatar state that is
// neither portable data nor meant to leave the API.
type auditRecord struct {
	ID         uuid.UUID       `json:"id"`
	OccurredAt string          `json:"occurredAt"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType"`
	TargetID   string          `json:"targetId"`
	Metadata   json.RawMessage `json:"metadata"`
	Actor      *auditActor     `json:"actor"`
}

type auditActor struct {
	UserID        uuid.UUID `json:"userId"`
	PreferredName *string   `json:"preferredName"`
	GivenNames    string    `json:"givenNames"`
	LastName      string    `json:"lastName"`
}

func auditRecordOf(event audit.Event) auditRecord {
	record := auditRecord{
		ID:         event.ID,
		OccurredAt: timestamp(event.OccurredAt),
		Action:     event.Action,
		TargetType: event.TargetType,
		TargetID:   event.TargetID,
		// The {before, after} envelope travels verbatim: it is the event's own
		// record of what changed, and re-encoding it would drop fields this
		// package does not know about.
		Metadata: event.Metadata,
	}
	if len(record.Metadata) == 0 {
		record.Metadata = json.RawMessage("null")
	}
	if event.Actor != nil {
		record.Actor = &auditActor{
			UserID:        event.Actor.UserID,
			PreferredName: event.Actor.PreferredName,
			GivenNames:    event.Actor.GivenNames,
			LastName:      event.Actor.LastName,
		}
	}
	return record
}
