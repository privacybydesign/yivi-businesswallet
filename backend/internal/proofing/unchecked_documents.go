package proofing

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/crypto"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/database"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
)

// DocumentReasonSealPending is a file whose seal could not be checked, as the
// trust lists or the revocation responder were unreachable: kept for a
// reviewer to check again, so the subject needs to do nothing more.
const DocumentReasonSealPending = "seal_check_pending"

// ErrSealUnchecked is a reviewer confirming a step that holds a file whose
// seal was never checked: check it again first.
var ErrSealUnchecked = errors.New("proofing: a document of this step has a seal nobody checked yet")

// MaxUncheckedStatements is how many Verzekeringsberichten wait together
// while their seal cannot be checked: none can be told from a fake yet, so a
// first one does not lock the real one out. The first that checks out is
// kept, the others refused.
const MaxUncheckedStatements = 3

const (
	// sealCheckAttempts is how often an upload tries a seal check the trust
	// lists or a revocation responder could not answer.
	sealCheckAttempts = 3
	// sealCheckRetryDelay is the wait between those tries.
	sealCheckRetryDelay = 2 * time.Second
)

// sealUnreachable is a check that could not reach what vouches for a seal:
// the document is not at fault.
func sealUnreachable(err error) bool {
	return errors.Is(err, diploma.ErrTrustUnavailable) || errors.Is(err, diploma.ErrRevocationUnavailable)
}

// sealCheck runs the seal checks of one upload, or of a reviewer's check
// again (recheck). Once a file stayed unreachable through every try, the
// next files try once, so an outage does not hold the upload up per file.
type sealCheck struct {
	retryDelay time.Duration
	recheck    bool
	down       bool
}

func (s *Service) newSealCheck(recheck bool) *sealCheck {
	return &sealCheck{retryDelay: s.sealRetryDelay, recheck: recheck}
}

func (c *sealCheck) run(ctx context.Context, check func() error) error {
	attempts := sealCheckAttempts
	if c.down {
		attempts = 1
	}
	var err error
	for i := range attempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}
		if err = check(); !sealUnreachable(err) {
			return err
		}
	}
	c.down = true
	return err
}

type sealRecheckKey struct{}

// withSealRecheck marks ctx as a reviewer checking fileName's seal again: what
// it keeps or refuses is audited as such (sealRecheckFields).
func withSealRecheck(ctx context.Context, fileName string) context.Context {
	return context.WithValue(ctx, sealRecheckKey{}, fileName)
}

// sealRecheckFields adds to an audit snapshot that ctx checks a seal again,
// and of which file.
func sealRecheckFields(ctx context.Context, fields map[string]any) map[string]any {
	if name, ok := ctx.Value(sealRecheckKey{}).(string); ok {
		fields["sealCheckedAgain"], fields["fileName"] = true, name
	}
	return fields
}

// keepsUnchecked is whether an unreachable seal check keeps the file for a
// reviewer, rather than failing: on an upload, never on a check again.
func (c *sealCheck) keepsUnchecked(err error) bool {
	return !c.recheck && sealUnreachable(err)
}

// uncheckedDocument is a file kept while its seal could not be checked, with
// the identity it must name, under the review item of Kind.
type uncheckedDocument struct {
	ID       uuid.UUID
	Kind     ReviewKind
	FileName string
	Holder   diploma.Person
	Bytes    []byte
}

// uncheckedPayload is what is sealed of an uncheckedDocument.
type uncheckedPayload struct {
	Holder diploma.Person `json:"holder"`
	PDF    []byte         `json:"pdf"`
}

// What became of a file whose seal waited, besides a refusal reason.
const (
	SealCheckKept       = "kept"
	SealCheckNotChecked = "not_checked"
)

// SealCheck is a file kept for a reviewer as its seal could not be checked at
// upload: under the review item of Kind, waiting while Outcome is empty, then
// SealCheckKept, a refusal reason (the diploma's or the statement's), or
// SealCheckNotChecked when its item closed first.
type SealCheck struct {
	Kind       ReviewKind `json:"kind"`
	FileName   string     `json:"fileName"`
	Outcome    string     `json:"outcome"`
	UploadedAt time.Time  `json:"uploadedAt"`
	CheckedAt  *time.Time `json:"checkedAt"`
}

type uncheckedDocumentStore interface {
	Add(ctx context.Context, req Request, kind ReviewKind, holder diploma.Person, f DiplomaFile) (bool, error)
	List(ctx context.Context, req Request, kind ReviewKind) ([]uncheckedDocument, error)
	Resolve(ctx context.Context, req Request, id uuid.UUID, outcome string) error
}

// UncheckedDocumentStore keeps the files whose seal could not be checked,
// sealed, until a reviewer checks them again.
type UncheckedDocumentStore struct {
	db     database.DB
	audit  audit.Recorder
	cipher *crypto.Cipher
}

func NewUncheckedDocumentStore(db database.DB, recorder audit.Recorder, cipher *crypto.Cipher) *UncheckedDocumentStore {
	return &UncheckedDocumentStore{db: db, audit: recorder, cipher: cipher}
}

// Add keeps f, sealed with holder, on req under the review item of kind,
// which it opens, audited identity_proofing.document_unchecked. It is false,
// and changes nothing, for a file the request already keeps, a statement on a
// request that holds one or MaxUncheckedStatements waiting, or an extract
// past MaxDiplomasPerRequest.
func (s *UncheckedDocumentStore) Add(ctx context.Context, req Request, kind ReviewKind, holder diploma.Person, f DiplomaFile) (bool, error) {
	if s.cipher == nil {
		return false, ErrNoEncryptionKey
	}
	plain, err := json.Marshal(uncheckedPayload{Holder: holder, PDF: f.Bytes})
	if err != nil {
		return false, fmt.Errorf("proofing: marshal unchecked document request %s: %w", req.ID, err)
	}
	sealed, err := s.cipher.Encrypt(plain)
	if err != nil {
		return false, fmt.Errorf("proofing: encrypt unchecked document request %s: %w", req.ID, err)
	}
	sum := sha256.Sum256(f.Bytes)

	added := false
	err = database.InTx(ctx, s.db, func(q database.Querier) error {
		if ok, err := lockUnpurged(ctx, q, req.ID); err != nil || !ok {
			return cmp.Or(err, ErrSessionOver)
		}
		tag, err := q.Exec(ctx, `INSERT INTO identity_proofing_request_unchecked_documents
				(organization_id, request_id, kind, file_name, file_sha256, ciphertext)
			SELECT $1, $2, $3, $4, $5, $6
			WHERE CASE $3
				WHEN 'employment' THEN NOT EXISTS (SELECT 1 FROM identity_proofing_requests
						WHERE id = $2 AND employment_ciphertext IS NOT NULL)
					AND (SELECT count(*) FROM identity_proofing_request_unchecked_documents
						WHERE request_id = $2 AND kind = 'employment' AND ciphertext IS NOT NULL) < $8
				ELSE (SELECT count(*) FROM identity_proofing_request_diplomas WHERE request_id = $2)
					+ (SELECT count(*) FROM identity_proofing_request_unchecked_documents
						WHERE request_id = $2 AND kind = 'diplomas' AND ciphertext IS NOT NULL) < $7
			END
			ON CONFLICT (request_id, file_sha256) DO NOTHING`,
			req.OrganizationID, req.ID, string(kind), f.Name, sum[:], sealed, MaxDiplomasPerRequest, MaxUncheckedStatements)
		if err != nil {
			return fmt.Errorf("proofing: keep unchecked document request %s: %w", req.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		added = true
		if err := openReviewItem(ctx, q, req, kind); err != nil {
			return err
		}
		return s.audit.Record(ctx, q, audit.IdentityProofingDocumentUnchecked,
			audit.Target{Type: audit.TargetIdentityProofingRequest, ID: req.ID.String(), OrgID: &req.OrganizationID},
			audit.Created(req.auditFields(map[string]any{"kind": string(kind), "fileName": f.Name, "sealUnchecked": true})))
	})
	if err != nil {
		return false, err
	}
	return added, nil
}

// List returns req's files of kind whose seal waits, oldest first.
func (s *UncheckedDocumentStore) List(ctx context.Context, req Request, kind ReviewKind) ([]uncheckedDocument, error) {
	if s.cipher == nil {
		return nil, ErrNoEncryptionKey
	}
	rows, err := s.db.Query(ctx, `SELECT id, file_name, ciphertext FROM identity_proofing_request_unchecked_documents
		WHERE request_id = $1 AND kind = $2 AND ciphertext IS NOT NULL ORDER BY created_at, id`, req.ID, string(kind))
	if err != nil {
		return nil, fmt.Errorf("proofing: list unchecked documents request %s: %w", req.ID, err)
	}
	type sealedRow struct {
		id     uuid.UUID
		name   string
		sealed []byte
	}
	sealedRows, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (sealedRow, error) {
		var r sealedRow
		err := row.Scan(&r.id, &r.name, &r.sealed)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("proofing: list unchecked documents request %s: %w", req.ID, err)
	}
	out := make([]uncheckedDocument, 0, len(sealedRows))
	for _, r := range sealedRows {
		plain, err := s.cipher.Decrypt(r.sealed)
		if err != nil {
			return nil, fmt.Errorf("proofing: decrypt unchecked document %s: %w", r.id, err)
		}
		var p uncheckedPayload
		if err := json.Unmarshal(plain, &p); err != nil {
			return nil, fmt.Errorf("proofing: unmarshal unchecked document %s: %w", r.id, err)
		}
		out = append(out, uncheckedDocument{ID: r.id, Kind: kind, FileName: r.name, Holder: p.Holder, Bytes: p.PDF})
	}
	return out, nil
}

// Resolve deletes a file's PDF once its seal was checked, keeping what
// became of it (outcome) for the reviewer.
func (s *UncheckedDocumentStore) Resolve(ctx context.Context, req Request, id uuid.UUID, outcome string) error {
	if _, err := s.db.Exec(ctx, `UPDATE identity_proofing_request_unchecked_documents
		SET ciphertext = NULL, outcome = $3, checked_at = now()
		WHERE request_id = $1 AND id = $2 AND ciphertext IS NOT NULL`, req.ID, id, outcome); err != nil {
		return fmt.Errorf("proofing: resolve unchecked document %s: %w", id, err)
	}
	return nil
}

// closeUncheckedDocuments deletes the PDFs of req's files of kind still
// waiting, as its review item closed: SealCheckNotChecked.
func closeUncheckedDocuments(ctx context.Context, q database.Querier, id uuid.UUID, kind ReviewKind) error {
	if _, err := q.Exec(ctx, `UPDATE identity_proofing_request_unchecked_documents
		SET ciphertext = NULL, outcome = $3, checked_at = now()
		WHERE request_id = $1 AND kind = $2 AND ciphertext IS NOT NULL`, id, string(kind), SealCheckNotChecked); err != nil {
		return fmt.Errorf("proofing: close unchecked documents request %s: %w", id, err)
	}
	return nil
}

// purgeUncheckedDocuments deletes every file req kept for a seal check, at
// its purge: the file names may name the person.
func purgeUncheckedDocuments(ctx context.Context, q database.Querier, id uuid.UUID) error {
	if _, err := q.Exec(ctx, `DELETE FROM identity_proofing_request_unchecked_documents
		WHERE request_id = $1`, id); err != nil {
		return fmt.Errorf("proofing: purge unchecked documents request %s: %w", id, err)
	}
	return nil
}

// sealChecksColumn is r's SealChecks as JSON, oldest first.
const sealChecksColumn = `COALESCE((SELECT json_agg(json_build_object('kind', c.kind, 'fileName', c.file_name,
		'outcome', COALESCE(c.outcome, ''), 'uploadedAt', c.created_at, 'checkedAt', c.checked_at)
		ORDER BY c.created_at, c.id)
	FROM identity_proofing_request_unchecked_documents c WHERE c.request_id = r.id), '[]')`

// readSealChecks fills r's SealChecks and its waiting counts from the JSON of
// sealChecksColumn.
func readSealChecks(r *Request, raw []byte) error {
	if err := json.Unmarshal(raw, &r.SealChecks); err != nil {
		return fmt.Errorf("proofing: read seal checks request %s: %w", r.ID, err)
	}
	for _, c := range r.SealChecks {
		if c.Outcome != "" {
			continue
		}
		if c.Kind == ReviewKindEmployment {
			r.UncheckedEmployment = true
			r.uncheckedStatements++
		} else {
			r.UncheckedDiplomas++
		}
	}
	return nil
}

// hasUnchecked is whether r holds a file of kind whose seal waits.
func (r Request) hasUnchecked(kind ReviewKind) bool {
	switch kind {
	case ReviewKindDiplomas:
		return r.UncheckedDiplomas > 0
	case ReviewKindEmployment:
		return r.UncheckedEmployment
	}
	return false
}

// keepUnchecked keeps f for a reviewer, as its seal could not be checked:
// false when it was not (Add).
func (s *Service) keepUnchecked(ctx context.Context, req Request, kind ReviewKind, holder diploma.Person, f DiplomaFile) (bool, error) {
	if s.unchecked == nil {
		return false, errors.New("proofing: no unchecked document store configured")
	}
	added, err := s.unchecked.Add(ctx, req, kind, holder, f)
	if err != nil || !added {
		return false, err
	}
	s.rejectStepReviewsIfPaused(ctx, req.OrganizationID)
	return true, nil
}

// CheckSealsAgain checks the seals of request id's files of kind that could
// not be checked at upload, as a reviewer of its pending item: each one
// decided is kept or refused as at upload, and no longer waits. A seal still
// unreachable stops it, with what was decided before it done.
func (s *Service) CheckSealsAgain(ctx context.Context, orgID, id uuid.UUID, kind ReviewKind, in StepReviewInput) (Request, error) {
	if kind != ReviewKindDiplomas && kind != ReviewKindEmployment {
		return Request{}, fmt.Errorf("%w: only diplomas and the Verzekeringsbericht have a seal", ErrInvalidInput)
	}
	if s.unchecked == nil {
		return Request{}, ErrNotUnderReview
	}
	release, err := s.requests.LockReview(ctx, id)
	if err != nil {
		return Request{}, err
	}
	defer release()
	req, err := s.requests.Get(ctx, orgID, id)
	if err != nil {
		return Request{}, err
	}
	if !in.Scope.reachesStepReview(req) {
		return Request{}, ErrRequestNotFound
	}
	if req.stepReview(kind) != ReviewPending {
		return Request{}, ErrNotUnderReview
	}
	if ownReview(req, in.ReviewerID) && !in.OwnAllowed {
		return Request{}, ErrOwnReview
	}
	docs, err := s.unchecked.List(ctx, req, kind)
	if err != nil {
		return Request{}, err
	}
	seal := s.newSealCheck(true)
	for _, doc := range docs {
		outcome, err := s.checkUnchecked(withSealRecheck(ctx, doc.FileName), req, doc, seal)
		if err != nil {
			return Request{}, err
		}
		if err := s.unchecked.Resolve(ctx, req, doc.ID, outcome); err != nil {
			return Request{}, err
		}
	}
	return s.requests.Get(ctx, orgID, id)
}

// checkUnchecked checks doc as at upload, keeping or refusing it, and
// returns what became of it: SealCheckKept or the refusal reason.
func (s *Service) checkUnchecked(ctx context.Context, req Request, doc uncheckedDocument, seal *sealCheck) (string, error) {
	f := DiplomaFile{Name: doc.FileName, Bytes: doc.Bytes}
	if doc.Kind == ReviewKindDiplomas {
		if s.diplomaChecker == nil || s.diplomas == nil {
			return "", ErrNoDiplomaChecker
		}
		v, err := s.checkDiploma(ctx, req, doc.Holder, f, seal)
		if err != nil {
			return "", err
		}
		if v.Diploma != nil {
			return SealCheckKept, nil
		}
		return v.Reason, nil
	}
	if s.employmentChecker == nil {
		return "", ErrNoEmploymentChecker
	}
	v, err := s.checkEmployment(ctx, req, doc.Holder, f, seal)
	// A statement kept meanwhile leaves this one nothing to add.
	if errors.Is(err, ErrEmploymentClosed) {
		return DiplomaReasonDuplicate, nil
	}
	if err != nil {
		return "", err
	}
	if v.Employment != nil {
		return SealCheckKept, nil
	}
	return v.Reason, nil
}
