package database

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeriodicRunsAtStartAndOnEveryTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var runs atomic.Int32
	Periodic{Name: "test", Interval: 10 * time.Millisecond}.Start(ctx, func(context.Context) error {
		runs.Add(1)
		return nil
	})

	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := runs.Load(); got < 3 {
		t.Fatalf("runs = %d, want the run at start and then one per tick", got)
	}
}

func TestPeriodicChecksOftenEnoughToNoticeADueRun(t *testing.T) {
	if got := (Periodic{Interval: 24 * time.Hour, Runs: struct{ Querier }{}}).checkEvery(); got != maxDueCheck {
		t.Fatalf("a daily job checks every %s, want %s", got, maxDueCheck)
	}
	if got := (Periodic{Interval: time.Minute, Runs: struct{ Querier }{}}).checkEvery(); got != time.Minute {
		t.Fatalf("a minutely job checks every %s, want its interval", got)
	}
}
