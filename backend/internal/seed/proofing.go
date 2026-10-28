package seed

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

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
// for external people, with the flows its use case needs, as an org admin
// would set them up. See my data and Delete my data are not among them: the
// wallet creates those for every org itself (proofing.Service.standardFlows).
// A flow is found by its name within its org, so a re-run creates only what is
// missing. What a session of a use case needs beyond the flow (an expected
// person's name and date of birth, a reference photo) is sent with the session
// through the API.

// demoProofingFlow is one of a demo org's flows: its engine definition and
// the wallet's own settings for it.
type demoProofingFlow struct {
	def      flow.FlowDefinition
	diplomas proofing.DiplomaMode
	address  proofing.AddressMode
	// employment is whether the flow asks for the UWV Verzekeringsbericht.
	employment proofing.EmploymentMode
}

// demoProofingOrg is a demo org (by slug, one of demoOrganizations) with its
// own flows, the one of them that is its default, how long its flows keep a
// session's personal data and optionally its theme, which the Idem app also
// shows its sessions in.
type demoProofingOrg struct {
	slug          string
	flows         []demoProofingFlow
	defaultFlow   string
	retentionDays int
	theme         *demoTheme
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

// odixusBlue is odixus.nl's logo blue.
const odixusBlue = "#0056AD"

// odixusTheme is odixus.nl's look: its logo blue, the darker blue its
// gradients end in as accent (the logo's orange does not read on white), its
// body text, light background, input border, success and danger colours.
// Its Poppins is not a font the wallet ships; Montserrat is the closest.
var odixusTheme = &demoTheme{
	settings: themesettings.SettingsInput{
		PrimaryColor: odixusBlue, AccentColor: "#0A4286", TextColor: "#2F281E", SurfaceColor: "#F0F4F9",
		BorderColor: "#E0E0E0", LinkColor: odixusBlue, SuccessColor: "#79B93C", ErrorColor: "#DF6951",
		TopbarColor: odixusBlue, FontFamily: fontMontserrat,
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
	// learns their name and that they are of age, never their date of birth
	// (the age check withholds it from dg1).
	cmAttributes = []string{"dg1", flow.AttributeAgeOver, "chip_checks", "biometrics"}
	// cmReasons are what the person reads under each of them.
	cmReasons = map[string]map[string]string{
		"dg1": {
			"en": "To put your name on the ticket.",
			"nl": "Voor je naam op het ticket.",
		},
		flow.AttributeAgeOver: {
			"en": "To check you are 18 or older.",
			"nl": "Om te controleren dat je 18 of ouder bent.",
		},
		"chip_checks": {
			"en": "To make sure your document is real.",
			"nl": "Om te controleren dat je document echt is.",
		},
		"biometrics": {
			"en": "To make sure the ticket goes to the document's holder.",
			"nl": "Om te controleren dat het ticket naar de houder van het document gaat.",
		},
	}
)

// demoReasons are why a demo flow collects each item it can, in each language
// a hosted page offers: what its person reads under the item, and what the
// flow editor asks for before it saves, so a demo flow's first edit does not
// ask for them. A flow without its own reasons gets these for what it collects.
var demoReasons = map[string]map[string]string{
	"dg1": {
		"en": "To know who you are: your name, date of birth and the document you used.",
		"nl": "Om te weten wie je bent: je naam, geboortedatum en het document dat je gebruikte.",
	},
	"dg11": {
		"en": "To record the extra details on your document's chip, such as your place of birth.",
		"nl": "Om de extra gegevens op de chip van je document vast te leggen, zoals je geboorteplaats.",
	},
	"dg2": {
		"en": "To keep the photo from your document's chip with the check.",
		"nl": "Om de foto van de chip van je document bij de controle te bewaren.",
	},
	"chip_checks": {
		"en": "To make sure your document is real.",
		"nl": "Om te controleren dat je document echt is.",
	},
	"document_image": {
		"en": "To keep a picture of your document with the check.",
		"nl": "Om een afbeelding van je document bij de controle te bewaren.",
	},
	"selfie": {
		"en": "To keep the photo taken of you with the check.",
		"nl": "Om de foto die van je is gemaakt bij de controle te bewaren.",
	},
	"biometrics": {
		"en": "To make sure you are the holder of the document.",
		"nl": "Om te controleren dat jij de houder van het document bent.",
	},
}

// reasonsFor is demoReasons for attributes.
func reasonsFor(attributes ...string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(attributes))
	for _, attribute := range attributes {
		out[attribute] = maps.Clone(demoReasons[attribute])
	}
	return out
}

// chipAndFaceAttributes and faceOnlyAttributes are what the engine releases for
// chipAndFaceSteps and faceOnlySteps: a flow that lists none releases
// everything its steps collect.
var (
	chipAndFaceAttributes = []string{"dg1", "document_image", "dg11", "dg2", "chip_checks", "selfie", "biometrics"}
	faceOnlyAttributes    = []string{"selfie", "biometrics"}
)

// demoRetentionDays and shortRetentionDays are how long a demo org's flows
// keep a session's personal data: most orgs', and CM's minimum for a ticket's
// age check. The org's own retention is a fixed default, so each flow sets it.
const (
	demoRetentionDays  = 30
	shortRetentionDays = 7
)

// cmAgeOver is the age a CM ticket buyer must be.
const cmAgeOver = 18

// day turns a retention in days into the flow's duration.
const day = 24 * time.Hour

// chipAndFace is an eIDAS substantial flow: the document's chip read in the
// Idem app (passport, identity card or Dutch driving licence) and a live face
// matched against its portrait by Regula.
func chipAndFace(name string, basis privacy.LegalBasis, purpose string) flow.FlowDefinition {
	return flow.FlowDefinition{
		Name: name, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
		FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
		RequiredAssuranceLevel: flow.AssuranceLevelSubstantial, LegalBasis: basis, ProcessingPurpose: purpose,
		AttributeReasons: reasonsFor(chipAndFaceAttributes...),
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
	flowOdixusOnboarding = "Nieuwe klant met woonadres"
)

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
		theme: radboudTheme,
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
				AttributeReasons: reasonsFor(faceOnlyAttributes...),
			}},
		},
		theme: asrTheme,
	},
	{
		slug: unibetSlug, defaultFlow: flowUnibetOnboarding, retentionDays: demoRetentionDays,
		flows: []demoProofingFlow{
			// A new player under the AMLR: full identification and liveness,
			// then their occupational status from the UWV Verzekeringsbericht.
			{
				def:        chipAndFace(flowUnibetOnboarding, privacy.LegalBasisLegalObligation, "Klantonderzoek kansspelen (AMLR/Wwft)"),
				employment: proofing.EmploymentRequired,
			},
		},
		theme: unibetTheme,
	},
	{
		slug: cmSlug, defaultFlow: flowCMAgeCheck, retentionDays: shortRetentionDays,
		flows: []demoProofingFlow{
			// Buying a ticket: a real, live person of age. Their name, whether
			// they are of age and the checks come back, never their date of
			// birth or an image.
			{def: flow.FlowDefinition{
				Name: flowCMAgeCheck, Steps: chipAndFaceSteps, SelfieLocation: flow.LocationNative,
				FaceProvider: flow.FaceProviderRegula, RequiredChecks: substantialChecks,
				RequestedAttributes: cmAttributes, AgeOver: cmAgeOver, AttributeReasons: cmReasons,
				LegalBasis: privacy.LegalBasisContract, ProcessingPurpose: "Leeftijdscontrole bij aankoop ticket",
			}},
		},
		theme: cmTheme,
	},
	{
		slug: odixusSlug, defaultFlow: flowOdixusOnboarding, retentionDays: demoRetentionDays,
		flows: []demoProofingFlow{
			// A new client identifies themselves, then enters the postal
			// address they live at.
			{def: chipAndFace(flowOdixusOnboarding, privacy.LegalBasisContract, "Onboarding nieuwe klant"), address: proofing.AddressRequired},
		},
		theme: odixusTheme,
	},
}

// seededFlows is o's own flows, each keeping a session's personal data for o's
// retentionDays unless it sets its own.
func (o demoProofingOrg) seededFlows() []demoProofingFlow {
	flows := slices.Clone(o.flows)
	for i := range flows {
		if flows[i].def.RetentionOverride == 0 {
			flows[i].def.RetentionOverride = time.Duration(o.retentionDays) * day
		}
	}
	return flows
}

// seedProofing gives each demo proofing org, found in orgsBySlug, its flows
// and default flow.
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

// seedProofingOrg gives orgID o's flows and default flow. An org that has a
// default flow already keeps it.
func seedProofingOrg(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, o demoProofingOrg) error {
	recorder := audit.NewDBRecorder()
	flowIDs, err := ensureProofingFlows(ctx, pool, recorder, orgID, o.seededFlows())
	if err != nil {
		return err
	}
	if err := ensureDemoTheme(ctx, themesettings.NewStore(pool, recorder), orgID, o); err != nil {
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
	for _, o := range demoOrganizations {
		if !slices.ContainsFunc(demoProofingOrgs, func(p demoProofingOrg) bool { return p.slug == o.slug }) {
			continue
		}
		org, err := ensureDemoProofingOrg(ctx, pool, users, orgs, o, addressDomain)
		if err != nil {
			return err
		}
		orgsBySlug[o.slug] = org
	}

	if err := seedProofing(ctx, pool, orgsBySlug); err != nil {
		return err
	}
	slog.Info("ensured identity proofing demo", slog.Int("orgs", len(orgsBySlug)))
	return nil
}

// ensureDemoProofingOrg ensures demo proofing org o, under its current name,
// with the Yivi team as its admins.
func ensureDemoProofingOrg(ctx context.Context, pool *pgxpool.Pool, users *user.Store, orgs *organization.Store, o demoOrganization, addressDomain string) (organization.Organization, error) {
	if err := requireSeedOwned(ctx, orgs, o); err != nil {
		return organization.Organization{}, err
	}
	org, err := ensureOrg(ctx, pool, o, addressDomain)
	if err != nil {
		return organization.Organization{}, err
	}

	// requireSeedOwned proved it the seed's: an older seed's name (one
	// without "(Demo)") follows the current one.
	if org.Name != o.name {
		if _, err := pool.Exec(ctx, `UPDATE organizations SET name = $2 WHERE id = $1`, org.ID, o.name); err != nil {
			return organization.Organization{}, fmt.Errorf("seed: rename org %q: %w", o.slug, err)
		}
		org.Name = o.name
	}

	for _, m := range yiviTeam {
		if err := ensureTeamMember(ctx, users, orgs, org.ID, m); err != nil {
			return organization.Organization{}, err
		}
	}
	return org, nil
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

// ensureProofingFlows creates each of want that orgID lacks, with its diploma
// and address settings, and returns every one's id by name.
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

// createProofingFlow saves f for orgID with its diploma, address and employment
// settings in one transaction, so a re-run never finds the flow without them.
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
	if f.employment != "" {
		if _, err := proofing.NewFlowEmploymentStore(tx, recorder).Save(ctx, orgID, saved.ID, f.employment); err != nil {
			return "", fmt.Errorf("seed: flow %s employment: %w", f.def.Name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("seed: flow %s: %w", f.def.Name, err)
	}
	return saved.ID, nil
}
