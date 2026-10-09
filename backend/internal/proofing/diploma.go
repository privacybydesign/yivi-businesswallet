package proofing

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// DiplomaMode is whether a flow has the diploma step: once their identity is
// approved, its subject must add their DUO diploma extracts: the PDFs DUO signs, which the
// wallet checks itself (package diploma). Flows live in the engine, which knows
// nothing of diplomas, so the wallet keeps this per org and flow.
type DiplomaMode string

const (
	DiplomasOff      DiplomaMode = "off"
	DiplomasRequired DiplomaMode = "required"
)

func (m DiplomaMode) valid() bool {
	return m == DiplomasOff || m == DiplomasRequired
}

// asked is the mode as read from a row; "" (a store-less service) is off.
func (m DiplomaMode) asked() bool { return m == DiplomasRequired }

const (
	// MaxDiplomasPerRequest bounds how many extracts one request keeps.
	MaxDiplomasPerRequest = 10
	// MaxDiplomaFileBytes bounds one uploaded extract: DUO's are well under
	// half a megabyte, also with a grade list.
	MaxDiplomaFileBytes = 5 << 20
	// DiplomaUploadWindow is how long after the identity was approved the
	// subject can still add extracts, on the page that ran the session.
	DiplomaUploadWindow = time.Hour
)

// DiplomaReasonDuplicate is an extract the request already holds.
const DiplomaReasonDuplicate = "duplicate"

var (
	// ErrDiplomasNotAsked is an upload on a request whose flow asks for none.
	ErrDiplomasNotAsked = errors.New("proofing: the request asks for no diplomas")
	// ErrDiplomasClosed is an upload before the identity was approved, or
	// once DiplomaUploadWindow passed.
	ErrDiplomasClosed = errors.New("proofing: diplomas can only be added within DiplomaUploadWindow of an approved identity")
	// ErrDiplomasNeedPage is a diploma flow sent where its subject gets no
	// page to upload on: by mail, or as a bare deep link.
	ErrDiplomasNeedPage = errors.New("proofing: a flow that asks for diplomas runs on a page: show it on screen or send a hosted link")
	// ErrNoDiplomaChecker is an upload on a deployment without a checker.
	ErrNoDiplomaChecker = errors.New("proofing: no diploma checker configured")
)

// Diploma is an extract a request holds: what DUO printed about the
// qualification. Never the holder's name or date of birth, which were matched
// against the proven identity and dropped.
type Diploma struct {
	ID             uuid.UUID
	DocumentType   string
	Qualification  string
	Profiles       []string
	Institution    string
	PlaceOfIssue   string
	DateAwarded    time.Time
	NLQFLevel      string
	EQFLevel       string
	DocumentNumber string
	// SignedAt is when DUO signed the extract; nil when not known.
	SignedAt  *time.Time
	CreatedAt time.Time
}

func newDiploma(out diploma.Outcome) Diploma {
	doc := out.Document
	d := Diploma{
		DocumentType: doc.DocumentType, Qualification: doc.Qualification, Profiles: doc.Profiles,
		Institution: doc.Institution, PlaceOfIssue: doc.PlaceOfIssue, DateAwarded: doc.DateAwarded,
		NLQFLevel: doc.NLQFLevel, EQFLevel: doc.EQFLevel, DocumentNumber: doc.DocumentNumber,
	}
	if d.Profiles == nil {
		d.Profiles = []string{}
	}
	if !out.SignedAt.IsZero() {
		at := out.SignedAt
		d.SignedAt = &at
	}
	return d
}

func (d Diploma) auditFields() map[string]any {
	return map[string]any{
		"documentType": d.DocumentType, "qualification": d.Qualification, "institution": d.Institution,
		"dateAwarded": d.DateAwarded.Format(time.DateOnly), "nlqfLevel": d.NLQFLevel, "documentNumber": d.DocumentNumber,
	}
}

// webhookDiplomaKey is where a session.diploma_added delivery carries the
// extract; Purge drops it from the request's deliveries.
const webhookDiplomaKey = "diploma"

// DiplomaFile is one uploaded file.
type DiplomaFile struct {
	Name  string
	Bytes []byte
}

// DiplomaVerdict is what became of one uploaded file: Diploma when kept, else
// Reason (a diploma.Reason* or DiplomaReasonDuplicate) and, for a signature
// that did not verify, the first FailedCheck.
type DiplomaVerdict struct {
	FileName    string
	Diploma     *Diploma
	Reason      string
	FailedCheck string
}

// diplomaChecker decides on one extract (implemented by *diploma.Checker).
type diplomaChecker interface {
	Check(ctx context.Context, pdf []byte, holder diploma.Person) (diploma.Outcome, error)
}

// SetDiplomaChecker sets what checks uploaded extracts; call before serving.
// Without one, an upload is ErrNoDiplomaChecker.
func (s *Service) SetDiplomaChecker(c diplomaChecker) { s.diplomaChecker = c }

type flowDiplomaStore interface {
	Get(ctx context.Context, orgID uuid.UUID, flowID string) (DiplomaMode, error)
	All(ctx context.Context, orgID uuid.UUID) (map[string]DiplomaMode, error)
	Save(ctx context.Context, orgID uuid.UUID, flowID string, mode DiplomaMode) (DiplomaMode, error)
}

type diplomaStore interface {
	List(ctx context.Context, requestIDs []uuid.UUID) (map[uuid.UUID][]Diploma, error)
	Add(ctx context.Context, req Request, d Diploma) (Diploma, bool, error)
	RecordRejected(ctx context.Context, req Request, reason, failedCheck string) error
}

// FlowDiplomaStore persists each flow's DiplomaMode per org.
type FlowDiplomaStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewFlowDiplomaStore(db database.DB, recorder audit.Recorder) *FlowDiplomaStore {
	return &FlowDiplomaStore{db: db, audit: recorder}
}

// Get returns the flow's mode; a flow never set is DiplomasOff.
func (s *FlowDiplomaStore) Get(ctx context.Context, orgID uuid.UUID, flowID string) (DiplomaMode, error) {
	return getFlowDiplomas(ctx, s.db, orgID, flowID)
}

func getFlowDiplomas(ctx context.Context, q database.Querier, orgID uuid.UUID, flowID string) (DiplomaMode, error) {
	var mode DiplomaMode
	err := q.QueryRow(ctx, `SELECT diplomas FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND flow_id = $2`, orgID, flowID).Scan(&mode)
	if errors.Is(err, pgx.ErrNoRows) {
		return DiplomasOff, nil
	}
	if err != nil {
		return "", fmt.Errorf("proofing: read diploma setting flow %s: %w", flowID, err)
	}
	return mode, nil
}

// All returns every flow of the org that asks for diplomas; a flow missing is
// off.
func (s *FlowDiplomaStore) All(ctx context.Context, orgID uuid.UUID) (map[string]DiplomaMode, error) {
	rows, err := s.db.Query(ctx, `SELECT flow_id, diplomas FROM identity_proofing_flow_settings
		WHERE organization_id = $1 AND diplomas <> 'off'`, orgID)
	if err != nil {
		return nil, fmt.Errorf("proofing: list diploma settings org %s: %w", orgID, err)
	}
	out := map[string]DiplomaMode{}
	var flowID string
	var mode DiplomaMode
	if _, err := pgx.ForEachRow(rows, []any{&flowID, &mode}, func() error {
		out[flowID] = mode
		return nil
	}); err != nil {
		return nil, fmt.Errorf("proofing: list diploma settings org %s: %w", orgID, err)
	}
	return out, nil
}

// Save sets the flow's mode and audits identity_proofing.flow_diplomas_configured
// with before and after. Saving what it already has changes nothing.
func (s *FlowDiplomaStore) Save(ctx context.Context, orgID uuid.UUID, flowID string, mode DiplomaMode) (DiplomaMode, error) {
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		before, err := getFlowDiplomas(ctx, q, orgID, flowID)
		if err != nil {
			return err
		}
		if before == mode {
			return nil
		}
		if _, err := q.Exec(ctx, `INSERT INTO identity_proofing_flow_settings (organization_id, flow_id, diplomas)
			VALUES ($1, $2, $3)
			ON CONFLICT (organization_id, flow_id) DO UPDATE SET diplomas = EXCLUDED.diplomas, updated_at = now()`,
			orgID, flowID, string(mode)); err != nil {
			return fmt.Errorf("proofing: save diploma setting flow %s: %w", flowID, err)
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingFlowDiplomasConfigured,
			audit.Target{Type: audit.TargetIdentityProofingFlow, ID: flowID, OrgID: &orgID},
			audit.Updated(map[string]any{"diplomas": string(before)}, map[string]any{"diplomas": string(mode)}))
	})
	if err != nil {
		return "", err
	}
	return mode, nil
}

// DiplomaStore persists the extracts requests hold.
type DiplomaStore struct {
	db    database.DB
	audit audit.Recorder
}

func NewDiplomaStore(db database.DB, recorder audit.Recorder) *DiplomaStore {
	return &DiplomaStore{db: db, audit: recorder}
}

// List returns the extracts of each request, oldest first.
func (s *DiplomaStore) List(ctx context.Context, requestIDs []uuid.UUID) (map[uuid.UUID][]Diploma, error) {
	out := map[uuid.UUID][]Diploma{}
	if len(requestIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT request_id, id, document_type, qualification, profiles, institution,
			place_of_issue, date_awarded, nlqf_level, eqf_level, document_number, signed_at, created_at
		FROM identity_proofing_request_diplomas WHERE request_id = ANY($1)
		ORDER BY created_at, id`, requestIDs)
	if err != nil {
		return nil, fmt.Errorf("proofing: list diplomas: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var requestID uuid.UUID
		var d Diploma
		if err := rows.Scan(&requestID, &d.ID, &d.DocumentType, &d.Qualification, &d.Profiles,
			&d.Institution, &d.PlaceOfIssue, &d.DateAwarded, &d.NLQFLevel, &d.EQFLevel, &d.DocumentNumber,
			&d.SignedAt, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("proofing: scan diploma: %w", err)
		}
		out[requestID] = append(out[requestID], d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proofing: list diplomas: %w", err)
	}
	return out, nil
}

// Add keeps an accepted extract on req, audited
// identity_proofing.diploma_added, and sends session.diploma_added. It is
// false, and changes nothing, for an extract the request already holds, or
// one a concurrent upload would take past MaxDiplomasPerRequest.
func (s *DiplomaStore) Add(ctx context.Context, req Request, d Diploma) (Diploma, bool, error) {
	added := false
	err := database.InTx(ctx, s.db, func(q database.Querier) error {
		// Serialises uploads on one request, so the cap holds; an erased one
		// takes none.
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return cmp.Or(err, ErrSessionOver)
		}
		err := q.QueryRow(ctx, `INSERT INTO identity_proofing_request_diplomas
				(request_id, document_type, qualification, profiles, institution, place_of_issue, date_awarded,
				 nlqf_level, eqf_level, document_number, signed_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
			WHERE (SELECT count(*) FROM identity_proofing_request_diplomas WHERE request_id = $1) < $12
			ON CONFLICT (request_id, document_number) DO NOTHING
			RETURNING id, created_at`,
			req.ID, d.DocumentType, d.Qualification, d.Profiles, d.Institution, d.PlaceOfIssue, d.DateAwarded,
			d.NLQFLevel, d.EQFLevel, d.DocumentNumber, d.SignedAt, MaxDiplomasPerRequest).Scan(&d.ID, &d.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("proofing: add diploma request %s: %w", req.ID, err)
		}
		added = true
		if err := s.audit.Record(ctx, q, audit.IdentityProofingDiplomaAdded,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(d.auditFields()))); err != nil {
			return err
		}
		data := sessionEventData(req, req.Status)
		data[webhookDiplomaKey] = map[string]any{
			"qualification": d.Qualification, "institution": d.Institution,
			"dateAwarded": d.DateAwarded.Format(time.DateOnly), "nlqfLevel": d.NLQFLevel, "eqfLevel": d.EQFLevel,
		}
		return enqueueWebhook(ctx, q, req.OrganizationID, req.CustomerID, EventSessionDiplomaAdded, &req.ID, data)
	})
	if err != nil {
		return Diploma{}, false, err
	}
	return d, added, nil
}

// RecordRejected audits identity_proofing.diploma_rejected: why a file was
// refused, never what it said.
func (s *DiplomaStore) RecordRejected(ctx context.Context, req Request, reason, failedCheck string) error {
	fields := map[string]any{"reason": reason}
	if failedCheck != "" {
		fields["failedCheck"] = failedCheck
	}
	return database.InTx(ctx, s.db, func(q database.Querier) error {
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return err
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingDiplomaRejected,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			req.auditFields(fields))
	})
}

// flowDiplomas is the flow's mode; a service without a store asks for none.
func (s *Service) flowDiplomas(ctx context.Context, orgID uuid.UUID, flowID string) (DiplomaMode, error) {
	if s.flowDiplomaSettings == nil {
		return DiplomasOff, nil
	}
	return s.flowDiplomaSettings.Get(ctx, orgID, flowID)
}

// allFlowDiplomas is every flow's mode that was set; a missing flow is off.
func (s *Service) allFlowDiplomas(ctx context.Context, orgID uuid.UUID) (map[string]DiplomaMode, error) {
	if s.flowDiplomaSettings == nil {
		return map[string]DiplomaMode{}, nil
	}
	return s.flowDiplomaSettings.All(ctx, orgID)
}

// FlowDiplomas returns one of the org's flows' DiplomaMode.
func (s *Service) FlowDiplomas(ctx context.Context, org Org, flowID string) (DiplomaMode, error) {
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowDiplomas(ctx, org.ID, flowID)
}

// SaveFlowDiplomas sets one of the org's flows' DiplomaMode. Requests already
// sent keep the mode they were sent with.
func (s *Service) SaveFlowDiplomas(ctx context.Context, org Org, flowID string, mode DiplomaMode) (DiplomaMode, error) {
	if s.flowDiplomaSettings == nil {
		return "", errors.New("proofing: no diploma settings store configured")
	}
	if !mode.valid() {
		return "", fmt.Errorf("%w: diplomas is off or required", ErrInvalidInput)
	}
	if err := s.orgHasFlow(ctx, org, flowID); err != nil {
		return "", err
	}
	return s.flowDiplomaSettings.Save(ctx, org.ID, flowID, mode)
}

// RequestDiplomas returns the extracts each request holds.
func (s *Service) RequestDiplomas(ctx context.Context, requestIDs []uuid.UUID) (map[uuid.UUID][]Diploma, error) {
	if s.diplomas == nil {
		return map[uuid.UUID][]Diploma{}, nil
	}
	return s.diplomas.List(ctx, requestIDs)
}

// AddDiplomas checks extracts uploaded on the on-screen page of a request the
// caller may reach (requestedBy as in Request) and keeps the accepted ones.
func (s *Service) AddDiplomas(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, files []DiplomaFile) ([]DiplomaVerdict, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return nil, err
	}
	return s.addDiplomas(ctx, req, files)
}

// HostedAddDiplomas is AddDiplomas for a hosted request, by its subject.
// HostedDiplomasOpen reports whether a hosted link takes extracts now, the
// same checks HostedAddDiplomas makes: what the upload route asks before it
// reads a body of up to maxDiplomaUploadBytes from an anonymous caller.
func (s *Service) HostedDiplomasOpen(ctx context.Context, token string) error {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return err
	}
	return s.diplomasOpen(req)
}

func (s *Service) HostedAddDiplomas(ctx context.Context, token string, files []DiplomaFile) ([]DiplomaVerdict, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.addDiplomas(audit.ContextWithActor(ctx, hostedSubjectActor), req, files)
}

// diplomasOpen reports whether req takes extracts now: it asks for them, is
// approved, not erased or cancelled, and within DiplomaUploadWindow.
func (s *Service) diplomasOpen(req Request) error {
	if s.diplomaChecker == nil || s.diplomas == nil {
		return ErrNoDiplomaChecker
	}
	if !req.Diplomas.asked() {
		return ErrDiplomasNotAsked
	}
	if req.PurgedAt != nil || req.CancelledAt != nil {
		return ErrSessionOver
	}
	now := s.now()
	if req.EffectiveStatus(now) != StatusApproved || req.CompletedAt == nil || now.Sub(*req.CompletedAt) > DiplomaUploadWindow {
		return ErrDiplomasClosed
	}
	return nil
}

// addDiplomas checks each file against the identity the engine approved for req:
// DUO's signature, then the printed holder's name and date of birth. An
// accepted extract is kept, a refused one only audited with its reason. A
// file the checker cannot decide on fails the whole call, with what was
// decided before it kept.
func (s *Service) addDiplomas(ctx context.Context, req Request, files []DiplomaFile) ([]DiplomaVerdict, error) {
	if err := s.diplomasOpen(req); err != nil {
		return nil, err
	}
	held, err := s.diplomas.List(ctx, []uuid.UUID{req.ID})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 || len(held[req.ID])+len(files) > MaxDiplomasPerRequest {
		return nil, fmt.Errorf("%w: a session holds at most %d extracts", ErrInvalidInput, MaxDiplomasPerRequest)
	}
	if req.session == nil {
		return nil, ErrDiplomasClosed
	}
	tenant := requestTenant(req)
	identity, err := s.ips.SessionIdentity(ctx, tenant, req.session.ID, req.session.Token)
	if err != nil {
		return nil, fmt.Errorf("proofing: identity for diplomas request %s: %w", req.ID, err)
	}
	holder := diploma.Person{GivenNames: identity.GivenName, Surname: identity.FamilyName, DateOfBirth: identity.BirthDate}

	verdicts := make([]DiplomaVerdict, 0, len(files))
	for _, f := range files {
		verdict, err := s.checkDiploma(ctx, req, holder, f)
		if err != nil {
			return nil, err
		}
		verdicts = append(verdicts, verdict)
	}
	return verdicts, nil
}

func (s *Service) checkDiploma(ctx context.Context, req Request, holder diploma.Person, f DiplomaFile) (DiplomaVerdict, error) {
	verdict := DiplomaVerdict{FileName: f.Name}
	out, err := s.diplomaChecker.Check(ctx, f.Bytes, holder)
	if err != nil {
		return DiplomaVerdict{}, fmt.Errorf("proofing: check diploma request %s: %w", req.ID, err)
	}
	if out.Accepted() {
		kept, added, err := s.diplomas.Add(ctx, req, newDiploma(out))
		if err != nil {
			return DiplomaVerdict{}, err
		}
		if added {
			verdict.Diploma = &kept
			return verdict, nil
		}
		out.Reason = DiplomaReasonDuplicate
	}
	verdict.Reason, verdict.FailedCheck = out.Reason, out.FailedCheck
	if err := s.diplomas.RecordRejected(ctx, req, out.Reason, out.FailedCheck); err != nil {
		return DiplomaVerdict{}, err
	}
	return verdict, nil
}
