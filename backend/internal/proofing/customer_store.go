package proofing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// uniqueViolation is Postgres' SQLSTATE for a unique constraint violation.
const uniqueViolation = "23505"

// CustomerStore persists the org's customers and the flows assigned to each.
// Every mutation is audited in its own transaction.
type CustomerStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewCustomerStore(db database.DB, recorder audit.Recorder) *CustomerStore {
	return &CustomerStore{db: db, audit: recorder}
}

const customerColumns = `id, organization_id, name, paused_at, session_ttl_seconds, data_retention_days,
	COALESCE(display_name, ''), COALESCE(primary_color, ''), COALESCE(support_contact, ''), COALESCE(privacy_url, ''),
	logo_bytes IS NOT NULL, hide_powered_by, created_at, updated_at, allowed_redirect_origins,
	EXISTS (SELECT 1 FROM identity_proofing_api_keys k WHERE k.customer_id = identity_proofing_customers.id
		AND k.revoked_at IS NULL)`

// List returns the org's customers by name, each with its assigned flows.
func (s *CustomerStore) List(ctx context.Context, orgID uuid.UUID) ([]Customer, error) {
	rows, err := s.db.Query(ctx, `SELECT `+customerColumns+` FROM identity_proofing_customers
		WHERE organization_id = $1 ORDER BY lower(name)`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list customers org %s: %w", orgID, err)
	}
	out, err := pgx.CollectRows(rows, scanCustomer)
	if err != nil {
		return nil, fmt.Errorf("proofing: list customers org %s: %w", orgID, err)
	}
	flows, err := readCustomerFlows(ctx, s.db, orgID, nil)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Flows = flowsOf(flows, out[i].ID)
	}
	return out, nil
}

// Get returns one customer of the org, or ErrCustomerNotFound.
func (s *CustomerStore) Get(ctx context.Context, orgID, id uuid.UUID) (Customer, error) {
	return getCustomer(ctx, s.db, orgID, id)
}

// customerLock is how strongly lockCustomer locks a customer's row.
type customerLock string

const (
	// customerLockExclusive (FOR UPDATE) also waits on, and holds off, every
	// foreign-key check against the row (FOR KEY SHARE): a request's insert,
	// and a webhook delivery's a request write enqueues.
	customerLockExclusive customerLock = "FOR UPDATE"
	// customerLockSettings (FOR NO KEY UPDATE) lets foreign-key checks through
	// but conflicts with customerLockCreate.
	customerLockSettings customerLock = "FOR NO KEY UPDATE"
	// customerLockCreate (FOR SHARE) is a request insert's: it waits on a
	// retention change in flight, and holds the next one off until it commits.
	customerLockCreate customerLock = "FOR SHARE"
)

// lockCustomer locks one customer of the org's row as strength says for the
// rest of q's transaction. An unknown customer is ErrCustomerNotFound.
//
// Lock order: a request writer locks its request's row first, then reaches
// the customer's row by a foreign-key check (FOR KEY SHARE, enqueueWebhook).
// So a transaction holding the customer's row must never wait on a request's
// row while holding it in a mode that blocks that check:
//   - customerLockExclusive (SetStatus, Remove) only reads requests, or deletes
//     purged ones, which no writer that enqueues a webhook still touches.
//   - SaveSettings, which rewrites the customer's requests, holds
//     customerLockSettings, which a foreign-key check passes. Requests it then
//     locks in id order (refreshCustomerPurgeAt), as every statement that
//     waits on several requests' rows must (ClearExpiredProofedNames).
//   - A request insert takes customerLockCreate before any request row, so
//     none is created against a retention being changed (Create).
func lockCustomer(ctx context.Context, q database.Querier, orgID, id uuid.UUID, strength customerLock) error {
	var locked uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM identity_proofing_customers
		WHERE organization_id = $1 AND id = $2 `+string(strength), orgID, id).Scan(&locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCustomerNotFound
	}
	if err != nil {
		return fmt.Errorf("proofing: lock customer %s: %w", id, err)
	}
	return nil
}

func getCustomer(ctx context.Context, q database.Querier, orgID, id uuid.UUID) (Customer, error) {
	rows, err := q.Query(ctx, `SELECT `+customerColumns+` FROM identity_proofing_customers
		WHERE organization_id = $1 AND id = $2`, orgID, id)
	if err != nil {
		return Customer{}, fmt.Errorf("proofing: read customer %s: %w", id, err)
	}
	c, err := pgx.CollectExactlyOneRow(rows, scanCustomer)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrCustomerNotFound
	}
	if err != nil {
		return Customer{}, fmt.Errorf("proofing: read customer %s: %w", id, err)
	}
	flows, err := readCustomerFlows(ctx, q, orgID, &id)
	if err != nil {
		return Customer{}, err
	}
	c.Flows = flowsOf(flows, id)
	return c, nil
}

func scanCustomer(row pgx.CollectableRow) (Customer, error) {
	var c Customer
	var ttlSeconds int
	b := &c.Branding
	err := row.Scan(&c.ID, &c.OrganizationID, &c.Name, &c.PausedAt, &ttlSeconds, &c.Settings.DataRetentionDays,
		&b.DisplayName, &b.PrimaryColor, &b.SupportContact, &b.PrivacyURL, &b.HasLogo, &b.HidePoweredBy, &c.CreatedAt, &c.UpdatedAt, &c.RedirectOrigins, &c.HasAPIKey)
	c.Settings.SessionTTL = time.Duration(ttlSeconds) * time.Second
	return c, err
}

// Create stores a new customer with no flows assigned and audits
// identity_proofing.customer_created. A name the org already uses (any case) is
// ErrCustomerExists.
func (s *CustomerStore) Create(ctx context.Context, orgID, createdBy uuid.UUID, name string) (Customer, error) {
	var id uuid.UUID
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		err := q.QueryRow(ctx, `INSERT INTO identity_proofing_customers (organization_id, name, created_by)
			VALUES ($1, $2, $3) RETURNING id`, orgID, name, createdBy).Scan(&id)
		if isUniqueViolation(err) {
			return ErrCustomerExists
		}
		if err != nil {
			return fmt.Errorf("proofing: create customer org %s: %w", orgID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerCreated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": name}))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// Rename changes a customer's name and audits identity_proofing.customer_updated.
func (s *CustomerStore) Rename(ctx context.Context, orgID, id uuid.UUID, name string) (Customer, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		if before.Name == name {
			return nil
		}
		_, err = q.Exec(ctx, `UPDATE identity_proofing_customers SET name = $3, updated_at = now()
			WHERE organization_id = $1 AND id = $2`, orgID, id, name)
		if isUniqueViolation(err) {
			return ErrCustomerExists
		}
		if err != nil {
			return fmt.Errorf("proofing: rename customer %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerUpdated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"name": before.Name}, map[string]any{"name": name}))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// SetStatus pauses or resumes proofing for a customer and audits
// identity_proofing.customer_updated with its status before and after. Setting
// the state it already has changes and audits nothing.
func (s *CustomerStore) SetStatus(ctx context.Context, orgID, id uuid.UUID, status CustomerStatus) (Customer, error) {
	paused := status == CustomerPaused
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		if err := lockCustomer(ctx, q, orgID, id, customerLockExclusive); err != nil {
			return err
		}
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		if before.Paused() == paused {
			return nil
		}
		if paused {
			// Checked under the lock, so no second pause or resume interleaves.
			// A review reached after the pause stays decidable: a paused
			// customer refuses only new requests.
			var open bool
			if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity_proofing_requests r
				WHERE r.organization_id = $1 AND r.customer_id = $2 AND `+openReviewWhere+`)`, orgID, id).Scan(&open); err != nil {
				return fmt.Errorf("proofing: open reviews customer %s: %w", id, err)
			}
			if open {
				return ErrCustomerHasOpenReviews
			}
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_customers
			SET paused_at = CASE WHEN $3 THEN now() END, updated_at = now()
			WHERE organization_id = $1 AND id = $2`, orgID, id, paused); err != nil {
			return fmt.Errorf("proofing: pause customer %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerUpdated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"status": before.Status()}, map[string]any{"status": status}))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// SaveSettings replaces a customer's session settings and audits
// identity_proofing.customer_updated with before and after. Saving the settings
// it already has changes and audits nothing.
func (s *CustomerStore) SaveSettings(ctx context.Context, orgID, id uuid.UUID, settings CustomerSettings) (Customer, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// A request created meanwhile waits on this lock (customerLockCreate),
		// so it reads the new retention for its purge time. Not FOR UPDATE: a
		// request write holding a row refreshCustomerPurgeAt waits on must
		// still pass its webhook's foreign-key check (see lockCustomer).
		if err := lockCustomer(ctx, q, orgID, id, customerLockSettings); err != nil {
			return err
		}
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		if before.Settings == settings {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_customers
			SET session_ttl_seconds = $3, data_retention_days = $4, updated_at = now()
			WHERE organization_id = $1 AND id = $2`,
			orgID, id, int(settings.SessionTTL.Seconds()), settings.DataRetentionDays); err != nil {
			return fmt.Errorf("proofing: save customer settings %s: %w", id, err)
		}
		if before.Settings.DataRetentionDays != settings.DataRetentionDays {
			if err := refreshCustomerPurgeAt(ctx, q, orgID, id); err != nil {
				return err
			}
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerUpdated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(before.Settings.auditFields(), settings.auditFields()))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// SaveRedirectOrigins replaces the origins a customer's hosted pages may
// redirect to and be embedded on, and audits identity_proofing.customer_updated
// with before and after. Saving the origins it already has changes nothing.
func (s *CustomerStore) SaveRedirectOrigins(ctx context.Context, orgID, id uuid.UUID, origins []string) (Customer, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		if slices.Equal(before.RedirectOrigins, origins) {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_customers
			SET allowed_redirect_origins = $3, updated_at = now()
			WHERE organization_id = $1 AND id = $2`, orgID, id, origins); err != nil {
			return fmt.Errorf("proofing: save customer redirect origins %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerUpdated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(map[string]any{"allowedRedirectOrigins": before.RedirectOrigins},
				map[string]any{"allowedRedirectOrigins": origins}))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// SaveBranding replaces a customer's branding, and its logo as logo says, and
// audits identity_proofing.customer_updated with before and after (whether a
// logo is set, never its bytes).
func (s *CustomerStore) SaveBranding(ctx context.Context, orgID, id uuid.UUID, b CustomerBranding, logo LogoChange) (Customer, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		after := b
		after.HasLogo = before.Branding.HasLogo
		if logo.Replace {
			after.HasLogo = len(logo.Logo.Bytes) > 0
		}
		if after == before.Branding {
			return nil
		}
		if _, err := q.Exec(ctx, `UPDATE identity_proofing_customers SET
				display_name = NULLIF($3, ''), primary_color = NULLIF($4, ''),
				support_contact = NULLIF($5, ''), privacy_url = NULLIF($6, ''),
				logo_bytes = CASE WHEN $7 THEN $8 ELSE logo_bytes END,
				logo_content_type = CASE WHEN $7 THEN NULLIF($9, '') ELSE logo_content_type END,
				hide_powered_by = $10, updated_at = now()
			WHERE organization_id = $1 AND id = $2`,
			orgID, id, b.DisplayName, b.PrimaryColor, b.SupportContact, b.PrivacyURL,
			logo.Replace, nilIfEmpty(logo.Logo.Bytes), logo.Logo.ContentType, b.HidePoweredBy); err != nil {
			return fmt.Errorf("proofing: save customer branding %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerUpdated,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(before.Branding.auditFields(), after.auditFields()))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

func nilIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

// Logo returns a customer's logo, or ErrNoCustomerLogo.
func (s *CustomerStore) Logo(ctx context.Context, orgID, id uuid.UUID) (CustomerLogo, error) {
	var logo CustomerLogo
	var contentType *string
	err := s.db.QueryRow(ctx, `SELECT logo_bytes, logo_content_type FROM identity_proofing_customers
		WHERE organization_id = $1 AND id = $2`, orgID, id).Scan(&logo.Bytes, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return CustomerLogo{}, ErrCustomerNotFound
	}
	if err != nil {
		return CustomerLogo{}, fmt.Errorf("proofing: read customer logo %s: %w", id, err)
	}
	if contentType == nil {
		return CustomerLogo{}, ErrNoCustomerLogo
	}
	logo.ContentType = *contentType
	return logo, nil
}

// Remove deletes a customer with every request sent for it (their outcomes)
// and, by cascade, its flows, API keys and webhook, and audits
// identity_proofing.customer_removed. The audit trail of those requests stays,
// so each must be purged first (Service.RemoveCustomer): one that is not is
// ErrCustomerSessionsLeft, and nothing is removed. The customer's row is
// locked first, so no request can be created for it while this runs.
func (s *CustomerStore) Remove(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if err := lockCustomer(ctx, q, orgID, id, customerLockExclusive); err != nil {
			return err
		}
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		var left bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM identity_proofing_requests
			WHERE organization_id = $1 AND customer_id = $2 AND purged_at IS NULL)`, orgID, id).Scan(&left); err != nil {
			return fmt.Errorf("proofing: unpurged requests customer %s: %w", id, err)
		}
		if left {
			return ErrCustomerSessionsLeft
		}
		tag, err := q.Exec(ctx, `DELETE FROM identity_proofing_requests
			WHERE organization_id = $1 AND customer_id = $2`, orgID, id)
		if err != nil {
			return fmt.Errorf("proofing: purge customer requests %s: %w", id, err)
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_customers
			WHERE organization_id = $1 AND id = $2`, orgID, id); err != nil {
			return fmt.Errorf("proofing: remove customer %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerRemoved,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"name": before.Name, "requestsPurged": tag.RowsAffected()}))
	})
}

// SaveFlows replaces the flows assigned to a customer and audits
// identity_proofing.customer_flows_configured with before and after.
func (s *CustomerStore) SaveFlows(ctx context.Context, orgID, id uuid.UUID, sel FlowSelection) (Customer, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getCustomer(ctx, q, orgID, id)
		if err != nil {
			return err
		}
		if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_customer_flows WHERE customer_id = $1`, id); err != nil {
			return fmt.Errorf("proofing: clear customer flows %s: %w", id, err)
		}
		const insert = `INSERT INTO identity_proofing_customer_flows (organization_id, customer_id, flow_id, is_default)
			SELECT $1, $2, fid, fid = $4 FROM unnest($3::text[]) AS fid`
		if _, err := q.Exec(ctx, insert, orgID, id, sel.FlowIDs, sel.DefaultFlowID); err != nil {
			return fmt.Errorf("proofing: save customer flows %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingCustomerFlowsConfigured,
			audit.Target{Type: audit.TargetIdentityProofingCustomer, ID: id.String(), OrgID: &orgID},
			audit.Updated(before.Flows.auditFields(), sel.auditFields()))
	})
	if err != nil {
		return Customer{}, err
	}
	return s.Get(ctx, orgID, id)
}

// flowsOf is one customer's assignment out of readCustomerFlows; empty when it
// has none.
func flowsOf(flows map[uuid.UUID]FlowSelection, id uuid.UUID) FlowSelection {
	if sel, ok := flows[id]; ok {
		return sel
	}
	return FlowSelection{FlowIDs: []string{}}
}

// readCustomerFlows reads the flows assigned to the org's customers, or to one
// customer when customerID is set, keyed by customer.
func readCustomerFlows(ctx context.Context, q database.Querier, orgID uuid.UUID, customerID *uuid.UUID) (map[uuid.UUID]FlowSelection, error) {
	rows, err := q.Query(ctx, `SELECT customer_id, flow_id, is_default FROM identity_proofing_customer_flows
		WHERE organization_id = $1 AND ($2::uuid IS NULL OR customer_id = $2)
		ORDER BY customer_id, flow_id`, orgID, customerID)
	if err != nil {
		return nil, fmt.Errorf("proofing: read customer flows org %s: %w", orgID, err)
	}
	defer rows.Close()
	out := map[uuid.UUID]FlowSelection{}
	for rows.Next() {
		var id uuid.UUID
		var flowID string
		var isDefault bool
		if err := rows.Scan(&id, &flowID, &isDefault); err != nil {
			return nil, fmt.Errorf("proofing: scan customer flows org %s: %w", orgID, err)
		}
		sel := out[id]
		sel.FlowIDs = append(sel.FlowIDs, flowID)
		if isDefault {
			sel.DefaultFlowID = flowID
		}
		out[id] = sel
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: read customer flows org %s: %w", orgID, err)
	}
	return out, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
