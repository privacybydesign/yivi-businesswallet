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

	return nil
}

func seedOrganizations(ctx context.Context, pool *pgxpool.Pool) error {
	result, err := pool.Exec(ctx, `
		INSERT INTO organizations (name, slug) VALUES
			('Firsty.app', 'firsty'),
			('Yivi', 'yivi'),
			('Radboud Universiteit', 'radboud-universiteit')
		ON CONFLICT (slug) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("seed: organizations: %w", err)
	}

	slog.Info("seeded organizations", slog.Int64("inserted", result.RowsAffected()))

	return nil
}
