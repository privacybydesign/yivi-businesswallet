package proofing

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// The standard flows are See my data and Delete my data: the two data
// requests every org offers, as the GDPR requires (Art. 15 and 17). The wallet
// creates one flow of each kind per org the first time the org needs it
// (standardFlows), new orgs and existing ones alike, on the wallet's own
// definition (standardFlowSpec). No admin edits them (ErrDataRequestFlow) or
// lists them among the flows, and no flow becomes a data request any other
// way. A data request runs on the standard flow of its kind, also when it
// names another flow of that kind (one made a data request before the
// standard flows, migration 20261021122000).

// standardKinds are the data requests every org offers, in the order they are
// shown.
var standardKinds = []FlowKind{FlowDataAccess, FlowDataErasure}

// standardFlowNames name the standard flows. Only the org's members read
// them, next to the requests sent on them.
var standardFlowNames = map[FlowKind]string{
	FlowDataAccess:  "See my data",
	FlowDataErasure: "Delete my data",
}

const (
	// stepDocumentCapture and stepFaceVerification are the document scan and
	// the live face matched against the chip's photo; with stepNFCRead the
	// steps of a standard flow.
	stepDocumentCapture  = "document_capture"
	stepFaceVerification = "face_verification"
	// attributeChipChecks and attributeBiometrics are the chip and face checks'
	// outcomes and scores: what a standard flow's result carries besides the
	// document data. Never an image.
	attributeChipChecks = "chip_checks"
	attributeBiometrics = "biometrics"
	// legalBasisLegalObligation is the GDPR basis a data request is processed
	// on (Art. 6(1)(c)): the org must answer it.
	legalBasisLegalObligation = "legal_obligation"
	// standardProcessingPurpose is why a standard flow processes personal data.
	standardProcessingPurpose = "Answering a person's request to see or delete their data (GDPR Art. 15 and 17)"
)

// substantialChecks are the checks the engine holds an eIDAS Substantial flow
// to: a genuine, original chip and a live face matched against its portrait.
var substantialChecks = []string{"nfc.passive_auth", "nfc.chip_auth", "face.match", "face.liveness"}

// ErrDataRequestFlow is a change to a data request flow: See my data and
// Delete my data are the wallet's own, not flows an admin edits.
var ErrDataRequestFlow = errors.New("proofing: See my data and Delete my data are the wallet's own flows")

// standardFlowSpec is the wallet's definition of kind's standard flow: the
// person proves who they are at eIDAS Substantial (the document scanned, its
// chip read, a face check), and the org receives the name and date of birth
// a data request finds their sessions by (findDataMatches), with how surely
// they were proven.
func standardFlowSpec(kind FlowKind) proofingprovider.FlowSpec {
	return proofingprovider.FlowSpec{
		Name:                   standardFlowNames[kind],
		Steps:                  []string{stepDocumentCapture, stepNFCRead, stepFaceVerification},
		RequestedAttributes:    []string{attributeDocument, attributeChipChecks, attributeBiometrics},
		RequiredChecks:         substantialChecks,
		RequiredAssuranceLevel: eidasSubstantial,
		LegalBasis:             legalBasisLegalObligation,
		ProcessingPurpose:      standardProcessingPurpose,
	}
}

// meetsStandard reports whether f, the active version of a standard flow,
// holds what the wallet's definition requires: it reads the name and date of
// birth, scans the document, reads its chip and checks the face in the app, at
// eIDAS Substantial or above.
func meetsStandard(f proofingprovider.Flow) bool {
	return readsIdentity(f) && meetsAssurance(f.RequiredAssuranceLevel, eidasSubstantial) &&
		slices.Contains(f.Steps, stepDocumentCapture) && slices.Contains(f.Steps, stepNFCRead) &&
		hasFaceStep(f.Steps) && flowCompletable(f)
}

// DataRequestFlows returns the org's standard flows, See my data first, as
// the flow list has a flow: what the Verify a person dialog and the on-screen
// page show of a data request. A flow the org lacks is created first.
func (s *Service) DataRequestFlows(ctx context.Context, org Org) ([]OrgFlow, error) {
	standard, err := s.standardFlows(ctx, org)
	if err != nil {
		return nil, err
	}
	flows, err := s.Flows(ctx, org, FlowsAll)
	if err != nil {
		return nil, err
	}
	out := make([]OrgFlow, 0, len(standardKinds))
	for _, kind := range standardKinds {
		i := slices.IndexFunc(flows, func(f OrgFlow) bool { return f.ID == standard[kind].ID })
		if i < 0 {
			return nil, fmt.Errorf("proofing: standard flow %s org %s is not listed", standard[kind].ID, org.ID)
		}
		out = append(out, flows[i])
	}
	return out, nil
}

// standardFlows returns the org's standard flow of each data request kind. It
// creates the ones the org lacks, and gives one whose active version falls
// short of the wallet's definition (meetsStandard) that definition again in a
// new version, under its own name. Neither is the doing of whoever's call
// needed the flows, so their audit events name no actor.
func (s *Service) standardFlows(ctx context.Context, org Org) (map[FlowKind]proofingprovider.Flow, error) {
	if s.dataRequests == nil {
		return nil, errors.New("proofing: no data request store configured")
	}
	ctx = audit.WithoutActor(ctx)
	tenant := orgTenant(org.ID)
	flows, err := s.ips.ListFlows(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	ids, err := s.dataRequests.StandardFlows(ctx, org.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[FlowKind]proofingprovider.Flow, len(standardKinds))
	for _, kind := range standardKinds {
		flow, err := s.standardFlow(ctx, org, tenant, kind, ids[kind], flows)
		if err != nil {
			return nil, err
		}
		out[kind] = flow
	}
	return out, nil
}

// standardFlow is kind's standard flow, flow id among flows (the engine's
// list): created when id is empty or the engine no longer lists it, given the
// wallet's definition again when it falls short of it.
func (s *Service) standardFlow(ctx context.Context, org Org, tenant proofingprovider.Tenant, kind FlowKind,
	id string, flows []proofingprovider.Flow,
) (proofingprovider.Flow, error) {
	i := slices.IndexFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == id })
	if id == "" || i < 0 {
		return s.createStandardFlow(ctx, org, tenant, kind, id)
	}
	if meetsStandard(flows[i]) {
		return flows[i], nil
	}
	return s.restoreStandardFlow(ctx, org, tenant, kind, flows[i])
}

// createStandardFlow creates kind's standard flow in the engine and claims it
// as the org's (ClaimStandardFlow), from stale, a standard flow the engine no
// longer lists, if there is one. When a concurrent call claimed one first,
// that one is returned, and the flow created here is left a data request flow
// nothing runs on.
func (s *Service) createStandardFlow(ctx context.Context, org Org, tenant proofingprovider.Tenant, kind FlowKind,
	stale string,
) (proofingprovider.Flow, error) {
	spec, err := normalizeFlow(standardFlowSpec(kind))
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	created, err := s.ips.CreateFlow(ctx, tenant, spec)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: create standard flow %s org %s: %w", kind, org.ID, err)
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowCreated, created); err != nil {
		return proofingprovider.Flow{}, err
	}
	standard, err := s.dataRequests.ClaimStandardFlow(ctx, org.ID, kind, created.ID, stale)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	if standard == created.ID {
		return created, nil
	}
	flows, err := s.ips.ListFlows(ctx, tenant)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: list flows org %s: %w", org.ID, err)
	}
	i := slices.IndexFunc(flows, func(f proofingprovider.Flow) bool { return f.ID == standard })
	if i < 0 {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: standard flow %s org %s is not listed", standard, org.ID)
	}
	return flows[i], nil
}

// restoreStandardFlow saves the wallet's definition of kind's standard flow as
// the next version of f, keeping f's name.
func (s *Service) restoreStandardFlow(ctx context.Context, org Org, tenant proofingprovider.Tenant, kind FlowKind,
	f proofingprovider.Flow,
) (proofingprovider.Flow, error) {
	spec := standardFlowSpec(kind)
	spec.Name = f.Name
	spec, err := normalizeFlow(spec)
	if err != nil {
		return proofingprovider.Flow{}, err
	}
	restored, err := s.ips.CreateFlowVersion(ctx, tenant, f.ID, spec)
	if err != nil {
		return proofingprovider.Flow{}, fmt.Errorf("proofing: restore standard flow %s org %s: %w", f.ID, org.ID, err)
	}
	if err := s.settings.RecordFlowEvent(ctx, org.ID, audit.IdentityProofingFlowVersionCreated, restored); err != nil {
		return proofingprovider.Flow{}, err
	}
	return restored, nil
}

// requestFlowID is the flow a new request runs on: the one it names (none is
// the org's default, which sendableFlow resolves), or for a data request
// (in.FlowKind, or a flow of a data request kind) the org's standard flow of
// that kind. A kind given with a flow must be that flow's. A data request goes
// to an external person, from a sender who may use any flow: no member may
// use a data request flow.
func (s *Service) requestFlowID(ctx context.Context, org Org, by Requester, in NewRequest) (string, error) {
	named := FlowIdentity
	if in.FlowID != "" {
		var err error
		if named, err = s.flowKind(ctx, org.ID, in.FlowID); err != nil {
			return "", err
		}
	}
	kind := in.FlowKind
	switch {
	case kind == "":
		kind = named
	case in.FlowID != "" && kind != named:
		return "", fmt.Errorf("%w: flowKind %s is not the kind of the flow named, which is %s", ErrInvalidInput, kind, named)
	}
	switch {
	case !kind.dataRequest():
		return in.FlowID, nil
	case !in.external():
		return "", ErrDataFlowForMember
	case !by.mayUseAnyFlow():
		return "", ErrFlowNotAllowed
	}
	standard, err := s.standardFlows(ctx, org)
	if err != nil {
		return "", err
	}
	return standard[kind].ID, nil
}

// refuseDataRequestFlow is ErrDataRequestFlow for flow flowID of the org when
// it is a data request flow: no admin changes one.
func (s *Service) refuseDataRequestFlow(ctx context.Context, org Org, flowID string) error {
	kind, err := s.flowKind(ctx, org.ID, flowID)
	if err != nil {
		return err
	}
	if kind.dataRequest() {
		return ErrDataRequestFlow
	}
	return nil
}
