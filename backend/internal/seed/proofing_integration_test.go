//go:build integration

package seed

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// The use cases seed their flows and customers once, however often the seed
// runs.
func TestSeedProofingIsIdempotent(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx := context.Background()
	var orgID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', 'acme', 'kvk-acme', 'NL.KVK.acme', 'acme@qerds.localhost') RETURNING id`).Scan(&orgID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, given_names, last_name)
		VALUES ('admin@example.org', 'Sam', 'Admin') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	for range 2 {
		if err := seedProofing(ctx, pool, orgID, userID); err != nil {
			t.Fatalf("seedProofing: %v", err)
		}
	}

	flows, err := flow.NewPostgresStore(pool).List(ctx, orgID.String())
	if err != nil || len(flows) != len(demoProofingFlows) {
		t.Fatalf("flows = %d, %v; want %d", len(flows), err, len(demoProofingFlows))
	}
	ids := map[string]string{}
	for _, f := range flows {
		ids[f.Name] = f.ID
	}
	data := proofing.NewDataRequestStore(pool, audit.NopRecorder{}, nil)
	for name, want := range map[string]proofing.FlowKind{
		flowDataAccess: proofing.FlowDataAccess, flowDataErasure: proofing.FlowDataErasure, flowRadboudEnrol: proofing.FlowIdentity,
	} {
		if kind, err := data.FlowKind(ctx, orgID, ids[name]); err != nil || kind != want {
			t.Errorf("%s kind = %q, %v; want %q", name, kind, err, want)
		}
	}
	if mode, err := proofing.NewFlowDiplomaStore(pool, audit.NopRecorder{}).Get(ctx, orgID, ids[flowRadboudEnrol]); err != nil || mode != proofing.DiplomasRequired {
		t.Errorf("Radboud enrol diplomas = %q, %v; want required", mode, err)
	}

	customers, err := proofing.NewCustomerStore(pool, audit.NopRecorder{}).List(ctx, orgID)
	if err != nil || len(customers) != len(demoProofingCustomers) {
		t.Fatalf("customers = %d, %v; want %d", len(customers), err, len(demoProofingCustomers))
	}
	for _, c := range customers {
		// Its own flows plus the two data request flows, its first flow the default.
		var want demoProofingCustomer
		for _, d := range demoProofingCustomers {
			if d.name == c.Name {
				want = d
			}
		}
		if len(c.Flows.FlowIDs) != len(want.flows)+len(dataRequestFlows) || c.Flows.DefaultFlowID != ids[want.flows[0]] {
			t.Errorf("%s flows = %+v; want %d flows, default %s", c.Name, c.Flows, len(want.flows)+len(dataRequestFlows), want.flows[0])
		}
		if c.Settings.DataRetentionDays != want.retentionDays {
			t.Errorf("%s retention = %d; want %d", c.Name, c.Settings.DataRetentionDays, want.retentionDays)
		}
	}
}
