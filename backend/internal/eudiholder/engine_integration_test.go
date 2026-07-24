//go:build integration

package eudiholder_test

import (
	"context"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/eudiholder"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// newTestEngine builds an irmago-backed Engine against a fresh test database,
// returning it alongside the raw pool (for asserting per-schema isolation).
func newTestEngine(t *testing.T) (*eudiholder.Engine, *pgxpool.Pool) {
	t.Helper()
	pool, dsn := testdb.Fresh(t)
	var key [32]byte
	for i := range key {
		key[i] = byte(i + 1)
	}
	eng := eudiholder.NewEngine(dsn, t.TempDir(), key, eudiholder.RedeemConfig{})
	t.Cleanup(func() { _ = eng.Close() })
	return eng, pool
}

func sampleCredential(vct, hash string) eudiholder.Credential {
	return eudiholder.Credential{
		VCT:              vct,
		IssuerURL:        "https://issuer.test",
		CredentialIssuer: "https://issuer.test",
		Hash:             hash,
		RawToken:         []byte("raw-sd-jwt-vc-token"),
		ProcessedPayload: []byte(fmt.Sprintf(`{"vct":%q,"company_name":"Demo B.V."}`, vct)),
		IssuedAt:         time.Unix(1_700_000_000, 0).UTC(),
	}
}

// countInstances reads the credential-instance count directly from an org's
// isolated Postgres schema. The schema naming mirrors Engine.schemaFor.
func countInstances(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) int {
	t.Helper()
	return countRows(t, pool, orgID, "issued_credential_instances")
}

// countBatches reads the CredentialBatch count from an org's schema. Store
// persists one batch per credential (BatchSize:1), and the batch — not the
// instance — carries the decoded SD-JWT payload, so Delete must leave it at 0.
func countBatches(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID) int {
	t.Helper()
	return countRows(t, pool, orgID, "credential_batches")
}

func countRows(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, table string) int {
	t.Helper()
	schema := "holder_" + hex.EncodeToString(orgID[:])
	var n int
	//nolint:gosec // schema + table are fixed identifiers, not user input.
	err := pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT count(*) FROM %q.%q`, schema, table)).Scan(&n)
	if err != nil {
		t.Fatalf("count %s for %s: %v", table, orgID, err)
	}
	return n
}

func TestEnginePing(t *testing.T) {
	eng, _ := newTestEngine(t)
	if err := eng.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestEngineStoreDeleteRoundTrip(t *testing.T) {
	eng, pool := newTestEngine(t)
	ctx := context.Background()
	org := uuid.New()

	ref, err := eng.Store(ctx, org, sampleCredential("nl.kvk.registration", "hash-1"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := uuid.Parse(ref); err != nil {
		t.Fatalf("store ref %q is not a uuid: %v", ref, err)
	}
	if got := countInstances(t, pool, org); got != 1 {
		t.Fatalf("expected 1 instance after store, got %d", got)
	}
	if got := countBatches(t, pool, org); got != 1 {
		t.Fatalf("expected 1 batch after store, got %d", got)
	}

	// Claims decodes the stored payload's attributes and strips the registered vct.
	claims, err := eng.Claims(ctx, org, ref, "nl.kvk.registration", "en")
	if err != nil {
		t.Fatalf("claims: %v", err)
	}
	if got := attributeValue(claims.Attributes, "company_name"); got != "Demo B.V." {
		t.Fatalf("claims[company_name] = %v, want the demo value", got)
	}
	if hasAttribute(claims.Attributes, "vct") {
		t.Fatal("claims should not include the registered vct claim")
	}

	// The vct fallback recovers the batch when the instance ref is empty — the
	// case irmago's redemption produces (unpopulated CredentialInstanceIds).
	viaVCT, err := eng.Claims(ctx, org, "", "nl.kvk.registration", "en")
	if err != nil {
		t.Fatalf("claims by vct: %v", err)
	}
	if got := attributeValue(viaVCT.Attributes, "company_name"); got != "Demo B.V." {
		t.Fatalf("claims by vct[company_name] = %v, want the demo value", got)
	}

	if err := eng.Delete(ctx, org, ref); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got := countInstances(t, pool, org); got != 0 {
		t.Fatalf("expected 0 instances after delete, got %d", got)
	}
	// The batch carries the decoded SD-JWT payload; deleting the instance alone
	// would orphan it, so assert erasure cascaded to the batch too.
	if got := countBatches(t, pool, org); got != 0 {
		t.Fatalf("expected 0 batches after delete, got %d", got)
	}
	// Claims for a now-absent ref and vct yields an empty attribute set, not an error.
	if got, err := eng.Claims(ctx, org, ref, "nl.kvk.registration", "en"); err != nil {
		t.Fatalf("claims after delete: %v", err)
	} else if len(got.Attributes) != 0 {
		t.Fatalf("claims after delete = %v, want empty", got.Attributes)
	}

	// Deleting an absent / non-uuid ref is a no-op.
	if err := eng.Delete(ctx, org, ref); err != nil {
		t.Fatalf("delete absent ref: %v", err)
	}
	if err := eng.Delete(ctx, org, "not-a-uuid"); err != nil {
		t.Fatalf("delete non-uuid ref: %v", err)
	}
}

// TestEnginePerOrgSchemaIsolation proves each org's credentials live in their own
// Postgres schema: a delete in one org never touches another's.
func TestEnginePerOrgSchemaIsolation(t *testing.T) {
	eng, pool := newTestEngine(t)
	ctx := context.Background()
	orgA, orgB := uuid.New(), uuid.New()

	refA, err := eng.Store(ctx, orgA, sampleCredential("vct.a", "hash-a"))
	if err != nil {
		t.Fatalf("store A: %v", err)
	}
	if _, err := eng.Store(ctx, orgB, sampleCredential("vct.b", "hash-b")); err != nil {
		t.Fatalf("store B: %v", err)
	}

	if got := countInstances(t, pool, orgA); got != 1 {
		t.Fatalf("org A: expected 1, got %d", got)
	}
	if got := countInstances(t, pool, orgB); got != 1 {
		t.Fatalf("org B: expected 1, got %d", got)
	}

	if err := eng.Delete(ctx, orgA, refA); err != nil {
		t.Fatalf("delete A: %v", err)
	}
	if got := countInstances(t, pool, orgA); got != 0 {
		t.Fatalf("org A after delete: expected 0, got %d", got)
	}
	if got := countInstances(t, pool, orgB); got != 1 {
		t.Fatalf("org B must be untouched by A's delete: expected 1, got %d", got)
	}
}
