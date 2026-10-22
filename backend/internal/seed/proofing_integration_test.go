//go:build integration

package seed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// defaultOrgRetentionDays is the org's fixed data retention
// (proofing.defaultDataRetentionDays).
const defaultOrgRetentionDays = 7

// Each proofing org seeds its flows, with its retention, and its default flow
// once, however often the seed runs, and only its own. The org's own
// retention stays the fixed default.
func TestSeedProofingIsIdempotent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	orgsBySlug := map[string]organization.Organization{}
	for _, o := range demoProofingOrgs {
		var org organization.Organization
		if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
			VALUES ($1, $1, $1, $1, $1 || '@qerds.localhost') RETURNING id`, o.slug).Scan(&org.ID); err != nil {
			t.Fatalf("create org %s: %v", o.slug, err)
		}
		orgsBySlug[o.slug] = org
	}
	for range 2 {
		if err := seedProofing(ctx, pool, orgsBySlug); err != nil {
			t.Fatalf("seedProofing: %v", err)
		}
	}

	data := proofing.NewDataRequestStore(pool, audit.NopRecorder{}, nil)
	selections := proofing.NewSettingsStore(pool, audit.NopRecorder{})
	settings := proofing.NewOrgSettingsStore(pool, audit.NopRecorder{})
	for _, o := range demoProofingOrgs {
		orgID := orgsBySlug[o.slug].ID
		flows, err := flow.NewPostgresStore(pool).List(ctx, orgID.String())
		if err != nil || len(flows) != len(o.flows) {
			t.Fatalf("%s flows = %d, %v; want %d", o.slug, len(flows), err, len(o.flows))
		}
		ids := map[string]string{}
		for _, f := range flows {
			ids[f.Name] = f.ID
		}
		// See my data and Delete my data are the wallet's to create, not the
		// seed's: every seeded flow is an identity check.
		for _, f := range o.flows {
			if kind, err := data.FlowKind(ctx, orgID, ids[f.def.Name]); err != nil || kind != proofing.FlowIdentity {
				t.Errorf("%s %s kind = %q, %v; want identity", o.slug, f.def.Name, kind, err)
			}
		}

		sel, err := selections.FlowSelection(ctx, orgID)
		if err != nil || sel.DefaultFlowID != ids[o.defaultFlow] {
			t.Errorf("%s default flow = %q, %v; want %s", o.slug, sel.DefaultFlowID, err, o.defaultFlow)
		}
		for _, f := range flows {
			if want := time.Duration(o.retentionDays) * day; f.RetentionOverride != want {
				t.Errorf("%s %s retention = %v, want %v", o.slug, f.Name, f.RetentionOverride, want)
			}
		}
		got, err := settings.Get(ctx, orgID)
		if err != nil || got.Session.DataRetentionDays != defaultOrgRetentionDays {
			t.Errorf("%s org retention = %d, %v; want the fixed %d", o.slug, got.Session.DataRetentionDays, err, defaultOrgRetentionDays)
		}
	}

	radboudID := orgsBySlug[radboudSlug].ID
	radboudFlows, err := flow.NewPostgresStore(pool).List(ctx, radboudID.String())
	if err != nil {
		t.Fatalf("radboud flows: %v", err)
	}
	for _, f := range radboudFlows {
		if f.Name != flowRadboudEnrol {
			continue
		}
		if mode, err := proofing.NewFlowDiplomaStore(pool, audit.NopRecorder{}).Get(ctx, radboudID, f.ID); err != nil || mode != proofing.DiplomasRequired {
			t.Errorf("Radboud enrol diplomas = %q, %v; want required", mode, err)
		}
	}

	odixusID := orgsBySlug[odixusSlug].ID
	odixusFlows, err := flow.NewPostgresStore(pool).List(ctx, odixusID.String())
	if err != nil {
		t.Fatalf("odixus flows: %v", err)
	}
	for _, f := range odixusFlows {
		if f.Name != flowOdixusOnboarding {
			continue
		}
		if mode, err := proofing.NewFlowAddressStore(pool, audit.NopRecorder{}).Get(ctx, odixusID, f.ID); err != nil || mode != proofing.AddressRequired {
			t.Errorf("odixus onboarding address = %q, %v; want required", mode, err)
		}
	}
}

// The staging proofing demo seeds the demo proofing orgs with their flows and
// the Yivi team as their admins, once however often it runs.
func TestEnsureProofingDemoIsIdempotent(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()
	for range 2 {
		if err := EnsureProofingDemo(ctx, dsn, "qerds.localhost"); err != nil {
			t.Fatalf("EnsureProofingDemo: %v", err)
		}
	}

	for _, o := range demoProofingOrgs {
		var orgID uuid.UUID
		if err := pool.QueryRow(ctx, "SELECT id FROM organizations WHERE slug = $1", o.slug).Scan(&orgID); err != nil {
			t.Fatalf("org %s: %v", o.slug, err)
		}
		var admins int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE organization_id = $1 AND role = 'admin'", orgID).Scan(&admins); err != nil || admins != len(yiviTeam) {
			t.Errorf("%s admins = %d, %v; want %d", o.slug, admins, err, len(yiviTeam))
		}
		flows, err := flow.NewPostgresStore(pool).List(ctx, orgID.String())
		if err != nil || len(flows) != len(o.flows) {
			t.Errorf("%s flows = %d, %v; want %d", o.slug, len(flows), err, len(o.flows))
		}
	}
}

// The staging proofing demo refuses a demo slug taken by an org it did not
// create, and leaves that org untouched.
func TestEnsureProofingDemoRefusesForeignOrg(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()
	var orgID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('a.s.r.', $1, '12345678', 'NL.KVK.12345678', 'asr@qerds.example') RETURNING id`, asrSlug).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}

	if err := EnsureProofingDemo(ctx, dsn, "qerds.localhost"); !errors.Is(err, ErrNotSeedOwned) {
		t.Fatalf("EnsureProofingDemo = %v, want ErrNotSeedOwned", err)
	}

	var members, addresses int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE organization_id = $1", orgID).Scan(&members); err != nil || members != 0 {
		t.Errorf("foreign org members = %d, %v; want 0", members, err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM qerds_addresses WHERE organization_id = $1", orgID).Scan(&addresses); err != nil || addresses != 0 {
		t.Errorf("foreign org QERDS addresses = %d, %v; want 0", addresses, err)
	}
	settings, err := proofing.NewOrgSettingsStore(pool, audit.NopRecorder{}).Get(ctx, orgID)
	if err != nil || !settings.UpdatedAt.IsZero() {
		t.Errorf("foreign org proofing settings = %+v, %v; want none saved", settings, err)
	}
}

// A demo org an older seed named without "(Demo)" takes the current name: the
// seed owns it (requireSeedOwned), so the rename is its to make.
func TestEnsureProofingDemoRenamesItsOwnOrg(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()
	if err := EnsureProofingDemo(ctx, dsn, "qerds.localhost"); err != nil {
		t.Fatalf("EnsureProofingDemo: %v", err)
	}
	want := ""
	for _, o := range demoOrganizations {
		if o.slug == radboudSlug {
			want = o.name
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE organizations SET name = 'Radboud Universiteit' WHERE slug = $1`, radboudSlug); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProofingDemo(ctx, dsn, "qerds.localhost"); err != nil {
		t.Fatalf("EnsureProofingDemo again: %v", err)
	}
	var name string
	if err := pool.QueryRow(ctx, "SELECT name FROM organizations WHERE slug = $1", radboudSlug).Scan(&name); err != nil || name != want {
		t.Errorf("name = %q, %v; want %q", name, err, want)
	}
}
