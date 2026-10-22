//go:build integration

package migrate_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// orgDefaultsVersion is 20261021114000_copy_org_defaults_into_identity_proofing_flow_versions.sql,
// the first of the migrations that fix an org's QR lifetime and data
// retention; orgDefaultsMigrations is how many there are.
const (
	orgDefaultsVersion    int64 = 20261021114000
	orgDefaultsMigrations       = 3
)

const day = 24 * time.Hour

// TestUpCopiesOrgDefaultsIntoFlows fills the schema before an org's QR
// lifetime and data retention were fixed with three orgs: one with its own
// values, one on the fixed defaults and one that saved no settings. After the
// migrations each flow and each sent external request runs as it did, with the
// org's values copied into those that set none of their own, and the orgs are
// on the fixed defaults. The down migrations run, and up again changes
// nothing.
func TestUpCopiesOrgDefaultsIntoFlows(t *testing.T) {
	ctx := context.Background()
	dsn := testdb.Bare(t)
	if err := migrate.UpTo(ctx, dsn, orgDefaultsVersion-1); err != nil {
		t.Fatalf("migrate up to before the org defaults: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	scan := func(dst any, sql string, args ...any) {
		t.Helper()
		if err := conn.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	org := func(slug string) string {
		t.Helper()
		var id string
		scan(&id, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, $1, $1 || '@qerds.test') RETURNING id`, slug)
		return id
	}
	version := func(orgID, flowID string, v int, active bool, fd flow.FlowDefinition) {
		t.Helper()
		fd.ID, fd.TenantID, fd.Version, fd.Name, fd.Active = flowID, orgID, v, flowID, active
		def, err := json.Marshal(fd)
		if err != nil {
			t.Fatalf("marshal %s v%d: %v", flowID, v, err)
		}
		exec(`INSERT INTO identity_proofing_flow_versions (organization_id, flow_id, version, definition, active)
			VALUES ($1, $2, $3, $4, $5)`, orgID, flowID, v, def, active)
	}
	request := func(orgID, kind string, override *int, purged bool) string {
		t.Helper()
		var id string
		scan(&id, `INSERT INTO identity_proofing_requests
				(organization_id, subject_kind, subject_user_id, subject_name, subject_email, flow_id, flow_name,
				 link_expires_at, retention_override_seconds, purged_at)
			VALUES ($1, $2, CASE WHEN $2 = 'member' THEN (SELECT id FROM users LIMIT 1) END,
				CASE WHEN $4 THEN '' ELSE 'Anna' END, CASE WHEN $4 THEN '' ELSE 'anna@example.org' END,
				'f', 'F', now() + interval '1 hour', $3, CASE WHEN $4 THEN now() END) RETURNING id`,
			orgID, kind, override, purged)
		return id
	}

	exec(`INSERT INTO users (email, given_names, last_name) VALUES ('m@example.org', 'Mia', 'Member')`)
	own := org("own")
	exec(`INSERT INTO identity_proofing_settings (organization_id, session_ttl_seconds, data_retention_days)
		VALUES ($1, 300, 90)`, own)
	fixed := org("fixed")
	exec(`INSERT INTO identity_proofing_settings (organization_id, session_ttl_seconds, data_retention_days)
		VALUES ($1, 600, 7)`, fixed)
	unsaved := org("unsaved")

	// own's plain flow had a newer version activated back to its second one.
	version(own, "plain", 1, false, flow.FlowDefinition{})
	version(own, "plain", 2, true, flow.FlowDefinition{RequiredChecks: []flow.Check{flow.CheckNFCPassiveAuth}})
	version(own, "plain", 3, false, flow.FlowDefinition{})
	version(own, "set", 1, true, flow.FlowDefinition{RetentionOverride: day, SessionTTL: 2 * time.Minute})
	version(own, "half", 1, true, flow.FlowDefinition{RetentionOverride: day})
	version(fixed, "plain", 1, true, flow.FlowDefinition{})
	version(unsaved, "plain", 1, true, flow.FlowDefinition{})

	hour := int(time.Hour / time.Second)
	ownFollows := request(own, "external", nil, false)
	ownSets := request(own, "external", &hour, false)
	ownMember := request(own, "member", nil, false)
	ownPurged := request(own, "external", nil, true)
	fixedFollows := request(fixed, "external", nil, false)
	unsavedFollows := request(unsaved, "external", nil, false)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	store := flow.NewPostgresStore(conn)
	wantFlow := func(orgID, flowID string, wantVersion int, retention, ttl time.Duration) {
		t.Helper()
		fd, err := store.Get(ctx, orgID, flowID, 0)
		if err != nil {
			t.Fatalf("read %s's active version: %v", flowID, err)
		}
		if fd.Version != wantVersion || !fd.Active || fd.RetentionOverride != retention || fd.SessionTTL != ttl {
			t.Errorf("%s active = v%d (active %v), retention %v, lifetime %v; want v%d, %v, %v",
				flowID, fd.Version, fd.Active, fd.RetentionOverride, fd.SessionTTL, wantVersion, retention, ttl)
		}
	}
	wantFlow(own, "plain", 4, 90*day, 5*time.Minute)
	wantFlow(own, "set", 1, day, 2*time.Minute)
	wantFlow(own, "half", 2, day, 5*time.Minute)
	wantFlow(fixed, "plain", 1, 0, 0)
	wantFlow(unsaved, "plain", 2, 30*day, 0)

	copied, err := store.Get(ctx, own, "plain", 4)
	if err != nil || len(copied.RequiredChecks) != 1 || copied.CreatedAt.IsZero() {
		t.Errorf("copied version = %+v, %v; want the active one's checks and its own creation time", copied, err)
	}
	assertCount(t, ctx, conn, "active versions of own's plain flow", 1,
		`SELECT count(*) FROM identity_proofing_flow_versions WHERE organization_id = $1 AND flow_id = 'plain' AND active`, own)

	wantRetention := func(what, id string, want *int) {
		t.Helper()
		var got *int
		scan(&got, `SELECT retention_override_seconds FROM identity_proofing_requests WHERE id = $1`, id)
		if (got == nil) != (want == nil) || (got != nil && *got != *want) {
			t.Errorf("%s retention = %v, want %v", what, got, want)
		}
	}
	ninety, thirty := int(90*day/time.Second), int(30*day/time.Second)
	wantRetention("a request following its org's", ownFollows, &ninety)
	wantRetention("a request with its own", ownSets, &hour)
	wantRetention("a member's request", ownMember, nil)
	wantRetention("a purged request", ownPurged, nil)
	wantRetention("a request on the fixed defaults", fixedFollows, nil)
	wantRetention("a request of an org without settings", unsavedFollows, &thirty)

	assertCount(t, ctx, conn, "orgs off the fixed defaults", 0,
		`SELECT count(*) FROM identity_proofing_settings WHERE session_ttl_seconds <> 600 OR data_retention_days <> 7`)
	assertCount(t, ctx, conn, "settings rows made", 2, `SELECT count(*) FROM identity_proofing_settings`)
	exec(`INSERT INTO identity_proofing_settings (organization_id) VALUES ($1)`, unsaved)
	assertCount(t, ctx, conn, "a new org on the fixed retention", 1,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id = $1 AND data_retention_days = 7`, unsaved)
	exec(`DELETE FROM identity_proofing_settings WHERE organization_id = $1`, unsaved)

	for range orgDefaultsMigrations {
		if err := migrate.Down(ctx, dsn); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
	}
	if v, err := migrate.Version(ctx, dsn); err != nil || v >= orgDefaultsVersion {
		t.Fatalf("version after down = %d, %v; want before %d", v, err, orgDefaultsVersion)
	}
	exec(`INSERT INTO identity_proofing_settings (organization_id) VALUES ($1)`, unsaved)
	assertCount(t, ctx, conn, "a new org on the old default retention", 1,
		`SELECT count(*) FROM identity_proofing_settings WHERE organization_id = $1 AND data_retention_days = 30`, unsaved)
	exec(`DELETE FROM identity_proofing_settings WHERE organization_id = $1`, unsaved)
	wantFlow(own, "plain", 4, 90*day, 5*time.Minute)

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	wantFlow(own, "plain", 4, 90*day, 5*time.Minute)
	wantFlow(unsaved, "plain", 2, 30*day, 0)
	wantRetention("a request following its org's, after up again", ownFollows, &ninety)
}
