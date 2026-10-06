//go:build integration

package seed

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// Each proofing org seeds its flows and customers once, however often the
// seed runs, and only its own.
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
	admin, err := ensureUser(ctx, user.NewStore(pool), "admin@example.org", "Sam", "Admin", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	for range 2 {
		if err := seedProofing(ctx, pool, orgsBySlug, admin.ID); err != nil {
			t.Fatalf("seedProofing: %v", err)
		}
	}

	data := proofing.NewDataRequestStore(pool, audit.NopRecorder{}, nil)
	customerStore := proofing.NewCustomerStore(pool, audit.NopRecorder{})
	for _, o := range demoProofingOrgs {
		orgID := orgsBySlug[o.slug].ID
		flows, err := flow.NewPostgresStore(pool).List(ctx, orgID.String())
		if err != nil || len(flows) != len(o.allFlows()) {
			t.Fatalf("%s flows = %d, %v; want %d", o.slug, len(flows), err, len(o.allFlows()))
		}
		ids := map[string]string{}
		for _, f := range flows {
			ids[f.Name] = f.ID
		}
		for _, f := range o.allFlows() {
			want := f.kind
			if want == "" {
				want = proofing.FlowIdentity
			}
			if kind, err := data.FlowKind(ctx, orgID, ids[f.def.Name]); err != nil || kind != want {
				t.Errorf("%s %s kind = %q, %v; want %q", o.slug, f.def.Name, kind, err, want)
			}
		}

		customers, err := customerStore.List(ctx, orgID)
		if err != nil || len(customers) != len(o.customers) {
			t.Fatalf("%s customers = %d, %v; want %d", o.slug, len(customers), err, len(o.customers))
		}
		for _, c := range customers {
			// Its own flows plus the data request flows, its first flow the default.
			var want demoProofingCustomer
			for _, d := range o.customers {
				if d.name == c.Name {
					want = d
				}
			}
			if len(c.Flows.FlowIDs) != len(want.flowNames()) || c.Flows.DefaultFlowID != ids[want.flows[0]] {
				t.Errorf("%s flows = %+v; want %d flows, default %s", c.Name, c.Flows, len(want.flowNames()), want.flows[0])
			}
			if c.Settings.DataRetentionDays != want.retentionDays {
				t.Errorf("%s retention = %d; want %d", c.Name, c.Settings.DataRetentionDays, want.retentionDays)
			}
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
}

// The staging proofing demo seeds the demo proofing orgs with their flows and
// customers and the Yivi team as their admins, once however often it runs.
func TestEnsureProofingDemoIsIdempotent(t *testing.T) {
	pool, dsn := testdb.Fresh(t)
	ctx := context.Background()
	for range 2 {
		if err := EnsureProofingDemo(ctx, dsn, "qerds.localhost"); err != nil {
			t.Fatalf("EnsureProofingDemo: %v", err)
		}
	}

	customerStore := proofing.NewCustomerStore(pool, audit.NopRecorder{})
	for _, o := range demoProofingOrgs {
		var orgID uuid.UUID
		if err := pool.QueryRow(ctx, "SELECT id FROM organizations WHERE slug = $1", o.slug).Scan(&orgID); err != nil {
			t.Fatalf("org %s: %v", o.slug, err)
		}
		var admins int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE organization_id = $1 AND role = 'admin'", orgID).Scan(&admins); err != nil || admins != len(yiviTeam) {
			t.Errorf("%s admins = %d, %v; want %d", o.slug, admins, err, len(yiviTeam))
		}
		customers, err := customerStore.List(ctx, orgID)
		if err != nil || len(customers) != len(o.customers) {
			t.Errorf("%s customers = %d, %v; want %d", o.slug, len(customers), err, len(o.customers))
		}
	}
}
