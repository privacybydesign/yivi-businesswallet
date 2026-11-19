//go:build integration

// Package testdb provisions isolated, migrated PostgreSQL databases for
// integration tests. It is compiled only under the `integration` build tag.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/migrate"
)

const (
	envTestDatabaseURL = "TEST_DATABASE_URL"
	namePrefix         = "ybw_test_"
	maxNameBase        = 40
	// dropAttemptTimeout bounds a single drop attempt; dropAttempts retries cover a
	// loaded CI Postgres, where the forced drop plus the admin connect it needs can
	// transiently run long. A drop that loses this race is left behind, not failed:
	// see dropDatabase.
	dropAttemptTimeout = 20 * time.Second
	dropAttempts       = 3

	// templatePrefix and templateHashLen name the migrated template database
	// after the migrations' fingerprint, so a changed migration builds a new one.
	templatePrefix  = namePrefix + "tmpl_"
	templateHashLen = 16
	// inUseAttempts and inUseBackoff wait out a connection the server is still
	// closing, which blocks copying or renaming a database (object_in_use).
	inUseAttempts       = 50
	inUseBackoff        = 100 * time.Millisecond
	sqlStateObjectInUse = "55006"
)

var dbCounter atomic.Int64

// template is the migrated database every Fresh database is copied from, built
// once per test binary.
var template = sync.OnceValues(func() (string, error) {
	return ensureTemplate(context.Background(), os.Getenv(envTestDatabaseURL))
})

// Fresh creates a uniquely-named database on the server pointed at by
// TEST_DATABASE_URL as a copy of the migrated template, and returns a connected pool
// plus its DSN. The database is dropped and the pool closed via t.Cleanup. When
// TEST_DATABASE_URL is unset the test is skipped, so the default suite stays
// green without a database.
func Fresh(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	ctx := context.Background()
	adminDSN := adminDSN(t)
	tmpl, err := template()
	if err != nil {
		t.Fatalf("testdb: migrated template: %v", err)
	}
	dsn := newDatabase(t, adminDSN, tmpl)

	pool, err := database.New(ctx, dsn)
	if err != nil {
		t.Fatalf("testdb: pool %q: %v", dsn, err)
	}

	t.Cleanup(pool.Close)

	return pool, dsn
}

// Bare creates a uniquely-named, empty database on the server pointed at by
// TEST_DATABASE_URL and returns its DSN without applying any migration, for
// tests that exercise the migration history itself. The database is dropped via
// t.Cleanup. When TEST_DATABASE_URL is unset the test is skipped.
func Bare(t *testing.T) string {
	t.Helper()
	return newDatabase(t, adminDSN(t), "")
}

// adminDSN is TEST_DATABASE_URL; the test is skipped when it is unset.
func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(envTestDatabaseURL)
	if dsn == "" {
		t.Skipf("set %s to run integration tests", envTestDatabaseURL)
	}
	return dsn
}

// newDatabase creates a uniquely-named database, a copy of tmpl or empty when
// tmpl is "", dropped via t.Cleanup, and returns its DSN.
func newDatabase(t *testing.T, adminDSN, tmpl string) string {
	t.Helper()
	name := uniqueName(t)
	if err := createDatabase(context.Background(), adminDSN, name, tmpl); err != nil {
		t.Fatalf("testdb: %v", err)
	}
	t.Cleanup(func() { dropDatabase(t, adminDSN, name) })
	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("testdb: build dsn: %v", err)
	}
	return dsn
}

func createDatabase(ctx context.Context, adminDSN, name, tmpl string) error {
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("connect admin: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	// Names are sanitized to [a-z0-9_], so quoting them is safe; CREATE DATABASE
	// cannot be parameterized.
	stmt := fmt.Sprintf(`CREATE DATABASE %q`, name)
	if tmpl != "" {
		stmt += fmt.Sprintf(` TEMPLATE %q`, tmpl)
	}
	if err := execWhileInUse(ctx, admin, stmt); err != nil {
		return fmt.Errorf("create database %q: %w", name, err)
	}
	return nil
}

// ensureTemplate returns the migrated template for the current migrations,
// building it if no earlier run did. An advisory lock lets one of the test
// binaries that start together build it, under another name renamed once
// complete, so a run cut short never leaves a half-migrated template.
func ensureTemplate(ctx context.Context, adminDSN string) (string, error) {
	fingerprint, err := migrate.Fingerprint()
	if err != nil {
		return "", fmt.Errorf("fingerprint migrations: %w", err)
	}
	name := templatePrefix + fingerprint[:templateHashLen]

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return "", fmt.Errorf("connect admin: %w", err)
	}
	// Closing the connection also releases the advisory lock.
	defer func() { _ = admin.Close(ctx) }()
	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, name); err != nil {
		return "", fmt.Errorf("lock template %q: %w", name, err)
	}
	var exists bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return "", fmt.Errorf("look up template %q: %w", name, err)
	}
	if exists {
		return name, nil
	}

	building := fmt.Sprintf("%s_build_%d", name, os.Getpid())
	if err := createDatabase(ctx, adminDSN, building, ""); err != nil {
		return "", err
	}
	dsn, err := withDatabase(adminDSN, building)
	if err != nil {
		return "", fmt.Errorf("build dsn: %w", err)
	}
	if err := migrate.Up(ctx, dsn); err != nil {
		_ = tryDropDatabase(adminDSN, building)
		return "", fmt.Errorf("migrate %q: %w", building, err)
	}
	if err := execWhileInUse(ctx, admin, fmt.Sprintf(`ALTER DATABASE %q RENAME TO %q`, building, name)); err != nil {
		return "", fmt.Errorf("rename %q to %q: %w", building, name, err)
	}
	return name, nil
}

// execWhileInUse runs stmt, retrying while a connection the server is still
// closing keeps the database it copies or renames in use.
func execWhileInUse(ctx context.Context, conn *pgx.Conn, stmt string) error {
	var err error
	for range inUseAttempts {
		if _, err = conn.Exec(ctx, stmt); !isObjectInUse(err) {
			return err
		}
		time.Sleep(inUseBackoff)
	}
	return err
}

func isObjectInUse(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateObjectInUse
}

// dropDatabase tears down a test database, best-effort. The database is uniquely
// named and ephemeral, so a drop that loses a race with a loaded server must not
// fail a test whose assertions already passed — teardown latency says nothing
// about the code under test. Retry a few times to clean up under transient load,
// then log and leave it behind rather than reporting a spurious failure.
func dropDatabase(t *testing.T, adminDSN, name string) {
	t.Helper()

	var lastErr error
	for attempt := 1; attempt <= dropAttempts; attempt++ {
		if lastErr = tryDropDatabase(adminDSN, name); lastErr == nil {
			return
		}
	}
	t.Logf("testdb: drop database %q left behind after %d attempts (harmless, ephemeral): %v",
		name, dropAttempts, lastErr)
}

func tryDropDatabase(adminDSN, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), dropAttemptTimeout)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return fmt.Errorf("connect admin for drop: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
		return fmt.Errorf("drop database %q: %w", name, err)
	}
	return nil
}

// uniqueName derives a valid, collision-free database name from the test name,
// the process id and a process-wide counter (no math/rand, which the harness
// disallows).
//
// The process id is what makes the name unique *between* packages. `go test ./...`
// runs one process per package, several at a time, and the counter restarts at 1
// in each — so two packages that happen to name a test the same way (three of them
// had a TestGetSettingsUnconfigured) would both ask for
// ybw_test_testgetsettingsunconfigured_1 and the second one loses with "database
// already exists". That is a race between packages, so it fails whichever package
// is unlucky rather than the one whose test was added.
func uniqueName(t *testing.T) string {
	var b strings.Builder
	b.WriteString(namePrefix)
	for _, r := range strings.ToLower(t.Name()) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	base := b.String()
	if len(base) > maxNameBase {
		base = base[:maxNameBase]
	}
	return fmt.Sprintf("%s_%d_%d", base, os.Getpid(), dbCounter.Add(1))
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
