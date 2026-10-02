package seed

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
)

// The identity proofing use cases the demo org runs for its customers: each a
// customer with the flows its use case needs, as an admin would set them up.
// A flow is found by its name, a customer by its name, so a re-run creates
// only what is missing. What a session of a use case needs beyond the flow
// (an expected person's name and date of birth, a reference photo) is sent
// with the session through the customer API.

// demoProofingFlow is one of the demo org's flows: its engine definition and
// the wallet's own settings for it.
type demoProofingFlow struct {
	def      flow.FlowDefinition
	kind     proofing.FlowKind
	diplomas proofing.DiplomaMode
}

// demoProofingCustomer is a customer with the flows assigned to it, the
// first its default.
type demoProofingCustomer struct {
	name          string
	flows         []string
	retentionDays int
}

// The engine's names for what a flow reads and checks.
var (
	chipAndFaceSteps = []flow.Step{flow.StepDocumentCapture, flow.StepNFCRead, flow.StepFaceVerification}
	faceOnlySteps    = []flow.Step{flow.StepFaceVerification}
	// substantialChecks are what an eIDAS substantial flow must require
	// (flow.LevelRequirements): a genuine, original chip and a live face
	// matched against its portrait.
	substantialChecks = []flow.Check{flow.CheckNFCPassiveAuth, flow.CheckNFCChipAuth, flow.CheckFaceMatch, flow.CheckFaceLiveness}
	faceChecks        = []flow.Check{flow.CheckFaceMatch, flow.CheckFaceLiveness}
	// cmAttributes leave out every image: CM checks a real, adult person and
	// keeps no photo of them.
	cmAttributes = []string{"dg1", "chip_checks", "biometrics"}
)

// demoRetentionDays and shortRetentionDays are how long a demo customer
// keeps a session's personal data: the default, and CM's minimum for a
// ticket's age check.
const (
	demoRetentionDays  = 30
	shortRetentionDays = 7
)

// chipAndFace is an eIDAS substantial flow: the document's chip read in the
// Idem app (passport, identity card or EU driving licence) and a live face
// matched against its portrait by Regula.
func chipAndFace(name string, basis privacy.LegalBasis, purpose string) flow.FlowDefinition {
	return flow.FlowDefinition{
		Name: name, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
		FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
		RequiredAssuranceLevel: flow.AssuranceLevelSubstantial, LegalBasis: basis, ProcessingPurpose: purpose,
	}
}

// Flow names, also how a customer below names its flows.
const (
	flowRadboudEnrol     = "Radboud – Aanmelden cursist"
	flowRadboudPassword  = "Radboud – Wachtwoord herstellen"
	flowASRKnownPerson   = "a.s.r. – Identificatie bekende persoon"
	flowASRAccountChange = "a.s.r. – Rekeningnummer wijzigen"
	flowUnibetOnboarding = "Unibet – Nieuwe klant (AMLR)"
	flowCMAgeCheck       = "CM – Ticket: echte persoon en leeftijd"
	flowDataAccess       = "Mijn gegevens inzien"
	flowDataErasure      = "Mijn gegevens verwijderen"
)

// purposeDataRequest is the processing purpose of the data request flows.
const purposeDataRequest = "Verzoek van de betrokkene (AVG art. 15/17)"

var demoProofingFlows = []demoProofingFlow{
	// A new course participant enrols with their passport, identity card or
	// driving licence, then uploads their DUO diploma extracts.
	{def: chipAndFace(flowRadboudEnrol, privacy.LegalBasisContract, "Inschrijving cursus Radboud Academy"), diplomas: proofing.DiplomasRequired},
	// A user resets their password by proving they are the account's
	// holder: sent for one known person (name and birth date with the session).
	{def: chipAndFace(flowRadboudPassword, privacy.LegalBasisContract, "Herstel van het wachtwoord")},
	// A pre-configured person (name and birth date sent with the session)
	// identifies themselves; anyone else is rejected (IDENTITY_MISMATCH).
	{def: chipAndFace(flowASRKnownPerson, privacy.LegalBasisContract, "Identificatie van de verzekerde")},
	// Changing the bank account on a policy: a liveness check of the known
	// policyholder, their live face matched against a.s.r.'s own photo of
	// them (sent with the session); no document is read.
	{def: flow.FlowDefinition{
		Name: flowASRAccountChange, Steps: faceOnlySteps, SelfieLocation: flow.LocationNative,
		FaceProvider: flow.FaceProviderRegula, RequiredChecks: faceChecks,
		LegalBasis: privacy.LegalBasisContract, ProcessingPurpose: "Wijziging van het rekeningnummer op de polis",
	}},
	// A new player under the AMLR: full identification and liveness. The
	// occupational status (UWV Verzekeringsbericht) is not a step the wallet
	// can collect yet.
	{def: chipAndFace(flowUnibetOnboarding, privacy.LegalBasisLegalObligation, "Klantonderzoek kansspelen (AMLR/Wwft)")},
	// Buying a ticket: a real, live person of age. Only the chip's data and
	// the checks come back, never an image.
	{def: flow.FlowDefinition{
		Name: flowCMAgeCheck, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
		FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
		RequestedAttributes: cmAttributes, RequestedAttributesConfigured: true,
		LegalBasis: privacy.LegalBasisContract, ProcessingPurpose: "Leeftijdscontrole bij aankoop ticket",
	}},
	// A customer's subject asks what is held of them, or for its erasure: the
	// session goes to review once they are proven.
	{def: chipAndFace(flowDataAccess, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataAccess},
	{def: chipAndFace(flowDataErasure, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataErasure},
}

// dataRequestFlows are offered to every customer: each holds personal data a
// subject may ask about.
var dataRequestFlows = []string{flowDataAccess, flowDataErasure}

var demoProofingCustomers = []demoProofingCustomer{
	{name: "Radboud Universiteit", flows: []string{flowRadboudEnrol, flowRadboudPassword}, retentionDays: demoRetentionDays},
	{name: "a.s.r. verzekeringen", flows: []string{flowASRKnownPerson, flowASRAccountChange}, retentionDays: demoRetentionDays},
	{name: "Unibet", flows: []string{flowUnibetOnboarding}, retentionDays: demoRetentionDays},
	{name: "CM", flows: []string{flowCMAgeCheck}, retentionDays: shortRetentionDays},
}

// seedProofing gives orgID the demo flows and customers, created by createdBy.
func seedProofing(ctx context.Context, pool *pgxpool.Pool, orgID, createdBy uuid.UUID) error {
	recorder := audit.NewDBRecorder()
	flowIDs, err := ensureProofingFlows(ctx, pool, recorder, orgID)
	if err != nil {
		return err
	}
	customers := proofing.NewCustomerStore(pool, recorder)
	existing, err := customers.List(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: list customers: %w", err)
	}
	for _, c := range demoProofingCustomers {
		if slices.ContainsFunc(existing, func(e proofing.Customer) bool { return e.Name == c.name }) {
			continue
		}
		customer, err := customers.Create(ctx, orgID, createdBy, c.name)
		if err != nil {
			return fmt.Errorf("seed: customer %s: %w", c.name, err)
		}
		sel := proofing.FlowSelection{DefaultFlowID: flowIDs[c.flows[0]]}
		for _, name := range append(slices.Clone(c.flows), dataRequestFlows...) {
			sel.FlowIDs = append(sel.FlowIDs, flowIDs[name])
		}
		if _, err := customers.SaveFlows(ctx, orgID, customer.ID, sel); err != nil {
			return fmt.Errorf("seed: customer %s flows: %w", c.name, err)
		}
		settings := customer.Settings
		settings.DataRetentionDays = c.retentionDays
		if _, err := customers.SaveSettings(ctx, orgID, customer.ID, settings); err != nil {
			return fmt.Errorf("seed: customer %s settings: %w", c.name, err)
		}
	}
	return nil
}

// ensureProofingFlows creates each demo flow orgID lacks, with its kind and
// diploma setting, and returns every demo flow's id by name.
func ensureProofingFlows(ctx context.Context, pool *pgxpool.Pool, recorder audit.Recorder, orgID uuid.UUID) (map[string]string, error) {
	flows := flow.NewPostgresStore(pool)
	existing, err := flows.List(ctx, orgID.String())
	if err != nil {
		return nil, fmt.Errorf("seed: list flows: %w", err)
	}
	kinds := proofing.NewDataRequestStore(pool, recorder, nil)
	diplomas := proofing.NewFlowDiplomaStore(pool, recorder)
	ids := map[string]string{}
	for _, f := range demoProofingFlows {
		i := slices.IndexFunc(existing, func(e flow.FlowDefinition) bool { return e.Name == f.def.Name })
		if i >= 0 {
			ids[f.def.Name] = existing[i].ID
			continue
		}
		def := f.def
		def.TenantID = orgID.String()
		saved, err := flows.Save(ctx, def)
		if err != nil {
			return nil, fmt.Errorf("seed: flow %s: %w", f.def.Name, err)
		}
		ids[f.def.Name] = saved.ID
		if f.kind != "" {
			if _, err := kinds.SaveFlowKind(ctx, orgID, saved.ID, f.kind); err != nil {
				return nil, fmt.Errorf("seed: flow %s kind: %w", f.def.Name, err)
			}
		}
		if f.diplomas != "" {
			if _, err := diplomas.Save(ctx, orgID, saved.ID, f.diplomas); err != nil {
				return nil, fmt.Errorf("seed: flow %s diplomas: %w", f.def.Name, err)
			}
		}
	}
	return ids, nil
}
