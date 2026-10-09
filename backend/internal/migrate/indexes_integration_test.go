//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// Deleting a request sets its deliveries' request_id to NULL; that lookup
// needs an index leading on request_id, or it scans the whole outbox.
func TestDeliveryRequestIDIndexed(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	var indexed bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
			WHERE i.indrelid = 'identity_proofing_webhook_deliveries'::regclass
				AND a.attname = 'request_id')`).Scan(&indexed); err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	if !indexed {
		t.Fatal("identity_proofing_webhook_deliveries.request_id has no index")
	}
}
