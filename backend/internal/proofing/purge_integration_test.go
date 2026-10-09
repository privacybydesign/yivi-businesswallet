//go:build integration

package proofing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// purgeFixture is a request store over a fresh database with one org, one
// member and one customer (30 days' retention).
type purgeFixture struct {
	pool      *pgxpool.Pool
	requests  *RequestStore
	customers *CustomerStore
	orgID     uuid.UUID
	sam       uuid.UUID
	customer  Customer
}

func newPurgeFixture(t *testing.T) purgeFixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	f := purgeFixture{
		pool: pool, requests: NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t)),
		customers: NewCustomerStore(pool, audit.NopRecorder{}),
		orgID:     makeOrg(t, pool, "acme"), sam: makeUser(t, pool, "sam@example.org"),
	}
	var err error
	if f.customer, err = f.customers.Create(context.Background(), f.orgID, f.sam, "Initech"); err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return f
}

func (f purgeFixture) subject(name, email, birthDate string) Subject {
	return Subject{CustomerID: &f.customer.ID, Name: name, Email: email, BirthDate: birthDate}
}

// birthDateKept reports whether request id still holds its expected birth date.
func (f purgeFixture) birthDateKept(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var kept bool
	if err := f.pool.QueryRow(context.Background(), `SELECT expected_birth_date_ciphertext IS NOT NULL
		FROM identity_proofing_requests WHERE id = $1`, id).Scan(&kept); err != nil {
		t.Fatalf("read birth date: %v", err)
	}
	return kept
}

// A customer's changed retention moves the purge of its settled requests.
func TestPurgeDueFollowsRetention(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req := createStarted(t, f.requests, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")), "s1")
	if err := f.requests.RecordOutcome(ctx, req, "s1", StatusApproved, proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '10 days'
		WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the request: %v", err)
	}
	// A raw write: the store would set purge_at in the same transaction.
	if err := refreshPurgeAt(ctx, f.pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	if due, err := f.requests.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("purge due 10 days into 30 = %d, %v; want none", len(due), err)
	}
	week := CustomerSettings{SessionTTL: SessionTTL, DataRetentionDays: 7}
	if _, err := f.customers.SaveSettings(ctx, f.orgID, f.customer.ID, week); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if due, err := f.requests.ListPurgeDue(ctx, 10); err != nil || len(due) != 1 || due[0].ID != req.ID {
		t.Errorf("purge due 10 days into 7 = %+v, %v; want the request", due, err)
	}
}

// The purge reads its due requests over an index, not a scan of every
// unpurged row.
func TestPurgeDueIsIndexed(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	conn, err := f.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("disable seq scans: %v", err)
	}
	rows, err := conn.Query(ctx, `EXPLAIN SELECT r.id FROM identity_proofing_requests r
		WHERE r.purged_at IS NULL AND r.purge_at <= now() ORDER BY r.purge_at LIMIT 10`)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		plan.WriteString(line + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !strings.Contains(plan.String(), "identity_proofing_requests_purge_due_idx") {
		t.Errorf("plan does not use the purge index:\n%s", plan.String())
	}
}

// A purge drops a reviewer's free-text reason from the audit log: it may
// name the subject.
func TestPurgeStripsReviewReason(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	const reason = "Anna Jansen's photo matched on a second look"
	req := createStarted(t, f.requests, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")), "s1")
	if err := f.requests.RecordReviewDecision(ctx, req, StatusApproved, reason, ""); err != nil {
		t.Fatalf("RecordReviewDecision: %v", err)
	}
	leaks := func() int {
		t.Helper()
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%' || $1 || '%'`,
			reason).Scan(&n); err != nil {
			t.Fatalf("count reasons: %v", err)
		}
		return n
	}
	if n := leaks(); n != 1 {
		t.Fatalf("audit events with the reason before purge = %d, want 1", n)
	}
	if err := f.requests.Purge(ctx, req); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n := leaks(); n != 0 {
		t.Errorf("audit events with the reason after purge = %d, want none", n)
	}
}

// A session that ends undecided drops the expected birth date: no match can
// follow.
func TestEndSessionDropsBirthDate(t *testing.T) {
	f := newPurgeFixture(t)
	req := createStarted(t, f.requests, newStoredRequest(f.orgID, f.sam,
		f.subject("Anna de Vries", "anna@example.org", "1990-04-12")), "s1")
	if !f.birthDateKept(t, req.ID) {
		t.Fatal("no birth date stored to drop")
	}
	if err := f.requests.EndSession(context.Background(), req, "s1", proofingprovider.StatusExpired, ""); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if f.birthDateKept(t, req.ID) {
		t.Error("birth date kept after the session ended")
	}
}

// A hosted link that lapses unstarted drops the expected birth date.
func TestLapseLinksDropsBirthDate(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	in := newStoredRequest(f.orgID, f.sam, f.subject("Anna de Vries", "anna@example.org", "1990-04-12"))
	in.LinkTokenHash, in.LinkExpiresAt = []byte("link-hash"), time.Now().Add(-time.Minute)
	req, err := f.requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !f.birthDateKept(t, req.ID) {
		t.Fatal("no birth date stored to drop")
	}
	if n, err := f.requests.LapseLinks(ctx, time.Now(), 10); err != nil || n != 1 {
		t.Fatalf("LapseLinks = %d, %v; want the link lapsed", n, err)
	}
	if f.birthDateKept(t, req.ID) {
		t.Error("birth date kept after the link lapsed")
	}
}

// A purge of a request read before its session was attached is refused, so
// that session is erased in the engine before the row is marked purged.
func TestPurgeRefusesStaleSession(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	stale, err := f.requests.Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := f.requests.AttachSession(ctx, stale, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if err := f.requests.Purge(ctx, stale); !errors.Is(err, errPurgeSessionMoved) {
		t.Fatalf("Purge of the stale read = %v, want %v", err, errPurgeSessionMoved)
	}
	fresh, err := f.requests.Get(ctx, f.orgID, stale.ID)
	if err != nil || fresh.PurgedAt != nil {
		t.Fatalf("after the refused purge = %+v, %v; want it kept", fresh, err)
	}
	if err := f.requests.Purge(ctx, fresh); err != nil {
		t.Errorf("Purge of the fresh read = %v", err)
	}
}

// Removing a customer refuses while one of its requests is unpurged (one sent
// while the removal ran), and goes through once it is purged.
func TestRemoveRefusesUnpurged(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req, err := f.requests.Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.customers.Remove(ctx, f.orgID, f.customer.ID); !errors.Is(err, ErrCustomerSessionsLeft) {
		t.Fatalf("Remove with an unpurged request = %v, want %v", err, ErrCustomerSessionsLeft)
	}
	if _, err := f.customers.Get(ctx, f.orgID, f.customer.ID); err != nil {
		t.Fatalf("customer after the refused removal = %v, want it kept", err)
	}
	if err := f.requests.Purge(ctx, req); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if err := f.customers.Remove(ctx, f.orgID, f.customer.ID); err != nil {
		t.Errorf("Remove once purged = %v", err)
	}
}

// A customer with a session waiting for review is not paused: the check runs
// in the pause's own transaction.
func TestCustomerPauseChecksReviews(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req := createStarted(t, f.requests, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")), "s1")
	if err := f.requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if _, err := f.customers.SetStatus(ctx, f.orgID, f.customer.ID, CustomerPaused); !errors.Is(err, ErrCustomerHasOpenReviews) {
		t.Fatalf("pause with an open review = %v, want %v", err, ErrCustomerHasOpenReviews)
	}
	if c, err := f.customers.Get(ctx, f.orgID, f.customer.ID); err != nil || c.Paused() {
		t.Errorf("customer = paused %v, %v; want active", c.Paused(), err)
	}
}

// purgeAtDrift reads request id's stored purge_at and the one the rule
// computes from the row as it stands (refreshPurgeAt, rolled back): a write
// that skipped refreshPurgeAt leaves them apart.
func (f purgeFixture) purgeAtDrift(t *testing.T, id uuid.UUID) (stored, want *time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Errorf("rollback: %v", err)
		}
	}()
	read := func() *time.Time {
		var at *time.Time
		if err := tx.QueryRow(ctx, `SELECT purge_at FROM identity_proofing_requests WHERE id = $1`, id).Scan(&at); err != nil {
			t.Fatalf("read purge time: %v", err)
		}
		return at
	}
	stored = read()
	if err := refreshPurgeAt(ctx, tx, id); err != nil {
		t.Fatalf("refreshPurgeAt: %v", err)
	}
	return stored, read()
}

// Every store write to an input of the purge time stores it in the same
// transaction: what is stored is what the rule computes from the row.
func TestPurgeAtSetOnEveryWrite(t *testing.T) {
	ctx := context.Background()
	approved := proofingprovider.Result{Status: proofingprovider.StatusApproved}
	review := proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}
	for _, tc := range []struct {
		name    string
		hosted  bool // sent as a hosted link that already lapsed, unstarted
		started bool // its session attached
		write   func(f purgeFixture, req Request) error
		cleared bool // the write leaves no purge time
	}{
		{name: "Create", write: func(purgeFixture, Request) error { return nil }},
		{name: "AttachSession", write: func(f purgeFixture, req Request) error {
			_, err := f.requests.AttachSession(ctx, req, attachedSession("s1"))
			return err
		}},
		{name: "MarkStarted", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.MarkStarted(ctx, req, "s1", "")
		}},
		{name: "EndSession", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.EndSession(ctx, req, "s1", proofingprovider.StatusExpired, "")
		}},
		{name: "Cancel", started: true, write: func(f purgeFixture, req Request) error {
			_, err := f.requests.Cancel(ctx, req)
			return err
		}},
		{name: "RecordOutcome", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.RecordOutcome(ctx, req, "s1", StatusApproved, approved)
		}},
		{name: "RecordOutcome review", started: true, cleared: true, write: func(f purgeFixture, req Request) error {
			return f.requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview, review)
		}},
		{name: "LapseLinks", hosted: true, write: func(f purgeFixture, _ Request) error {
			_, err := f.requests.LapseLinks(ctx, time.Now(), 10)
			return err
		}},
		{name: "Purge", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.Purge(ctx, req)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPurgeFixture(t)
			in := newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", ""))
			if tc.hosted {
				in.LinkTokenHash, in.LinkExpiresAt = []byte("link-hash"), time.Now().Add(-time.Minute)
			}
			var req Request
			if tc.started {
				req = createStarted(t, f.requests, in, "s1")
			} else {
				var err error
				if req, err = f.requests.Create(ctx, in); err != nil {
					t.Fatalf("Create: %v", err)
				}
			}
			if err := tc.write(f, req); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			stored, want := f.purgeAtDrift(t, req.ID)
			if (stored == nil) != tc.cleared || (want == nil) != tc.cleared {
				t.Fatalf("purge time = %v, rule %v; want cleared %v", stored, want, tc.cleared)
			}
			if stored != nil && !stored.Equal(*want) {
				t.Errorf("stored purge time %v, rule %v", stored, want)
			}
		})
	}
}

// An unsent request's purge time is its link's lapse plus its customer's
// retention.
func TestPurgeAtFollowsTheLink(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req, err := f.requests.Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var ok bool
	if err := f.pool.QueryRow(ctx, `SELECT purge_at = link_expires_at + make_interval(days => $2)
		FROM identity_proofing_requests WHERE id = $1`, req.ID, f.customer.Settings.DataRetentionDays).Scan(&ok); err != nil || !ok {
		t.Errorf("purge time is the link's lapse plus %d days = %v, %v", f.customer.Settings.DataRetentionDays, ok, err)
	}
}

// waitForLockWait is how long awaitLockWait waits for the retention change to
// block on the open transaction.
const waitForLockWait = 10 * time.Second

// lockWaitPoll is how often awaitLockWait looks again.
const lockWaitPoll = 10 * time.Millisecond

// awaitLockWait returns once a statement in the test's database waits on a
// lock, or once result (the call that should wait) finished first: done, with
// its error.
func awaitLockWait(t *testing.T, pool *pgxpool.Pool, result <-chan error) (bool, error) {
	t.Helper()
	deadline := time.Now().Add(waitForLockWait)
	for {
		var waiting int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("read lock waits: %v", err)
		}
		if waiting > 0 {
			return false, nil
		}
		if time.Now().After(deadline) {
			t.Fatal("the retention change neither waited nor finished")
		}
		select {
		case err := <-result:
			return true, err
		case <-time.After(lockWaitPoll):
		}
	}
}

// A retention change waits for a request being created for the customer, so
// that request's purge time follows the new retention rather than keeping
// the old one it read.
func TestRetentionWaitsForInsert(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	}()
	// Create's writes, held open: its purge time read from the 30 days.
	req, err := NewRequestStore(tx, audit.NopRecorder{}, newTestCipher(t)).
		Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const weekDays = 7
	week := CustomerSettings{SessionTTL: SessionTTL, DataRetentionDays: weekDays}
	saved := make(chan error, 1)
	go func() {
		_, err := f.customers.SaveSettings(ctx, f.orgID, f.customer.ID, week)
		saved <- err
	}()
	done, saveErr := awaitLockWait(t, f.pool, saved)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !done {
		saveErr = <-saved
	}
	if saveErr != nil {
		t.Fatalf("SaveSettings: %v", saveErr)
	}
	var ok bool
	if err := f.pool.QueryRow(ctx, `SELECT purge_at = link_expires_at + make_interval(days => $2)
		FROM identity_proofing_requests WHERE id = $1`, req.ID, weekDays).Scan(&ok); err != nil || !ok {
		t.Errorf("purge time on the new retention = %v, %v; want the link's lapse plus %d days", ok, err, weekDays)
	}
}

// A request write that sends a webhook, running while the customer's
// retention changes, finishes with it rather than deadlocking: the retention
// change waits on the request's row, and the webhook's foreign-key check on
// the customer's row does not wait on the retention change.
func TestRetentionBesideWrite(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req := createStarted(t, f.requests, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")), "s1")
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	}()
	// Cancel's first half, held open: the request's row is written.
	if _, err := tx.Exec(ctx, `UPDATE identity_proofing_requests SET cancelled_at = now(), updated_at = now()
		WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	const weekDays = 7
	week := CustomerSettings{SessionTTL: SessionTTL, DataRetentionDays: weekDays}
	saved := make(chan error, 1)
	go func() {
		_, err := f.customers.SaveSettings(ctx, f.orgID, f.customer.ID, week)
		saved <- err
	}()
	done, saveErr := awaitLockWait(t, f.pool, saved)
	// Its second half: the webhook's insert checks the customer's row.
	if err := enqueueWebhook(ctx, tx, f.orgID, &f.customer.ID, EventSessionCancelled, &req.ID,
		sessionEventData(req, StatusCancelled)); err != nil {
		t.Fatalf("enqueue webhook: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !done {
		saveErr = <-saved
	}
	if saveErr != nil {
		t.Fatalf("SaveSettings: %v", saveErr)
	}
}
