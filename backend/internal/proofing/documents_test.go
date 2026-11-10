package proofing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/diploma"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// newDocumentFixture is a provisioned fixture whose appFlow asks for diplomas
// and the Verzekeringsbericht as given. A file starting "UWV" reads as UWV's,
// one starting "unsealed" as nobody's, and any other as DUO's.
func newDocumentFixture(diplomas DiplomaMode, statement EmploymentMode) (diplomaFixture, *fakeEmploymentChecker) {
	d := newDiplomaFixture(diplomas)
	e := &fakeEmploymentChecker{}
	d.svc.flowEmploymentSettings = &fakeFlowEmployment{modes: map[string]EmploymentMode{appFlow.ID: statement}}
	d.svc.SetEmploymentChecker(e)
	d.svc.detectDocument = func(pdf []byte) DocumentKind {
		switch {
		case strings.HasPrefix(string(pdf), "UWV"):
			return DocumentEmployment
		case strings.HasPrefix(string(pdf), "unsealed"):
			return ""
		}
		return DocumentDiploma
	}
	return d, e
}

func uploadDocuments(d diplomaFixture, req Request, files ...string) ([]DocumentVerdict, error) {
	in := make([]DiplomaFile, 0, len(files))
	for _, f := range files {
		in = append(in, DiplomaFile{Name: f + ".pdf", Bytes: []byte(f)})
	}
	return d.svc.AddDocuments(context.Background(), testOrg.ID, req.ID, nil, in)
}

func TestAddDocumentsRoutesEachFileBySeal(t *testing.T) {
	d, e := newDocumentFixture(DiplomasRequired, EmploymentRequired)
	req := d.sendApproved(t, time.Minute)
	verdicts, err := uploadDocuments(d, req, "Anna Maria Jansen", "UWV statement", "unsealed scan", "UWV statement")
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}
	want := []struct {
		kind   DocumentKind
		reason string
	}{
		{DocumentDiploma, ""},
		{DocumentEmployment, ""},
		{"", DocumentReasonNotSupported},
		{DocumentEmployment, DiplomaReasonDuplicate},
	}
	if len(verdicts) != len(want) {
		t.Fatalf("verdicts = %+v, want %d", verdicts, len(want))
	}
	for i, v := range verdicts {
		if v.Kind != want[i].kind || v.Reason != want[i].reason {
			t.Errorf("verdict %d = %+v, want %s %q", i, v, want[i].kind, want[i].reason)
		}
	}
	if verdicts[0].Diploma == nil || verdicts[1].Employment == nil {
		t.Errorf("kept = %+v, %+v; want the diploma and the statement", verdicts[0].Diploma, verdicts[1].Employment)
	}
	if len(d.diplomas.held[req.ID]) != 1 || d.requests.stored.SubjectEmployment == nil {
		t.Errorf("held %d extracts and statement %v, want one of each", len(d.diplomas.held[req.ID]), d.requests.stored.SubjectEmployment)
	}
	// The second statement is refused before UWV's check runs.
	if len(e.holders) != 1 {
		t.Errorf("statements checked = %d, want one", len(e.holders))
	}
}

// A document the step takes but the request does not ask for is refused
// without a check.
func TestAddDocumentsRefusesWhatIsNotAsked(t *testing.T) {
	d, e := newDocumentFixture(DiplomasRequired, EmploymentOff)
	req := d.sendApproved(t, time.Minute)
	verdicts, err := uploadDocuments(d, req, "UWV statement")
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Kind != DocumentEmployment || verdicts[0].Reason != DocumentReasonNotAsked {
		t.Errorf("verdicts = %+v, want the statement not asked", verdicts)
	}
	if len(e.holders) != 0 || d.requests.stored.SubjectEmployment != nil {
		t.Errorf("a statement nobody asked for was checked or kept")
	}
}

func TestAddDocumentsCapsTheDiplomas(t *testing.T) {
	d, _ := newDocumentFixture(DiplomasRequired, EmploymentOff)
	req := d.sendApproved(t, time.Minute)
	for i := range MaxDiplomasPerRequest {
		d.diplomas.held[req.ID] = append(d.diplomas.held[req.ID], Diploma{ID: uuid.New(), DocumentNumber: fmt.Sprint(i)})
	}
	verdicts, err := uploadDocuments(d, req, "Anna Maria Jansen")
	if err != nil {
		t.Fatalf("AddDocuments: %v", err)
	}
	if len(verdicts) != 1 || verdicts[0].Reason != DocumentReasonTooMany || len(d.checker.holders) != 0 {
		t.Errorf("verdicts = %+v, want too_many without a check", verdicts)
	}
}

func TestAddDocumentsAsksForADocument(t *testing.T) {
	d, _ := newDocumentFixture(DiplomasOff, EmploymentOff)
	req := d.sendApproved(t, time.Minute)
	if _, err := uploadDocuments(d, req, "Anna Maria Jansen"); !errors.Is(err, ErrDocumentsNotAsked) {
		t.Errorf("AddDocuments = %v, want ErrDocumentsNotAsked", err)
	}
}

// A hosted link takes every extract it still holds room for and the one
// statement, until that is kept.
func TestHostedDocumentsOpenCountsTheSlots(t *testing.T) {
	d, _ := newDocumentFixture(DiplomasRequired, EmploymentRequired)
	_, token := d.sendHosted(t)
	checked := time.Now().Add(-time.Minute)
	d.requests.stored.Status, d.requests.stored.CompletedAt, d.requests.stored.CheckedAt = StatusApproved, &checked, &checked
	ctx := context.Background()

	if slots, err := d.svc.HostedDocumentsOpen(ctx, token); err != nil || slots != MaxDocumentsPerUpload {
		t.Errorf("slots = %d, %v; want %d", slots, err, MaxDocumentsPerUpload)
	}
	d.requests.stored.SubjectEmployment = &Employment{StatementDate: "2026-10-15"}
	if slots, err := d.svc.HostedDocumentsOpen(ctx, token); err != nil || slots != MaxDiplomasPerRequest {
		t.Errorf("slots with a statement kept = %d, %v; want %d", slots, err, MaxDiplomasPerRequest)
	}
	if _, err := d.svc.HostedDocumentsOpen(ctx, "unknown"); !errors.Is(err, ErrRequestNotFound) {
		t.Errorf("an unknown link = %v, want ErrRequestNotFound", err)
	}
}

// A document upload to an unknown link is refused before its body is read.
func TestHostedDocumentsChecksLink(t *testing.T) {
	f := newFixture()
	h := NewHandler(f.svc, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/proof/unknown/documents", unreadBody{t})
	r.SetPathValue("token", "unknown")
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	var apiErr *respond.APIError
	if err := h.hostedAddDocuments(httptest.NewRecorder(), r); !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
		t.Errorf("upload to an unknown link = %v, want 404", err)
	}
}

// A check that breaks on one file fails that file only: the statement kept
// before it is still reported, so the page does not ask for it again.
func TestAddDocumentsReportsKeptFilesWhenACheckBreaks(t *testing.T) {
	for name, checkErr := range map[string]error{
		"unreachable": fmt.Errorf("diploma: validate: %w", diploma.ErrRevocationUnavailable),
		"timed out":   fmt.Errorf("%w: %w", diploma.ErrTrustUnavailable, context.DeadlineExceeded),
	} {
		t.Run(name, func(t *testing.T) {
			d, _ := newDocumentFixture(DiplomasRequired, EmploymentRequired)
			d.checker.err = checkErr
			req := d.sendApproved(t, time.Minute)

			verdicts, err := uploadDocuments(d, req, "UWV statement", "Anna Maria Jansen")
			if err != nil {
				t.Fatalf("AddDocuments: %v", err)
			}
			if len(verdicts) != 2 || verdicts[0].Employment == nil ||
				verdicts[1].Kind != DocumentDiploma || verdicts[1].Reason != DocumentReasonCheckFailed {
				t.Fatalf("verdicts = %+v, want the statement kept and the diploma check_failed", verdicts)
			}
			if d.requests.stored.SubjectEmployment == nil || len(d.diplomas.held[req.ID]) != 0 {
				t.Errorf("kept statement %v, %d extracts; want the statement only",
					d.requests.stored.SubjectEmployment, len(d.diplomas.held[req.ID]))
			}
		})
	}
}

// An upload whose own request ends fails whole, whatever its check returned.
func TestAddDocumentsFailsWhollyWhenItsRequestEnds(t *testing.T) {
	d, _ := newDocumentFixture(DiplomasRequired, EmploymentRequired)
	d.checker.err = context.Canceled
	req := d.sendApproved(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	files := []DiplomaFile{{Name: "duo.pdf", Bytes: []byte("Anna Maria Jansen")}}
	if verdicts, err := d.svc.AddDocuments(ctx, testOrg.ID, req.ID, nil, files); !errors.Is(err, context.Canceled) {
		t.Errorf("AddDocuments = %+v, %v; want context.Canceled", verdicts, err)
	}
}

// A request that no longer takes documents still fails the whole upload.
func TestAddDocumentsFailsWhollyWhenClosed(t *testing.T) {
	if fileCheckFailed(ErrSessionOver) || fileCheckFailed(fmt.Errorf("x: %w", ErrNoEncryptionKey)) {
		t.Error("an upload-wide error read as one file's")
	}
	if !fileCheckFailed(fmt.Errorf("x: %w", diploma.ErrTrustUnavailable)) {
		t.Error("an unreachable seal read as upload-wide")
	}
}
