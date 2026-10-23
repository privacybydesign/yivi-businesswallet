//go:build integration

package session_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func newStore(t *testing.T) (*session.PostgresStore, string) {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	var orgID string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO organizations (name, slug, kvk_number, euid, digital_address) VALUES ('Acme', 'acme', '12345678', 'NLNHR.12345678', 'acme@example.com') RETURNING id::text`,
	).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	cipher, err := crypto.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return session.NewPostgresStore(pool, cipher), orgID
}

func TestStoreRoundTripAndHandover(t *testing.T) {
	s, org := newStore(t)
	created, err := s.Create(session.Session{TenantID: org, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Authenticate(created.Token); err != nil || got.ID != created.ID {
		t.Fatalf("authenticate = %+v, %v", got.ID, err)
	}
	if _, err := s.AuthenticateSession(org, created.ID, "wrong"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("wrong token err = %v", err)
	}
	var token string
	if _, err := s.Update(org, created.ID, func(sess *session.Session) error {
		token, err = sess.Access.MintHandover(session.DeviceRoleNative, time.Now().Add(time.Minute))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	found, err := s.FindByHandover(session.HashAccessToken(token))
	if err != nil || found.ID != created.ID || found.Token != created.Token {
		t.Fatalf("find = %+v, %v", found.ID, err)
	}
	if _, err := s.FindByHandover(session.HashAccessToken("nope")); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("unknown err = %v", err)
	}
	if _, err := s.Get("00000000-0000-0000-0000-000000000000", created.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("other tenant err = %v", err)
	}
}

func TestStoreExpiresLazilyOnce(t *testing.T) {
	s, org := newStore(t)
	var expired int
	s.SetOnExpire(func(session.Session) { expired++ })
	created, err := s.Create(session.Session{TenantID: org, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := s.Get(org, created.ID)
		if err != nil || got.Status != session.StatusExpired {
			t.Fatalf("get = %q, %v", got.Status, err)
		}
	}
	if expired != 1 {
		t.Fatalf("onExpire ran %d times, want 1", expired)
	}
	removed, err := s.Purge(time.Second, time.Now().Add(time.Minute))
	if err != nil || len(removed) != 1 {
		t.Fatalf("purge = %d, %v", len(removed), err)
	}
}

// A backlog larger than one removal batch is worked off in one Purge: each
// batch has its own timeout, and the loop runs until a batch comes back short.
func TestStorePurgeManyBatches(t *testing.T) {
	s, org := newStore(t)
	const sessions = 150 // more than the store's removal batch of 100
	for range sessions {
		if _, err := s.Create(session.Session{TenantID: org, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(-time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := s.Purge(time.Second, time.Now().Add(time.Minute))
	if err != nil || len(removed) != sessions {
		t.Fatalf("purge = %d, %v; want all %d", len(removed), err, sessions)
	}
}

// A row's sealed data copied into another org's row does not open there: with
// write access to the database, one org's evidence must not read as another's.
func TestStoreRefusesMovedData(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	org := func(slug string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, 'NLNHR.' || $1, $1 || '@example.com') RETURNING id::text`, slug).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	orgA, orgB := org("org-a"), org("org-b")
	cipher, err := crypto.NewCipher(testKey)
	if err != nil {
		t.Fatal(err)
	}
	s := session.NewPostgresStore(pool, cipher)
	a, err := s.Create(session.Session{TenantID: orgA, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(session.Session{TenantID: orgB, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `UPDATE identity_proofing_sessions SET data = (
		SELECT data FROM identity_proofing_sessions WHERE id = $1) WHERE id = $2`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(orgB, b.ID); err == nil {
		t.Errorf("org B's row opened org A's session %s", got.ID)
	}
	if _, err := s.Update(orgB, b.ID, func(*session.Session) error { return nil }); err == nil {
		t.Error("Update opened org A's session in org B's row")
	}
}

// ChangedAt moves on every write and on nothing else, and is the row's own:
// another org's tenant id reads nothing.
func TestStoreChangedAt(t *testing.T) {
	s, org := newStore(t)
	created, err := s.Create(session.Session{TenantID: org, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ChangedAt(org, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(org, created.ID); err != nil {
		t.Fatal(err)
	}
	if again, err := s.ChangedAt(org, created.ID); err != nil || !again.Equal(first) {
		t.Fatalf("after a read = %v, %v; want %v", again, err, first)
	}
	if _, err := s.Update(org, created.ID, func(sess *session.Session) error {
		sess.Status = session.StatusInProgress
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if moved, err := s.ChangedAt(org, created.ID); err != nil || !moved.After(first) {
		t.Fatalf("after a write = %v, %v; want after %v", moved, err, first)
	}
	if _, err := s.ChangedAt("00000000-0000-0000-0000-000000000000", created.ID); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("other tenant err = %v", err)
	}
}

// SetRetention changes how long the named sessions of one tenant are kept once
// they ended, and no other session's.
func TestStoreSetRetention(t *testing.T) {
	s, org := newStore(t)
	expired := func() session.Session {
		sess, err := s.Create(session.Session{
			TenantID: org, Method: session.MethodNFCPassport, ExpiresAt: time.Now().Add(-time.Second), RetentionOverride: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	extended, ofAnotherTenant, untouched := expired(), expired(), expired()

	if err := s.SetRetention(org, []string{extended.ID}, time.Hour); err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	if err := s.SetRetention("00000000-0000-0000-0000-000000000000", []string{ofAnotherTenant.ID}, time.Hour); err != nil {
		t.Fatalf("SetRetention of another tenant: %v", err)
	}
	purgedAt := func(after time.Duration) []string {
		removed, err := s.Purge(0, time.Now().Add(after))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, sess := range removed {
			ids = append(ids, sess.ID)
		}
		return ids
	}
	if got := purgedAt(10 * time.Minute); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values([]string{ofAnotherTenant.ID, untouched.ID}))) {
		t.Errorf("purge after 10 minutes removed %v; want the two sessions on their own minute", got)
	}
	if got := purgedAt(2 * time.Hour); !slices.Equal(got, []string{extended.ID}) {
		t.Errorf("purge after 2 hours removed %v; want the extended session", got)
	}
}
