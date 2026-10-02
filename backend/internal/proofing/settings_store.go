package proofing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// SettingsStore persists the org admin's flow selection.
type SettingsStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewSettingsStore(db database.DB, recorder audit.Recorder) *SettingsStore {
	return &SettingsStore{db: db, audit: recorder}
}

// FlowSelection returns the org's allow-list; empty when the admin chose none.
func (s *SettingsStore) FlowSelection(ctx context.Context, orgID uuid.UUID) (FlowSelection, error) {
	return readFlowSelection(ctx, s.db, orgID)
}

// SaveFlowSelection replaces the org's allow-list and audits
// identity_proofing.flows_configured in the same transaction. The org must
// already be provisioned (the rows reference its settings row).
func (s *SettingsStore) SaveFlowSelection(ctx context.Context, orgID uuid.UUID, sel FlowSelection) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := readFlowSelection(ctx, q, orgID)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM org_identity_proofing_flows WHERE organization_id = $1`, orgID); err != nil {
			return fmt.Errorf("proofing: clear flow selection org %s: %w", orgID, err)
		}
		const insert = `INSERT INTO org_identity_proofing_flows (organization_id, flow_id, is_default)
			SELECT $1, id, id = $3 FROM unnest($2::text[]) AS id`
		if _, err := q.Exec(ctx, insert, orgID, sel.FlowIDs, sel.DefaultFlowID); err != nil {
			return fmt.Errorf("proofing: save flow selection org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowsConfigured,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(before.auditFields(), sel.auditFields()))
	})
}

func readFlowSelection(ctx context.Context, q database.Querier, orgID uuid.UUID) (FlowSelection, error) {
	rows, err := q.Query(ctx,
		`SELECT flow_id, is_default FROM org_identity_proofing_flows WHERE organization_id = $1 ORDER BY flow_id`, orgID)
	if err != nil {
		return FlowSelection{}, fmt.Errorf("proofing: read flow selection org %s: %w", orgID, err)
	}
	defer rows.Close()
	sel := FlowSelection{FlowIDs: []string{}}
	for rows.Next() {
		var id string
		var isDefault bool
		if err := rows.Scan(&id, &isDefault); err != nil {
			return FlowSelection{}, fmt.Errorf("proofing: scan flow selection org %s: %w", orgID, err)
		}
		sel.FlowIDs = append(sel.FlowIDs, id)
		if isDefault {
			sel.DefaultFlowID = id
		}
	}
	if err := rows.Err(); err != nil {
		return FlowSelection{}, fmt.Errorf("proofing: read flow selection org %s: %w", orgID, err)
	}
	return sel, nil
}

func (sel FlowSelection) auditFields() map[string]any {
	return map[string]any{"flowIds": sel.FlowIDs, "defaultFlowId": sel.DefaultFlowID}
}

// RecordFlowEvent audits a flow change at IPS (created, a version added or
// activated), with the flow's configuration as it now stands. The flow lives at
// IPS, not in this database, so there is no local write to share a transaction
// with. A flow holds no personal data, so the whole configuration is audited.
func (s *SettingsStore) RecordFlowEvent(ctx context.Context, orgID uuid.UUID, action string, flow proofingprovider.Flow) error {
	raw, err := json.Marshal(flow.FlowSpec)
	if err != nil {
		return fmt.Errorf("proofing: encode flow %s for audit: %w", flow.ID, err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		return fmt.Errorf("proofing: encode flow %s for audit: %w", flow.ID, err)
	}
	spec["version"] = flow.Version
	return s.audit.Record(ctx, s.db, action,
		audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flow.ID, OrgID: &orgID},
		audit.Created(spec))
}
