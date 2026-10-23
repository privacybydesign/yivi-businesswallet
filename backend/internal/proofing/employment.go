package proofing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/employment"
)

// EmploymentMode is whether a flow has the employment step: once their
// identity is approved, its subject must upload their UWV
// Verzekeringsbericht, the PDF UWV seals, which the wallet checks itself
// (package employment). Flows live in the engine, which knows nothing of it,
// so the wallet keeps this per org and flow.
type EmploymentMode string

const (
	EmploymentOff      EmploymentMode = "off"
	EmploymentRequired EmploymentMode = "required"
)

func (m EmploymentMode) valid() bool {
	return m == EmploymentOff || m == EmploymentRequired
}

// asked is the mode as read from a row; "" (a store-less service) is off.
func (m EmploymentMode) asked() bool { return m == EmploymentRequired }

// EmploymentWindow is how long after the identity was approved the subject
// can still upload their statement, on the page that ran the session.
const EmploymentWindow = time.Hour

var (
	// ErrEmploymentNotAsked is an upload on a request whose flow asks for no
	// statement.
	ErrEmploymentNotAsked = errors.New("proofing: the request asks for no Verzekeringsbericht")
	// ErrEmploymentClosed is an upload before the identity was approved, once
	// EmploymentWindow passed, or after a statement was kept already.
	ErrEmploymentClosed = errors.New("proofing: a Verzekeringsbericht is kept once, within EmploymentWindow of an approved identity")
	// ErrEmploymentNeedsPage is an employment flow sent where its subject gets
	// no page to upload on: by mail, or as a bare deep link.
	ErrEmploymentNeedsPage = errors.New("proofing: a flow that asks for the Verzekeringsbericht runs on a page: show it on screen or send a hosted link")
	// ErrNoEmploymentChecker is an upload on a deployment without a checker.
	ErrNoEmploymentChecker = errors.New("proofing: no Verzekeringsbericht checker configured")
)

// Employment is what a request keeps of the subject's Verzekeringsbericht:
// the day UWV made it and the relations current then (employment.Current).
// Never past relations, the holder, the hours or the wages: the purpose is the
// occupational status now.
type Employment struct {
	// StatementDate is the day UWV made the statement (YYYY-MM-DD).
	StatementDate string `json:"statementDate"`
	// Employed is a current relation that is not a benefit
	// (employment.Statement.Employed). Without one the request goes to review
	// (reviewReason).
	Employed bool `json:"employed"`
	// Relations are the current ones; empty when none is.
	Relations []EmploymentRelation `json:"relations"`
}

// EmploymentRelation is one current employer or paying body under one
// contract: From is the first day of its first month on the statement (since
// when), To the last day of its newest month reported (YYYY-MM-DD).
type EmploymentRelation struct {
	// Kind is employment, benefit (paid by UWV), sick_via_employer or
	// other_income (paid without hours worked).
	Kind             string `json:"kind"`
	Employer         string `json:"employer"`
	PayrollTaxNumber string `json:"payrollTaxNumber"`
	ContractForm     string `json:"contractForm"`
	From             string `json:"from"`
	To               string `json:"to"`
}

func newEmployment(st *employment.Statement) Employment {
	current := st.Current()
	out := Employment{
		StatementDate: st.Date.Format(time.DateOnly), Employed: st.Employed(),
		Relations: make([]EmploymentRelation, 0, len(current)),
	}
	for _, r := range current {
		out.Relations = append(out.Relations, EmploymentRelation{
			Kind: string(r.Kind), Employer: r.Employer, PayrollTaxNumber: r.PayrollTaxNumber, ContractForm: r.ContractForm,
			From: r.From.Format(time.DateOnly), To: r.To.Format(time.DateOnly),
		})
	}
	return out
}

// Why the wallet puts an approved request in review itself, from its kept
// Verzekeringsbericht: it lists no current job, a current UWV benefit, or
// other current income without hours worked.
const (
	ReviewNoCurrentEmployment = "no_current_employment"
	ReviewCurrentBenefit      = "current_benefit"
	ReviewCurrentOtherIncome  = "current_other_income"
)

// reviewReason is why emp sends an approved request to review; "" for none.
// No current job weighs most, then a UWV benefit, then other income.
func (e Employment) reviewReason() string {
	if !e.Employed {
		return ReviewNoCurrentEmployment
	}
	if e.hasKind(employment.KindBenefit) {
		return ReviewCurrentBenefit
	}
	if e.hasKind(employment.KindOtherIncome) {
		return ReviewCurrentOtherIncome
	}
	return ""
}

func (e Employment) hasKind(kind employment.Kind) bool {
	return slices.ContainsFunc(e.Relations, func(r EmploymentRelation) bool { return r.Kind == string(kind) })
}

// ErrorEmploymentNotProvided is the error code of an approved request whose
// subject kept no Verzekeringsbericht within EmploymentWindow: a flow that
// requires one is not passed without it (RejectMissingEmployment).
const ErrorEmploymentNotProvided = "EMPLOYMENT_NOT_PROVIDED"

// webhookEmploymentKey is where a session.employment_added delivery carries
// the employment; Purge drops it from the request's deliveries.
const webhookEmploymentKey = "employment"

// EmploymentVerdict is what became of an uploaded statement: Employment when
// kept, else Reason (an employment.Reason*) and, for a seal that did not
// verify, the first FailedCheck.
type EmploymentVerdict struct {
	FileName    string
	Employment  *Employment
	Reason      string
	FailedCheck string
}

// employmentChecker decides on one statement (implemented by
// *employment.Checker).
type employmentChecker interface {
	Check(ctx context.Context, pdf []byte, holder diploma.Person) (employment.Outcome, error)
}

// SetEmploymentChecker sets what checks uploaded statements; call before
// serving. Without one, an upload is ErrNoEmploymentChecker.
func (s *Service) SetEmploymentChecker(c employmentChecker) { s.employmentChecker = c }

type flowEmploymentStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (EmploymentMode, error)
	All(ctx context.Context, orgID uuid.UUID) (map[string]EmploymentMode, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, mode EmploymentMode) (EmploymentMode, error)
}

// FlowEmploymentStore persists each flow's EmploymentMode per org.
type FlowEmploymentStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewFlowEmploymentStore(db database.DB, recorder audit.Recorder) *FlowEmploymentStore {
	return &FlowEmploymentStore{db: db, audit: recorder}
}

// Get returns the flow's mode; a flow never set is EmploymentOff.
func (s *FlowEmploymentStore) Get(ctx context.Context, orgID uuid.UUID, flowID string) (EmploymentMode, error) {
	return getFlowEmployment(ctx, s.db, orgID, flowID)
}

func getFlowEmployment(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (EmploymentMode, error) {
	var mode EmploymentMode
	err := q.QueryRow(ctx, `SELECT employment FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmploymentOff, nil
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read employment setting flow %s: %w", flowID, err)
	}
	return mode, nil
}

// All returns every flow of the org that asks for the statement; a flow
// missing is off.
func (s *FlowEmploymentStore) All(ctx context.Context, orgID uuid.UUID) (map[string]EmploymentMode, error) {
	rows, err := s.db.Query(ctx, `SELECT flow_id, employment FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND employment <> 'off'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list employment settings org %s: %w", orgID, err)
	}

	out := map[string]EmploymentMode{}
	var flowID string
	var mode EmploymentMode
	if _, err := pgx.ForEachRow(rows, []any{&flowID, &mode}, func() error {
		out[flowID] = mode
		return nil
	}); err != nil {
		return nil, fmt.Errorf("proofing: list employment settings org %s: %w", orgID, err)
	}
	return out, nil
}

// Save sets the flow's mode and audits
// identity_proofing.flow_employment_configured with before and after. Saving
// what it already has changes nothing.
func (s *FlowEmploymentStore) Save(ctx context.Context, orgID uuid.UUID, flowID string, mode EmploymentMode) (EmploymentMode, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowEmployment(ctx, q, orgID, flowID)
		if err != nil {
			return err
		}
		if before == mode {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, employment)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, flow_id) DO UPDATE SET employment = EXCLUDED.employment, updated_at = now()`,
			orgID, flowID, string(mode)); err != nil {
			return fmt.Errorf("proofing: save employment setting flow %s: %w", flowID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowEmploymentConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(map[string]any{"employment": string(before)}, map[string]any{"employment": string(mode)}))
	})
	if err != nil {
		return "", err
	}
	return mode, nil
}

// AddEmployment seals emp on req, audited identity_proofing.employment_added
// (without the relations), and sends session.employment_added. A statement
// without a current job, or with a current benefit or other income, also moves the approved
// request to needs_review (reviewReason), audited identity_proofing.needs_review, and sends
// session.review_opened: the identity is proven, the rest is for a reviewer.
// It is false, and changes nothing, when the request holds a statement already
// or was erased.
func (s *RequestStore) AddEmployment(ctx context.Context, req Request, emp Employment) (bool, error) {
	if s.cipher == nil {
		return false, ErrNoEncryptionKey
	}
	plain, err := json.Marshal(emp)
	if err != nil {
		return false, fmt.Errorf("proofing: marshal employment request %s: %w", req.ID, err)
	}

	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return false, fmt.Errorf("proofing: encrypt employment request %s: %w", req.ID, err)
	}

	added := false
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
			SET employment_ciphertext = $2, employment_added_at = now(), updated_at = now()
			WHERE id = $1 AND purged_at IS NULL AND employment_ciphertext IS NULL`, req.ID, sealed)
		if err != nil {
			return fmt.Errorf("proofing: add employment request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		added = true

		if err := s.audit.Record(ctx, q, audit.IdentityProofingEmploymentAdded,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{"statementDate": emp.StatementDate}))); err != nil {
			return err
		}

		data := sessionEventData(req, req.Status)
		data[webhookEmploymentKey] = emp
		if err := enqueueRequestWebhook(ctx, q, req, EventSessionEmploymentAdded, data); err != nil {
			return err
		}
		reason := emp.reviewReason()
		if reason == "" {
			return nil
		}
		return openWalletReview(ctx, q, s.audit, req, reason)
	})
	if err != nil {
		return false, err
	}
	return added, nil
}

// openWalletReview moves an approved req to needs_review for reason, which the
// engine never sees: its session stays decided, and a reviewer decides the
// request in the wallet (decideWalletReview).
func openWalletReview(ctx context.Context, q database.Querier, recorder audit.Recorder, req Request, reason string) error {
	tag, err := q.Exec(ctx, `UPDATE identity_proofing_requests
		SET status = 'needs_review', review_reason = $2, updated_at = now()
		WHERE id = $1 AND status = 'approved' AND purged_at IS NULL`, req.ID, reason)
	if err != nil {
		return fmt.Errorf("proofing: open review request %s: %w", req.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := refreshPurgeAt(ctx, q, req.ID); err != nil {
		return err
	}
	if err := recorder.Record(ctx, q, audit.IdentityProofingNeedsReview,
		audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
		audit.Updated(req.auditFields(map[string]any{"status": string(StatusApproved)}),
			req.auditFields(map[string]any{"status": string(StatusNeedsReview), "reviewReason": reason}))); err != nil {
		return err
	}
	inReview := req
	inReview.ReviewReason = reason
	return enqueueRequestWebhook(ctx, q, req, EventSessionReviewOpened, sessionEventData(inReview, StatusNeedsReview))
}

// missingEmploymentWhere is an approved request r that asks for a
// Verzekeringsbericht and holds none, not erased or cancelled; it is due
// EmploymentWindow after its approval ($2, in seconds).
const missingEmploymentWhere = `r.employment = 'required' AND r.status = 'approved'
	AND r.employment_ciphertext IS NULL AND r.purged_at IS NULL AND r.cancelled_at IS NULL
	AND r.completed_at IS NOT NULL`

// RejectMissingEmployment rejects up to limit approved requests whose subject
// kept no Verzekeringsbericht within EmploymentWindow, with
// ErrorEmploymentNotProvided: audited identity_proofing.rejected, webhook
// session.failed. The engine approved the identity and keeps saying so; the
// rejection is the wallet's, and reconcile leaves it (tryReconcile). It
// returns how many it rejected.
func (s *RequestStore) RejectMissingEmployment(ctx context.Context, now time.Time, limit int) (int, error) {
	rejected := 0
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		reqs, err := s.rejectDueWithoutEmployment(ctx, q, now, limit)
		if err != nil {
			return err
		}
		for _, req := range reqs {
			if err := s.recordEmploymentNotProvided(ctx, q, req); err != nil {
				return err
			}
		}
		rejected = len(reqs)
		return nil
	})
	return rejected, err
}

// rejectDueWithoutEmployment marks up to limit due requests without a
// Verzekeringsbericht rejected, and returns them as they now are.
func (s *RequestStore) rejectDueWithoutEmployment(ctx context.Context, q database.Querier, now time.Time, limit int) ([]Request, error) {
	idRows, err := q.Query(ctx, `WITH due AS (
			SELECT r.id FROM identity_proofing_requests r
			WHERE `+missingEmploymentWhere+` AND r.completed_at + make_interval(secs => $2) <= $1
			ORDER BY r.completed_at LIMIT $3 FOR UPDATE SKIP LOCKED
		)
		UPDATE identity_proofing_requests e SET status = 'rejected', error_code = $4, updated_at = now()
		FROM due WHERE e.id = due.id RETURNING e.id`, now, EmploymentWindow.Seconds(), limit, ErrorEmploymentNotProvided)
	if err != nil {
		return nil, fmt.Errorf("proofing: reject missing employment: %w", err)
	}
	ids, err := pgx.CollectRows(idRows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return nil, fmt.Errorf("proofing: reject missing employment: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if err := refreshPurgeAt(ctx, q, ids...); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT `+requestColumns+requestFrom+` WHERE r.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("proofing: reject missing employment: %w", err)
	}
	reqs, err := s.collectRequests(rows)
	if err != nil {
		return nil, fmt.Errorf("proofing: reject missing employment: %w", err)
	}
	return reqs, nil
}

// recordEmploymentNotProvided audits and sends the rejection of req for
// ErrorEmploymentNotProvided.
func (s *RequestStore) recordEmploymentNotProvided(ctx context.Context, q database.Querier, req Request) error {
	if err := s.audit.Record(ctx, q, audit.IdentityProofingRejected,
		audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
		audit.Updated(req.auditFields(map[string]any{"status": string(StatusApproved)}),
			req.auditFields(map[string]any{"status": string(StatusRejected), "errorCode": ErrorEmploymentNotProvided}))); err != nil {
		return err
	}
	return enqueueRequestWebhook(ctx, q, req, EventSessionFailed, sessionEventData(req, StatusRejected))
}

// NextMissingEmployment is when the next approved request without a
// Verzekeringsbericht is due for RejectMissingEmployment; zero for none.
func (s *RequestStore) NextMissingEmployment(ctx context.Context, now time.Time) (time.Time, error) {
	var next *time.Time
	if err := s.db.QueryRow(ctx, `SELECT min(r.completed_at + make_interval(secs => $2))
		FROM identity_proofing_requests r WHERE `+missingEmploymentWhere+`
			AND r.completed_at + make_interval(secs => $2) > $1`, now, EmploymentWindow.Seconds()).Scan(&next); err != nil {
		return time.Time{}, fmt.Errorf("proofing: next missing employment: %w", err)
	}
	if next == nil {
		return time.Time{}, nil
	}
	return *next, nil
}

// RecordEmploymentRejected audits identity_proofing.employment_rejected: why
// a statement was refused, never what it said.
func (s *RequestStore) RecordEmploymentRejected(ctx context.Context, req Request, reason, failedCheck string) error {
	fields := map[string]any{"reason": reason}
	if failedCheck != "" {
		fields["failedCheck"] = failedCheck
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return err
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingEmploymentRejected,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			req.auditFields(fields))
	})
}

// decryptEmployment opens a statement sealed by AddEmployment.
func (s *RequestStore) decryptEmployment(id uuid.UUID, sealed []byte) (*Employment, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	plain, err := s.cipher.Decrypt(sealed)
	if err != nil {
		return nil, fmt.Errorf("proofing: decrypt employment request %s: %w", id, err)
	}
	var emp Employment
	if err := json.Unmarshal(plain, &emp); err != nil {
		return nil, fmt.Errorf("proofing: unmarshal employment request %s: %w", id, err)
	}
	return &emp, nil
}

// flowEmployment is the flow's mode; a service without a store asks for none.
func (s *Service) flowEmployment(ctx context.Context, orgID uuid.UUID, flowID string) (EmploymentMode, error) {
	if s.flowEmploymentSettings == nil {
		return EmploymentOff, nil
	}
	return s.flowEmploymentSettings.Get(ctx, orgID, flowID)
}

// allFlowEmployment is every flow's mode that was set; a missing flow is off.
func (s *Service) allFlowEmployment(ctx context.Context, orgID uuid.UUID) (map[string]EmploymentMode, error) {
	if s.flowEmploymentSettings == nil {
		return map[string]EmploymentMode{}, nil
	}
	return s.flowEmploymentSettings.All(ctx, orgID)
}

// FlowEmployment returns one of the org's flows' EmploymentMode.
func (s *Service) FlowEmployment(ctx context.Context, org Org, flowID string) (EmploymentMode, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowEmployment(ctx, org.ID, flowID)
}

// SaveFlowEmployment sets one of the org's flows' EmploymentMode, not a data
// request flow's: required only for a flow that reads the name and date of birth, which the
// statement's holder is matched against. Requests already sent keep the mode
// they were sent with.
func (s *Service) SaveFlowEmployment(ctx context.Context, org Org, flowID string, mode EmploymentMode) (EmploymentMode, error) {
	if s.flowEmploymentSettings == nil {
		return "", errors.New("proofing: no employment settings store configured")
	}
	if !mode.valid() {
		return "", fmt.Errorf("%w: employment is off or required", ErrInvalidInput)
	}
	if err := s.checkFlowIdentity(ctx, org, flowID, mode.asked()); err != nil {
		return "", err
	}
	return s.flowEmploymentSettings.Save(ctx, org.ID, flowID, mode)
}

// AddEmployment checks the statement uploaded on the on-screen page of a
// request the caller may reach (requestedBy as in Request) and keeps it when
// accepted.
func (s *Service) AddEmployment(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, f DiplomaFile) (EmploymentVerdict, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return EmploymentVerdict{}, err
	}
	return s.addEmployment(ctx, req, f)
}

// HostedEmploymentOpen reports whether a hosted link takes a statement now,
// the same checks HostedAddEmployment makes: what the upload route asks before
// it reads a body from an anonymous caller.
func (s *Service) HostedEmploymentOpen(ctx context.Context, token string) error {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return err
	}
	return s.employmentOpen(req)
}

// HostedAddEmployment is AddEmployment for a hosted request, by its subject.
func (s *Service) HostedAddEmployment(ctx context.Context, token string, f DiplomaFile) (EmploymentVerdict, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return EmploymentVerdict{}, err
	}
	return s.addEmployment(audit.ContextWithActor(ctx, hostedSubjectActor), req, f)
}

// employmentOpen reports whether req takes a statement now: it asks for one,
// holds none yet, is approved, not erased or cancelled, and within
// EmploymentWindow.
func (s *Service) employmentOpen(req Request) error {
	if s.employmentChecker == nil {
		return ErrNoEmploymentChecker
	}
	if !req.Employment.asked() {
		return ErrEmploymentNotAsked
	}
	if req.PurgedAt != nil || req.CancelledAt != nil {
		return ErrSessionOver
	}
	now := s.now()
	if req.SubjectEmployment != nil || req.EffectiveStatus(now) != StatusApproved || req.CompletedAt == nil ||
		now.Sub(*req.CompletedAt) > EmploymentWindow {
		return ErrEmploymentClosed
	}
	return nil
}

// addEmployment checks f against the identity the engine approved for req:
// UWV's seal, then the printed holder's initials, surname and date of birth,
// then its age. An accepted statement is kept, a refused one only audited
// with its reason, so the subject can try another within the window.
func (s *Service) addEmployment(ctx context.Context, req Request, f DiplomaFile) (EmploymentVerdict, error) {
	if err := s.employmentOpen(req); err != nil {
		return EmploymentVerdict{}, err
	}
	if req.session == nil {
		return EmploymentVerdict{}, ErrEmploymentClosed
	}
	holder, err := s.sessionHolder(ctx, req)
	if err != nil {
		return EmploymentVerdict{}, err
	}

	verdict := EmploymentVerdict{FileName: f.Name}
	out, err := s.employmentChecker.Check(ctx, f.Bytes, holder)
	if err != nil {
		return EmploymentVerdict{}, fmt.Errorf("proofing: check employment request %s: %w", req.ID, err)
	}
	if !out.Accepted() {
		verdict.Reason, verdict.FailedCheck = out.Reason, out.FailedCheck
		if err := s.requests.RecordEmploymentRejected(ctx, req, out.Reason, out.FailedCheck); err != nil {
			return EmploymentVerdict{}, err
		}
		return verdict, nil
	}

	emp := newEmployment(out.Statement)
	added, err := s.requests.AddEmployment(ctx, req, emp)
	if err != nil {
		return EmploymentVerdict{}, err
	}
	// Lost to a concurrent upload, or to a purge.
	if !added {
		return EmploymentVerdict{}, ErrEmploymentClosed
	}
	verdict.Employment = &emp
	return verdict, nil
}
