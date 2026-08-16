package organization

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// ErrAlreadyTerminated is returned when service was already ended for an
// organisation. Terminating twice would owe a second export of data the owner
// has already been handed.
var ErrAlreadyTerminated = errors.New("organization: already terminated")

// ErrInvalidInstruction is returned for a data instruction outside the closed set.
var ErrInvalidInstruction = errors.New("organization: invalid data instruction")

// exportQueuer queues the export a termination owes, inside the same transaction
// that records the termination.
type exportQueuer interface {
	EnqueueTx(ctx context.Context, q database.Querier, orgID uuid.UUID, origin string, requestedBy *uuid.UUID) error
}

// SetDataInstruction records what the owner wants done with their data when the
// provider stops serving them. It is captured in advance because termination is
// exactly the moment nobody can be asked.
func (s *Store) SetDataInstruction(ctx context.Context, orgID uuid.UUID, instruction string) (Organization, error) {
	if instruction != InstructionTransfer && instruction != InstructionDelete {
		return Organization{}, ErrInvalidInstruction
	}

	var org Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `
			WITH old AS (SELECT data_instruction FROM organizations WHERE id = $1 FOR UPDATE)
			UPDATE organizations o SET data_instruction = $2, updated_at = now()
			FROM old WHERE o.id = $1
			RETURNING ` + orgColumnsQ + `, old.data_instruction`
		var before string
		row := q.QueryRow(ctx, update, orgID, instruction)
		err := row.Scan(&org.ID, &org.Name, &org.Slug, &org.KVKNumber, &org.EUID,
			&org.DigitalAddress, &org.Status, &org.BootstrappedAt, &org.DataInstruction,
			&org.TerminatedAt, &org.ErasurePendingAt, &before)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: set data instruction %s: %w", orgID, err)
		}
		if before == instruction {
			return nil
		}
		return s.audit.Record(ctx, q, audit.DataInstructionUpdated,
			audit.Target{Type: audit.TargetOrganization, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"dataInstruction": before},
				map[string]any{"dataInstruction": instruction}))
	})
	return org, err
}

// Terminate ends service for an organisation and queues the export Art 7(6)(f)
// owes it, in one transaction: a termination recorded without its export would
// leave the obligation with nothing tracking it.
//
// A delete instruction is marked, not carried out. Erasure is irreversible and
// destroys the trail that proves the handover happened, so it stays a deliberate
// operator step behind this marker rather than a side effect of the trigger.
func (s *Store) Terminate(ctx context.Context, orgID uuid.UUID, exports exportQueuer) (Organization, error) {
	var org Organization
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const lock = `SELECT ` + orgColumns + ` FROM organizations WHERE id = $1 FOR UPDATE`
		current, err := scanOrg(q.QueryRow(ctx, lock, orgID))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: lock for termination %s: %w", orgID, err)
		}
		if current.TerminatedAt != nil {
			return ErrAlreadyTerminated
		}

		erasurePending := current.DataInstruction == InstructionDelete
		const update = `UPDATE organizations
			SET status = $2, terminated_at = now(),
			    erasure_pending_at = CASE WHEN $3 THEN now() ELSE NULL END,
			    updated_at = now()
			WHERE id = $1
			RETURNING ` + orgColumns
		org, err = scanOrg(q.QueryRow(ctx, update, orgID, StatusRevoked, erasurePending))
		if err != nil {
			return fmt.Errorf("organization: terminate %s: %w", orgID, err)
		}

		// The origin is a literal because internal/export imports this package, so
		// the constant cannot be read back the other way. export.OriginTermination
		// is the same value and its CHECK constraint is what enforces the pair.
		if err := exports.EnqueueTx(ctx, q, orgID, "termination", nil); err != nil {
			return err
		}

		return s.audit.Record(ctx, q, audit.OrganizationTerminated,
			audit.Target{Type: audit.TargetOrganization, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"status": current.Status},
				map[string]any{
					"status":          org.Status,
					"dataInstruction": org.DataInstruction,
					"erasurePending":  erasurePending,
				}))
	})
	return org, err
}
