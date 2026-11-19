//go:build integration

package regulasweep

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// Add keeps the later due time, Settle the earlier one: a running session is
// never swept early, an ended one is swept from its end.
func TestStoreAddAndSettle(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	s := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiry, end := now.Add(time.Hour), now.Add(-time.Minute)

	if err := s.Add(ctx, "tag", end); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(ctx, "tag", expiry); err != nil {
		t.Fatal(err)
	}
	if due, err := s.Due(ctx, now, Batch); err != nil || len(due) != 0 {
		t.Fatalf("Due after Add = %+v, %v; want nothing: Add keeps the later expiry", due, err)
	}
	if err := s.Settle(ctx, "tag", end); err != nil {
		t.Fatal(err)
	}
	if due, err := s.Due(ctx, now, Batch); err != nil || len(due) != 1 || due[0].Tag != "tag" {
		t.Fatalf("Due after Settle = %+v, %v; want the tag, due from the session's end", due, err)
	}
}
