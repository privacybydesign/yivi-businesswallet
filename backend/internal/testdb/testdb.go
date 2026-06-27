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
	dropTimeout        = 10 * time.Second
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

func dropDatabase(t *testing.T, adminDSN, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), dropTimeout)
	defer cancel()

	admin, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Errorf("testdb: connect admin for drop: %v", err)
		return
	}
	defer func() { _ = admin.Close(ctx) }()

	if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, name)); err != nil {
		t.Errorf("testdb: drop database %q: %v", name, err)
	}
}

// uniqueName derives a valid, collision-free database name from the test name
// and a process-wide counter (no math/rand, which the harness disallows).
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
	return fmt.Sprintf("%s_%d", base, dbCounter.Add(1))
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
