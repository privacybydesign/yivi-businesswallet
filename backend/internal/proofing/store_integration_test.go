//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// testEncryptionKey is a throwaway AES-256 key (hex 32 bytes).
const testEncryptionKey = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

func newTestCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	cipher, err := crypto.NewCipher(testEncryptionKey)
	if err != nil {
		t.Fatalf("build cipher: %v", err)
	}
	return cipher
}

func makeOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		slug, slug, "kvk-"+slug, "NL.KVK."+slug, slug+"@qerds.localhost").Scan(&id)
	if err != nil {
		t.Fatalf("create org %q: %v", slug, err)
	}
	return id
}

func makeUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO users (email, given_names, last_name) VALUES ($1, $2, $3) RETURNING id",
		email, "Sam", "de Vries").Scan(&id)
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}
	return id
}

func auditCount(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_events WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	return n
}

func TestSettingsStoreReplacesFlowSelection(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	empty, err := store.FlowSelection(ctx, orgID)
	if err != nil || len(empty.FlowIDs) != 0 || empty.DefaultFlowID != "" {
		t.Fatalf("FlowSelection before any = %+v, %v; want empty", empty, err)
	}
	for _, sel := range []FlowSelection{
		{FlowIDs: []string{"f1", "f2"}, DefaultFlowID: "f1"},
		{FlowIDs: []string{"f2", "f3"}, DefaultFlowID: "f3"},
	} {
		if err := store.SaveFlowSelection(ctx, orgID, sel); err != nil {
			t.Fatalf("SaveFlowSelection %+v: %v", sel, err)
		}
	}
	got, err := store.FlowSelection(ctx, orgID)
	if err != nil || !slices.Equal(got.FlowIDs, []string{"f2", "f3"}) || got.DefaultFlowID != "f3" {
		t.Errorf("FlowSelection = %+v, %v; want the second selection only", got, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingFlowsConfigured); n != 2 {
		t.Errorf("flows_configured audits = %d, want 2", n)
	}
}

func makeMember(t *testing.T, pool *pgxpool.Pool, orgID, userID uuid.UUID, role string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO memberships (user_id, organization_id, role) VALUES ($1, $2, $3)`, userID, orgID, role); err != nil {
		t.Fatalf("add membership: %v", err)
	}
}

func memberSubject(m Member) Subject {
	return Subject{UserID: &m.UserID, Name: m.Name, Email: m.Email}
}

func newStoredRequest(orgID, requestedBy uuid.UUID, subject Subject) NewStoredRequest {
	return NewStoredRequest{
		ID: uuid.New(), OrgID: orgID, RequestedBy: &requestedBy, Subject: subject,
		Flow: proofingprovider.Flow{
			FlowSpec: proofingprovider.FlowSpec{Name: "Passport + face", RequiredAssuranceLevel: eidasSubstantial},
			ID:       "f1", Version: 3,
		},
		LinkExpiresAt: time.Now().Add(SessionTTL),
	}
}

// attachedSession is the IPS session a recipient's start attaches, pinned to a
// newer flow version than the one the request was sent on.
func attachedSession(id string) proofingprovider.Session {
	return proofingprovider.Session{ID: id, Token: "rp-token", ExpiresAt: time.Now().Add(SessionTTL), FlowVersion: 4}
}

// createStarted stores a request and attaches its session, as sending does.
func createStarted(t *testing.T, store *RequestStore, in NewStoredRequest, sessionID string) Request {
	t.Helper()
	ctx := context.Background()
	req, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := store.AttachSession(ctx, req, attachedSession(sessionID)); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	return onlyRequest(t, store, in.OrgID)
}

// onlyRequest reads the org's one request back.
func onlyRequest(t *testing.T, store *RequestStore, orgID uuid.UUID) Request {
	t.Helper()
	reqs, err := store.List(context.Background(), orgID, RequestFilter{})
	if err != nil || len(reqs) != 1 {
		t.Fatalf("List = %+v, %v; want one request", reqs, err)
	}
	return reqs[0]
}

func TestRequestStoreLifecycle(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	subjectID := makeUser(t, pool, "alex@example.org")
	makeMember(t, pool, orgID, subjectID, "member")
	subject, err := store.Member(context.Background(), orgID, subjectID)
	if err != nil {
		t.Fatalf("Member: %v", err)
	}
	ctx := context.Background()

	req, err := store.Create(ctx, newStoredRequest(orgID, requester, memberSubject(subject)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Status != StatusPending || req.RequestedByName != "Sam de Vries" || req.SubjectUserID == nil ||
		*req.SubjectUserID != subjectID || req.FlowVersion != 3 || req.RequiredAssuranceLevel != eidasSubstantial ||
		req.session != nil {
		t.Errorf("created = %+v; want a pending request with the flow's level and no session yet", req)
	}

	// Sending attaches the session once; a second attach is refused.
	if ok, err := store.AttachSession(ctx, req, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if ok, err := store.AttachSession(ctx, req, attachedSession("s2")); err != nil || ok {
		t.Errorf("second AttachSession = %v, %v; want it refused", ok, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionCreated); n != 1 {
		t.Errorf("session_created audits = %d, want 1", n)
	}
	sent := onlyRequest(t, store, orgID)
	if s := sent.session; s == nil || s.ID != "s1" || s.Token != "rp-token" || s.EndedAt != nil || sent.FlowVersion != 4 {
		t.Errorf("sent = %+v; want session s1 on the pinned version", sent)
	}

	var tokenCT []byte
	if err := pool.QueryRow(ctx, `SELECT ips_session_token_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&tokenCT); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if bytes.Contains(tokenCT, []byte("rp-token")) {
		t.Error("the session token is stored in the clear")
	}

	for range 2 {
		if err := store.MarkStarted(ctx, sent, "s1", proofingprovider.MethodIdem); err != nil {
			t.Fatalf("MarkStarted: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionStarted); n != 1 {
		t.Errorf("session_started audits = %d, want 1", n)
	}
	started := onlyRequest(t, store, orgID)
	if started.Method != proofingprovider.MethodIdem {
		t.Errorf("method = %q, want the Idem app", started.Method)
	}

	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, AssuranceLevel: "high", EIDASLevel: "substantial"}
	for range 2 {
		if err := store.RecordOutcome(ctx, started, "s1", StatusApproved, res); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingApproved); n != 1 {
		t.Errorf("approved audits = %d, want 1", n)
	}

	mine, err := store.List(ctx, orgID, RequestFilter{RequestedBy: &requester})
	if err != nil || len(mine) != 1 || mine[0].Status != StatusApproved || mine[0].EIDASLevel != "substantial" {
		t.Fatalf("List own = %+v, %v", mine, err)
	}
	other := uuid.New()
	if theirs, err := store.List(ctx, orgID, RequestFilter{RequestedBy: &other}); err != nil || len(theirs) != 0 {
		t.Errorf("List for another member = %+v, %v; want none", theirs, err)
	}
}

// A session that ends undecided ends the request: the end is recorded once,
// and the request reads as expired.
func TestRequestStoreEndSessionExpiresTheRequest(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	req := createStarted(t, store,
		newStoredRequest(orgID, requester, memberSubject(Member{UserID: requester, Name: "Sam", Email: "sam@example.org"})), "s1")
	if err := store.MarkStarted(ctx, req, "s1", ""); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	for range 2 {
		if err := store.EndSession(ctx, req, "s1", proofingprovider.StatusCancelled, ""); err != nil {
			t.Fatalf("EndSession: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionEnded); n != 1 {
		t.Errorf("session_ended audits = %d, want 1", n)
	}
	ended := onlyRequest(t, store, orgID)
	if ended.needsReconcile() || ended.liveSession(time.Now()) != nil || ended.EffectiveStatus(time.Now()) != StatusExpired {
		t.Errorf("ended = %+v; want the session over and the request expired", ended)
	}
}

func TestRequestStoreEndSessionEndsAReview(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	req := createStarted(t, store,
		newStoredRequest(orgID, requester, memberSubject(Member{UserID: requester, Name: "Sam", Email: "sam@example.org"})), "s1")
	if err := store.RecordOutcome(ctx, req, "s1", StatusNeedsReview, proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if !onlyRequest(t, store, orgID).needsReconcile() {
		t.Fatal("an open review must still be reconciled")
	}
	if err := store.EndSession(ctx, onlyRequest(t, store, orgID), "s1", proofingprovider.StatusExpired, ""); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if onlyRequest(t, store, orgID).needsReconcile() {
		t.Error("an ended review must no longer be reconciled")
	}
	if got := onlyRequest(t, store, orgID).EffectiveStatus(time.Now()); got != StatusExpired {
		t.Errorf("status = %s, want expired", got)
	}
}

func TestRequestStoreSessionDeadlines(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	req := createStarted(t, store,
		newStoredRequest(orgID, requester, memberSubject(Member{UserID: requester, Name: "Sam", Email: "sam@example.org"})), "s1")
	got, err := store.GetBySession(ctx, "s1")
	if err != nil || got.ID != req.ID {
		t.Fatalf("GetBySession = %+v, %v", got, err)
	}
	if _, err := store.GetBySession(ctx, "nope"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("GetBySession(unknown) = %v, want ErrRequestNotFound", err)
	}
	deadline := got.session.ExpiresAt
	if next, err := store.NextDeadline(ctx, deadline.Add(-time.Second)); err != nil || !next.Equal(deadline) {
		t.Errorf("NextDeadline before the cap = %v, %v; want %v", next, err, deadline)
	}
	if due, err := store.ListDue(ctx, deadline.Add(-time.Second), 10); err != nil || len(due) != 0 {
		t.Errorf("ListDue before the cap = %d, %v; want none", len(due), err)
	}
	if due, err := store.ListDue(ctx, deadline.Add(time.Second), 10); err != nil || len(due) != 1 {
		t.Errorf("ListDue past the cap = %d, %v; want the request", len(due), err)
	}
	// Leased: another replica's run does not get it until the lease lapses.
	if due, err := store.ListDue(ctx, deadline.Add(2*time.Second), 10); err != nil || len(due) != 0 {
		t.Errorf("ListDue while leased = %d, %v; want none", len(due), err)
	}
	if due, err := store.ListDue(ctx, deadline.Add(time.Second+deadlineRetry), 10); err != nil || len(due) != 1 {
		t.Errorf("ListDue after the lease = %d, %v; want the request again", len(due), err)
	}
	if err := store.EndSession(ctx, got, "s1", proofingprovider.StatusExpired, ""); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if due, err := store.ListDue(ctx, deadline.Add(time.Second), 10); err != nil || len(due) != 0 {
		t.Errorf("ListDue after the end = %d, %v; want none", len(due), err)
	}
	if next, err := store.NextDeadline(ctx, deadline.Add(-time.Second)); err != nil || !next.IsZero() {
		t.Errorf("NextDeadline after the end = %v, %v; want none", next, err)
	}
}

func TestCustomerStoreLifecycle(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewCustomerStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	admin := makeUser(t, pool, "admin@example.org")
	ctx := context.Background()

	c, err := store.Create(ctx, orgID, admin, "Initech")
	if err != nil || c.Name != "Initech" || len(c.Flows.FlowIDs) != 0 {
		t.Fatalf("Create = %+v, %v", c, err)
	}
	if _, err := store.Create(ctx, orgID, admin, "INITECH"); !errors.Is(err, ErrCustomerExists) {
		t.Errorf("duplicate name err = %v, want ErrCustomerExists", err)
	}
	if _, err := store.Create(ctx, otherOrg, admin, "Initech"); err != nil {
		t.Errorf("another org's customer of the same name: %v", err)
	}
	if _, err := store.Get(ctx, otherOrg, c.ID); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("Get from another org = %v, want ErrCustomerNotFound", err)
	}

	if c, err = store.Rename(ctx, orgID, c.ID, "Initech BV"); err != nil || c.Name != "Initech BV" {
		t.Fatalf("Rename = %+v, %v", c, err)
	}
	sel := FlowSelection{FlowIDs: []string{"f1", "f2"}, DefaultFlowID: "f2"}
	if c, err = store.SaveFlows(ctx, orgID, c.ID, sel); err != nil {
		t.Fatalf("SaveFlows: %v", err)
	}
	if !slices.Equal(c.Flows.FlowIDs, sel.FlowIDs) || c.Flows.DefaultFlowID != "f2" {
		t.Errorf("flows = %+v, want %+v", c.Flows, sel)
	}
	if c, err = store.SaveFlows(ctx, orgID, c.ID, FlowSelection{FlowIDs: []string{}}); err != nil || len(c.Flows.FlowIDs) != 0 {
		t.Errorf("clearing flows = %+v, %v", c.Flows, err)
	}

	list, err := store.List(ctx, orgID)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("List = %+v, %v; want only this org's customer", list, err)
	}
	for action, want := range map[string]int{
		audit.IdentityProofingCustomerCreated:         2,
		audit.IdentityProofingCustomerUpdated:         1,
		audit.IdentityProofingCustomerFlowsConfigured: 2,
	} {
		if n := auditCount(t, pool, action); n != want {
			t.Errorf("%s audits = %d, want %d", action, n, want)
		}
	}
}

// A rejection is audited as its own action, with IPS's error code as the reason
// and the subject it was about.
func TestRequestStoreRejectionAuditsReason(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{Name: "Anna Jansen", Email: "anna@example.org"}), "s1")

	const code = "DOCUMENT_TYPE_NOT_ACCEPTED"
	res := proofingprovider.Result{Status: proofingprovider.StatusRejected, ErrorCode: code}
	if err := store.RecordOutcome(ctx, req, "s1", StatusRejected, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingApproved); n != 0 {
		t.Errorf("approved audits = %d, want 0", n)
	}
	var after struct {
		Status       string `json:"status"`
		ErrorCode    string `json:"errorCode"`
		SubjectName  string `json:"subjectName"`
		SubjectEmail string `json:"subjectEmail"`
	}
	if err := pool.QueryRow(ctx, `SELECT metadata->'after' FROM audit_events WHERE action = $1`,
		audit.IdentityProofingRejected).Scan(&after); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if after.Status != string(StatusRejected) || after.ErrorCode != code ||
		after.SubjectName != "Anna Jansen" || after.SubjectEmail != "anna@example.org" {
		t.Errorf("rejected audit after = %+v", after)
	}
}

// A customer's subject is stored without a member; the proofed name is sealed,
// shown until its retention passes, and then purged with the subject.
func TestRequestStoreCustomerSubjectAndProofedName(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, requester, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{CustomerID: &customer.ID, Email: "anna@example.org"}), "s1")
	if req.SubjectUserID != nil || req.CustomerID == nil || *req.CustomerID != customer.ID || req.CustomerName != "Initech" {
		t.Errorf("created = %+v", req)
	}

	const name = "Anna Jansen"
	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: name}
	if err := store.RecordOutcome(ctx, req, "s1", StatusApproved, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	var nameCT []byte
	if err := pool.QueryRow(ctx, `SELECT proofed_name_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&nameCT); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if len(nameCT) == 0 || bytes.Contains(nameCT, []byte(name)) {
		t.Error("the proofed name is missing or stored in the clear")
	}
	var metadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action = $1`,
		audit.IdentityProofingApproved).Scan(&metadata); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(metadata, name) {
		t.Error("the proofed name was audited")
	}

	theirs, err := store.List(ctx, orgID, RequestFilter{CustomerID: &customer.ID})
	if err != nil || len(theirs) != 1 || theirs[0].ProofedName != name {
		t.Fatalf("List for customer = %+v, %v", theirs, err)
	}
	other := uuid.New()
	if none, err := store.List(ctx, orgID, RequestFilter{CustomerID: &other}); err != nil || len(none) != 0 {
		t.Errorf("List for another customer = %+v, %v; want none", none, err)
	}

	if due, err := store.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Errorf("purge due within retention = %d, %v; want none", len(due), err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '31 days' WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the request: %v", err)
	}
	due, err := store.ListPurgeDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != req.ID {
		t.Fatalf("purge due past retention = %+v, %v; want the request", due, err)
	}
	if err := store.Purge(ctx, due[0]); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	after, err := store.List(ctx, orgID, RequestFilter{CustomerID: &customer.ID})
	if err != nil || len(after) != 1 || after[0].ProofedName != "" || after[0].SubjectEmail != "" ||
		after[0].Status != StatusApproved || after[0].PurgedAt == nil {
		t.Errorf("after purge = %+v, %v; want the outcome without the subject", after, err)
	}
	var leaks int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%anna@example.org%'`).Scan(&leaks); err != nil || leaks != 0 {
		t.Errorf("audit events naming the subject after purge = %d, %v; want none", leaks, err)
	}
	if due, err := store.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Errorf("purge due after purge = %d, %v; want none", len(due), err)
	}
}

// A request for one known person seals the expected birth date, audits only
// that a person is expected, and drops the date once decided.
func TestRequestStoreExpectedSubject(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, requester, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	const birthDate = "1984-07-21"
	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{CustomerID: &customer.ID, Name: "Dibran Mulder", Email: "dibran@example.org", BirthDate: birthDate}), "s1")
	if !req.ExpectsSubject || req.expectedBirthDate != birthDate {
		t.Fatalf("created expects subject = %v, birth date = %q", req.ExpectsSubject, req.expectedBirthDate)
	}
	var birthDateCT []byte
	if err := pool.QueryRow(ctx, `SELECT expected_birth_date_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&birthDateCT); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if len(birthDateCT) == 0 || bytes.Contains(birthDateCT, []byte(birthDate)) {
		t.Error("the expected birth date is missing or stored in the clear")
	}
	var metadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action = $1`,
		audit.IdentityProofingRequested).Scan(&metadata); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if strings.Contains(metadata, birthDate) || !strings.Contains(metadata, "expectsSubject") {
		t.Errorf("requested audit = %s; want expectsSubject without the birth date", metadata)
	}

	// Under review the match is still to be made.
	if err := store.RecordOutcome(ctx, req, "s1", StatusNeedsReview, proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome review: %v", err)
	}
	if got := onlyRequest(t, store, orgID); got.expectedBirthDate != birthDate {
		t.Errorf("birth date under review = %q, want it kept", got.expectedBirthDate)
	}
	req = onlyRequest(t, store, orgID)
	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, ErrorCode: ErrorIdentityMismatch}
	if err := store.RecordOutcome(ctx, req, "s1", StatusRejected, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	got := onlyRequest(t, store, orgID)
	if !got.ExpectsSubject || got.expectedBirthDate != "" || got.ErrorCode != ErrorIdentityMismatch {
		t.Errorf("decided = expects %v, birth date %q, code %q; want expected, dropped, mismatch",
			got.ExpectsSubject, got.expectedBirthDate, got.ErrorCode)
	}
}

// A hosted request holds its reference photo sealed, read only at start, and
// drops it once the session is attached.
func TestRequestStoreHoldsTheReferencePhotoUntilStart(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, requester, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	photo := &proofingprovider.Image{MimeType: "image/jpeg", Base64: "/9j/4AAQ"}
	in := newStoredRequest(orgID, requester, Subject{CustomerID: &customer.ID, Email: "dibran@example.org"})
	in.LinkTokenHash, in.ReferencePhoto = []byte("hash"), photo
	req, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var photoCT []byte
	if err := pool.QueryRow(ctx, `SELECT reference_photo_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&photoCT); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if len(photoCT) == 0 || bytes.Contains(photoCT, []byte(photo.Base64)) {
		t.Error("the reference photo is missing or stored in the clear")
	}
	if got, err := store.ReferencePhoto(ctx, req); err != nil || got == nil || *got != *photo {
		t.Fatalf("ReferencePhoto = %+v, %v; want the photo", got, err)
	}
	if ok, err := store.AttachSession(ctx, req, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if got, err := store.ReferencePhoto(ctx, req); err != nil || got != nil {
		t.Errorf("ReferencePhoto after start = %+v, %v; want none", got, err)
	}
}

func TestCustomerStorePauseAndResume(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewCustomerStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	admin := makeUser(t, pool, "admin@example.org")
	ctx := context.Background()
	c, err := store.Create(ctx, orgID, admin, "Initech")
	if err != nil || c.Status() != CustomerActive {
		t.Fatalf("Create = %+v, %v; want an active customer", c, err)
	}

	if c, err = store.SetPaused(ctx, orgID, c.ID, true); err != nil || c.Status() != CustomerPaused {
		t.Fatalf("pause = %+v, %v", c, err)
	}
	// Pausing a paused customer changes and audits nothing.
	if c, err = store.SetPaused(ctx, orgID, c.ID, true); err != nil || !c.Paused() {
		t.Fatalf("pause again = %+v, %v", c, err)
	}
	if c, err = store.SetPaused(ctx, orgID, c.ID, false); err != nil || c.Status() != CustomerActive {
		t.Fatalf("resume = %+v, %v", c, err)
	}
	if _, err := store.SetPaused(ctx, orgID, uuid.New(), true); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("pause unknown = %v, want ErrCustomerNotFound", err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingCustomerUpdated); n != 2 {
		t.Errorf("customer_updated audited %d times, want 2", n)
	}
}

func TestRequestStoreStatsCountsCustomerRequestsByOutcome(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	kim := makeUser(t, pool, "kim@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	subject := Subject{CustomerID: &customer.ID, Email: "anna@example.org"}

	// Sam: one approved, one still running. Kim: one never attached (expired).
	approved, err := store.Create(ctx, newStoredRequest(orgID, sam, subject))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := store.AttachSession(ctx, approved, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if err := store.RecordOutcome(ctx, approved, "s1", StatusApproved, proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	running, err := store.Create(ctx, newStoredRequest(orgID, sam, subject))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := store.AttachSession(ctx, running, attachedSession("s2")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if _, err := store.Create(ctx, newStoredRequest(orgID, kim, subject)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Kim: a review still open, and one IPS ended (expired, as EffectiveStatus reads it).
	for i, sid := range []string{"s3", "s4"} {
		review, err := store.Create(ctx, newStoredRequest(orgID, kim, subject))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if ok, err := store.AttachSession(ctx, review, attachedSession(sid)); err != nil || !ok {
			t.Fatalf("AttachSession = %v, %v", ok, err)
		}
		if err := store.RecordOutcome(ctx, review, sid, StatusNeedsReview, proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		if i == 1 {
			if err := store.EndSession(ctx, review, sid, proofingprovider.StatusExpired, ""); err != nil {
				t.Fatalf("EndSession: %v", err)
			}
		}
	}

	since := time.Now().Add(-StatsWindow)
	rows, err := store.Stats(ctx, orgID, nil, since)
	want := StatsRow{CustomerID: customer.ID, FlowID: "f1", Sessions: 5, Approved: 1, NeedsReview: 1, Expired: 2}
	if err != nil || len(rows) != 1 || rows[0] != want {
		t.Errorf("Stats = %+v, %v; want [%+v]", rows, err, want)
	}
	rows, err = store.Stats(ctx, orgID, &sam, since)
	want = StatsRow{CustomerID: customer.ID, FlowID: "f1", Sessions: 2, Approved: 1}
	if err != nil || len(rows) != 1 || rows[0] != want {
		t.Errorf("Stats for sam = %+v, %v; want [%+v]", rows, err, want)
	}
	if rows, err = store.Stats(ctx, orgID, nil, time.Now().Add(time.Minute)); err != nil || len(rows) != 0 {
		t.Errorf("Stats after every request = %+v, %v; want none", rows, err)
	}
}

func TestCustomerStoreRemovePurgesItsRequests(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	customers := NewCustomerStore(pool, audit.NewDBRecorder())
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	gone, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	kept, err := customers.Create(ctx, orgID, sam, "Globex")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, c := range []Customer{gone, kept} {
		if _, err := requests.Create(ctx, newStoredRequest(orgID, sam, Subject{CustomerID: &c.ID, Email: "a@example.org"})); err != nil {
			t.Fatalf("create request: %v", err)
		}
	}

	if err := customers.Remove(ctx, orgID, gone.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := customers.Get(ctx, orgID, gone.ID); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("Get removed = %v, want ErrCustomerNotFound", err)
	}
	left := onlyRequest(t, requests, orgID)
	if left.CustomerID == nil || *left.CustomerID != kept.ID {
		t.Errorf("left request = %+v, want only the kept customer's", left)
	}
	if err := customers.Remove(ctx, orgID, gone.ID); !errors.Is(err, ErrCustomerNotFound) {
		t.Errorf("Remove again = %v, want ErrCustomerNotFound", err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingCustomerRemoved); n != 1 {
		t.Errorf("customer_removed audited %d times, want 1", n)
	}
}

func TestCustomerStoreSavesSettingsAndRetainsNamesForThem(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	customers := NewCustomerStore(pool, audit.NewDBRecorder())
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	c, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil || c.Settings.SessionTTL != SessionTTL || c.Settings.DataRetention() != ProofedNameRetention {
		t.Fatalf("Create = %+v, %v; want the default settings", c, err)
	}
	week := CustomerSettings{SessionTTL: 5 * time.Minute, DataRetentionDays: 7}
	year := CustomerSettings{SessionTTL: SessionTTL, DataRetentionDays: 365}
	if saved, err := customers.SaveSettings(ctx, orgID, c.ID, year); err != nil || saved.Settings != year {
		t.Fatalf("SaveSettings(a year) = %+v, %v; the database must allow up to 365 days", saved.Settings, err)
	}
	if c, err = customers.SaveSettings(ctx, orgID, c.ID, week); err != nil || c.Settings != week {
		t.Fatalf("SaveSettings = %+v, %v", c.Settings, err)
	}

	req := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &c.ID, Email: "a@example.org"}), "s1")
	if req.NameRetention != week.DataRetention() {
		t.Errorf("NameRetention = %v, want %v", req.NameRetention, week.DataRetention())
	}
	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna"}
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	var purgeAfter time.Time
	if err := pool.QueryRow(ctx, `SELECT proofed_name_purge_after FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&purgeAfter); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if want := time.Now().Add(week.DataRetention()); purgeAfter.Sub(want).Abs() > time.Minute {
		t.Errorf("purge after = %v, want about %v", purgeAfter, want)
	}
}

func TestWebhookOutboxDeliversSignedEventsWithBackOff(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NewDBRecorder(), cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}

	var failing atomic.Bool
	type received struct {
		event, signature string
		body             []byte
	}
	got := make(chan received, 10)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Get(EventHeader), r.Header.Get(SignatureHeader), body}
		if failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(receiver.Close)

	// Only session.verified is subscribed to.
	hook, secret, err := webhooks.Save(ctx, orgID, customer.ID, receiver.URL, []string{EventSessionVerified})
	if err != nil || !strings.HasPrefix(secret, webhookSecretPrefix) || hook.SecretLast4 != secretLast4(secret) {
		t.Fatalf("Save = %+v, %q, %v", hook, secret, err)
	}
	if _, again, err := webhooks.Save(ctx, orgID, customer.ID, receiver.URL, WebhookEvents); err != nil || again != "" {
		t.Fatalf("second Save = %q, %v; want the secret kept", again, err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, receiver.URL, []string{EventSessionVerified}); err != nil {
		t.Fatalf("third Save: %v", err)
	}

	subject := Subject{CustomerID: &customer.ID, Email: "a@example.org"}
	approved := createStarted(t, requests, newStoredRequest(orgID, sam, subject), "s1")
	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, EIDASLevel: "substantial", Name: "Anna"}
	if err := requests.RecordOutcome(ctx, approved, "s1", StatusApproved, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	deliverer := NewDeliverer(webhooks, safehttp.Policy{AllowInsecureHTTP: true})
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 1 {
		t.Fatalf("DeliverDue = %d, %v; want the one verified event", n, err)
	}
	first := <-got
	if first.event != EventSessionVerified {
		t.Errorf("event = %q, want %q", first.event, EventSessionVerified)
	}
	ts := strings.TrimPrefix(strings.Split(first.signature, ",")[0], "t=")
	if tsTime, _ := strconv.ParseInt(ts, 10, 64); first.signature != webhookSignature(secret, time.Unix(tsTime, 0), first.body) {
		t.Errorf("signature %q does not verify under the secret", first.signature)
	}
	if bytes.Contains(first.body, []byte("Anna")) || bytes.Contains(first.body, []byte("a@example.org")) {
		t.Errorf("the event body carries personal data: %s", first.body)
	}
	if !bytes.Contains(first.body, []byte(PublicSessionID(approved.ID))) || !bytes.Contains(first.body, []byte("substantial")) {
		t.Errorf("the event body misses the session or its assurance: %s", first.body)
	}

	// A failing endpoint is retried later, not now, and shows as failing.
	failing.Store(true)
	if err := webhooks.SendTest(ctx, orgID, customer.ID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 1 {
		t.Fatalf("DeliverDue test = %d, %v", n, err)
	}
	<-got
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 0 {
		t.Errorf("DeliverDue right after a failure = %d, %v; want nothing due", n, err)
	}
	health, err := webhooks.Health(ctx, orgID)
	h := health[customer.ID]
	if err != nil || h.State != WebhookFailing || h.LastStatusCode == nil || *h.LastStatusCode != http.StatusServiceUnavailable ||
		h.PendingRetries != 1 || h.FailingSince == nil {
		t.Errorf("health = %+v, %v; want failing on 503 with one retry pending", h, err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 2 || deliveries[0].Event != EventTest || deliveries[0].Status != DeliveryPending ||
		deliveries[0].Attempts != 1 || deliveries[1].Status != DeliveryDelivered {
		t.Errorf("deliveries = %+v, %v", deliveries, err)
	}

	// Removing the endpoint drops what waits for it.
	if err := webhooks.Remove(ctx, orgID, customer.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if deliveries, _ := webhooks.Deliveries(ctx, orgID, customer.ID); len(deliveries) != 1 {
		t.Errorf("deliveries after remove = %+v, want only the delivered one", deliveries)
	}
	for action, want := range map[string]int{
		audit.IdentityProofingWebhookConfigured: 3, audit.IdentityProofingWebhookRemoved: 1,
	} {
		if n := auditCount(t, pool, action); n != want {
			t.Errorf("%s audited %d times, want %d", action, n, want)
		}
	}
}

func TestPurgeSendsPurgedEvent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", []string{EventSessionPurged}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "a@example.org"}), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := requests.Purge(ctx, req); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionPurged ||
		deliveries[0].RequestID == nil || *deliveries[0].RequestID != req.ID {
		t.Errorf("deliveries = %+v, %v; want only session.purged for the request", deliveries, err)
	}
}

// A session going to manual review sends session.review_opened, and its
// decision then sends the outcome.
func TestNeedsReviewSendsReviewOpenedEvent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "a@example.org"}), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	// Newest first: the session's creation, then its review.
	if err != nil || len(deliveries) != 2 || deliveries[0].Event != EventSessionReviewOpened ||
		deliveries[1].Event != EventSessionCreated {
		t.Errorf("deliveries = %+v, %v; want session.created then session.review_opened", deliveries, err)
	}
}

// A session's states before its outcome reach the customer too: created, the
// app joining, a new code handed over, and a cancel.
func TestSessionStatesSendWebhooks(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "a@example.org"}), "s1")
	if err := requests.MarkStarted(ctx, req, "s1", proofingprovider.MethodIdem); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	req = onlyRequest(t, requests, orgID)
	if err := requests.RecordHandover(ctx, req, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("RecordHandover: %v", err)
	}
	if ok, err := requests.Cancel(ctx, req); err != nil || !ok {
		t.Fatalf("Cancel = %v, %v", ok, err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil {
		t.Fatalf("Deliveries: %v", err)
	}
	got := make([]string, 0, len(deliveries))
	for _, d := range deliveries {
		got = append(got, d.Event)
	}
	// Newest first.
	want := []string{EventSessionCancelled, EventSessionHandover, EventSessionStarted, EventSessionCreated}
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

// A customer without its own endpoint is sent every event at the wallet's
// default endpoint: signed with the default secret, retried like any other.
func TestWebhookWithoutEndpointSendsToDefault(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	secret, err := webhooks.DefaultSecret()
	if err != nil {
		t.Fatalf("DefaultSecret: %v", err)
	}
	type received struct {
		event string
		err   error
	}
	got := make(chan received, 10)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		err := verifyWebhookSignature(secret, r.Header.Get(SignatureHeader), body, time.Now())
		got <- received{r.Header.Get(EventHeader), err}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	webhooks.SetDefaultEndpoint(receiver.URL)

	req := createStarted(t, requests, newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID, Email: "a@example.org"}), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, ErrorCode: "ASSURANCE_NOT_MET"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	// Strict policy: the default endpoint is the deployment's, reachable on loopback anyway.
	deliverer := NewDeliverer(webhooks, safehttp.Policy{})
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 2 {
		t.Fatalf("DeliverDue = %d, %v; want the created and failed events", n, err)
	}
	events := map[string]bool{}
	for range 2 {
		r := <-got
		if r.err != nil {
			t.Errorf("received %q, signature %v", r.event, r.err)
		}
		events[r.event] = true
	}
	if !events[EventSessionCreated] || !events[EventSessionFailed] {
		t.Errorf("received %v, want session.created and session.failed", events)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 2 || deliveries[0].Event != EventSessionFailed || deliveries[0].Status != DeliveryDelivered ||
		deliveries[0].EndpointURL != "" || deliveries[0].Attempts != 1 || deliveries[0].LastStatusCode == nil ||
		*deliveries[0].LastStatusCode != http.StatusNoContent {
		t.Fatalf("deliveries = %+v, %v; want session.failed delivered to the default endpoint", deliveries, err)
	}
	if err := webhooks.SendTest(ctx, orgID, customer.ID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("SendTest = %v, want ErrWebhookNotFound", err)
	}

	// Its own endpoint takes over from then on; the default's deliveries leave its health alone.
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := requests.Purge(ctx, req); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	deliveries, err = webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 3 || deliveries[0].Event != EventSessionPurged ||
		deliveries[0].Status != DeliveryPending || deliveries[0].EndpointURL != "https://hooks.example.org/x" {
		t.Errorf("deliveries = %+v, %v; want session.purged queued for the endpoint", deliveries, err)
	}
	health, err := webhooks.Health(ctx, orgID)
	if h := health[customer.ID]; err != nil || h.State != WebhookDelivering || h.LastStatusCode != nil || h.PendingRetries != 0 {
		t.Errorf("health = %+v, %v", h, err)
	}
}

// A hosted request is found by its link token's hash and, until its link
// lapses, counts as pending rather than expired, though it has no session.
func TestRequestStoreHostedLink(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	_, hash, err := newLinkToken()
	if err != nil {
		t.Fatal(err)
	}
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID})
	in.LinkTokenHash, in.LinkExpiresAt = hash, time.Now().Add(HostedLinkTTL)
	if _, err := store.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	req, err := store.GetByLinkToken(ctx, hash)
	if err != nil || req.ID != in.ID || !req.Hosted || req.EffectiveStatus(time.Now()) != StatusPending {
		t.Fatalf("GetByLinkToken = %+v, %v; want the hosted, pending request", req, err)
	}
	if _, err := store.GetByLinkToken(ctx, []byte("other")); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("unknown link = %v, want ErrRequestNotFound", err)
	}
	rows, err := store.Stats(ctx, orgID, nil, time.Now().Add(-StatsWindow))
	if err != nil || len(rows) != 1 || rows[0].Expired != 0 {
		t.Errorf("Stats = %+v, %v; want the unstarted link not counted as expired", rows, err)
	}
	req.Method = proofingprovider.MethodYivi
	if ok, err := store.AttachSession(ctx, req, attachedSession("s1")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if started, _ := store.GetByLinkToken(ctx, hash); started.Method != proofingprovider.MethodYivi {
		t.Errorf("method after start = %q, want the app the subject picked", started.Method)
	}
}

// A hosted link that lapses unstarted is ended once by the deadline job, with
// session_ended and a session.expired webhook, and wakes the job at its lapse.
func TestRequestStoreLapsesUnstartedLinks(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	customers := NewCustomerStore(pool, audit.NopRecorder{})
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	customer, err := customers.Create(ctx, orgID, sam, "Initech")
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, customer.ID, "https://hooks.example.org/proofing", []string{EventSessionExpired}); err != nil {
		t.Fatalf("save webhook: %v", err)
	}
	_, hash, err := newLinkToken()
	if err != nil {
		t.Fatal(err)
	}
	lapse := time.Now().Add(time.Hour)
	in := newStoredRequest(orgID, sam, Subject{CustomerID: &customer.ID})
	in.LinkTokenHash, in.LinkExpiresAt = hash, lapse
	if _, err := store.Create(ctx, in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if next, err := store.NextDeadline(ctx, time.Now()); err != nil || !next.Equal(lapse.Truncate(time.Microsecond)) {
		t.Errorf("NextDeadline = %v, %v; want the link's lapse %v", next, err, lapse)
	}
	if n, err := store.LapseLinks(ctx, time.Now(), 10); err != nil || n != 0 {
		t.Errorf("LapseLinks before the lapse = %d, %v; want none", n, err)
	}
	for want := range []int{1, 0} {
		n, err := store.LapseLinks(ctx, lapse.Add(time.Second), 10)
		if err != nil || n != 1-want {
			t.Errorf("LapseLinks run %d = %d, %v; want %d", want+1, n, err, 1-want)
		}
	}
	req, err := store.GetByLinkToken(ctx, hash)
	if err != nil || req.EffectiveStatus(lapse.Add(time.Second)) != StatusExpired {
		t.Errorf("lapsed link = %+v, %v; want expired", req, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionEnded); n != 1 {
		t.Errorf("session_ended audits = %d, want 1", n)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID, customer.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionExpired {
		t.Errorf("deliveries = %+v, %v; want one session.expired", deliveries, err)
	}
}
