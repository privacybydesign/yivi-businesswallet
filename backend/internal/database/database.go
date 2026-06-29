package database

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	MaxConns = 10
	MinConns = 2
)

type DB interface {
	Querier
	Begin(ctx context.Context) (pgx.Tx, error)
}

type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func InTx(ctx context.Context, db DB, fn func(Querier) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func New(context context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	config.MaxConns = MaxConns
	config.MinConns = MinConns

	pool, err := pgxpool.NewWithConfig(context, config)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(context); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}
