package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
)

// Store persists verification_templates and verification_sessions and writes
// each mutation's audit event in the same transaction.
type Store struct {
	db    database.DB
	audit audit.Recorder
}

func NewStore(db database.DB, recorder audit.Recorder) *Store {
	return &Store{db: db, audit: recorder}
}

const templateColumns = `id, organization_id, name, vct, claims, purpose, created_at, updated_at`

func scanTemplate(row pgx.Row) (Template, error) {
	var (
		t         Template
		claimsRaw []byte
	)
	if err := row.Scan(&t.ID, &t.OrganizationID, &t.Name, &t.VCT, &claimsRaw, &t.Purpose, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return Template{}, err
	}
	if err := json.Unmarshal(claimsRaw, &t.Claims); err != nil {
		return Template{}, fmt.Errorf("verification: decode template claims: %w", err)
	}
	return t, nil
}

// ListTemplates returns an organisation's templates, oldest first, so the one
// seeded or created first is the default pick.
func (s *Store) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	const q = `SELECT ` + templateColumns + ` FROM verification_templates WHERE organization_id = $1 ORDER BY created_at, id`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("verification: list templates: %w", err)
	}
	defer rows.Close()

	templates := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, fmt.Errorf("verification: list templates scan: %w", err)
		}
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("verification: list templates rows: %w", err)
	}
	return templates, nil
}

func (s *Store) GetTemplate(ctx context.Context, orgID, id uuid.UUID) (Template, error) {
	const q = `SELECT ` + templateColumns + ` FROM verification_templates WHERE organization_id = $1 AND id = $2`
	t, err := scanTemplate(s.db.QueryRow(ctx, q, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, fmt.Errorf("verification: get template %s: %w", id, err)
	}
	return t, nil
}

// CreateTemplate inserts a template and audits it, in one transaction.
func (s *Store) CreateTemplate(ctx context.Context, orgID uuid.UUID, in Template) (Template, error) {
	claims, err := json.Marshal(in.Claims)
	if err != nil {
		return Template{}, fmt.Errorf("verification: encode template claims: %w", err)
	}

	var id uuid.UUID
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO verification_templates (organization_id, name, vct, claims, purpose)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`
		if err := q.QueryRow(ctx, insert, orgID, in.Name, in.VCT, claims, in.Purpose).Scan(&id); err != nil {
			return fmt.Errorf("verification: create template: %w", err)
		}
		return s.audit.Record(ctx, q, audit.VerificationTemplateCreated,
			audit.Target{Type: audit.TargetVerificationTemplate, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{"name": in.Name, "vct": in.VCT, "claims": in.Claims, "purpose": in.Purpose}))
	})
	if err != nil {
		return Template{}, err
	}
	return s.GetTemplate(ctx, orgID, id)
}

// DeleteTemplate removes a template and audits it. Sessions run from it keep
// their copy of the name (template_id is set NULL by the schema).
func (s *Store) DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error {
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		const del = `DELETE FROM verification_templates WHERE organization_id = $1 AND id = $2 RETURNING name`
		var name string
		if err := q.QueryRow(ctx, del, orgID, id).Scan(&name); errors.Is(err, pgx.ErrNoRows) {
			return ErrTemplateNotFound
		} else if err != nil {
			return fmt.Errorf("verification: delete template %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.VerificationTemplateDeleted,
			audit.Target{Type: audit.TargetVerificationTemplate, ID: id.String(), OrgID: &orgID},
			audit.Deleted(map[string]any{"name": name}))
	})
}

const sessionColumns = `id, organization_id, template_id, template_name, vct, transaction_id, wallet_link,
	status, started_by_user_id, claims, checks, valid, completed_at, expires_at, created_at`

func scanSession(row pgx.Row) (Session, error) {
	var (
		s         Session
		claimsRaw []byte
		checksRaw []byte
	)
	if err := row.Scan(
		&s.ID, &s.OrganizationID, &s.TemplateID, &s.TemplateName, &s.VCT, &s.TransactionID, &s.WalletLink,
		&s.Status, &s.StartedByUserID, &claimsRaw, &checksRaw, &s.Valid, &s.CompletedAt, &s.ExpiresAt, &s.CreatedAt,
	); err != nil {
		return Session{}, err
	}
	if len(claimsRaw) > 0 {
		if err := json.Unmarshal(claimsRaw, &s.Claims); err != nil {
			return Session{}, fmt.Errorf("verification: decode session claims: %w", err)
		}
	}
	if len(checksRaw) > 0 {
		if err := json.Unmarshal(checksRaw, &s.Checks); err != nil {
			return Session{}, fmt.Errorf("verification: decode session checks: %w", err)
		}
	}
	return s, nil
}

// NewSession is what CreateSession persists once the verifier has minted the
// request: the template snapshot and the verifier's handles.
type NewSession struct {
	Template      Template
	TransactionID string
	WalletLink    string
	StartedBy     uuid.UUID
	ExpiresAt     time.Time
}

// CreateSession records a started verification and audits it. The audit
// metadata names the template and credential type, never the verifier handles.
func (s *Store) CreateSession(ctx context.Context, in NewSession) (Session, error) {
	orgID := in.Template.OrganizationID
	var id uuid.UUID
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		const insert = `INSERT INTO verification_sessions
			(organization_id, template_id, template_name, vct, transaction_id, wallet_link, started_by_user_id, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
		if err := q.QueryRow(ctx, insert,
			orgID, in.Template.ID, in.Template.Name, in.Template.VCT, in.TransactionID, in.WalletLink, in.StartedBy, in.ExpiresAt,
		).Scan(&id); err != nil {
			return fmt.Errorf("verification: create session: %w", err)
		}
		return s.audit.Record(ctx, q, audit.VerificationStarted,
			audit.Target{Type: audit.TargetVerificationSession, ID: id.String(), OrgID: &orgID},
			audit.Created(map[string]any{"template": in.Template.Name, "vct": in.Template.VCT}))
	})
	if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, orgID, id)
}

func (s *Store) GetSession(ctx context.Context, orgID, id uuid.UUID) (Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM verification_sessions WHERE organization_id = $1 AND id = $2`
	sess, err := scanSession(s.db.QueryRow(ctx, q, orgID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("verification: get session %s: %w", id, err)
	}
	return sess, nil
}

// ListSessions is the organisation's check history, newest first.
func (s *Store) ListSessions(ctx context.Context, orgID uuid.UUID) ([]Session, error) {
	const q = `SELECT ` + sessionColumns + ` FROM verification_sessions WHERE organization_id = $1 ORDER BY created_at DESC, id`
	rows, err := s.db.Query(ctx, q, orgID)
	if err != nil {
		return nil, fmt.Errorf("verification: list sessions: %w", err)
	}
	defer rows.Close()

	sessions := []Session{}
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("verification: list sessions scan: %w", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("verification: list sessions rows: %w", err)
	}
	return sessions, nil
}

// CompleteSession stores the disclosure and its grading on a still-pending
// session and audits the outcome; a session already completed is left as it
// is (the first poll to see the result wins, later ones read it back). The
// audit metadata carries the verdict and the failed checks, not the claims.
func (s *Store) CompleteSession(ctx context.Context, orgID, id uuid.UUID, res Result) (Session, error) {
	claims, err := json.Marshal(res.Claims)
	if err != nil {
		return Session{}, fmt.Errorf("verification: encode session claims: %w", err)
	}
	checks, err := json.Marshal(res.Checks)
	if err != nil {
		return Session{}, fmt.Errorf("verification: encode session checks: %w", err)
	}

	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		const update = `UPDATE verification_sessions
			SET status = $3, claims = $4, checks = $5, valid = $6, completed_at = now()
			WHERE organization_id = $1 AND id = $2 AND status = $7
			RETURNING template_name`
		var name string
		err := q.QueryRow(ctx, update, orgID, id, StatusCompleted, claims, checks, res.Valid, StatusPending).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verification: complete session %s: %w", id, err)
		}
		return s.audit.Record(ctx, q, audit.VerificationCompleted,
			audit.Target{Type: audit.TargetVerificationSession, ID: id.String(), OrgID: &orgID},
			audit.Updated(
				map[string]any{"status": StatusPending},
				map[string]any{"status": StatusCompleted, "template": name, "valid": res.Valid, "failedChecks": failedChecks(res.Checks)},
			))
	})
	if err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, orgID, id)
}

func failedChecks(checks []Check) []string {
	failed := []string{}
	for _, c := range checks {
		if !c.Passed {
			failed = append(failed, c.Name)
		}
	}
	return failed
}
