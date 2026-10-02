package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

func TestSessionChangedReconcilesTheSession(t *testing.T) {
	f := newFixture()
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}

	f.ips.resultErr = errors.New("unreachable")
	f.svc.SessionChanged(context.Background(), f.requests.stored.session.ID)
	if len(f.requests.outcomes) != 0 {
		t.Fatalf("a failed engine read recorded %v", f.requests.outcomes)
	}
	f.ips.resultErr = nil
	f.svc.SessionChanged(context.Background(), f.requests.stored.session.ID)
	if len(f.requests.outcomes) != 1 || f.requests.outcomes[0] != StatusApproved {
		t.Errorf("outcomes = %v, want approved recorded", f.requests.outcomes)
	}
	reads := f.ips.statusReads
	f.svc.SessionChanged(context.Background(), "ses-unknown")
	if f.ips.statusReads != reads {
		t.Errorf("an unknown session was read from the engine")
	}
}

func TestReconcileDueWakesAtTheCap(t *testing.T) {
	f := newFixture()
	f.send(t)
	deadline := f.requests.stored.session.ExpiresAt

	f.svc.now = func() time.Time { return deadline.Add(-time.Minute) }
	next, err := f.svc.ReconcileDue(context.Background())
	if err != nil || !next.Equal(deadline) || f.requests.ended != 0 {
		t.Fatalf("before the cap: next = %v, %v, ended = %d; want the cap and nothing done", next, err, f.requests.ended)
	}

	// IPS still reports the session open (its clock trails): ask again soon.
	now := deadline.Add(time.Second)
	f.svc.now = func() time.Time { return now }
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusInProgress}
	if next, err = f.svc.ReconcileDue(context.Background()); err != nil || !next.Equal(now.Add(deadlineRetry)) {
		t.Fatalf("still open at the cap: next = %v, %v; want a retry", next, err)
	}

	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusExpired}
	if next, err = f.svc.ReconcileDue(context.Background()); err != nil || !next.IsZero() || f.requests.ended != 1 {
		t.Errorf("expired at IPS: next = %v, %v, ended = %d; want it ended and nothing left", next, err, f.requests.ended)
	}
}

func TestARequestFollowsTheSendersLanguage(t *testing.T) {
	f := newFixture()
	if _, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Language: email.LocaleNL}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if got := f.ips.sessions[0].Language; got != "nl" {
		t.Errorf("IPS session language = %q, want nl (the Idem app's)", got)
	}
	if got := f.mailer.sent[0].mail.Locale; got != email.LocaleNL {
		t.Errorf("mail locale = %q, want nl", got)
	}
	_, err := f.svc.CreateRequest(context.Background(), testOrg, Requester{UserID: uuid.New(), Name: "Sam"},
		NewRequest{SubjectUserID: alex.UserID, FlowID: appFlow.ID, Language: "de"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("an unsupported language = %v, want ErrInvalidInput", err)
	}
}
