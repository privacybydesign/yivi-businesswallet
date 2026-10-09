//go:build integration

package flow

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// concurrentSaves is how many saves of one flow race in
// TestConcurrentSavesVersion.
const concurrentSaves = 8

// Saves of one flow at the same time each get their own next version, none a
// primary key error.
func TestConcurrentSavesVersion(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)
	var orgID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, kvk_number, euid, digital_address)
		VALUES ('Acme', 'acme', '12345678', 'NLNHR.12345678', 'acme@example.com') RETURNING id::text`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(pool)
	fd := validFlow()
	fd.TenantID = orgID
	first, err := store.Save(ctx, fd)
	if err != nil {
		t.Fatalf("first save: %v", err)
	}

	var wg sync.WaitGroup
	versions := make([]int, concurrentSaves)
	for i := range concurrentSaves {
		wg.Go(func() {
			saved, err := store.Save(ctx, first)
			if err != nil {
				t.Errorf("concurrent save: %v", err)
				return
			}
			versions[i] = saved.Version
		})
	}
	wg.Wait()

	slices.Sort(versions)
	for i, v := range versions {
		if want := first.Version + 1 + i; v != want {
			t.Fatalf("versions = %v, want %d..%d", versions, first.Version+1, first.Version+concurrentSaves)
		}
	}
	active, err := store.Get(ctx, orgID, first.ID, 0)
	if err != nil || active.Version != first.Version+concurrentSaves {
		t.Errorf("active = v%d, %v; want the last save", active.Version, err)
	}
}
