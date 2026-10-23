package proofing

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// newStandardFixture is a service over the in-memory engine stub with no flow
// yet, and a data request store with no standard flows.
func newStandardFixture() (*Service, *proofingprovider.Stub, *fakeDataRequests, *fakeSettings) {
	stub := proofingprovider.NewStub()
	data := &fakeDataRequests{kinds: map[string]FlowKind{}, standard: map[FlowKind]string{}}
	settings := &fakeSettings{}
	org := &fakeOrgSettings{settings: defaultSettings(testOrg.ID)}
	svc := NewService(Stores{Settings: settings, Requests: &fakeRequests{}, OrgSettings: org, APIKeys: fakeAPIKeys{}, DataRequests: data},
		stub, nil, nil)
	return svc, stub, data, settings
}

// The wallet's definition of a standard flow is one it holds the standard
// flows to, and one the wallet itself accepts as a flow.
func TestStandardFlowSpecMeetsStandard(t *testing.T) {
	for _, kind := range standardKinds {
		spec, err := normalizeFlow(standardFlowSpec(kind))
		if err != nil {
			t.Fatalf("%s: normalizeFlow: %v", kind, err)
		}
		f := proofingprovider.Flow{FlowSpec: spec}
		if !meetsStandard(f) || !flowCompletable(f) || spec.Name == "" {
			t.Errorf("%s spec %+v does not meet the standard", kind, spec)
		}
		if slices.ContainsFunc(spec.RequestedAttributes, func(a string) bool {
			return a != attributeDocument && a != attributeChipChecks && a != attributeBiometrics
		}) {
			t.Errorf("%s releases %v; want the document data and the checks only, no image", kind, spec.RequestedAttributes)
		}

		// The age check withholds the date of birth a data request finds the
		// person's sessions by: a version with it falls short of the standard.
		withAge := f
		withAge.RequestedAttributes = append(slices.Clone(spec.RequestedAttributes), attributeAgeOver)
		withAge.AgeOver = 18
		if meetsStandard(withAge) {
			t.Errorf("%s with the age check meets the standard; want not", kind)
		}
	}
	weak := proofingprovider.Flow{FlowSpec: proofingprovider.FlowSpec{Steps: []string{stepDocumentCapture, stepNFCRead}, RequestedAttributes: []string{attributeDocument}}}
	if meetsStandard(weak) {
		t.Error("a flow without face check or level meets the standard; want not")
	}
}

// Every org gets See my data and Delete my data the first time it needs them,
// once: a second read finds the same flows. Creating them is nobody's doing.
func TestStandardFlowsCreatedOnce(t *testing.T) {
	svc, stub, data, settings := newStandardFixture()
	ctx := audit.ContextWithActor(context.Background(), audit.Actor{UserID: uuid.New(), Label: "Sam"})
	flows, err := svc.DataRequestFlows(ctx, testOrg)
	if err != nil {
		t.Fatalf("DataRequestFlows: %v", err)
	}
	if len(flows) != len(standardKinds) {
		t.Fatalf("flows = %+v; want one per kind", flows)
	}
	for i, kind := range standardKinds {
		if flows[i].Kind != kind || flows[i].Name != standardFlowNames[kind] || !meetsStandard(flows[i].Flow) {
			t.Errorf("flow %d = %+v; want the standard %s flow", i, flows[i], kind)
		}
	}
	if len(data.claims) != len(standardKinds) || len(settings.flowEvents) != len(standardKinds) {
		t.Errorf("claims = %v, flow events = %v; want one each per kind", data.claims, settings.flowEvents)
	}

	again, err := svc.DataRequestFlows(ctx, testOrg)
	if err != nil {
		t.Fatalf("DataRequestFlows again: %v", err)
	}
	if again[0].ID != flows[0].ID || again[1].ID != flows[1].ID {
		t.Errorf("second read = %s, %s; want the same flows %s, %s", again[0].ID, again[1].ID, flows[0].ID, flows[1].ID)
	}
	if listed, _ := stub.ListFlows(ctx, orgTenant(testOrg.ID)); len(listed) != len(standardKinds) || len(data.claims) != len(standardKinds) {
		t.Errorf("engine flows = %d, claims = %d after a second read; want none created", len(listed), len(data.claims))
	}
}

// A standard flow (kept from before, as the oldest of its kind) whose
// definition falls short of the wallet's gets it back in a new version, under
// its own name; one the engine no longer lists is replaced.
func TestStandardFlowKeptToTheDefinition(t *testing.T) {
	svc, stub, data, settings := newStandardFixture()
	ctx := context.Background()
	tenant := orgTenant(testOrg.ID)
	weak, err := stub.CreateFlow(ctx, tenant, proofingprovider.FlowSpec{
		Name: "Mijn gegevens inzien", Steps: []string{stepDocumentCapture, stepNFCRead}, RequestedAttributes: []string{attributeDocument},
	})
	if err != nil {
		t.Fatalf("CreateFlow: %v", err)
	}
	data.kinds[weak.ID], data.standard[FlowDataAccess] = FlowDataAccess, weak.ID
	data.kinds["gone"], data.standard[FlowDataErasure] = FlowDataErasure, "gone"

	standard, err := svc.standardFlows(ctx, testOrg)
	if err != nil {
		t.Fatalf("standardFlows: %v", err)
	}
	access := standard[FlowDataAccess]
	if access.ID != weak.ID || access.Version != 2 || access.Name != weak.Name || !meetsStandard(access) {
		t.Errorf("See my data = %+v; want %s at version 2 on the wallet's definition, its name kept", access, weak.ID)
	}
	if erasure := standard[FlowDataErasure]; erasure.ID == "gone" || data.standard[FlowDataErasure] != erasure.ID {
		t.Errorf("Delete my data = %s, store %s; want a new flow in place of the unlisted one", erasure.ID, data.standard[FlowDataErasure])
	}
	want := []string{audit.IdentityProofingFlowVersionCreated, audit.IdentityProofingFlowCreated}
	if !slices.Equal(settings.flowEvents, want) {
		t.Errorf("flow events = %v; want %v", settings.flowEvents, want)
	}
}

// Two first sends at once each create a flow; the one claimed first is the
// standard flow, and the other is left a data request flow nothing runs on.
func TestStandardFlowLostClaim(t *testing.T) {
	svc, stub, data, _ := newStandardFixture()
	ctx := context.Background()
	var winner string
	data.beforeClaim = func(kind FlowKind) {
		if kind != FlowDataAccess || winner != "" {
			return
		}
		spec, err := normalizeFlow(standardFlowSpec(kind))
		if err != nil {
			t.Fatalf("normalizeFlow: %v", err)
		}
		other, err := stub.CreateFlow(ctx, orgTenant(testOrg.ID), spec)
		if err != nil {
			t.Fatalf("CreateFlow: %v", err)
		}
		winner = other.ID
		data.kinds[winner], data.standard[kind] = kind, winner
	}
	standard, err := svc.standardFlows(ctx, testOrg)
	if err != nil {
		t.Fatalf("standardFlows: %v", err)
	}
	if standard[FlowDataAccess].ID != winner {
		t.Errorf("See my data = %s; want the flow claimed first, %s", standard[FlowDataAccess].ID, winner)
	}
	lost := data.claims[0]
	if lost == winner || data.kinds[lost] != FlowDataAccess {
		t.Errorf("the flow created here = %s of kind %q; want a hidden data_access flow", lost, data.kinds[lost])
	}
}

// A data request is sent by its kind, on the org's standard flow of that kind;
// a request naming a flow of a data request kind runs on that kind's standard
// flow. Only an external person, from a sender who may use any flow.
func TestDataRequestSentByKind(t *testing.T) {
	ctx := context.Background()
	f, _ := newDataFixture(FlowDataErasure)
	sent, err := f.svc.CreateRequest(ctx, testOrg, sam, NewRequest{SubjectEmail: "anna@example.org", FlowKind: FlowDataAccess})
	if err != nil {
		t.Fatalf("CreateRequest by kind: %v", err)
	}
	if sent.Request.FlowID != standardTestFlow(FlowDataAccess).ID || sent.Request.FlowKind != FlowDataAccess {
		t.Errorf("sent on %s as %q; want the standard See my data flow", sent.Request.FlowID, sent.Request.FlowKind)
	}
	legacy := f.sendExternal(t, "anna@example.org", "")
	if legacy.Request.FlowID != standardTestFlow(FlowDataErasure).ID || legacy.Request.FlowKind != FlowDataErasure {
		t.Errorf("request naming an older erasure flow ran on %s as %q; want the standard Delete my data flow", legacy.Request.FlowID, legacy.Request.FlowKind)
	}

	refused := []struct {
		name string
		by   Requester
		in   NewRequest
		want error
	}{
		{"a kind that is not the named flow's", sam, NewRequest{SubjectEmail: "a@example.org", FlowKind: FlowDataAccess, FlowID: appFlow.ID}, ErrInvalidInput},
		{"another data request kind than the flow's", sam, NewRequest{SubjectEmail: "a@example.org", FlowKind: FlowDataAccess, FlowID: chipFlow.ID}, ErrInvalidInput},
		{"an unknown kind", sam, NewRequest{SubjectEmail: "a@example.org", FlowKind: "everything"}, ErrInvalidInput},
		{"to a member", sam, NewRequest{SubjectUserID: alex.UserID, FlowKind: FlowDataAccess}, ErrDataFlowForMember},
		{"by a member", Requester{UserID: uuid.New(), Name: "Kim"}, NewRequest{SubjectEmail: "a@example.org", FlowKind: FlowDataAccess}, ErrFlowNotAllowed},
	}
	for _, tc := range refused {
		if _, err := f.svc.CreateRequest(ctx, testOrg, tc.by, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s = %v; want %v", tc.name, err, tc.want)
		}
	}
}

// No admin changes a data request flow: not its definition, an older version,
// or any of the wallet's settings for it.
func TestDataRequestFlowsAreNotEdited(t *testing.T) {
	ctx := context.Background()
	f, _ := newDataFixture(FlowDataAccess)
	f.svc.flowHostedSettings = &fakeFlowHosted{byFlow: map[string]FlowHosted{}}
	f.svc.flowBrandings = newFakeFlowBranding()
	f.svc.flowDiplomaSettings = &fakeFlowDiplomas{modes: map[string]DiplomaMode{}}
	f.svc.flowAddressSettings = &fakeFlowAddresses{modes: map[string]AddressMode{}}
	for _, id := range []string{standardTestFlow(FlowDataAccess).ID, chipFlow.ID} {
		edits := map[string]func() error{
			"EditFlow": func() error {
				_, err := f.svc.EditFlow(ctx, testOrg, id, standardFlowSpec(FlowDataAccess))
				return err
			},
			"ActivateFlowVersion": func() error {
				_, err := f.svc.ActivateFlowVersion(ctx, testOrg, id, 1)
				return err
			},
			"SaveFlowHosted": func() error {
				_, err := f.svc.SaveFlowHosted(ctx, testOrg, id, FlowHosted{Completion: CompletionRedirect})
				return err
			},
			"SaveFlowBranding": func() error {
				_, err := f.svc.SaveFlowBranding(ctx, testOrg, id, FlowBranding{}, LogoChange{})
				return err
			},
			"SaveFlowDiplomas": func() error {
				_, err := f.svc.SaveFlowDiplomas(ctx, testOrg, id, DiplomasRequired)
				return err
			},
			"SaveFlowAddress": func() error {
				_, err := f.svc.SaveFlowAddress(ctx, testOrg, id, AddressRequired)
				return err
			},
		}
		for name, edit := range edits {
			if err := edit(); !errors.Is(err, ErrDataRequestFlow) {
				t.Errorf("%s on %s = %v; want ErrDataRequestFlow", name, id, err)
			}
		}
	}
	if len(f.ips.versions) != 0 {
		t.Errorf("engine versions = %v; want none", f.ips.versions)
	}
}
