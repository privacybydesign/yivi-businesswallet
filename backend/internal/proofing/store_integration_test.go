//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
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

func TestSettingsStoreSealsKeyAndSavesOnce(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewSettingsStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	saved, err := store.Save(ctx, orgID, "t1", "sk_live_secret", "whsec_secret")
	if err != nil || !saved {
		t.Fatalf("Save = %v, %v; want saved", saved, err)
	}
	again, err := store.Save(ctx, orgID, "t2", "sk_live_other", "")
	if err != nil || again {
		t.Fatalf("second Save = %v, %v; want not saved", again, err)
	}

	var stored []byte
	if err := pool.QueryRow(ctx, `SELECT api_key_ciphertext FROM org_identity_proofing_settings WHERE organization_id = $1`,
		orgID).Scan(&stored); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if bytes.Contains(stored, []byte("sk_live_secret")) {
		t.Error("the API key is stored in the clear")
	}
	key, err := store.APIKey(ctx, orgID)
	if err != nil || key != "sk_live_secret" {
		t.Errorf("APIKey = %q, %v; want the first saved key", key, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingProvisioned); n != 1 {
		t.Errorf("provisioned audits = %d, want 1", n)
	}
}

func TestSettingsStoreWithoutKeyRefuses(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewSettingsStore(pool, audit.NopRecorder{}, nil)
	orgID := makeOrg(t, pool, "acme")

	if _, err := store.Save(context.Background(), orgID, "t1", "sk", ""); !errors.Is(err, ErrNoEncryptionKey) {
		t.Errorf("Save = %v, want ErrNoEncryptionKey", err)
	}
	if _, err := store.APIKey(context.Background(), orgID); !errors.Is(err, ErrNotProvisioned) {
		t.Errorf("APIKey = %v, want ErrNotProvisioned", err)
	}
}

func TestSettingsStoreReplacesFlowSelection(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewSettingsStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	if _, err := store.Save(ctx, orgID, "t1", "sk", ""); err != nil {
		t.Fatalf("Save: %v", err)
	}

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
		ID: uuid.New(), OrgID: orgID, RequestedBy: requestedBy, Subject: subject,
		Flow:          proofingprovider.Flow{FlowSpec: proofingprovider.FlowSpec{Name: "Passport + face"}, ID: "f1", Version: 3},
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
		*req.SubjectUserID != subjectID || req.FlowVersion != 3 || req.session != nil {
		t.Errorf("created = %+v; want a pending request with no session yet", req)
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
		if err := store.MarkStarted(ctx, sent, "s1"); err != nil {
			t.Fatalf("MarkStarted: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionStarted); n != 1 {
		t.Errorf("session_started audits = %d, want 1", n)
	}
	started := onlyRequest(t, store, orgID)

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
	if err := store.MarkStarted(ctx, req, "s1"); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	for range 2 {
		if err := store.EndSession(ctx, req, "s1", proofingprovider.StatusCancelled); err != nil {
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
// shown until its retention passes, and then purged.
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

	if n, err := store.PurgeProofedNames(ctx); err != nil || n != 0 {
		t.Errorf("purge within retention = %d, %v; want nothing purged", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET proofed_name_purge_after = now() WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the name: %v", err)
	}
	if n, err := store.PurgeProofedNames(ctx); err != nil || n != 1 {
		t.Errorf("purge past retention = %d, %v; want one", n, err)
	}
	after, err := store.List(ctx, orgID, RequestFilter{CustomerID: &customer.ID})
	if err != nil || len(after) != 1 || after[0].ProofedName != "" || after[0].Status != StatusApproved {
		t.Errorf("after purge = %+v, %v; want the outcome without the name", after, err)
	}
}
