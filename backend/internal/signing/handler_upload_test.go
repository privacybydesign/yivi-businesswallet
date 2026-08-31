package signing

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/auth"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/organization"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
	"github.com/privacybydesign/yivi-businesswallet/backend/internal/user"
)

// recordingService accepts a request and keeps the document it was handed, so an
// upload test can tell "the handler let it through whole" from "the handler
// truncated it" and from "the handler never called the service".
type recordingService struct {
	pdf []byte
}

func (s *recordingService) CreateRequest(_ context.Context, _, _ uuid.UUID, _, _ string, pdf []byte, _ []SignerInput, _ string, _ RecipientInput) (uuid.UUID, error) {
	s.pdf = pdf
	return uuid.New(), nil
}

// The remainder of signingService is unreachable from createRequest.
func (s *recordingService) StartLink(context.Context, uuid.UUID, uuid.UUID, string) (Start, error) {
	panic("unused")
}

func (s *recordingService) StartSign(context.Context, uuid.UUID, uuid.UUID, string, uuid.UUID) (Start, error) {
	panic("unused")
}

func (s *recordingService) HandleCallback(context.Context, string, string) string { panic("unused") }

func (s *recordingService) GetRequest(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bool) (Request, error) {
	panic("unused")
}

func (s *recordingService) GetSignedDocument(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, bool) ([]byte, string, error) {
	panic("unused")
}

func (s *recordingService) ListPending(context.Context, uuid.UUID, uuid.UUID) ([]Request, error) {
	panic("unused")
}

func (s *recordingService) ListRequests(context.Context, uuid.UUID, string, int) ([]Request, string, error) {
	panic("unused")
}

func (s *recordingService) GetCredential(context.Context, uuid.UUID, uuid.UUID) (LinkedCredential, error) {
	panic("unused")
}

func (s *recordingService) Available(context.Context, uuid.UUID) (bool, error) { panic("unused") }

func (s *recordingService) ExternalView(context.Context, string) (ExternalView, error) {
	panic("unused")
}

func (s *recordingService) StartExternalLink(context.Context, string) (Start, error) {
	panic("unused")
}

func (s *recordingService) StartExternalSign(context.Context, string) (Start, error) {
	panic("unused")
}

func (s *recordingService) ExternalDocument(context.Context, string) ([]byte, string, error) {
	panic("unused")
}

// uploadRequest is a POST .../signing/requests carrying one document part and one
// signer, with the org and user the org-scoped middleware would have put in place.
func uploadRequest(t *testing.T, pdf []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("document", "contract.pdf")
	if err != nil {
		t.Fatalf("create the document part: %v", err)
	}
	if _, err := part.Write(pdf); err != nil {
		t.Fatalf("write the document part: %v", err)
	}
	if err := mw.WriteField("signers", `{"kind":"external","email":"signee@example.org"}`); err != nil {
		t.Fatalf("write the signers field: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/orgs/acme/signing/requests", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	ctx := organization.ContextWithOrg(req.Context(), organization.Organization{ID: uuid.New(), Slug: "acme"})
	return req.WithContext(auth.ContextWithUser(ctx, user.User{ID: uuid.New()}))
}

func assertTooLarge(t *testing.T, err error) {
	t.Helper()
	var apiErr *respond.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("createRequest returned %v, want a *respond.APIError", err)
	}
	if apiErr.Status != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", apiErr.Status)
	}
	if apiErr.Code != "document_too_large" {
		t.Errorf("code = %q, want document_too_large", apiErr.Code)
	}
	// The limit belongs in the message: a caller told only "too large" cannot tell
	// what to send instead.
	if !strings.Contains(apiErr.Message, "25 MB") {
		t.Errorf("message = %q, want it to name the 25 MB maximum", apiErr.Message)
	}
}

// A real signing document is a scan with images in it. The 10 MiB cap this replaced
// refused ordinary contracts, which is what the "content too large" report was.
func TestCreateRequestAcceptsADocumentOverTheOldTenMiBCap(t *testing.T) {
	svc := &recordingService{}
	pdf := bytes.Repeat([]byte{0x25}, 12<<20)

	if err := NewHandler(svc, nil, nil).createRequest(httptest.NewRecorder(), uploadRequest(t, pdf)); err != nil {
		t.Fatalf("createRequest: %v", err)
	}

	if len(svc.pdf) != len(pdf) {
		t.Errorf("the service was handed %d bytes, want the whole %d-byte document", len(svc.pdf), len(pdf))
	}
}

// Just over the document cap: the body still fits within the slack, so the form
// parses and the payload gate after reading the document part is what refuses it.
func TestCreateRequestRefusesADocumentOverTheCap(t *testing.T) {
	svc := &recordingService{}
	req := uploadRequest(t, bytes.Repeat([]byte{0x25}, maxUploadBytes+1))

	assertTooLarge(t, NewHandler(svc, nil, nil).createRequest(httptest.NewRecorder(), req))

	if req.MultipartForm == nil {
		t.Error("the form did not parse, so the payload gate is not what refused this")
	}
	if svc.pdf != nil {
		t.Error("an over-sized document reached the service anyway")
	}
}

// Far over the cap: MaxBytesReader stops the body while the form is being parsed, so
// a hostile upload is never spooled to a temp file in full. Same 413 either way.
func TestCreateRequestRefusesABodyOverTheHardLimitWhileParsing(t *testing.T) {
	svc := &recordingService{}
	req := uploadRequest(t, bytes.Repeat([]byte{0x25}, maxUploadBytes+bodySlack+1))

	assertTooLarge(t, NewHandler(svc, nil, nil).createRequest(httptest.NewRecorder(), req))

	if req.MultipartForm != nil {
		t.Error("the whole form parsed, so the body cap did not stop the upload")
	}
	if svc.pdf != nil {
		t.Error("an over-sized document reached the service anyway")
	}
}
