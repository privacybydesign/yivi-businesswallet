package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/provisioner"
)

// maxRunErrorLength bounds what is kept from a failed run's error, so a driver
// that returns a long message cannot grow the settings row without limit.
const maxRunErrorLength = 500

// Store persists an organisation's provisioning configuration and the links
// recording which departments and members the sync owns. The source's client
// secret is encrypted at rest with the deployment provisioning key (`cipher`);
// when cipher is nil the deployment has no key configured and storing a secret is
// rejected, exactly like the per-org SMTP password.
type Store struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *Store {
	return &Store{db: db, audit: recorder, cipher: cipher}
}

// GetSettings returns the non-secret view of an organisation's configuration.
func (s *Store) GetSettings(ctx context.Context, orgID uuid.UUID) (Settings, error) {
	const query = `SELECT source, enabled, tenant_id, client_id,
		client_secret_ciphertext IS NOT NULL, group_id, admin_groups,
		last_run_at, last_run_status, last_run_error, updated_at
		FROM org_provisioning_settings WHERE organization_id = $1`
	var (
		out         Settings
		adminGroups []byte
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&out.Source, &out.Enabled, &out.TenantID, &out.ClientID, &out.HasClientSecret,
		&out.GroupID, &adminGroups, &out.LastRunAt, &out.LastRunStatus, &out.LastRunError, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{Source: provisioner.SourceEntra, AdminGroupIDs: []string{}}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("provisioning: get settings org %s: %w", orgID, err)
	}
	out.Configured = true
	out.AdminGroupIDs, err = decodeGroups(adminGroups)
	if err != nil {
		return Settings{}, fmt.Errorf("provisioning: get settings org %s: %w", orgID, err)
	}
	return out, nil
}

// Save replaces an organisation's configuration with the (already normalized)
// input and audits the change in the same transaction.
func (s *Store) Save(ctx context.Context, orgID uuid.UUID, in SettingsInput) (Settings, error) {
	groups := in.AdminGroupIDs
	if groups == nil {
		groups = []string{}
	}
	encodedGroups, err := json.Marshal(groups)
	if err != nil {
		return Settings{}, fmt.Errorf("provisioning: encode admin groups org %s: %w", orgID, err)
	}

	// A nil secret keeps what is stored, a pointer to "" clears it, and anything
	// else is (re)encrypted. setSecret carries which of the three this is into the
	// statement, so "leave it alone" cannot be confused with "clear it".
	var secretArg any
	setSecret := false
	if in.ClientSecret != nil {
		setSecret = true
		if *in.ClientSecret != "" {
			if s.cipher == nil {
				return Settings{}, ErrNoEncryptionKey
			}
			ct, err := s.cipher.Encrypt([]byte(*in.ClientSecret))
			if err != nil {
				return Settings{}, err
			}
			secretArg = ct
		}
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		// Read the current row inside the transaction (and lock it) so the audited
		// {before, after} diff is the change this save actually made.
		before, err := readAuditable(ctx, q, orgID)
		if err != nil {
			return err
		}

		const upsert = `INSERT INTO org_provisioning_settings
			(organization_id, source, enabled, tenant_id, client_id, group_id, admin_groups, client_secret_ciphertext)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (organization_id) DO UPDATE SET
				source = EXCLUDED.source, enabled = EXCLUDED.enabled,
				tenant_id = EXCLUDED.tenant_id, client_id = EXCLUDED.client_id,
				group_id = EXCLUDED.group_id, admin_groups = EXCLUDED.admin_groups,
				client_secret_ciphertext = CASE WHEN $9 THEN EXCLUDED.client_secret_ciphertext
				                                ELSE org_provisioning_settings.client_secret_ciphertext END,
				updated_at = now()`
		if _, err := q.Exec(ctx, upsert, orgID, string(in.Source), in.Enabled, in.TenantID,
			in.ClientID, in.GroupID, encodedGroups, secretArg, setSecret); err != nil {
			return fmt.Errorf("provisioning: save settings org %s: %w", orgID, err)
		}

		after := map[string]any{
			"source":        string(in.Source),
			"enabled":       in.Enabled,
			"tenantId":      in.TenantID,
			"clientId":      in.ClientID,
			"groupId":       in.GroupID,
			"adminGroupIds": groups,
		}
		return s.audit.Record(ctx, q, audit.ProvisioningSettingsUpdated,
			audit.Target{Type: audit.TargetProvisioningSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Updated(before, after))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, orgID)
}

// readAuditable reads the settings fields that go in an audit diff. A row that
// does not exist yet reads as nil, which renders as a create rather than an
// update against an invented empty configuration.
func readAuditable(ctx context.Context, q database.Querier, orgID uuid.UUID) (map[string]any, error) {
	const query = `SELECT source, enabled, tenant_id, client_id, group_id, admin_groups
		FROM org_provisioning_settings WHERE organization_id = $1 FOR UPDATE`
	var (
		source, tenantID, clientID, groupID string
		enabled                             bool
		adminGroups                         []byte
	)
	err := q.QueryRow(ctx, query, orgID).Scan(&source, &enabled, &tenantID, &clientID, &groupID, &adminGroups)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("provisioning: read settings org %s: %w", orgID, err)
	}
	groups, err := decodeGroups(adminGroups)
	if err != nil {
		return nil, fmt.Errorf("provisioning: read settings org %s: %w", orgID, err)
	}
	return map[string]any{
		"source":        source,
		"enabled":       enabled,
		"tenantId":      tenantID,
		"clientId":      clientID,
		"groupId":       groupID,
		"adminGroupIds": groups,
	}, nil
}

// SourceConfig resolves the source and its credentials for a sync, decrypting
// the client secret. It returns ErrNotConfigured when the organisation has no
// row and ErrDisabled when provisioning is switched off, so a caller can tell a
// misconfiguration from a deliberate "not now".
func (s *Store) SourceConfig(ctx context.Context, orgID uuid.UUID) (provisioner.SourceID, provisioner.Config, error) {
	const query = `SELECT source, enabled, tenant_id, client_id, group_id, admin_groups, client_secret_ciphertext
		FROM org_provisioning_settings WHERE organization_id = $1`
	var (
		source      provisioner.SourceID
		enabled     bool
		cfg         provisioner.Config
		adminGroups []byte
		ciphertext  []byte
	)
	err := s.db.QueryRow(ctx, query, orgID).Scan(
		&source, &enabled, &cfg.TenantID, &cfg.ClientID, &cfg.GroupID, &adminGroups, &ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", provisioner.Config{}, ErrNotConfigured
	}
	if err != nil {
		return "", provisioner.Config{}, fmt.Errorf("provisioning: source config org %s: %w", orgID, err)
	}
	if !enabled {
		return "", provisioner.Config{}, ErrDisabled
	}
	if cfg.AdminGroupIDs, err = decodeGroups(adminGroups); err != nil {
		return "", provisioner.Config{}, fmt.Errorf("provisioning: source config org %s: %w", orgID, err)
	}
	if len(ciphertext) > 0 {
		if s.cipher == nil {
			return "", provisioner.Config{}, errors.New("provisioning: client secret stored but no encryption key configured")
		}
		secret, err := s.cipher.Decrypt(ciphertext)
		if err != nil {
			return "", provisioner.Config{}, err
		}
		cfg.ClientSecret = string(secret)
	}
	return source, cfg, nil
}

// ListEnabled returns the organisations whose provisioning is switched on, for
// the scheduler to walk.
func (s *Store) ListEnabled(ctx context.Context) ([]uuid.UUID, error) {
	const query = `SELECT organization_id FROM org_provisioning_settings WHERE enabled ORDER BY organization_id`
	rows, err := s.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("provisioning: list enabled: %w", err)
	}
	defer rows.Close()

	orgIDs := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("provisioning: list enabled scan: %w", err)
		}
		orgIDs = append(orgIDs, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: list enabled rows: %w", err)
	}
	return orgIDs, nil
}

// RecordRun stores the outcome of a sync on the settings row and audits it. A
// failed run records the driver's error so the settings screen can show why,
// bounded so a verbose source cannot grow the row.
func (s *Store) RecordRun(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, result Result, runErr error) error {
	status := RunSucceeded
	message := ""
	action := audit.ProvisioningRunCompleted
	if runErr != nil {
		status = RunFailed
		message = truncate(runErr.Error(), maxRunErrorLength)
		action = audit.ProvisioningRunFailed
	}

	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE org_provisioning_settings
			SET last_run_at = now(), last_run_status = $2, last_run_error = $3, updated_at = now()
			WHERE organization_id = $1`
		if _, err := q.Exec(ctx, update, orgID, status, message); err != nil {
			return fmt.Errorf("provisioning: record run org %s: %w", orgID, err)
		}

		metadata := result.auditSnapshot(source)
		if runErr != nil {
			metadata = map[string]any{"source": string(source), "error": message}
		}
		return s.audit.Record(ctx, q, action,
			audit.Target{Type: audit.TargetProvisioningSettings, ID: orgID.String(), OrgID: &orgID},
			audit.Created(metadata))
	})
}

// MemberLinks returns the people this sync owns in an organisation, keyed by the
// source's id for them.
func (s *Store) MemberLinks(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID) (map[string]Link, error) {
	const query = `SELECT external_id, email FROM provisioned_members
		WHERE organization_id = $1 AND source = $2`
	rows, err := s.db.Query(ctx, query, orgID, string(source))
	if err != nil {
		return nil, fmt.Errorf("provisioning: member links org %s: %w", orgID, err)
	}
	defer rows.Close()

	links := map[string]Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ExternalID, &l.Email); err != nil {
			return nil, fmt.Errorf("provisioning: member links scan: %w", err)
		}
		links[l.ExternalID] = l
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: member links rows: %w", err)
	}
	return links, nil
}

// LinkMember records that this sync owns the invitation/membership for email.
func (s *Store) LinkMember(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID, email string) error {
	const insert = `INSERT INTO provisioned_members (organization_id, source, external_id, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, source, external_id) DO UPDATE SET email = EXCLUDED.email`
	if _, err := s.db.Exec(ctx, insert, orgID, string(source), externalID, email); err != nil {
		return fmt.Errorf("provisioning: link member org %s: %w", orgID, err)
	}
	return nil
}

// UnlinkMember drops the ownership record, after the membership or invitation it
// pointed at has been taken away.
func (s *Store) UnlinkMember(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID string) error {
	const del = `DELETE FROM provisioned_members
		WHERE organization_id = $1 AND source = $2 AND external_id = $3`
	if _, err := s.db.Exec(ctx, del, orgID, string(source), externalID); err != nil {
		return fmt.Errorf("provisioning: unlink member org %s: %w", orgID, err)
	}
	return nil
}

// DepartmentLinks returns the departments this sync created, keyed by the
// source's id for them.
func (s *Store) DepartmentLinks(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID) (map[string]uuid.UUID, error) {
	const query = `SELECT external_id, department_id FROM provisioned_departments
		WHERE organization_id = $1 AND source = $2`
	rows, err := s.db.Query(ctx, query, orgID, string(source))
	if err != nil {
		return nil, fmt.Errorf("provisioning: department links org %s: %w", orgID, err)
	}
	defer rows.Close()

	links := map[string]uuid.UUID{}
	for rows.Next() {
		var (
			externalID string
			deptID     uuid.UUID
		)
		if err := rows.Scan(&externalID, &deptID); err != nil {
			return nil, fmt.Errorf("provisioning: department links scan: %w", err)
		}
		links[externalID] = deptID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("provisioning: department links rows: %w", err)
	}
	return links, nil
}

// LinkDepartment records that departmentID mirrors the source department
// externalID.
func (s *Store) LinkDepartment(ctx context.Context, orgID uuid.UUID, source provisioner.SourceID, externalID string, departmentID uuid.UUID) error {
	const insert = `INSERT INTO provisioned_departments (organization_id, source, external_id, department_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, source, external_id) DO UPDATE SET department_id = EXCLUDED.department_id`
	if _, err := s.db.Exec(ctx, insert, orgID, string(source), externalID, departmentID); err != nil {
		return fmt.Errorf("provisioning: link department org %s: %w", orgID, err)
	}
	return nil
}

func decodeGroups(raw []byte) ([]string, error) {
	groups := []string{}
	if len(raw) == 0 {
		return groups, nil
	}
	if err := json.Unmarshal(raw, &groups); err != nil {
		return nil, fmt.Errorf("decode admin groups: %w", err)
	}
	return groups, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Cut back to a rune boundary: a byte slice through a multi-byte rune is
	// invalid UTF-8, which Postgres rejects outright rather than storing — so a
	// truncated error message would fail the write that was recording a failure.
	cut := s[:max]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
