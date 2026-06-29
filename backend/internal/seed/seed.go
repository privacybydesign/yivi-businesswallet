package seed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// fakerSeed is fixed so generated demo data is identical on every run, which
// keeps the seeder reproducible and idempotent.
const fakerSeed = 1

const (
	demoOrgSlug      = "yivi"
	demoInviterEmail = "user@yivi.app"
	fakerMemberCount = 15
	fakerInviteCount = 3
	auditEventCount  = 60
)

type demoOrganization struct {
	name string
	slug string
}

type demoUser struct {
	email         string
	preferredName string
	givenNames    string
	namePrefix    string
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
	{name: "Yivi", slug: "yivi"},
	{name: "Firsty.app", slug: "firsty"},
	{name: "Radboud Universiteit", slug: "radboud-universiteit"},
}

var demoUsers = []demoUser{
	{email: "admin@yivi.app", givenNames: "Johannes Hendrik", preferredName: "Jan", lastName: "Janssen"},
	{email: "user@yivi.app", givenNames: "Thijs Adriaan", namePrefix: "de", lastName: "Vries"},
}

var demoDepartments = []demoDepartment{
	{slug: "yivi", name: "Engineering"},
	{slug: "yivi", name: "Operations"},
	{slug: "firsty", name: "Sales"},
}

var demoMemberships = []demoMembership{
	{email: "user@yivi.app", slug: "yivi", role: "admin", jobTitle: "Chief Technology Officer", department: "Engineering"},
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
		org, err := ensureOrg(ctx, orgs, o.name, o.slug)
		if err != nil {
			return err
		}
		orgsBySlug[o.slug] = org
	}

	usersByEmail := map[string]user.User{}
	for _, u := range demoUsers {
		seeded, err := ensureUser(ctx, users, u.email, u.givenNames, u.namePrefix, u.lastName, u.preferredName)
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
	demoDeptIDs := []uuid.UUID{
		deptsByOrgName[demoOrgSlug+"/Engineering"].ID,
		deptsByOrgName[demoOrgSlug+"/Operations"].ID,
	}

	if err := seedFakerMembers(ctx, faker, users, orgs, demoOrg.ID, demoDeptIDs); err != nil {
		return err
	}

	if err := seedInvitations(ctx, faker, orgs, demoOrg.ID, usersByEmail[demoInviterEmail].ID, demoDeptIDs[0]); err != nil {
		return err
	}

	if err := seedAuditEvents(ctx, pool, faker, demoOrg.ID, usersByEmail[demoInviterEmail].ID); err != nil {
		return err
	}

	return nil
}

func ensureOrg(ctx context.Context, orgs *organization.Store, name, slug string) (organization.Organization, error) {
	if org, err := orgs.GetBySlug(ctx, slug); err == nil {
		return org, nil
	} else if !errors.Is(err, organization.ErrNotFound) {
		return organization.Organization{}, fmt.Errorf("seed: lookup org %q: %w", slug, err)
	}
	org, err := orgs.Create(ctx, name, slug)
	if err != nil {
		return organization.Organization{}, fmt.Errorf("seed: create org %q: %w", slug, err)
	}
	return org, nil
}

func ensureUser(ctx context.Context, users *user.Store, email, givenNames, namePrefix, lastName, preferredName string) (user.User, error) {
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
		NamePrefix:    optional(namePrefix),
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

func seedFakerMembers(ctx context.Context, faker *gofakeit.Faker, users *user.Store, orgs *organization.Store, orgID uuid.UUID, deptIDs []uuid.UUID) error {
	for i := 0; i < fakerMemberCount; i++ {
		first, last := faker.FirstName(), faker.LastName()
		email := fmt.Sprintf("%s.%s%d@example.test", slugify(first), slugify(last), i)

		u, err := ensureUser(ctx, users, email, first, "", last, "")
		if err != nil {
			return err
		}

		role := organization.RoleMember
		if i%6 == 0 {
			role = organization.RoleAdmin
		}
		// Round-robin the org's departments, leaving every (len+1)th unassigned.
		var deptID *uuid.UUID
		if slot := i % (len(deptIDs) + 1); slot < len(deptIDs) {
			deptID = &deptIDs[slot]
		}
		if err := ensureMembership(ctx, orgs, orgID, u.ID, role, faker.JobTitle(), deptID); err != nil {
			return err
		}
	}
	slog.Info("seeded faker members", slog.Int("count", fakerMemberCount))
	return nil
}

func seedInvitations(ctx context.Context, faker *gofakeit.Faker, orgs *organization.Store, orgID, invitedBy, deptID uuid.UUID) error {
	invitations := []organization.Invitation{
		{Email: "invited@yivi.app", Role: organization.RoleMember, JobTitle: optional("Software Engineer"), DepartmentID: &deptID, GivenNames: "Robin", LastName: "Bakker"},
	}
	for i := 0; i < fakerInviteCount; i++ {
		first, last := faker.FirstName(), faker.LastName()
		invitations = append(invitations, organization.Invitation{
			Email:      fmt.Sprintf("invite.%s.%s%d@example.test", slugify(first), slugify(last), i),
			Role:       organization.RoleMember,
			JobTitle:   optional(faker.JobTitle()),
			GivenNames: first,
			LastName:   last,
		})
	}

	var inserted int
	for _, inv := range invitations {
		inv.OrganizationID = orgID
		inv.InvitedBy = &invitedBy
		if _, err := orgs.CreateInvitation(ctx, inv); err != nil {
			if errors.Is(err, organization.ErrAlreadyInvited) {
				continue
			}
			return fmt.Errorf("seed: create invitation %q: %w", inv.Email, err)
		}
		inserted++
	}
	slog.Info("seeded invitations", slog.Int("inserted", inserted))
	return nil
}

// seedAuditEvents fabricates events on the demo org so the audit log's cursor
// pagination (page size 50) has more than one page. Guarded by request_id =
// 'seed' so re-running the seeder doesn't pile up duplicates.
func seedAuditEvents(ctx context.Context, pool *pgxpool.Pool, faker *gofakeit.Faker, orgID, actorID uuid.UUID) error {
	var existing int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE request_id = 'seed'`).Scan(&existing); err != nil {
		return fmt.Errorf("seed: count audit events: %w", err)
	}
	if existing > 0 {
		slog.Info("audit events already seeded", slog.Int("count", existing))
		return nil
	}

	const insert = `
		INSERT INTO audit_events (actor_user_id, organization_id, action, target_type, target_id, metadata, request_id, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'seed', $7)`
	for i := 0; i < auditEventCount; i++ {
		email := faker.Email()
		var action string
		var metadata map[string]any
		switch i % 3 {
		case 0:
			action = audit.MembershipInvited
			metadata = audit.Created(map[string]any{"email": email, "role": organization.RoleMember})
		case 1:
			action = audit.MembershipRoleChanged
			metadata = audit.Updated(map[string]any{"role": organization.RoleMember}, map[string]any{"role": organization.RoleAdmin})
		default:
			action = audit.MembershipRevoked
			metadata = audit.Deleted(map[string]any{"email": email, "role": organization.RoleMember})
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			return fmt.Errorf("seed: marshal audit metadata: %w", err)
		}
		occurredAt := time.Now().Add(-time.Duration(i) * time.Minute)
		if _, err := pool.Exec(ctx, insert, actorID, orgID, action, audit.TargetMembership, email, raw, occurredAt); err != nil {
			return fmt.Errorf("seed: insert audit event: %w", err)
		}
	}
	slog.Info("seeded audit events", slog.Int("inserted", auditEventCount))
	return nil
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
