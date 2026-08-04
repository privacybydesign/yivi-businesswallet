package provisioning

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeEnabled struct {
	orgIDs []uuid.UUID
	err    error
}

func (f fakeEnabled) ListEnabled(context.Context) ([]uuid.UUID, error) {
	return f.orgIDs, f.err
}

// countingSyncer fails for one nominated organisation and succeeds for the rest.
type countingSyncer struct {
	failFor uuid.UUID
	synced  []uuid.UUID
}

func (c *countingSyncer) Sync(_ context.Context, orgID uuid.UUID) (Result, error) {
	c.synced = append(c.synced, orgID)
	if orgID == c.failFor {
		return Result{}, errors.New("status 401 (invalid_client)")
	}
	return Result{}, nil
}

func TestSyncAllKeepsGoingAfterOneOrganisationFails(t *testing.T) {
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	sync := &countingSyncer{failFor: second}
	scheduler := NewScheduler(fakeEnabled{orgIDs: []uuid.UUID{first, second, third}}, sync)

	if err := scheduler.SyncAll(context.Background()); err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	// One tenant's expired secret must not stop everybody else's sync.
	if len(sync.synced) != 3 {
		t.Errorf("synced %d organisations, want all 3", len(sync.synced))
	}
}

func TestSyncAllReportsAFailureToListOrganisations(t *testing.T) {
	sync := &countingSyncer{}
	scheduler := NewScheduler(fakeEnabled{err: errors.New("database is down")}, sync)

	if err := scheduler.SyncAll(context.Background()); err == nil {
		t.Fatal("SyncAll succeeded although the organisations could not be listed")
	}
	if len(sync.synced) != 0 {
		t.Error("nothing should have been synced")
	}
}

func TestSyncAllStopsOnACancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sync := &countingSyncer{}
	scheduler := NewScheduler(fakeEnabled{orgIDs: []uuid.UUID{uuid.New()}}, sync)

	if err := scheduler.SyncAll(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(sync.synced) != 0 {
		t.Error("a shutting-down process should not start another organisation's sync")
	}
}
