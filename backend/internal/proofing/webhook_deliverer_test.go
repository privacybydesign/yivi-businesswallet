package proofing

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/safehttp"
)

const (
	slowEndpoint = "https://slow.example/hook"
	fastEndpoint = "https://fast.example/hook"
	// slowBacklog is more than deliveryWorkers, so a plain worker pool fed
	// in claim order would spend every worker on the slow endpoint.
	slowBacklog = 2 * deliveryWorkers
	fastCount   = 2
	// fairWait bounds how long the fast endpoint may wait; the slow one
	// answers only when the test lets it.
	fairWait = 5 * time.Second
)

func dueTo(url string, n int) []dueDelivery {
	out := make([]dueDelivery, n)
	for i := range out {
		out[i] = dueDelivery{ID: uuid.New(), URL: url}
	}
	return out
}

func TestSlowEndpointNotBlocking(t *testing.T) {
	// The slow endpoint's backlog is claimed first, as next_attempt_at orders it.
	due := append(dueTo(slowEndpoint, slowBacklog), dueTo(fastEndpoint, fastCount)...)

	release := make(chan struct{})
	fastDone := make(chan struct{}, fastCount)
	var inFlight, maxInFlight atomic.Int32
	deliver := func(_ context.Context, d dueDelivery) {
		if d.URL == fastEndpoint {
			fastDone <- struct{}{}
			return
		}
		n := inFlight.Add(1)
		for {
			seen := maxInFlight.Load()
			if n <= seen || maxInFlight.CompareAndSwap(seen, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
	}

	var wg sync.WaitGroup
	wg.Go(func() { sendFair(t.Context(), due, deliver) })
	defer wg.Wait()
	defer close(release)

	for range fastCount {
		select {
		case <-fastDone:
		case <-time.After(fairWait):
			t.Fatal("fast endpoint waited behind the slow one")
		}
	}
	if got := maxInFlight.Load(); got > endpointWorkers {
		t.Fatalf("slow endpoint had %d sends at once, want at most %d", got, endpointWorkers)
	}
}

func TestSendFairSendsEveryDelivery(t *testing.T) {
	due := append(dueTo(slowEndpoint, slowBacklog), dueTo(fastEndpoint, fastCount)...)
	var sent sync.Map
	sendFair(t.Context(), due, func(_ context.Context, d dueDelivery) { sent.Store(d.ID, true) })
	for _, d := range due {
		if _, ok := sent.Load(d.ID); !ok {
			t.Fatalf("delivery %s was not sent", d.ID)
		}
	}
}

func TestDeliveryBatchFitsLease(t *testing.T) {
	// One endpoint that never answers holds the whole batch, endpointWorkers
	// deep, while every worker busy adds the rest; keep that under half the lease.
	depth := (deliveryBatch + endpointWorkers - 1) / endpointWorkers
	busy := (deliveryBatch + deliveryWorkers - 1) / deliveryWorkers
	worst := time.Duration(depth+busy) * safehttp.RequestTimeout
	if worst > deliveryLease/2 {
		t.Fatalf("a batch may take %s, want at most half of the %s lease", worst, deliveryLease)
	}
}
