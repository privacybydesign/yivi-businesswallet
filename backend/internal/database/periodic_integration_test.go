//go:build integration

package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// Of two replicas starting together one runs the job, and neither runs it
// again before the interval is over, also not after a restart.
func TestPeriodicRunsOncePerIntervalAcrossReplicas(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runs := make(chan string, 8)
	start := func(replica string) {
		database.Periodic{Name: "test-job", Interval: time.Hour, Runs: pool}.Start(ctx, func(context.Context) error {
			runs <- replica
			return nil
		})
	}
	start("a")
	start("b")
	start("restarted")

	select {
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("no replica ran the job at start")
	}
	select {
	case second := <-runs:
		t.Fatalf("replica %q ran the job a second time within its interval", second)
	case <-time.After(300 * time.Millisecond):
	}

	// Once the interval has passed the next check runs it again.
	if _, err := pool.Exec(ctx, `UPDATE periodic_job_runs SET last_run_at = now() - interval '2 hours' WHERE name = 'test-job'`); err != nil {
		t.Fatalf("age the run: %v", err)
	}
	start("later")
	select {
	case <-runs:
	case <-time.After(5 * time.Second):
		t.Fatal("the job did not run once its interval had passed")
	}
}
