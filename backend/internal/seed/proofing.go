package seed

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofing"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/flow"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingengine/privacy"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// The identity proofing use cases of the demo orgs: each org runs proofing
// for external people, with the flows its use case needs and the two data
// request flows, as an org admin would set them up. A flow is found by its
// name within its org, so a re-run creates only what is missing. What a session
// of a use case needs beyond the flow (an expected person's name and date of
// birth, a reference photo) is sent with the session through the API.

// demoProofingFlow is one of a demo org's flows: its engine definition and
// the wallet's own settings for it.
type demoProofingFlow struct {
	def      flow.FlowDefinition
	kind     proofing.FlowKind
	diplomas proofing.DiplomaMode
}

// demoProofingOrg is a demo org (by slug, one of demoOrganizations) with its
// own flows, besides the data request flows every org has, the one of them that
// is its default, and how long it keeps a session's personal data.
type demoProofingOrg struct {
	slug          string
	flows         []demoProofingFlow
	defaultFlow   string
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

// demoRetentionDays and shortRetentionDays are how long a demo org keeps a
// session's personal data: the default, and CM's minimum for a ticket's age
// check.
const (
	demoRetentionDays  = 30
	shortRetentionDays = 7
)

// chipAndFace is an eIDAS substantial flow: the document's chip read in the
// Idem app (passport or identity card; the engine refuses an EU driving
// licence for now) and a live face matched against its portrait by Regula.
func chipAndFace(name string, basis privacy.LegalBasis, purpose string) flow.FlowDefinition {
	return flow.FlowDefinition{
		Name: name, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
		FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
		RequiredAssuranceLevel: flow.AssuranceLevelSubstantial, LegalBasis: basis, ProcessingPurpose: purpose,
	}
}

// Flow names, also how an org below names its default flow.
const (
	flowRadboudEnrol     = "Aanmelden cursist"
	flowRadboudPassword  = "Wachtwoord herstellen"
	flowASRKnownPerson   = "Identificatie bekende persoon"
	flowASRAccountChange = "Rekeningnummer wijzigen"
	flowUnibetOnboarding = "Nieuwe klant (AMLR)"
	flowCMAgeCheck       = "Ticket: echte persoon en leeftijd"
	flowDataAccess       = "Mijn gegevens inzien"
	flowDataErasure      = "Mijn gegevens verwijderen"
)

// purposeDataRequest is the processing purpose of the data request flows.
const purposeDataRequest = "Verzoek van de betrokkene (AVG art. 15/17)"

// dataRequestFlows are every org's: an external person asks what is held of
// them, or for its erasure, and the session goes to review once they are
// proven. Every org has them: each holds personal data a person may ask about.
var dataRequestFlows = []demoProofingFlow{
	{def: chipAndFace(flowDataAccess, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataAccess},
	{def: chipAndFace(flowDataErasure, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataErasure},
}

var demoProofingOrgs = []demoProofingOrg{
	{
		slug: radboudSlug, defaultFlow: flowRadboudEnrol, retentionDays: demoRetentionDays,
		flows: []demoProofingFlow{
			// A new course participant enrols with their passport or identity
			// card, then uploads their DUO diploma extracts.
			{def: chipAndFace(flowRadboudEnrol, privacy.LegalBasisContract, "Inschrijving cursus Radboud Academy"), diplomas: proofing.DiplomasRequired},
			// A user resets their password by proving they are the account's
			// holder: sent for one known person (name and birth date with the
			// session).
			{def: chipAndFace(flowRadboudPassword, privacy.LegalBasisContract, "Herstel van het wachtwoord")},
		},
	},
	{
		slug: asrSlug, defaultFlow: flowASRKnownPerson, retentionDays: demoRetentionDays,
		flows: []demoProofingFlow{
			// A pre-configured person (name and birth date sent with the
			// session) identifies themselves; anyone else is rejected
			// (IDENTITY_MISMATCH).
			{def: chipAndFace(flowASRKnownPerson, privacy.LegalBasisContract, "Identificatie van de verzekerde")},
			// Changing the bank account on a policy: a liveness check of the
			// known policyholder, their live face matched against a.s.r.'s own
			// photo of them (sent with the session); no document is read.
			{def: flow.FlowDefinition{
				Name: flowASRAccountChange, Steps: faceOnlySteps, SelfieLocation: flow.LocationNative,
				FaceProvider: flow.FaceProviderRegula, RequiredChecks: faceChecks,
				LegalBasis: privacy.LegalBasisContract, ProcessingPurpose: "Wijziging van het rekeningnummer op de polis",
			}},
		},
	},
	{
		slug: unibetSlug, defaultFlow: flowUnibetOnboarding, retentionDays: demoRetentionDays,
		flows: []demoProofingFlow{
			// A new player under the AMLR: full identification and liveness.
			// The occupational status (UWV Verzekeringsbericht) is not a step
			// the wallet can collect yet.
			{def: chipAndFace(flowUnibetOnboarding, privacy.LegalBasisLegalObligation, "Klantonderzoek kansspelen (AMLR/Wwft)")},
		},
	},
	{
		slug: cmSlug, defaultFlow: flowCMAgeCheck, retentionDays: shortRetentionDays,
		flows: []demoProofingFlow{
			// Buying a ticket: a real, live person of age. Only the chip's data
			// and the checks come back, never an image.
			{def: flow.FlowDefinition{
				Name: flowCMAgeCheck, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
				FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
				RequestedAttributes: cmAttributes,
				LegalBasis:          privacy.LegalBasisContract, ProcessingPurpose: "Leeftijdscontrole bij aankoop ticket",
			}},
		},
	},
}

// allFlows is o's own flows and the data request flows.
func (o demoProofingOrg) allFlows() []demoProofingFlow {
	return append(slices.Clone(o.flows), dataRequestFlows...)
}

// seedProofing gives each demo proofing org, found in orgsBySlug, its flows,
// default flow and retention.
func seedProofing(ctx context.Context, pool *pgxpool.Pool, orgsBySlug map[string]organization.Organization) error {
	for _, o := range demoProofingOrgs {
		org, ok := orgsBySlug[o.slug]
		if !ok {
			return fmt.Errorf("seed: proofing org %s not seeded", o.slug)
		}
		if err := seedProofingOrg(ctx, pool, org.ID, o); err != nil {
			return err
		}
	}
	return nil
}

// seedProofingOrg gives orgID o's flows, default flow and retention. An org
// that has a default flow already keeps it.
func seedProofingOrg(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, o demoProofingOrg) error {
	recorder := audit.NewDBRecorder()
	flowIDs, err := ensureProofingFlows(ctx, pool, recorder, orgID, o.allFlows())
	if err != nil {
		return err
	}
	selections := proofing.NewSettingsStore(pool, recorder)
	sel, err := selections.FlowSelection(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: flow selection %s: %w", o.slug, err)
	}
	if sel.DefaultFlowID == "" {
		sel.DefaultFlowID = flowIDs[o.defaultFlow]
		if err := selections.SaveFlowSelection(ctx, orgID, sel); err != nil {
			return fmt.Errorf("seed: default flow %s: %w", o.slug, err)
		}
	}
	settings := proofing.NewOrgSettingsStore(pool, recorder)
	current, err := settings.Get(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: proofing settings %s: %w", o.slug, err)
	}
	if !current.UpdatedAt.IsZero() {
		return nil
	}

	session := current.Session
	session.DataRetentionDays = o.retentionDays
	if _, err := settings.SaveSession(ctx, orgID, session); err != nil {
		return fmt.Errorf("seed: proofing settings %s: %w", o.slug, err)
	}
	return nil
}

// EnsureProofingDemo provisions the demo proofing orgs with their flows, and
// the Yivi team as their admins, so identity proofing can be tried on staging.
// Idempotent; staging only (`seed --proofing-demo`).
func EnsureProofingDemo(ctx context.Context, dsn, addressDomain string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: connect: %w", err)
	}
	defer pool.Close()

	users := user.NewStore(pool)
	orgs := organization.NewStore(pool, audit.NewDBRecorder())
	orgsBySlug := map[string]organization.Organization{}
	for _, o := range slices.Concat(demoOrganizations, proofingOrganizations) {
		if !slices.ContainsFunc(demoProofingOrgs, func(p demoProofingOrg) bool { return p.slug == o.slug }) {
			continue
		}
		org, err := ensureOrg(ctx, pool, o, addressDomain)
		if err != nil {
			return err
		}
		for _, m := range yiviTeam {
			if err := ensureTeamMember(ctx, users, orgs, org.ID, m); err != nil {
				return err
			}
		}
		orgsBySlug[o.slug] = org
	}

	if err := seedProofing(ctx, pool, orgsBySlug); err != nil {
		return err
	}
	slog.Info("ensured identity proofing demo", slog.Int("orgs", len(orgsBySlug)))
	return nil
}

// ensureProofingFlows creates each of want that orgID lacks, with its kind and
// diploma setting, and returns every one's id by name.
func ensureProofingFlows(ctx context.Context, pool *pgxpool.Pool, recorder audit.Recorder, orgID uuid.UUID, want []demoProofingFlow) (map[string]string, error) {
	flows := flow.NewPostgresStore(pool)
	existing, err := flows.List(ctx, orgID.String())
	if err != nil {
		return nil, fmt.Errorf("seed: list flows: %w", err)
	}
	kinds := proofing.NewDataRequestStore(pool, recorder, nil)
	diplomas := proofing.NewFlowDiplomaStore(pool, recorder)
	ids := map[string]string{}
	for _, f := range want {
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
