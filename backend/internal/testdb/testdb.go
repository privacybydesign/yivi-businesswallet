//go:build integration

// Package testdb provisions isolated, migrated PostgreSQL databases for
// integration tests. It is compiled only under the `integration` build tag.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
)

var dbCounter atomic.Int64

// Fresh creates a uniquely-named database on the server pointed at by
// TEST_DATABASE_URL, applies all migrations to it, and returns a connected pool
// plus its DSN. The database is dropped and the pool closed via t.Cleanup. When
// TEST_DATABASE_URL is unset the test is skipped, so the default suite stays
// green without a database.
func Fresh(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()

	adminDSN := os.Getenv(envTestDatabaseURL)
	if adminDSN == "" {
		t.Skipf("set %s to run integration tests", envTestDatabaseURL)
	}

	name := uniqueName(t)
	ctx := context.Background()

	createDatabase(t, ctx, adminDSN, name)

	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("testdb: build dsn: %v", err)
	}

	if err := migrate.Up(ctx, dsn); err != nil {
		t.Fatalf("testdb: migrate %q: %v", name, err)
	}

	pool, err := database.New(ctx, dsn)
	if err != nil {
		t.Fatalf("testdb: pool %q: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		dropDatabase(t, adminDSN, name)
	})

	return pool, dsn
}

func createDatabase(t *testing.T, ctx context.Context, adminDSN, name string) {
	t.Helper()
	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("testdb: connect admin: %v", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	// name is sanitized to [a-z0-9_], so quoting it is safe; CREATE DATABASE
	// cannot be parameterized.
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %q`, name)); err != nil {
		t.Fatalf("testdb: create database %q: %v", name, err)
	}
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
