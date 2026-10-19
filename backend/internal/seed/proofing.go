package seed

import (
	"context"
	_ "embed"
	"errors"
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
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// The identity proofing use cases of the demo orgs: each org runs proofing
// for its own dummy customers, with the flows its use case needs and the two
// data request flows, as an org admin would set them up. A flow is found by
// its name within its org, a customer by its name, so a re-run creates only
// what is missing. What a session of a use case needs beyond the flow (an
// expected person's name and date of birth, a reference photo) is sent with
// the session through the customer API.

// demoProofingFlow is one of a demo org's flows: its engine definition and
// the wallet's own settings for it.
type demoProofingFlow struct {
	def      flow.FlowDefinition
	kind     proofing.FlowKind
	diplomas proofing.DiplomaMode
	address  proofing.AddressMode
}

// demoProofingCustomer is a customer with the flows assigned to it, the
// first its default; every customer is also given the data request flows. Its
// name says "(Demo)", like the seeded orgs: they are real organisations.
type demoProofingCustomer struct {
	name          string
	flows         []string
	retentionDays int
}

// demoProofingOrg is a demo org (by slug, one of demoOrganizations) with its
// own flows, besides the data request flows every org has, its customers and
// optionally its theme, which the Idem app also shows its sessions in.
type demoProofingOrg struct {
	slug      string
	flows     []demoProofingFlow
	customers []demoProofingCustomer
	theme     *demoTheme
}

// demoTheme is a demo org's theme and its logo.
type demoTheme struct {
	settings themesettings.SettingsInput
	logo     []byte
}

// cmLogoPNG is CM.com's mark, as the CM build of the Idem app showed it.
//
//go:embed assets/cm-logo.png
var cmLogoPNG []byte

// cmTheme is CM.com's look from that build: its action blue (CM's brand blue
// is too light for white text), purple, ink text and header, and its greys.
var cmTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: "#036BD2", AccentColor: "#5412C7", TextColor: "#101E1E", SurfaceColor: "#FBFBFC",
		BorderColor: "#EAEBEE", LinkColor: "#036BD2", SuccessColor: "#2A9066", TopbarColor: "#101E1E",
	},
	logo: cmLogoPNG,
}

// The other demo orgs' marks; assets/README.md has each source and licence.
var (
	//go:embed assets/radboud-logo.png
	radboudLogoPNG []byte
	//go:embed assets/asr-logo.png
	asrLogoPNG []byte
	//go:embed assets/unibet-logo.png
	unibetLogoPNG []byte
	//go:embed assets/odixus-logo.png
	odixusLogoPNG []byte
)

// The demo themes set only what the Idem app shows a session in (its
// AppBranding): primary, accent (the app's secondary: step labels and badge
// text, so it must read on white), text, surface, border, link, success,
// error, top bar (the app's header, ignored below 3:1 against white text), font
// and logo. Each font is a stack the theme settings page offers.
const (
	fontOpenSans   = `"Open Sans", system-ui, sans-serif`
	fontMontserrat = `"Montserrat", system-ui, sans-serif`
	fontSystemUI   = `system-ui, -apple-system, "Segoe UI", Roboto, sans-serif`
	fontHelvetica  = `"Helvetica Neue", Arial, sans-serif`
)

// radboudTheme is Radboud's house style: Red Impact, Maroon, Lady Bug and
// Berry, the secondary palette's green, and ru.nl's text, greys and Open Sans.
var radboudTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: "#E3000B", AccentColor: "#730E04", TextColor: "#303030", SurfaceColor: "#F8F7F7",
		BorderColor: "#D7D7D7", LinkColor: "#BE311E", SuccessColor: "#4AA943", ErrorColor: "#8F2011",
		TopbarColor: "#E3000B", FontFamily: fontOpenSans,
	},
	logo: radboudLogoPNG,
}

// asrTheme is asr.nl's design tokens: its dark green (the logo's green is too
// light for white text), its blue as accent (the brand amber does not read on
// white), ink as header, greys and feedback colours. Its Avenir is not a font
// the wallet ships, so the closest stack stands in.
var asrTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: "#0F865D", AccentColor: "#0064A8", TextColor: "#2A292E", SurfaceColor: "#F6F6F7",
		BorderColor: "#E5E5E5", LinkColor: "#0064A8", SuccessColor: "#0F865D", ErrorColor: "#CE0A1E",
		TopbarColor: "#2A292E", FontFamily: fontHelvetica,
	},
	logo: asrLogoPNG,
}

// unibetTheme is unibet.nl's look: its green header, its darker navigation
// green as accent (the yellow call to action does not read on white), logo
// green, ink and greys and link blue. unibet.nl shows no error colour, so the
// default's is kept; its Roboto is in the system stack.
var unibetTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: "#147B45", AccentColor: "#00531D", TextColor: "#111111", SurfaceColor: "#EEEEEE",
		BorderColor: "#CCCCCC", LinkColor: "#007299", SuccessColor: "#3EAB37", TopbarColor: "#147B45",
		FontFamily: fontSystemUI,
	},
	logo: unibetLogoPNG,
}

// odixusTheme is odixus.nl's look: its logo blue, the darker blue its
// gradients end in as accent (the logo's orange does not read on white), its
// body text, light background, input border, success and danger colours.
// Its Poppins is not a font the wallet ships; Montserrat is the closest.
var odixusTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: "#0056AD", AccentColor: "#0A4286", TextColor: "#2F281E", SurfaceColor: "#F0F4F9",
		BorderColor: "#E0E0E0", LinkColor: "#0056AD", SuccessColor: "#79B93C", ErrorColor: "#DF6951",
		TopbarColor: "#0056AD", FontFamily: fontMontserrat,
	},
	logo: odixusLogoPNG,
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
// Idem app (passport, identity card or Dutch driving licence) and a live face
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
	flowRadboudEnrol     = "Aanmelden cursist"
	flowRadboudPassword  = "Wachtwoord herstellen"
	flowASRKnownPerson   = "Identificatie bekende persoon"
	flowASRAccountChange = "Rekeningnummer wijzigen"
	flowUnibetOnboarding = "Nieuwe klant (AMLR)"
	flowCMAgeCheck       = "Ticket: echte persoon en leeftijd"
	flowOdixusOnboarding = "Nieuwe klant met woonadres"
	flowDataAccess       = "Mijn gegevens inzien"
	flowDataErasure      = "Mijn gegevens verwijderen"
)

// purposeDataRequest is the processing purpose of the data request flows.
const purposeDataRequest = "Verzoek van de betrokkene (AVG art. 15/17)"

// dataRequestFlows are every org's: a customer's subject asks what is held of
// them, or for its erasure, and the session goes to review once they are
// proven. Every customer is offered them: each holds personal data a subject
// may ask about.
var dataRequestFlows = []demoProofingFlow{
	{def: chipAndFace(flowDataAccess, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataAccess},
	{def: chipAndFace(flowDataErasure, privacy.LegalBasisLegalObligation, purposeDataRequest), kind: proofing.FlowDataErasure},
}

var demoProofingOrgs = []demoProofingOrg{
	{
		slug: radboudSlug,
		flows: []demoProofingFlow{
			// A new course participant enrols with their passport or identity
			// card, then uploads their DUO diploma extracts.
			{def: chipAndFace(flowRadboudEnrol, privacy.LegalBasisContract, "Inschrijving cursus Radboud Academy"), diplomas: proofing.DiplomasRequired},
			// A user resets their password by proving they are the account's
			// holder: sent for one known person (name and birth date with the
			// session).
			{def: chipAndFace(flowRadboudPassword, privacy.LegalBasisContract, "Herstel van het wachtwoord")},
		},
		customers: []demoProofingCustomer{
			{name: "Radboud Academy (Demo)", flows: []string{flowRadboudEnrol, flowRadboudPassword}, retentionDays: demoRetentionDays},
			{name: "Radboud Studentenservice (Demo)", flows: []string{flowRadboudPassword}, retentionDays: demoRetentionDays},
		},
		theme: radboudTheme,
	},
	{
		slug: asrSlug,
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
		customers: []demoProofingCustomer{
			{name: "a.s.r. Schadeverzekeringen (Demo)", flows: []string{flowASRKnownPerson, flowASRAccountChange}, retentionDays: demoRetentionDays},
			{name: "a.s.r. Levensverzekeringen (Demo)", flows: []string{flowASRKnownPerson, flowASRAccountChange}, retentionDays: demoRetentionDays},
		},
		theme: asrTheme,
	},
	{
		slug: unibetSlug,
		flows: []demoProofingFlow{
			// A new player under the AMLR: full identification and liveness.
			// The occupational status (UWV Verzekeringsbericht) is not a step
			// the wallet can collect yet.
			{def: chipAndFace(flowUnibetOnboarding, privacy.LegalBasisLegalObligation, "Klantonderzoek kansspelen (AMLR/Wwft)")},
		},
		customers: []demoProofingCustomer{
			{name: "Unibet Casino (Demo)", flows: []string{flowUnibetOnboarding}, retentionDays: demoRetentionDays},
			{name: "Unibet Sport (Demo)", flows: []string{flowUnibetOnboarding}, retentionDays: demoRetentionDays},
		},
		theme: unibetTheme,
	},
	{
		slug: cmSlug,
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
		customers: []demoProofingCustomer{
			{name: "CM Tickets Concerten (Demo)", flows: []string{flowCMAgeCheck}, retentionDays: shortRetentionDays},
			{name: "CM Tickets Festivals (Demo)", flows: []string{flowCMAgeCheck}, retentionDays: shortRetentionDays},
		},
		theme: cmTheme,
	},
	{
		slug: odixusSlug,
		flows: []demoProofingFlow{
			// A new customer identifies themselves, then enters the postal
			// address they live at.
			{def: chipAndFace(flowOdixusOnboarding, privacy.LegalBasisContract, "Onboarding nieuwe klant"), address: proofing.AddressRequired},
		},
		customers: []demoProofingCustomer{
			{name: "odixus Onboarding (Demo)", flows: []string{flowOdixusOnboarding}, retentionDays: demoRetentionDays},
		},
		theme: odixusTheme,
	},
}

// allFlows is o's own flows and the data request flows.
func (o demoProofingOrg) allFlows() []demoProofingFlow {
	return append(slices.Clone(o.flows), dataRequestFlows...)
}

// seedProofing gives each demo proofing org, found in orgsBySlug, its flows
// and customers, created by createdBy.
func seedProofing(ctx context.Context, pool *pgxpool.Pool, orgsBySlug map[string]organization.Organization, createdBy uuid.UUID) error {
	for _, o := range demoProofingOrgs {
		org, ok := orgsBySlug[o.slug]
		if !ok {
			return fmt.Errorf("seed: proofing org %s not seeded", o.slug)
		}
		if err := seedProofingOrg(ctx, pool, org.ID, createdBy, o); err != nil {
			return err
		}
	}
	return nil
}

// seedProofingOrg gives orgID o's flows and customers, created by createdBy.
func seedProofingOrg(ctx context.Context, pool *pgxpool.Pool, orgID, createdBy uuid.UUID, o demoProofingOrg) error {
	recorder := audit.NewDBRecorder()
	flowIDs, err := ensureProofingFlows(ctx, pool, recorder, orgID, o.allFlows())
	if err != nil {
		return err
	}
	if err := ensureDemoTheme(ctx, themesettings.NewStore(pool, recorder), orgID, o); err != nil {
		return err
	}
	customers := proofing.NewCustomerStore(pool, recorder)
	existing, err := customers.List(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: list customers %s: %w", o.slug, err)
	}
	for _, c := range o.customers {
		if slices.ContainsFunc(existing, func(e proofing.Customer) bool { return e.Name == c.name }) {
			continue
		}
		if err := createProofingCustomer(ctx, pool, recorder, orgID, createdBy, c, flowIDs); err != nil {
			return err
		}
	}
	return nil
}

// createProofingCustomer creates c with its flows and settings in one
// transaction, so a re-run, which skips a customer it finds by name, never
// finds one without them.
func createProofingCustomer(ctx context.Context, pool *pgxpool.Pool, recorder audit.Recorder, orgID, createdBy uuid.UUID,
	c demoProofingCustomer, flowIDs map[string]string,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("seed: customer %s: %w", c.name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	customers := proofing.NewCustomerStore(tx, recorder)
	customer, err := customers.Create(ctx, orgID, createdBy, c.name)
	if err != nil {
		return fmt.Errorf("seed: customer %s: %w", c.name, err)
	}
	sel := proofing.FlowSelection{DefaultFlowID: flowIDs[c.flows[0]]}
	for _, name := range c.flowNames() {
		sel.FlowIDs = append(sel.FlowIDs, flowIDs[name])
	}
	if _, err := customers.SaveFlows(ctx, orgID, customer.ID, sel); err != nil {
		return fmt.Errorf("seed: customer %s flows: %w", c.name, err)
	}
	if _, err := customers.SaveSettings(ctx, orgID, customer.ID,
		proofing.CustomerSettingsChange{DataRetentionDays: &c.retentionDays}); err != nil {
		return fmt.Errorf("seed: customer %s settings: %w", c.name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("seed: customer %s: %w", c.name, err)
	}
	return nil
}

// ensureDemoTheme gives orgID o's theme unless the org already has one, so a
// theme changed since is kept.
func ensureDemoTheme(ctx context.Context, themes *themesettings.Store, orgID uuid.UUID, o demoProofingOrg) error {
	if o.theme == nil {
		return nil
	}
	current, err := themes.GetSettings(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: theme %s: %w", o.slug, err)
	}
	if current.Configured {
		return nil
	}
	contentType, ok := themesettings.DetectLogoType(o.theme.logo)
	if !ok {
		return fmt.Errorf("seed: theme %s: logo is not an image", o.slug)
	}
	_, err = themes.Save(ctx, orgID, o.theme.settings,
		themesettings.LogoUpdate{Replace: true, Logo: themesettings.Logo{Bytes: o.theme.logo, ContentType: contentType}})
	if err != nil {
		return fmt.Errorf("seed: theme %s: %w", o.slug, err)
	}
	return nil
}

// EnsureProofingDemo provisions the demo proofing orgs with their flows and
// customers, and the Yivi team as their admins, so identity proofing can be
// tried on staging. Idempotent; staging only (`seed --proofing-demo`).
func EnsureProofingDemo(ctx context.Context, dsn, addressDomain string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: connect: %w", err)
	}
	defer pool.Close()

	users := user.NewStore(pool)
	orgs := organization.NewStore(pool, audit.NewDBRecorder())
	orgsBySlug := map[string]organization.Organization{}
	for _, o := range demoOrganizations {
		if !slices.ContainsFunc(demoProofingOrgs, func(p demoProofingOrg) bool { return p.slug == o.slug }) {
			continue
		}
		if err := requireSeedOwned(ctx, orgs, o); err != nil {
			return err
		}
		org, err := ensureOrg(ctx, pool, o, addressDomain)
		if err != nil {
			return err
		}
		// requireSeedOwned proved it the seed's: an older seed's name (one
		// without "(Demo)") follows the current one.
		if org.Name != o.name {
			if _, err := pool.Exec(ctx, `UPDATE organizations SET name = $2 WHERE id = $1`, org.ID, o.name); err != nil {
				return fmt.Errorf("seed: rename org %q: %w", o.slug, err)
			}
			org.Name = o.name
		}
		for _, m := range yiviTeam {
			if err := ensureTeamMember(ctx, users, orgs, org.ID, m); err != nil {
				return err
			}
		}
		orgsBySlug[o.slug] = org
	}

	creator, err := ensureUser(ctx, users, yiviTeam[0].email, yiviTeam[0].givenNames, yiviTeam[0].lastName, yiviTeam[0].preferredName)
	if err != nil {
		return err
	}
	if err := seedProofing(ctx, pool, orgsBySlug, creator.ID); err != nil {
		return err
	}
	slog.Info("ensured identity proofing demo", slog.Int("orgs", len(orgsBySlug)))
	return nil
}

// ErrNotSeedOwned is returned when a demo org's slug belongs to an org the
// seed did not create: the demo must never adopt a real tenant.
var ErrNotSeedOwned = errors.New("seed: slug taken by an organisation the seed did not create")

// requireSeedOwned refuses o when an org with its slug exists that does not
// carry o's placeholder KVK number and EUID, the seed's mark on its orgs.
func requireSeedOwned(ctx context.Context, orgs *organization.Store, o demoOrganization) error {
	existing, err := orgs.GetBySlug(ctx, o.slug)
	if errors.Is(err, organization.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("seed: look up org %q: %w", o.slug, err)
	}
	if existing.KVKNumber != o.kvkNumber || existing.EUID != o.euid {
		return fmt.Errorf("%w: %q", ErrNotSeedOwned, o.slug)
	}
	return nil
}

// flowNames is every flow c is offered: its own and the data request flows.
func (c demoProofingCustomer) flowNames() []string {
	names := slices.Clone(c.flows)
	for _, f := range dataRequestFlows {
		names = append(names, f.def.Name)
	}
	return names
}

// ensureProofingFlows creates each of want that orgID lacks, with its kind and
// diploma setting, and returns every one's id by name.
func ensureProofingFlows(ctx context.Context, pool *pgxpool.Pool, recorder audit.Recorder, orgID uuid.UUID, want []demoProofingFlow) (map[string]string, error) {
	existing, err := flow.NewPostgresStore(pool).List(ctx, orgID.String())
	if err != nil {
		return nil, fmt.Errorf("seed: list flows: %w", err)
	}
	ids := map[string]string{}
	for _, f := range want {
		i := slices.IndexFunc(existing, func(e flow.FlowDefinition) bool { return e.Name == f.def.Name })
		if i >= 0 {
			ids[f.def.Name] = existing[i].ID
			continue
		}
		id, err := createProofingFlow(ctx, pool, recorder, orgID, f)
		if err != nil {
			return nil, err
		}
		ids[f.def.Name] = id
	}
	return ids, nil
}

// createProofingFlow saves f for orgID with its kind, diploma and address settings in
// one transaction, so a re-run never finds the flow without them.
func createProofingFlow(ctx context.Context, pool *pgxpool.Pool, recorder audit.Recorder, orgID uuid.UUID, f demoProofingFlow) (string, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("seed: flow %s: %w", f.def.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	def := f.def
	def.TenantID = orgID.String()
	saved, err := flow.NewPostgresStore(tx).Save(ctx, def)
	if err != nil {
		return "", fmt.Errorf("seed: flow %s: %w", f.def.Name, err)
	}
	if f.kind != "" {
		if _, err := proofing.NewDataRequestStore(tx, recorder, nil).SaveFlowKind(ctx, orgID, saved.ID, f.kind); err != nil {
			return "", fmt.Errorf("seed: flow %s kind: %w", f.def.Name, err)
		}
	}
	if f.diplomas != "" {
		if _, err := proofing.NewFlowDiplomaStore(tx, recorder).Save(ctx, orgID, saved.ID, f.diplomas); err != nil {
			return "", fmt.Errorf("seed: flow %s diplomas: %w", f.def.Name, err)
		}
	}
	if f.address != "" {
		if _, err := proofing.NewFlowAddressStore(tx, recorder).Save(ctx, orgID, saved.ID, f.address); err != nil {
			return "", fmt.Errorf("seed: flow %s address: %w", f.def.Name, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("seed: flow %s: %w", f.def.Name, err)
	}
	return saved.ID, nil
}
