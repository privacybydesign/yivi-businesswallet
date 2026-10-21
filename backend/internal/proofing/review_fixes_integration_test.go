//go:build integration

package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// A batch takes at most perEndpoint of one endpoint's deliveries, its oldest,
// so another org's delivery is claimed alongside a backlog.
func TestClaimCapsEachEndpoint(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, newTestCipher(t))
	busy, quiet := makeOrg(t, pool, "acme"), makeOrg(t, pool, "globex")
	for orgID, name := range map[uuid.UUID]string{busy: "busy", quiet: "quiet"} {
		if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/"+name, WebhookEvents); err != nil {
			t.Fatal(err)
		}
	}
	for range 5 {
		if err := webhooks.SendTest(ctx, busy); err != nil {
			t.Fatal(err)
		}
	}
	if err := webhooks.SendTest(ctx, quiet); err != nil {
		t.Fatal(err)
	}
	due, err := webhooks.claimDue(ctx, deliveryBatch, endpointWorkers)
	if err != nil {
		t.Fatalf("claimDue: %v", err)
	}
	per := map[string]int{}
	for _, d := range due {
		per[d.URL]++
	}
	if per["https://hooks.example.org/busy"] != endpointWorkers || per["https://hooks.example.org/quiet"] != 1 {
		t.Errorf("claimed per endpoint = %v, want %d of the backlog and the other one", per, endpointWorkers)
	}
}

// A delivery whose secret does not open is a failed attempt, so it runs out
// of attempts instead of coming back forever; the endpoint's failing streak
// counts from a delivery's first attempt, not its latest retry.
func TestWebhookSealedSecretAndFailingSince(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatal(err)
	}
	if err := webhooks.SendTest(ctx, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhooks SET secret_ciphertext = '\x00'`); err != nil {
		t.Fatal(err)
	}
	if due, err := webhooks.claimDue(ctx, deliveryBatch, endpointWorkers); err != nil || len(due) != 0 {
		t.Fatalf("claimDue = %d, %v; want none to send", len(due), err)
	}
	var attempts int
	var lastError *string
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error FROM identity_proofing_webhook_deliveries`).Scan(&attempts, &lastError); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastError == nil {
		t.Errorf("attempts = %d, last error %v; want one failed attempt recorded", attempts, lastError)
	}

	first := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries
		SET first_attempt_at = $1, last_attempt_at = $2`, first, first.Add(10*time.Hour)); err != nil {
		t.Fatal(err)
	}
	health, err := webhooks.Health(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if got := health.FailingSince; got == nil || !got.Equal(first) {
		t.Errorf("failing since = %v, want the first attempt %v", got, first)
	}
}

// The engine moving a session's expiry later moves the stored cap with it,
// never earlier, and its purge time follows.
func TestExtendSession(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	req := createStarted(t, store, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s1")
	later := req.session.ExpiresAt.Add(10 * time.Minute)
	if err := store.ExtendSession(ctx, req, "s1", later); err != nil {
		t.Fatalf("ExtendSession: %v", err)
	}
	if err := store.ExtendSession(ctx, req, "s1", req.session.ExpiresAt); err != nil {
		t.Fatalf("ExtendSession earlier: %v", err)
	}
	got := onlyRequest(t, store, orgID)
	if !got.session.ExpiresAt.Equal(later.Truncate(time.Microsecond)) {
		t.Errorf("cap = %v, want %v", got.session.ExpiresAt, later)
	}
	// Read as nil while the session runs (purgeAtColumn): the stored one counts.
	var purgeAt time.Time
	if err := pool.QueryRow(ctx, `SELECT purge_at FROM identity_proofing_requests WHERE id = $1`, req.ID).Scan(&purgeAt); err != nil {
		t.Fatal(err)
	}
	if !purgeAt.After(later) {
		t.Errorf("stored purge at = %v, want after the new cap %v", purgeAt, later)
	}
}

// A lapsed link whose sealed birth date no longer opens ends like any other:
// it does not stop every link after it from lapsing.
func TestLapseLinksPastAnUnreadableSeal(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	lapse := time.Now().Add(time.Minute)
	for _, name := range []string{"Anna Jansen", "Bram de Vries"} {
		_, hash, err := newLinkToken()
		if err != nil {
			t.Fatal(err)
		}
		in := newStoredRequest(orgID, sam, Subject{Kind: SubjectExternal, Name: name, BirthDate: testBirthDate})
		in.LinkTokenHash, in.LinkExpiresAt = hash, lapse
		if _, err := store.Create(ctx, in); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET expected_birth_date_ciphertext = '\x00'
		WHERE subject_name = 'Anna Jansen'`); err != nil {
		t.Fatal(err)
	}
	if n, err := store.LapseLinks(ctx, lapse.Add(time.Second), 10); err != nil || n != 2 {
		t.Errorf("LapseLinks = %d, %v; want both lapsed", n, err)
	}
}

// An org's erasure handling is stored; an approved erasure's kept count is
// kept on its request; an undecided identity review is purged at its retention
// like a decided one.
func TestErasureSettingsAndReviewRetention(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	settings := NewOrgSettingsStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	current, err := settings.Get(ctx, orgID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Session.ErasureHandling != ErasureReview {
		t.Errorf("a new org's erasure handling = %q, want review", current.Session.ErasureHandling)
	}
	keep := ErasureKeep
	saved, err := settings.SaveSession(ctx, orgID, SessionSettingsChange{ErasureHandling: &keep})
	if err != nil || saved.Session.ErasureHandling != ErasureKeep || saved.Session.DataRetentionDays != current.Session.DataRetentionDays {
		t.Errorf("SaveSession(keep) = %+v, %v; want keep, the rest as it was", saved.Session, err)
	}

	req := createStarted(t, store, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s1")
	if err := store.RecordOutcome(ctx, req, "s1", StatusNeedsReview, proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	got := onlyRequest(t, store, orgID)
	if got.PurgeAt == nil || got.CompletedAt == nil || !got.PurgeAt.Equal(got.CompletedAt.Add(current.Session.DataRetention())) {
		t.Errorf("review's purge at = %v, want its retention after it went to review (%v)", got.PurgeAt, got.CompletedAt)
	}

	data := NewDataRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET flow_kind = 'data_erasure' WHERE id = $1`, got.ID); err != nil {
		t.Fatal(err)
	}
	kept := 2
	if err := data.RecordDecision(ctx, got, []uuid.UUID{}, nil, &kept); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if got := onlyRequest(t, store, orgID); got.DataMatchesKept == nil || *got.DataMatchesKept != kept {
		t.Errorf("kept = %v, want %d", got.DataMatchesKept, kept)
	}
}

// A cancel whose engine call fails is rolled back: the request is not marked
// cancelled while its session still runs.
func TestCancelRollsBackWhenTheEngineRefuses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	req := createStarted(t, store, newStoredRequest(orgID, makeUser(t, pool, "sam@example.org"), externalSubject("anna@example.org")), "s1")
	refused := errors.New("the engine decided first")
	if ok, err := store.Cancel(ctx, req, func(context.Context) error { return refused }); ok || !errors.Is(err, refused) {
		t.Fatalf("Cancel = %v, %v; want the engine's refusal", ok, err)
	}
	if got := onlyRequest(t, store, orgID); got.CancelledAt != nil || got.session.EndedAt != nil {
		t.Errorf("request after a refused cancel = cancelled %v, ended %v; want neither", got.CancelledAt, got.session.EndedAt)
	}
	if ok, err := store.Cancel(ctx, req, func(context.Context) error { return nil }); !ok || err != nil {
		t.Errorf("Cancel = %v, %v; want cancelled", ok, err)
	}
}
