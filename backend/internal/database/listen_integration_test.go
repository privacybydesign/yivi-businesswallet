//go:build integration

package database_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

const (
	testChannel = "test_wakeups"
	testWait    = 5 * time.Second
)

// waitRuns waits until the job has run at least n times.
func waitRuns(t *testing.T, runs *atomic.Int32, n int32) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for runs.Load() < n {
		if time.Now().After(deadline) {
			t.Fatalf("job ran %d times, want %d", runs.Load(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunOnNotifyWakesOnNotifyAndDeadlineOnly(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int32
	var deadline atomic.Pointer[time.Time]
	database.RunOnNotify(ctx, pool, testChannel, "test", func(context.Context) (time.Time, error) {
		runs.Add(1)
		if d := deadline.Swap(nil); d != nil {
			return *d, nil
		}
		return time.Time{}, nil
	})
	// Once at start and once when LISTEN holds.
	waitRuns(t, &runs, 2)

	// Nothing wakes it on its own: no notify, no deadline.
	time.Sleep(300 * time.Millisecond)
	if got := runs.Load(); got != 2 {
		t.Fatalf("job ran %d times with nothing to do, want 2", got)
	}

	next := time.Now().Add(200 * time.Millisecond)
	deadline.Store(&next)
	for runs.Load() < 3 {
		if err := database.Notify(ctx, pool, testChannel); err != nil {
			t.Fatalf("Notify: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// That run returned a deadline, which wakes it once more.
	waitRuns(t, &runs, 4)
}
