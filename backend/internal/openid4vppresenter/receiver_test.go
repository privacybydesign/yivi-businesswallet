package openid4vppresenter

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

func requestBody(t *testing.T) string {
	t.Helper()
	body, err := MarshalPresentationRequestEnvelope("Acme", testClientID, "https://v/req", "")
	if err != nil {
		t.Fatalf("marshal presentation request: %v", err)
	}
	return body
}

func TestReceiverQueuesPresentationRequest(t *testing.T) {
	svc, store, _ := newTestService()
	rec := NewReceiver(svc)

	orgID, msgID := uuid.New(), uuid.New()
	if err := rec.OnInboundMessage(context.Background(), qerds.Inbound{OrgID: orgID, MessageID: msgID, Body: requestBody(t)}); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}

	queued := store.forOrg(orgID)
	if len(queued) != 1 {
		t.Fatalf("expected 1 queued request, got %d", len(queued))
	}
	if queued[0].Status != StatusOrgSelected {
		t.Errorf("status = %q, want %q", queued[0].Status, StatusOrgSelected)
	}
	if queued[0].SourceMessageID == nil || *queued[0].SourceMessageID != msgID {
		t.Errorf("SourceMessageID = %v, want %v", queued[0].SourceMessageID, msgID)
	}
}

func TestReceiverIgnoresNonPresentationRequest(t *testing.T) {
	svc, store, _ := newTestService()
	rec := NewReceiver(svc)

	orgID := uuid.New()
	if err := rec.OnInboundMessage(context.Background(), qerds.Inbound{OrgID: orgID, MessageID: uuid.New(), Body: "just a human message"}); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if queued := store.forOrg(orgID); len(queued) != 0 {
		t.Errorf("non-request message must not queue anything (%d queued)", len(queued))
	}
}

// A re-delivered message must not queue a second request, and must not reopen a
// decision already made.
func TestReceiverIdempotentOnRedelivery(t *testing.T) {
	svc, store, _ := newTestService()
	rec := NewReceiver(svc)

	ctx := context.Background()
	orgID, msgID := uuid.New(), uuid.New()
	body := requestBody(t)
	if err := rec.OnInboundMessage(ctx, qerds.Inbound{OrgID: orgID, MessageID: msgID, Body: body}); err != nil {
		t.Fatalf("first OnInboundMessage: %v", err)
	}
	queued := store.forOrg(orgID)
	if len(queued) != 1 {
		t.Fatalf("after first delivery: %d queued, want 1", len(queued))
	}
	// Simulate the governance layer (#113) having already decided this one —
	// whatever mechanism eventually owns that decision, a re-delivery must not
	// reopen it.
	decided := queued[0]
	decided.Status = StatusDenied
	store.orgTransactions[decided.ID] = decided

	if err := rec.OnInboundMessage(ctx, qerds.Inbound{OrgID: orgID, MessageID: msgID, Body: body}); err != nil {
		t.Fatalf("second OnInboundMessage: %v", err)
	}
	queued = store.forOrg(orgID)
	if len(queued) != 1 || queued[0].Status != StatusDenied {
		t.Fatalf("re-delivery reopened or duplicated the decided request: %+v", queued)
	}
}

// A request whose invocation cannot be validated is not queued, but the QERDS
// message is not treated as an error either — nothing an admin could act on.
func TestReceiverSwallowsInvalidInvocation(t *testing.T) {
	svc, store, _ := newTestService()
	rec := NewReceiver(svc)
	// A well-formed envelope (so it is not simply ignored as "not a presentation
	// request") whose request_uri_method the service cannot execute.
	body, err := MarshalPresentationRequestEnvelope("Acme", testClientID, "https://v/req", "put")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	orgID := uuid.New()
	if err := rec.OnInboundMessage(context.Background(), qerds.Inbound{OrgID: orgID, MessageID: uuid.New(), Body: body}); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if queued := store.forOrg(orgID); len(queued) != 0 {
		t.Errorf("an unvalidatable invocation must not be queued (%d pending)", len(queued))
	}
}

// isInvocationError must recognise every sentinel Service.validate can return so
// a broken invocation is logged and dropped rather than surfaced as a qerds
// inbound-consumer failure.
func TestIsInvocationErrorCoversValidationSentinels(t *testing.T) {
	for _, err := range []error{ErrInvalidRequest, ErrInvalidRequestURIMethod, ErrInvalidRequestObject, ErrRequestURIUnreachable} {
		if !isInvocationError(err) {
			t.Errorf("isInvocationError(%v) = false, want true", err)
		}
	}
	if isInvocationError(errors.New("boom")) {
		t.Error("isInvocationError must not swallow an unrelated (store/infra) error")
	}
}
