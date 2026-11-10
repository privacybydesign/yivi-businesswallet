package proofing

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/audit"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/trust"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma/verify"
)

// DocumentKind is what an uploaded document is, read from who sealed it.
type DocumentKind string

const (
	DocumentDiploma    DocumentKind = "diploma"
	DocumentEmployment DocumentKind = "employment"
)

// Reasons a file of a document upload is refused besides its own check's.
const (
	// DocumentReasonNotSupported is a file sealed by no issuer the step takes.
	DocumentReasonNotSupported = "not_supported"
	// DocumentReasonNotAsked is a document the step takes but this request
	// does not ask for.
	DocumentReasonNotAsked = "not_asked"
	// DocumentReasonTooMany is a diploma past MaxDiplomasPerRequest.
	DocumentReasonTooMany = "too_many"
	// DocumentReasonCheckFailed is a file whose check broke on the server's
	// side (fileCheckFailed): nothing of it is kept, so the subject uploads it
	// again, and the upload's other files keep their verdicts.
	DocumentReasonCheckFailed = "check_failed"
)

// uploadErrors end a whole upload rather than one file's check: the request
// no longer takes documents, or the deployment checks none.
var uploadErrors = []error{
	ErrSessionOver, ErrDiplomasClosed, ErrEmploymentClosed, ErrNoDiplomaChecker, ErrNoEmploymentChecker,
	ErrNoEncryptionKey,
}

// fileCheckFailed is an error that breaks one file's check only: the
// checker's own (a seal it cannot reach, PDFium), or storing what it decided.
func fileCheckFailed(err error) bool {
	return !slices.ContainsFunc(uploadErrors, func(target error) bool { return errors.Is(err, target) })
}

// MaxDocumentsPerUpload is how many files one upload takes: every diploma a
// request keeps, and its one statement.
const MaxDocumentsPerUpload = MaxDiplomasPerRequest + 1

// ErrDocumentsNotAsked is an upload on a request that asks for no document.
var ErrDocumentsNotAsked = errors.New("proofing: the request asks for no document")

// documentIssuers are the documents the upload step takes, each by the issuer
// whose seal its checker verifies.
var documentIssuers = []struct {
	kind   DocumentKind
	issuer trust.Issuer
}{
	{DocumentDiploma, trust.DUO},
	{DocumentEmployment, trust.UWV},
}

// sealedDocument is the document pdf's seal claims to be; "" for none the
// step takes. It only routes the file: that document's checker then decides
// whether the seal holds.
func sealedDocument(pdf []byte) DocumentKind {
	for _, d := range documentIssuers {
		if verify.SealedBy(pdf, d.issuer) {
			return d.kind
		}
	}
	return ""
}

// DocumentVerdict is what became of one file of a document upload: the
// document it is, and Diploma or Employment when kept, else Reason (a
// DocumentReason*, or that document's own) and, for a seal that did not
// verify, the first FailedCheck.
type DocumentVerdict struct {
	FileName    string
	Kind        DocumentKind
	Diploma     *Diploma
	Employment  *Employment
	Reason      string
	FailedCheck string
}

// AddDocuments checks the documents uploaded on the on-screen page of a
// request the caller may reach (requestedBy as in Request) and keeps the
// accepted ones.
func (s *Service) AddDocuments(ctx context.Context, orgID, id uuid.UUID, requestedBy *uuid.UUID, files []DiplomaFile) ([]DocumentVerdict, error) {
	req, err := s.sentRequest(ctx, orgID, id, requestedBy)
	if err != nil {
		return nil, err
	}
	return s.addDocuments(ctx, req, files)
}

// HostedDocumentsOpen reports whether a hosted link takes documents now, the
// same checks HostedAddDocuments makes, and how many files it still takes:
// what the upload route asks before it reads a body from an anonymous caller.
func (s *Service) HostedDocumentsOpen(ctx context.Context, token string) (int, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return 0, err
	}
	if err := s.documentsOpen(req); err != nil {
		return 0, err
	}
	diplomas, err := s.diplomaSlots(ctx, req)
	if err != nil {
		return 0, err
	}
	slots := diplomas
	if s.employmentOpen(req) == nil {
		slots++
	}
	if slots == 0 {
		return 0, fmt.Errorf("%w: a session holds at most %d extracts and one Verzekeringsbericht", ErrInvalidInput, MaxDiplomasPerRequest)
	}
	return slots, nil
}

// HostedAddDocuments is AddDocuments for a hosted request, by its subject.
func (s *Service) HostedAddDocuments(ctx context.Context, token string, files []DiplomaFile) ([]DocumentVerdict, error) {
	req, err := s.hostedRequest(ctx, token)
	if err != nil {
		return nil, err
	}
	return s.addDocuments(audit.ContextWithActor(ctx, hostedSubjectActor), req, files)
}

// documentsOpen reports whether req takes a document now: one it asks for is
// open (diplomasOpen, employmentOpen). Else it is the first one's error.
func (s *Service) documentsOpen(req Request) error {
	var first error
	if req.Diplomas.asked() {
		if first = s.diplomasOpen(req); first == nil {
			return nil
		}
	}
	if req.Employment.asked() {
		err := s.employmentOpen(req)
		if err == nil {
			return nil
		}
		first = cmp.Or(first, err)
	}
	return cmp.Or(first, ErrDocumentsNotAsked)
}

// diplomaSlots is how many more extracts req takes now; none while its
// diplomas are not open.
func (s *Service) diplomaSlots(ctx context.Context, req Request) (int, error) {
	if s.diplomasOpen(req) != nil {
		return 0, nil
	}
	held, err := s.diplomas.List(ctx, []uuid.UUID{req.ID})
	if err != nil {
		return 0, err
	}
	return max(MaxDiplomasPerRequest-len(held[req.ID]), 0), nil
}

// documentUpload is what one upload still takes while its files are checked:
// how many extracts, and whether the statement (employmentErr nil).
type documentUpload struct {
	req           Request
	holder        diploma.Person
	diplomasErr   error
	diplomas      int
	employmentErr error
}

// addDocuments checks each file as the document its seal names, with that
// document's own check (checkDiploma, checkEmployment), against the identity
// the engine approved for req. A file sealed by no issuer the step takes, or
// by one req does not ask for, is refused without a check. A file whose check
// breaks on the server's side is DocumentReasonCheckFailed, and the others
// keep their verdicts.
func (s *Service) addDocuments(ctx context.Context, req Request, files []DiplomaFile) ([]DocumentVerdict, error) {
	if err := s.documentsOpen(req); err != nil {
		return nil, err
	}
	if len(files) == 0 || len(files) > MaxDocumentsPerUpload {
		return nil, fmt.Errorf("%w: upload one to %d files", ErrInvalidInput, MaxDocumentsPerUpload)
	}
	if req.session == nil {
		if req.Diplomas.asked() {
			return nil, ErrDiplomasClosed
		}
		return nil, ErrEmploymentClosed
	}
	holder, err := s.sessionHolder(ctx, req)
	if err != nil {
		return nil, err
	}
	slots, err := s.diplomaSlots(ctx, req)
	if err != nil {
		return nil, err
	}
	up := &documentUpload{
		req: req, holder: holder,
		diplomasErr: s.diplomasOpen(req), diplomas: slots, employmentErr: s.employmentOpen(req),
	}

	detect := s.detectDocument
	if detect == nil {
		detect = sealedDocument
	}
	verdicts := make([]DocumentVerdict, 0, len(files))
	for _, f := range files {
		kind := detect(f.Bytes)
		verdict, err := s.checkDocument(ctx, up, kind, f)
		// The files decided before it are kept already: their verdicts must
		// still reach the page, or it asks for them again.
		// Only the upload's own ctx ending fails it whole: a seal server's
		// timeout also matches context.DeadlineExceeded.
		if err != nil && ctx.Err() == nil && fileCheckFailed(err) {
			slog.ErrorContext(ctx, "proofing: check uploaded document",
				slog.String("request", req.ID.String()), slog.String("kind", string(kind)), slog.Any("error", err))
			verdict, err = DocumentVerdict{FileName: f.Name, Kind: kind, Reason: DocumentReasonCheckFailed}, nil
		}
		if err != nil {
			return nil, err
		}
		verdicts = append(verdicts, verdict)
	}
	return verdicts, nil
}

// checkDocument checks f as kind for up, and counts what it kept.
func (s *Service) checkDocument(ctx context.Context, up *documentUpload, kind DocumentKind, f DiplomaFile) (DocumentVerdict, error) {
	switch kind {
	case DocumentDiploma:
		return s.checkDiplomaFile(ctx, up, f)
	case DocumentEmployment:
		return s.checkEmploymentFile(ctx, up, f)
	default:
		return DocumentVerdict{FileName: f.Name, Kind: kind, Reason: DocumentReasonNotSupported}, nil
	}
}

func (s *Service) checkDiplomaFile(ctx context.Context, up *documentUpload, f DiplomaFile) (DocumentVerdict, error) {
	out := DocumentVerdict{FileName: f.Name, Kind: DocumentDiploma}
	if !up.req.Diplomas.asked() {
		out.Reason = DocumentReasonNotAsked
		return out, nil
	}
	if up.diplomasErr != nil {
		return DocumentVerdict{}, up.diplomasErr
	}
	if up.diplomas == 0 {
		out.Reason = DocumentReasonTooMany
		return out, nil
	}
	v, err := s.checkDiploma(ctx, up.req, up.holder, f)
	if err != nil {
		return DocumentVerdict{}, err
	}
	if v.Diploma != nil {
		up.diplomas--
	}
	out.Diploma, out.Reason, out.FailedCheck = v.Diploma, v.Reason, v.FailedCheck
	return out, nil
}

func (s *Service) checkEmploymentFile(ctx context.Context, up *documentUpload, f DiplomaFile) (DocumentVerdict, error) {
	out := DocumentVerdict{FileName: f.Name, Kind: DocumentEmployment}
	if !up.req.Employment.asked() {
		out.Reason = DocumentReasonNotAsked
		return out, nil
	}
	// The windows are the same, so a closed one is a statement held.
	if errors.Is(up.employmentErr, ErrEmploymentClosed) {
		out.Reason = DiplomaReasonDuplicate
		return out, nil
	}
	if up.employmentErr != nil {
		return DocumentVerdict{}, up.employmentErr
	}
	v, err := s.checkEmployment(ctx, up.req, up.holder, f)
	if errors.Is(err, ErrEmploymentClosed) {
		up.employmentErr = err
		out.Reason = DiplomaReasonDuplicate
		return out, nil
	}
	if err != nil {
		return DocumentVerdict{}, err
	}
	if v.Employment != nil {
		up.employmentErr = ErrEmploymentClosed
	}
	out.Employment, out.Reason, out.FailedCheck = v.Employment, v.Reason, v.FailedCheck
	return out, nil
}
