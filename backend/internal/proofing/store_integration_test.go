//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// assertAuditCounts checks how often each action was audited.
func assertAuditCounts(t *testing.T, pool *pgxpool.Pool, want map[string]int) {
	t.Helper()
	for action, n := range want {
		if got := auditCount(t, pool, action); got != n {
			t.Errorf("%s audits = %d, want %d", action, got, n)
		}
	}
}

func TestSettingsReplacesFlows(t *testing.T) {
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

// The org's default flow is independent of the flows members may use: it can be
// one they may not send, it moves without touching them, and the org holds one.
func TestDefaultNeedNotBeAllowed(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	defaults := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_flow_settings
			WHERE organization_id = $1 AND is_default`, orgID).Scan(&n); err != nil {
			t.Fatalf("count defaults: %v", err)
		}
		return n
	}

	for _, sel := range []FlowSelection{
		{FlowIDs: []string{"f1"}, DefaultFlowID: "f2"},
		{FlowIDs: []string{}, DefaultFlowID: "f3"},
		{FlowIDs: []string{"f3", "f4"}, DefaultFlowID: "f3"},
		{FlowIDs: []string{"f4"}},
	} {
		if err := store.SaveFlowSelection(ctx, orgID, sel); err != nil {
			t.Fatalf("SaveFlowSelection %+v: %v", sel, err)
		}
		got, err := store.FlowSelection(ctx, orgID)
		if err != nil || !slices.Equal(got.FlowIDs, sel.FlowIDs) || got.DefaultFlowID != sel.DefaultFlowID {
			t.Errorf("FlowSelection = %+v, %v; want %+v", got, err, sel)
		}
		wantDefaults := 0
		if sel.DefaultFlowID != "" {
			wantDefaults = 1
		}
		if n := defaults(); n != wantDefaults {
			t.Errorf("default flows after %+v = %d, want %d", sel, n, wantDefaults)
		}
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
	return Subject{Kind: SubjectMember, UserID: &m.UserID, Name: m.Name, Email: m.Email}
}

// sessionChange sets every one of settings, as a whole-form save does; an unset
// erasure handling is left as it is.
func sessionChange(settings SessionSettings) SessionSettingsChange {
	change := SessionSettingsChange{SessionTTL: &settings.SessionTTL, DataRetentionDays: &settings.DataRetentionDays}
	if settings.ErasureHandling != "" {
		change.ErasureHandling = &settings.ErasureHandling
	}
	return change
}

// externalSubject is an external person known by e-mail address only.
func externalSubject(email string) Subject {
	return Subject{Kind: SubjectExternal, Email: email}
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
func TestStoreEndSessionExpires(t *testing.T) {
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

func TestStoreEndSessionEndsReview(t *testing.T) {
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

func TestStoreSessionDeadlines(t *testing.T) {
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

// settingsAudit is one identity_proofing.settings_updated event: the fields it
// held before and after.
type settingsAudit struct{ Before, After map[string]any }

func settingsAudits(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) []settingsAudit {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT metadata->'before', metadata->'after' FROM audit_events
		WHERE action = $1 AND organization_id = $2 ORDER BY occurred_at, id`, audit.IdentityProofingSettingsUpdated, orgID)
	if err != nil {
		t.Fatalf("read settings audits: %v", err)
	}
	audits, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (settingsAudit, error) {
		var a settingsAudit
		err := row.Scan(&a.Before, &a.After)
		return a, err
	})
	if err != nil {
		t.Fatalf("read settings audits: %v", err)
	}
	return audits
}

// An org that saved nothing reads as the defaults, without a row being made
// for it; one that does not exist is not found.
func TestOrgSettingsDefaults(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	got, err := store.Get(ctx, orgID)
	if err != nil || got.OrganizationID != orgID || got.OrgName != "acme" || got.Session.SessionTTL != SessionTTL ||
		got.Session.DataRetentionDays != defaultDataRetentionDays || got.Branding != (Branding{}) ||
		len(got.RedirectOrigins) != 0 || !got.UpdatedAt.IsZero() || got.SignedAs() != "acme" {
		t.Errorf("Get = %+v, %v; want the defaults, signed with the org's name", got, err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_settings`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("settings rows after a read = %d, %v; want none", rows, err)
	}
	if _, err := store.Logo(ctx, orgID); !errors.Is(err, ErrNoLogo) {
		t.Errorf("Logo of an org without settings = %v, want ErrNoLogo", err)
	}

	unknown := uuid.New()
	if _, err := store.Get(ctx, unknown); !errors.Is(err, ErrOrgNotFound) {
		t.Errorf("Get unknown org = %v, want ErrOrgNotFound", err)
	}
	if _, err := store.SaveSession(ctx, unknown, sessionChange(SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 7})); !errors.Is(err, ErrOrgNotFound) {
		t.Errorf("SaveSession unknown org = %v, want ErrOrgNotFound", err)
	}
	if _, err := store.SaveBranding(ctx, unknown, Branding{DisplayName: "Acme"}, LogoChange{}); !errors.Is(err, ErrOrgNotFound) {
		t.Errorf("SaveBranding unknown org = %v, want ErrOrgNotFound", err)
	}
	if _, err := store.SaveRedirectOrigins(ctx, unknown, []string{"https://portal.example"}); !errors.Is(err, ErrOrgNotFound) {
		t.Errorf("SaveRedirectOrigins unknown org = %v, want ErrOrgNotFound", err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingSettingsUpdated); n != 0 {
		t.Errorf("settings_updated audits after the refused saves = %d, want 0", n)
	}
}

// Session settings round trip per org, are audited with before and after, and
// saving what the org already has changes and audits nothing.
func TestOrgSettingsSession(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	ctx := context.Background()
	week := SessionSettings{SessionTTL: 5 * time.Minute, DataRetentionDays: 7, ErasureHandling: ErasureReview}
	year := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 365, ErasureHandling: ErasureAutomatic}

	if saved, err := store.SaveSession(ctx, orgID, sessionChange(year)); err != nil || saved.Session != year {
		t.Fatalf("SaveSession(a year) = %+v, %v; the database must allow up to 365 days", saved.Session, err)
	}
	saved, err := store.SaveSession(ctx, orgID, sessionChange(week))
	if err != nil || saved.Session != week || saved.UpdatedAt.IsZero() {
		t.Fatalf("SaveSession = %+v, %v", saved, err)
	}
	for range 2 {
		again, err := store.SaveSession(ctx, orgID, sessionChange(week))
		if err != nil || again.Session != week || !again.UpdatedAt.Equal(saved.UpdatedAt) {
			t.Errorf("saving the same settings = %+v, %v; want them and their update time kept", again, err)
		}
	}

	audits := settingsAudits(t, pool, orgID)
	if len(audits) != 2 {
		t.Fatalf("settings_updated audits = %d, want 2 (a repeat changes nothing)", len(audits))
	}
	if last := audits[1]; last.Before["dataRetentionDays"] != float64(365) || last.After["dataRetentionDays"] != float64(7) ||
		last.Before["sessionTtlSeconds"] != float64(600) || last.After["sessionTtlSeconds"] != float64(300) {
		t.Errorf("last audit = %+v; want the year before and the week after", last)
	}
	if first := audits[0]; first.Before["dataRetentionDays"] != float64(defaultDataRetentionDays) {
		t.Errorf("first audit before = %+v; want the defaults", first.Before)
	}

	other, err := store.Get(ctx, otherOrg)
	if err != nil || other.Session.SessionTTL != SessionTTL || other.Session.DataRetentionDays != defaultDataRetentionDays {
		t.Errorf("another org = %+v, %v; want its own defaults", other.Session, err)
	}
	if n := len(settingsAudits(t, pool, otherOrg)); n != 0 {
		t.Errorf("settings_updated audits of another org = %d, want 0", n)
	}
}

// The redirect origins round trip, are audited, and are cleared by an empty
// list.
func TestSettingsRedirectOrigins(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	origins := []string{"https://portal.initech.example", "http://localhost:3000"}

	// Nothing to save for an org that has none: no row, no audit.
	if saved, err := store.SaveRedirectOrigins(ctx, orgID, nil); err != nil || len(saved.RedirectOrigins) != 0 {
		t.Fatalf("SaveRedirectOrigins(none) = %v, %v", saved.RedirectOrigins, err)
	}
	if n := len(settingsAudits(t, pool, orgID)); n != 0 {
		t.Fatalf("settings_updated audits after saving no origins = %d, want 0", n)
	}

	saved, err := store.SaveRedirectOrigins(ctx, orgID, origins)
	if err != nil || !slices.Equal(saved.RedirectOrigins, origins) {
		t.Fatalf("SaveRedirectOrigins = %v, %v; want %v", saved.RedirectOrigins, err, origins)
	}
	// The row the save made holds the defaults for the rest.
	if saved.Session.SessionTTL != SessionTTL || saved.Session.DataRetentionDays != defaultDataRetentionDays || saved.Branding != (Branding{}) {
		t.Errorf("settings after saving origins = %+v; want the defaults for the rest", saved)
	}
	again, err := store.SaveRedirectOrigins(ctx, orgID, origins)
	if err != nil || !slices.Equal(again.RedirectOrigins, origins) || !again.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Errorf("saving the same origins = %+v, %v; want them and their update time kept", again, err)
	}
	if n := len(settingsAudits(t, pool, orgID)); n != 1 {
		t.Errorf("settings_updated audits = %d, want 1 (a repeat changes nothing)", n)
	}

	cleared, err := store.SaveRedirectOrigins(ctx, orgID, []string{})
	if err != nil || len(cleared.RedirectOrigins) != 0 {
		t.Fatalf("clearing the origins = %v, %v", cleared.RedirectOrigins, err)
	}
	audits := settingsAudits(t, pool, orgID)
	if len(audits) != 2 || len(audits[1].Before["allowedRedirectOrigins"].([]any)) != 2 ||
		len(audits[1].After["allowedRedirectOrigins"].([]any)) != 0 {
		t.Errorf("settings_updated audits = %+v; want the clearing audited with both origins before", audits)
	}
}

// Session settings and redirect origins saved together are one transaction
// and one audit event: when the origins cannot be stored, the session settings
// written before them are not kept either.
func TestSettingsSaveIsOneTransaction(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	week := SessionSettings{SessionTTL: 5 * time.Minute, DataRetentionDays: 7, ErasureHandling: ErasureReview}
	origins := []string{"https://portal.example.org"}

	// The settings row takes no origins, so their write fails after the
	// session settings' one made the row.
	for _, sql := range []string{
		`CREATE FUNCTION refuse_origins() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF cardinality(NEW.allowed_redirect_origins) > 0 THEN RAISE EXCEPTION 'origins refused'; END IF;
			RETURN NEW;
		END $$`,
		`CREATE TRIGGER refuse_origins BEFORE INSERT OR UPDATE ON identity_proofing_settings
		FOR EACH ROW EXECUTE FUNCTION refuse_origins()`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("refuse origins: %v", err)
		}
	}
	if _, err := store.Save(ctx, orgID, sessionChange(week), nil, &origins); err == nil {
		t.Fatal("Save with refused origins succeeded")
	}
	got, err := store.Get(ctx, orgID)
	if err != nil || got.Session.DataRetentionDays != defaultDataRetentionDays || len(got.RedirectOrigins) != 0 {
		t.Errorf("settings after the failed save = %+v, %v; want the defaults, nothing half saved", got, err)
	}
	if n := len(settingsAudits(t, pool, orgID)); n != 0 {
		t.Errorf("settings_updated audits after the failed save = %d, want 0", n)
	}

	if _, err := pool.Exec(ctx, `DROP TRIGGER refuse_origins ON identity_proofing_settings`); err != nil {
		t.Fatalf("allow origins: %v", err)
	}
	saved, err := store.Save(ctx, orgID, sessionChange(week), nil, &origins)
	if err != nil || saved.Session != week || !slices.Equal(saved.RedirectOrigins, origins) {
		t.Fatalf("Save = %+v, %v; want the week and the origins", saved, err)
	}
	audits := settingsAudits(t, pool, orgID)
	if len(audits) != 1 || audits[0].After["dataRetentionDays"] != float64(week.DataRetentionDays) ||
		len(audits[0].After["allowedRedirectOrigins"].([]any)) != len(origins) {
		t.Errorf("settings_updated audits = %+v; want one with the session settings and the origins", audits)
	}
}

// expectLogo checks that the org's stored logo is want after step.
func expectLogo(t *testing.T, store *OrgSettingsStore, orgID uuid.UUID, step string, want Logo) {
	t.Helper()
	got, err := store.Logo(context.Background(), orgID)
	if err != nil || !bytes.Equal(got.Bytes, want.Bytes) || got.ContentType != want.ContentType {
		t.Errorf("%s: Logo = %q %q, %v; want %q %q", step, got.Bytes, got.ContentType, err, want.Bytes, want.ContentType)
	}
}

// The branding round trips with its logo, which a save keeps, replaces or
// removes as asked; each change is audited once, without the logo's bytes, and
// the other settings stay as they were.
func TestOrgSettingsBranding(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	logoA := Logo{Bytes: []byte("\x89PNG logo a"), ContentType: "image/png"}
	logoB := Logo{Bytes: []byte("\xff\xd8\xff logo b"), ContentType: "image/jpeg"}
	branding := Branding{
		DisplayName: "Acme Verify", PrimaryColor: "#0a5cff", SupportContact: "help@acme.example",
		PrivacyURL: "https://acme.example/privacy", HidePoweredBy: true,
	}

	week := SessionSettings{SessionTTL: 5 * time.Minute, DataRetentionDays: 7, ErasureHandling: ErasureReview}
	if _, err := store.SaveSession(ctx, orgID, sessionChange(week)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	saved, err := store.SaveBranding(ctx, orgID, branding, LogoChange{Replace: true, Logo: logoA})
	wantBranding := branding
	wantBranding.HasLogo = true
	if err != nil || saved.Branding != wantBranding || saved.SignedAs() != "Acme Verify" || saved.Session != week {
		t.Fatalf("SaveBranding = %+v, %v; want %+v beside the session settings", saved, err, wantBranding)
	}
	expectLogo(t, store, orgID, "added", logoA)

	// The same values, the logo kept: nothing changes.
	again, err := store.SaveBranding(ctx, orgID, branding, LogoChange{})
	if err != nil || again.Branding != wantBranding || !again.UpdatedAt.Equal(saved.UpdatedAt) {
		t.Errorf("saving the same branding = %+v, %v; want it and its update time kept", again.Branding, err)
	}
	if n := len(settingsAudits(t, pool, orgID)); n != 2 {
		t.Errorf("settings_updated audits = %d, want 2 (the session settings, then the branding)", n)
	}

	// A change that keeps the logo leaves it as it is.
	renamed := branding
	renamed.DisplayName = "Acme"
	if got, err := store.SaveBranding(ctx, orgID, renamed, LogoChange{}); err != nil || got.Branding.DisplayName != "Acme" || !got.Branding.HasLogo {
		t.Fatalf("rename with the logo kept = %+v, %v", got.Branding, err)
	}
	expectLogo(t, store, orgID, "kept", logoA)

	// A change that replaces the logo.
	recoloured := renamed
	recoloured.PrimaryColor = "#112233"
	if got, err := store.SaveBranding(ctx, orgID, recoloured, LogoChange{Replace: true, Logo: logoB}); err != nil || got.Branding.PrimaryColor != "#112233" || !got.Branding.HasLogo {
		t.Fatalf("recolour with another logo = %+v, %v", got.Branding, err)
	}
	expectLogo(t, store, orgID, "replaced", logoB)

	assertBrandingLogoRemoved(t, store, orgID, recoloured, logoA)
	assertBrandingAuditsHideLogo(t, pool, orgID)
}

// assertBrandingLogoRemoved checks that saving recoloured with no logo removes
// the logo and nothing else, that session settings saved over the branding,
// with logo added again, leave it alone, and that an emptied branding reads as
// the org's own again.
func assertBrandingLogoRemoved(t *testing.T, store *OrgSettingsStore, orgID uuid.UUID, recoloured Branding, logo Logo) {
	t.Helper()
	ctx := context.Background()
	removed, err := store.SaveBranding(ctx, orgID, recoloured, LogoChange{Replace: true})
	if err != nil || removed.Branding.HasLogo || removed.Branding.PrimaryColor != "#112233" {
		t.Fatalf("remove the logo = %+v, %v", removed.Branding, err)
	}
	if _, err := store.Logo(ctx, orgID); !errors.Is(err, ErrNoLogo) {
		t.Errorf("Logo after removing = %v, want ErrNoLogo", err)
	}

	// Session settings saved on top leave the branding alone.
	if _, err := store.SaveBranding(ctx, orgID, recoloured, LogoChange{Replace: true, Logo: logo}); err != nil {
		t.Fatalf("add a logo again: %v", err)
	}
	year := SessionSettings{SessionTTL: SessionTTL, DataRetentionDays: 365, ErasureHandling: ErasureAutomatic}
	if got, err := store.SaveSession(ctx, orgID, sessionChange(year)); err != nil || got.Session != year ||
		got.Branding.DisplayName != "Acme" || got.Branding.PrimaryColor != "#112233" || !got.Branding.HasLogo {
		t.Errorf("SaveSession over the branding = %+v, %v; want the branding kept", got, err)
	}
	expectLogo(t, store, orgID, "after the session settings", logo)

	// Emptied, the branding reads as the org's own again.
	cleared, err := store.SaveBranding(ctx, orgID, Branding{}, LogoChange{Replace: true})
	if err != nil || cleared.Branding != (Branding{}) || cleared.SignedAs() != "acme" {
		t.Errorf("cleared branding = %+v, %v; want none, signed with the org's name", cleared.Branding, err)
	}
}

// assertBrandingAuditsHideLogo checks the org's eight settings audits: they
// hold whether a logo is set, never its bytes.
func assertBrandingAuditsHideLogo(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) {
	t.Helper()
	audits := settingsAudits(t, pool, orgID)
	if len(audits) != 8 {
		t.Fatalf("settings_updated audits = %d, want 8", len(audits))
	}
	if a := audits[1]; a.Before["displayName"] != "" || a.After["displayName"] != "Acme Verify" ||
		a.Before["hasLogo"] != false || a.After["hasLogo"] != true {
		t.Errorf("branding added audit = %+v", a)
	}
	var leaks int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%logo a%' OR metadata::text LIKE '%logo b%'`).
		Scan(&leaks); err != nil || leaks != 0 {
		t.Errorf("audit events holding a logo's bytes = %d, %v; want none", leaks, err)
	}
}

// A new logo is saved when nothing else in the branding changes: the branding
// only says whether a logo is set, so the bytes have to be compared apart.
func TestBrandingSavesLogoAlone(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewOrgSettingsStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	logoA := Logo{Bytes: []byte("\x89PNG logo a"), ContentType: "image/png"}
	logoB := Logo{Bytes: []byte("\xff\xd8\xff logo b"), ContentType: "image/jpeg"}
	branding := Branding{DisplayName: "Acme Verify"}

	if _, err := store.SaveBranding(ctx, orgID, branding, LogoChange{Replace: true, Logo: logoA}); err != nil {
		t.Fatalf("add a logo: %v", err)
	}
	if _, err := store.SaveBranding(ctx, orgID, branding, LogoChange{Replace: true, Logo: logoB}); err != nil {
		t.Fatalf("replace the logo: %v", err)
	}
	got, err := store.Logo(ctx, orgID)
	if err != nil || !bytes.Equal(got.Bytes, logoB.Bytes) || got.ContentType != logoB.ContentType {
		t.Errorf("Logo = %q %q, %v; want the replacement %q %q", got.Bytes, got.ContentType, err, logoB.Bytes, logoB.ContentType)
	}
	if n := len(settingsAudits(t, pool, orgID)); n != 2 {
		t.Errorf("settings_updated audits = %d, want 2 (the logo added, then replaced)", n)
	}
}

// A rejection is audited as its own action, with IPS's error code as the reason
// and the subject it was about.
func TestStoreRejectionAuditsReason(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{Kind: SubjectExternal, Name: "Anna Jansen", Email: "anna@example.org"}), "s1")

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

// An external person's subject is stored without a member; the proofed name is
// sealed, shown until its retention passes, and then purged with the subject.
func TestStoreExternalProofedName(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	req := createStarted(t, store, newStoredRequest(orgID, requester, externalSubject("anna@example.org")), "s1")
	if req.SubjectUserID != nil || req.SubjectKind != SubjectExternal || !req.External() {
		t.Errorf("created = %+v; want an external request without a member", req)
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

	theirs, err := store.List(ctx, orgID, RequestFilter{})
	if err != nil || len(theirs) != 1 || theirs[0].ProofedName != name {
		t.Fatalf("List = %+v, %v", theirs, err)
	}
	if none, err := store.List(ctx, otherOrg, RequestFilter{}); err != nil || len(none) != 0 {
		t.Errorf("List for another org = %+v, %v; want none", none, err)
	}

	assertProofedNamePurged(t, pool, store, req)
	assertOrphanPurgeDue(t, pool, store, orgID, requester)
}

// assertProofedNamePurged checks that req, an approved external request, is
// due for purge only once its retention has passed, and that the purge takes
// its proofed name and subject out of it and out of the audit log.
func assertProofedNamePurged(t *testing.T, pool *pgxpool.Pool, store *RequestStore, req Request) {
	t.Helper()
	ctx := context.Background()
	if due, err := store.ListPurgeDue(ctx, 10); err != nil || len(due) != 0 {
		t.Errorf("purge due within retention = %d, %v; want none", len(due), err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - interval '31 days' WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the request: %v", err)
	}
	// A raw write: the store would set purge_at in the same transaction.
	if err := refreshPurgeAt(ctx, pool, req.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	due, err := store.ListPurgeDue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != req.ID {
		t.Fatalf("purge due past retention = %+v, %v; want the request", due, err)
	}
	if err := store.Purge(ctx, due[0], nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	after, err := store.List(ctx, req.OrganizationID, RequestFilter{})
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

// assertOrphanPurgeDue checks that a request whose session never got attached
// settles when its link lapses, and is purged its retention after that.
func assertOrphanPurgeDue(t *testing.T, pool *pgxpool.Pool, store *RequestStore, orgID, requester uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	orphan, err := store.Create(ctx, newStoredRequest(orgID, requester, externalSubject("bram@example.org")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET link_expires_at = now() - interval '31 days'
		WHERE id = $1`, orphan.ID); err != nil {
		t.Fatalf("age the orphan: %v", err)
	}
	// A raw write: the store would set purge_at in the same transaction.
	if err := refreshPurgeAt(ctx, pool, orphan.ID); err != nil {
		t.Fatalf("refresh purge time: %v", err)
	}
	if due, err := store.ListPurgeDue(ctx, 10); err != nil || len(due) != 1 || due[0].ID != orphan.ID {
		t.Errorf("purge due for a request without a session = %+v, %v; want it", due, err)
	}
}

// The API reads only the org's external requests: a member's request is neither
// listed nor found, and another org's is not either.
func TestExternalReadsSkipMembers(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	member, err := store.Create(ctx, newStoredRequest(orgID, sam, memberSubject(Member{UserID: sam, Name: "Sam", Email: "sam@example.org"})))
	if err != nil {
		t.Fatalf("Create member request: %v", err)
	}
	if member.External() || member.SubjectKind != SubjectMember {
		t.Errorf("member request = kind %q; want a member's", member.SubjectKind)
	}
	var sent []Request
	for _, email := range []string{"anna@example.org", "bram@example.org", "carla@example.org"} {
		req, err := store.Create(ctx, newStoredRequest(orgID, sam, externalSubject(email)))
		if err != nil {
			t.Fatalf("Create external request: %v", err)
		}
		sent = append(sent, req)
	}
	foreign, err := store.Create(ctx, newStoredRequest(otherOrg, sam, externalSubject("dora@example.org")))
	if err != nil {
		t.Fatalf("Create other org's request: %v", err)
	}

	if got, err := store.GetExternal(ctx, orgID, sent[0].ID); err != nil || got.ID != sent[0].ID {
		t.Errorf("GetExternal = %+v, %v; want the external request", got, err)
	}
	for name, id := range map[string]uuid.UUID{"a member's": member.ID, "another org's": foreign.ID} {
		if _, err := store.GetExternal(ctx, orgID, id); !errors.Is(err, ErrRequestNotFound) {
			t.Errorf("GetExternal of %s request = %v, want ErrRequestNotFound", name, err)
		}
	}
	if got, err := store.Get(ctx, orgID, member.ID); err != nil || got.ID != member.ID {
		t.Errorf("Get of the member's request = %+v, %v; want it, which the wallet's own reads may", got, err)
	}

	// Newest first, in pages: the cursor continues after the last one read.
	page, err := store.ListExternalPage(ctx, orgID, nil, 2)
	if err != nil || len(page) != 2 || page[0].ID != sent[2].ID || page[1].ID != sent[1].ID {
		t.Fatalf("first page = %+v, %v; want the two newest external requests", page, err)
	}
	rest, err := store.ListExternalPage(ctx, orgID, &RequestCursor{CreatedAt: page[1].CreatedAt, ID: page[1].ID}, 2)
	if err != nil || len(rest) != 1 || rest[0].ID != sent[0].ID {
		t.Errorf("second page = %+v, %v; want the oldest external request only", rest, err)
	}
}

// A request for one known person seals the expected birth date, audits only
// that a person is expected, and drops the date once decided.
func TestStoreExpectedSubject(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	const birthDate = "1984-07-21"
	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{Kind: SubjectExternal, Name: "Dibran Mulder", Email: "dibran@example.org", BirthDate: birthDate}), "s1")
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
	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, ErrorCode: errorIdentityMismatch}
	if err := store.RecordOutcome(ctx, req, "s1", StatusRejected, res); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	got := onlyRequest(t, store, orgID)
	if !got.ExpectsSubject || got.expectedBirthDate != "" || got.ErrorCode != errorIdentityMismatch {
		t.Errorf("decided = expects %v, birth date %q, code %q; want expected, dropped, mismatch",
			got.ExpectsSubject, got.expectedBirthDate, got.ErrorCode)
	}
}

// A hosted request holds its reference photo sealed, read only at start, and
// drops it once the session is attached.
func TestStoreHoldsPhotoUntilStart(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	photo := &proofingprovider.Image{MimeType: "image/jpeg", Base64: "/9j/4AAQ"}
	in := newStoredRequest(orgID, requester, externalSubject("dibran@example.org"))
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

func TestStoreStatsByOutcome(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	kim := makeUser(t, pool, "kim@example.org")
	ctx := context.Background()
	subject := externalSubject("anna@example.org")

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

	// A cancelled one counts apart from the expired ones.
	cancelled, err := store.Create(ctx, newStoredRequest(orgID, kim, externalSubject("c@example.org")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ok, err := store.Cancel(ctx, cancelled, nil); err != nil || !ok {
		t.Fatalf("Cancel = %v, %v", ok, err)
	}

	// A request to a member is not counted: the overview is of external people.
	if _, err := store.Create(ctx, newStoredRequest(orgID, sam, memberSubject(Member{UserID: kim, Name: "Kim", Email: "kim@example.org"}))); err != nil {
		t.Fatalf("Create member request: %v", err)
	}

	since := time.Now().Add(-statsWindow)
	rows, err := store.Stats(ctx, orgID, nil, since)
	want := StatsRow{FlowID: "f1", Sessions: 6, Approved: 1, NeedsReview: 1, Expired: 2, Cancelled: 1}
	if err != nil || len(rows) != 1 || rows[0] != want {
		t.Errorf("Stats = %+v, %v; want [%+v]", rows, err, want)
	}
	rows, err = store.Stats(ctx, orgID, &sam, since)
	want = StatsRow{FlowID: "f1", Sessions: 2, Approved: 1}
	if err != nil || len(rows) != 1 || rows[0] != want {
		t.Errorf("Stats for sam = %+v, %v; want [%+v]", rows, err, want)
	}
	if rows, err = store.Stats(ctx, orgID, nil, time.Now().Add(time.Minute)); err != nil || len(rows) != 0 {
		t.Errorf("Stats after every request = %+v, %v; want none", rows, err)
	}
}

// An external request's proofed name is kept as long as the org's retention,
// counted from its outcome; a member's request keeps it for the fixed period,
// whatever the org set.
func TestOrgSettingsNameRetention(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	settings := NewOrgSettingsStore(pool, audit.NopRecorder{})
	requests := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	week := SessionSettings{SessionTTL: 5 * time.Minute, DataRetentionDays: 7, ErasureHandling: ErasureReview}
	if _, err := settings.SaveSession(ctx, orgID, sessionChange(week)); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
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

	member, err := requests.Create(ctx, newStoredRequest(orgID, sam, memberSubject(Member{UserID: sam, Name: "Sam", Email: "sam@example.org"})))
	if err != nil {
		t.Fatalf("Create member request: %v", err)
	}
	if member.NameRetention != proofedNameRetention {
		t.Errorf("a member's NameRetention = %v, want %v", member.NameRetention, proofedNameRetention)
	}
}

func TestWebhookOutboxBackOff(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NewDBRecorder(), cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

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
	hook, secret, err := webhooks.Save(ctx, orgID, receiver.URL, []string{EventSessionVerified})
	if err != nil || !strings.HasPrefix(secret, webhookSecretPrefix) || hook.SecretLast4 != secretLast4(secret) {
		t.Fatalf("Save = %+v, %q, %v", hook, secret, err)
	}
	if _, again, err := webhooks.Save(ctx, orgID, receiver.URL, WebhookEvents); err != nil || again != "" {
		t.Fatalf("second Save = %q, %v; want the secret kept", again, err)
	}
	if _, _, err := webhooks.Save(ctx, orgID, receiver.URL, []string{EventSessionVerified}); err != nil {
		t.Fatalf("third Save: %v", err)
	}

	subject := externalSubject("a@example.org")
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
	if tsTime, _ := strconv.ParseInt(ts, 10, 64); first.signature != webhookSignature([]string{secret}, time.Unix(tsTime, 0), first.body) {
		t.Errorf("signature %q does not verify under the secret", first.signature)
	}
	if bytes.Contains(first.body, []byte("Anna")) || bytes.Contains(first.body, []byte("a@example.org")) {
		t.Errorf("the event body carries personal data: %s", first.body)
	}
	if !bytes.Contains(first.body, []byte(publicSessionID(approved.ID))) || !bytes.Contains(first.body, []byte("substantial")) {
		t.Errorf("the event body misses the session or its assurance: %s", first.body)
	}

	// A failing endpoint is retried later, not now, and shows as failing.
	failing.Store(true)
	if err := webhooks.SendTest(ctx, orgID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 1 {
		t.Fatalf("DeliverDue test = %d, %v", n, err)
	}
	<-got
	if n, err := deliverer.DeliverDue(ctx); err != nil || n != 0 {
		t.Errorf("DeliverDue right after a failure = %d, %v; want nothing due", n, err)
	}
	h, err := webhooks.Health(ctx, orgID)
	if err != nil || h.State != WebhookFailing || h.LastStatusCode == nil || *h.LastStatusCode != http.StatusServiceUnavailable ||
		h.PendingRetries != 1 || h.FailingSince == nil {
		t.Errorf("health = %+v, %v; want failing on 503 with one retry pending", h, err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 2 || deliveries[0].Event != EventTest || deliveries[0].Status != DeliveryPending ||
		deliveries[0].Attempts != 1 || deliveries[1].Status != DeliveryDelivered {
		t.Errorf("deliveries = %+v, %v", deliveries, err)
	}

	// Removing the endpoint drops what waits for it.
	if err := webhooks.Remove(ctx, orgID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if deliveries, _ := webhooks.Deliveries(ctx, orgID); len(deliveries) != 1 {
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
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", []string{EventSessionPurged}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, Name: "Anna"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := requests.Purge(ctx, req, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionPurged ||
		deliveries[0].RequestID == nil || *deliveries[0].RequestID != req.ID {
		t.Errorf("deliveries = %+v, %v; want only session.purged for the request", deliveries, err)
	}
}

// A session going to manual review sends session.review_opened, and its
// decision then sends the outcome.
func TestNeedsReviewSendsEvent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	// Newest first: the session's creation, then its review.
	if err != nil || len(deliveries) != 2 || deliveries[0].Event != EventSessionReviewOpened ||
		deliveries[1].Event != EventSessionCreated {
		t.Errorf("deliveries = %+v, %v; want session.created then session.review_opened", deliveries, err)
	}
}

// A session's states before its outcome reach the org's endpoint too: created,
// the app joining, a new code handed over, and a cancel.
func TestSessionStatesSendWebhooks(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if err := requests.MarkStarted(ctx, req, "s1", proofingprovider.MethodIdem); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	req = onlyRequest(t, requests, orgID)
	if err := requests.RecordHandover(ctx, req, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("RecordHandover: %v", err)
	}
	if ok, err := requests.Cancel(ctx, req, nil); err != nil || !ok {
		t.Fatalf("Cancel = %v, %v", ok, err)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
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

// An org without an endpoint of its own is sent nothing: no delivery is queued
// for it.
func TestWebhookNoEndpointNoDeliveries(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusRejected,
		proofingprovider.Result{Status: proofingprovider.StatusApproved, ErrorCode: "ASSURANCE_NOT_MET"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if deliveries, err := webhooks.Deliveries(ctx, orgID); err != nil || len(deliveries) != 0 {
		t.Errorf("deliveries = %+v, %v; want none without an endpoint", deliveries, err)
	}
	if err := webhooks.SendTest(ctx, orgID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("SendTest = %v, want ErrWebhookNotFound", err)
	}
}

// After a rotation a delivery is signed under the new and the previous
// secret, so a receiver still on the previous one keeps verifying.
func TestWebhookRotationOverlap(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()

	signatures := make(chan struct {
		header string
		body   []byte
	}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		signatures <- struct {
			header string
			body   []byte
		}{r.Header.Get(SignatureHeader), body}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	_, oldSecret, err := webhooks.Save(ctx, orgID, receiver.URL, WebhookEvents)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, newSecret, err := webhooks.RotateSecret(ctx, orgID)
	if err != nil {
		t.Fatalf("RotateSecret: %v", err)
	}
	if err := webhooks.SendTest(ctx, orgID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	if n, err := NewDeliverer(webhooks, safehttp.Policy{AllowInsecureHTTP: true}).DeliverDue(ctx); err != nil || n != 1 {
		t.Fatalf("DeliverDue = %d, %v; want the test event", n, err)
	}

	got := <-signatures
	for name, secret := range map[string]string{"new": newSecret, "previous": oldSecret} {
		if err := verifyWebhookSignature(secret, got.header, got.body, time.Now()); err != nil {
			t.Errorf("the %s secret does not verify %q: %v", name, got.header, err)
		}
	}
}

// A worker whose lease lapsed records nothing: the delivery's newer outcome,
// from the worker that claimed it next, stands.
func TestWebhookLeaseFence(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := webhooks.SendTest(ctx, orgID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	late, err := webhooks.claimDue(ctx, 1, 1)
	if err != nil || len(late) != 1 {
		t.Fatalf("claimDue = %d, %v", len(late), err)
	}
	// The lease lapses and another worker claims and delivers it.
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}
	current, err := webhooks.claimDue(ctx, 1, 1)
	if err != nil || len(current) != 1 {
		t.Fatalf("second claimDue = %d, %v", len(current), err)
	}
	ok := http.StatusOK
	if err := webhooks.recordAttempt(ctx, current[0], &ok, ""); err != nil {
		t.Fatal(err)
	}
	failed := http.StatusBadGateway
	if err := webhooks.recordAttempt(ctx, late[0], &failed, "late"); err != nil {
		t.Fatal(err)
	}

	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != DeliveryDelivered || deliveries[0].Attempts != 1 {
		t.Errorf("deliveries = %+v, %v; want the current worker's delivered outcome", deliveries, err)
	}
}

// A session.diploma_added or session.address_added delivery keeps its diploma
// or address while it may be sent again, and drops it in the write that makes
// it delivered or failed; the rest of the payload stays.
func TestWebhookFinalDropsDiploma(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	diplomas := NewDiplomaStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x",
		[]string{EventSessionDiplomaAdded, EventSessionAddressAdded}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	in := newStoredRequest(orgID, sam, externalSubject("anna@example.org"))
	in.Diplomas = DiplomasRequired
	in.Address = AddressRequired
	req := createStarted(t, requests, in, "s1")
	if err := requests.RecordOutcome(ctx, req, "s1", StatusApproved, proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	for _, number := range []string{"delivered", "failed"} {
		extract := Diploma{
			DocumentType: "Diploma", Qualification: "HBO Bachelor Verpleegkunde", Profiles: []string{},
			Institution: "Hogeschool Utrecht", PlaceOfIssue: "Utrecht", DateAwarded: time.Date(2015, 7, 1, 0, 0, 0, 0, time.UTC),
			DocumentNumber: number,
		}
		if _, added, err := diplomas.Add(ctx, req, extract); err != nil || !added {
			t.Fatalf("Add %s = %v, %v", number, added, err)
		}
	}
	addr := Address{Street: "Kerkstraat", HouseNumber: "1", PostalCode: "1234 AB", City: "Utrecht", Country: "NL"}
	if added, err := requests.AddAddress(ctx, req, addr); err != nil || !added {
		t.Fatalf("AddAddress = %v, %v", added, err)
	}
	// keys returns each delivery's status and payload keys.
	keys := func() [][]string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT status, ARRAY(SELECT jsonb_object_keys(payload) ORDER BY 1)
			FROM identity_proofing_webhook_deliveries ORDER BY created_at, id`)
		if err != nil {
			t.Fatalf("read deliveries: %v", err)
		}
		out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([]string, error) {
			var status string
			var k []string
			err := row.Scan(&status, &k)
			return append([]string{status}, k...), err
		})
		if err != nil {
			t.Fatalf("read deliveries: %v", err)
		}
		return out
	}
	claim := func() dueDelivery {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries SET next_attempt_at = now()
			WHERE status = $1`, DeliveryPending); err != nil {
			t.Fatalf("make due: %v", err)
		}
		due, err := webhooks.claimDue(ctx, 1, 1)
		if err != nil || len(due) != 1 {
			t.Fatalf("claimDue = %d, %v", len(due), err)
		}
		return due[0]
	}
	unavailable, ok := http.StatusServiceUnavailable, http.StatusOK

	// A retryable failure keeps the diploma or address for the next attempt; a
	// success drops it.
	if err := webhooks.recordAttempt(ctx, claim(), &unavailable, "503"); err != nil {
		t.Fatalf("recordAttempt retry: %v", err)
	}
	for _, got := range keys() {
		if got[0] != DeliveryPending || (!slices.Contains(got, webhookDiplomaKey) && !slices.Contains(got, webhookAddressKey)) {
			t.Errorf("after a retryable failure = %v, want pending with its diploma or address", got)
		}
	}
	if err := webhooks.recordAttempt(ctx, claim(), &ok, ""); err != nil {
		t.Fatalf("recordAttempt delivered: %v", err)
	}
	// The last attempt failing fails it for good, and drops its diploma or
	// address too.
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries SET attempts = $1
		WHERE status = $2`, MaxWebhookAttempts-1, DeliveryPending); err != nil {
		t.Fatalf("spend attempts: %v", err)
	}
	for range 2 {
		if err := webhooks.recordAttempt(ctx, claim(), &unavailable, "503"); err != nil {
			t.Fatalf("recordAttempt failed: %v", err)
		}
	}
	got := keys()
	slices.SortFunc(got, func(a, b []string) int { return strings.Compare(a[0], b[0]) })
	want := [][]string{
		{DeliveryDelivered, "flowId", "sessionId", "status"},
		{DeliveryFailed, "flowId", "sessionId", "status"},
		{DeliveryFailed, "flowId", "sessionId", "status"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deliveries = %v, want %v", got, want)
	}
}

// An org has one endpoint: saving again changes it and keeps its secret, and an
// org without one reads as not configured, whatever another org's endpoint
// receives.
func TestWebhookIsOnePerOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NewDBRecorder(), cipher)
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	const firstURL, secondURL = "https://hooks.example.org/one", "https://hooks.example.org/two"

	if h, err := webhooks.Health(ctx, orgID); err != nil || h.State != WebhookNotConfigured {
		t.Errorf("health without an endpoint = %+v, %v; want %s", h, err, WebhookNotConfigured)
	}
	if _, err := webhooks.Get(ctx, orgID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("Get without an endpoint = %v, want ErrWebhookNotFound", err)
	}
	if _, _, err := webhooks.RotateSecret(ctx, orgID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("RotateSecret without an endpoint = %v, want ErrWebhookNotFound", err)
	}
	if err := webhooks.Remove(ctx, orgID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("Remove without an endpoint = %v, want ErrWebhookNotFound", err)
	}

	first, secret, err := webhooks.Save(ctx, orgID, firstURL, []string{EventSessionVerified})
	if err != nil || secret == "" || first.URL != firstURL {
		t.Fatalf("first Save = %+v, %q, %v", first, secret, err)
	}
	second, again, err := webhooks.Save(ctx, orgID, secondURL, WebhookEvents)
	if err != nil || again != "" || second.URL != secondURL || second.SecretLast4 != first.SecretLast4 ||
		!slices.Equal(second.Events, WebhookEvents) {
		t.Fatalf("second Save = %+v, %q, %v; want the endpoint changed and its secret kept", second, again, err)
	}
	var endpoints int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity_proofing_webhooks WHERE organization_id = $1`, orgID).
		Scan(&endpoints); err != nil || endpoints != 1 {
		t.Errorf("endpoints of the org = %d, %v; want one", endpoints, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingWebhookConfigured); n != 2 {
		t.Errorf("webhook_configured audits = %d, want 2 (created, then updated)", n)
	}
	if rotated, newSecret, err := webhooks.RotateSecret(ctx, orgID); err != nil || newSecret == "" || newSecret == secret || rotated.URL != secondURL {
		t.Errorf("RotateSecret = %+v, %q, %v; want a new secret on the same endpoint", rotated, newSecret, err)
	}

	assertWebhookDeliveriesPerOrg(t, requests, webhooks, orgID, otherOrg, sam, secondURL)
	assertWebhookRemovedPerOrg(t, webhooks, orgID, otherOrg, firstURL)
}

// assertWebhookDeliveriesPerOrg checks that each org's events reach its own
// endpoint, secondURL for orgID, and that otherOrg, without one, is sent none.
func assertWebhookDeliveriesPerOrg(t *testing.T, requests *RequestStore, webhooks *WebhookStore, orgID, otherOrg, sam uuid.UUID, secondURL string) {
	t.Helper()
	ctx := context.Background()
	createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	createStarted(t, requests, newStoredRequest(otherOrg, sam, externalSubject("b@example.org")), "s2")
	ours, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(ours) != 1 || ours[0].EndpointURL != secondURL {
		t.Errorf("deliveries of the org = %+v, %v; want one, for its endpoint", ours, err)
	}
	theirs, err := webhooks.Deliveries(ctx, otherOrg)
	if err != nil || len(theirs) != 0 {
		t.Errorf("deliveries of the other org = %+v, %v; want none without an endpoint", theirs, err)
	}
	if h, err := webhooks.Health(ctx, otherOrg); err != nil || h.State != WebhookNotConfigured {
		t.Errorf("health of the org without an endpoint = %+v, %v; want %s", h, err, WebhookNotConfigured)
	}
	if _, err := webhooks.Get(ctx, otherOrg); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("Get of the org without an endpoint = %v, want ErrWebhookNotFound", err)
	}
}

// assertWebhookRemovedPerOrg checks that removing orgID's endpoint leaves
// otherOrg's, saved at firstURL.
func assertWebhookRemovedPerOrg(t *testing.T, webhooks *WebhookStore, orgID, otherOrg uuid.UUID, firstURL string) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, otherOrg, firstURL, WebhookEvents); err != nil {
		t.Fatalf("Save other org: %v", err)
	}
	if err := webhooks.Remove(ctx, orgID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got, err := webhooks.Get(ctx, otherOrg); err != nil || got.URL != firstURL {
		t.Errorf("Get of the other org after the removal = %+v, %v; want its endpoint kept", got, err)
	}
	if _, err := webhooks.Get(ctx, orgID); !errors.Is(err, ErrWebhookNotFound) {
		t.Errorf("Get after the removal = %v, want ErrWebhookNotFound", err)
	}
}

// Only external requests are reported: one to a member sends no webhook, though
// the endpoint subscribes to every event.
func TestMemberRequestNoWebhook(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}

	member := createStarted(t, requests, newStoredRequest(orgID, sam,
		memberSubject(Member{UserID: sam, Name: "Sam", Email: "sam@example.org"})), "s1")
	if err := requests.MarkStarted(ctx, member, "s1", proofingprovider.MethodIdem); err != nil {
		t.Fatalf("MarkStarted: %v", err)
	}
	if err := requests.RecordOutcome(ctx, member, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := requests.RecordOutcome(ctx, member, "s1", StatusApproved,
		proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if err := requests.Purge(ctx, onlyRequest(t, requests, orgID), nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deliveries, err := webhooks.Deliveries(ctx, orgID); err != nil || len(deliveries) != 0 {
		t.Fatalf("deliveries for a member's request = %+v, %v; want none", deliveries, err)
	}

	// The same events of an external request are sent.
	external, err := requests.Create(ctx, newStoredRequest(orgID, sam, externalSubject("a@example.org")))
	if err != nil {
		t.Fatalf("Create external request: %v", err)
	}
	if ok, err := requests.AttachSession(ctx, external, attachedSession("s2")); err != nil || !ok {
		t.Fatalf("AttachSession = %v, %v", ok, err)
	}
	if deliveries, err := webhooks.Deliveries(ctx, orgID); err != nil || len(deliveries) != 1 ||
		deliveries[0].RequestID == nil || *deliveries[0].RequestID != external.ID {
		t.Errorf("deliveries for an external request = %+v, %v; want its session.created", deliveries, err)
	}
}

// An API key belongs to the org: it is listed there only, authenticates to it,
// and only the org can revoke it.
func TestAPIKeysBelongToOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewAPIKeyStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "acme")
	otherOrg := makeOrg(t, pool, "globex")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	ci, ciSecret, err := store.Create(ctx, orgID, sam, "CI", APIKeyScopes)
	if err != nil || ci.OrganizationID != orgID || !strings.HasPrefix(ciSecret, apiKeyPrefix) {
		t.Fatalf("Create = %+v, %v", ci, err)
	}
	_, backofficeSecret, err := store.Create(ctx, orgID, sam, "Backoffice", APIKeyScopes)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	other, otherSecret, err := store.Create(ctx, otherOrg, sam, "Other", APIKeyScopes)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	keys, err := store.List(ctx, orgID)
	if err != nil || len(keys) != 2 || keys[0].Name != "CI" || keys[1].Name != "Backoffice" {
		t.Fatalf("List = %+v, %v; want the org's two keys, oldest first", keys, err)
	}
	if keys, err := store.List(ctx, otherOrg); err != nil || len(keys) != 1 || keys[0].ID != other.ID {
		t.Errorf("List of the other org = %+v, %v; want its own key only", keys, err)
	}

	assertAPIKeyAuthenticates(t, store, orgID, otherOrg, ci, ciSecret, otherSecret)
	assertAPIKeyRevokedByOrgOnly(t, store, orgID, otherOrg, ci.ID, ciSecret, backofficeSecret)

	if keys, err := store.List(ctx, orgID); err != nil || len(keys) != 2 || keys[0].RevokedAt == nil {
		t.Errorf("List after the revoke = %+v, %v; want the revoked key listed", keys, err)
	}
	for action, want := range map[string]int{audit.IdentityProofingAPIKeyCreated: 3, audit.IdentityProofingAPIKeyRevoked: 1} {
		if n := auditCount(t, pool, action); n != want {
			t.Errorf("%s audited %d times, want %d (a repeat changes nothing)", action, n, want)
		}
	}
}

// assertAPIKeyAuthenticates checks that each org's key authenticates to that
// org, ci with ciSecret acting for orgID and stamped as used, and that an
// unknown key does not.
func assertAPIKeyAuthenticates(t *testing.T, store *APIKeyStore, orgID, otherOrg uuid.UUID, ci APIKey, ciSecret, otherSecret string) {
	t.Helper()
	ctx := context.Background()
	caller, err := store.Authenticate(ctx, ciSecret)
	if err == nil {
		err = store.Touch(ctx, caller)
	}
	if err != nil || caller.Org.ID != orgID || caller.Org.Name != "acme" || caller.KeyID != ci.ID || caller.KeyName != "CI" ||
		!slices.Equal(caller.Scopes, APIKeyScopes) {
		t.Errorf("Authenticate = %+v, %v; want the key acting for its org", caller, err)
	}
	if caller, err := store.Authenticate(ctx, otherSecret); err != nil || caller.Org.ID != otherOrg {
		t.Errorf("Authenticate the other org's key = %+v, %v; want that org", caller, err)
	}
	if _, err := store.Authenticate(ctx, apiKeyPrefix+"unknown"); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("Authenticate unknown key = %v, want ErrAPIKeyInvalid", err)
	}
	if keys, _ := store.List(ctx, orgID); keys[0].LastUsedAt == nil || keys[1].LastUsedAt != nil {
		t.Errorf("last used = %v and %v; want only the key that authenticated stamped", keys[0].LastUsedAt, keys[1].LastUsedAt)
	}
}

// assertAPIKeyRevokedByOrgOnly checks that another org cannot revoke ciID,
// that orgID revokes it, twice to no further effect, and that its other key,
// backofficeSecret, keeps working.
func assertAPIKeyRevokedByOrgOnly(t *testing.T, store *APIKeyStore, orgID, otherOrg, ciID uuid.UUID, ciSecret, backofficeSecret string) {
	t.Helper()
	ctx := context.Background()
	// Another org cannot revoke it.
	if _, err := store.Revoke(ctx, otherOrg, ciID); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Errorf("Revoke from another org = %v, want ErrAPIKeyNotFound", err)
	}
	if _, err := store.Authenticate(ctx, ciSecret); err != nil {
		t.Errorf("Authenticate after the refused revoke = %v, want the key kept", err)
	}
	for range 2 {
		if revoked, err := store.Revoke(ctx, orgID, ciID); err != nil || revoked.RevokedAt == nil {
			t.Fatalf("Revoke = %+v, %v", revoked, err)
		}
	}
	if _, err := store.Authenticate(ctx, ciSecret); !errors.Is(err, ErrAPIKeyInvalid) {
		t.Errorf("Authenticate a revoked key = %v, want ErrAPIKeyInvalid", err)
	}
	if _, err := store.Authenticate(ctx, backofficeSecret); err != nil {
		t.Errorf("Authenticate the org's other key = %v, want it kept", err)
	}
}

// A key shows who created it, and nothing once that user is gone.
func TestAPIKeyShowsCreator(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewAPIKeyStore(pool, audit.NopRecorder{})
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	// The display name makeUser gives its user.
	const samName = "Sam de Vries"

	created, _, err := store.Create(ctx, orgID, sam, "CI", APIKeyScopes)
	if err != nil || created.CreatedByName != samName {
		t.Fatalf("Create = %+v, %v; want it created by %s", created, err, samName)
	}
	if keys, err := store.List(ctx, orgID); err != nil || len(keys) != 1 || keys[0].CreatedByName != samName {
		t.Errorf("List = %+v, %v; want the creator named", keys, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, sam); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if keys, err := store.List(ctx, orgID); err != nil || len(keys) != 1 || keys[0].CreatedByName != "" {
		t.Errorf("List after the creator is gone = %+v, %v; want no name", keys, err)
	}
}

// A hosted request is found by its link token's hash and, until its link
// lapses, counts as pending rather than expired, though it has no session.
func TestRequestStoreHostedLink(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	_, hash, err := newLinkToken()
	if err != nil {
		t.Fatal(err)
	}
	in := newStoredRequest(orgID, sam, Subject{Kind: SubjectExternal})
	in.LinkTokenHash, in.LinkExpiresAt = hash, time.Now().Add(hostedLinkTTL)
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
	rows, err := store.Stats(ctx, orgID, nil, time.Now().Add(-statsWindow))
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
func TestStoreLapsesUnstartedLinks(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/proofing", []string{EventSessionExpired}); err != nil {
		t.Fatalf("save webhook: %v", err)
	}
	_, hash, err := newLinkToken()
	if err != nil {
		t.Fatal(err)
	}
	lapse := time.Now().Add(time.Hour)
	in := newStoredRequest(orgID, sam, Subject{Kind: SubjectExternal})
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
	for run, want := range []int{1, 0} {
		n, err := store.LapseLinks(ctx, lapse.Add(time.Second), 10)
		if err != nil || n != want {
			t.Errorf("LapseLinks run %d = %d, %v; want %d", run+1, n, err, want)
		}
	}
	req, err := store.GetByLinkToken(ctx, hash)
	if err != nil || req.EffectiveStatus(lapse.Add(time.Second)) != StatusExpired {
		t.Errorf("lapsed link = %+v, %v; want expired", req, err)
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionEnded); n != 1 {
		t.Errorf("session_ended audits = %d, want 1", n)
	}
	deliveries, err := webhooks.Deliveries(ctx, orgID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Event != EventSessionExpired {
		t.Errorf("deliveries = %+v, %v; want one session.expired", deliveries, err)
	}
}

// Delivered and failed deliveries are pruned past DeliveryRetention; a
// pending one stays whatever its age.
func TestPruneDeliveries(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	webhooks := NewWebhookStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	if _, _, err := webhooks.Save(ctx, orgID, "https://hooks.example.org/x", WebhookEvents); err != nil {
		t.Fatalf("Save: %v", err)
	}
	createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries
		SET created_at = now() - $1::interval - interval '1 day'`, DeliveryRetention.String()); err != nil {
		t.Fatalf("age deliveries: %v", err)
	}
	if n, err := webhooks.PruneDeliveries(ctx); err != nil || n != 0 {
		t.Fatalf("pruned %d pending, %v; want none", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_webhook_deliveries SET status = $1`, DeliveryDelivered); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n, err := webhooks.PruneDeliveries(ctx); err != nil || n == 0 {
		t.Errorf("pruned %d delivered past retention, %v; want them gone", n, err)
	}
}

// LockReview holds one decision at a time on a request: a second waits until
// the first releases.
func TestLockReviewSerialises(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	ctx := context.Background()
	id := uuid.New()
	release, err := store.LockReview(ctx, id)
	if err != nil {
		t.Fatalf("LockReview: %v", err)
	}
	second := make(chan struct{})
	go func() {
		releaseSecond, err := store.LockReview(ctx, id)
		if err != nil {
			t.Errorf("second LockReview: %v", err)
			close(second)
			return
		}
		releaseSecond()
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("a second decision took the lock while the first held it")
	case <-time.After(200 * time.Millisecond):
	}
	release()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second decision never got the lock after the first released it")
	}
	if other, err := store.LockReview(ctx, uuid.New()); err != nil {
		t.Fatalf("another request's lock: %v", err)
	} else {
		other()
	}
}

// A proofed name is dropped once its own retention passed, even while the
// request is not due for a purge.
func TestClearExpiredProofedNames(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if err := store.RecordOutcome(ctx, req, "s1", StatusNeedsReview,
		proofingprovider.Result{Status: proofingprovider.StatusNeedsReview, Name: "Anna Jansen"}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}
	if n, err := store.ClearExpiredProofedNames(ctx); err != nil || n != 0 {
		t.Fatalf("cleared %d within retention, %v; want none", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET proofed_name_purge_after = now() - interval '1 minute'
		WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("age the name: %v", err)
	}
	if n, err := store.ClearExpiredProofedNames(ctx); err != nil || n != 1 {
		t.Fatalf("cleared %d past retention, %v; want 1", n, err)
	}
	got, err := store.Get(ctx, orgID, req.ID)
	if err != nil || got.ProofedName != "" || got.Status != StatusNeedsReview {
		t.Errorf("request = %q %s, %v; want the name gone and the review kept", got.ProofedName, got.Status, err)
	}
}

// A list narrowed to one member holds only the requests proofing them.
func TestRequestListBySubjectUser(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	alex := makeUser(t, pool, "alex@example.org")
	ctx := context.Background()
	mine := createStarted(t, store, newStoredRequest(orgID, sam, Subject{Kind: SubjectMember, UserID: &alex, Email: "alex@example.org"}), "s1")
	if _, err := store.Create(ctx, newStoredRequest(orgID, alex, Subject{Kind: SubjectMember, UserID: &sam, Email: "sam@example.org"})); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.List(ctx, orgID, RequestFilter{SubjectUserID: &alex})
	if err != nil || len(got) != 1 || got[0].ID != mine.ID {
		t.Errorf("List for alex = %d rows, %v; want only the request proofing alex", len(got), err)
	}
}

// The cap on a list applies after the narrowing: an external request older
// than the newest maxListedRequests member ones is still listed when only the
// external ones are asked for.
func TestRequestListBySubjectKind(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	alex := makeUser(t, pool, "alex@example.org")
	ctx := context.Background()
	external, err := store.Create(ctx, newStoredRequest(orgID, sam, externalSubject("a@example.org")))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for range maxListedRequests {
		if _, err := store.Create(ctx, newStoredRequest(orgID, sam, Subject{Kind: SubjectMember, UserID: &alex, Email: "alex@example.org"})); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	all, err := store.List(ctx, orgID, RequestFilter{})
	if err != nil || len(all) != maxListedRequests || slices.ContainsFunc(all, func(r Request) bool { return r.ID == external.ID }) {
		t.Fatalf("List = %d rows, %v; want the newest %d, all members'", len(all), err, maxListedRequests)
	}
	kind := SubjectExternal
	got, err := store.List(ctx, orgID, RequestFilter{SubjectKind: &kind})
	if err != nil || len(got) != 1 || got[0].ID != external.ID {
		t.Errorf("List external = %d rows, %v; want only the external request", len(got), err)
	}
	kind = SubjectMember
	if got, err = store.List(ctx, orgID, RequestFilter{SubjectKind: &kind}); err != nil || len(got) != maxListedRequests {
		t.Errorf("List members = %d rows, %v; want %d", len(got), err, maxListedRequests)
	}
}

// The cap on a list applies after the flow narrowing too: a quiet flow's one
// request, older than the newest maxListedRequests on a busy flow, is listed
// when only its flow is asked for, and the flow combines with the subject kind.
func TestRequestListByFlow(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	alex := makeUser(t, pool, "alex@example.org")
	ctx := context.Background()
	onFlow := func(subject Subject, flowID string) NewStoredRequest {
		in := newStoredRequest(orgID, sam, subject)
		in.Flow.ID = flowID
		return in
	}
	const quietFlow, busyFlow = "quiet", "busy"
	quiet, err := store.Create(ctx, onFlow(externalSubject("a@example.org"), quietFlow))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Create(ctx, onFlow(Subject{Kind: SubjectMember, UserID: &alex, Email: "alex@example.org"}, quietFlow)); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for range maxListedRequests {
		if _, err := store.Create(ctx, onFlow(externalSubject("b@example.org"), busyFlow)); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	kind := SubjectExternal
	all, err := store.List(ctx, orgID, RequestFilter{SubjectKind: &kind})
	if err != nil || len(all) != maxListedRequests || slices.ContainsFunc(all, func(r Request) bool { return r.ID == quiet.ID }) {
		t.Fatalf("List external = %d rows, %v; want the newest %d, all on the busy flow", len(all), err, maxListedRequests)
	}
	flowID := quietFlow
	got, err := store.List(ctx, orgID, RequestFilter{SubjectKind: &kind, FlowID: &flowID})
	if err != nil || len(got) != 1 || got[0].ID != quiet.ID {
		t.Errorf("List external on the quiet flow = %d rows, %v; want only its external request", len(got), err)
	}
	if got, err = store.List(ctx, orgID, RequestFilter{FlowID: &flowID}); err != nil || len(got) != 2 {
		t.Errorf("List on the quiet flow = %d rows, %v; want both its requests", len(got), err)
	}
	flowID = busyFlow
	if got, err = store.List(ctx, orgID, RequestFilter{FlowID: &flowID}); err != nil || len(got) != maxListedRequests {
		t.Errorf("List on the busy flow = %d rows, %v; want %d", len(got), err, maxListedRequests)
	}
}

// A request for one known person drops the expected birth date as soon as no
// match can follow: here, cancelled.
func TestCancelDropsBirthDate(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, requester,
		Subject{Kind: SubjectExternal, Name: "Anna de Vries", Email: "anna@example.org", BirthDate: "1990-04-12"}), "s1")
	if ok, err := store.Cancel(ctx, req, nil); err != nil || !ok {
		t.Fatalf("Cancel = %v, %v", ok, err)
	}
	var birthDateCT []byte
	if err := pool.QueryRow(ctx, `SELECT expected_birth_date_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&birthDateCT); err != nil || birthDateCT != nil {
		t.Errorf("birth date after cancel = %d bytes, %v; want it dropped", len(birthDateCT), err)
	}
}

// A flow's own retention sets the purge, in place of the org's: a week
// brings it forward, a year pushes it back.
func TestPurgeFollowsFlowRetention(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		override time.Duration
		age      string
		due      bool
	}{
		{"a week's override, 8 days on", 7 * 24 * time.Hour, "8 days", true},
		{"a year's override, 31 days on", 365 * 24 * time.Hour, "31 days", false},
	} {
		if _, err := pool.Exec(ctx, `DELETE FROM identity_proofing_requests`); err != nil {
			t.Fatalf("clear: %v", err)
		}
		in := newStoredRequest(orgID, sam, externalSubject("a@example.org"))
		in.Flow.RetentionOverrideSeconds = int(tc.override.Seconds())
		req := createStarted(t, store, in, "s-"+tc.age)
		if req.RetentionOverride != tc.override || req.NameRetention != tc.override {
			t.Errorf("%s: retention = %v, name %v; want the flow's", tc.name, req.RetentionOverride, req.NameRetention)
		}
		if err := store.RecordOutcome(ctx, req, "s-"+tc.age, StatusApproved, proofingprovider.Result{Status: proofingprovider.StatusApproved}); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests SET completed_at = now() - $2::interval WHERE id = $1`, req.ID, tc.age); err != nil {
			t.Fatalf("age the request: %v", err)
		}
		// A raw write: the store would set purge_at in the same transaction.
		if err := refreshPurgeAt(ctx, pool, req.ID); err != nil {
			t.Fatalf("refresh purge time: %v", err)
		}
		due, err := store.ListPurgeDue(ctx, 10)
		if err != nil || (len(due) == 1) != tc.due {
			t.Errorf("%s: purge due = %d, %v; want due %v", tc.name, len(due), err, tc.due)
		}
	}
}

// A session's device event lands in the audit log on its request; one for a
// session the org holds no request of records nothing.
func TestRecordDeviceEvent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NewDBRecorder(), newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, store, newStoredRequest(orgID, sam, Subject{Kind: SubjectMember, UserID: &sam, Email: "sam@example.org"}), "s1")
	if err := store.RecordDeviceEvent(ctx, orgID.String(), "s1", "device_claimed", map[string]any{"role": "native", "deviceId": "d1"}); err != nil {
		t.Fatalf("RecordDeviceEvent: %v", err)
	}
	var target, metadata string
	if err := pool.QueryRow(ctx, `SELECT target_id, metadata::text FROM audit_events WHERE action = $1`,
		audit.IdentityProofingDeviceClaimed).Scan(&target, &metadata); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if target != req.ID.String() || !strings.Contains(metadata, "d1") {
		t.Errorf("audited on %s with %s; want the request, with the device", target, metadata)
	}
	for _, tc := range []struct{ tenant, session string }{{orgID.String(), "unknown"}, {uuid.NewString(), "s1"}} {
		if err := store.RecordDeviceEvent(ctx, tc.tenant, tc.session, "access_denied", nil); err != nil {
			t.Errorf("RecordDeviceEvent(%s, %s) = %v, want nothing recorded", tc.tenant, tc.session, err)
		}
	}
	if n := countAction(t, pool, audit.IdentityProofingAccessDenied); n != 0 {
		t.Errorf("access_denied audits = %d, want none", n)
	}
	if err := store.RecordDeviceEvent(ctx, orgID.String(), "s1", "nonsense", nil); err == nil {
		t.Error("an unknown device event was accepted")
	}
}

func countAction(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", action, err)
	}
	return n
}

// Purging a request clears the redirect and the Yivi transaction, which point
// at its subject, as well as the subject's own details.
func TestPurgeClearsPointers(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	cipher := newTestCipher(t)
	requests := NewRequestStore(pool, audit.NopRecorder{}, cipher)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()
	req := createStarted(t, requests, newStoredRequest(orgID, sam, externalSubject("a@example.org")), "s1")
	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_requests
		SET redirect_url = 'https://shop.example.org/back?user=anna', yivi_transaction_id = 'tx-1' WHERE id = $1`, req.ID); err != nil {
		t.Fatal(err)
	}
	if err := requests.Purge(ctx, req, nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	var redirect, transaction *string
	if err := pool.QueryRow(ctx, `SELECT redirect_url, yivi_transaction_id FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&redirect, &transaction); err != nil {
		t.Fatal(err)
	}
	if redirect != nil || transaction != nil {
		t.Errorf("after purge: redirect %v, yivi transaction %v; want both cleared", redirect, transaction)
	}
}
