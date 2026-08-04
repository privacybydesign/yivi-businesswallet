package notifications

import (
	"context"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// outboxWriter is the enqueue side of the outbox, implemented by *Store.
type outboxWriter interface {
	Enqueue(ctx context.Context, q database.Querier, e Event) error
}

// Recorder is the dispatch hook: an audit.Recorder that records the event through
// the recorder it wraps and then, for a subscribable org event, queues it for
// notification on the same querier — so the queued row commits (or rolls back)
// with the action that caused it.
//
// It queues every subscribable event, without consulting the org's subscriptions.
// That keeps the write path free of an extra query per audit event; the Dispatcher
// resolves subscriptions when it drains the queue and drops what nobody wants.
type Recorder struct {
	inner  audit.Recorder
	outbox outboxWriter
}

// NewRecorder wraps inner so that recording an event also queues it. Every store
// that records audit events should be given this recorder — an event recorded
// through a bare audit.NewDBRecorder() is invisible to notifications.
func NewRecorder(inner audit.Recorder, outbox outboxWriter) Recorder {
	return Recorder{inner: inner, outbox: outbox}
}

func (r Recorder) Record(ctx context.Context, q database.Querier, action string, target audit.Target, metadata map[string]any) error {
	if err := r.inner.Record(ctx, q, action, target, metadata); err != nil {
		return err
	}
	// A platform-level event (no organization) has nobody to notify, and an event
	// outside the catalog cannot be subscribed to.
	if target.OrgID == nil || !Subscribable(action) {
		return nil
	}

	e := Event{
		OrgID:      *target.OrgID,
		Action:     action,
		TargetType: target.Type,
		TargetID:   target.ID,
		Metadata:   metadata,
	}
	if actor, ok := audit.ActorFromContext(ctx); ok {
		userID := actor.UserID
		e.ActorUserID = &userID
	}
	return r.outbox.Enqueue(ctx, q, e)
}
