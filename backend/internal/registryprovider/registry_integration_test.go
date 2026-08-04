//go:build integration

package registryprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/registryprovider"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// lastAuditEvent reads the most recent audit event recorded against the register
// org, returning its action and the {after} metadata envelope.
func lastAuditEvent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (string, map[string]any) {
	t.Helper()
	var (
		action string
		raw    []byte
	)
	if err := pool.QueryRow(ctx,
		`SELECT action, metadata FROM audit_events WHERE organization_id = $1 ORDER BY occurred_at DESC, id DESC LIMIT 1`,
		orgID).Scan(&action, &raw); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	var env struct {
		After map[string]any `json:"after"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	return action, env.After
}

// TestConsultAuditsDecisionsOnRegisterOrg is the end-to-end regression for the
// audit criterion: every consult — validated, non-matching identity, unknown KVK
// number — records exactly one decision on the seeded KVK register org's log,
// carrying the consulted KVK number, identification data and outcome/reason.
func TestConsultAuditsDecisionsOnRegisterOrg(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()

	var orgID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		registryprovider.RegisterLegalName, registryprovider.RegisterSlug,
		registryprovider.RegisterKVKNumber, registryprovider.RegisterEUID, "kvk@qerds.localhost").Scan(&orgID); err != nil {
		t.Fatalf("seed register org: %v", err)
	}

	reg := registryprovider.NewSeededRegistry(pool, audit.NewDBRecorder())

	// Validated consult.
	att, err := reg.Consult(ctx, registryprovider.ConsultRequest{
		KVKNumber: "90000010", GivenNames: "Johannes Hendrik", FamilyName: "Janssen", DateOfBirth: "1979-05-14",
	})
	if err != nil || !att.RequesterIsRepresentative {
		t.Fatalf("validated consult: att=%+v err=%v", att, err)
	}
	action, meta := lastAuditEvent(t, ctx, pool, orgID)
	if action != audit.KVKRegistrationValidated {
		t.Fatalf("action = %q, want %q", action, audit.KVKRegistrationValidated)
	}
	if meta["kvkNumber"] != "90000010" || meta["familyName"] != "Janssen" || meta["outcome"] != "validated" {
		t.Fatalf("audited metadata = %+v", meta)
	}

	// Non-matching identity.
	if _, err := reg.Consult(ctx, registryprovider.ConsultRequest{
		KVKNumber: "90000010", GivenNames: "Mallory", FamilyName: "Impostor",
	}); err != nil {
		t.Fatalf("non-matching consult: %v", err)
	}
	action, meta = lastAuditEvent(t, ctx, pool, orgID)
	if action != audit.KVKRegistrationNotValidated || meta["reason"] != "not_a_representative" {
		t.Fatalf("action = %q meta = %+v, want not_validated/not_a_representative", action, meta)
	}

	// Unknown KVK number.
	if _, err := reg.Consult(ctx, registryprovider.ConsultRequest{
		KVKNumber: "00000001", GivenNames: "Johannes Hendrik", FamilyName: "Janssen",
	}); !errors.Is(err, registryprovider.ErrUnknownKVK) {
		t.Fatalf("unknown consult err = %v, want ErrUnknownKVK", err)
	}
	action, meta = lastAuditEvent(t, ctx, pool, orgID)
	if action != audit.KVKRegistrationNotValidated || meta["reason"] != "unknown_kvk" {
		t.Fatalf("action = %q meta = %+v, want not_validated/unknown_kvk", action, meta)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE organization_id = $1`, orgID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 3 {
		t.Fatalf("audit events = %d, want 3 (one per consult)", count)
	}
}
