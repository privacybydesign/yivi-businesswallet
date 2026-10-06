package proofing

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/respond"
)

// diplomaFormField is the multipart field each extract is uploaded in.
const diplomaFormField = "file"

// maxDiplomaUploadBytes bounds one upload request: every extract at its
// largest, plus the multipart framing.
const maxDiplomaUploadBytes = MaxDiplomasPerRequest*MaxDiplomaFileBytes + 1<<20

// diplomaMultipartMemory is how much of an upload is held in memory before
// the multipart reader spills to disk.
const diplomaMultipartMemory = 8 << 20

type flowDiplomasBody struct {
	DiplomaMode DiplomaMode `json:"diplomaMode"`
}

func (h *Handler) getFlowDiplomas(w http.ResponseWriter, r *http.Request) error {
	mode, err := h.service.FlowDiplomas(r.Context(), orgFromRequest(r), r.PathValue("flowID"))
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowDiplomasBody{DiplomaMode: mode})
	return nil
}

func (h *Handler) saveFlowDiplomas(w http.ResponseWriter, r *http.Request) error {
	var body flowDiplomasBody
	if err := decode(r, &body); err != nil {
		return err
	}
	mode, err := h.service.SaveFlowDiplomas(r.Context(), orgFromRequest(r), r.PathValue("flowID"), body.DiplomaMode)
	if err != nil {
		return mapError(err)
	}
	respond.JSON(w, r, http.StatusOK, flowDiplomasBody{DiplomaMode: mode})
	return nil
}

// diplomaResponse is an extract a request holds, as DUO printed it.
type diplomaResponse struct {
	DocumentType   string     `json:"documentType"`
	Qualification  string     `json:"qualification"`
	Profiles       []string   `json:"profiles"`
	Institution    string     `json:"institution"`
	PlaceOfIssue   string     `json:"placeOfIssue"`
	DateAwarded    string     `json:"dateAwarded"`
	NLQFLevel      string     `json:"nlqfLevel,omitempty"`
	EQFLevel       string     `json:"eqfLevel,omitempty"`
	DocumentNumber string     `json:"documentNumber"`
	SignedAt       *time.Time `json:"signedAt,omitempty"`
	AddedAt        time.Time  `json:"addedAt"`
}

func newDiplomaResponse(d Diploma) diplomaResponse {
	return diplomaResponse{
		DocumentType: d.DocumentType, Qualification: d.Qualification, Profiles: d.Profiles,
		Institution: d.Institution, PlaceOfIssue: d.PlaceOfIssue, DateAwarded: d.DateAwarded.Format(time.DateOnly),
		NLQFLevel: d.NLQFLevel, EQFLevel: d.EQFLevel, DocumentNumber: d.DocumentNumber,
		SignedAt: d.SignedAt, AddedAt: d.CreatedAt,
	}
}

func newDiplomaResponses(diplomas []Diploma) []diplomaResponse {
	out := make([]diplomaResponse, 0, len(diplomas))
	for _, d := range diplomas {
		out = append(out, newDiplomaResponse(d))
	}
	return out
}

// diplomaModeOf is the request's mode, off for a row from before diplomas.
func diplomaModeOf(req Request) DiplomaMode {
	if req.Diplomas == "" {
		return DiplomasOff
	}
	return req.Diplomas
}

// diplomasUntil is when the subject can last add an extract: an hour after
// the approval of a request that asks for them, nil otherwise.
func diplomasUntil(req Request) *time.Time {
	if !req.Diplomas.asked() || req.Status != StatusApproved || req.CompletedAt == nil {
		return nil
	}
	until := req.CompletedAt.Add(DiplomaUploadWindow)
	return &until
}

// diplomaVerdictResponse is what became of one uploaded file.
type diplomaVerdictResponse struct {
	FileName    string           `json:"fileName"`
	Accepted    bool             `json:"accepted"`
	Diploma     *diplomaResponse `json:"diploma,omitempty"`
	Reason      string           `json:"reason,omitempty"`
	FailedCheck string           `json:"failedCheck,omitempty"`
}

func writeDiplomaVerdicts(w http.ResponseWriter, r *http.Request, verdicts []DiplomaVerdict) {
	out := make([]diplomaVerdictResponse, 0, len(verdicts))
	for _, v := range verdicts {
		resp := diplomaVerdictResponse{FileName: v.FileName, Reason: v.Reason, FailedCheck: v.FailedCheck}
		if v.Diploma != nil {
			d := newDiplomaResponse(*v.Diploma)
			resp.Accepted, resp.Diploma = true, &d
		}
		out = append(out, resp)
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// readDiplomaFiles reads the extracts of a multipart upload, each at most
// MaxDiplomaFileBytes.
func readDiplomaFiles(w http.ResponseWriter, r *http.Request) ([]DiplomaFile, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDiplomaUploadBytes)
	if err := r.ParseMultipartForm(diplomaMultipartMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: "the upload is too large"}
		}
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "upload the extracts as multipart/form-data"}
	}
	headers := r.MultipartForm.File[diplomaFormField]
	if len(headers) == 0 {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "no file uploaded"}
	}
	if len(headers) > MaxDiplomasPerRequest {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "too_many_files", Message: fmt.Sprintf("upload at most %d extracts", MaxDiplomasPerRequest)}
	}
	files := make([]DiplomaFile, 0, len(headers))
	for _, header := range headers {
		if header.Size > MaxDiplomaFileBytes {
			return nil, &respond.APIError{Status: http.StatusRequestEntityTooLarge, Code: "too_large", Message: fmt.Sprintf("an extract is at most %d bytes", MaxDiplomaFileBytes)}
		}
		b, err := readPart(header)
		if err != nil {
			return nil, err
		}
		files = append(files, DiplomaFile{Name: header.Filename, Bytes: b})
	}
	return files, nil
}

func readPart(header *multipart.FileHeader) ([]byte, error) {
	f, err := header.Open()
	if err != nil {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "unreadable file"}
	}
	defer func() { _ = f.Close() }() // read-only; a close error loses nothing
	b, err := io.ReadAll(io.LimitReader(f, MaxDiplomaFileBytes+1))
	if err != nil || len(b) > MaxDiplomaFileBytes {
		return nil, &respond.APIError{Status: http.StatusBadRequest, Code: "invalid_body", Message: "unreadable file"}
	}
	return b, nil
}

// addDiplomas checks the extracts uploaded on a request's on-screen page.
func (h *Handler) addDiplomas(w http.ResponseWriter, r *http.Request) error {
	id, requestedBy, err := sentRequestTarget(r)
	if err != nil {
		return err
	}
	files, err := readDiplomaFiles(w, r)
	if err != nil {
		return err
	}
	verdicts, err := h.service.AddDiplomas(r.Context(), orgFromRequest(r).ID, id, requestedBy, files)
	if err != nil {
		return mapError(err)
	}
	writeDiplomaVerdicts(w, r, verdicts)
	return nil
}

// hostedAddDiplomas checks the extracts a hosted link's subject uploaded. The
// link is checked before the body is read: the route is anonymous, and its
// rate limit is keyed on the token, so a fresh token per call would otherwise
// buy a full upload's worth of memory each time.
func (h *Handler) hostedAddDiplomas(w http.ResponseWriter, r *http.Request) error {
	if err := h.service.HostedDiplomasOpen(r.Context(), r.PathValue("token")); err != nil {
		return mapError(err)
	}
	files, err := readDiplomaFiles(w, r)
	if err != nil {
		return err
	}
	verdicts, err := h.service.HostedAddDiplomas(r.Context(), r.PathValue("token"), files)
	if err != nil {
		return mapError(err)
	}
	writeDiplomaVerdicts(w, r, verdicts)
	return nil
}
