package proofing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/proofingprovider"
)

// SettingsStore persists what the wallet holds for an org's IPS tenant (whose
// id is the org's own) and the admin's flow selection. The API keys and webhook
// secret are sealed under the deployment IDENTITY_PROOFING_ENCRYPTION_KEY; with
// no key (cipher nil) nothing can be stored, so no org can be provisioned.
type SettingsStore struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewSettingsStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *SettingsStore {
	return &SettingsStore{db: db, audit: recorder, cipher: cipher}
}

// CanStoreSecrets reports whether the deployment has an encryption key, checked
// before provisioning so a missing key never leaves an orphaned IPS tenant.
func (s *SettingsStore) CanStoreSecrets() bool { return s.cipher != nil }

// APIKey returns the decrypted IPS API key of the org's tenant for mode (its
// test key for ModeTest), or ErrNotProvisioned.
func (s *SettingsStore) APIKey(ctx context.Context, orgID uuid.UUID, mode Mode) (string, error) {
	column := "api_key_ciphertext"
	if mode == ModeTest {
		column = "test_api_key_ciphertext"
	}
	var ciphertext []byte
	err := s.db.QueryRow(ctx,
		`SELECT `+column+` FROM org_identity_proofing_settings WHERE organization_id = $1`, orgID).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotProvisioned
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read api key org %s: %w", orgID, err)
	}
	if s.cipher == nil {
		return "", ErrNoEncryptionKey
	}
	plain, err := s.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("proofing: decrypt api key org %s: %w", orgID, err)
	}
	return string(plain), nil
}

// WebhookSecret returns the secret IPS signs the org's session changes with, or
// ErrNotProvisioned.
func (s *SettingsStore) WebhookSecret(ctx context.Context, orgID uuid.UUID) (string, error) {
	var ciphertext []byte
	err := s.db.QueryRow(ctx,
		`SELECT webhook_secret_ciphertext FROM org_identity_proofing_settings WHERE organization_id = $1`, orgID).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotProvisioned
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read webhook secret org %s: %w", orgID, err)
	}
	if s.cipher == nil {
		return "", ErrNoEncryptionKey
	}
	plain, err := s.cipher.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("proofing: decrypt webhook secret org %s: %w", orgID, err)
	}
	return string(plain), nil
}

// ProvisionedTenant is an org's IPS tenant as the wallet sets it up: the
// webhook secret and a live and a test API key.
type ProvisionedTenant struct {
	WebhookSecret string
	LiveKey       string
	TestKey       string
}

// Provision stores the org's IPS tenant, set up by create, and audits
// identity_proofing.provisioned in the same transaction. A per-org lock holds
// concurrent first uses (on any instance) back, and create is not called for an
// org that has its tenant already.
func (s *SettingsStore) Provision(ctx context.Context, orgID uuid.UUID, create func(context.Context) (ProvisionedTenant, error)) error {
	if s.cipher == nil {
		return ErrNoEncryptionKey
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if _, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"identity_proofing_provision:"+orgID.String()); err != nil {
			return fmt.Errorf("proofing: lock provisioning org %s: %w", orgID, err)
		}
		var exists bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_identity_proofing_settings WHERE organization_id = $1)`,
			orgID).Scan(&exists); err != nil {
			return fmt.Errorf("proofing: read settings org %s: %w", orgID, err)
		}
		if exists {
			return nil
		}
		t, err := create(ctx)
		if err != nil {
			return err
		}
		var sealed [3][]byte
		for i, v := range []string{t.LiveKey, t.TestKey, t.WebhookSecret} {
			if sealed[i], err = s.cipher.Encrypt([]byte(v)); err != nil {
				return fmt.Errorf("proofing: encrypt tenant secrets org %s: %w", orgID, err)
			}
		}
		if _, err := q.Exec(ctx, `INSERT INTO org_identity_proofing_settings
				(organization_id, api_key_ciphertext, test_api_key_ciphertext, webhook_secret_ciphertext)
				VALUES ($1, $2, $3, $4)`, orgID, sealed[0], sealed[1], sealed[2]); err != nil {
			return fmt.Errorf("proofing: save settings org %s: %w", orgID, err)
		}
		// The keys and secret never enter the audit log.
		return s.audit.Record(ctx, q, audit.IdentityProofingProvisioned,
			audit.Target{Type: audit.TargetIdentityProofingSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Created(map[string]any{"modes": []string{string(ModeLive), string(ModeTest)}}))
	})
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
