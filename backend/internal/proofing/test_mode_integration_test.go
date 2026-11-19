//go:build integration

package proofing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// An org sees only the requests of the mode it is in, and a request keeps the
// mode it was made in.
func TestTestModeKeepsRequestsApart(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	running := time.Now().Add(SessionTTL)

	real := createdRequest(t, store, hostedRequestIn(t, orgID, sam, running))
	setTestMode(t, pool, orgID, true)
	test := createdRequest(t, store, hostedRequestIn(t, orgID, sam, running))
	if real.Test || !test.Test {
		t.Fatalf("test flags = %v, %v; want false, true", real.Test, test.Test)
	}

	sees := func(mode string, shown, hidden Request) {
		t.Helper()
		if _, err := store.Get(ctx, orgID, shown.ID); err != nil {
			t.Errorf("%s: Get shown = %v", mode, err)
		}
		if _, err := store.Get(ctx, orgID, hidden.ID); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("%s: Get hidden = %v, want ErrRequestNotFound", mode, err)
		}
		if _, err := store.GetExternal(ctx, orgID, hidden.ID); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("%s: GetExternal hidden = %v, want ErrRequestNotFound", mode, err)
		}
		if got, err := store.Reread(ctx, hidden); err != nil || got.ID != hidden.ID {
			t.Errorf("%s: Reread hidden = %v; a step under way finishes on it", mode, err)
		}
		list, err := store.List(ctx, orgID, RequestFilter{})
		if err != nil {
			t.Fatalf("%s: List: %v", mode, err)
		}
		assertIDs(t, mode+" list", list, shown.ID)
		page, err := store.ListExternalPage(ctx, orgID, nil, 10)
		if err != nil {
			t.Fatalf("%s: ListExternalPage: %v", mode, err)
		}
		assertIDs(t, mode+" page", page, shown.ID)
		o, err := store.Overview(ctx, orgID, OverviewScope{})
		if err != nil {
			t.Fatalf("%s: Overview: %v", mode, err)
		}
		assertIDs(t, mode+" overview", o.Waiting, shown.ID)
		stats, err := store.Stats(ctx, orgID, nil, time.Now().Add(-time.Hour))
		if err != nil || len(stats) != 1 || stats[0].Sessions != 1 {
			t.Errorf("%s: Stats = %+v, %v; want one session", mode, stats, err)
		}
	}
	sees("test", test, real)
	setTestMode(t, pool, orgID, false)
	sees("live", real, test)
}

// A test session's webhooks say so; a real one's do not.
func TestTestModeMarksWebhooks(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", []string{EventSessionReviewOpened}); err != nil {
		t.Fatalf("Save webhook: %v", err)
	}
	real := storeInReview(t, requests, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s-real")
	setTestMode(t, pool, orgID, true)
	test := storeInReview(t, requests, newStoredRequest(orgID, sam, externalSubject("bob@example.org")), "s-test")

	for req, want := range map[uuid.UUID]bool{real.ID: false, test.ID: true} {
		var n int
		var marked bool
		if err := pool.QueryRow(ctx, `SELECT count(*), COALESCE(bool_and(payload->>'test' = 'true'), false)
			FROM identity_proofing_webhook_deliveries WHERE request_id = $1`, req).Scan(&n, &marked); err != nil {
			t.Fatal(err)
		}
		if n == 0 || marked != want {
			t.Errorf("request %s: %d deliveries, test marked %v; want some, %v", req, n, marked, want)
		}
	}
}

// What happens to a test session is marked test in the audit log.
func TestTestModeMarksAuditEvents(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	requests := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	real := storeInReview(t, requests, newStoredRequest(orgID, sam, externalSubject("anna@example.org")), "s-real")
	setTestMode(t, pool, orgID, true)
	test := storeInReview(t, requests, newStoredRequest(orgID, sam, externalSubject("bob@example.org")), "s-test")

	for req, want := range map[uuid.UUID]bool{real.ID: false, test.ID: true} {
		var n int
		var marked bool
		if err := pool.QueryRow(context.Background(), `SELECT count(*), COALESCE(bool_and(test), false) FROM audit_events
			WHERE target_type = $1 AND target_id = $2`, audit.TargetIdentityProofingRequest, req.String()).Scan(&n, &marked); err != nil {
			t.Fatal(err)
		}
		if n == 0 || marked != want {
			t.Errorf("request %s: %d events, test marked %v; want some, %v", req, n, marked, want)
		}
	}
}

func setTestMode(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, on bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE organizations SET test_mode = $2 WHERE id = $1`, orgID, on); err != nil {
		t.Fatalf("set test mode: %v", err)
	}
}
