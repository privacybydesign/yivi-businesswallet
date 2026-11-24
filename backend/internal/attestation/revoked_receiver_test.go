package attestation_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/qerds"
)

type recheckRecorder struct{ orgs []uuid.UUID }

func (r *recheckRecorder) RecheckHeld(_ context.Context, orgID uuid.UUID) (int, error) {
	r.orgs = append(r.orgs, orgID)
	return 1, nil
}

// A revocation notice makes the receiving organization re-read its status
// lists; any other message does not.
func TestRevokedReceiverRechecksOnNotice(t *testing.T) {
	t.Parallel()
	body, err := attestation.MarshalCredentialRevokedEnvelope("Yivi B.V.", "LEI", "Registration lapsed")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := &recheckRecorder{}
	receiver := attestation.NewRevokedReceiver(rec)
	org := uuid.New()

	for _, b := range []string{"hello", `{"type":"eaa-credential-offer-declined/v1","offerHash":"ab"}`} {
		if err := receiver.OnInboundMessage(context.Background(), qerds.Inbound{OrgID: org, Body: b}); err != nil {
			t.Fatalf("OnInboundMessage(%q): %v", b, err)
		}
	}
	if len(rec.orgs) != 0 {
		t.Fatalf("re-checked on another message: %v", rec.orgs)
	}
	if err := receiver.OnInboundMessage(context.Background(), qerds.Inbound{OrgID: org, Body: body}); err != nil {
		t.Fatalf("OnInboundMessage: %v", err)
	}
	if len(rec.orgs) != 1 || rec.orgs[0] != org {
		t.Fatalf("re-checked %v, want %v once", rec.orgs, org)
	}
}
