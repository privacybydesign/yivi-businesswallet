//go:build integration

package organization_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/export"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/testdb"
)

// failingQueuer stands in for an export store that cannot queue, so the test can
// prove the termination rolls back with it.
type failingQueuer struct{}

func (failingQueuer) EnqueueTx(context.Context, database.Querier, uuid.UUID, string, *uuid.UUID) error {
	return errors.New("queue unavailable")
}

func TestTerminateRevokesAndQueuesTheExport(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	exports := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID
	ctx := context.Background()

	org, err := store.Terminate(ctx, orgID, exports)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if org.Status != organization.StatusRevoked || org.TerminatedAt == nil {
		t.Fatalf("org = %+v, want revoked and stamped", org)
	}
	// The default instruction is transfer, so nothing is owed beyond the handover.
	if org.ErasurePendingAt != nil {
		t.Errorf("erasurePendingAt = %v, want none for a transfer instruction", org.ErasurePendingAt)
	}

	jobs, err := exports.ListJobs(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Origin != export.OriginTermination {
		t.Fatalf("jobs = %+v, want one termination export", jobs)
	}
}

// A delete instruction is marked, never carried out: erasure is irreversible and
// destroys the trail proving the handover.
func TestTerminateMarksErasureForADeleteInstruction(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	exports := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID
	ctx := context.Background()

	if _, err := store.SetDataInstruction(ctx, orgID, organization.InstructionDelete); err != nil {
		t.Fatalf("SetDataInstruction: %v", err)
	}

	org, err := store.Terminate(ctx, orgID, exports)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if org.ErasurePendingAt == nil {
		t.Error("erasurePendingAt is nil, want the erasure debt marked")
	}
	// Marked, not done: the organisation is still there to erase.
	if _, err := store.GetByID(ctx, orgID); err != nil {
		t.Errorf("GetByID after termination = %v, want the org still present", err)
	}
}

// Terminating twice would owe a second export of data already handed over.
func TestTerminateIsIdempotentlyRefused(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	exports := export.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID
	ctx := context.Background()

	if _, err := store.Terminate(ctx, orgID, exports); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if _, err := store.Terminate(ctx, orgID, exports); !errors.Is(err, organization.ErrAlreadyTerminated) {
		t.Errorf("second Terminate = %v, want ErrAlreadyTerminated", err)
	}

	jobs, err := exports.ListJobs(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("queued %d exports, want the refused termination to add none", len(jobs))
	}
}

// The termination and the export it owes commit together: a termination recorded
// without its export would drop the handover obligation on the floor.
func TestTerminateRollsBackWhenTheExportCannotBeQueued(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID
	ctx := context.Background()

	if _, err := store.Terminate(ctx, orgID, failingQueuer{}); err == nil {
		t.Fatal("Terminate = nil, want the queue error")
	}

	org, err := store.GetByID(ctx, orgID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if org.TerminatedAt != nil || org.Status == organization.StatusRevoked {
		t.Errorf("org = %+v, want the termination rolled back", org)
	}
}

func TestSetDataInstructionRejectsUnknownValues(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID

	if _, err := store.SetDataInstruction(context.Background(), orgID, "shred"); !errors.Is(err, organization.ErrInvalidInstruction) {
		t.Errorf("SetDataInstruction = %v, want ErrInvalidInstruction", err)
	}
}

// Re-recording the same instruction is not a change, so it writes no event.
func TestSetDataInstructionAuditsOnlyRealChanges(t *testing.T) {
	pool, _ := testdb.Fresh(t)
	store := organization.NewStore(pool, audit.NewDBRecorder())
	orgID := makeOrg(t, pool, "Caesar Groep B.V.", "caesar").ID
	ctx := context.Background()

	if _, err := store.SetDataInstruction(ctx, orgID, organization.InstructionDelete); err != nil {
		t.Fatalf("SetDataInstruction: %v", err)
	}
	if _, err := store.SetDataInstruction(ctx, orgID, organization.InstructionDelete); err != nil {
		t.Fatalf("SetDataInstruction (repeat): %v", err)
	}

	page, err := audit.NewReader(pool).ListForOrganization(ctx, orgID, nil, 10)
	if err != nil {
		t.Fatalf("ListForOrganization: %v", err)
	}
	changes := 0
	for _, event := range page.Events {
		if event.Action == audit.DataInstructionUpdated {
			changes++
		}
	}
	if changes != 1 {
		t.Errorf("recorded %d instruction changes, want 1", changes)
	}
}
