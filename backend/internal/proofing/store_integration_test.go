//go:build integration

package proofing

import (
	"bytes"
	"context"
	"errors"
	"slices"
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

func newStoredRequest(orgID, requestedBy uuid.UUID, subject Member, sessionID string) NewStoredRequest {
	expires := time.Now().Add(SessionTTL)
	return NewStoredRequest{
		ID: uuid.New(), OrgID: orgID, RequestedBy: requestedBy, Subject: subject,
		Flow:          proofingprovider.Flow{FlowSpec: proofingprovider.FlowSpec{Name: "Passport + face"}, ID: "f1", Version: 3},
		Session:       proofingprovider.Session{ID: sessionID, Token: "rp-token", ExpiresAt: expires},
		LinkExpiresAt: expires,
	}
}

func TestRequestStoreMembersListsEveryRole(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	admin := makeUser(t, pool, "admin@example.org")
	member := makeUser(t, pool, "member@example.org")
	makeMember(t, pool, orgID, admin, "admin")
	makeMember(t, pool, orgID, member, "member")
	outsider := makeUser(t, pool, "outsider@example.org")
	ctx := context.Background()

	members, err := store.Members(ctx, orgID)
	if err != nil || len(members) != 2 {
		t.Fatalf("Members = %+v, %v; want the admin and the member", members, err)
	}
	if members[0].Name != "Sam de Vries" || members[0].MemberType != "employee" {
		t.Errorf("member = %+v", members[0])
	}
	if _, err := store.Member(ctx, orgID, outsider); !errors.Is(err, ErrMemberNotFound) {
		t.Errorf("Member(outsider) = %v, want ErrMemberNotFound", err)
	}
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

	req, raw, err := store.Create(ctx, newStoredRequest(orgID, requester, subject, "s1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if req.Status != StatusPending || req.RequestedByName != "Sam de Vries" || req.SubjectUserID == nil ||
		*req.SubjectUserID != subjectID || req.FlowVersion != 3 || req.session == nil || req.session.Token != "rp-token" {
		t.Errorf("created = %+v", req)
	}

	var hash, tokenCT []byte
	if err := pool.QueryRow(ctx, `SELECT token_hash, ips_session_token_ciphertext FROM identity_proofing_requests WHERE id = $1`,
		req.ID).Scan(&hash, &tokenCT); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if bytes.Contains(hash, []byte(raw)) || bytes.Contains(tokenCT, []byte("rp-token")) {
		t.Error("the link token or the session token is stored in the clear")
	}

	link, err := store.ByToken(ctx, raw)
	if err != nil || link.OrganizationName != "acme" || link.Request.ID != req.ID {
		t.Fatalf("ByToken = %+v, %v", link, err)
	}
	if _, err := store.ByToken(ctx, "unknown"); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("unknown token = %v, want ErrLinkNotFound", err)
	}

	for range 2 {
		if err := store.MarkStarted(ctx, link.Request, "s1"); err != nil {
			t.Fatalf("MarkStarted: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingSessionStarted); n != 1 {
		t.Errorf("session_started audits = %d, want 1", n)
	}
	link, _ = store.ByToken(ctx, raw)

	res := proofingprovider.Result{Status: proofingprovider.StatusApproved, AssuranceLevel: "high", EIDASLevel: "substantial"}
	for range 2 {
		if err := store.RecordOutcome(ctx, link.Request, "s1", StatusApproved, res); err != nil {
			t.Fatalf("RecordOutcome: %v", err)
		}
	}
	if n := auditCount(t, pool, audit.IdentityProofingCompleted); n != 1 {
		t.Errorf("completed audits = %d, want 1", n)
	}

	mine, err := store.List(ctx, orgID, &requester)
	if err != nil || len(mine) != 1 || mine[0].Status != StatusApproved || mine[0].EIDASLevel != "substantial" {
		t.Fatalf("List own = %+v, %v", mine, err)
	}
	other := uuid.New()
	if theirs, err := store.List(ctx, orgID, &other); err != nil || len(theirs) != 0 {
		t.Errorf("List for another member = %+v, %v; want none", theirs, err)
	}
}

func TestRequestStoreExpireLinkEndsTheLink(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := NewRequestStore(pool, audit.NopRecorder{}, newTestCipher(t))
	orgID := makeOrg(t, pool, "acme")
	requester := makeUser(t, pool, "sam@example.org")
	ctx := context.Background()

	req, raw, err := store.Create(ctx, newStoredRequest(orgID, requester, Member{UserID: requester, Name: "Sam", Email: "sam@example.org"}, "s1"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.ExpireLink(ctx, req.ID, "s1"); err != nil {
		t.Fatalf("ExpireLink: %v", err)
	}
	if _, err := store.ByToken(ctx, raw); !errors.Is(err, ErrLinkNotFound) {
		t.Errorf("ByToken after expiry = %v, want ErrLinkNotFound", err)
	}
}
