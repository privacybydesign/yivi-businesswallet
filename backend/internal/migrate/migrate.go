package migrate

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const (
	maxOpenConns  = 1
	migrationsDir = "migrations"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func open(dns string) (*goose.Provider, error) {
	db, err := sql.Open("pgx", dns)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(maxOpenConns)

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	migrations, err := fs.Sub(migrationsFS, migrationsDir)
	if err != nil {
		return nil, err
	}

	p, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations,
		goose.WithSessionLocker(locker),
		// Feature-branch merges routinely produce migrations whose timestamp is
		// lower than an already-applied one (e.g. qerds_contacts landing after
		// wallet_representations was applied). Apply such missing migrations in
		// order rather than failing — safe here as our migrations are mutually
		// independent (each only references tables from earlier timestamps).
		goose.WithAllowOutofOrder(true),
	)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

func withProvider(dns string, fn func(*goose.Provider) error) error {
	p, err := open(dns)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := p.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	return fn(p)
}

func Up(ctx context.Context, dns string) error {
	return withProvider(dns, func(p *goose.Provider) error {
		_, err := p.Up(ctx)
		return err
	})
}

func Down(ctx context.Context, dsn string) error {
	return withProvider(dsn, func(p *goose.Provider) error {
		_, err := p.Down(ctx)
		return err
	})
}

func Version(ctx context.Context, dsn string) (int64, error) {
	var v int64
	err := withProvider(dsn, func(p *goose.Provider) error {
		var err error
		v, err = p.GetDBVersion(ctx)
		return err
	})
	return v, err
}
