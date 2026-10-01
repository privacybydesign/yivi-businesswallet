//go:build integration

package openid4vprequester_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

func createOrg(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $1, 'kvk-' || $1, 'NL.KVK.' || $1, $1 || '@qerds.localhost') RETURNING id`, slug).Scan(&id); err != nil {
		t.Fatalf("create org: %v", err)
	}
	return id
}

func newRequest(orgID uuid.UUID, expiresAt time.Time) openid4vprequester.NewRequest {
	return openid4vprequester.NewRequest{
		ID: uuid.New(), OrganizationID: orgID, SenderAddress: requesterAddress, RecipientAddress: holderAddress,
		ClientID: "x509_hash:abc", Nonce: "nonce", State: "state",
		Credentials:   []openid4vprequester.CredentialRequest{{ID: "credential_1", VCT: requestedVCT, Claims: []string{"legalName"}}},
		RequestObject: "header.payload.signature", ExpiresAt: expiresAt,
	}
}

func auditActions(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT action FROM audit_events WHERE target_id = $1 ORDER BY occurred_at`, id.String())
	if err != nil {
		t.Fatalf("audit events: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestStoreLifecycle(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	orgID, otherOrg := createOrg(t, pool, "acme"), createOrg(t, pool, "globex")
	store := openid4vprequester.NewStore(pool, audit.NewDBRecorder())

	r, err := store.Create(ctx, newRequest(orgID, time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.Status != openid4vprequester.StatusSent || len(r.Credentials) != 1 || r.Credentials[0].Claims[0] != "legalName" {
		t.Fatalf("created = %+v", r)
	}

	// Scoped per organization.
	if _, err := store.GetForOrg(ctx, otherOrg, r.ID); !errors.Is(err, openid4vprequester.ErrNotFound) {
		t.Errorf("another org's GetForOrg: err = %v, want %v", err, openid4vprequester.ErrNotFound)
	}
	if list, err := store.ListForOrg(ctx, otherOrg); err != nil || len(list) != 0 {
		t.Errorf("another org's list = %v, %v", list, err)
	}
	if list, err := store.ListForOrg(ctx, orgID); err != nil || len(list) != 1 {
		t.Errorf("own list = %v, %v", list, err)
	}

	// The Request Object is handed out exactly once.
	jar, err := store.FetchRequestObject(ctx, r.ID)
	if err != nil || jar != "header.payload.signature" {
		t.Fatalf("first fetch = %q, %v", jar, err)
	}
	if _, err := store.FetchRequestObject(ctx, r.ID); !errors.Is(err, openid4vprequester.ErrNotFound) {
		t.Errorf("second fetch: err = %v, want %v", err, openid4vprequester.ErrNotFound)
	}

	disclosed := []openid4vprequester.DisclosedCredential{{QueryID: "credential_1", VCT: requestedVCT, Issuer: "https://i.test", Claims: map[string]any{"legalName": "Globex"}}}
	if err := store.Complete(ctx, r.ID, disclosed); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if err := store.Complete(ctx, r.ID, disclosed); !errors.Is(err, openid4vprequester.ErrNotPending) {
		t.Errorf("second Complete: err = %v, want %v", err, openid4vprequester.ErrNotPending)
	}
	if err := store.Fail(ctx, r.ID, openid4vprequester.ReasonVerificationFailed); !errors.Is(err, openid4vprequester.ErrNotPending) {
		t.Errorf("Fail after Complete: err = %v, want %v", err, openid4vprequester.ErrNotPending)
	}
	got, err := store.GetForOrg(ctx, orgID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != openid4vprequester.StatusCompleted || got.RespondedAt == nil || got.RequestFetchedAt == nil ||
		len(got.Disclosed) != 1 || got.Disclosed[0].Claims["legalName"] != "Globex" {
		t.Errorf("completed = %+v", got)
	}

	actions := auditActions(t, pool, r.ID)
	if len(actions) != 2 || actions[0] != audit.PresentationRequestSent || actions[1] != audit.PresentationResponseReceived {
		t.Errorf("audit trail = %v", actions)
	}
}

// A failed request is audited as a failure, never as a received answer.
func TestStoreFailIsAuditedAsAFailure(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := openid4vprequester.NewStore(pool, audit.NewDBRecorder())
	r, err := store.Create(ctx, newRequest(createOrg(t, pool, "acme"), time.Now().Add(time.Hour)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.Fail(ctx, r.ID, openid4vprequester.ReasonVerificationFailed); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	actions := auditActions(t, pool, r.ID)
	if len(actions) != 2 || actions[1] != audit.PresentationRequestFailed {
		t.Errorf("audit trail = %v, want it to end in %s", actions, audit.PresentationRequestFailed)
	}
}

// An expired request serves nothing and settles nothing.
func TestStoreExpiredRequestIsClosed(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	store := openid4vprequester.NewStore(pool, audit.NewDBRecorder())
	r, err := store.Create(ctx, newRequest(createOrg(t, pool, "acme"), time.Now().Add(-time.Minute)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.FetchRequestObject(ctx, r.ID); !errors.Is(err, openid4vprequester.ErrNotFound) {
		t.Errorf("fetch: err = %v, want %v", err, openid4vprequester.ErrNotFound)
	}
	if err := store.Fail(ctx, r.ID, openid4vprequester.ReasonVerificationFailed); !errors.Is(err, openid4vprequester.ErrNotPending) {
		t.Errorf("Fail: err = %v, want %v", err, openid4vprequester.ErrNotPending)
	}
	if got := r.EffectiveStatus(time.Now()); got != openid4vprequester.StatusExpired {
		t.Errorf("effective status = %q, want expired", got)
	}
}
