package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/attestation"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakerSeed is fixed so generated demo data is identical on every run, which
// keeps the seeder reproducible and idempotent.
const fakerSeed = 1

const (
	demoOrgSlug      = "yivi"
	fakerMemberCount = 15
	inviteCount      = 35
	revokeCount      = 22
	roleChangeCount  = 8
)

// demoOrganization is a seeded business wallet: an org with KVK identity, a QERDS
// digital address and one representative. Demo KVK numbers (900000xx) are kept
// distinct from the live register-flow demo (94861412).
type demoOrganization struct {
	name      string // legal name
	slug      string
	kvkNumber string
	euid      string
	address   string
	repGiven  string
	repFamily string
	repKind   string
	repAuth   string
}

type demoUser struct {
	email         string
	preferredName string
	givenNames    string
	lastName      string
}

type demoDepartment struct {
	slug string
	name string
}

type demoMembership struct {
	email      string
	slug       string
	role       string
	jobTitle   string
	department string
}

// Anchor data: recognizable accounts/orgs that must stay stable so developers
// can log in predictably. Volume and variety are generated with the faker.
var demoOrganizations = []demoOrganization{
	{name: "Yivi B.V.", slug: "yivi", kvkNumber: "90000010", euid: "NL.KVK.90000010", address: "yivi@qerds.localhost", repGiven: "Johannes Hendrik", repFamily: "Janssen", repKind: "bestuurder", repAuth: "sole"},
	{name: "Firsty.app B.V.", slug: "firsty", kvkNumber: "90000020", euid: "NL.KVK.90000020", address: "firsty@qerds.localhost", repGiven: "Thijs Adriaan", repFamily: "de Vries", repKind: "bestuurder", repAuth: "jointly"},
	{name: "Radboud Universiteit", slug: "radboud-universiteit", kvkNumber: "90000030", euid: "NL.KVK.90000030", address: "radboud@qerds.localhost", repGiven: "Anke", repFamily: "Bakker", repKind: "gevolmachtigde", repAuth: "beperkt"},
}

var demoUsers = []demoUser{
	{email: "admin@yivi.app", givenNames: "Johannes Hendrik", preferredName: "Jan", lastName: "Janssen"},
	{email: "user@yivi.app", givenNames: "Thijs Adriaan", lastName: "de Vries"},
}

var demoDepartments = []demoDepartment{
	{slug: "yivi", name: "Engineering"},
	{slug: "yivi", name: "Operations"},
	{slug: "firsty", name: "Sales"},
}

var demoMemberships = []demoMembership{
	{email: "user@yivi.app", slug: "yivi", role: "admin", jobTitle: "Chief Technology Officer", department: "Engineering"},
	{email: "admin@yivi.app", slug: "yivi", role: "admin", jobTitle: "Chief Executive Officer", department: "Operations"},
	{email: "user@yivi.app", slug: "firsty", role: "member", jobTitle: "Account Manager", department: "Sales"},
}

func Run(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: connect: %w", err)
	}
	defer pool.Close()

	faker := gofakeit.New(fakerSeed)
	users := user.NewStore(pool)
	orgs := organization.NewStore(pool, audit.NewDBRecorder())

	orgsBySlug := map[string]organization.Organization{}
	for _, o := range demoOrganizations {
		org, err := ensureOrg(ctx, pool, o)
		if err != nil {
			return err
		}
		orgsBySlug[o.slug] = org
	}

	usersByEmail := map[string]user.User{}
	for _, u := range demoUsers {
		seeded, err := ensureUser(ctx, users, u.email, u.givenNames, u.lastName, u.preferredName)
		if err != nil {
			return err
		}
		usersByEmail[u.email] = seeded
	}

	deptsByOrgName := map[string]organization.Department{}
	for _, d := range demoDepartments {
		dept, err := ensureDepartment(ctx, orgs, orgsBySlug[d.slug].ID, d.name)
		if err != nil {
			return err
		}
		deptsByOrgName[d.slug+"/"+d.name] = dept
	}

	for _, m := range demoMemberships {
		var deptID *uuid.UUID
		if d, ok := deptsByOrgName[m.slug+"/"+m.department]; ok {
			deptID = &d.ID
		}
		if err := ensureMembership(ctx, orgs, orgsBySlug[m.slug].ID, usersByEmail[m.email].ID, m.role, m.jobTitle, deptID); err != nil {
			return err
		}
	}

	demoOrg := orgsBySlug[demoOrgSlug]
	if err := seedAttestations(ctx, pool, demoOrg.ID); err != nil {
		return err
	}
	if err := seedEmailSettings(ctx, pool, demoOrg.ID); err != nil {
		return err
	}
	if err := seedIssuerSettings(ctx, pool, demoOrg.ID); err != nil {
		return err
	}

	demoDeptIDs := []uuid.UUID{
		deptsByOrgName[demoOrgSlug+"/Engineering"].ID,
		deptsByOrgName[demoOrgSlug+"/Operations"].ID,
	}

	members, admins, err := seedFakerMembers(ctx, faker, users, orgs, demoOrg.ID, demoDeptIDs)
	if err != nil {
		return err
	}
	admins = append(admins, usersByEmail["user@yivi.app"].ID, usersByEmail["admin@yivi.app"].ID)

	// The activity is the audited history; guard on it so re-runs (which can't
	// recreate already-revoked invitations) don't pile up duplicate events.
	seeded, err := hasInvitedEvents(ctx, pool, demoOrg.ID)
	if err != nil {
		return err
	}
	if seeded {
		slog.Info("activity already seeded")
		return nil
	}
	if err := seedActivity(ctx, faker, orgs, demoOrg.ID, admins, members, demoDeptIDs); err != nil {
		return err
	}
	return spreadAuditTimestamps(ctx, pool, demoOrg.ID)
}

// ensureOrg creates the demo organization/business wallet (identity + default
// QERDS address + one representative). Idempotent: ON CONFLICT (slug) returns the
// existing row, and the address/representation inserts are guarded.
func ensureOrg(ctx context.Context, pool *pgxpool.Pool, o demoOrganization) (organization.Organization, error) {
	var org organization.Organization
	err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug
		RETURNING id, name, slug, kvk_number, euid, digital_address, status, bootstrapped_at`,
		o.name, o.slug, o.kvkNumber, o.euid, o.address).Scan(
		&org.ID, &org.Name, &org.Slug, &org.KVKNumber, &org.EUID, &org.DigitalAddress, &org.Status, &org.BootstrappedAt)
	if err != nil {
		return organization.Organization{}, fmt.Errorf("seed: ensure org %q: %w", o.slug, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO qerds_addresses (organization_id, address, is_default) VALUES ($1, $2, true)
		ON CONFLICT (address) DO NOTHING`, org.ID, o.address); err != nil {
		return organization.Organization{}, fmt.Errorf("seed: qerds address %q: %w", o.slug, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO wallet_representations (organization_id, kind, given_names, family_name, authority)
		SELECT $1, $2, $3, $4, $5
		WHERE NOT EXISTS (SELECT 1 FROM wallet_representations WHERE organization_id = $1)`,
		org.ID, o.repKind, o.repGiven, o.repFamily, o.repAuth); err != nil {
		return organization.Organization{}, fmt.Errorf("seed: representation %q: %w", o.slug, err)
	}
	return org, nil
}

func ensureUser(ctx context.Context, users *user.Store, email, givenNames, lastName, preferredName string) (user.User, error) {
	parsed, err := user.ParseEmail(email)
	if err != nil {
		return user.User{}, fmt.Errorf("seed: parse email %q: %w", email, err)
	}
	if existing, err := users.FindByEmail(ctx, parsed); err == nil {
		return existing, nil
	} else if !errors.Is(err, user.ErrNotFound) {
		return user.User{}, fmt.Errorf("seed: lookup user %q: %w", email, err)
	}
	created, err := users.Create(ctx, user.User{
		Email:         parsed,
		GivenNames:    givenNames,
		LastName:      lastName,
		PreferredName: optional(preferredName),
	})
	if err != nil {
		return user.User{}, fmt.Errorf("seed: create user %q: %w", email, err)
	}
	return created, nil
}

func ensureDepartment(ctx context.Context, orgs *organization.Store, orgID uuid.UUID, name string) (organization.Department, error) {
	list, err := orgs.ListDepartments(ctx, orgID)
	if err != nil {
		return organization.Department{}, fmt.Errorf("seed: list departments: %w", err)
	}
	for _, d := range list {
		if d.Name == name {
			return d, nil
		}
	}
	dept, err := orgs.CreateDepartment(ctx, orgID, name)
	if err != nil {
		return organization.Department{}, fmt.Errorf("seed: create department %q: %w", name, err)
	}
	return dept, nil
}

func ensureMembership(ctx context.Context, orgs *organization.Store, orgID, userID uuid.UUID, role, jobTitle string, deptID *uuid.UUID) error {
	_, err := orgs.AddMembership(ctx, orgID, userID, role, optional(jobTitle), deptID)
	if err != nil && !errors.Is(err, organization.ErrAlreadyMember) {
		return fmt.Errorf("seed: add membership: %w", err)
	}
	return nil
}

// seedFakerMembers provisions active members for the demo org and returns the
// member and admin user ids, so the activity below can attribute actions to a
// realistic spread of actors.
func seedFakerMembers(ctx context.Context, faker *gofakeit.Faker, users *user.Store, orgs *organization.Store, orgID uuid.UUID, deptIDs []uuid.UUID) (members, admins []uuid.UUID, err error) {
	for i := 0; i < fakerMemberCount; i++ {
		first, last := faker.FirstName(), faker.LastName()
		email := fmt.Sprintf("%s.%s%d@example.test", slugify(first), slugify(last), i)

		u, err := ensureUser(ctx, users, email, first, last, "")
		if err != nil {
			return nil, nil, err
		}

		role := organization.RoleMember
		if i%6 == 0 {
			role = organization.RoleAdmin
		}
		// Round-robin the departments, leaving every (len+1)th unassigned.
		var deptID *uuid.UUID
		if slot := i % (len(deptIDs) + 1); slot < len(deptIDs) {
			deptID = &deptIDs[slot]
		}
		if err := ensureMembership(ctx, orgs, orgID, u.ID, role, faker.JobTitle(), deptID); err != nil {
			return nil, nil, err
		}
		if role == organization.RoleAdmin {
			admins = append(admins, u.ID)
		} else {
			members = append(members, u.ID)
		}
	}
	slog.Info("seeded faker members", slog.Int("count", fakerMemberCount))
	return members, admins, nil
}

// seedActivity performs real, audited operations — invitations, revocations and
// role changes — each attributed to a random admin, so the audit log reflects
// genuine history (every entry corresponds to real data) with varied actors.
func seedActivity(ctx context.Context, faker *gofakeit.Faker, orgs *organization.Store, orgID uuid.UUID, admins, members, deptIDs []uuid.UUID) error {
	actorCtx := func(actor uuid.UUID) context.Context {
		return audit.ContextWithActor(ctx, audit.Actor{UserID: actor})
	}
	pickAdmin := func() uuid.UUID { return admins[faker.Number(0, len(admins)-1)] }
	pickDept := func() *uuid.UUID {
		if slot := faker.Number(0, len(deptIDs)); slot < len(deptIDs) {
			return &deptIDs[slot]
		}
		return nil
	}

	invitationIDs := make([]uuid.UUID, 0, inviteCount)
	for i := 0; i < inviteCount; i++ {
		first, last := faker.FirstName(), faker.LastName()
		inviter := pickAdmin()
		inv, err := orgs.CreateInvitation(actorCtx(inviter), organization.Invitation{
			OrganizationID: orgID,
			Email:          fmt.Sprintf("invite.%s.%s%d@example.test", slugify(first), slugify(last), i),
			InvitedBy:      &inviter,
			Role:           organization.RoleMember,
			JobTitle:       optional(faker.JobTitle()),
			DepartmentID:   pickDept(),
			GivenNames:     first,
			LastName:       last,
		})
		if err != nil {
			return fmt.Errorf("seed: invite: %w", err)
		}
		invitationIDs = append(invitationIDs, inv.ID)
	}

	// Revoke a subset — these become 'gone' history, correctly absent from the list.
	for i := 0; i < revokeCount && i < len(invitationIDs); i++ {
		if err := orgs.RevokeInvitation(actorCtx(pickAdmin()), orgID, invitationIDs[i]); err != nil {
			return fmt.Errorf("seed: revoke: %w", err)
		}
	}

	// A recognizable, always-pending invitation.
	anchorInviter := pickAdmin()
	if _, err := orgs.CreateInvitation(actorCtx(anchorInviter), organization.Invitation{
		OrganizationID: orgID,
		Email:          "invited@yivi.app",
		InvitedBy:      &anchorInviter,
		Role:           organization.RoleMember,
		JobTitle:       optional("Software Engineer"),
		DepartmentID:   &deptIDs[0],
		GivenNames:     "Robin",
		LastName:       "Bakker",
	}); err != nil && !errors.Is(err, organization.ErrAlreadyInvited) {
		return fmt.Errorf("seed: anchor invite: %w", err)
	}

	// Role changes on existing members (promote some, retitle others) — always
	// member->admin so the last-admin guard can never trip.
	for i := 0; i < roleChangeCount && i < len(members); i++ {
		role := organization.RoleMember
		if i%2 == 0 {
			role = organization.RoleAdmin
		}
		dept := deptIDs[faker.Number(0, len(deptIDs)-1)]
		if _, err := orgs.UpdateMembership(actorCtx(pickAdmin()), orgID, members[i], &role, optional(faker.JobTitle()), &dept); err != nil {
			return fmt.Errorf("seed: role change: %w", err)
		}
	}

	slog.Info("seeded activity",
		slog.Int("invited", inviteCount+1), slog.Int("revoked", revokeCount), slog.Int("roleChanged", roleChangeCount))
	return nil
}

// seedAttestations gives the demo org a couple of credential schemas + issuance
// templates matching the Attestations mockup. Idempotent: skips when the org
// already has schemas. The "Corporate e-mail" schema maps to the Veramo
// EmailCredentialSdJwt so it can issue end-to-end against the real hosted issuer.
func seedAttestations(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) error {
	store := attestation.NewStore(pool, audit.NewDBRecorder())

	existing, err := store.ListSchemas(ctx, orgID)
	if err != nil {
		return fmt.Errorf("seed: list attestation schemas: %w", err)
	}
	if len(existing) > 0 {
		return nil
	}

	demoSchemas := []struct {
		schema       attestation.Schema
		templateName string
	}{
		{
			schema: attestation.Schema{
				VCT:                "nl.yivi.email",
				DisplayName:        "Corporate e-mail",
				CredentialConfigID: "YiviEmailSdJwt",
				SubjectType:        attestation.SubjectNaturalPerson,
				Display: []attestation.LocalizedName{
					{Lang: "en", Name: "Corporate e-mail"},
					{Lang: "nl", Name: "Zakelijk e-mailadres"},
				},
				Attributes: []attestation.AttributeDef{
					{Key: "email", Label: "E-mail", Type: "string", Required: true, Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "E-mail"}, {Lang: "nl", Label: "E-mailadres"},
					}},
					{Key: "domain", Label: "Domain", Type: "string", Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "Domain"}, {Lang: "nl", Label: "Domein"},
					}},
				},
			},
			templateName: "Corporate e-mail",
		},
		{
			schema: attestation.Schema{
				VCT:                "nl.yivi.employee",
				DisplayName:        "Employee of Yivi B.V.",
				CredentialConfigID: "YiviEmployeeSdJwt",
				SubjectType:        attestation.SubjectNaturalPerson,
				Display: []attestation.LocalizedName{
					{Lang: "en", Name: "Employee of Yivi B.V."},
					{Lang: "nl", Name: "Medewerker van Yivi B.V."},
				},
				Attributes: []attestation.AttributeDef{
					{Key: "fullName", Label: "Full name", Type: "string", Required: true, Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "Full name"}, {Lang: "nl", Label: "Volledige naam"},
					}},
					{Key: "department", Label: "Department", Type: "string", Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "Department"}, {Lang: "nl", Label: "Afdeling"},
					}},
					{Key: "role", Label: "Role", Type: "string", Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "Role"}, {Lang: "nl", Label: "Functie"},
					}},
				},
			},
			templateName: "Employee of Yivi B.V.",
		},
		{
			// An organization-subject credential: delivered over QERDS to the
			// recipient business wallet's digital address (from the address book).
			schema: attestation.Schema{
				VCT:                "nl.yivi.supplier",
				DisplayName:        "Approved supplier",
				CredentialConfigID: "YiviSupplierSdJwt",
				SubjectType:        attestation.SubjectOrganization,
				Display: []attestation.LocalizedName{
					{Lang: "en", Name: "Approved supplier"},
					{Lang: "nl", Name: "Erkende leverancier"},
				},
				Attributes: []attestation.AttributeDef{
					{Key: "name", Label: "Legal name", Type: "string", Required: true, Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "Legal name"}, {Lang: "nl", Label: "Statutaire naam"},
					}},
					{Key: "kvkNumber", Label: "KVK number", Type: "string", Display: []attestation.LocalizedLabel{
						{Lang: "en", Label: "KVK number"}, {Lang: "nl", Label: "KVK-nummer"},
					}},
				},
			},
			templateName: "Approved supplier",
		},
	}

	for _, d := range demoSchemas {
		schema, err := store.CreateSchema(ctx, orgID, d.schema)
		if err != nil {
			return fmt.Errorf("seed: create attestation schema %q: %w", d.schema.VCT, err)
		}
		if _, err := store.CreateTemplate(ctx, orgID, attestation.Template{
			SchemaID: schema.ID,
			Name:     d.templateName,
		}); err != nil {
			return fmt.Errorf("seed: create attestation template %q: %w", d.templateName, err)
		}
	}

	slog.Info("seeded attestation schemas + templates", slog.Int("count", len(demoSchemas)))
	return nil
}

// seedEmailSettings points the demo org's SMTP at the dev Mailpit service (no
// auth), so person-facing credential-offer e-mails are captured and viewable at
// http://localhost:8025 out of the box. Idempotent.
func seedEmailSettings(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) error {
	const insert = `INSERT INTO org_email_settings (organization_id, host, port, from_name, from_address, enabled)
		VALUES ($1, 'mailpit', 1025, 'Yivi B.V.', 'noreply@yivi.app', true)
		ON CONFLICT (organization_id) DO NOTHING`
	if _, err := pool.Exec(ctx, insert, orgID); err != nil {
		return fmt.Errorf("seed: email settings: %w", err)
	}
	return nil
}

// seedIssuerSettings gives the demo org its own Veramo issuer instance ("yivi")
// with branding, so its attestations issue from a per-org issuer and the
// generated GitOps bundle (org settings → Issuer) is populated out of the box.
// Idempotent.
func seedIssuerSettings(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) error {
	const insert = `INSERT INTO org_issuer_settings (organization_id, instance_name, display_name, enabled)
		VALUES ($1, 'yivi', 'Yivi B.V.', true)
		ON CONFLICT (organization_id) DO NOTHING`
	if _, err := pool.Exec(ctx, insert, orgID); err != nil {
		return fmt.Errorf("seed: issuer settings: %w", err)
	}
	return nil
}

func hasInvitedEvents(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) (bool, error) {
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_events WHERE organization_id = $1 AND action = $2`,
		orgID, audit.MembershipInvited).Scan(&n); err != nil {
		return false, fmt.Errorf("seed: count invited events: %w", err)
	}
	return n > 0, nil
}

// spreadAuditTimestamps backdates the org's audit events across the last two
// weeks so the log reads like real history rather than one bulk import. setseed
// keeps random() deterministic; it must share the transaction with the update.
func spreadAuditTimestamps(ctx context.Context, pool *pgxpool.Pool, orgID uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("seed: spread timestamps: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT setseed(0.4242)`); err != nil {
		return fmt.Errorf("seed: setseed: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE audit_events SET occurred_at = now() - (random() * interval '14 days') WHERE organization_id = $1`,
		orgID); err != nil {
		return fmt.Errorf("seed: spread timestamps: %w", err)
	}
	return tx.Commit(ctx)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// slugify reduces a name to lowercase ASCII letters so it is safe in an email
// local-part (faker names may carry apostrophes, hyphens or accents).
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
