package seed

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Run(ctx context.Context, dsn string) error {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("seed: connect: %w", err)
	}
	defer pool.Close()

	if err := seedOrganizations(ctx, pool); err != nil {
		return err
	}

	if err := seedUsers(ctx, pool); err != nil {
		return err
	}

	if err := seedDepartments(ctx, pool); err != nil {
		return err
	}

	if err := seedMemberships(ctx, pool); err != nil {
		return err
	}

	return nil
}

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

// demoDepartment belongs to a demoOrganization (by slug).
type demoDepartment struct {
	slug string
	name string
}

// demoMembership links a demoUser (by email) to a demoOrganization (by slug).
// department is a demoDepartment name within that org, or empty for none.
type demoMembership struct {
	email      string
	slug       string
	role       string
	jobTitle   string
	department string
}

var demoOrganizations = []demoOrganization{
	{name: "Yivi", slug: "yivi"},
	{name: "Firsty.app", slug: "firsty"},
	{name: "Radboud Universiteit", slug: "radboud-universiteit"},
}

var demoUsers = []demoUser{
	{email: "admin@yivi.app", preferredName: "Jan", givenNames: "Johannes Hendrik", lastName: "Janssen"},
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

func seedOrganizations(ctx context.Context, pool *pgxpool.Pool) error {
	var inserted int64
	for _, o := range demoOrganizations {
		result, err := pool.Exec(ctx, `
			INSERT INTO organizations (name, slug)
			VALUES ($1, $2)
			ON CONFLICT (slug) DO NOTHING
		`, o.name, o.slug)
		if err != nil {
			return fmt.Errorf("seed: organizations: %w", err)
		}
		inserted += result.RowsAffected()
	}

	slog.Info("seeded organizations", slog.Int64("inserted", inserted))

	return nil
}

func seedUsers(ctx context.Context, pool *pgxpool.Pool) error {
	var inserted int64
	for _, u := range demoUsers {
		result, err := pool.Exec(ctx, `
			INSERT INTO users (email, preferred_name, given_names, name_prefix, last_name)
			VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''), $5)
			ON CONFLICT (email) DO UPDATE SET
				preferred_name = EXCLUDED.preferred_name,
				given_names = EXCLUDED.given_names,
				name_prefix = EXCLUDED.name_prefix,
				last_name = EXCLUDED.last_name
		`, u.email, u.preferredName, u.givenNames, u.namePrefix, u.lastName)
		if err != nil {
			return fmt.Errorf("seed: users: %w", err)
		}
		inserted += result.RowsAffected()
	}

	slog.Info("seeded users", slog.Int64("inserted", inserted))

	return nil
}

func seedDepartments(ctx context.Context, pool *pgxpool.Pool) error {
	var inserted int64
	for _, d := range demoDepartments {
		result, err := pool.Exec(ctx, `
			INSERT INTO departments (organization_id, name)
			SELECT o.id, $2
			FROM organizations o
			WHERE o.slug = $1
			ON CONFLICT (organization_id, name) DO NOTHING
		`, d.slug, d.name)
		if err != nil {
			return fmt.Errorf("seed: departments: %w", err)
		}
		inserted += result.RowsAffected()
	}

	slog.Info("seeded departments", slog.Int64("inserted", inserted))

	return nil
}

func seedMemberships(ctx context.Context, pool *pgxpool.Pool) error {
	var inserted int64
	for _, m := range demoMemberships {
		result, err := pool.Exec(ctx, `
			INSERT INTO memberships (user_id, organization_id, role, job_title, department_id)
			SELECT u.id, o.id, $3, NULLIF($4, ''), d.id
			FROM users u, organizations o
			LEFT JOIN departments d ON d.organization_id = o.id AND d.name = NULLIF($5, '')
			WHERE u.email = $1 AND o.slug = $2
			ON CONFLICT (user_id, organization_id) DO NOTHING
		`, m.email, m.slug, m.role, m.jobTitle, m.department)
		if err != nil {
			return fmt.Errorf("seed: memberships: %w", err)
		}
		inserted += result.RowsAffected()
	}

	slog.Info("seeded memberships", slog.Int64("inserted", inserted))

	return nil
}
