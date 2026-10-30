package seed

import (
	"context"
	_ "embed" // the court's and the office's logos
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/openid4vprequester"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/themesettings"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// The beschermingsbewind (protective administration) demo: who mandates whom
// when a professional bewindvoerder acts for a client towards a municipality.
//
//	Rechtbank Gelderland ──(beschikking, held by the office)──▶ Waalstad Bewindvoering
//	Waalstad: Dibran Mulder (bestuurder, legal representative)
//	   └─ full mandate ─▶ Fatima El Amrani (teamleider, org-wide)
//	        ├─ full mandate ─▶ Joris Peters (Team Nijmegen-Oost)
//	        └─ full mandate ─▶ Lotte de Graaf (Team Nijmegen-West)
//	Gemeente Nijmegen ──(toekenning bijzondere bijstand, held by the office)──▶ Waalstad
//
// The court appoints the office by court order, not the client by power of
// attorney, so the appointment is an organization-subject credential the
// court's wallet issues to the office's. The office's own mandate register
// carries authority down to the employees who do the work, and the office
// issues each of them a natural-person credential so a counterparty can see who
// acted. Gemeente Nijmegen is the existing tenant; the court and the office are
// demo orgs.
const (
	bewindOfficeSlug = "waalstad-bewind"
	bewindCourtSlug  = "rechtbank-gelderland"
	nijmegenSlug     = "nijmegen"
)

// The office's teams, one per client portfolio. A department-scoped mandate is
// the closest the mandate model gets to "these clients only".
const (
	bewindTeamEast = "Team Nijmegen-Oost"
	bewindTeamWest = "Team Nijmegen-West"
)

// bewindDirectorEmail is the office's legal representative: the Yivi team
// member whose name the office's register entry carries, so the presenter of
// the demo is the director who grants and revokes.
const bewindDirectorEmail = "d.mulder@yivi.app"

// The court's and the office's marks: our own drawings (assets/README.md).
var (
	//go:embed assets/rechtbank-logo.svg
	rechtbankLogoSVG []byte
	//go:embed assets/waalstad-logo.svg
	waalstadLogoSVG []byte
)

// The court's and the office's colours.
const (
	courtNavy  = "#1B2A41"
	officeTeal = "#0B6E5B"
)

// The court and the office get clearly different chrome, so a screen recording
// shows at a glance whose wallet is on screen: the court in navy and gold, the
// office in teal. Gemeente Nijmegen keeps whatever theme it has.
var (
	bewindCourtTheme = &demoTheme{
		settings: themesettings.SettingsInput{
			PrimaryColor: courtNavy, AccentColor: "#8A6A1F", TextColor: courtNavy, SurfaceColor: "#F6F4EE",
			BorderColor: "#DAD4C3", LinkColor: courtNavy, SidebarColor: courtNavy, TopbarColor: courtNavy,
			FontFamily: fontSystemUI,
		},
		logo: rechtbankLogoSVG,
	}
	bewindOfficeTheme = &demoTheme{
		settings: themesettings.SettingsInput{
			PrimaryColor: officeTeal, AccentColor: officeTeal, TextColor: "#14302A", SurfaceColor: "#F2F8F6",
			BorderColor: "#CFE3DD", LinkColor: officeTeal, SidebarColor: "#0B4F45", TopbarColor: officeTeal,
			FontFamily: fontOpenSans,
		},
		logo: waalstadLogoSVG,
	}
)

// bewindStaffMember is a fictional office employee: their membership and the
// mandate the team leader (or, for the team leader, the director) grants them.
type bewindStaffMember struct {
	user       demoUser
	role       string
	jobTitle   string
	department string // empty: org-wide
}

const bewindTeamLeaderEmail = "fatima.elamrani@example.test"

var bewindStaff = []bewindStaffMember{
	{
		user: demoUser{email: bewindTeamLeaderEmail, givenNames: "Fatima", lastName: "El Amrani"},
		role: organization.RoleAdmin, jobTitle: "Teamleider bewindvoering",
	},
	{
		user: demoUser{email: "joris.peters@example.test", givenNames: "Joris", lastName: "Peters"},
		role: organization.RoleMember, jobTitle: "Bewindvoerder", department: bewindTeamEast,
	},
	{
		user: demoUser{email: "lotte.degraaf@example.test", givenNames: "Lotte", lastName: "de Graaf"},
		role: organization.RoleMember, jobTitle: "Bewindvoerder", department: bewindTeamWest,
	},
}

// bewindCredential is one schema of the demo with the template the issue
// wizard starts from.
type bewindCredential struct {
	schema   attestation.Schema
	template attestation.Template
}

// The demo's credential names: each one's Dutch display name and its template's.
const (
	beschikkingName = "Beschikking beschermingsbewind"
	toekenningName  = "Toekenning bijzondere bijstand bewindskosten"
	medewerkerName  = "Medewerker beschermingsbewind"
)

// Attribute keys the templates pre-fill, shared by the court's and the
// municipality's schemas.
const (
	attrBewindvoerderNaam = "bewindvoerder_naam"
	attrBewindvoerderKVK  = "bewindvoerder_kvk"
)

// attr is one attribute with its English label (also its default label) and
// its Dutch one.
func attr(key, typ, en, nl string, required bool) attestation.AttributeDef {
	return attestation.AttributeDef{
		Key: key, Label: en, Type: typ, Required: required,
		Display: []attestation.LocalizedLabel{{Lang: "en", Label: en}, {Lang: "nl", Label: nl}},
	}
}

// The client and the office, as both the court's beschikking and Nijmegen's
// decision name them.
var (
	attrClientName = attr("rechthebbende_naam", attestation.AttributeTypeString, "Client name", "Naam rechthebbende", true)
	attrClientBSN  = attr("rechthebbende_bsn", attestation.AttributeTypeString, "Client BSN", "BSN rechthebbende", true)
	attrOfficeName = attr(attrBewindvoerderNaam, attestation.AttributeTypeString, "Administrator", "Bewindvoerder", true)
	attrOfficeKVK  = attr(attrBewindvoerderKVK, attestation.AttributeTypeString, "Administrator KVK number", "KVK-nummer bewindvoerder", true)
)

// bewindOfficeSources pre-fills the office's name and KVK number from the
// recipient organization in the issue wizard.
var bewindOfficeSources = map[string]string{
	attrBewindvoerderNaam: attestation.SourceOrgName,
	attrBewindvoerderKVK:  attestation.SourceOrgKVKNumber,
}

// bewindCourtCredential is the beschikking beschermingsbewind: the court's
// appointment of the office as bewindvoerder for one client (art. 1:431 BW).
// Revoking it is the court ending or transferring the bewind.
var bewindCourtCredential = bewindCredential{
	schema: attestation.Schema{
		VCT:                "nl.rechtspraak.beschermingsbewind",
		DisplayName:        beschikkingName,
		CredentialConfigID: "RechtbankBeschermingsbewindSdJwt",
		SubjectType:        attestation.SubjectOrganization,
		Display: []attestation.LocalizedName{
			{Lang: "en", Name: "Protective administration order"},
			{Lang: "nl", Name: beschikkingName},
		},
		Attributes: []attestation.AttributeDef{
			attr("zaaknummer", attestation.AttributeTypeString, "Case number", "Zaaknummer", true),
			attr("beschikkingsdatum", attestation.AttributeTypeDate, "Date of order", "Datum beschikking", true),
			attr("kantongerecht", attestation.AttributeTypeString, "Court", "Kantongerecht", true),
			attrClientName,
			attr("rechthebbende_geboortedatum", attestation.AttributeTypeDate, "Client date of birth", "Geboortedatum rechthebbende", true),
			attrClientBSN,
			attrOfficeName,
			attrOfficeKVK,
			attr("omvang", attestation.AttributeTypeString, "Scope", "Omvang bewind", true),
			attr("ingangsdatum", attestation.AttributeTypeDate, "Effective from", "Ingangsdatum", true),
		},
	},
	template: attestation.Template{
		Name: beschikkingName,
		DefaultAttributes: map[string]string{
			"kantongerecht": "Rechtbank Gelderland, locatie Nijmegen",
			"omvang":        "Alle goederen die (zullen) toebehoren aan de rechthebbende",
		},
		AttributeSources: bewindOfficeSources,
	},
}

// bewindNijmegenCredential is Gemeente Nijmegen's decision to pay a client's
// bewind fees from bijzondere bijstand (art. 35 Participatiewet), issued to the
// office that asked for it on the client's behalf.
var bewindNijmegenCredential = bewindCredential{
	schema: attestation.Schema{
		VCT:                "nl.nijmegen.bijzondere-bijstand.bewindskosten",
		DisplayName:        toekenningName,
		CredentialConfigID: "NijmegenBijzondereBijstandBewindskostenSdJwt",
		SubjectType:        attestation.SubjectOrganization,
		Display: []attestation.LocalizedName{
			{Lang: "en", Name: "Special assistance grant for administration fees"},
			{Lang: "nl", Name: toekenningName},
		},
		Attributes: []attestation.AttributeDef{
			attr("besluitnummer", attestation.AttributeTypeString, "Decision number", "Besluitnummer", true),
			attr("besluitdatum", attestation.AttributeTypeDate, "Decision date", "Besluitdatum", true),
			attr("regeling", attestation.AttributeTypeString, "Scheme", "Regeling", true),
			attrClientName,
			attrClientBSN,
			attrOfficeName,
			attrOfficeKVK,
			attr("bedrag_per_maand", attestation.AttributeTypeNumber, "Amount per month (EUR)", "Bedrag per maand (EUR)", false),
			attr("geldig_van", attestation.AttributeTypeDate, "Valid from", "Geldig van", true),
			attr("geldig_tot", attestation.AttributeTypeDate, "Valid until", "Geldig tot", false),
		},
	},
	template: attestation.Template{
		Name: toekenningName,
		DefaultAttributes: map[string]string{
			"regeling": "Bijzondere bijstand kosten beschermingsbewind (art. 35 Participatiewet)",
		},
		AttributeSources: bewindOfficeSources,
	},
}

// bewindOfficeCredential is the office's statement that a person works for it
// as a bewindvoerder, issued to their own Yivi app. Mandates are internal
// authorisation and never leave the wallet, so this is what lets a
// municipality see which employee acted, next to the office's beschikking.
var bewindOfficeCredential = bewindCredential{
	schema: attestation.Schema{
		VCT:                "nl.waalstad.bewindvoering.medewerker",
		DisplayName:        medewerkerName,
		CredentialConfigID: "WaalstadMedewerkerBewindvoeringSdJwt",
		SubjectType:        attestation.SubjectNaturalPerson,
		Display: []attestation.LocalizedName{
			{Lang: "en", Name: "Protective administration officer"},
			{Lang: "nl", Name: medewerkerName},
		},
		Attributes: []attestation.AttributeDef{
			attr("naam", attestation.AttributeTypeString, "Full name", "Volledige naam", true),
			attr("functie", attestation.AttributeTypeString, "Role", "Functie", false),
			attr("team", attestation.AttributeTypeString, "Team", "Team", false),
			attr("kantoor_naam", attestation.AttributeTypeString, "Office", "Bewindvoeringskantoor", true),
			attr("kantoor_kvk", attestation.AttributeTypeString, "Office KVK number", "KVK-nummer kantoor", true),
		},
	},
	template: attestation.Template{
		Name: medewerkerName,
		DefaultAttributes: map[string]string{
			"kantoor_naam": bewindOfficeOrg.name,
			"kantoor_kvk":  bewindOfficeOrg.kvkNumber,
		},
		AttributeSources: map[string]string{
			"naam":    attestation.SourceMemberFullName,
			"functie": attestation.SourceMemberJobTitle,
			"team":    attestation.SourceMemberDepartment,
		},
	},
}

// bewindRequestTemplate is what Gemeente Nijmegen asks a bewind office for
// before paying the fees: just enough of the beschikking to tie the office to
// its resident and show the bewind is in force and covers their finances.
var bewindRequestTemplate = openid4vprequester.NewTemplate{
	Name: beschikkingName,
	Credentials: []openid4vprequester.CredentialRequest{{
		VCT:    bewindCourtCredential.schema.VCT,
		Claims: []string{"zaaknummer", attrClientBSN.Key, attrBewindvoerderKVK, "omvang", "ingangsdatum"},
	}},
}

// bewindCredentials lists every credential of the demo, for the well-formedness
// test.
var bewindCredentials = []bewindCredential{bewindCourtCredential, bewindNijmegenCredential, bewindOfficeCredential}

// EnsureBewindDemo provisions the beschermingsbewind demo on its own: the court
// and the office (refusing a slug the seed does not own), Gemeente Nijmegen as
// it is (created bare if absent, never given a representative), and everything
// seedBewindDemo adds. Like the proofing demo it names a real institution, so
// it is staging only. Idempotent.
func EnsureBewindDemo(ctx context.Context, dsn, addressDomain string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: connect: %w", err)
	}
	defer pool.Close()

	users := user.NewStore(pool)
	orgs := organization.NewStore(pool, audit.NewDBRecorder())

	orgsBySlug := map[string]organization.Organization{}
	for _, o := range demoOrganizations {
		if o.slug != bewindOfficeSlug && o.slug != bewindCourtSlug {
			continue
		}
		if err := requireSeedOwned(ctx, orgs, o); err != nil {
			return err
		}
		org, err := ensureOrg(ctx, pool, o, addressDomain)
		if err != nil {
			return err
		}
		orgsBySlug[o.slug] = org
	}

	// Nijmegen is a partner tenant on staging: ensure it as the partner seed
	// does, without a representative.
	i := slices.IndexFunc(partnerOrganizations, func(p partnerOrganization) bool { return p.org.slug == nijmegenSlug })
	nijmegen, err := ensureOrg(ctx, pool, partnerOrganizations[i].org, addressDomain)
	if err != nil {
		return err
	}
	orgsBySlug[nijmegenSlug] = nijmegen

	return seedBewindDemo(ctx, pool, users, orgs, orgsBySlug)
}

// seedBewindDemo adds the demo to the three orgs in orgsBySlug: the Yivi team
// as admins of the court and the office, the director's claimed
// representation, the office's teams, staff and mandate chain, the court's and
// the office's themes, Nijmegen's request template, each org's
// schema + template and issuer settings, and the address book entries the
// issue and request flows pick recipients from. Idempotent.
func seedBewindDemo(ctx context.Context, pool *pgxpool.Pool, users *user.Store, orgs *organization.Store, orgsBySlug map[string]organization.Organization) error {
	office, court, nijmegen := orgsBySlug[bewindOfficeSlug], orgsBySlug[bewindCourtSlug], orgsBySlug[nijmegenSlug]

	for _, orgID := range []uuid.UUID{office.ID, court.ID} {
		for _, m := range yiviTeam {
			if err := ensureTeamMember(ctx, users, orgs, orgID, m); err != nil {
				return err
			}
		}
	}

	director, err := users.FindByEmail(ctx, user.Email(bewindDirectorEmail))
	if err != nil {
		return fmt.Errorf("seed: find bewind director: %w", err)
	}
	if err := claimRepresentation(ctx, pool, office.ID, director.ID); err != nil {
		return err
	}

	if err := seedBewindStaff(ctx, users, orgs, office.ID, director.ID); err != nil {
		return err
	}

	themes := themesettings.NewStore(pool, audit.NewDBRecorder())
	if err := ensureDemoTheme(ctx, themes, court.ID, court.Slug, bewindCourtTheme); err != nil {
		return err
	}
	if err := ensureDemoTheme(ctx, themes, office.ID, office.Slug, bewindOfficeTheme); err != nil {
		return err
	}

	store := attestation.NewStore(pool, audit.NewDBRecorder())
	for _, c := range []struct {
		org        organization.Organization
		credential bewindCredential
		instance   string
	}{
		{court, bewindCourtCredential, bewindCourtSlug},
		{nijmegen, bewindNijmegenCredential, nijmegenSlug},
		{office, bewindOfficeCredential, bewindOfficeSlug},
	} {
		if err := ensureSchemaTemplate(ctx, store, c.org.ID, c.credential.schema, c.credential.template); err != nil {
			return err
		}
		if err := seedIssuerSettings(ctx, pool, c.org.ID, c.instance, c.org.Name); err != nil {
			return err
		}
	}

	if err := ensureRequestTemplate(ctx, openid4vprequester.NewStore(pool, audit.NewDBRecorder()), nijmegen.ID, bewindRequestTemplate); err != nil {
		return err
	}

	// The office deals with both; each of them only with the office.
	for _, pair := range [][2]organization.Organization{{office, court}, {office, nijmegen}, {court, office}, {nijmegen, office}} {
		if err := ensureQerdsContact(ctx, pool, pair[0].ID, pair[1]); err != nil {
			return err
		}
	}

	slog.Info("ensured beschermingsbewind demo",
		slog.String("office", office.Slug), slog.String("court", court.Slug), slog.String("municipality", nijmegen.Slug))
	return nil
}

// ensureBewindPresenter makes the demo's presenter (the office's director) an
// admin of orgID with its bestuurder entry claimed, so they can act for that
// org in the demo as well.
func ensureBewindPresenter(ctx context.Context, users *user.Store, orgs *organization.Store, pool *pgxpool.Pool, orgID uuid.UUID) error {
	presenter, err := users.FindByEmail(ctx, user.Email(bewindDirectorEmail))
	if err != nil {
		return fmt.Errorf("seed: find bewind presenter: %w", err)
	}
	if err := ensureMembership(ctx, orgs, orgID, presenter.ID, organization.RoleAdmin, "", nil); err != nil {
		return err
	}
	return claimRepresentation(ctx, pool, orgID, presenter.ID)
}

// claimRepresentation marks the office's bestuurder as claimed by userID, which
// makes them its legal representative: the root the mandate chain is granted
// from. A wallet opened through the register flow does this when the requester
// is the representative; the seeded org never went through that flow.
func claimRepresentation(ctx context.Context, pool *pgxpool.Pool, orgID, userID uuid.UUID) error {
	if _, err := pool.Exec(ctx, `
		UPDATE wallet_representations SET claimed_by_user_id = $2, claimed_at = now()
		WHERE organization_id = $1 AND kind = 'bestuurder' AND claimed_by_user_id IS NULL`,
		orgID, userID); err != nil {
		return fmt.Errorf("seed: claim representation: %w", err)
	}
	return nil
}

// seedBewindStaff adds the office's teams and staff, and grants the mandate
// chain: the director gives the team leader an org-wide full mandate, and she
// delegates a full mandate confined to their team to each bewindvoerder. The
// grants run through the store, so they are audited and clamped like any other.
// Skipped once the office has granted anything, so a re-run never stacks a
// second chain on a register the demo may have revoked from.
func seedBewindStaff(ctx context.Context, users *user.Store, orgs *organization.Store, officeID, directorID uuid.UUID) error {
	teams := map[string]uuid.UUID{}
	for _, name := range []string{bewindTeamEast, bewindTeamWest} {
		dept, err := ensureDepartment(ctx, orgs, officeID, name)
		if err != nil {
			return err
		}
		teams[name] = dept.ID
	}

	staffIDs := map[string]uuid.UUID{}
	for _, s := range bewindStaff {
		u, err := ensureUser(ctx, users, s.user.email, s.user.givenNames, s.user.lastName, s.user.preferredName)
		if err != nil {
			return err
		}
		var deptID *uuid.UUID
		if id, ok := teams[s.department]; ok {
			deptID = &id
		}
		if err := ensureMembership(ctx, orgs, officeID, u.ID, s.role, s.jobTitle, deptID); err != nil {
			return err
		}
		staffIDs[s.user.email] = u.ID
	}

	granted, err := orgs.ListMandates(ctx, officeID)
	if err != nil {
		return fmt.Errorf("seed: list bewind mandates: %w", err)
	}
	if len(granted) > 0 {
		return nil
	}

	now := time.Now()
	leaderID := staffIDs[bewindTeamLeaderEmail]
	root, err := orgs.GrantMandate(audit.ContextWithActor(ctx, audit.Actor{UserID: directorID}), officeID, directorID, organization.MandateGrant{
		Type: organization.MandateFull, GranteeUserID: leaderID, Scope: organization.MandateScopeOrganization, ValidFrom: now,
	})
	if err != nil {
		return fmt.Errorf("seed: grant team leader mandate: %w", err)
	}
	for _, s := range bewindStaff {
		if s.department == "" {
			continue
		}
		teamID := teams[s.department]
		if _, err := orgs.GrantMandate(audit.ContextWithActor(ctx, audit.Actor{UserID: leaderID}), officeID, leaderID, organization.MandateGrant{
			Type: organization.MandateFull, GranteeUserID: staffIDs[s.user.email],
			Scope: organization.MandateScopeDepartment, ScopeDepartmentID: &teamID, ValidFrom: now,
			// Named, not looked up: the lookup asks the database clock whether
			// the root is active yet, and it was stamped with this process's.
			ParentMandateID: &root.ID,
		}); err != nil {
			return fmt.Errorf("seed: grant %s mandate: %w", s.user.email, err)
		}
	}
	return nil
}

// ensureSchemaTemplate creates schema with template over it unless orgID
// already has a schema with that VCT. The guard is per VCT, not per org, so a
// demo can add a schema to a tenant whose catalogue another seed already
// started.
func ensureSchemaTemplate(ctx context.Context, store *attestation.Store, orgID uuid.UUID, schema attestation.Schema, template attestation.Template) error {
	existing, err := store.ListSchemas(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: list attestation schemas: %w", err)
	}
	if slices.ContainsFunc(existing, func(s attestation.Schema) bool { return s.VCT == schema.VCT }) {
		return nil
	}
	created, err := store.CreateSchema(ctx, orgID, schema)
	if err != nil {
		return fmt.Errorf("seed: create attestation schema %q: %w", schema.VCT, err)
	}
	template.SchemaID = created.ID
	if _, err := store.CreateTemplate(ctx, orgID, template); err != nil {
		return fmt.Errorf("seed: create attestation template %q: %w", template.Name, err)
	}
	return nil
}

// ensureRequestTemplate saves template for orgID unless the org already has
// one by that name, so a template changed since is kept.
func ensureRequestTemplate(ctx context.Context, store *openid4vprequester.Store, orgID uuid.UUID, template openid4vprequester.NewTemplate) error {
	existing, err := store.ListTemplates(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: list request templates: %w", err)
	}
	if slices.ContainsFunc(existing, func(t openid4vprequester.Template) bool { return t.Name == template.Name }) {
		return nil
	}
	template.OrganizationID = orgID
	if _, err := store.CreateTemplate(ctx, template); err != nil {
		return fmt.Errorf("seed: create request template %q: %w", template.Name, err)
	}
	return nil
}

// ensureQerdsContact puts contact in orgID's QERDS address book under its name.
func ensureQerdsContact(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID, contact organization.Organization) error {
	if _, err := pool.Exec(ctx, `
		INSERT INTO qerds_contacts (organization_id, name, address) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, address) DO NOTHING`,
		orgID, contact.Name, contact.DigitalAddress); err != nil {
		return fmt.Errorf("seed: qerds contact %q: %w", contact.Slug, err)
	}
	return nil
}
