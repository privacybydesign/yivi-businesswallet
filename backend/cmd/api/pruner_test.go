package main

import (
	"context"
	"testing"
	"time"
)

// prunerWait bounds how long the test waits for a run it expects.
const prunerWait = 5 * time.Second

// A pruner runs at start, not only after its first interval: retention must
// not depend on how long a replica stays up.
func TestStartPrunerRunsAtStart(t *testing.T) {
	ctx := t.Context()
	ran := make(chan struct{}, 1)
	startPruner(ctx, "test", time.Hour, func(context.Context) (int64, error) {
		select {
		case ran <- struct{}{}:
		default:
		}
		return 0, nil
	})
	select {
	case <-ran:
	case <-time.After(prunerWait):
		t.Fatal("the pruner did not run at start")
	}
}
