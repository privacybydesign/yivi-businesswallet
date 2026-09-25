package export

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store writes the export's only row: the audit event. The export mutates
// nothing else, so this slice has no table of its own.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

// RecordExport records who exported what. The target id is the bundle's own id,
// so a bundle in someone's hands traces back to the admin who asked for it.
func (s *Store) RecordExport(ctx context.Context, orgID, bundleID uuid.UUID, sections []string) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		metadata := map[string]any{
			"bundleId":      bundleID.String(),
			"schemaVersion": SchemaVersion,
			"sections":      sections,
		}
		if err := s.audit.Record(ctx, q, audit.ExportRequested,
			audit.Target{Type: audit.TargetExport, ID: bundleID.String(), OrgID: &orgID},
			audit.Created(metadata)); err != nil {
			return fmt.Errorf("export: recording export for org %s: %w", orgID, err)
		}
		return nil
	})
}
