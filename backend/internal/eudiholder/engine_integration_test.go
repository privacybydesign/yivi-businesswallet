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

// TestEngineClaimsWithSeveralCredentialsOfOneVCT is the irmago-backed half of the
// "held-credential detail shows the wrong credential" fix: with two credentials of
// one vct, each ref must resolve to its own attributes, and a held row whose ref no
// longer resolves must yield nothing rather than an arbitrary sibling's data.
func TestEngineClaimsWithSeveralCredentialsOfOneVCT(t *testing.T) {
	eng, _ := newTestEngine(t)
	ctx := context.Background()
	org := uuid.New()

	const vct = "nl.kvk.registration"
	alpha := sampleCredential(vct, "hash-alpha")
	alpha.ProcessedPayload = []byte(`{"vct":"nl.kvk.registration","company_name":"Alpha B.V."}`)
	beta := sampleCredential(vct, "hash-beta")
	beta.ProcessedPayload = []byte(`{"vct":"nl.kvk.registration","company_name":"Beta B.V."}`)

	refA, err := eng.Store(ctx, org, alpha)
	if err != nil {
		t.Fatalf("store alpha: %v", err)
	}
	refB, err := eng.Store(ctx, org, beta)
	if err != nil {
		t.Fatalf("store beta: %v", err)
	}

	for _, tc := range []struct{ ref, want string }{{refA, "Alpha B.V."}, {refB, "Beta B.V."}} {
		claims, err := eng.Claims(ctx, org, tc.ref, vct, "en")
		if err != nil {
			t.Fatalf("claims %s: %v", tc.ref, err)
		}
		if got := attributeValue(claims.Attributes, "company_name"); got != tc.want {
			t.Errorf("claims[company_name] for ref %s = %v, want %q", tc.ref, got, tc.want)
		}
	}

	// No usable ref (empty, a non-uuid legacy/seed ref, or one that no longer
	// resolves) and a vct that matches both: the fallback must decline.
	for _, ref := range []string{"", "demo-kvk-registration", uuid.NewString()} {
		claims, err := eng.Claims(ctx, org, ref, vct, "en")
		if err != nil {
			t.Fatalf("claims ref %q: %v", ref, err)
		}
		if len(claims.Attributes) != 0 {
			t.Errorf("claims for ref %q = %v, want empty: the vct matches two credentials",
				ref, claims.Attributes)
		}
	}

	// One credential of the type left: the vct is a discriminator again, so the
	// fallback recovers it (what a ref-less legacy row relies on).
	if err := eng.Delete(ctx, org, refB); err != nil {
		t.Fatalf("delete beta: %v", err)
	}
	claims, err := eng.Claims(ctx, org, "", vct, "en")
	if err != nil {
		t.Fatalf("claims by vct: %v", err)
	}
	if got := attributeValue(claims.Attributes, "company_name"); got != "Alpha B.V." {
		t.Errorf("claims by vct[company_name] = %v, want the only remaining credential", got)
	}
}

// TestEngineValidities covers the held view's status source against the real
// engine: each credential's expiry and last known status-list bit, keyed by the
// instance ref the held index points at. The bit is written the way irmago's status
// refresh writes it, since nothing in this process fetches a status list.
func TestEngineValidities(t *testing.T) {
	eng, pool := newTestEngine(t)
	ctx := context.Background()
	org := uuid.New()

	expires := time.Unix(1_900_000_000, 0).UTC()
	expiring := sampleCredential("nl.kvk.registration", "hash-expiring")
	expiring.ExpiresAt = &expires
	expiringRef, err := eng.Store(ctx, org, expiring)
	if err != nil {
		t.Fatalf("store expiring: %v", err)
	}
	perpetualRef, err := eng.Store(ctx, org, sampleCredential("eaa.perpetual", "hash-perpetual"))
	if err != nil {
		t.Fatalf("store perpetual: %v", err)
	}

	validities, err := eng.Validities(ctx, org)
	if err != nil {
		t.Fatalf("validities: %v", err)
	}
	if len(validities) != 2 {
		t.Fatalf("validities has %d entries, want one per held credential", len(validities))
	}
	if got := validities[expiringRef].ExpiresAt; got == nil || !got.Equal(expires) {
		t.Errorf("validities[expiring].ExpiresAt = %v, want %v", got, expires)
	}
	if got := validities[perpetualRef].ExpiresAt; got != nil {
		t.Errorf("validities[perpetual].ExpiresAt = %v, want nil: it does not expire", got)
	}
	for ref, validity := range validities {
		if validity.Revoked {
			t.Errorf("validities[%s].Revoked = true, want false: no status bit was written yet", ref)
		}
	}

	// statuslist.StatusInvalid (2) is what a status refresh writes back for a revoked
	// credential; StatusValid (1) and the never-checked default (0) are not revoked.
	setInstanceStatus(t, pool, org, perpetualRef, 2)
	setInstanceStatus(t, pool, org, expiringRef, 1)
	validities, err = eng.Validities(ctx, org)
	if err != nil {
		t.Fatalf("validities after status writeback: %v", err)
	}
	if !validities[perpetualRef].Revoked {
		t.Error("validities[perpetual].Revoked = false, want true: its status bit reads invalid")
	}
	if validities[expiringRef].Revoked {
		t.Error("validities[expiring].Revoked = true, want false: its status bit reads valid")
	}

	// A deleted credential drops out of the map, so its held row gets no validity.
	if err := eng.Delete(ctx, org, perpetualRef); err != nil {
		t.Fatalf("delete perpetual: %v", err)
	}
	validities, err = eng.Validities(ctx, org)
	if err != nil {
		t.Fatalf("validities after delete: %v", err)
	}
	if _, ok := validities[perpetualRef]; ok {
		t.Error("validities still carries a deleted credential")
	}
}

// setInstanceStatus writes a credential instance's last known status-list bit in an
// org's isolated schema, standing in for irmago's status-refresh writeback.
func setInstanceStatus(t *testing.T, pool *pgxpool.Pool, orgID uuid.UUID, ref string, status uint8) {
	t.Helper()
	schema := "holder_" + hex.EncodeToString(orgID[:])
	//nolint:gosec // schema is a fixed identifier derived from the org id, not user input.
	query := fmt.Sprintf(
		`UPDATE %q.issued_credential_instances SET last_known_status = $1 WHERE id = $2`, schema)
	tag, err := pool.Exec(context.Background(), query, status, ref)
	if err != nil {
		t.Fatalf("set status for %s: %v", ref, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set status for %s affected %d rows, want 1", ref, tag.RowsAffected())
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
