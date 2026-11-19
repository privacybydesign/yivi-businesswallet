//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// Deleting a referenced row sets these ON DELETE SET NULL columns to NULL; that
// lookup needs an index leading on the column, or it scans the whole table.
func TestSetNullForeignKeysIndexed(t *testing.T) {
	ctx := context.Background()
	pool, _ := testdb.Fresh(t)

	for _, tc := range []struct {
		table  string
		column string
	}{
		{"identity_proofing_webhook_deliveries", "request_id"},
		{"identity_proofing_requests", "requested_by"},
		{"identity_proofing_requests", "subject_user_id"},
		{"identity_proofing_requests", "api_key_id"},
	} {
		t.Run(tc.table+"."+tc.column, func(t *testing.T) {
			var indexed bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (
					SELECT 1 FROM pg_index i
					JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = i.indkey[0]
					WHERE i.indrelid = $1::regclass
						AND a.attname = $2)`, tc.table, tc.column).Scan(&indexed); err != nil {
				t.Fatalf("query indexes: %v", err)
			}

			if !indexed {
				t.Fatalf("%s.%s has no index", tc.table, tc.column)
			}
		})
	}
}
