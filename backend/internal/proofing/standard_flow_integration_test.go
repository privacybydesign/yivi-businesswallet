//go:build integration

package proofing

import (
	"context"
	"errors"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine"
	proofingflow "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/regula"
	proofingsession "github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/session"
)

// errNoRegula is every call to idleRegula.
var errNoRegula = errors.New("regula: not called in this test")

// idleRegula is a configured Regula Face API that no session reaches: what
// the engine needs to accept a flow with a Regula face check.
type idleRegula struct{}

func (idleRegula) GetLiveness(context.Context, string) (regula.LivenessTransaction, error) {
	return regula.LivenessTransaction{}, errNoRegula
}

func (idleRegula) Match(context.Context, string, string) (regula.MatchResult, error) {
	return regula.MatchResult{}, errNoRegula
}

func (idleRegula) MatchImages(context.Context, string, string) (regula.ImageMatch, error) {
	return regula.ImageMatch{}, errNoRegula
}

func (idleRegula) DeleteLiveness(context.Context, string) error { return errNoRegula }

// The engine accepts the wallet's definition of See my data and Delete my
// data, and lists them back as meeting it, so a second read creates nothing.
func TestStandardFlowsOnTheEngine(t *testing.T) {
	f := newPurgeFixture(t)
	ctx := context.Background()
	engineConfig := proofingengine.DefaultConfig()
	engineConfig.PublicBaseURL = "https://wallet.example.test"
	engineConfig.Regula = idleRegula{}
	sessions := proofingsession.NewPostgresStore(f.pool, newTestCipher(t))
	engine := proofingengine.New(engineConfig, sessions, proofingflow.NewPostgresStore(f.pool), engineOrgNames{}, nil)
	recorder := audit.NewDBRecorder()
	svc := NewService(Stores{
		Settings: NewSettingsStore(f.pool, recorder), Requests: f.requests, OrgSettings: f.settings,
		DataRequests: NewDataRequestStore(f.pool, recorder, newTestCipher(t)),
	}, engine, nil, nil)
	org := Org{ID: f.orgID, Name: "Acme"}

	first, err := svc.DataRequestFlows(ctx, org)
	if err != nil {
		t.Fatalf("DataRequestFlows: %v", err)
	}
	for i, kind := range standardKinds {
		if first[i].Kind != kind || first[i].RequiredAssuranceLevel != eidasSubstantial || !meetsStandard(first[i].Flow) {
			t.Errorf("%s = %+v; want the engine's flow on the wallet's definition", kind, first[i])
		}
	}
	again, err := svc.DataRequestFlows(ctx, org)
	if err != nil {
		t.Fatalf("DataRequestFlows again: %v", err)
	}
	if again[0].ID != first[0].ID || again[0].Version != 1 || again[1].ID != first[1].ID || again[1].Version != 1 {
		t.Errorf("again = %s v%d, %s v%d; want the same flows, still version 1", again[0].ID, again[0].Version, again[1].ID, again[1].Version)
	}
	if n := auditCount(t, f.pool, audit.IdentityProofingFlowCreated); n != len(standardKinds) {
		t.Errorf("flow_created audited %d times, want %d", n, len(standardKinds))
	}
}
