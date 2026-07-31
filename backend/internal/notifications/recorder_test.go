package notifications

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// recordingInner captures what the wrapped audit recorder was asked to write and
// can be made to fail.
type recordingInner struct {
	calls int
	err   error
}

func (r *recordingInner) Record(context.Context, database.Querier, string, audit.Target, map[string]any) error {
	r.calls++
	return r.err
}

// captureOutbox records the events the Recorder enqueued.
type captureOutbox struct {
	events []Event
	err    error
}

func (o *captureOutbox) Enqueue(_ context.Context, _ database.Querier, e Event) error {
	o.events = append(o.events, e)
	return o.err
}

func TestRecorderQueuesASubscribableOrgEvent(t *testing.T) {
	inner := &recordingInner{}
	outbox := &captureOutbox{}
	orgID := uuid.New()
	actorID := uuid.New()
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{UserID: actorID})

	metadata := audit.Created(map[string]any{"email": "someone@example.org"})
	err := NewRecorder(inner, outbox).Record(ctx, nil, audit.MembershipInvited,
		audit.Target{Type: audit.TargetMembership, ID: "member-1", OrgID: &orgID}, metadata)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	if inner.calls != 1 {
		t.Errorf("wrapped recorder called %d times, want 1", inner.calls)
	}
	if len(outbox.events) != 1 {
		t.Fatalf("enqueued %d events, want 1", len(outbox.events))
	}
	got := outbox.events[0]
	if got.OrgID != orgID || got.Action != audit.MembershipInvited ||
		got.TargetType != audit.TargetMembership || got.TargetID != "member-1" {
		t.Errorf("enqueued %+v, want the recorded event", got)
	}
	if got.ActorUserID == nil || *got.ActorUserID != actorID {
		t.Errorf("ActorUserID = %v, want the actor from context", got.ActorUserID)
	}
	if len(got.Metadata) == 0 {
		t.Error("the enqueued event carries no metadata")
	}
}

func TestRecorderSkipsEventsNobodyCanSubscribeTo(t *testing.T) {
	orgID := uuid.New()
	cases := map[string]struct {
		action string
		target audit.Target
	}{
		"outside the catalog": {
			audit.ThemeSettingsUpdated,
			audit.Target{Type: audit.TargetThemeSettings, ID: orgID.String(), OrgID: &orgID},
		},
		"no organization": {
			audit.MembershipInvited,
			audit.Target{Type: audit.TargetMembership, ID: "member-1"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inner := &recordingInner{}
			outbox := &captureOutbox{}

			if err := NewRecorder(inner, outbox).Record(context.Background(), nil, tc.action, tc.target, nil); err != nil {
				t.Fatalf("Record: %v", err)
			}
			if inner.calls != 1 {
				t.Errorf("wrapped recorder called %d times, want 1", inner.calls)
			}
			if len(outbox.events) != 0 {
				t.Errorf("enqueued %d events, want none", len(outbox.events))
			}
		})
	}
}

func TestRecorderQueuesNothingWhenTheAuditWriteFails(t *testing.T) {
	inner := &recordingInner{err: errors.New("audit boom")}
	outbox := &captureOutbox{}
	orgID := uuid.New()

	err := NewRecorder(inner, outbox).Record(context.Background(), nil, audit.MembershipInvited,
		audit.Target{Type: audit.TargetMembership, ID: "member-1", OrgID: &orgID}, nil)
	if err == nil {
		t.Fatal("Record succeeded, want the wrapped recorder's error")
	}
	if len(outbox.events) != 0 {
		t.Errorf("enqueued %d events, want none when the audit write failed", len(outbox.events))
	}
}

func TestRecorderReturnsTheEnqueueError(t *testing.T) {
	outbox := &captureOutbox{err: errors.New("outbox boom")}
	orgID := uuid.New()

	// The enqueue shares the caller's transaction, so a failure has to surface:
	// silently dropping it would leave the event recorded but never notified.
	err := NewRecorder(&recordingInner{}, outbox).Record(context.Background(), nil, audit.MembershipInvited,
		audit.Target{Type: audit.TargetMembership, ID: "member-1", OrgID: &orgID}, nil)
	if err == nil {
		t.Fatal("Record succeeded, want the enqueue error")
	}
}
