//go:build integration

package proofing

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// failingFinishDB fails the statement that stores an idempotent answer.
type failingFinishDB struct {
	database.DB
}

var errStoreAnswer = errors.New("store answer failed")

func (d failingFinishDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "SET status_code") {
		return pgconn.CommandTag{}, errStoreAnswer
	}
	return d.DB.Exec(ctx, sql, args...)
}

// idemFixture is an org with an API key, calling an idempotent route in front
// of next.
type idemFixture struct {
	pool   *pgxpool.Pool
	orgID  uuid.UUID
	keyID  uuid.UUID
	userID uuid.UUID
}

func newIdemFixture(t *testing.T) idemFixture {
	t.Helper()
	pool, _ := testdb.Fresh(t)
	orgID := makeOrg(t, pool, "acme")
	sam := makeUser(t, pool, "sam@example.org")
	return idemFixture{pool: pool, orgID: orgID, keyID: makeAPIKey(t, pool, orgID, sam), userID: sam}
}

// makeAPIKey creates an API key of the org and returns its id.
func makeAPIKey(t *testing.T, pool *pgxpool.Pool, orgID, createdBy uuid.UUID) uuid.UUID {
	t.Helper()
	key, _, err := NewAPIKeyStore(pool, audit.NopRecorder{}).Create(context.Background(), orgID, createdBy, "Backend", APIKeyScopes)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return key.ID
}

// call sends one keyed POST through an idempotent next on store.
func (f idemFixture) call(t *testing.T, store *IdempotencyStore, key string, next respond.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	h := &Handler{idempotency: store}
	r := httptest.NewRequest(http.MethodPost, "/proofing/sessions", strings.NewReader(`{"email":"a@example.org"}`))
	r.Header.Set(idempotencyHeader, key)
	r = r.WithContext(context.WithValue(r.Context(), apiCallerKey{}, APIKeyCaller{KeyID: f.keyID, Org: Org{ID: f.orgID}}))
	w := httptest.NewRecorder()
	h.idempotent(next).ServeHTTP(w, r)
	return w
}

func (f idemFixture) keyCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM identity_proofing_idempotency_keys WHERE organization_id = $1`, f.orgID).Scan(&n); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	return n
}

// answerWith answers 201 with a session id, counting its runs.
func answerWith(id uuid.UUID, runs *int) respond.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) error {
		*runs++
		w.WriteHeader(http.StatusCreated)
		_, err := w.Write([]byte(`{"id":"` + publicSessionID(id) + `"}`))
		return err
	}
}

// A key is the org's: another org sending the same key runs its own call, and a
// retry in the first org replays its answer.
func TestIdemKeyIsPerOrg(t *testing.T) {
	f := newIdemFixture(t)
	cipher := newTestCipher(t)
	store := NewIdempotencyStore(f.pool, cipher)
	requests := NewRequestStore(f.pool, audit.NopRecorder{}, cipher)
	other := f
	other.orgID = makeOrg(t, f.pool, "globex")
	other.keyID = makeAPIKey(t, f.pool, other.orgID, f.userID)
	ctx := context.Background()
	// The stored answer names a session, which has to exist.
	ours, err := requests.Create(ctx, newStoredRequest(f.orgID, f.userID, externalSubject("a@example.org")))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	theirs, err := requests.Create(ctx, newStoredRequest(other.orgID, f.userID, externalSubject("a@example.org")))
	if err != nil {
		t.Fatalf("create other org's request: %v", err)
	}
	runs := 0

	if first := f.call(t, store, "k1", answerWith(ours.ID, &runs)); first.Code != http.StatusCreated || runs != 1 {
		t.Fatalf("first call = %d after %d runs, want 201 from one run", first.Code, runs)
	}
	if second := other.call(t, store, "k1", answerWith(theirs.ID, &runs)); second.Code != http.StatusCreated || runs != 2 {
		t.Errorf("other org's call = %d after %d runs, want 201 from its own run", second.Code, runs)
	}
	replay := f.call(t, store, "k1", answerWith(ours.ID, &runs))
	if replay.Code != http.StatusCreated || replay.Header().Get(headerIdempotentReplayed) != "true" || runs != 2 {
		t.Errorf("retry = %d (replayed %q) after %d runs, want the first answer replayed",
			replay.Code, replay.Header().Get(headerIdempotentReplayed), runs)
	}
	if n, m := f.keyCount(t), other.keyCount(t); n != 1 || m != 1 {
		t.Errorf("keys held = %d and %d, want one each", n, m)
	}
}

// A key is the API key's: another key of the same org sending the same one
// runs its own call and is never replayed the first key's answer, which
// carries its subject's details; each key's retry replays its own.
func TestIdemKeyIsPerAPIKey(t *testing.T) {
	f := newIdemFixture(t)
	cipher := newTestCipher(t)
	store := NewIdempotencyStore(f.pool, cipher)
	requests := NewRequestStore(f.pool, audit.NopRecorder{}, cipher)
	other := f
	other.keyID = makeAPIKey(t, f.pool, f.orgID, f.userID)
	ctx := context.Background()
	ours, err := requests.Create(ctx, newStoredRequest(f.orgID, f.userID, externalSubject("a@example.org")))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	theirs, err := requests.Create(ctx, newStoredRequest(f.orgID, f.userID, externalSubject("b@example.org")))
	if err != nil {
		t.Fatalf("create the other key's request: %v", err)
	}
	runs := 0

	if first := f.call(t, store, "k1", answerWith(ours.ID, &runs)); first.Code != http.StatusCreated || runs != 1 {
		t.Fatalf("first call = %d after %d runs, want 201 from one run", first.Code, runs)
	}
	second := other.call(t, store, "k1", answerWith(theirs.ID, &runs))
	if second.Code != http.StatusCreated || runs != 2 || second.Header().Get(headerIdempotentReplayed) != "" ||
		!strings.Contains(second.Body.String(), publicSessionID(theirs.ID)) {
		t.Errorf("other key's call = %d (replayed %q, %s) after %d runs, want 201 from its own run",
			second.Code, second.Header().Get(headerIdempotentReplayed), second.Body.String(), runs)
	}
	for _, c := range []struct {
		caller idemFixture
		want   uuid.UUID
	}{{f, ours.ID}, {other, theirs.ID}} {
		replay := c.caller.call(t, store, "k1", answerWith(c.want, &runs))
		if replay.Header().Get(headerIdempotentReplayed) != "true" || !strings.Contains(replay.Body.String(), publicSessionID(c.want)) {
			t.Errorf("retry = %s (replayed %q), want its own key's answer replayed",
				replay.Body.String(), replay.Header().Get(headerIdempotentReplayed))
		}
	}
	if runs != 2 {
		t.Errorf("runs = %d, want the two first calls only", runs)
	}
}

func TestIdemFinishFailKeepsKey(t *testing.T) {
	f := newIdemFixture(t)
	cipher := newTestCipher(t)
	runs := 0
	next := answerWith(uuid.New(), &runs)

	first := f.call(t, NewIdempotencyStore(failingFinishDB{f.pool}, cipher), "k1", next)
	if first.Code != http.StatusCreated {
		t.Errorf("first call = %d, want the call's own 201", first.Code)
	}
	retry := f.call(t, NewIdempotencyStore(f.pool, cipher), "k1", next)
	if retry.Code != http.StatusConflict || runs != 1 {
		t.Errorf("retry = %d after %d runs, want 409 and one run", retry.Code, runs)
	}
}

func TestIdemLostCallStaysInFlight(t *testing.T) {
	f := newIdemFixture(t)
	store := NewIdempotencyStore(f.pool, newTestCipher(t))
	hash := []byte("hash")
	// A claim from a replica that died an hour ago.
	if _, err := f.pool.Exec(context.Background(), `INSERT INTO identity_proofing_idempotency_keys
		(organization_id, api_key_id, key, request_hash, created_at) VALUES ($1, $2, 'k1', $3, now() - interval '1 hour')`,
		f.orgID, f.keyID, hash); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	if _, err := store.begin(context.Background(), idempotencyScope{orgID: f.orgID, apiKeyID: f.keyID}, "k1", hash); !errors.Is(err, errIdempotencyInFlight) {
		t.Errorf("begin = %v, want errIdempotencyInFlight", err)
	}
}

func TestIdemPanicReleasesKey(t *testing.T) {
	f := newIdemFixture(t)
	store := NewIdempotencyStore(f.pool, newTestCipher(t))
	boom := errors.New("boom")
	func() {
		defer func() {
			if p := recover(); p != boom {
				t.Errorf("recovered %v, want the handler's panic again", p)
			}
		}()
		f.call(t, store, "k1", func(http.ResponseWriter, *http.Request) error { panic(boom) })
	}()
	if n := f.keyCount(t); n != 0 {
		t.Errorf("%d keys after a panic, want the key released", n)
	}
	runs := 0
	if retry := f.call(t, store, "k1", answerWith(uuid.New(), &runs)); retry.Code != http.StatusCreated || runs != 1 {
		t.Errorf("retry = %d after %d runs, want 201 from one run", retry.Code, runs)
	}
}

func TestPurgeDropsIdempotent(t *testing.T) {
	f := newIdemFixture(t)
	store := NewIdempotencyStore(f.pool, newTestCipher(t))
	requests := NewRequestStore(f.pool, audit.NopRecorder{}, newTestCipher(t))
	ctx := context.Background()
	var reqs []Request
	for range 2 {
		req, err := requests.Create(ctx, newStoredRequest(f.orgID, f.userID, externalSubject("a@example.org")))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}
		reqs = append(reqs, req)
	}
	runs := 0
	f.call(t, store, "purged", answerWith(reqs[0].ID, &runs))
	f.call(t, store, "kept", answerWith(reqs[1].ID, &runs))

	if err := requests.Purge(ctx, reqs[0], nil); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	var keys []string
	rows, err := f.pool.Query(ctx, `SELECT key FROM identity_proofing_idempotency_keys`)
	if err != nil {
		t.Fatalf("list keys: %v", err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("scan key: %v", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list keys: %v", err)
	}
	if len(keys) != 1 || keys[0] != "kept" {
		t.Errorf("keys after purge = %v, want only the other session's", keys)
	}
}
