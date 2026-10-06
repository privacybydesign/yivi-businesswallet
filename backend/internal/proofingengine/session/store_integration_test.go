//go:build integration

package session_test

import (
	"context"
	"errors"
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
