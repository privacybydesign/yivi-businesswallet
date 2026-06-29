package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

var ErrInvalidCursor = fmt.Errorf("audit: invalid cursor")

type EventActor struct {
	UserID        uuid.UUID `json:"userId"`
	PreferredName *string   `json:"preferredName"`
	GivenNames    string    `json:"givenNames"`
	NamePrefix    *string   `json:"namePrefix"`
	LastName      string    `json:"lastName"`
}

type Event struct {
	ID         uuid.UUID       `json:"id"`
	OccurredAt time.Time       `json:"occurredAt"`
	Action     string          `json:"action"`
	TargetType string          `json:"targetType"`
	TargetID   string          `json:"targetId"`
	Metadata   json.RawMessage `json:"metadata"`
	Actor      *EventActor     `json:"actor"`
}

type Page struct {
	Events     []Event `json:"events"`
	NextCursor *string `json:"nextCursor"`
}

// Cursor is the keyset position: the (occurred_at, id) of the last event a page
// returned. Both are needed because occurred_at alone is not unique.
type Cursor struct {
	OccurredAt time.Time
	ID         uuid.UUID
}

func EncodeCursor(c Cursor) string {
	raw := c.OccurredAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func DecodeCursor(s string) (Cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	ts, idStr, ok := strings.Cut(string(b), "|")
	if !ok {
		return Cursor{}, ErrInvalidCursor
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}
	return Cursor{OccurredAt: occurredAt, ID: id}, nil
}

type Reader struct {
	db database.DB
}

func NewReader(db database.DB) *Reader { return &Reader{db: db} }

// ListForOrganization returns one page of events for an org, newest first. A nil
// after starts from the newest event; otherwise it returns events strictly older
// than the cursor. limit is clamped to [1, MaxListLimit].
func (r *Reader) ListForOrganization(ctx context.Context, orgID uuid.UUID, after *Cursor, limit int) (Page, error) {
	switch {
	case limit <= 0:
		limit = DefaultListLimit
	case limit > MaxListLimit:
		limit = MaxListLimit
	}

	var cursorTime *time.Time
	var cursorID *uuid.UUID
	if after != nil {
		cursorTime = &after.OccurredAt
		cursorID = &after.ID
	}

	// Fetch one extra row to detect whether a further page exists.
	const q = `
		SELECT a.id, a.occurred_at, a.action, a.target_type, a.target_id, a.metadata,
		       u.id, u.preferred_name, u.given_names, u.name_prefix, u.last_name
		FROM audit_events a
		LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE a.organization_id = $1
		  AND ($2::timestamptz IS NULL OR (a.occurred_at, a.id) < ($2, $3::uuid))
		ORDER BY a.occurred_at DESC, a.id DESC
		LIMIT $4`
	rows, err := r.db.Query(ctx, q, orgID, cursorTime, cursorID, limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("audit: list events org %s: %w", orgID, err)
	}
	defer rows.Close()

	events := []Event{}
	for rows.Next() {
		var (
			e         Event
			meta      []byte
			actorID   *uuid.UUID
			preferred *string
			given     *string
			prefix    *string
			last      *string
		)
		if err := rows.Scan(&e.ID, &e.OccurredAt, &e.Action, &e.TargetType, &e.TargetID, &meta,
			&actorID, &preferred, &given, &prefix, &last); err != nil {
			return Page{}, fmt.Errorf("audit: list events scan: %w", err)
		}
		e.Metadata = meta
		if actorID != nil {
			e.Actor = &EventActor{UserID: *actorID, PreferredName: preferred, NamePrefix: prefix}
			if given != nil {
				e.Actor.GivenNames = *given
			}
			if last != nil {
				e.Actor.LastName = *last
			}
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("audit: list events rows: %w", err)
	}

	page := Page{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		last := page.Events[limit-1]
		cursor := EncodeCursor(Cursor{OccurredAt: last.OccurredAt, ID: last.ID})
		page.NextCursor = &cursor
	}
	return page, nil
}
