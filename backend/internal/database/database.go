package database

import (
	"context"
	"strconv"
	"time"

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

// Options tune a pool; the zero value is the defaults.
type Options struct {
	// MaxConns caps the pool; 0 is MaxConns.
	MaxConns int32
	// StatementTimeout is the longest one statement may run; 0 is no limit.
	// It is set per connection and not in the DSN, which migrations share.
	StatementTimeout time.Duration
	// HoldsLocks is for a pool whose transactions sit idle on purpose while
	// they hold a lock: it switches off the server's idle-in-transaction
	// timeout, which would otherwise drop the lock without the holder knowing.
	HoldsLocks bool
}

func New(context context.Context, dsn string) (*pgxpool.Pool, error) {
	return NewWithOptions(context, dsn, Options{})
}

func NewWithOptions(context context.Context, dsn string, opts Options) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}

	config.MaxConns = MaxConns
	if opts.MaxConns > 0 {
		config.MaxConns = opts.MaxConns
	}
	config.MinConns = min(MinConns, config.MaxConns)
	if opts.StatementTimeout > 0 {
		config.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(opts.StatementTimeout.Milliseconds(), 10)
	}

	if opts.HoldsLocks {
		config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "0"
	}

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
