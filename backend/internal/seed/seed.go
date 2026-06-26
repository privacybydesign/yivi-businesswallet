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

	if err := seedMemberships(ctx, pool); err != nil {
		return err
	}

	return nil
}

type demoMembership struct {
	email string
	slug  string
	role  string
}

var demoMemberships = []demoMembership{
	{email: "user@yivi.app", slug: "yivi", role: "admin"},
	{email: "user@yivi.app", slug: "firsty", role: "member"},
}

func seedMemberships(ctx context.Context, pool *pgxpool.Pool) error {
	var inserted int64
	for _, m := range demoMemberships {
		result, err := pool.Exec(ctx, `
			WITH u AS (
				INSERT INTO users (email) VALUES ($1)
				ON CONFLICT (email) DO UPDATE SET email = EXCLUDED.email
				RETURNING id
			)
			INSERT INTO memberships (user_id, organization_id, role)
			SELECT u.id, o.id, $3
			FROM u
			JOIN organizations o ON o.slug = $2
			ON CONFLICT (user_id, organization_id) DO NOTHING
		`, m.email, m.slug, m.role)
		if err != nil {
			return fmt.Errorf("seed: memberships: %w", err)
		}
		inserted += result.RowsAffected()
	}

	slog.Info("seeded memberships", slog.Int64("inserted", inserted))

	return nil
}

func seedOrganizations(ctx context.Context, pool *pgxpool.Pool) error {
	result, err := pool.Exec(ctx, `
		INSERT INTO organizations (name, slug) VALUES
			('Yivi', 'yivi'),
			('Firsty.app', 'firsty'),
			('Radboud Universiteit', 'radboud-universiteit')
		ON CONFLICT (slug) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("seed: organizations: %w", err)
	}

	slog.Info("seeded organizations", slog.Int64("inserted", result.RowsAffected()))

	return nil
}
