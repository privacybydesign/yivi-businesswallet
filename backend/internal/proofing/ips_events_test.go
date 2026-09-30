package proofing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/email"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

const testWebhookSecret = "whsec-test"

// testTenantID is testOrg's IPS tenant: its own id.
var testTenantID = testOrg.ID.String()

// signIPSEvent signs body the way IPS's webhook dispatcher does.
func signIPSEvent(secret string, body []byte, at time.Time) (timestamp, signature string) {
	timestamp = strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return timestamp, "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyIPSSignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"event":"proofing.result.verified"}`)
	ts, sig := signIPSEvent(testWebhookSecret, body, now)
	if err := verifyIPSSignature(testWebhookSecret, body, ts, sig, now); err != nil {
		t.Fatalf("a valid signature = %v", err)
	}
	staleTS, staleSig := signIPSEvent(testWebhookSecret, body, now.Add(-2*ipsEventMaxSkew))
	for name, tc := range map[string]struct{ secret, ts, sig string }{
		"wrong secret": {"other", ts, sig},
		"no secret":    {"", ts, sig},
		"tampered":     {testWebhookSecret, ts, sig[:len(sig)-2] + "00"},
		"stale":        {testWebhookSecret, staleTS, staleSig},
		"bad ts":       {testWebhookSecret, "x", sig},
	} {
		if err := verifyIPSSignature(tc.secret, body, tc.ts, tc.sig, now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: %v, want ErrBadSignature", name, err)
		}
	}
}

func TestHandleIPSEventReconcilesThePushedSession(t *testing.T) {
	f := newFixture(true)
	f.settings.webhookSecret = testWebhookSecret
	f.send(t)
	f.ips.result = proofingprovider.Result{Status: proofingprovider.StatusApproved}
	body := []byte(`{"event":"proofing.result.verified","tenantId":"` + testTenantID + `","sessionId":"` + f.requests.stored.session.ID + `"}`)

	ts, sig := signIPSEvent("forged", body, time.Now())
	if err := f.svc.HandleIPSEvent(context.Background(), body, ts, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged event = %v, want ErrBadSignature", err)
	}
	if len(f.requests.outcomes) != 0 {
		t.Fatalf("a forged event recorded %v", f.requests.outcomes)
	}
	ts, sig = signIPSEvent(testWebhookSecret, body, time.Now())
	f.ips.resultErr = errors.New("unreachable")
	if err := f.svc.HandleIPSEvent(context.Background(), body, ts, sig); err == nil {
		t.Fatal("a failed IPS read must fail the event, so IPS retries it")
	}
	f.ips.resultErr = nil
	if err := f.svc.HandleIPSEvent(context.Background(), body, ts, sig); err != nil {
		t.Fatalf("HandleIPSEvent: %v", err)
	}
	if len(f.requests.outcomes) != 1 || f.requests.outcomes[0] != StatusApproved {
		t.Errorf("outcomes = %v, want approved recorded", f.requests.outcomes)
	}

	unknown := []byte(`{"event":"proofing.result.verified","tenantId":"` + testTenantID + `","sessionId":"ses-unknown"}`)
	ts, sig = signIPSEvent(testWebhookSecret, unknown, time.Now())
	if err := f.svc.HandleIPSEvent(context.Background(), unknown, ts, sig); err != nil {
		t.Errorf("an unknown session must be acknowledged, got %v", err)
	}
	otherTenant := []byte(`{"event":"proofing.result.verified","tenantId":"tenant-2","sessionId":"x"}`)
	ts, sig = signIPSEvent(testWebhookSecret, otherTenant, time.Now())
	if err := f.svc.HandleIPSEvent(context.Background(), otherTenant, ts, sig); err != nil {
		t.Errorf("an unknown tenant must be acknowledged, so IPS stops retrying; got %v", err)
	}
}

func TestHandleIPSEventBeforeTheSessionIsStoredIsRetried(t *testing.T) {
	f := newFixture(true)
	f.settings.webhookSecret = testWebhookSecret
	event := func(occurred time.Time, ref string) error {
		body := []byte(`{"event":"proofing.result.verified","occurredAt":"` + occurred.Format(time.RFC3339) +
			`","tenantId":"` + testTenantID + `","sessionId":"ses-new","clientReference":"` + ref + `"}`)
		ts, sig := signIPSEvent(testWebhookSecret, body, time.Now())
		return f.svc.HandleIPSEvent(context.Background(), body, ts, sig)
	}
	if err := event(time.Now(), uuid.NewString()); !errors.Is(err, ErrEventTooEarly) {
		t.Errorf("a fresh event for an unstored session = %v, want ErrEventTooEarly", err)
	}
	if err := event(time.Now().Add(-2*earlyEventWindow), uuid.NewString()); err != nil {
		t.Errorf("an old event for an unknown session = %v, want acknowledged", err)
	}
	if err := event(time.Now(), "not-ours"); err != nil {
		t.Errorf("an event naming no request of ours = %v, want acknowledged", err)
	}
}

func TestSessionsAskIPSToPushChanges(t *testing.T) {
	f := newFixture(true)
	f.svc.SetCallbackURL("https://wallet.example/api/v1/identity-proofing/ips-events")
	f.send(t)
	if got := f.ips.sessions[0].CallbackURL; got != "https://wallet.example/api/v1/identity-proofing/ips-events" {
		t.Errorf("session callback = %q", got)
	}
}

func TestReconcileDueWakesAtTheCap(t *testing.T) {
	f := newFixture(true)
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
	f := newFixture(true)
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
