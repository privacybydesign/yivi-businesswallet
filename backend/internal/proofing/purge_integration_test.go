//go:build integration

package proofing

import (
	"context"
	"errors"
	"slices"
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

// purgeFixture is a request store over a fresh database with one org, which
// saved no settings (defaultDataRetentionDays), and one member.
type purgeFixture struct {
	pool     *pgxpool.Pool
	requests *RequestStore
	settings *OrgSettingsStore
	orgID    uuid.UUID
	sam      uuid.UUID
}

func newPurgeFixture(t *testing.T) purgeFixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	return purgeFixture{
		pool: pool, requests: NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t)),
		settings: NewOrgSettingsStore(pool, audit.NopRecorder{}),
		orgID:    makeOrg(t, pool, "acme"), sam: makeUser(t, pool, "sam@example.org"),
	}
}

// subject is an external person: the purge time of a member's request does not
// follow the org's retention.
func (f purgeFixture) subject(name, email, birthDate string) Subject {
	return Subject{Kind: SubjectExternal, Name: name, Email: email, BirthDate: birthDate}
}

// settledAgo stores a request in the org, approves it, and moves its outcome
// back by age (a Postgres interval), as the pruner will see it.
func (f purgeFixture) settledAgo(t *testing.T, in NewStoredRequest, sessionID, age string) Request {
	t.Helper()
	return f.decidedAgo(t, in, sessionID, StatusApproved, age)
}

// decidedAgo is settledAgo with the outcome status.
func (f purgeFixture) decidedAgo(t *testing.T, in NewStoredRequest, sessionID string, status Status, age string) Request {
	t.Helper()
	ctx := context.Background()
	req, err := f.requests.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := f.requests.AttachSession(ctx, req, attachedSession(sessionID)); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if err := f.requests.RecordOutcome(ctx, req, sessionID, status,
		proofingprovider.Result{Status: proofingprovider.Status(status)}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - $2::interval
		WHERE id = $1`, req.ID, age); err != nil {
		t.Fatalf("age the request: %v", err)
	}
	// A raw write: the store would set purge_at in the same transaction.
	if err := refreshPurgeAt(ctx, f.pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	return req
}

// purgeDue is the ids of the requests due for a purge.
func (f purgeFixture) purgeDue(t *testing.T) []uuid.UUID {
	t.Helper()
	due, err := f.requests.ListPurgeDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListPurgeDue: %v", err)
	}
	ids := make([]uuid.UUID, len(due))
	for i, req := range due {
		ids[i] = req.ID
	}
	return ids
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

// An org's changed retention moves the purge of its settled external requests,
// and only those: not another org's, not a flow's own retention, not a member's
// request, which keeps the wallet's own. The API no longer changes it (it is a
// fixed default), but the store's rule still holds for a value an org kept.
func TestPurgeDueFollowsRetention(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	otherOrg := makeOrg(t, f.pool, "globex")
	const tenDays = "10 days"
	month := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 30, ErasureHandling: ErasureReview}
	for _, org := range []uuid.UUID{f.orgID, otherOrg} {
		if _, err := f.settings.SaveSession(ctx, org, sessionChange(month)); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}

	plain := f.settledAgo(t, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")), "s1", tenDays)
	flowOwn := newStoredRequest(f.orgID, f.sam, f.subject("", "b@example.org", ""))
	flowOwn.Flow.RetentionOverrideSeconds = int((365 * 24 * time.Hour).Seconds())
	f.settledAgo(t, flowOwn, "s2", tenDays)
	member := f.settledAgo(t, newStoredRequest(f.orgID, f.sam, memberSubject(Member{UserID: f.sam, Name: "Sam", Email: "sam@example.org"})), "s3", tenDays)
	f.settledAgo(t, newStoredRequest(otherOrg, f.sam, f.subject("", "c@example.org", "")), "s4", tenDays)

	if due := f.purgeDue(t); len(due) != 0 {
		t.Fatalf("purge due 10 days into 30 = %v; want none", due)
	}
	week := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 7, ErasureHandling: ErasureReview}
	if _, err := f.settings.SaveSession(ctx, f.orgID, sessionChange(week)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if due := f.purgeDue(t); !slices.Equal(due, []uuid.UUID{plain.ID}) {
		t.Errorf("purge due 10 days into 7 = %v; want only the org's request without a retention of its own", due)
	}
	var purgeAt, completedAt *time.Time
	if err := f.pool.QueryRow(ctx, `SELECT purge_at, completed_at FROM identity_proofing_requests WHERE id = $1`, member.ID).
		Scan(&purgeAt, &completedAt); err != nil || purgeAt == nil || completedAt == nil || purgeAt.Sub(*completedAt) != proofedNameRetention {
		t.Errorf("a member's purge time = %v after %v, %v; want the wallet's own retention", purgeAt, completedAt, err)
	}

	// Back to a longer retention takes it out of the due ones again.
	if _, err := f.settings.SaveSession(ctx, f.orgID, sessionChange(month)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if due := f.purgeDue(t); len(due) != 0 {
		t.Errorf("purge due 10 days into 30 again = %v; want none", due)
	}
}

// A member's request without a flow retention is purged
// proofedNameRetention after it settled, on the purge an external person's takes:
// its diplomas and the audit fields naming its subject go with it.
func TestPurgeDueTakesMemberRequests(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	makeMember(t, f.pool, f.orgID, f.sam, "member")
	member, err := f.requests.Member(ctx, f.orgID, f.sam)
	if err != nil {
		t.Fatalf("Member: %v", err)
	}
	in := newStoredRequest(f.orgID, f.sam, memberSubject(member))
	in.Diplomas = DiplomasRequired
	req := createStarted(t, f.requests, in, "s1")
	if err := f.requests.RecordOutcome(ctx, req, "s1", StatusApproved, proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	req, err = f.requests.Get(ctx, f.orgID, req.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if req.PurgeAt == nil || req.CompletedAt == nil || !req.PurgeAt.Equal(req.CompletedAt.Add(proofedNameRetention)) {
		t.Errorf("purge time = %v, want completed %v plus %v", req.PurgeAt, req.CompletedAt, proofedNameRetention)
	}
	extract := Diploma{
		DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", Profiles: []string{},
		Institution: "Hogeschool Utrecht", PlaceOfIssue: "Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
		DocumentNumber: "2896311",
	}
	if _, added, err := NewDiplomaStore(f.pool, audit.NewDBRecorder()).Add(ctx, req, extract); err != nil || !added {
		t.Fatalf("Add diploma = %v, %v", added, err)
	}
	if due, err := f.requests.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Fatalf("purge due on settling = %d, %v; want none", len(due), err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - $2::interval
		WHERE id = $1`, req.ID, (proofedNameRetention + time.Hour).String()); err != nil {
		t.Fatalf("age the request: %v", err)
	}
	// A raw write: the store would set purge_at in the same transaction.
	if err := refreshPurgeAt(ctx, f.pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	due, err := f.requests.ListPurgeDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != req.ID {
		t.Fatalf("purge due past its retention = %+v, %v; want the member's request", due, err)
	}
	if err := f.requests.Purge(ctx, due[0], nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	var diplomas, leaks int
	if err := f.pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM identity_proofing_request_diplomas WHERE request_id = $1),
			(SELECT count(*) FROM audit_events WHERE target_id = $1::text AND metadata::text ~ $2)`,
		req.ID, `Verpleegkunde|2896311|`+member.Email).Scan(&diplomas, &leaks); err != nil {
		t.Fatalf("read leftovers: %v", err)
	}
	if diplomas != 0 || leaks != 0 {
		t.Errorf("after purge: %d diplomas, %d audit events naming the subject or diploma; want none", diplomas, leaks)
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
		WHERE r.purged_at IS NULL AND r.purge_at <= now()
			AND (r.purge_retry_after IS NULL OR r.purge_retry_after <= now())
		ORDER BY r.purge_at LIMIT 10`)
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

// A request that fails to purge, or whose sealed fields do not open, is
// deferred for longer after each failure, up to purgeRetryMax, and does not
// hold up the requests due after it.
func TestPurgeDueDefersFailures(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	due := func(session string, overdue time.Duration) Request {
		t.Helper()
		req, err := f.requests.Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
		if err != nil {
			t.Fatalf("Create %s: %v", session, err)
		}
		if ok, err := f.requests.AttachSession(ctx, req, attachedSession(session)); err != nil || !ok {
			t.Fatalf("AttachSession %s = %v, %v", session, ok, err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET purge_at = now() - $2::interval
			WHERE id = $1`, req.ID, overdue.String()); err != nil {
			t.Fatalf("make %s due: %v", session, err)
		}
		return req
	}
	unreadable, failing, next := due("s1", 3*time.Hour), due("s2", 2*time.Hour), due("s3", time.Hour)
	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET ips_session_token_ciphertext = '\x00'
		WHERE id = $1`, unreadable.ID); err != nil {
		t.Fatalf("break the sealed token: %v", err)
	}
	retryIn := func(id uuid.UUID) (int, time.Duration) {
		t.Helper()
		var attempts int
		var wait float64
		if err := f.pool.QueryRow(ctx, `SELECT purge_attempts, EXTRACT(EPOCH FROM purge_retry_after - now())
			FROM identity_proofing_requests WHERE id = $1`, id).Scan(&attempts, &wait); err != nil {
			t.Fatalf("read the purge wait: %v", err)
		}
		return attempts, time.Duration(wait * float64(time.Second))
	}

	got, err := f.requests.ListPurgeDue(ctx, 1)
	if err != nil || len(got) != 0 {
		t.Fatalf("first batch = %+v, %v; want the unreadable request left out", got, err)
	}
	if attempts, wait := retryIn(unreadable.ID); attempts != 1 || (wait-purgeRetryBase).Abs() > time.Minute {
		t.Errorf("unreadable request: %d attempts, retry in %v; want 1, %v", attempts, wait, purgeRetryBase)
	}
	if got, err = f.requests.ListPurgeDue(ctx, 1); err != nil || len(got) != 1 || got[0].ID != failing.ID {
		t.Fatalf("second batch = %+v, %v; want the failing request", got, err)
	}
	for range 2 {
		if err := f.requests.DeferPurge(ctx, failing.ID); err != nil {
			t.Fatalf("DeferPurge: %v", err)
		}
	}
	if attempts, wait := retryIn(failing.ID); attempts != 2 || (wait-2*purgeRetryBase).Abs() > time.Minute {
		t.Errorf("failing request: %d attempts, retry in %v; want 2, %v", attempts, wait, 2*purgeRetryBase)
	}
	if got, err = f.requests.ListPurgeDue(ctx, 1); err != nil || len(got) != 1 || got[0].ID != next.ID {
		t.Fatalf("third batch = %+v, %v; want the request due after the deferred ones", got, err)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE identity_proofing_requests SET purge_attempts = 1000 WHERE id = $1`, failing.ID); err != nil {
		t.Fatalf("fail often: %v", err)
	}
	if err := f.requests.DeferPurge(ctx, failing.ID); err != nil {
		t.Fatalf("DeferPurge after many failures: %v", err)
	}
	if _, wait := retryIn(failing.ID); (wait - purgeRetryMax).Abs() > time.Minute {
		t.Errorf("retry after many failures in %v; want %v", wait, purgeRetryMax)
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
	if err := f.requests.Purge(ctx, req, nil); err != nil {
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
	if err := f.requests.Purge(ctx, stale, nil); !errors.Is(err, errPurgeSessionMoved) {
		t.Fatalf("Purge of the stale read = %v, want %v", err, errPurgeSessionMoved)
	}
	fresh, err := f.requests.Get(ctx, f.orgID, stale.ID)
	if err != nil || fresh.PurgedAt != nil {
		t.Fatalf("after the refused purge = %+v, %v; want it kept", fresh, err)
	}
	if err := f.requests.Purge(ctx, fresh, nil); err != nil {
		t.Errorf("Purge of the fresh read = %v", err)
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
			_, err := f.requests.Cancel(ctx, req, nil)
			return err
		}},
		{name: "RecordOutcome", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.RecordOutcome(ctx, req, "s1", StatusApproved, approved)
		}},
		// An identity review counts its retention from going there, as a decision would.
		{name: "RecordOutcome review", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview, review)
		}},
		{name: "LapseLinks", hosted: true, write: func(f purgeFixture, _ Request) error {
			_, err := f.requests.LapseLinks(ctx, time.Now(), 10)
			return err
		}},
		{name: "Purge", started: true, write: func(f purgeFixture, req Request) error {
			return f.requests.Purge(ctx, req, nil)
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

// An unsent request's purge time is its link's lapse plus its org's retention,
// the fixed defaultDataRetentionDays for an org that saved no settings.
func TestPurgeAtFollowsTheLink(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	req, err := f.requests.Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	settings, err := f.settings.Get(ctx, f.orgID)
	if err != nil || settings.Session.DataRetentionDays != defaultDataRetentionDays {
		t.Fatalf("settings = %+v, %v; want the default retention", settings.Session, err)
	}
	days := settings.Session.DataRetentionDays
	var ok bool
	if err := f.pool.QueryRow(ctx, `SELECT purge_at = link_expires_at + make_interval(days => $2)
		FROM identity_proofing_requests WHERE id = $1`, req.ID, days).Scan(&ok); err != nil || !ok {
		t.Errorf("purge time is the link's lapse plus %d days = %v, %v", days, ok, err)
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

// A retention change waits for an external request being created for the org,
// so that request's purge time follows the new retention rather than keeping
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
	// Create's writes, held open: its purge time read from the default week.
	req, err := NewRequestStore(tx, audit.NopRecorder{}, newTestCipher(t)).
		Create(ctx, newStoredRequest(f.orgID, f.sam, f.subject("", "a@example.org", "")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	const monthDays = 30
	month := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: monthDays, ErasureHandling: ErasureReview}
	saved := make(chan error, 1)
	go func() {
		_, err := f.settings.SaveSession(ctx, f.orgID, sessionChange(month))
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
		t.Fatalf("SaveSession: %v", saveErr)
	}
	var ok bool
	if err := f.pool.QueryRow(ctx, `SELECT purge_at = link_expires_at + make_interval(days => $2)
		FROM identity_proofing_requests WHERE id = $1`, req.ID, monthDays).Scan(&ok); err != nil || !ok {
		t.Errorf("purge time on the new retention = %v, %v; want the link's lapse plus %d days", ok, err, monthDays)
	}
}

// A request write that sends a webhook, running while the org's retention
// changes, finishes with it rather than deadlocking: the retention change waits
// on the request's row, and the webhook's foreign-key check on the org's row
// does not wait on the retention change.
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
	const monthDays = 30
	month := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: monthDays, ErasureHandling: ErasureReview}
	saved := make(chan error, 1)
	go func() {
		_, err := f.settings.SaveSession(ctx, f.orgID, sessionChange(month))
		saved <- err
	}()
	done, saveErr := awaitLockWait(t, f.pool, saved)
	// Its second half: the webhook's insert checks the org's row.
	if err := enqueueRequestWebhook(ctx, tx, req, EventSessionCancelled, sessionEventData(req, StatusCancelled)); err != nil {
		t.Fatalf("enqueue webhook: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !done {
		saveErr = <-saved
	}
	if saveErr != nil {
		t.Fatalf("SaveSession: %v", saveErr)
	}
}
